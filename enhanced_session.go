package invmux

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// EnhancedConfig provides advanced configuration options
type EnhancedConfig struct {
	*Config // Embed the original config

	// Pluggable components
	ConnectionPool     ConnectionPool
	ConnectionBalancer ConnectionBalancer
	ErrorHandler       ErrorHandler
	HealthMonitor      HealthMonitor
	Middleware         []StreamMiddleware

	// Advanced features
	AutoHealing                bool
	AutoReconnect              bool
	ConnectionRedundancy       int // Minimum number of connections to maintain
	MaxReconnectAttempts       int
	ReconnectBackoffInitial    time.Duration
	ReconnectBackoffMultiplier float64
	ReconnectBackoffMax        time.Duration

	// Health monitoring
	HealthThresholds HealthThresholds

	// Connection priorities
	PreferReliableConnections bool
	ConnectionPriorityWeights map[string]int

	// Advanced distribution
	UseAdaptiveDistribution bool
	DistributionHistorySize int
}

// DefaultEnhancedConfig returns a sensible default enhanced configuration
func DefaultEnhancedConfig() *EnhancedConfig {
	return &EnhancedConfig{
		Config:                     DefaultConfig(),
		AutoHealing:                true,
		AutoReconnect:              true,
		ConnectionRedundancy:       2,
		MaxReconnectAttempts:       5,
		ReconnectBackoffInitial:    1 * time.Second,
		ReconnectBackoffMultiplier: 2.0,
		ReconnectBackoffMax:        30 * time.Second,
		PreferReliableConnections:  true,
		UseAdaptiveDistribution:    true,
		DistributionHistorySize:    1000,
		HealthThresholds: HealthThresholds{
			MaxLatency:          500 * time.Millisecond,
			MinBandwidth:        1024, // 1KB/s minimum
			MaxPacketLoss:       0.05, // 5% max packet loss
			MaxErrorRate:        0.02, // 2% max error rate
			MinHealthScore:      0.7,  // 70% minimum health score
			MaxIdleTime:         60 * time.Second,
			HealthCheckInterval: 10 * time.Second,
		},
		ConnectionPriorityWeights: make(map[string]int),
		Middleware:                make([]StreamMiddleware, 0),
	}
}

// EnhancedSession is the improved session with pluggable interfaces
type EnhancedSession struct {
	config *EnhancedConfig

	// Core components
	connectionPool ConnectionPool
	balancer       ConnectionBalancer
	errorHandler   ErrorHandler
	healthMonitor  HealthMonitor
	middleware     []StreamMiddleware

	// Connection management
	connections     map[string]PluggableConnection
	connectionOrder []string // For round-robin
	connectionMu    sync.RWMutex
	nextConnIndex   int64

	// Stream management
	streams      map[uint32]*EnhancedStream
	streamLock   sync.RWMutex
	nextStreamID uint32

	// Channels for managing operations
	acceptCh       chan *EnhancedStream
	acceptNotifyCh chan struct{}
	shutdownCh     chan struct{}
	shutdownOnce   sync.Once
	shutdownErr    error

	// Metrics and monitoring
	metrics *sessionMetrics

	// Context for lifecycle management
	ctx    context.Context
	cancel context.CancelFunc
}

// SessionMetrics tracks comprehensive session statistics
type SessionMetrics struct {
	// Connection metrics
	ConnectionsActive      int64
	ConnectionsTotal       int64
	ConnectionsFailed      int64
	ConnectionsReconnected int64

	// Stream metrics
	StreamsActive int64
	StreamsTotal  int64
	StreamsClosed int64

	// Data metrics
	BytesSent       int64
	BytesReceived   int64
	PacketsSent     int64
	PacketsReceived int64
	PacketsLost     int64

	// Error metrics
	ErrorsTotal      int64
	ErrorsConnection int64
	ErrorsStream     int64
	ErrorsTimeout    int64

	// Performance metrics
	AverageLatency   time.Duration
	AverageBandwidth int64
	HealthScore      float64
}

