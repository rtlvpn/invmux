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
	"sync/atomic"
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

// RoundRobinBalancer implements round-robin load balancing
type RoundRobinBalancer struct {
	name    string
	counter int64
}

// NewRoundRobinBalancer creates a new round-robin load balancer
func NewRoundRobinBalancer() *RoundRobinBalancer {
	return &RoundRobinBalancer{
		name: "round-robin",
	}
}

func (b *RoundRobinBalancer) Name() string {
	return b.name
}

func (b *RoundRobinBalancer) Select(connections []Connection, streamID uint32, data []byte) (Connection, error) {
	if len(connections) == 0 {
		return nil, fmt.Errorf("no connections available")
	}
	
	index := atomic.AddInt64(&b.counter, 1) % int64(len(connections))
	return connections[index], nil
}

func (b *RoundRobinBalancer) OnConnectionAdded(conn Connection) {
	// No specific action needed for round-robin
}

func (b *RoundRobinBalancer) OnConnectionRemoved(conn Connection) {
	// No specific action needed for round-robin
}

func (b *RoundRobinBalancer) UpdateStats(conn Connection, quality *Quality) {
	// Round-robin doesn't use stats
}

// WeightedBalancer implements priority-based load balancing
type WeightedBalancer struct {
	name        string
	weights     map[string]int
	totalWeight int64
	mu          sync.RWMutex
}

// NewWeightedBalancer creates a new weighted load balancer
func NewWeightedBalancer() *WeightedBalancer {
	return &WeightedBalancer{
		name:    "weighted",
		weights: make(map[string]int),
	}
}

func (b *WeightedBalancer) Name() string {
	return b.name
}

func (b *WeightedBalancer) Select(connections []Connection, streamID uint32, data []byte) (Connection, error) {
	if len(connections) == 0 {
		return nil, fmt.Errorf("no connections available")
	}
	
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	// Simple priority selection - choose highest priority connection
	var best Connection
	var bestPriority int = -1
	
	for _, conn := range connections {
		if conn.Priority() > bestPriority && conn.Quality().IsHealthy {
			best = conn
			bestPriority = conn.Priority()
		}
	}
	
	if best == nil {
		// Fallback to first available connection
		return connections[0], nil
	}
	
	return best, nil
}

func (b *WeightedBalancer) OnConnectionAdded(conn Connection) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.weights[conn.ID()] = conn.Priority()
	b.recalculateWeight()
}

func (b *WeightedBalancer) OnConnectionRemoved(conn Connection) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.weights, conn.ID())
	b.recalculateWeight()
}

func (b *WeightedBalancer) UpdateStats(conn Connection, quality *Quality) {
	// Can be used to adjust weights based on performance
}

func (b *WeightedBalancer) recalculateWeight() {
	total := int64(0)
	for _, weight := range b.weights {
		total += int64(weight)
	}
	b.totalWeight = total
}

// RandomBalancer implements random load balancing
type RandomBalancer struct {
	name string
	rand *rand.Rand
	mu   sync.Mutex
}

