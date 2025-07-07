// Package invmux implements an inverse multiplexer with pluggable connection interfaces
package invmux

import (
	"context"
	"net"
	"time"
)

// ConnectionProvider defines the interface for creating and managing connections
type ConnectionProvider interface {
	// Name returns the name of this connection provider
	Name() string

	// Dial creates a new connection to the specified address
	Dial(ctx context.Context, address string) (PluggableConnection, error)

	// Listen starts listening for incoming connections
	Listen(ctx context.Context, address string) (ConnectionListener, error)

	// SupportsAddress checks if this provider can handle the given address
	SupportsAddress(address string) bool

	// HealthCheck performs a quick health check on the provider
	HealthCheck(ctx context.Context) error
}

// PluggableConnection extends net.Conn with additional metadata and capabilities
type PluggableConnection interface {
	net.Conn

	// Provider returns the connection provider that created this connection
	Provider() ConnectionProvider

	// Metadata returns connection-specific metadata
	Metadata() ConnectionMetadata

	// Quality returns current connection quality metrics
	Quality() ConnectionQuality

	// Priority returns the priority of this connection (higher = more preferred)
	Priority() int

	// SetPriority sets the priority of this connection
	SetPriority(priority int)

	// IsReliable indicates if this connection guarantees packet delivery
	IsReliable() bool

	// MaxMTU returns the maximum transmission unit for this connection
	MaxMTU() int
}

// ConnectionListener listens for incoming connections
type ConnectionListener interface {
	// Accept waits for and returns the next connection
	Accept() (PluggableConnection, error)

	// Close closes the listener
	Close() error

	// Addr returns the listener's network address
	Addr() net.Addr

	// Provider returns the connection provider that created this listener
	Provider() ConnectionProvider
}

// ConnectionMetadata contains metadata about a connection
type ConnectionMetadata struct {
	// Type describes the connection type (tcp, udp, dns, http, etc.)
	Type string

	// Protocol version or specific implementation details
	Protocol string

	// Remote address as known by the connection
	RemoteAddress string

	// Local address as known by the connection
	LocalAddress string

	// Capabilities of this connection
	Capabilities []string

	// Custom properties specific to the connection type
	Properties map[string]interface{}

	// Created timestamp
	Created time.Time
}

// ConnectionQuality represents the current quality metrics of a connection
type ConnectionQuality struct {
	// Latency measurements
	Latency         time.Duration
	LatencyVariance time.Duration

	// Bandwidth measurements (bytes per second)
	UploadBandwidth   int64
	DownloadBandwidth int64

	// Reliability metrics
	PacketLoss    float64 // 0.0 to 1.0
	ErrorRate     float64 // 0.0 to 1.0
	ConnectionAge time.Duration
	LastActivity  time.Time

	// Health status
	IsHealthy   bool
	HealthScore float64 // 0.0 to 1.0, higher is better

	// Connection-specific metrics
	CustomMetrics map[string]float64
}

// StreamMiddleware allows custom processing of stream data
type StreamMiddleware interface {
	// Name returns the middleware name
	Name() string

	// ProcessWrite processes data being written to a stream
	ProcessWrite(streamID uint32, data []byte) ([]byte, error)

	// ProcessRead processes data being read from a stream
	ProcessRead(streamID uint32, data []byte) ([]byte, error)

	// OnStreamOpen is called when a new stream is opened
	OnStreamOpen(streamID uint32) error

	// OnStreamClose is called when a stream is closed
	OnStreamClose(streamID uint32) error
}

// ConnectionBalancer defines strategies for selecting connections
type ConnectionBalancer interface {
	// Name returns the balancer name
	Name() string

	// SelectConnection chooses the best connection for sending data
	SelectConnection(connections []PluggableConnection, data []byte) (PluggableConnection, error)

	// UpdateStats updates the balancer with connection statistics
	UpdateStats(connID string, quality ConnectionQuality)

	// OnConnectionAdded is called when a new connection is added
	OnConnectionAdded(conn PluggableConnection)

	// OnConnectionRemoved is called when a connection is removed
	OnConnectionRemoved(conn PluggableConnection)
}

// ErrorHandler handles connection and session errors
type ErrorHandler interface {
	// HandleConnectionError handles connection-specific errors
	HandleConnectionError(conn PluggableConnection, err error) ErrorAction

	// HandleSessionError handles session-level errors
	HandleSessionError(session *EnhancedSession, err error) ErrorAction

	// HandleStreamError handles stream-specific errors
	HandleStreamError(stream *EnhancedStream, err error) ErrorAction
}

// ErrorAction defines what action to take when an error occurs
type ErrorAction int

const (
	ErrorActionIgnore ErrorAction = iota
	ErrorActionRetry
	ErrorActionRemoveConnection
	ErrorActionReconnect
	ErrorActionFailover
	ErrorActionShutdown
)

// HealthMonitor monitors connection health and triggers recovery actions
type HealthMonitor interface {
	// StartMonitoring begins health monitoring for the session
	StartMonitoring(session *EnhancedSession) error

	// StopMonitoring stops health monitoring
	StopMonitoring() error

	// CheckConnection performs a health check on a specific connection
	CheckConnection(conn PluggableConnection) ConnectionQuality

	// SetHealthThresholds sets the thresholds for determining connection health
	SetHealthThresholds(thresholds HealthThresholds)
}

// HealthThresholds defines the thresholds for connection health
type HealthThresholds struct {
	MaxLatency          time.Duration
	MinBandwidth        int64
	MaxPacketLoss       float64
	MaxErrorRate        float64
	MinHealthScore      float64
	MaxIdleTime         time.Duration
	HealthCheckInterval time.Duration
}

// ConnectionPool manages a pool of connections with automatic healing
type ConnectionPool interface {
	// AddProvider registers a connection provider
	AddProvider(provider ConnectionProvider) error

	// RemoveProvider unregisters a connection provider
	RemoveProvider(name string) error

	// CreateConnection creates a new connection using the best available provider
	CreateConnection(ctx context.Context, address string) (PluggableConnection, error)

	// GetHealthyConnections returns all healthy connections
	GetHealthyConnections() []PluggableConnection

	// GetConnectionsByType returns connections of a specific type
	GetConnectionsByType(connType string) []PluggableConnection

	// SetAutoHealing enables/disables automatic connection healing
	SetAutoHealing(enabled bool)

	// StartAutoHealing starts the auto-healing process
	StartAutoHealing(ctx context.Context) error
}