// sessionMetrics is the internal metrics struct with mutex
type sessionMetrics struct {
	SessionMetrics
	mu sync.RWMutex
}

// NewEnhancedSession creates a new enhanced session
func NewEnhancedSession(config *EnhancedConfig) *EnhancedSession {
	if config == nil {
		config = DefaultEnhancedConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	session := &EnhancedSession{
		config:          config,
		connections:     make(map[string]PluggableConnection),
		connectionOrder: make([]string, 0),
		streams:         make(map[uint32]*EnhancedStream),
		nextStreamID:    1,
		acceptCh:        make(chan *EnhancedStream, config.AcceptBacklog),
		acceptNotifyCh:  make(chan struct{}, 1),
		shutdownCh:      make(chan struct{}),
		metrics:         &sessionMetrics{},
		ctx:             ctx,
		cancel:          cancel,
	}

	// Initialize components with defaults if not provided
	if config.ConnectionPool == nil {
		config.ConnectionPool = NewDefaultConnectionPool()
	}

	if config.ConnectionBalancer == nil {
		config.ConnectionBalancer = NewRoundRobinBalancer()
	}

	if config.ErrorHandler == nil {
		config.ErrorHandler = NewDefaultErrorHandler()
	}

	if config.HealthMonitor == nil {
		config.HealthMonitor = NewDefaultHealthMonitor()
	}

	session.connectionPool = config.ConnectionPool
	session.balancer = config.ConnectionBalancer
	session.errorHandler = config.ErrorHandler
	session.healthMonitor = config.HealthMonitor
	session.middleware = append([]StreamMiddleware{}, config.Middleware...)

	// Start background services
	go session.backgroundServices()

	// Start health monitoring
	if session.healthMonitor != nil {
		session.healthMonitor.SetHealthThresholds(config.HealthThresholds)
		go session.healthMonitor.StartMonitoring(session)
	}

	// Start auto-healing if enabled
	if config.AutoHealing && session.connectionPool != nil {
		session.connectionPool.SetAutoHealing(true)
		go session.connectionPool.StartAutoHealing(ctx)
	}

	return session
}

// backgroundServices handles background maintenance tasks
func (s *EnhancedSession) backgroundServices() {
	keepAliveTicker := time.NewTicker(s.config.KeepAliveInterval)
	metricsTicker := time.NewTicker(5 * time.Second)
	defer keepAliveTicker.Stop()
	defer metricsTicker.Stop()

	for {
		select {
		case <-keepAliveTicker.C:
			s.sendKeepAlives()
		case <-metricsTicker.C:
			s.updateMetrics()
		case <-s.shutdownCh:
			return
		case <-s.ctx.Done():
			return
		}
	}
}

// AddConnection adds a pluggable connection to the session
func (s *EnhancedSession) AddConnection(conn PluggableConnection) error {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()

	connID := s.generateConnectionID(conn)
	s.connections[connID] = conn
	s.connectionOrder = append(s.connectionOrder, connID)

	// Notify balancer about new connection
	s.balancer.OnConnectionAdded(conn)

	// Start handling the connection
	go s.handleConnection(conn)

	// Update metrics
	atomic.AddInt64(&s.metrics.ConnectionsActive, 1)
	atomic.AddInt64(&s.metrics.ConnectionsTotal, 1)

	log.Printf("Added connection: %s (type: %s, priority: %d)",
		connID, conn.Metadata().Type, conn.Priority())

	return nil
}

// AddConnectionByAddress creates and adds a connection using the connection pool
func (s *EnhancedSession) AddConnectionByAddress(address string) error {
	conn, err := s.connectionPool.CreateConnection(s.ctx, address)
	if err != nil {
		return fmt.Errorf("failed to create connection to %s: %w", address, err)
	}

	return s.AddConnection(conn)
}

// generateConnectionID creates a unique ID for a connection
func (s *EnhancedSession) generateConnectionID(conn PluggableConnection) string {
	metadata := conn.Metadata()
	return fmt.Sprintf("%s-%s-%s-%d",
		metadata.Type, metadata.RemoteAddress, metadata.LocalAddress, time.Now().UnixNano())
}

// handleConnection processes data from a pluggable connection
func (s *EnhancedSession) handleConnection(conn PluggableConnection) {
	defer s.removeConnection(conn)

	connID := s.generateConnectionID(conn)
	buf := make([]byte, conn.MaxMTU())

	for {
		select {
		case <-s.shutdownCh:
			return
		case <-s.ctx.Done():
			return
		default:
		}

		// Set read timeout
		conn.SetReadDeadline(time.Now().Add(s.config.ConnectionWriteTimeout))

		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				// Handle the error through the error handler
				action := s.errorHandler.HandleConnectionError(conn, err)
				switch action {
				case ErrorActionIgnore:
					continue
				case ErrorActionRetry:
					time.Sleep(100 * time.Millisecond)
					continue
				case ErrorActionReconnect:
					// TODO: Implement reconnection logic
					return
				case ErrorActionRemoveConnection:
					return
				case ErrorActionShutdown:
					s.shutdown(err)
					return
				}
			}
			return
		}

		// Update connection activity
		quality := conn.Quality()
		quality.LastActivity = time.Now()

		// Update metrics
		atomic.AddInt64(&s.metrics.BytesReceived, int64(n))
		atomic.AddInt64(&s.metrics.PacketsReceived, 1)

		// Process the received data
		if err := s.processReceivedData(connID, buf[:n]); err != nil {
			log.Printf("Error processing received data from %s: %v", connID, err)
		}
	}
}

