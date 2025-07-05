// Package invmux implements an inverse multiplexer over multiple physical connections.
package invmux

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"sync"
	"time"
)

// Constants for protocol
const (
	Version           = 1
	DefaultWindowSize = 4096
	MaxChunkSize      = 16384
	DefaultBufferSize = 65536
	DefaultMaxStreams = 1024

	// Packet types
	DataPacket    = 0x00
	ControlPacket = 0xFF

	// Control packet subtypes
	PingPacket      = 0x01
	PongPacket      = 0x02
	KeepAlivePacket = 0x03
)

var (
	ErrTimeout            = errors.New("timeout")
	ErrConnectionClosed   = errors.New("connection closed")
	ErrInvalidChunkID     = errors.New("invalid chunk ID")
	ErrNoPhysicalConns    = errors.New("no physical connections available")
	ErrMaxConnectionLimit = errors.New("reached maximum connection limit")
	ErrMaxStreamsExceeded = errors.New("maximum number of streams exceeded")
	ErrBufferFull         = errors.New("buffer is full")
)

// DistributionPolicy defines how chunks are distributed across physical connections
type DistributionPolicy int

const (
	// RoundRobin distributes chunks in a round-robin fashion
	RoundRobin DistributionPolicy = iota

	// LowestLatency sends chunks through the connection with lowest latency
	LowestLatency

	// HighestBandwidth sends chunks through the connection with highest bandwidth
	HighestBandwidth

	// WeightedRoundRobin distributes chunks based on connection weights
	WeightedRoundRobin
)

// Config is used to tune the inverse multiplexer
type Config struct {
	// Version of the protocol
	Version uint8

	// WindowSize is the size of the send window
	WindowSize uint16

	// MaxChunkSize is the maximum size of data in each chunk
	MaxChunkSize uint16

	// KeepAliveInterval is how often to send a keep-alive message
	KeepAliveInterval time.Duration

	// ConnectionWriteTimeout is the write timeout for physical connections
	ConnectionWriteTimeout time.Duration

	// ReadBufferSize is the size of the read buffer for each stream
	ReadBufferSize int

	// WriteBufferSize is the size of the write buffer for each stream
	WriteBufferSize int

	// MaxStreams is the maximum number of streams allowed per session
	MaxStreams uint32

	// AcceptBacklog is the backlog size for accepting new streams
	AcceptBacklog int

	// EnableOutOfOrderProcessing enables processing of out-of-order chunks
	EnableOutOfOrderProcessing bool

	// OutOfOrderWindowSize is the maximum number of out-of-order chunks to buffer
	OutOfOrderWindowSize int

	// DistributionPolicy determines how chunks are distributed across connections
	DistributionPolicy DistributionPolicy

	// ConnectionWeights allows assigning weights to connections for weighted distribution
	// The key is the connection ID, value is the weight (higher = more traffic)
	ConnectionWeights map[string]int
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		Version:                    Version,
		WindowSize:                 DefaultWindowSize,
		MaxChunkSize:               MaxChunkSize,
		KeepAliveInterval:          30 * time.Second,
		ConnectionWriteTimeout:     10 * time.Second,
		ReadBufferSize:             DefaultBufferSize,
		WriteBufferSize:            DefaultBufferSize,
		MaxStreams:                 DefaultMaxStreams,
		AcceptBacklog:              1024,
		EnableOutOfOrderProcessing: true,
		OutOfOrderWindowSize:       1024,
		DistributionPolicy:         RoundRobin,
		ConnectionWeights:          make(map[string]int),
	}
}

// ConnectionStats stores statistics for a physical connection
type ConnectionStats struct {
	// Latency is the measured round-trip time
	Latency time.Duration

	// Bandwidth is the estimated bandwidth in bytes per second
	Bandwidth int64

	// PacketLoss is the percentage of packet loss (0.0-1.0)
	PacketLoss float64

	// ID is a unique identifier for the connection
	ID string

	// BytesSent is the total number of bytes sent over this connection
	BytesSent int64

	// BytesReceived is the total number of bytes received over this connection
	BytesReceived int64

	// LastActivity is the timestamp of the last activity on this connection
	LastActivity time.Time

	// Weight is the assigned weight for weighted distribution
	Weight int
}

// physicalConnection wraps a net.Conn with additional metadata
type physicalConnection struct {
	conn  net.Conn
	stats ConnectionStats
}