// NewRandomBalancer creates a new random load balancer
func NewRandomBalancer() *RandomBalancer {
	return &RandomBalancer{
		name: "random",
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (b *RandomBalancer) Name() string {
	return b.name
}

func (b *RandomBalancer) Select(connections []Connection, streamID uint32, data []byte) (Connection, error) {
	if len(connections) == 0 {
		return nil, fmt.Errorf("no connections available")
	}
	
	b.mu.Lock()
	index := b.rand.Intn(len(connections))
	b.mu.Unlock()
	
	return connections[index], nil
}

func (b *RandomBalancer) OnConnectionAdded(conn Connection) {}
func (b *RandomBalancer) OnConnectionRemoved(conn Connection) {}
func (b *RandomBalancer) UpdateStats(conn Connection, quality *Quality) {}

// DefaultErrorHandler implements basic error handling
type DefaultErrorHandler struct {
	name string
}

// NewDefaultErrorHandler creates a new default error handler
func NewDefaultErrorHandler() *DefaultErrorHandler {
	return &DefaultErrorHandler{
		name: "default",
	}
}

func (h *DefaultErrorHandler) HandleConnectionError(conn Connection, err error) ErrorAction {
	// For connection errors, try to reconnect
	return ErrorActionReconnect
}

func (h *DefaultErrorHandler) HandleStreamError(streamID uint32, err error) ErrorAction {
	// For stream errors, retry first
	return ErrorActionRetry
}

func (h *DefaultErrorHandler) HandleSessionError(err error) ErrorAction {
	// For session errors, try to failover
	return ErrorActionFailover
}

// DefaultHealthMonitor implements basic health monitoring
type DefaultHealthMonitor struct {
	name       string
	thresholds *HealthThresholds
	mu         sync.RWMutex
	running    bool
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewDefaultHealthMonitor creates a new default health monitor
func NewDefaultHealthMonitor() *DefaultHealthMonitor {
	return &DefaultHealthMonitor{
		name: "default",
		thresholds: &HealthThresholds{
			MaxLatency:    500 * time.Millisecond,
			MinBandwidth:  1024, // 1KB/s
			MaxPacketLoss: 0.05, // 5%
			MaxErrorRate:  0.02, // 2%
			MinScore:      0.7,  // 70%
			MaxIdleTime:   60 * time.Second,
			CheckInterval: 10 * time.Second,
		},
	}
}

func (m *DefaultHealthMonitor) StartMonitoring(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if m.running {
		return fmt.Errorf("monitoring already running")
	}
	
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.running = true
	
	// Start monitoring in background
	go m.monitorLoop()
	
	return nil
}

func (m *DefaultHealthMonitor) StopMonitoring() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if !m.running {
		return nil
	}
	
	m.cancel()
	m.running = false
	return nil
}

func (m *DefaultHealthMonitor) CheckConnection(conn Connection) *Quality {
	quality := conn.Quality()
	
	// Update health based on thresholds
	healthy := true
	score := 1.0
	
	if quality.Latency > m.thresholds.MaxLatency {
		healthy = false
		score -= 0.3
	}
	
	if quality.Bandwidth < m.thresholds.MinBandwidth {
		healthy = false
		score -= 0.2
	}
	
	if quality.PacketLoss > m.thresholds.MaxPacketLoss {
		healthy = false
		score -= 0.3
	}
	
	if time.Since(quality.LastActivity) > m.thresholds.MaxIdleTime {
		healthy = false
		score -= 0.2
	}
	
	quality.IsHealthy = healthy
	quality.Score = score
	
	return quality
}

func (m *DefaultHealthMonitor) SetThresholds(thresholds *HealthThresholds) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.thresholds = thresholds
}

func (m *DefaultHealthMonitor) OnUnhealthyConnection(conn Connection) {
	// Default action: log unhealthy connection
	fmt.Printf("Connection %s is unhealthy\n", conn.ID())
}

func (m *DefaultHealthMonitor) monitorLoop() {
	ticker := time.NewTicker(m.thresholds.CheckInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			// Health checking would happen here in a real implementation
			// This is just a placeholder
		case <-m.ctx.Done():
			return
		}
	}
}

// PassthroughMiddleware is a no-op middleware that passes data through unchanged
type PassthroughMiddleware struct {
	name string
}

// NewPassthroughMiddleware creates a new passthrough middleware
func NewPassthroughMiddleware() *PassthroughMiddleware {
	return &PassthroughMiddleware{
		name: "passthrough",
	}
}

func (m *PassthroughMiddleware) Name() string {
	return m.name
}

func (m *PassthroughMiddleware) ProcessOutbound(streamID uint32, data []byte) ([]byte, error) {
	return data, nil
}

func (m *PassthroughMiddleware) ProcessInbound(streamID uint32, data []byte) ([]byte, error) {
	return data, nil
}

func (m *PassthroughMiddleware) OnStreamOpen(streamID uint32) error {
	return nil
}

func (m *PassthroughMiddleware) OnStreamClose(streamID uint32) error {
	return nil
}

// CompressionMiddleware provides simple compression (placeholder implementation)
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

func (m *CompressionMiddleware) ProcessOutbound(streamID uint32, data []byte) ([]byte, error) {
	// Placeholder: In real implementation, compress data here
	return data, nil
}

func (m *CompressionMiddleware) ProcessInbound(streamID uint32, data []byte) ([]byte, error) {
	// Placeholder: In real implementation, decompress data here
	return data, nil
}

func (m *CompressionMiddleware) OnStreamOpen(streamID uint32) error {
	return nil
}

func (m *CompressionMiddleware) OnStreamClose(streamID uint32) error {
	return nil
}

// DefaultConnectionManager implements basic connection management
type DefaultConnectionManager struct {
	connections   map[string]Connection
	autoReconnect bool
	mu            sync.RWMutex
}

// NewDefaultConnectionManager creates a new default connection manager
func NewDefaultConnectionManager() *DefaultConnectionManager {
	return &DefaultConnectionManager{
		connections:   make(map[string]Connection),
		autoReconnect: true,
	}
}

func (m *DefaultConnectionManager) AddConnection(conn Connection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connections[conn.ID()] = conn
	return nil
}

func (m *DefaultConnectionManager) RemoveConnection(connID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.connections, connID)
	return nil
}

func (m *DefaultConnectionManager) GetConnection(connID string) (Connection, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	conn, ok := m.connections[connID]
	return conn, ok
}

func (m *DefaultConnectionManager) GetHealthyConnections() []Connection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	var healthy []Connection
	for _, conn := range m.connections {
		if conn.Quality().IsHealthy {
			healthy = append(healthy, conn)
		}
	}
	return healthy
}

func (m *DefaultConnectionManager) GetAllConnections() []Connection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	connections := make([]Connection, 0, len(m.connections))
	for _, conn := range m.connections {
		connections = append(connections, conn)
	}
	return connections
}

func (m *DefaultConnectionManager) SetAutoReconnect(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.autoReconnect = enabled
}
