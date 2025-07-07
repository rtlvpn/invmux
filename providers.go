package invmux

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Standard connection providers

// TCPProvider implements TCP connections
type TCPProvider struct {
	name    string
	timeout time.Duration
}

// NewTCPProvider creates a new TCP connection provider
func NewTCPProvider(timeout time.Duration) *TCPProvider {
	return &TCPProvider{
		name:    "tcp",
		timeout: timeout,
	}
}

func (p *TCPProvider) Name() string {
	return p.name
}

func (p *TCPProvider) SupportsAddress(address string) bool {
	return strings.HasPrefix(address, "tcp://") || (!strings.Contains(address, "://") && strings.Contains(address, ":"))
}

func (p *TCPProvider) Dial(ctx context.Context, address string) (PluggableConnection, error) {
	address = strings.TrimPrefix(address, "tcp://")

	dialer := &net.Dialer{Timeout: p.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}

	return &StandardConnection{
		Conn:     conn,
		provider: p,
		metadata: ConnectionMetadata{
			Type:          "tcp",
			Protocol:      "tcp",
			RemoteAddress: conn.RemoteAddr().String(),
			LocalAddress:  conn.LocalAddr().String(),
			Capabilities:  []string{"reliable", "bidirectional", "stream"},
			Properties:    make(map[string]interface{}),
			Created:       time.Now(),
		},
		quality: ConnectionQuality{
			IsHealthy:     true,
			HealthScore:   1.0,
			LastActivity:  time.Now(),
			CustomMetrics: make(map[string]float64),
		},
		priority: 100,  // High priority for reliable connections
		mtu:      1500, // Standard Ethernet MTU
	}, nil
}

func (p *TCPProvider) Listen(ctx context.Context, address string) (ConnectionListener, error) {
	address = strings.TrimPrefix(address, "tcp://")

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}

	return &StandardListener{
		Listener: listener,
		provider: p,
	}, nil
}

func (p *TCPProvider) HealthCheck(ctx context.Context) error {
	// TCP provider is always healthy
	return nil
}

// UDPProvider implements UDP connections
type UDPProvider struct {
	name    string
	timeout time.Duration
}

// NewUDPProvider creates a new UDP connection provider
func NewUDPProvider(timeout time.Duration) *UDPProvider {
	return &UDPProvider{
		name:    "udp",
		timeout: timeout,
	}
}

func (p *UDPProvider) Name() string {
	return p.name
}

func (p *UDPProvider) SupportsAddress(address string) bool {
	return strings.HasPrefix(address, "udp://")
}

func (p *UDPProvider) Dial(ctx context.Context, address string) (PluggableConnection, error) {
	address = strings.TrimPrefix(address, "udp://")

	conn, err := net.DialTimeout("udp", address, p.timeout)
	if err != nil {
		return nil, err
	}

	return &StandardConnection{
		Conn:     conn,
		provider: p,
		metadata: ConnectionMetadata{
			Type:          "udp",
			Protocol:      "udp",
			RemoteAddress: conn.RemoteAddr().String(),
			LocalAddress:  conn.LocalAddr().String(),
			Capabilities:  []string{"unreliable", "bidirectional", "datagram"},
			Properties:    make(map[string]interface{}),
			Created:       time.Now(),
		},
		quality: ConnectionQuality{
			IsHealthy:     true,
			HealthScore:   0.8, // Lower than TCP due to unreliability
			LastActivity:  time.Now(),
			CustomMetrics: make(map[string]float64),
		},
		priority: 80,   // Lower priority than TCP
		mtu:      1472, // UDP payload size for standard Ethernet
	}, nil
}

func (p *UDPProvider) Listen(ctx context.Context, address string) (ConnectionListener, error) {
	address = strings.TrimPrefix(address, "udp://")

	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}

	listener, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}

	return &UDPListener{
		PacketConn: listener,
		provider:   p,
		conns:      make(map[string]*UDPConnection),
		acceptCh:   make(chan PluggableConnection, 100),
	}, nil
}