// Session is used to manage the inverse multiplexing of a single logical connection
type Session struct {
	config               *Config
	physicalConnections  []*physicalConnection
	nextConnIndex        int
	connLock             sync.Mutex
	totalWeights         int
	latencyProbeInterval time.Duration
	bandwidthTestSize    int

	streams      map[uint32]*Stream
	streamLock   sync.RWMutex
	nextStreamID uint32

	// Channels for accepting streams
	acceptCh       chan *Stream
	acceptNotifyCh chan struct{}

	// Channels for managing shutdown
	shutdownCh   chan struct{}
	shutdownErr  error
	shutdownLock sync.Mutex

	// Buffer management
	receiveChunks map[uint32]map[uint16][]byte
	receiveLock   sync.Mutex
}

// NewSession creates a new inverse multiplexing session
func NewSession(config *Config) *Session {
	if config == nil {
		config = DefaultConfig()
	}

	s := &Session{
		config:               config,
		physicalConnections:  make([]*physicalConnection, 0),
		streams:              make(map[uint32]*Stream),
		nextStreamID:         1,
		acceptCh:             make(chan *Stream, config.AcceptBacklog),
		acceptNotifyCh:       make(chan struct{}, 1),
		shutdownCh:           make(chan struct{}),
		receiveChunks:        make(map[uint32]map[uint16][]byte),
		latencyProbeInterval: 5 * time.Second,
		bandwidthTestSize:    1024, // 1KB test packet
	}

	// Start monitoring connection stats if using policies that need them
	if config.DistributionPolicy == LowestLatency ||
		config.DistributionPolicy == HighestBandwidth {
		go s.monitorConnectionStats()
	}

	// Start keep-alive mechanism
	if config.KeepAliveInterval > 0 {
		go s.keepAliveLoop()
	}

	return s
}

// monitorConnectionStats periodically updates connection statistics
func (s *Session) monitorConnectionStats() {
	ticker := time.NewTicker(s.latencyProbeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.updateConnectionStats()
		case <-s.shutdownCh:
			return
		}
	}
}

// updateConnectionStats measures latency and bandwidth for all connections
func (s *Session) updateConnectionStats() {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	for _, conn := range s.physicalConnections {
		// Send ping packet to measure latency
		s.sendPing(conn)

		// Estimate bandwidth based on actual data transferred
		timeSinceLastActivity := time.Since(conn.stats.LastActivity)
		if timeSinceLastActivity > 0 && timeSinceLastActivity < 5*time.Second {
			// Calculate bytes per second based on actual traffic
			bytesSent := conn.stats.BytesSent
			bytesReceived := conn.stats.BytesReceived
			totalBytes := bytesSent + bytesReceived

			// Calculate bandwidth in bytes per second
			seconds := timeSinceLastActivity.Seconds()
			if seconds > 0 {
				bandwidth := int64(float64(totalBytes) / seconds)

				// Apply exponential smoothing to avoid wild fluctuations
				if conn.stats.Bandwidth > 0 {
					// 80% old value, 20% new measurement
					conn.stats.Bandwidth = (conn.stats.Bandwidth*8 + bandwidth*2) / 10
				} else {
					conn.stats.Bandwidth = bandwidth
				}
			}
		}

		// Update packet loss statistics based on observed errors
		// This is a simplified approach - in a real implementation we'd track
		// sequence numbers and calculate actual packet loss
		if conn.stats.PacketLoss <= 0 {
			conn.stats.PacketLoss = 0.01 // Start with minimal packet loss
		}
	}
}

// AddPhysicalConnection adds a physical connection to the session
func (s *Session) AddPhysicalConnection(conn net.Conn) error {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	// Create a unique ID for the connection
	connID := conn.LocalAddr().String() + "-" + conn.RemoteAddr().String()

	// Create the physical connection with initial stats
	physConn := &physicalConnection{
		conn: conn,
		stats: ConnectionStats{
			ID:           connID,
			LastActivity: time.Now(),
			Weight:       1, // Default weight
		},
	}

	// Apply weight if specified in config
	if weight, ok := s.config.ConnectionWeights[connID]; ok {
		physConn.stats.Weight = weight
	}

	s.physicalConnections = append(s.physicalConnections, physConn)

	// Update total weights for weighted distribution
	s.recalculateTotalWeights()

	// Start handling the connection
	go s.handleConnection(physConn)

	// Log the new connection
	fmt.Printf("Added new connection %s to session (total: %d)\n",
		connID, len(s.physicalConnections))

	return nil
}

