package invmux

import (
	"encoding/binary"
	"io"
	"net"
	"sync"

	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	E "github.com/sagernet/sing/common/exceptions"
)

const (
	ProtocolInvMux = iota + 3 // Assign a new protocol ID
	maxStreamID    = 0xFFFFFFFF
	headerSize     = 6 // 4 bytes for stream ID, 2 bytes for data length
)

type invMuxSession struct {
	conn        net.Conn
	streams     map[uint32]net.Conn
	streamID    uint32
	mutex       sync.Mutex
	acceptQueue chan net.Conn
	closed      bool
}

func (s *invMuxSession) Open() (net.Conn, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.closed {
		return nil, E.New("session closed")
	}

	stream, err := s.openStream()
	if err != nil {
		return nil, err
	}

	return stream, nil
}

func (s *invMuxSession) Accept() (net.Conn, error) {
	if s.acceptQueue == nil {
		s.acceptQueue = make(chan net.Conn, 1024)
		go s.handleIncomingStreams()
	}

	return <-s.acceptQueue, nil
}

func (s *invMuxSession) NumStreams() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return len(s.streams)
}

func (s *invMuxSession) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	for _, stream := range s.streams {
		stream.Close()
	}

	if s.acceptQueue != nil {
		close(s.acceptQueue)
	}

	return s.conn.Close()
}

func (s *invMuxSession) IsClosed() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.closed
}

func (s *invMuxSession) CanTakeNewRequest() bool {
	return true
}

func (s *invMuxSession) openStream() (net.Conn, error) {
	s.streamID++
	if s.streamID > maxStreamID {
		return nil, E.New("maximum stream ID reached")
	}

	stream := &invMuxStream{
		session:  s,
		streamID: s.streamID,
		conn:     s.conn,
	}
	s.streams[s.streamID] = stream
	return stream, nil
}

func (s *invMuxSession) handleIncomingStreams() {
	buffer := buf.NewSize(headerSize)
	for {
		if _, err := io.ReadFull(s.conn, buffer.Extend(headerSize)); err != nil {
			if s.IsClosed() {
				return
			}
			// Handle error
			continue
		}

		streamID := binary.BigEndian.Uint32(buffer.Bytes())
		dataLen := binary.BigEndian.Uint16(buffer.Bytes()[4:])

		if dataLen > 0 {
			data := buf.NewSize(int(dataLen))
			if _, err := io.ReadFull(s.conn, data.Extend(int(dataLen))); err != nil {
				// Handle error
				continue
			}

			stream, ok := s.streams[streamID]
			if !ok {
				stream = &invMuxStream{
					session:  s,
					streamID: streamID,
					conn:     s.conn,
				}
				s.streams[streamID] = stream
			}

			_, err := stream.Write(data.Bytes())
			if err != nil {
				// Handle error
			}
		} else {
			s.acceptQueue <- &invMuxStream{
				session:  s,
				streamID: streamID,
				conn:     s.conn,
			}
		}

		buffer.Reset()
	}
}

type invMuxStream struct {
	session  *invMuxSession
	streamID uint32
	conn     net.Conn
	buffer   *buf.Buffer
}

func (s *invMuxStream) Read(p []byte) (int, error) {
	if s.buffer == nil {
		s.buffer = buf.New()
	}

	n, err := s.buffer.Read(p)
	if err != nil {
		return n, err
	}

	if s.buffer.Len() == 0 {
		header := buf.NewSize(headerSize)
		if _, err := io.ReadFull(s.conn, header.Extend(headerSize)); err != nil {
			return 0, err
		}

		streamID := binary.BigEndian.Uint32(header.Bytes())
		if streamID != s.streamID {
			return 0, E.New("invalid stream ID")
		}

		dataLen := binary.BigEndian.Uint16(header.Bytes()[4:])
		if dataLen > 0 {
			data := buf.NewSize(int(dataLen))
			if _, err := io.ReadFull(s.conn, data.Extend(int(dataLen))); err != nil {
				return 0, err
			}
			s.buffer.Write(data.Bytes())
		}
	}

	return s.buffer.Read(p)
}

func (s *invMuxStream) Write(p []byte) (int, error) {
	header := buf.NewSize(headerSize)
	common.Must(binary.Write(header, binary.BigEndian, s.streamID))
	common.Must(binary.Write(header, binary.BigEndian, uint16(len(p))))
	header.Write(p)
	_, err := s.conn.Write(header.Bytes())
	return len(p), err
}

func (s *invMuxStream) Close() error {
	s.session.mutex.Lock()
	defer s.session.mutex.Unlock()
	delete(s.session.streams, s.streamID)
	return nil
}

func (s *invMuxStream) Upstream() any {
	return s.conn
}

func newInvMuxClient(conn net.Conn) (abstractSession, error) {
	return &invMuxSession{
		conn:    conn,
		streams: make(map[uint32]net.Conn),
	}, nil
}

func newInvMuxServer(conn net.Conn) (abstractSession, error) {
	return newInvMuxClient(conn)
}
