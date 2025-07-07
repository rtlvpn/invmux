// Package invmux implements an inverse multiplexer over multiple connections.
package invmux

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Protocol constants
const (
	Version           = 1
	DefaultWindowSize = 4096
	MaxChunkSize      = 16384
	DefaultBufferSize = 65536

	// Packet types
	DataPacket    = 0x00
	ControlPacket = 0xFF

	// Control packet subtypes
	PingPacket      = 0x01
	PongPacket      = 0x02
	KeepAlivePacket = 0x03
)

// Common errors
var (
	ErrConnectionClosed = fmt.Errorf("connection closed")
	ErrNoConnections    = fmt.Errorf("no connections available")
	ErrSessionClosed    = fmt.Errorf("session closed")
	ErrInvalidData      = fmt.Errorf("invalid data")
)

// Config provides session configuration
type Config struct {
	// Basic settings
	WindowSize      uint16
	MaxChunkSize    uint16
	KeepAlive       time.Duration
	WriteTimeout    time.Duration
	MaxStreams      uint32
	AcceptBacklog   int

	// Components
	LoadBalancer      LoadBalancer
	ErrorHandler      ErrorHandler
	HealthMonitor     HealthMonitor
	ConnectionManager ConnectionManager
	Middleware        []Middleware
}

// DefaultConfig returns a sensible default configuration
func DefaultConfig() *Config {
	return &Config{
		WindowSize:        DefaultWindowSize,
		MaxChunkSize:      MaxChunkSize,
		KeepAlive:         30 * time.Second,
		WriteTimeout:      10 * time.Second,
		MaxStreams:        1024,
		AcceptBacklog:     256,
		LoadBalancer:      NewRoundRobinBalancer(),
		ErrorHandler:      NewDefaultErrorHandler(),
		HealthMonitor:     NewDefaultHealthMonitor(),
		ConnectionManager: NewDefaultConnectionManager(),
		Middleware:        []Middleware{NewPassthroughMiddleware()},
	}
}

// session implements the core multiplexing session
type session struct {
	config *Config

	// Core components
	loadBalancer      LoadBalancer
	errorHandler      ErrorHandler
	healthMonitor     HealthMonitor
	connectionManager ConnectionManager
	middleware        []Middleware

	// Stream management
	streams      map[uint32]*stream
	streamsLock  sync.RWMutex
	nextStreamID uint32
	acceptCh     chan *stream

	// State management
	ctx        context.Context
	cancel     context.CancelFunc
	closed     int32
	closeCh    chan struct{}
	closeOnce  sync.Once

	// Statistics
	stats struct {
		streamsOpened  int64
		streamsClosed  int64
		bytesReceived  int64
		bytesSent      int64
		packetsReceived int64
		packetsSent    int64
	}
}