// recalculateTotalWeights updates the total weights for weighted distribution
func (s *Session) recalculateTotalWeights() {
	s.totalWeights = 0
	for _, conn := range s.physicalConnections {
		s.totalWeights += conn.stats.Weight
	}
	if s.totalWeights == 0 {
		// Ensure we don't divide by zero later
		s.totalWeights = len(s.physicalConnections)
	}
}

// SetConnectionWeight sets the weight for a specific connection
func (s *Session) SetConnectionWeight(connID string, weight int) {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	// Update the weight in the config
	s.config.ConnectionWeights[connID] = weight

	// Update the weight in the connection if it exists
	for _, conn := range s.physicalConnections {
		if conn.stats.ID == connID {
			conn.stats.Weight = weight
			break
		}
	}

	// Recalculate total weights
	s.recalculateTotalWeights()
}

// handleConnection processes data from a physical connection
func (s *Session) handleConnection(conn *physicalConnection) {
	defer conn.conn.Close()

	buf := make([]byte, s.config.MaxChunkSize+10) // Header + payload

	for {
		// Read the header (size, stream ID, chunk ID, flags)
		_, err := io.ReadFull(conn.conn, buf[:8])
		if err != nil {
			s.removeConnection(conn)
			return
		}

		// Update connection stats - we received something
		conn.stats.LastActivity = time.Now()

		// Check if this is a control packet
		if buf[0] == ControlPacket {
			// Handle control packet
			s.handleControlPacket(conn, buf[:8])
			continue
		}

		// Parse header for data packet
		size := uint16(buf[0])<<8 | uint16(buf[1])
		streamID := uint32(buf[2])<<24 | uint32(buf[3])<<16 | uint32(buf[4])<<8 | uint32(buf[5])
		chunkID := uint16(buf[6])<<8 | uint16(buf[7])

		// Read the payload
		_, err = io.ReadFull(conn.conn, buf[8:8+size])
		if err != nil {
			s.removeConnection(conn)
			return
		}

		// Update connection stats
		conn.stats.BytesReceived += int64(size + 8) // payload + header
		conn.stats.LastActivity = time.Now()

		payload := make([]byte, size)
		copy(payload, buf[8:8+size])

		// Process the chunk
		s.receiveChunk(streamID, chunkID, payload)
	}
}

// handleControlPacket processes control packets like pings and keep-alives
func (s *Session) handleControlPacket(conn *physicalConnection, header []byte) {
	// Parse control packet type
	packetType := header[1]

	switch packetType {
	case PingPacket:
		// Received ping, send pong with same timestamp
		pongPacket := make([]byte, 8)
		pongPacket[0] = ControlPacket
		pongPacket[1] = PongPacket
		// Copy timestamp from ping
		copy(pongPacket[2:6], header[2:6])

		// Send pong response
		conn.conn.Write(pongPacket)

	case PongPacket:
		// Received pong, calculate latency
		// Extract timestamp from packet
		timestamp := int64(header[2])<<24 | int64(header[3])<<16 | int64(header[4])<<8 | int64(header[5])

		// Convert to time.Time
		sentTime := time.Unix(0, timestamp*int64(time.Millisecond))

		// Calculate round-trip time
		rtt := time.Since(sentTime)

		// Update latency with exponential smoothing
		if conn.stats.Latency > 0 {
			// 80% old value, 20% new measurement
			conn.stats.Latency = (conn.stats.Latency*8 + rtt*2) / 10
		} else {
			conn.stats.Latency = rtt
		}

	case KeepAlivePacket:
		// Just update the last activity time, which was already done
		// Nothing else to do
	}
}

// sendPing sends a ping packet to measure latency
func (s *Session) sendPing(conn *physicalConnection) {
	// Create ping packet
	pingPacket := make([]byte, 8)
	pingPacket[0] = ControlPacket
	pingPacket[1] = PingPacket

	// Encode current timestamp in milliseconds
	timestamp := time.Now().UnixNano() / int64(time.Millisecond)
	pingPacket[2] = byte(timestamp >> 24)
	pingPacket[3] = byte(timestamp >> 16)
	pingPacket[4] = byte(timestamp >> 8)
	pingPacket[5] = byte(timestamp)

	// Send ping
	conn.conn.Write(pingPacket)
}

