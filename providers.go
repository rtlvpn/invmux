package invmux

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// StandardConnectionFactory provides basic network connections
type StandardConnectionFactory struct {
	name    string
	network string
	timeout time.Duration
}

// NewTCPFactory creates a TCP connection factory
func NewTCPFactory(timeout time.Duration) *StandardConnectionFactory {
	return &StandardConnectionFactory{
		name:    "tcp",
		network: "tcp",
		timeout: timeout,
	}
}

// NewUDPFactory creates a UDP connection factory
func NewUDPFactory(timeout time.Duration) *StandardConnectionFactory {
	return &StandardConnectionFactory{
		name:    "udp", 
		network: "udp",
		timeout: timeout,
	}
}

func (f *StandardConnectionFactory) Name() string {
	return f.name
}

func (f *StandardConnectionFactory) CanHandle(address string) bool {
	// Handle scheme-prefixed addresses
	if strings.HasPrefix(address, f.network+"://") {
		return true
	}
	
	// Handle plain addresses for TCP (default)
	if f.network == "tcp" && !strings.Contains(address, "://") && strings.Contains(address, ":") {
		return true
	}
	
	return false
}

func (f *StandardConnectionFactory) Dial(ctx context.Context, address string) (Connection, error) {
	// Strip scheme prefix if present
	address = strings.TrimPrefix(address, f.network+"://")
	
	dialer := &net.Dialer{Timeout: f.timeout}
	conn, err := dialer.DialContext(ctx, f.network, address)
	if err != nil {
		return nil, err
	}
	
	return newStandardConnection(conn, f), nil
}

func (f *StandardConnectionFactory) Listen(ctx context.Context, address string) (Listener, error) {
	// Strip scheme prefix if present
	address = strings.TrimPrefix(address, f.network+"://")
	
	listener, err := net.Listen(f.network, address)
	if err != nil {
		return nil, err
	}
	
	return &standardListener{
		Listener: listener,
		factory:  f,
	}, nil
}

// standardConnection wraps a net.Conn to implement our Connection interface
type standardConnection struct {
	net.Conn
	factory  *StandardConnectionFactory
	id       string
	connType string
	priority int
	quality  *Quality
	mu       sync.RWMutex
}

func newStandardConnection(conn net.Conn, factory *StandardConnectionFactory) *standardConnection {
	id := fmt.Sprintf("%s-%s-%s-%d", 
		factory.name, 
		conn.LocalAddr().String(), 
		conn.RemoteAddr().String(),
		time.Now().UnixNano())
	
	return &standardConnection{
		Conn:     conn,
		factory:  factory,
		id:       id,
		connType: factory.name,
		priority: 100, // Default priority
		quality: &Quality{
			IsHealthy:    true,
			Score:        1.0,
			LastActivity: time.Now(),
		},
	}
}

func (c *standardConnection) Quality() *Quality {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.quality
}

func (c *standardConnection) Priority() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.priority
}

func (c *standardConnection) SetPriority(priority int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.priority = priority
}

func (c *standardConnection) ID() string {
	return c.id
}

func (c *standardConnection) Type() string {
	return c.connType
}

func (c *standardConnection) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if err == nil {
		c.mu.Lock()
		c.quality.LastActivity = time.Now()
		c.mu.Unlock()
	}
	return
}

func (c *standardConnection) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if err == nil {
		c.mu.Lock()
		c.quality.LastActivity = time.Now()
		c.mu.Unlock()
	}
	return
}

// standardListener wraps a net.Listener to implement our Listener interface
type standardListener struct {
	net.Listener
	factory *StandardConnectionFactory
}

func (l *standardListener) Accept() (Connection, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	
	return newStandardConnection(conn, l.factory), nil
}

func (l *standardListener) Addr() string {
	return l.Listener.Addr().String()
}

// ConnectionRegistry manages multiple connection factories
type ConnectionRegistry struct {
	factories []ConnectionFactory
	mu        sync.RWMutex
}

// NewConnectionRegistry creates a new connection registry
func NewConnectionRegistry() *ConnectionRegistry {
	return &ConnectionRegistry{
		factories: make([]ConnectionFactory, 0),
	}
}

// Register adds a connection factory to the registry
func (r *ConnectionRegistry) Register(factory ConnectionFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories = append(r.factories, factory)
}

// Dial creates a connection using the first factory that can handle the address
func (r *ConnectionRegistry) Dial(ctx context.Context, address string) (Connection, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	for _, factory := range r.factories {
		if factory.CanHandle(address) {
			return factory.Dial(ctx, address)
		}
	}
	
	return nil, fmt.Errorf("no factory can handle address: %s", address)
}

// Listen creates a listener using the first factory that can handle the address
func (r *ConnectionRegistry) Listen(ctx context.Context, address string) (Listener, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	for _, factory := range r.factories {
		if factory.CanHandle(address) {
			return factory.Listen(ctx, address)
		}
	}
	
	return nil, fmt.Errorf("no factory can handle address: %s", address)
}

// GetFactories returns all registered factories
func (r *ConnectionRegistry) GetFactories() []ConnectionFactory {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]ConnectionFactory, len(r.factories))
	copy(result, r.factories)
	return result
}

// DefaultRegistry returns a registry with standard TCP and UDP factories
func DefaultRegistry() *ConnectionRegistry {
	registry := NewConnectionRegistry()
	registry.Register(NewTCPFactory(10 * time.Second))
	registry.Register(NewUDPFactory(10 * time.Second))
	return registry
}