// NewSession creates a new multiplexing session
func NewSession(config *Config) Session {
	if config == nil {
		config = DefaultConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &session{
		config:            config,
		loadBalancer:      config.LoadBalancer,
		errorHandler:      config.ErrorHandler,
		healthMonitor:     config.HealthMonitor,
		connectionManager: config.ConnectionManager,
		middleware:        config.Middleware,
		streams:           make(map[uint32]*stream),
		nextStreamID:      1, // Start with odd numbers for client-initiated streams
		acceptCh:          make(chan *stream, config.AcceptBacklog),
		ctx:               ctx,
		cancel:            cancel,
		closeCh:           make(chan struct{}),
	}

	// Start background services
	go s.backgroundLoop()

	// Start health monitoring
	if s.healthMonitor != nil {
		go s.healthMonitor.StartMonitoring(ctx)
	}

	return s
}

// OpenStream creates a new outbound stream
func (s *session) OpenStream() (Stream, error) {
	if s.IsClosed() {
		return nil, ErrSessionClosed
	}

	s.streamsLock.Lock()
	streamID := s.nextStreamID
	s.nextStreamID += 2 // Use odd numbers for client-initiated streams
	if s.nextStreamID > s.config.MaxStreams {
		s.streamsLock.Unlock()
		return nil, fmt.Errorf("max streams exceeded")
	}
	s.streamsLock.Unlock()

	stream := newStream(s, streamID)

	s.streamsLock.Lock()
	s.streams[streamID] = stream
	s.streamsLock.Unlock()

	// Notify middleware
	for _, mw := range s.middleware {
		if err := mw.OnStreamOpen(streamID); err != nil {
			s.removeStream(streamID)
			return nil, fmt.Errorf("middleware %s failed: %w", mw.Name(), err)
		}
	}

	atomic.AddInt64(&s.stats.streamsOpened, 1)
	return stream, nil
}

// AcceptStream waits for and returns an incoming stream
func (s *session) AcceptStream() (Stream, error) {
	select {
	case stream := <-s.acceptCh:
		return stream, nil
	case <-s.closeCh:
		return nil, ErrSessionClosed
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

// AddConnection adds a connection to the session
func (s *session) AddConnection(conn Connection) error {
	if s.IsClosed() {
		return ErrSessionClosed
	}

	// Add to connection manager
	if err := s.connectionManager.AddConnection(conn); err != nil {
		return err
	}

	// Notify load balancer
	s.loadBalancer.OnConnectionAdded(conn)

	// Start handling the connection
	go s.handleConnection(conn)

	return nil
}

// RemoveConnection removes a connection from the session
func (s *session) RemoveConnection(connID string) error {
	// Remove from connection manager
	if err := s.connectionManager.RemoveConnection(connID); err != nil {
		return err
	}

	// Find and notify load balancer
	if conn, ok := s.connectionManager.GetConnection(connID); ok {
		s.loadBalancer.OnConnectionRemoved(conn)
	}

	return nil
}

// Close closes the session and all streams
func (s *session) Close() error {
	s.closeOnce.Do(func() {
		atomic.StoreInt32(&s.closed, 1)
		close(s.closeCh)
		s.cancel()

		// Close all streams
		s.streamsLock.Lock()
		for _, stream := range s.streams {
			stream.Close()
		}
		s.streamsLock.Unlock()

		// Stop health monitoring
		if s.healthMonitor != nil {
			s.healthMonitor.StopMonitoring()
		}
	})

	return nil
}

// IsClosed returns true if the session is closed
func (s *session) IsClosed() bool {
	return atomic.LoadInt32(&s.closed) == 1
}

// NumStreams returns the number of active streams
func (s *session) NumStreams() int {
	s.streamsLock.RLock()
	defer s.streamsLock.RUnlock()
	return len(s.streams)
}

// NumConnections returns the number of active connections
func (s *session) NumConnections() int {
	connections := s.connectionManager.GetAllConnections()
	return len(connections)
}

// handleConnection processes data from a connection
func (s *session) handleConnection(conn Connection) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Connection handler panicked: %v\n", r)
		}
		s.connectionManager.RemoveConnection(conn.ID())
		s.loadBalancer.OnConnectionRemoved(conn)
		conn.Close()
	}()

	buffer := make([]byte, s.config.MaxChunkSize+16) // Extra space for headers

	for {
		select {
		case <-s.closeCh:
			return
		case <-s.ctx.Done():
			return
		default:
		}

		// Set read timeout
		conn.SetReadDeadline(time.Now().Add(s.config.WriteTimeout))

		n, err := conn.Read(buffer)
		if err != nil {
			if err == io.EOF {
				return
			}

			action := s.errorHandler.HandleConnectionError(conn, err)
			switch action {
			case ErrorActionIgnore:
				continue
			case ErrorActionRetry:
				time.Sleep(100 * time.Millisecond)
				continue
			case ErrorActionReconnect, ErrorActionFailover, ErrorActionShutdown:
				return
			}
		}

		// Process received data
		if err := s.processPacket(buffer[:n]); err != nil {
			fmt.Printf("Error processing packet: %v\n", err)
			continue
		}

		atomic.AddInt64(&s.stats.bytesReceived, int64(n))
		atomic.AddInt64(&s.stats.packetsReceived, 1)
	}
}

// processPacket processes an incoming packet
func (s *session) processPacket(data []byte) error {
	if len(data) < 8 {
		return ErrInvalidData
	}

	// Parse packet header
	packetType := data[0]
	if packetType == ControlPacket {
		return s.processControlPacket(data)
	}

	// Data packet
	size := uint16(data[1])<<8 | uint16(data[2])
	streamID := uint32(data[3])<<24 | uint32(data[4])<<16 | uint32(data[5])<<8 | uint32(data[6])
	chunkID := uint16(data[7])<<8 | uint16(data[8])

	if len(data) < int(9+size) {
		return ErrInvalidData
	}

	payload := data[9 : 9+size]

	// Process through middleware
	processedData := payload
	for _, mw := range s.middleware {
		var err error
		processedData, err = mw.ProcessInbound(streamID, processedData)
		if err != nil {
			return fmt.Errorf("middleware %s failed: %w", mw.Name(), err)
		}
	}

	// Find or create stream
	stream := s.getOrCreateStream(streamID)
	if stream == nil {
		return fmt.Errorf("failed to get stream %d", streamID)
	}

	// Deliver data to stream
	stream.receiveChunk(chunkID, processedData)

	return nil
}

