package invmux

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"
)

// DefaultConnectionPool provides a basic connection pool implementation
type DefaultConnectionPool struct {
	providers map[string]ConnectionProvider
	mu        sync.RWMutex
	autoHeal  bool
}

// NewDefaultConnectionPool creates a new default connection pool
func NewDefaultConnectionPool() *DefaultConnectionPool {
	pool := &DefaultConnectionPool{
		providers: make(map[string]ConnectionProvider),
	}

	// Register default providers
	pool.AddProvider(NewTCPProvider(10 * time.Second))
	pool.AddProvider(NewUDPProvider(5 * time.Second))
	pool.AddProvider(NewDNSTunnelProvider("8.8.8.8:53", "tunnel.example.com", 30*time.Second))

	return pool
}

func (p *DefaultConnectionPool) AddProvider(provider ConnectionProvider) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.providers[provider.Name()] = provider
	return nil
}

func (p *DefaultConnectionPool) RemoveProvider(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.providers, name)
	return nil
}

func (p *DefaultConnectionPool) CreateConnection(ctx context.Context, address string) (PluggableConnection, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Find a provider that supports this address
	var supportingProviders []ConnectionProvider
	for _, provider := range p.providers {
		if provider.SupportsAddress(address) {
			supportingProviders = append(supportingProviders, provider)
		}
	}

	if len(supportingProviders) == 0 {
		return nil, fmt.Errorf("no provider supports address: %s", address)
	}

	// Try providers in order (could be improved with better selection logic)
	var lastErr error
	for _, provider := range supportingProviders {
		conn, err := provider.Dial(ctx, address)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
}

func (p *DefaultConnectionPool) GetHealthyConnections() []PluggableConnection {
	// This would typically maintain a cache of active connections
	// For this default implementation, we return an empty slice
	return []PluggableConnection{}
}

func (p *DefaultConnectionPool) GetConnectionsByType(connType string) []PluggableConnection {
	// This would typically filter connections by type
	// For this default implementation, we return an empty slice
	return []PluggableConnection{}
}

func (p *DefaultConnectionPool) SetAutoHealing(enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.autoHeal = enabled
}

func (p *DefaultConnectionPool) StartAutoHealing(ctx context.Context) error {
	// Simple auto-healing implementation
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if p.autoHeal {
				p.performHealthChecks(ctx)
			}
		}
	}
}

func (p *DefaultConnectionPool) performHealthChecks(ctx context.Context) {
	p.mu.RLock()
	providers := make([]ConnectionProvider, 0, len(p.providers))
	for _, provider := range p.providers {
		providers = append(providers, provider)
	}
	p.mu.RUnlock()

	for _, provider := range providers {
		go func(p ConnectionProvider) {
			if err := p.HealthCheck(ctx); err != nil {
				log.Printf("Provider %s health check failed: %v", p.Name(), err)
			}
		}(provider)
	}
}

// RoundRobinBalancer implements round-robin connection selection
type RoundRobinBalancer struct {
	name  string
	index int64
	mu    sync.Mutex
}

// NewRoundRobinBalancer creates a new round-robin balancer
func NewRoundRobinBalancer() *RoundRobinBalancer {
	return &RoundRobinBalancer{
		name: "round-robin",
	}
}

func (b *RoundRobinBalancer) Name() string {
	return b.name
}

func (b *RoundRobinBalancer) SelectConnection(connections []PluggableConnection, data []byte) (PluggableConnection, error) {
	if len(connections) == 0 {
		return nil, errors.New("no connections available")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Simple round-robin
	selectedIndex := b.index % int64(len(connections))
	b.index++

	return connections[selectedIndex], nil
}

func (b *RoundRobinBalancer) UpdateStats(connID string, quality ConnectionQuality) {
	// Round-robin doesn't use stats
}

func (b *RoundRobinBalancer) OnConnectionAdded(conn PluggableConnection) {
	// Nothing to do for round-robin
}

func (b *RoundRobinBalancer) OnConnectionRemoved(conn PluggableConnection) {
	// Nothing to do for round-robin
}

// LatencyBasedBalancer selects connections based on latency
type LatencyBasedBalancer struct {
	name  string
	stats map[string]ConnectionQuality
	mu    sync.RWMutex
}

// NewLatencyBasedBalancer creates a new latency-based balancer
func NewLatencyBasedBalancer() *LatencyBasedBalancer {
	return &LatencyBasedBalancer{
		name:  "latency-based",
		stats: make(map[string]ConnectionQuality),
	}
}

func (b *LatencyBasedBalancer) Name() string {
	return b.name
}

func (b *LatencyBasedBalancer) SelectConnection(connections []PluggableConnection, data []byte) (PluggableConnection, error) {
	if len(connections) == 0 {
		return nil, errors.New("no connections available")
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	// Find connection with lowest latency
	var bestConn PluggableConnection
	var bestLatency time.Duration = time.Hour // Start with a very high value

	for _, conn := range connections {
		quality := conn.Quality()
		if quality.Latency < bestLatency && quality.IsHealthy {
			bestLatency = quality.Latency
			bestConn = conn
		}
	}

	if bestConn == nil {
		// Fall back to first healthy connection
		for _, conn := range connections {
			if conn.Quality().IsHealthy {
				return conn, nil
			}
		}
		// If no healthy connections, use the first one
		return connections[0], nil
	}

	return bestConn, nil
}

func (b *LatencyBasedBalancer) UpdateStats(connID string, quality ConnectionQuality) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stats[connID] = quality
}

func (b *LatencyBasedBalancer) OnConnectionAdded(conn PluggableConnection) {
	// Initialize stats
	b.UpdateStats(conn.Metadata().RemoteAddress, conn.Quality())
}

func (b *LatencyBasedBalancer) OnConnectionRemoved(conn PluggableConnection) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.stats, conn.Metadata().RemoteAddress)
}

