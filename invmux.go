// Package invmux implements an inverse multiplexer over multiple physical connections.
package invmux

import (
	"errors"
	"io"
	"net"
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

	// EnableAutoReconnect automatically tries to reconnect dropped connections
	EnableAutoReconnect bool

	// ReconnectInterval is how often to try reconnecting dropped connections
	ReconnectInterval time.Duration

	// EnableCompression enables compression of chunks
	EnableCompression bool

	// CompressionLevel sets the compression level (0-9, 0=none, 9=best)
	CompressionLevel int

	// EnableEncryption enables encryption of chunks
	EnableEncryption bool

	// EncryptionKey is the key used for encryption
	EncryptionKey []byte
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
		EnableAutoReconnect:        false,
		ReconnectInterval:          5 * time.Second,
		EnableCompression:          false,
		CompressionLevel:           6,
		EnableEncryption:           false,
	}
}

// Session is used to manage the inverse multiplexing of a single logical connection
type Session struct {
	config              *Config
	physicalConnections []net.Conn
	nextConnIndex       int
	connLock            sync.Mutex

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
		config:              config,
		physicalConnections: make([]net.Conn, 0),
		streams:             make(map[uint32]*Stream),
		nextStreamID:        1,
		acceptCh:            make(chan *Stream, 1024),
		acceptNotifyCh:      make(chan struct{}, 1),
		shutdownCh:          make(chan struct{}),
		receiveChunks:       make(map[uint32]map[uint16][]byte),
	}

	return s
}

// AddPhysicalConnection adds a physical connection to the session
func (s *Session) AddPhysicalConnection(conn net.Conn) error {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	s.physicalConnections = append(s.physicalConnections, conn)
	go s.handleConnection(conn)
	return nil
}

// handleConnection processes data from a physical connection
func (s *Session) handleConnection(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, s.config.MaxChunkSize+10) // Header + payload

	for {
		// Read the header (size, stream ID, chunk ID, flags)
		_, err := io.ReadFull(conn, buf[:8])
		if err != nil {
			s.Close()
			return
		}

		// Parse header
		size := uint16(buf[0])<<8 | uint16(buf[1])
		streamID := uint32(buf[2])<<24 | uint32(buf[3])<<16 | uint32(buf[4])<<8 | uint32(buf[5])
		chunkID := uint16(buf[6])<<8 | uint16(buf[7])

		// Read the payload
		_, err = io.ReadFull(conn, buf[8:8+size])
		if err != nil {
			s.Close()
			return
		}

		payload := make([]byte, size)
		copy(payload, buf[8:8+size])

		// Process the chunk
		s.receiveChunk(streamID, chunkID, payload)
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

// sendChunk sends a chunk through one of the physical connections
func (s *Session) sendChunk(streamID uint32, chunkID uint16, data []byte) error {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	if len(s.physicalConnections) == 0 {
		return ErrNoPhysicalConns
	}

	// Round robin selection of connection
	conn := s.physicalConnections[s.nextConnIndex]
	s.nextConnIndex = (s.nextConnIndex + 1) % len(s.physicalConnections)

	// Construct header + data
	header := make([]byte, 8)
	header[0] = byte(len(data) >> 8)
	header[1] = byte(len(data))
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
		conn.Close()
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

// ConnectionStats stores statistics for a physical connection
type ConnectionStats struct {
	Latency    time.Duration
	Bandwidth  int64 // bytes per second
	PacketLoss float64
	ID         string
}

// physicalConnection wraps a net.Conn with additional metadata
type physicalConnection struct {
	conn  net.Conn
	stats ConnectionStats
}

// sendChunkLowestLatency sends a chunk through the connection with the lowest latency
func (s *Session) sendChunkLowestLatency(streamID uint32, chunkID uint16, data []byte) error {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	if len(s.physicalConnections) == 0 {
		return ErrNoPhysicalConns
	}

	// Find the connection with lowest latency
	// In a real implementation, this would use actual latency measurements
	// For now, we'll just use the first connection as a placeholder
	conn := s.physicalConnections[0]

	// Construct header + data
	header := make([]byte, 8)
	header[0] = byte(len(data) >> 8)
	header[1] = byte(len(data))
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

// sendChunkHighestBandwidth sends a chunk through the connection with the highest bandwidth
func (s *Session) sendChunkHighestBandwidth(streamID uint32, chunkID uint16, data []byte) error {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	if len(s.physicalConnections) == 0 {
		return ErrNoPhysicalConns
	}

	// Find the connection with highest bandwidth
	// In a real implementation, this would use actual bandwidth measurements
	// For now, we'll just use the first connection as a placeholder
	conn := s.physicalConnections[0]

	// Construct header + data
	header := make([]byte, 8)
	header[0] = byte(len(data) >> 8)
	header[1] = byte(len(data))
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

// sendChunkWeighted sends a chunk through connections based on their weights
func (s *Session) sendChunkWeighted(streamID uint32, chunkID uint16, data []byte) error {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	if len(s.physicalConnections) == 0 {
		return ErrNoPhysicalConns
	}

	// In a real implementation, this would use the connection weights
	// For now, we'll just use round robin as a placeholder
	conn := s.physicalConnections[s.nextConnIndex]
	s.nextConnIndex = (s.nextConnIndex + 1) % len(s.physicalConnections)

	// Construct header + data
	header := make([]byte, 8)
	header[0] = byte(len(data) >> 8)
	header[1] = byte(len(data))
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
