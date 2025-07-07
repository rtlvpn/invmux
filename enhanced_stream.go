package invmux

import (
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// EnhancedStream represents an enhanced bidirectional stream with advanced features
type EnhancedStream struct {
	id      uint32
	session *EnhancedSession
	
	// Read related
	readBuffer       []byte
	readLock         sync.Mutex
	nextReadChunk    uint16
	readDeadline     time.Time
	readNotifyCh     chan struct{}
	readShutdown     bool
	outOfOrderChunks map[uint16][]byte
	
	// Write related
	writeLock      sync.Mutex
	nextWriteChunk uint16
	writeDeadline  time.Time
	writeShutdown  bool
	writeBuffer    []byte
	
	// Enhanced features
	priority       int
	qosClass       string
	metadata       map[string]interface{}
	
	// Metrics
	bytesRead      int64
	bytesWritten   int64
	chunksRead     int64
	chunksWritten  int64
	created        time.Time
	lastActivity   time.Time
	
	// Shared
	closeOnce sync.Once
	closeCh   chan struct{}
	closed    bool
	mu        sync.RWMutex
}

// newEnhancedStream creates a new enhanced stream
func newEnhancedStream(session *EnhancedSession, id uint32) *EnhancedStream {
	s := &EnhancedStream{
		id:               id,
		session:          session,
		nextReadChunk:    0,
		readNotifyCh:     make(chan struct{}, 1),
		nextWriteChunk:   0,
		closeCh:          make(chan struct{}),
		outOfOrderChunks: make(map[uint16][]byte),
		readBuffer:       make([]byte, 0, session.config.ReadBufferSize),
		writeBuffer:      make([]byte, 0, session.config.WriteBufferSize),
		priority:         100, // Default priority
		qosClass:         "default",
		metadata:         make(map[string]interface{}),
		created:          time.Now(),
		lastActivity:     time.Now(),
	}
	return s
}

// StreamID returns the unique identifier for this stream
func (s *EnhancedStream) StreamID() uint32 {
	return s.id
}

// Priority returns the stream priority
func (s *EnhancedStream) Priority() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priority
}

// SetPriority sets the stream priority
func (s *EnhancedStream) SetPriority(priority int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.priority = priority
}

// QoSClass returns the Quality of Service class
func (s *EnhancedStream) QoSClass() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.qosClass
}

// SetQoSClass sets the Quality of Service class
func (s *EnhancedStream) SetQoSClass(class string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.qosClass = class
}

// SetMetadata sets metadata for the stream
func (s *EnhancedStream) SetMetadata(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metadata[key] = value
}

// GetMetadata gets metadata for the stream
func (s *EnhancedStream) GetMetadata(key string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, exists := s.metadata[key]
	return value, exists
}

// GetMetrics returns stream metrics
func (s *EnhancedStream) GetMetrics() StreamMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	return StreamMetrics{
		StreamID:      s.id,
		BytesRead:     atomic.LoadInt64(&s.bytesRead),
		BytesWritten:  atomic.LoadInt64(&s.bytesWritten),
		ChunksRead:    atomic.LoadInt64(&s.chunksRead),
		ChunksWritten: atomic.LoadInt64(&s.chunksWritten),
		Created:       s.created,
		LastActivity:  s.lastActivity,
		Priority:      s.priority,
		QoSClass:      s.qosClass,
	}
}

// StreamMetrics contains metrics for a stream
type StreamMetrics struct {
	StreamID      uint32
	BytesRead     int64
	BytesWritten  int64
	ChunksRead    int64
	ChunksWritten int64
	Created       time.Time
	LastActivity  time.Time
	Priority      int
	QoSClass      string
}

// Read implements io.Reader with enhanced features
func (s *EnhancedStream) Read(b []byte) (n int, err error) {
	s.readLock.Lock()
	defer s.readLock.Unlock()
	
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return 0, io.EOF
	}
	s.mu.RUnlock()
	
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
		case <-s.session.ctx.Done():
			s.readLock.Lock()
			return 0, s.session.ctx.Err()
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
	
	// Update metrics
	atomic.AddInt64(&s.bytesRead, int64(n))
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
	
	return n, nil
}