// WeightedBalancer implements weighted connection selection
type WeightedBalancer struct {
	name    string
	weights map[string]int
	mu      sync.RWMutex
}

// NewWeightedBalancer creates a new weighted balancer
func NewWeightedBalancer() *WeightedBalancer {
	return &WeightedBalancer{
		name:    "weighted",
		weights: make(map[string]int),
	}
}

func (b *WeightedBalancer) Name() string {
	return b.name
}

func (b *WeightedBalancer) SelectConnection(connections []PluggableConnection, data []byte) (PluggableConnection, error) {
	if len(connections) == 0 {
		return nil, errors.New("no connections available")
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	// Calculate total weight
	totalWeight := 0
	for _, conn := range connections {
		weight := b.weights[conn.Metadata().RemoteAddress]
		if weight <= 0 {
			weight = 1 // Default weight
		}
		totalWeight += weight
	}

	if totalWeight == 0 {
		// Fall back to first connection
		return connections[0], nil
	}

	// Select based on weighted random
	target := rand.Intn(totalWeight)
	current := 0

	for _, conn := range connections {
		weight := b.weights[conn.Metadata().RemoteAddress]
		if weight <= 0 {
			weight = 1
		}
		current += weight
		if current > target {
			return conn, nil
		}
	}

	// Should never reach here, but fall back to first connection
	return connections[0], nil
}

func (b *WeightedBalancer) SetWeight(connID string, weight int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.weights[connID] = weight
}

func (b *WeightedBalancer) UpdateStats(connID string, quality ConnectionQuality) {
	// Could use quality metrics to auto-adjust weights
}

func (b *WeightedBalancer) OnConnectionAdded(conn PluggableConnection) {
	// Set default weight if not specified
	connID := conn.Metadata().RemoteAddress
	b.mu.Lock()
	if _, exists := b.weights[connID]; !exists {
		b.weights[connID] = 1
	}
	b.mu.Unlock()
}

func (b *WeightedBalancer) OnConnectionRemoved(conn PluggableConnection) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.weights, conn.Metadata().RemoteAddress)
}

// DefaultErrorHandler provides basic error handling
type DefaultErrorHandler struct{}

// NewDefaultErrorHandler creates a new default error handler
func NewDefaultErrorHandler() *DefaultErrorHandler {
	return &DefaultErrorHandler{}
}

func (h *DefaultErrorHandler) HandleConnectionError(conn PluggableConnection, err error) ErrorAction {
	if err == nil {
		return ErrorActionIgnore
	}

	// Check error type and decide action
	if netErr, ok := err.(net.Error); ok {
		if netErr.Timeout() {
			return ErrorActionRetry
		}
		if netErr.Temporary() {
			return ErrorActionRetry
		}
	}

	// Check for common network errors
	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no route to host") ||
		strings.Contains(errStr, "network unreachable") {
		return ErrorActionReconnect
	}

	if strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") {
		return ErrorActionRemoveConnection
	}

	// Default to retry for unknown errors
	return ErrorActionRetry
}

func (h *DefaultErrorHandler) HandleSessionError(session *EnhancedSession, err error) ErrorAction {
	if err == nil {
		return ErrorActionIgnore
	}

	// For session-level errors, usually we want to continue
	return ErrorActionIgnore
}

func (h *DefaultErrorHandler) HandleStreamError(stream *EnhancedStream, err error) ErrorAction {
	if err == nil {
		return ErrorActionIgnore
	}

	// For stream errors, usually ignore and let the stream handle it
	return ErrorActionIgnore
}

// DefaultHealthMonitor provides basic health monitoring
type DefaultHealthMonitor struct {
	thresholds HealthThresholds
	running    bool
	stopCh     chan struct{}
	mu         sync.Mutex
}

// NewDefaultHealthMonitor creates a new default health monitor
func NewDefaultHealthMonitor() *DefaultHealthMonitor {
	return &DefaultHealthMonitor{
		stopCh: make(chan struct{}),
	}
}

func (m *DefaultHealthMonitor) StartMonitoring(session *EnhancedSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return errors.New("health monitoring already running")
	}

	m.running = true
	go m.monitorLoop(session)
	return nil
}

func (m *DefaultHealthMonitor) StopMonitoring() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	m.running = false
	close(m.stopCh)
	m.stopCh = make(chan struct{}) // Reset for potential restart
	return nil
}