// processReceivedData handles incoming data packets
func (s *EnhancedSession) processReceivedData(connID string, data []byte) error {
	// Parse packet header (simplified - in real implementation this would be more robust)
	if len(data) < 8 {
		return fmt.Errorf("packet too short: %d bytes", len(data))
	}

	// Extract header information
	size := uint16(data[0])<<8 | uint16(data[1])
	streamID := uint32(data[2])<<24 | uint32(data[3])<<16 | uint32(data[4])<<8 | uint32(data[5])
	chunkID := uint16(data[6])<<8 | uint16(data[7])

	if int(size) > len(data)-8 {
		return fmt.Errorf("invalid packet size: %d > %d", size, len(data)-8)
	}

	payload := data[8 : 8+size]

	// Find or create the stream
	stream := s.getOrCreateStream(streamID)
	if stream == nil {
		return fmt.Errorf("failed to get or create stream %d", streamID)
	}

	// Process payload through middleware
	processedPayload := payload
	for _, middleware := range s.middleware {
		var err error
		processedPayload, err = middleware.ProcessRead(streamID, processedPayload)
		if err != nil {
			return fmt.Errorf("middleware %s failed: %w", middleware.Name(), err)
		}
	}

	// Add chunk to stream
	stream.receiveChunk(chunkID, processedPayload)

	return nil
}

// getOrCreateStream finds an existing stream or creates a new one
func (s *EnhancedSession) getOrCreateStream(streamID uint32) *EnhancedStream {
	s.streamLock.RLock()
	stream, exists := s.streams[streamID]
	s.streamLock.RUnlock()

	if exists {
		return stream
	}

	// Check if this is a remote-initiated stream
	if streamID%2 == 0 {
		// Create new stream for remote side
		stream = newEnhancedStream(s, streamID)

		s.streamLock.Lock()
		s.streams[streamID] = stream
		s.streamLock.Unlock()

		// Notify middleware about new stream
		for _, middleware := range s.middleware {
			if err := middleware.OnStreamOpen(streamID); err != nil {
				log.Printf("Middleware %s failed on stream open: %v", middleware.Name(), err)
			}
		}

		// Queue for acceptance
		select {
		case s.acceptCh <- stream:
		default:
			select {
			case s.acceptNotifyCh <- struct{}{}:
			default:
			}
		}

		atomic.AddInt64(&s.metrics.StreamsActive, 1)
		atomic.AddInt64(&s.metrics.StreamsTotal, 1)

		return stream
	}

	return nil
}