// Write implements io.Writer with enhanced features
func (s *EnhancedStream) Write(b []byte) (n int, err error) {
	s.writeLock.Lock()
	defer s.writeLock.Unlock()
	
	s.mu.RLock()
	if s.closed || s.writeShutdown {
		s.mu.RUnlock()
		return 0, ErrConnectionClosed
	}
	s.mu.RUnlock()
	
	// Check write deadline
	if !s.writeDeadline.IsZero() && time.Now().After(s.writeDeadline) {
		return 0, ErrTimeout
	}
	
	total := len(b)
	remaining := total
	sent := 0
	
	for remaining > 0 {
		// Determine chunk size based on connection capabilities
		chunkSize := int(s.session.config.MaxChunkSize)
		if remaining < chunkSize {
			chunkSize = remaining
		}
		
		// Send the chunk through the session
		err = s.session.SendChunk(s.id, s.nextWriteChunk, b[sent:sent+chunkSize])
		if err != nil {
			return sent, err
		}
		
		// Update counters
		sent += chunkSize
		remaining -= chunkSize
		s.nextWriteChunk++
		
		// Update metrics
		atomic.AddInt64(&s.chunksWritten, 1)
	}
	
	// Update metrics
	atomic.AddInt64(&s.bytesWritten, int64(total))
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
	
	return total, nil
}

// receiveChunk adds a received chunk to the stream's read buffer
func (s *EnhancedStream) receiveChunk(chunkID uint16, data []byte) {
	s.readLock.Lock()
	defer s.readLock.Unlock()
	
	// Update metrics
	atomic.AddInt64(&s.chunksRead, 1)
	
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
				atomic.AddInt64(&s.chunksRead, 1)
			}
		}
		
		// Update activity
		s.mu.Lock()
		s.lastActivity = time.Now()
		s.mu.Unlock()
		
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
func (s *EnhancedStream) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		
		close(s.closeCh)
		
		s.readLock.Lock()
		s.readShutdown = true
		s.readLock.Unlock()
		
		s.writeLock.Lock()
		s.writeShutdown = true
		s.writeLock.Unlock()
		
		// Notify middleware about stream closure
		for _, middleware := range s.session.middleware {
			if err := middleware.OnStreamClose(s.id); err != nil {
				// Log error but don't prevent closure
			}
		}
		
		// Remove from session
		s.session.streamLock.Lock()
		delete(s.session.streams, s.id)
		s.session.streamLock.Unlock()
		
		// Update metrics
		atomic.AddInt64(&s.session.metrics.StreamsActive, -1)
		atomic.AddInt64(&s.session.metrics.StreamsClosed, 1)
	})
	return nil
}

// SetDeadline implements net.Conn
func (s *EnhancedStream) SetDeadline(t time.Time) error {
	if err := s.SetReadDeadline(t); err != nil {
		return err
	}
	return s.SetWriteDeadline(t)
}

// SetReadDeadline implements net.Conn
func (s *EnhancedStream) SetReadDeadline(t time.Time) error {
	s.readLock.Lock()
	defer s.readLock.Unlock()
	s.readDeadline = t
	return nil
}

// SetWriteDeadline implements net.Conn
func (s *EnhancedStream) SetWriteDeadline(t time.Time) error {
	s.writeLock.Lock()
	defer s.writeLock.Unlock()
	s.writeDeadline = t
	return nil
}

// LocalAddr returns the local address of the stream
func (s *EnhancedStream) LocalAddr() net.Addr {
	return &enhancedStreamAddr{s.id, "local"}
}

// RemoteAddr returns the remote address of the stream
func (s *EnhancedStream) RemoteAddr() net.Addr {
	return &enhancedStreamAddr{s.id, "remote"}
}

// IsClosed returns whether the stream is closed
func (s *EnhancedStream) IsClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

// enhancedStreamAddr implements the net.Addr interface for enhanced streams
type enhancedStreamAddr struct {
	id   uint32
	side string
}

// Network returns the network name
func (a *enhancedStreamAddr) Network() string {
	return "invmux-enhanced"
}

// String returns a string representation of the address
func (a *enhancedStreamAddr) String() string {
	return fmt.Sprintf("invmux-enhanced:%s:%d", a.side, a.id)
}