func (m *DefaultHealthMonitor) CheckConnection(conn PluggableConnection) ConnectionQuality {
	quality := conn.Quality()

	// Update health status based on thresholds
	quality.IsHealthy = true

	if quality.Latency > m.thresholds.MaxLatency {
		quality.IsHealthy = false
		quality.HealthScore *= 0.5 // Reduce health score
	}

	if quality.UploadBandwidth+quality.DownloadBandwidth < m.thresholds.MinBandwidth {
		quality.IsHealthy = false
		quality.HealthScore *= 0.7
	}

	if quality.PacketLoss > m.thresholds.MaxPacketLoss {
		quality.IsHealthy = false
		quality.HealthScore *= 0.6
	}

	if quality.ErrorRate > m.thresholds.MaxErrorRate {
		quality.IsHealthy = false
		quality.HealthScore *= 0.4
	}

	if time.Since(quality.LastActivity) > m.thresholds.MaxIdleTime {
		quality.IsHealthy = false
		quality.HealthScore *= 0.3
	}

	if quality.HealthScore < m.thresholds.MinHealthScore {
		quality.IsHealthy = false
	}

	return quality
}

func (m *DefaultHealthMonitor) SetHealthThresholds(thresholds HealthThresholds) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.thresholds = thresholds
}

func (m *DefaultHealthMonitor) monitorLoop(session *EnhancedSession) {
	ticker := time.NewTicker(m.thresholds.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.performHealthChecks(session)
		case <-m.stopCh:
			return
		case <-session.ctx.Done():
			return
		}
	}
}

func (m *DefaultHealthMonitor) performHealthChecks(session *EnhancedSession) {
	connections := session.GetConnections()

	for _, conn := range connections {
		quality := m.CheckConnection(conn)

		// Update balancer with new stats
		if session.balancer != nil {
			session.balancer.UpdateStats(conn.Metadata().RemoteAddress, quality)
		}

		// Log unhealthy connections
		if !quality.IsHealthy {
			log.Printf("Connection %s is unhealthy (score: %.2f)",
				conn.Metadata().RemoteAddress, quality.HealthScore)
		}
	}
}

// CompressionMiddleware compresses/decompresses data
type CompressionMiddleware struct {
	name string
}

// NewCompressionMiddleware creates a new compression middleware
func NewCompressionMiddleware() *CompressionMiddleware {
	return &CompressionMiddleware{
		name: "compression",
	}
}

func (m *CompressionMiddleware) Name() string {
	return m.name
}

func (m *CompressionMiddleware) ProcessWrite(streamID uint32, data []byte) ([]byte, error) {
	// Simple compression simulation (in real implementation, use gzip/lz4/etc.)
	if len(data) < 100 {
		return data, nil // Don't compress small data
	}

	// Simulate compression by adding a header
	compressed := make([]byte, len(data)+4)
	compressed[0] = 0xC0 // Compression marker
	compressed[1] = 0x01 // Compression type
	compressed[2] = byte(len(data) >> 8)
	compressed[3] = byte(len(data))
	copy(compressed[4:], data)

	return compressed, nil
}

func (m *CompressionMiddleware) ProcessRead(streamID uint32, data []byte) ([]byte, error) {
	// Check if data is compressed
	if len(data) < 4 || data[0] != 0xC0 {
		return data, nil // Not compressed
	}

	// Extract original data
	originalSize := int(data[2])<<8 | int(data[3])
	if len(data) < 4+originalSize {
		return nil, errors.New("invalid compressed data")
	}

	return data[4 : 4+originalSize], nil
}

func (m *CompressionMiddleware) OnStreamOpen(streamID uint32) error {
	return nil
}

func (m *CompressionMiddleware) OnStreamClose(streamID uint32) error {
	return nil
}

// EncryptionMiddleware provides simple encryption
type EncryptionMiddleware struct {
	name string
	key  []byte
}

// NewEncryptionMiddleware creates a new encryption middleware
func NewEncryptionMiddleware(key []byte) *EncryptionMiddleware {
	return &EncryptionMiddleware{
		name: "encryption",
		key:  key,
	}
}

func (m *EncryptionMiddleware) Name() string {
	return m.name
}

func (m *EncryptionMiddleware) ProcessWrite(streamID uint32, data []byte) ([]byte, error) {
	// Simple XOR encryption (in real implementation, use AES/ChaCha20/etc.)
	encrypted := make([]byte, len(data))
	for i, b := range data {
		encrypted[i] = b ^ m.key[i%len(m.key)]
	}
	return encrypted, nil
}

func (m *EncryptionMiddleware) ProcessRead(streamID uint32, data []byte) ([]byte, error) {
	// XOR decryption (same as encryption for XOR)
	decrypted := make([]byte, len(data))
	for i, b := range data {
		decrypted[i] = b ^ m.key[i%len(m.key)]
	}
	return decrypted, nil
}

func (m *EncryptionMiddleware) OnStreamOpen(streamID uint32) error {
	return nil
}

func (m *EncryptionMiddleware) OnStreamClose(streamID uint32) error {
	return nil
}