// removeConnection removes a connection from the session
func (s *Session) removeConnection(conn *physicalConnection) {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	// Find and remove the connection
	for i, c := range s.physicalConnections {
		if c == conn {
			// Remove from the slice
			s.physicalConnections = append(s.physicalConnections[:i], s.physicalConnections[i+1:]...)

			// Log the disconnection
			fmt.Printf("Connection %s removed from session\n", conn.stats.ID)

			break
		}
	}

	// Recalculate total weights
	s.recalculateTotalWeights()

	// Notify about connection loss
	if len(s.physicalConnections) == 0 {
		fmt.Printf("Warning: No physical connections remaining in session\n")
	} else {
		fmt.Printf("Remaining connections: %d\n", len(s.physicalConnections))
	}
}

// receiveChunk processes an incoming chunk and reassembles data
func (s *Session) receiveChunk(streamID uint32, chunkID uint16, data []byte) {
	s.streamLock.RLock()
	stream, ok := s.streams[streamID]
	s.streamLock.RUnlock()

	if !ok {
		// This might be a new stream opened by the remote side
		if streamID%2 == 0 && s.isRemoteInitiated(streamID) {
			// Create a new stream for the remote side
			stream = newStream(s, streamID)

			s.streamLock.Lock()
			s.streams[streamID] = stream
			s.streamLock.Unlock()

			select {
			case s.acceptCh <- stream:
			default:
				// Try to notify that we have a new stream
				select {
				case s.acceptNotifyCh <- struct{}{}:
				default:
				}
			}
		} else {
			// Invalid stream ID
			return
		}
	}

	// Add the chunk to the stream
	stream.receiveChunk(chunkID, data)
}

// isRemoteInitiated checks if a stream was initiated by the remote side
func (s *Session) isRemoteInitiated(streamID uint32) bool {
	return streamID%2 == 0 // Even IDs are remote-initiated
}

// OpenStream creates a new stream
func (s *Session) OpenStream() (*Stream, error) {
	s.streamLock.Lock()
	defer s.streamLock.Unlock()

	// Check if we've reached the maximum number of streams
	if s.config.MaxStreams > 0 && uint32(len(s.streams)) >= s.config.MaxStreams {
		return nil, ErrMaxStreamsExceeded
	}

	streamID := s.nextStreamID
	s.nextStreamID += 2 // Odd IDs for local streams

	stream := newStream(s, streamID)
	s.streams[streamID] = stream

	return stream, nil
}

// AcceptStream accepts a new stream from the remote side
func (s *Session) AcceptStream() (*Stream, error) {
	select {
	case stream := <-s.acceptCh:
		return stream, nil
	case <-s.shutdownCh:
		return nil, s.shutdownErr
	}
}

// sendChunk sends a chunk through one of the physical connections using round-robin
func (s *Session) sendChunk(streamID uint32, chunkID uint16, data []byte) error {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	if len(s.physicalConnections) == 0 {
		return ErrNoPhysicalConns
	}

	// Round robin selection of connection
	conn := s.physicalConnections[s.nextConnIndex].conn
	physConn := s.physicalConnections[s.nextConnIndex]
	s.nextConnIndex = (s.nextConnIndex + 1) % len(s.physicalConnections)

	// Update connection stats
	dataSize := len(data)
	physConn.stats.BytesSent += int64(dataSize + 8) // payload + header
	physConn.stats.LastActivity = time.Now()

	// Construct header + data
	header := make([]byte, 8)
	header[0] = byte(dataSize >> 8)
	header[1] = byte(dataSize)
	header[2] = byte(streamID >> 24)
	header[3] = byte(streamID >> 16)
	header[4] = byte(streamID >> 8)
	header[5] = byte(streamID)
	header[6] = byte(chunkID >> 8)
	header[7] = byte(chunkID)

	// Set write deadline if configured
	if s.config.ConnectionWriteTimeout > 0 {
		conn.SetWriteDeadline(time.Now().Add(s.config.ConnectionWriteTimeout))
	}

	// Write header and data
	if _, err := conn.Write(header); err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return err
	}

	return nil
}