func (p *UDPProvider) HealthCheck(ctx context.Context) error {
	// UDP provider is always healthy
	return nil
}

// DNSTunnelProvider implements DNS tunneling connections
type DNSTunnelProvider struct {
	name       string
	dnsServer  string
	domain     string
	timeout    time.Duration
	maxPayload int
}

// NewDNSTunnelProvider creates a new DNS tunnel connection provider
func NewDNSTunnelProvider(dnsServer, domain string, timeout time.Duration) *DNSTunnelProvider {
	return &DNSTunnelProvider{
		name:       "dns-tunnel",
		dnsServer:  dnsServer,
		domain:     domain,
		timeout:    timeout,
		maxPayload: 240, // Conservative payload size for DNS queries
	}
}

func (p *DNSTunnelProvider) Name() string {
	return p.name
}

func (p *DNSTunnelProvider) SupportsAddress(address string) bool {
	return strings.HasPrefix(address, "dns://") || strings.HasPrefix(address, "dnstt://")
}

func (p *DNSTunnelProvider) Dial(ctx context.Context, address string) (PluggableConnection, error) {
	// Parse DNS tunnel address: dns://domain@nameserver or dnstt://domain@nameserver
	address = strings.TrimPrefix(address, "dns://")
	address = strings.TrimPrefix(address, "dnstt://")

	parts := strings.Split(address, "@")
	domain := parts[0]
	nameserver := p.dnsServer
	if len(parts) > 1 {
		nameserver = parts[1]
	}

	conn := &DNSTunnelConnection{
		provider:   p,
		domain:     domain,
		nameserver: nameserver,
		timeout:    p.timeout,
		maxPayload: p.maxPayload,
		sendQueue:  make(chan []byte, 1000),
		recvQueue:  make(chan []byte, 1000),
		closeCh:    make(chan struct{}),
		metadata: ConnectionMetadata{
			Type:          "dns-tunnel",
			Protocol:      "dns-over-udp",
			RemoteAddress: fmt.Sprintf("%s@%s", domain, nameserver),
			LocalAddress:  "local-dns-client",
			Capabilities:  []string{"unreliable", "bidirectional", "steganographic", "firewall-resistant"},
			Properties: map[string]interface{}{
				"domain":      domain,
				"nameserver":  nameserver,
				"max_payload": p.maxPayload,
			},
			Created: time.Now(),
		},
		quality: ConnectionQuality{
			IsHealthy:     true,
			HealthScore:   0.6, // Lower due to DNS overhead and potential unreliability
			LastActivity:  time.Now(),
			CustomMetrics: make(map[string]float64),
		},
		priority: 40, // Lower priority due to overhead
	}

	// Start the DNS tunnel goroutines
	if err := conn.start(ctx); err != nil {
		return nil, err
	}

	return conn, nil
}

func (p *DNSTunnelProvider) Listen(ctx context.Context, address string) (ConnectionListener, error) {
	// DNS tunneling typically works differently for listening
	// This is a simplified implementation
	return nil, errors.New("DNS tunnel listening not yet implemented")
}

func (p *DNSTunnelProvider) HealthCheck(ctx context.Context) error {
	// Perform a simple DNS query to check if the nameserver is responsive
	_, err := net.LookupTXT("health-check." + p.domain)
	return err
}

// StandardConnection wraps a net.Conn to implement PluggableConnection
type StandardConnection struct {
	net.Conn
	provider ConnectionProvider
	metadata ConnectionMetadata
	quality  ConnectionQuality
	priority int
	mtu      int
	mu       sync.RWMutex
}

func (c *StandardConnection) Provider() ConnectionProvider {
	return c.provider
}

func (c *StandardConnection) Metadata() ConnectionMetadata {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.metadata
}

func (c *StandardConnection) Quality() ConnectionQuality {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.quality
}

func (c *StandardConnection) Priority() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.priority
}