// processControlPacket processes control packets
func (s *session) processControlPacket(data []byte) error {
	if len(data) < 2 {
		return ErrInvalidData
	}

	subType := data[1]
	switch subType {
	case PingPacket:
		// Respond with pong
		// Implementation would send pong back
	case PongPacket:
		// Handle pong response
		// Implementation would update latency metrics
	case KeepAlivePacket:
		// Just acknowledge the keep-alive
	}

	return nil
}

// getOrCreateStream finds an existing stream or creates a new one for remote-initiated streams
func (s *session) getOrCreateStream(streamID uint32) *stream {
	s.streamsLock.RLock()
	stream, exists := s.streams[streamID]
	s.streamsLock.RUnlock()

	if exists {
		return stream
	}

	// Only create streams for even IDs (remote-initiated)
	if streamID%2 != 0 {
		return nil
	}

	// Create new stream
	stream = newStream(s, streamID)

	s.streamsLock.Lock()
	s.streams[streamID] = stream
	s.streamsLock.Unlock()

	// Notify middleware
	for _, mw := range s.middleware {
		if err := mw.OnStreamOpen(streamID); err != nil {
			s.removeStream(streamID)
			return nil
		}
	}

	// Queue for acceptance
	select {
	case s.acceptCh <- stream:
	default:
		// Channel full, drop the stream
		s.removeStream(streamID)
		return nil
	}

	atomic.AddInt64(&s.stats.streamsOpened, 1)
	return stream
}

// removeStream removes a stream from the session
func (s *session) removeStream(streamID uint32) {
	s.streamsLock.Lock()
	delete(s.streams, streamID)
	s.streamsLock.Unlock()

	// Notify middleware
	for _, mw := range s.middleware {
		mw.OnStreamClose(streamID)
	}

	atomic.AddInt64(&s.stats.streamsClosed, 1)
}

// sendData sends data through the best available connection
func (s *session) sendData(streamID uint32, chunkID uint16, data []byte) error {
	if s.IsClosed() {
		return ErrSessionClosed
	}

	// Process through middleware
	processedData := data
	for _, mw := range s.middleware {
		var err error
		processedData, err = mw.ProcessOutbound(streamID, processedData)
		if err != nil {
			return fmt.Errorf("middleware %s failed: %w", mw.Name(), err)
		}
	}

	// Get healthy connections
	connections := s.connectionManager.GetHealthyConnections()
	if len(connections) == 0 {
		return ErrNoConnections
	}

	// Select connection using load balancer
	conn, err := s.loadBalancer.Select(connections, streamID, processedData)
	if err != nil {
		return fmt.Errorf("load balancer failed: %w", err)
	}

	// Create packet
	packet := make([]byte, 9+len(processedData))
	packet[0] = DataPacket
	packet[1] = byte(len(processedData) >> 8)
	packet[2] = byte(len(processedData))
	packet[3] = byte(streamID >> 24)
	packet[4] = byte(streamID >> 16)
	packet[5] = byte(streamID >> 8)
	packet[6] = byte(streamID)
	packet[7] = byte(chunkID >> 8)
	packet[8] = byte(chunkID)
	copy(packet[9:], processedData)

	// Send packet
	conn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout))
	_, err = conn.Write(packet)
	if err != nil {
		action := s.errorHandler.HandleConnectionError(conn, err)
		if action == ErrorActionReconnect || action == ErrorActionFailover {
			s.connectionManager.RemoveConnection(conn.ID())
		}
		return err
	}

	atomic.AddInt64(&s.stats.bytesSent, int64(len(packet)))
	atomic.AddInt64(&s.stats.packetsSent, 1)

	return nil
}

// backgroundLoop handles background maintenance tasks
func (s *session) backgroundLoop() {
	if s.config.KeepAlive <= 0 {
		return
	}

	ticker := time.NewTicker(s.config.KeepAlive)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.sendKeepAlives()
		case <-s.closeCh:
			return
		case <-s.ctx.Done():
			return
		}
	}
}

// sendKeepAlives sends keep-alive packets to all connections
func (s *session) sendKeepAlives() {
	connections := s.connectionManager.GetAllConnections()
	
	keepAlivePacket := []byte{ControlPacket, KeepAlivePacket}
	
	for _, conn := range connections {
		conn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout))
		conn.Write(keepAlivePacket)
	}
}
