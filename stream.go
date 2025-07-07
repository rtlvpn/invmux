package invmux

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrStreamClosed = errors.New("stream closed")
	ErrWriteTimeout = errors.New("write timeout")
	ErrReadTimeout  = errors.New("read timeout")
)

// stream implements the Stream interface
type stream struct {
	session *session
	id      uint32

	// Buffer management
	readBuffer  []byte
	readPos     int
	readCond    *sync.Cond
	readMu      sync.Mutex

	writeBuffer []byte
	writeMu     sync.Mutex

	// Chunk management
	nextWriteChunk uint16
	receivedChunks map[uint16][]byte
	nextReadChunk  uint16
	chunkMu        sync.Mutex

	// State management
	closed    int32
	closeOnce sync.Once
	closeCh   chan struct{}

	// Deadlines
	readDeadline  time.Time
	writeDeadline time.Time
	deadlineMu    sync.RWMutex

	// Statistics
	bytesRead    int64
	bytesWritten int64
}

// newStream creates a new stream
func newStream(session *session, id uint32) *stream {
	s := &stream{
		session:        session,
		id:             id,
		readBuffer:     make([]byte, 0, DefaultBufferSize),
		writeBuffer:    make([]byte, 0, DefaultBufferSize),
		receivedChunks: make(map[uint16][]byte),
		closeCh:        make(chan struct{}),
	}

	s.readCond = sync.NewCond(&s.readMu)
	return s
}

// ID returns the stream ID
func (s *stream) ID() uint32 {
	return s.id
}

// Session returns the parent session
func (s *stream) Session() Session {
	return s.session
}

// Read reads data from the stream
func (s *stream) Read(b []byte) (n int, err error) {
	if s.isClosed() {
		return 0, ErrStreamClosed
	}

	s.readMu.Lock()
	defer s.readMu.Unlock()

	// Check deadline
	s.deadlineMu.RLock()
	deadline := s.readDeadline
	s.deadlineMu.RUnlock()

	for {
		// Check if we have data in the buffer
		if s.readPos < len(s.readBuffer) {
			available := len(s.readBuffer) - s.readPos
			if len(b) <= available {
				// Can satisfy the entire request
				copy(b, s.readBuffer[s.readPos:s.readPos+len(b)])
				s.readPos += len(b)
				atomic.AddInt64(&s.bytesRead, int64(len(b)))
				return len(b), nil
			} else {
				// Partial read
				copy(b, s.readBuffer[s.readPos:])
				n = available
				s.readPos = len(s.readBuffer)
				atomic.AddInt64(&s.bytesRead, int64(n))
				return n, nil
			}
		}

		// No data available, check if stream is closed
		if s.isClosed() {
			return 0, io.EOF
		}

		// Check deadline
		if !deadline.IsZero() && time.Now().After(deadline) {
			return 0, ErrReadTimeout
		}

		// Wait for data
		if !deadline.IsZero() {
			// Wait with timeout
			done := make(chan struct{})
			go func() {
				s.readCond.Wait()
				close(done)
			}()

			select {
			case <-done:
				// Continue to check for data
			case <-time.After(time.Until(deadline)):
				return 0, ErrReadTimeout
			case <-s.closeCh:
				return 0, ErrStreamClosed
			}
		} else {
			// Wait indefinitely
			s.readCond.Wait()
		}
	}
}

// Write writes data to the stream
func (s *stream) Write(b []byte) (n int, err error) {
	if s.isClosed() {
		return 0, ErrStreamClosed
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// Check deadline
	s.deadlineMu.RLock()
	deadline := s.writeDeadline
	s.deadlineMu.RUnlock()

	if !deadline.IsZero() && time.Now().After(deadline) {
		return 0, ErrWriteTimeout
	}

	// Split data into chunks and send
	maxChunkSize := int(s.session.config.MaxChunkSize)
	totalWritten := 0

	for totalWritten < len(b) {
		chunkSize := len(b) - totalWritten
		if chunkSize > maxChunkSize {
			chunkSize = maxChunkSize
		}

		chunk := b[totalWritten : totalWritten+chunkSize]

		// Get next chunk ID
		chunkID := atomic.AddUint32((*uint32)(&s.nextWriteChunk), 1)

		// Send chunk through session
		err := s.session.sendData(s.id, uint16(chunkID), chunk)
		if err != nil {
			if totalWritten == 0 {
				return 0, err
			}
			return totalWritten, err
		}

		totalWritten += chunkSize

		// Check deadline during multi-chunk writes
		if !deadline.IsZero() && time.Now().After(deadline) {
			if totalWritten == 0 {
				return 0, ErrWriteTimeout
			}
			return totalWritten, ErrWriteTimeout
		}
	}

	atomic.AddInt64(&s.bytesWritten, int64(totalWritten))
	return totalWritten, nil
}

// Close closes the stream
func (s *stream) Close() error {
	s.closeOnce.Do(func() {
		atomic.StoreInt32(&s.closed, 1)
		close(s.closeCh)

		// Notify waiting readers
		s.readCond.Broadcast()

		// Remove from session
		s.session.removeStream(s.id)
	})

	return nil
}

// receiveChunk processes a received data chunk
func (s *stream) receiveChunk(chunkID uint16, data []byte) {
	if s.isClosed() {
		return
	}

	s.chunkMu.Lock()
	defer s.chunkMu.Unlock()

	// Store the chunk
	s.receivedChunks[chunkID] = data

	// Try to add consecutive chunks to the read buffer
	s.readMu.Lock()
	defer s.readMu.Unlock()

	for {
		chunkData, exists := s.receivedChunks[s.nextReadChunk]
		if !exists {
			break
		}

		// Add chunk to read buffer
		s.readBuffer = append(s.readBuffer, chunkData...)
		delete(s.receivedChunks, s.nextReadChunk)
		s.nextReadChunk++

		// Notify readers
		s.readCond.Broadcast()
	}
}

// SetDeadline sets read and write deadlines
func (s *stream) SetDeadline(t time.Time) error {
	s.deadlineMu.Lock()
	defer s.deadlineMu.Unlock()
	s.readDeadline = t
	s.writeDeadline = t
	return nil
}

// SetReadDeadline sets the read deadline
func (s *stream) SetReadDeadline(t time.Time) error {
	s.deadlineMu.Lock()
	defer s.deadlineMu.Unlock()
	s.readDeadline = t
	return nil
}

// SetWriteDeadline sets the write deadline
func (s *stream) SetWriteDeadline(t time.Time) error {
	s.deadlineMu.Lock()
	defer s.deadlineMu.Unlock()
	s.writeDeadline = t
	return nil
}

// isClosed returns true if the stream is closed
func (s *stream) isClosed() bool {
	return atomic.LoadInt32(&s.closed) == 1
}

// Stats returns stream statistics
func (s *stream) Stats() StreamStats {
	return StreamStats{
		ID:           s.id,
		BytesRead:    atomic.LoadInt64(&s.bytesRead),
		BytesWritten: atomic.LoadInt64(&s.bytesWritten),
		IsClosed:     s.isClosed(),
	}
}

// StreamStats contains stream statistics
type StreamStats struct {
	ID           uint32
	BytesRead    int64
	BytesWritten int64
	IsClosed     bool
}