func (c *StandardConnection) SetPriority(priority int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.priority = priority
}

func (c *StandardConnection) IsReliable() bool {
	return c.metadata.Type == "tcp"
}

func (c *StandardConnection) MaxMTU() int {
	return c.mtu
}

func (c *StandardConnection) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if err == nil {
		c.mu.Lock()
		c.quality.LastActivity = time.Now()
		c.mu.Unlock()
	}
	return
}

func (c *StandardConnection) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if err == nil {
		c.mu.Lock()
		c.quality.LastActivity = time.Now()
		c.mu.Unlock()
	}
	return
}

// StandardListener wraps a net.Listener to implement ConnectionListener
type StandardListener struct {
	net.Listener
	provider ConnectionProvider
}

func (l *StandardListener) Accept() (PluggableConnection, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return &StandardConnection{
		Conn:     conn,
		provider: l.provider,
		metadata: ConnectionMetadata{
			Type:          l.provider.Name(),
			Protocol:      l.provider.Name(),
			RemoteAddress: conn.RemoteAddr().String(),
			LocalAddress:  conn.LocalAddr().String(),
			Capabilities:  []string{"reliable", "bidirectional", "stream"},
			Properties:    make(map[string]interface{}),
			Created:       time.Now(),
		},
		quality: ConnectionQuality{
			IsHealthy:     true,
			HealthScore:   1.0,
			LastActivity:  time.Now(),
			CustomMetrics: make(map[string]float64),
		},
		priority: 100,
		mtu:      1500,
	}, nil
}

func (l *StandardListener) Provider() ConnectionProvider {
	return l.provider
}

// UDPConnection represents a UDP "connection" (stateful wrapper around UDP socket)
type UDPConnection struct {
	net.Conn
	provider   ConnectionProvider
	metadata   ConnectionMetadata
	quality    ConnectionQuality
	priority   int
	remoteAddr net.Addr
}

func (c *UDPConnection) Provider() ConnectionProvider { return c.provider }
func (c *UDPConnection) Metadata() ConnectionMetadata { return c.metadata }
func (c *UDPConnection) Quality() ConnectionQuality   { return c.quality }
func (c *UDPConnection) Priority() int                { return c.priority }
func (c *UDPConnection) SetPriority(priority int)     { c.priority = priority }
func (c *UDPConnection) IsReliable() bool             { return false }
func (c *UDPConnection) MaxMTU() int                  { return 1472 }

// UDPListener handles UDP "connections"
type UDPListener struct {
	net.PacketConn
	provider ConnectionProvider
	conns    map[string]*UDPConnection
	acceptCh chan PluggableConnection
	mu       sync.RWMutex
}

func (l *UDPListener) Accept() (PluggableConnection, error) {
	select {
	case conn := <-l.acceptCh:
		return conn, nil
	}
}

func (l *UDPListener) Provider() ConnectionProvider {
	return l.provider
}

func (l *UDPListener) Addr() net.Addr {
	return l.PacketConn.LocalAddr()
}

// DNSTunnelConnection implements DNS tunneling
type DNSTunnelConnection struct {
	provider   *DNSTunnelProvider
	domain     string
	nameserver string
	timeout    time.Duration
	maxPayload int
	sendQueue  chan []byte
	recvQueue  chan []byte
	closeCh    chan struct{}
	closed     bool
	metadata   ConnectionMetadata
	quality    ConnectionQuality
	priority   int
	mu         sync.RWMutex
}

func (c *DNSTunnelConnection) start(ctx context.Context) error {
	// Start sender goroutine
	go c.senderLoop(ctx)

	// Start receiver goroutine (simplified - in real implementation would listen for DNS responses)
	go c.receiverLoop(ctx)

	return nil
}