// sendChunkLowestLatency sends a chunk through the connection with the lowest latency
func (s *Session) sendChunkLowestLatency(streamID uint32, chunkID uint16, data []byte) error {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	if len(s.physicalConnections) == 0 {
		return ErrNoPhysicalConns
	}

	// Find the connection with lowest latency
	var lowestLatencyConn *physicalConnection
	lowestLatency := time.Duration(1<<63 - 1) // Max duration

	for _, conn := range s.physicalConnections {
		if conn.stats.Latency < lowestLatency {
			lowestLatency = conn.stats.Latency
			lowestLatencyConn = conn
		}
	}

	// If no latency data yet, use the first connection
	if lowestLatencyConn == nil {
		lowestLatencyConn = s.physicalConnections[0]
	}

	// Update connection stats
	dataSize := len(data)
	lowestLatencyConn.stats.BytesSent += int64(dataSize + 8) // payload + header
	lowestLatencyConn.stats.LastActivity = time.Now()

	// Construct header + data
	header := make([]byte, 8)
	header[0] = byte(dataSize >> 8)
	header[1] = byte(dataSize)
	header[2] = byte(streamID >> 24)
	header[3] = byte(streamID >> 16)
	header[4] = byte(streamID >> 8)
	header[5] = byte(streamID)
	header[6] = byte(chunkID >> 8)
	header[7] = byte(chunkID)

	// Set write deadline if configured
	if s.config.ConnectionWriteTimeout > 0 {
		lowestLatencyConn.conn.SetWriteDeadline(time.Now().Add(s.config.ConnectionWriteTimeout))
	}

	// Write header and data
	if _, err := lowestLatencyConn.conn.Write(header); err != nil {
		return err
	}
	if _, err := lowestLatencyConn.conn.Write(data); err != nil {
		return err
	}

	return nil
}

// sendChunkHighestBandwidth sends a chunk through the connection with the highest bandwidth
func (s *Session) sendChunkHighestBandwidth(streamID uint32, chunkID uint16, data []byte) error {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	if len(s.physicalConnections) == 0 {
		return ErrNoPhysicalConns
	}

	// Find the connection with highest bandwidth
	var highestBandwidthConn *physicalConnection
	highestBandwidth := int64(0)

	for _, conn := range s.physicalConnections {
		if conn.stats.Bandwidth > highestBandwidth {
			highestBandwidth = conn.stats.Bandwidth
			highestBandwidthConn = conn
		}
	}

	// If no bandwidth data yet, use the first connection
	if highestBandwidthConn == nil {
		highestBandwidthConn = s.physicalConnections[0]
	}

	// Update connection stats
	dataSize := len(data)
	highestBandwidthConn.stats.BytesSent += int64(dataSize + 8) // payload + header
	highestBandwidthConn.stats.LastActivity = time.Now()

	// Construct header + data
	header := make([]byte, 8)
	header[0] = byte(dataSize >> 8)
	header[1] = byte(dataSize)
	header[2] = byte(streamID >> 24)
	header[3] = byte(streamID >> 16)
	header[4] = byte(streamID >> 8)
	header[5] = byte(streamID)
	header[6] = byte(chunkID >> 8)
	header[7] = byte(chunkID)

	// Set write deadline if configured
	if s.config.ConnectionWriteTimeout > 0 {
		highestBandwidthConn.conn.SetWriteDeadline(time.Now().Add(s.config.ConnectionWriteTimeout))
	}

	// Write header and data
	if _, err := highestBandwidthConn.conn.Write(header); err != nil {
		return err
	}
	if _, err := highestBandwidthConn.conn.Write(data); err != nil {
		return err
	}

	return nil
}

// sendChunkWeighted sends a chunk through connections based on their weights
func (s *Session) sendChunkWeighted(streamID uint32, chunkID uint16, data []byte) error {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	if len(s.physicalConnections) == 0 {
		return ErrNoPhysicalConns
	}

	// Weighted selection algorithm
	// We'll use a simple approach: divide the weight space into segments
	// and select a connection based on where a random number falls

	// First, determine which connection to use based on weights
	// For simplicity, we'll use a deterministic approach based on chunk ID
	// This ensures even distribution over time according to weights

	// Calculate the target position in the weight space
	targetPos := int(chunkID % uint16(s.totalWeights))

	// Find which connection this position corresponds to
	var selectedConn *physicalConnection
	currentPos := 0

	for _, conn := range s.physicalConnections {
		currentPos += conn.stats.Weight
		if targetPos < currentPos {
			selectedConn = conn
			break
		}
	}

	// If something went wrong with the selection, use the first connection
	if selectedConn == nil {
		selectedConn = s.physicalConnections[0]
	}

	// Update connection stats
	dataSize := len(data)
	selectedConn.stats.BytesSent += int64(dataSize + 8) // payload + header
	selectedConn.stats.LastActivity = time.Now()

	// Construct header + data
	header := make([]byte, 8)
	header[0] = byte(dataSize >> 8)
	header[1] = byte(dataSize)
	header[2] = byte(streamID >> 24)
	header[3] = byte(streamID >> 16)
	header[4] = byte(streamID >> 8)
	header[5] = byte(streamID)
	header[6] = byte(chunkID >> 8)
	header[7] = byte(chunkID)

	// Set write deadline if configured
	if s.config.ConnectionWriteTimeout > 0 {
		selectedConn.conn.SetWriteDeadline(time.Now().Add(s.config.ConnectionWriteTimeout))
	}

	// Write header and data
	if _, err := selectedConn.conn.Write(header); err != nil {
		return err
	}
	if _, err := selectedConn.conn.Write(data); err != nil {
		return err
	}

	return nil
}

