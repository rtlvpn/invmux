// Package invmux implements an inverse multiplexer with pluggable connection interfaces
package invmux

import (
	"context"
	"io"
	"time"
)

// Connection represents an abstract connection that can carry data
// This is the core abstraction that makes the system transport-agnostic
type Connection interface {
	io.ReadWriteCloser
	
	// Quality returns current connection quality metrics
	Quality() *Quality
	
	// Priority returns the priority of this connection (higher = more preferred)
	Priority() int
	
	// SetPriority sets the priority of this connection
	SetPriority(priority int)
	
	// ID returns a unique identifier for this connection
	ID() string
	
	// Type returns the connection type (for informational purposes only)
	Type() string
}

// ConnectionFactory creates new connections
type ConnectionFactory interface {
	// Name returns the factory name
	Name() string
	
	// Dial creates a new connection
	Dial(ctx context.Context, address string) (Connection, error)
	
	// Listen creates a listener for incoming connections
	Listen(ctx context.Context, address string) (Listener, error)
	
	// CanHandle returns true if this factory can handle the given address
	CanHandle(address string) bool
}

// Listener accepts incoming connections
type Listener interface {
	// Accept waits for and returns the next connection
	Accept() (Connection, error)
	
	// Close closes the listener
	Close() error
	
	// Addr returns the listener's address as a string
	Addr() string
}

// Quality represents connection quality metrics
type Quality struct {
	// Latency measurements
	Latency         time.Duration
	Jitter          time.Duration
	
	// Throughput measurements
	Bandwidth       int64 // bytes per second
	
	// Reliability metrics
	PacketLoss      float64 // 0.0 to 1.0
	ErrorRate       float64 // 0.0 to 1.0
	
	// Health and activity
	IsHealthy       bool
	LastActivity    time.Time
	
	// Overall score (0.0 to 1.0, higher is better)
	Score           float64
}

// Middleware processes data flowing through streams
type Middleware interface {
	// Name returns the middleware name
	Name() string
	
	// ProcessOutbound processes data being sent
	ProcessOutbound(streamID uint32, data []byte) ([]byte, error)
	
	// ProcessInbound processes data being received
	ProcessInbound(streamID uint32, data []byte) ([]byte, error)
	
	// OnStreamOpen is called when a new stream is opened
	OnStreamOpen(streamID uint32) error
	
	// OnStreamClose is called when a stream is closed
	OnStreamClose(streamID uint32) error
}

// LoadBalancer selects connections for sending data
type LoadBalancer interface {
	// Name returns the balancer name
	Name() string
	
	// Select chooses the best connection for sending data
	Select(connections []Connection, streamID uint32, data []byte) (Connection, error)
	
	// OnConnectionAdded is called when a new connection is added
	OnConnectionAdded(conn Connection)
	
	// OnConnectionRemoved is called when a connection is removed
	OnConnectionRemoved(conn Connection)
	
	// UpdateStats updates the balancer with connection statistics
	UpdateStats(conn Connection, quality *Quality)
}

// ErrorHandler handles various types of errors
type ErrorHandler interface {
	// HandleConnectionError handles connection-specific errors
	HandleConnectionError(conn Connection, err error) ErrorAction
	
	// HandleStreamError handles stream-specific errors
	HandleStreamError(streamID uint32, err error) ErrorAction
	
	// HandleSessionError handles session-level errors
	HandleSessionError(err error) ErrorAction
}

// ErrorAction defines what action to take when an error occurs
type ErrorAction int

const (
	ErrorActionIgnore ErrorAction = iota
	ErrorActionRetry
	ErrorActionReconnect
	ErrorActionFailover
	ErrorActionShutdown
)

// HealthMonitor monitors connection health
type HealthMonitor interface {
	// StartMonitoring begins health monitoring
	StartMonitoring(ctx context.Context) error
	
	// StopMonitoring stops health monitoring
	StopMonitoring() error
	
	// CheckConnection performs a health check on a connection
	CheckConnection(conn Connection) *Quality
	
	// SetThresholds sets the health check thresholds
	SetThresholds(thresholds *HealthThresholds)
	
	// OnUnhealthyConnection is called when a connection becomes unhealthy
	OnUnhealthyConnection(conn Connection)
}

// HealthThresholds defines the thresholds for connection health
type HealthThresholds struct {
	MaxLatency        time.Duration
	MinBandwidth      int64
	MaxPacketLoss     float64
	MaxErrorRate      float64
	MinScore          float64
	MaxIdleTime       time.Duration
	CheckInterval     time.Duration
}

// ConnectionManager manages the lifecycle of connections
type ConnectionManager interface {
	// AddConnection adds a connection to be managed
	AddConnection(conn Connection) error
	
	// RemoveConnection removes a connection from management
	RemoveConnection(connID string) error
	
	// GetConnection returns a connection by ID
	GetConnection(connID string) (Connection, bool)
	
	// GetHealthyConnections returns all healthy connections
	GetHealthyConnections() []Connection
	
	// GetAllConnections returns all connections
	GetAllConnections() []Connection
	
	// SetAutoReconnect enables/disables automatic reconnection
	SetAutoReconnect(enabled bool)
}

// Session represents a multiplexing session
type Session interface {
	// OpenStream creates a new outbound stream
	OpenStream() (Stream, error)
	
	// AcceptStream waits for and returns an incoming stream
	AcceptStream() (Stream, error)
	
	// AddConnection adds a connection to the session
	AddConnection(conn Connection) error
	
	// RemoveConnection removes a connection from the session
	RemoveConnection(connID string) error
	
	// Close closes the session and all streams
	Close() error
	
	// IsClosed returns true if the session is closed
	IsClosed() bool
	
	// NumStreams returns the number of active streams
	NumStreams() int
	
	// NumConnections returns the number of active connections
	NumConnections() int
}

// Stream represents a logical stream within a session
type Stream interface {
	io.ReadWriteCloser
	
	// ID returns the stream ID
	ID() uint32
	
	// Session returns the parent session
	Session() Session
	
	// SetDeadline sets read and write deadlines
	SetDeadline(t time.Time) error
	
	// SetReadDeadline sets the read deadline
	SetReadDeadline(t time.Time) error
	
	// SetWriteDeadline sets the write deadline
	SetWriteDeadline(t time.Time) error
}