func (c *DNSTunnelConnection) senderLoop(ctx context.Context) {
	for {
		select {
		case data := <-c.sendQueue:
			c.sendDNSQuery(data)
		case <-c.closeCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (c *DNSTunnelConnection) receiverLoop(ctx context.Context) {
	// This is a simplified receiver - in a real implementation,
	// you'd listen for DNS responses and decode the data
	for {
		select {
		case <-c.closeCh:
			return
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
			// Simulate receiving data (in real implementation, decode from DNS responses)
			// This would be replaced with actual DNS response handling
		}
	}
}

func (c *DNSTunnelConnection) sendDNSQuery(data []byte) error {
	// Encode data as base64 for DNS-safe transmission
	encoded := base64.StdEncoding.EncodeToString(data)

	// Split into chunks that fit in DNS labels (max 63 chars per label)
	chunks := c.chunkString(encoded, 60)

	// Create DNS query
	query := strings.Join(chunks, ".") + "." + c.domain

	// Send DNS TXT query (simplified - in real implementation use proper DNS library)
	_, err := net.LookupTXT(query)

	c.mu.Lock()
	c.quality.LastActivity = time.Now()
	c.mu.Unlock()

	return err
}

func (c *DNSTunnelConnection) chunkString(s string, chunkSize int) []string {
	var chunks []string
	for i := 0; i < len(s); i += chunkSize {
		end := i + chunkSize
		if end > len(s) {
			end = len(s)
		}
		chunks = append(chunks, s[i:end])
	}
	return chunks
}

func (c *DNSTunnelConnection) Read(b []byte) (n int, err error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return 0, errors.New("connection closed")
	}
	c.mu.RUnlock()

	select {
	case data := <-c.recvQueue:
		n = copy(b, data)
		c.mu.Lock()
		c.quality.LastActivity = time.Now()
		c.mu.Unlock()
		return n, nil
	case <-c.closeCh:
		return 0, errors.New("connection closed")
	}
}

func (c *DNSTunnelConnection) Write(b []byte) (n int, err error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return 0, errors.New("connection closed")
	}
	c.mu.RUnlock()

	// Split data into chunks that fit in DNS queries
	for i := 0; i < len(b); i += c.maxPayload {
		end := i + c.maxPayload
		if end > len(b) {
			end = len(b)
		}

		chunk := make([]byte, end-i)
		copy(chunk, b[i:end])

		select {
		case c.sendQueue <- chunk:
		case <-c.closeCh:
			return n, errors.New("connection closed")
		}

		n += len(chunk)
	}

	return n, nil
}

func (c *DNSTunnelConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.closed {
		c.closed = true
		close(c.closeCh)
	}
	return nil
}

func (c *DNSTunnelConnection) LocalAddr() net.Addr {
	return &dnsAddr{addr: "local-dns-client"}
}

func (c *DNSTunnelConnection) RemoteAddr() net.Addr {
	return &dnsAddr{addr: c.domain + "@" + c.nameserver}
}

func (c *DNSTunnelConnection) SetDeadline(t time.Time) error {
	// DNS tunneling doesn't support deadlines in this simplified implementation
	return nil
}

func (c *DNSTunnelConnection) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *DNSTunnelConnection) SetWriteDeadline(t time.Time) error {
	return nil
}

func (c *DNSTunnelConnection) Provider() ConnectionProvider { return c.provider }
func (c *DNSTunnelConnection) Metadata() ConnectionMetadata { return c.metadata }
func (c *DNSTunnelConnection) Quality() ConnectionQuality   { return c.quality }
func (c *DNSTunnelConnection) Priority() int                { return c.priority }
func (c *DNSTunnelConnection) SetPriority(priority int)     { c.priority = priority }
func (c *DNSTunnelConnection) IsReliable() bool             { return false }
func (c *DNSTunnelConnection) MaxMTU() int                  { return c.maxPayload }

// dnsAddr implements net.Addr for DNS addresses
type dnsAddr struct {
	addr string
}

func (a *dnsAddr) Network() string { return "dns" }
func (a *dnsAddr) String() string  { return a.addr }