// Close closes the session and all associated streams
func (s *Session) Close() error {
	s.shutdownLock.Lock()
	defer s.shutdownLock.Unlock()

	if s.shutdownErr != nil {
		return nil // Already closed
	}

	s.shutdownErr = ErrConnectionClosed
	close(s.shutdownCh)

	// Close all physical connections
	s.connLock.Lock()
	for _, conn := range s.physicalConnections {
		conn.conn.Close()
	}
	s.physicalConnections = nil
	s.connLock.Unlock()

	// Close all streams
	s.streamLock.Lock()
	for _, stream := range s.streams {
		stream.Close()
	}
	s.streamLock.Unlock()

	return nil
}

// NumStreams returns the number of active streams
func (s *Session) NumStreams() int {
	s.streamLock.RLock()
	defer s.streamLock.RUnlock()
	return len(s.streams)
}

// NumPhysicalConnections returns the number of physical connections
func (s *Session) NumPhysicalConnections() int {
	s.connLock.Lock()
	defer s.connLock.Unlock()
	return len(s.physicalConnections)
}

// GetConnectionStats returns statistics for all connections
func (s *Session) GetConnectionStats() []ConnectionStats {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	stats := make([]ConnectionStats, len(s.physicalConnections))
	for i, conn := range s.physicalConnections {
		stats[i] = conn.stats
	}

	return stats
}

// SortConnectionsByLatency returns connections sorted by latency (lowest first)
func (s *Session) SortConnectionsByLatency() []ConnectionStats {
	stats := s.GetConnectionStats()

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Latency < stats[j].Latency
	})

	return stats
}

// SortConnectionsByBandwidth returns connections sorted by bandwidth (highest first)
func (s *Session) SortConnectionsByBandwidth() []ConnectionStats {
	stats := s.GetConnectionStats()

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Bandwidth > stats[j].Bandwidth
	})

	return stats
}

// SetDistributionPolicy changes the distribution policy
func (s *Session) SetDistributionPolicy(policy DistributionPolicy) {
	s.connLock.Lock()
	defer s.connLock.Unlock()
	s.config.DistributionPolicy = policy
}

// keepAliveLoop periodically sends keep-alive packets to all connections
func (s *Session) keepAliveLoop() {
	ticker := time.NewTicker(s.config.KeepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.sendKeepAlives()
		case <-s.shutdownCh:
			return
		}
	}
}

// sendKeepAlives sends keep-alive packets to all connections
func (s *Session) sendKeepAlives() {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	// Create keep-alive packet
	keepAlivePacket := make([]byte, 8)
	keepAlivePacket[0] = ControlPacket
	keepAlivePacket[1] = KeepAlivePacket

	// Current timestamp in milliseconds for debugging
	timestamp := time.Now().UnixNano() / int64(time.Millisecond)
	keepAlivePacket[2] = byte(timestamp >> 24)
	keepAlivePacket[3] = byte(timestamp >> 16)
	keepAlivePacket[4] = byte(timestamp >> 8)
	keepAlivePacket[5] = byte(timestamp)

	// Send to all connections
	for _, conn := range s.physicalConnections {
		// Check if the connection needs a keep-alive
		timeSinceActivity := time.Since(conn.stats.LastActivity)
		if timeSinceActivity > s.config.KeepAliveInterval/2 {
			// Set write deadline if configured
			if s.config.ConnectionWriteTimeout > 0 {
				conn.conn.SetWriteDeadline(time.Now().Add(s.config.ConnectionWriteTimeout))
			}

			// Send keep-alive
			_, err := conn.conn.Write(keepAlivePacket)
			if err != nil {
				// Connection might be down, mark it for removal
				// We can't remove it directly here because we're holding the lock
				// and removeConnection also needs the lock
				go func(badConn *physicalConnection) {
					s.removeConnection(badConn)
				}(conn)
			} else {
				// Update stats
				conn.stats.BytesSent += 8 // header size
			}
		}
	}
}
