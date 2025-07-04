package invmux

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Stream represents a logical bidirectional connection
type Stream struct {
	id      uint32
	session *Session

	// Read related
	readBuffer       []byte
	readLock         sync.Mutex
	nextReadChunk    uint16
	readDeadline     time.Time
	readNotifyCh     chan struct{}
	readShutdown     bool
	outOfOrderChunks map[uint16][]byte // For storing out-of-order chunks

	// Write related
	writeLock      sync.Mutex
	nextWriteChunk uint16
	writeDeadline  time.Time
	writeShutdown  bool
	writeBuffer    []byte

	// Shared
	closeOnce sync.Once
	closeCh   chan struct{}
}

// newStream creates a new stream
func newStream(session *Session, id uint32) *Stream {
	s := &Stream{
		id:               id,
		session:          session,
		nextReadChunk:    0,
		readNotifyCh:     make(chan struct{}, 1),
		nextWriteChunk:   0,
		closeCh:          make(chan struct{}),
		outOfOrderChunks: make(map[uint16][]byte),
		readBuffer:       make([]byte, 0, session.config.ReadBufferSize),
		writeBuffer:      make([]byte, 0, session.config.WriteBufferSize),
	}
	return s
}

// StreamID returns the unique identifier for this stream
func (s *Stream) StreamID() uint32 {
	return s.id
}

// Read implements io.Reader
func (s *Stream) Read(b []byte) (n int, err error) {
	s.readLock.Lock()
	defer s.readLock.Unlock()

	if len(s.readBuffer) == 0 {
		if s.readShutdown {
			return 0, io.EOF
		}

		// Wait for data to arrive
		s.readLock.Unlock()

		// Check for deadline
		var timeout <-chan time.Time
		if !s.readDeadline.IsZero() {
			timeout = time.After(time.Until(s.readDeadline))
		}

		select {
		case <-s.readNotifyCh:
		case <-s.closeCh:
			s.readLock.Lock()
			return 0, io.EOF
		case <-timeout:
			s.readLock.Lock()
			return 0, ErrTimeout
		}

		s.readLock.Lock()

		// Double check if we have data now
		if len(s.readBuffer) == 0 {
			if s.readShutdown {
				return 0, io.EOF
			}
			return 0, nil
		}
	}

	// Copy data to the target buffer
	n = copy(b, s.readBuffer)
	s.readBuffer = s.readBuffer[n:]
	return n, nil
}

// Write implements io.Writer
func (s *Stream) Write(b []byte) (n int, err error) {
	s.writeLock.Lock()
	defer s.writeLock.Unlock()

	if s.writeShutdown {
		return 0, ErrConnectionClosed
	}

	// Check write deadline
	if !s.writeDeadline.IsZero() && time.Now().After(s.writeDeadline) {
		return 0, ErrTimeout
	}

	total := len(b)
	remaining := total
	sent := 0

	for remaining > 0 {
		// Determine chunk size
		chunkSize := int(s.session.config.MaxChunkSize)
		if remaining < chunkSize {
			chunkSize = remaining
		}

		// Apply the distribution policy
		var err error
		switch s.session.config.DistributionPolicy {
		case RoundRobin:
			// Default behavior already implemented in sendChunk
			err = s.session.sendChunk(s.id, s.nextWriteChunk, b[sent:sent+chunkSize])
		case LowestLatency:
			// Send through the connection with lowest latency
			err = s.session.sendChunkLowestLatency(s.id, s.nextWriteChunk, b[sent:sent+chunkSize])
		case HighestBandwidth:
			// Send through the connection with highest bandwidth
			err = s.session.sendChunkHighestBandwidth(s.id, s.nextWriteChunk, b[sent:sent+chunkSize])
		case WeightedRoundRobin:
			// Send through connections based on their weights
			err = s.session.sendChunkWeighted(s.id, s.nextWriteChunk, b[sent:sent+chunkSize])
		default:
			// Default to round robin
			err = s.session.sendChunk(s.id, s.nextWriteChunk, b[sent:sent+chunkSize])
		}

		if err != nil {
			return sent, err
		}

		// Update counters
		sent += chunkSize
		remaining -= chunkSize
		s.nextWriteChunk++
	}

	return total, nil
}

// receiveChunk adds a received chunk to the stream's read buffer
func (s *Stream) receiveChunk(chunkID uint16, data []byte) {
	s.readLock.Lock()
	defer s.readLock.Unlock()

	// Simple case: it's the next chunk we're expecting
	if chunkID == s.nextReadChunk {
		s.readBuffer = append(s.readBuffer, data...)
		s.nextReadChunk++

		// Check if we have any buffered out-of-order chunks that can now be processed
		if s.session.config.EnableOutOfOrderProcessing {
			for {
				nextData, exists := s.outOfOrderChunks[s.nextReadChunk]
				if !exists {
					break
				}

				// Add the chunk to the read buffer
				s.readBuffer = append(s.readBuffer, nextData...)
				delete(s.outOfOrderChunks, s.nextReadChunk)
				s.nextReadChunk++
			}
		}

		// Notify readers
		select {
		case s.readNotifyCh <- struct{}{}:
		default:
		}
		return
	}

	// Handle out-of-order chunks if enabled
	if s.session.config.EnableOutOfOrderProcessing {
		// Check if the chunk is within our window
		if chunkID > s.nextReadChunk &&
			chunkID < s.nextReadChunk+uint16(s.session.config.OutOfOrderWindowSize) {
			// Store the chunk for later processing
			s.outOfOrderChunks[chunkID] = data
		}
	}
	// Otherwise, we just drop out-of-order chunks
}

// Close closes the stream
func (s *Stream) Close() error {
	s.closeOnce.Do(func() {
		close(s.closeCh)
		s.readLock.Lock()
		s.readShutdown = true
		s.readLock.Unlock()

		s.writeLock.Lock()
		s.writeShutdown = true
		s.writeLock.Unlock()

		// Remove from session
		s.session.streamLock.Lock()
		delete(s.session.streams, s.id)
		s.session.streamLock.Unlock()
	})
	return nil
}

// SetDeadline implements net.Conn
func (s *Stream) SetDeadline(t time.Time) error {
	if err := s.SetReadDeadline(t); err != nil {
		return err
	}
	return s.SetWriteDeadline(t)
}

// SetReadDeadline implements net.Conn
func (s *Stream) SetReadDeadline(t time.Time) error {
	s.readLock.Lock()
	defer s.readLock.Unlock()
	s.readDeadline = t
	return nil
}

// SetWriteDeadline implements net.Conn
func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.writeLock.Lock()
	defer s.writeLock.Unlock()
	s.writeDeadline = t
	return nil
}

// LocalAddr returns the local address of the stream
// This is a placeholder implementation to satisfy the net.Conn interface
func (s *Stream) LocalAddr() net.Addr {
	return &invmuxAddr{s.id, "local"}
}

// RemoteAddr returns the remote address of the stream
// This is a placeholder implementation to satisfy the net.Conn interface
func (s *Stream) RemoteAddr() net.Addr {
	return &invmuxAddr{s.id, "remote"}
}

// invmuxAddr implements the net.Addr interface for a stream
type invmuxAddr struct {
	id   uint32
	side string
}

// Network returns the network name
func (a *invmuxAddr) Network() string {
	return "invmux"
}

// String returns a string representation of the address
func (a *invmuxAddr) String() string {
	return fmt.Sprintf("invmux:%s:%d", a.side, a.id)
}