// OpenStream creates a new outbound stream
func (s *EnhancedSession) OpenStream() (*EnhancedStream, error) {
	s.streamLock.Lock()
	streamID := s.nextStreamID
	s.nextStreamID += 2 // Odd numbers for client-initiated streams
	s.streamLock.Unlock()

	stream := newEnhancedStream(s, streamID)

	s.streamLock.Lock()
	s.streams[streamID] = stream
	s.streamLock.Unlock()

	// Notify middleware about new stream
	for _, middleware := range s.middleware {
		if err := middleware.OnStreamOpen(streamID); err != nil {
			return nil, fmt.Errorf("middleware %s failed on stream open: %w", middleware.Name(), err)
		}
	}

	atomic.AddInt64(&s.metrics.StreamsActive, 1)
	atomic.AddInt64(&s.metrics.StreamsTotal, 1)

	return stream, nil
}

// AcceptStream waits for and returns an incoming stream
func (s *EnhancedSession) AcceptStream() (*EnhancedStream, error) {
	select {
	case stream := <-s.acceptCh:
		return stream, nil
	case <-s.shutdownCh:
		return nil, ErrConnectionClosed
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

// SendChunk sends a data chunk through the best available connection
func (s *EnhancedSession) SendChunk(streamID uint32, chunkID uint16, data []byte) error {
	// Process data through middleware
	processedData := data
	for _, middleware := range s.middleware {
		var err error
		processedData, err = middleware.ProcessWrite(streamID, processedData)
		if err != nil {
			return fmt.Errorf("middleware %s failed: %w", middleware.Name(), err)
		}
	}

	// Get healthy connections
	s.connectionMu.RLock()
	var connections []PluggableConnection
	for _, conn := range s.connections {
		if conn.Quality().IsHealthy {
			connections = append(connections, conn)
		}
	}
	s.connectionMu.RUnlock()

	if len(connections) == 0 {
		return ErrNoPhysicalConns
	}

	// Select connection using balancer
	selectedConn, err := s.balancer.SelectConnection(connections, processedData)
	if err != nil {
		return fmt.Errorf("balancer failed to select connection: %w", err)
	}

	// Create packet
	packet := make([]byte, 8+len(processedData))
	packet[0] = byte(len(processedData) >> 8)
	packet[1] = byte(len(processedData))
	packet[2] = byte(streamID >> 24)
	packet[3] = byte(streamID >> 16)
	packet[4] = byte(streamID >> 8)
	packet[5] = byte(streamID)
	packet[6] = byte(chunkID >> 8)
	packet[7] = byte(chunkID)
	copy(packet[8:], processedData)

	// Send packet
	selectedConn.SetWriteDeadline(time.Now().Add(s.config.ConnectionWriteTimeout))
	_, err = selectedConn.Write(packet)
	if err != nil {
		action := s.errorHandler.HandleConnectionError(selectedConn, err)
		if action == ErrorActionRemoveConnection {
			s.removeConnection(selectedConn)
		}
		return err
	}

	// Update metrics
	atomic.AddInt64(&s.metrics.BytesSent, int64(len(packet)))
	atomic.AddInt64(&s.metrics.PacketsSent, 1)

	return nil
}

// removeConnection removes a connection from the session
func (s *EnhancedSession) removeConnection(conn PluggableConnection) {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()

	connID := s.generateConnectionID(conn)
	delete(s.connections, connID)

	// Remove from order slice
	for i, id := range s.connectionOrder {
		if id == connID {
			s.connectionOrder = append(s.connectionOrder[:i], s.connectionOrder[i+1:]...)
			break
		}
	}

	// Notify balancer
	s.balancer.OnConnectionRemoved(conn)

	// Close connection
	conn.Close()

	// Update metrics
	atomic.AddInt64(&s.metrics.ConnectionsActive, -1)

	log.Printf("Removed connection: %s", connID)
}

// sendKeepAlives sends keep-alive packets to all connections
func (s *EnhancedSession) sendKeepAlives() {
	s.connectionMu.RLock()
	defer s.connectionMu.RUnlock()

	for _, conn := range s.connections {
		// Send keep-alive packet (simplified)
		keepAlive := []byte{0xFF, 0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
		conn.SetWriteDeadline(time.Now().Add(s.config.ConnectionWriteTimeout))
		conn.Write(keepAlive)
	}
}

// updateMetrics calculates and updates session metrics
func (s *EnhancedSession) updateMetrics() {
	s.connectionMu.RLock()
	totalLatency := time.Duration(0)
	totalBandwidth := int64(0)
	healthyConnections := 0
	totalHealthScore := 0.0

	for _, conn := range s.connections {
		quality := conn.Quality()
		if quality.IsHealthy {
			healthyConnections++
			totalLatency += quality.Latency
			totalBandwidth += quality.UploadBandwidth + quality.DownloadBandwidth
			totalHealthScore += quality.HealthScore
		}
	}
	s.connectionMu.RUnlock()

	s.metrics.mu.Lock()
	if healthyConnections > 0 {
		s.metrics.AverageLatency = totalLatency / time.Duration(healthyConnections)
		s.metrics.AverageBandwidth = totalBandwidth / int64(healthyConnections)
		s.metrics.HealthScore = totalHealthScore / float64(healthyConnections)
	}
	s.metrics.mu.Unlock()
}

// GetMetrics returns current session metrics
func (s *EnhancedSession) GetMetrics() SessionMetrics {
	s.metrics.mu.RLock()
	defer s.metrics.mu.RUnlock()

	// Return a copy of the embedded SessionMetrics
	return s.metrics.SessionMetrics
}

// shutdown gracefully shuts down the session
func (s *EnhancedSession) shutdown(err error) {
	s.shutdownOnce.Do(func() {
		s.shutdownErr = err
		close(s.shutdownCh)
		s.cancel()

		// Close all connections
		s.connectionMu.Lock()
		for _, conn := range s.connections {
			conn.Close()
		}
		s.connectionMu.Unlock()

		// Close all streams
		s.streamLock.Lock()
		for _, stream := range s.streams {
			stream.Close()
		}
		s.streamLock.Unlock()

		// Stop health monitoring
		if s.healthMonitor != nil {
			s.healthMonitor.StopMonitoring()
		}
	})
}

// Close gracefully closes the session
func (s *EnhancedSession) Close() error {
	s.shutdown(nil)
	return s.shutdownErr
}

// Context returns the session's context
func (s *EnhancedSession) Context() context.Context {
	return s.ctx
}

// NumConnections returns the number of active connections
func (s *EnhancedSession) NumConnections() int {
	s.connectionMu.RLock()
	defer s.connectionMu.RUnlock()
	return len(s.connections)
}

// NumStreams returns the number of active streams
func (s *EnhancedSession) NumStreams() int {
	s.streamLock.RLock()
	defer s.streamLock.RUnlock()
	return len(s.streams)
}

// GetConnections returns all active connections
func (s *EnhancedSession) GetConnections() []PluggableConnection {
	s.connectionMu.RLock()
	defer s.connectionMu.RUnlock()

	connections := make([]PluggableConnection, 0, len(s.connections))
	for _, conn := range s.connections {
		connections = append(connections, conn)
	}

	return connections
}
