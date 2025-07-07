// Enhanced example demonstrates the new pluggable inverse mux library with DNS tunneling support
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/yourusername/invmux"
)

func main() {
	fmt.Println("=== Enhanced InvMux Example ===")
	fmt.Println("Demonstrating pluggable interfaces, DNS tunneling, middleware, and advanced features")

	// Create enhanced configuration with all features enabled
	config := &invmux.EnhancedConfig{
		Config: &invmux.Config{
			Version:                    1,
			WindowSize:                 8192,
			MaxChunkSize:               4096,
			KeepAliveInterval:          10 * time.Second,
			ConnectionWriteTimeout:     5 * time.Second,
			ReadBufferSize:             65536,
			WriteBufferSize:            65536,
			MaxStreams:                 500,
			AcceptBacklog:              100,
			EnableOutOfOrderProcessing: true,
			OutOfOrderWindowSize:       512,
			DistributionPolicy:         invmux.RoundRobin,
		},
		
		// Enhanced features
		AutoHealing:              true,
		AutoReconnect:            true,
		ConnectionRedundancy:     2,
		MaxReconnectAttempts:     3,
		ReconnectBackoffInitial:  1 * time.Second,
		ReconnectBackoffMultiplier: 2.0,
		ReconnectBackoffMax:      30 * time.Second,
		PreferReliableConnections: true,
		UseAdaptiveDistribution:  true,
		DistributionHistorySize:  1000,
		
		// Custom components
		ConnectionPool:     invmux.NewDefaultConnectionPool(),
		ConnectionBalancer: invmux.NewLatencyBasedBalancer(),
		ErrorHandler:       invmux.NewDefaultErrorHandler(),
		HealthMonitor:      invmux.NewDefaultHealthMonitor(),
		
		// Middleware stack
		Middleware: []invmux.StreamMiddleware{
			invmux.NewCompressionMiddleware(),
			invmux.NewEncryptionMiddleware([]byte("secret-key-1234")),
		},
		
		// Health thresholds
		HealthThresholds: invmux.HealthThresholds{
			MaxLatency:          200 * time.Millisecond,
			MinBandwidth:        10240, // 10KB/s minimum
			MaxPacketLoss:       0.02,  // 2% max packet loss
			MaxErrorRate:        0.01,  // 1% max error rate
			MinHealthScore:      0.8,   // 80% minimum health score
			MaxIdleTime:         30 * time.Second,
			HealthCheckInterval: 5 * time.Second,
		},
	}

	// Set up connection providers and connection pool
	setupConnectionProviders(config)

	// Create sessions
	fmt.Println("\nCreating enhanced sessions...")
	clientSession := invmux.NewEnhancedSession(config)
	serverSession := invmux.NewEnhancedSession(config)

	// Set up multiple connection types including DNS tunneling
	fmt.Println("\nSetting up multiple connection types...")
	setupConnections(clientSession, serverSession)

	// Start server to handle incoming streams
	fmt.Println("\nStarting server...")
	go runServer(serverSession)

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	// Demonstrate various features
	demonstrateFeatures(clientSession)

	// Clean up
	fmt.Println("\nCleaning up...")
	clientSession.Close()
	serverSession.Close()

	fmt.Println("Enhanced example completed successfully!")
}

func setupConnectionProviders(config *invmux.EnhancedConfig) {
	fmt.Println("Registering connection providers...")
	
	// Add additional providers to the connection pool
	tcpProvider := invmux.NewTCPProvider(10 * time.Second)
	udpProvider := invmux.NewUDPProvider(5 * time.Second)
	dnsProvider := invmux.NewDNSTunnelProvider("8.8.8.8:53", "tunnel.example.com", 30 * time.Second)
	
	config.ConnectionPool.AddProvider(tcpProvider)
	config.ConnectionPool.AddProvider(udpProvider)
	config.ConnectionPool.AddProvider(dnsProvider)
	
	fmt.Printf("Registered providers: %s, %s, %s\n", 
		tcpProvider.Name(), udpProvider.Name(), dnsProvider.Name())
}

func setupConnections(clientSession, serverSession *invmux.EnhancedSession) {
	// Create pipe connections for demo (simulating real network connections)
	conn1a, conn1b := net.Pipe()
	conn2a, conn2b := net.Pipe()
	conn3a, conn3b := net.Pipe()

	// Wrap standard connections as pluggable connections
	plugConn1a := wrapConnection(conn1a, "tcp", 100)
	plugConn1b := wrapConnection(conn1b, "tcp", 100)
	plugConn2a := wrapConnection(conn2a, "udp", 80)
	plugConn2b := wrapConnection(conn2b, "udp", 80)
	plugConn3a := wrapConnection(conn3a, "dns-tunnel", 40)
	plugConn3b := wrapConnection(conn3b, "dns-tunnel", 40)

	// Add connections to sessions
	clientSession.AddConnection(plugConn1a)
	clientSession.AddConnection(plugConn2a)
	clientSession.AddConnection(plugConn3a)

	serverSession.AddConnection(plugConn1b)
	serverSession.AddConnection(plugConn2b)
	serverSession.AddConnection(plugConn3b)

	fmt.Printf("Client connections: %d\n", clientSession.NumConnections())
	fmt.Printf("Server connections: %d\n", serverSession.NumConnections())
}

func wrapConnection(conn net.Conn, connType string, priority int) invmux.PluggableConnection {
	// Create a mock pluggable connection wrapper
	return &mockPluggableConnection{
		Conn:     conn,
		connType: connType,
		priority: priority,
		metadata: invmux.ConnectionMetadata{
			Type:          connType,
			Protocol:      connType,
			RemoteAddress: conn.RemoteAddr().String(),
			LocalAddress:  conn.LocalAddr().String(),
			Capabilities:  []string{"bidirectional"},
			Properties:    make(map[string]interface{}),
			Created:       time.Now(),
		},
		quality: invmux.ConnectionQuality{
			Latency:       50 * time.Millisecond,
			IsHealthy:     true,
			HealthScore:   0.9,
			LastActivity:  time.Now(),
			CustomMetrics: make(map[string]float64),
		},
	}
}

type mockPluggableConnection struct {
	net.Conn
	provider invmux.ConnectionProvider
	connType string
	priority int
	metadata invmux.ConnectionMetadata
	quality  invmux.ConnectionQuality
}

func (c *mockPluggableConnection) Provider() invmux.ConnectionProvider     { return c.provider }
func (c *mockPluggableConnection) Metadata() invmux.ConnectionMetadata     { return c.metadata }
func (c *mockPluggableConnection) Quality() invmux.ConnectionQuality       { return c.quality }
func (c *mockPluggableConnection) Priority() int                           { return c.priority }
func (c *mockPluggableConnection) SetPriority(priority int)                { c.priority = priority }
func (c *mockPluggableConnection) IsReliable() bool                        { return c.connType == "tcp" }
func (c *mockPluggableConnection) MaxMTU() int                             { return 1500 }

func runServer(session *invmux.EnhancedSession) {
	fmt.Println("Server started, waiting for connections...")
	
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			log.Printf("Accept error: %v", err)
			return
		}

		fmt.Printf("Server accepted stream %d\n", stream.StreamID())
		go handleServerStream(stream)
	}
}

func handleServerStream(stream *invmux.EnhancedStream) {
	defer stream.Close()
	
	// Set QoS and metadata
	stream.SetQoSClass("high-priority")
	stream.SetMetadata("handler", "echo-server")
	
	buf := make([]byte, 4096)
	for {
		n, err := stream.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("Server read error: %v", err)
			}
			break
		}

		// Echo back with metadata
		response := fmt.Sprintf("[Echo from stream %d] %s", stream.StreamID(), buf[:n])
		_, err = stream.Write([]byte(response))
		if err != nil {
			log.Printf("Server write error: %v", err)
			break
		}
	}
	
	// Print stream metrics
	metrics := stream.GetMetrics()
	fmt.Printf("Stream %d metrics - Read: %d bytes, Written: %d bytes, Priority: %d, QoS: %s\n",
		metrics.StreamID, metrics.BytesRead, metrics.BytesWritten, metrics.Priority, metrics.QoSClass)
}

func demonstrateFeatures(session *invmux.EnhancedSession) {
	fmt.Println("\n=== Demonstrating Enhanced Features ===")
	
	// 1. Multiple streams with different priorities
	demonstrateStreamPriorities(session)
	
	// 2. Middleware effects
	demonstrateMiddleware(session)
	
	// 3. Connection management
	demonstrateConnectionManagement(session)
	
	// 4. Metrics and monitoring
	demonstrateMetrics(session)
}

func demonstrateStreamPriorities(session *invmux.EnhancedSession) {
	fmt.Println("\n--- Stream Priorities ---")
	
	// Create streams with different priorities
	priorities := []int{10, 50, 100}
	for i, priority := range priorities {
		stream, err := session.OpenStream()
		if err != nil {
			log.Printf("Failed to open stream: %v", err)
			continue
		}
		
		stream.SetPriority(priority)
		stream.SetQoSClass(fmt.Sprintf("priority-%d", priority))
		stream.SetMetadata("test", fmt.Sprintf("priority-test-%d", i))
		
		// Send test data
		message := fmt.Sprintf("Priority %d message from stream %d", priority, stream.StreamID())
		stream.Write([]byte(message))
		
		// Read response
		response := make([]byte, 1024)
		n, _ := stream.Read(response)
		fmt.Printf("Stream %d (priority %d): %s\n", stream.StreamID(), priority, response[:n])
		
		stream.Close()
	}
}

func demonstrateMiddleware(session *invmux.EnhancedSession) {
	fmt.Println("\n--- Middleware Effects ---")
	
	stream, err := session.OpenStream()
	if err != nil {
		log.Printf("Failed to open stream: %v", err)
		return
	}
	defer stream.Close()
	
	// Send data that will be processed by compression and encryption middleware
	largeMessage := make([]byte, 1000)
	for i := range largeMessage {
		largeMessage[i] = byte('A' + (i % 26))
	}
	
	fmt.Printf("Sending %d bytes through middleware stack...\n", len(largeMessage))
	stream.Write(largeMessage)
	
	// Read response
	response := make([]byte, 2048)
	n, _ := stream.Read(response)
	fmt.Printf("Received %d bytes after middleware processing\n", n)
}

func demonstrateConnectionManagement(session *invmux.EnhancedSession) {
	fmt.Println("\n--- Connection Management ---")
	
	connections := session.GetConnections()
	fmt.Printf("Active connections: %d\n", len(connections))
	
	for i, conn := range connections {
		metadata := conn.Metadata()
		quality := conn.Quality()
		fmt.Printf("Connection %d: Type=%s, Priority=%d, Healthy=%t, Score=%.2f\n",
			i+1, metadata.Type, conn.Priority(), quality.IsHealthy, quality.HealthScore)
	}
}

func demonstrateMetrics(session *invmux.EnhancedSession) {
	fmt.Println("\n--- Session Metrics ---")
	
	metrics := session.GetMetrics()
	fmt.Printf("Connections Active: %d, Total: %d\n", 
		metrics.ConnectionsActive, metrics.ConnectionsTotal)
	fmt.Printf("Streams Active: %d, Total: %d, Closed: %d\n",
		metrics.StreamsActive, metrics.StreamsTotal, metrics.StreamsClosed)
	fmt.Printf("Data - Sent: %d bytes, Received: %d bytes\n",
		metrics.BytesSent, metrics.BytesReceived)
	fmt.Printf("Packets - Sent: %d, Received: %d, Lost: %d\n",
		metrics.PacketsSent, metrics.PacketsReceived, metrics.PacketsLost)
	fmt.Printf("Performance - Avg Latency: %v, Avg Bandwidth: %d bytes/s, Health Score: %.2f\n",
		metrics.AverageLatency, metrics.AverageBandwidth, metrics.HealthScore)
}

// demonstrateRealWorldUsage shows how to use the library in real-world scenarios
func demonstrateRealWorldUsage() {
	fmt.Println("\n=== Real-World Usage Examples ===")
	
	// Example 1: DNS Tunneling
	demonstrateDNSTunneling()
	
	// Example 2: Multi-path networking
	demonstrateMultiPath()
	
	// Example 3: Custom middleware
	demonstrateCustomMiddleware()
}

func demonstrateDNSTunneling() {
	fmt.Println("\n--- DNS Tunneling Example ---")
	
	config := invmux.DefaultEnhancedConfig()
	session := invmux.NewEnhancedSession(config)
	defer session.Close()
	
	// Add DNS tunnel connection
	err := session.AddConnectionByAddress("dns://tunnel.example.com@8.8.8.8:53")
	if err != nil {
		fmt.Printf("DNS tunnel connection failed (expected in demo): %v\n", err)
	} else {
		fmt.Println("DNS tunnel connection established successfully!")
	}
	
	// DNS tunneling would work with real DNS infrastructure
	fmt.Println("DNS tunneling provides steganographic communication through DNS queries")
	fmt.Println("Useful for bypassing firewalls and censorship")
}

func demonstrateMultiPath() {
	fmt.Println("\n--- Multi-Path Networking Example ---")
	
	config := invmux.DefaultEnhancedConfig()
	config.ConnectionBalancer = invmux.NewLatencyBasedBalancer()
	session := invmux.NewEnhancedSession(config)
	defer session.Close()
	
	// In real usage, you'd add multiple actual network paths:
	// session.AddConnectionByAddress("tcp://server1.example.com:8080")
	// session.AddConnectionByAddress("tcp://server2.example.com:8080") 
	// session.AddConnectionByAddress("udp://server3.example.com:8080")
	// session.AddConnectionByAddress("dns://tunnel.example.com@8.8.8.8")
	
	fmt.Println("Multi-path networking allows:")
	fmt.Println("- Increased bandwidth by bonding multiple connections")
	fmt.Println("- Redundancy and failover capability")
	fmt.Println("- Intelligent routing based on connection quality")
}

func demonstrateCustomMiddleware() {
	fmt.Println("\n--- Custom Middleware Example ---")
	
	// Example of a custom middleware that logs all traffic
	loggingMiddleware := &LoggingMiddleware{name: "traffic-logger"}
	
	config := invmux.DefaultEnhancedConfig()
	config.Middleware = append(config.Middleware, loggingMiddleware)
	
	session := invmux.NewEnhancedSession(config)
	defer session.Close()
	
	fmt.Println("Custom middleware can provide:")
	fmt.Println("- Traffic logging and analytics")
	fmt.Println("- Custom encryption/compression algorithms")
	fmt.Println("- Protocol translation")
	fmt.Println("- Rate limiting and flow control")
}

// LoggingMiddleware is an example custom middleware
type LoggingMiddleware struct {
	name string
}

func (m *LoggingMiddleware) Name() string { return m.name }

func (m *LoggingMiddleware) ProcessWrite(streamID uint32, data []byte) ([]byte, error) {
	fmt.Printf("[LOG] Stream %d write: %d bytes\n", streamID, len(data))
	return data, nil
}

func (m *LoggingMiddleware) ProcessRead(streamID uint32, data []byte) ([]byte, error) {
	fmt.Printf("[LOG] Stream %d read: %d bytes\n", streamID, len(data))
	return data, nil
}

func (m *LoggingMiddleware) OnStreamOpen(streamID uint32) error {
	fmt.Printf("[LOG] Stream %d opened\n", streamID)
	return nil
}

func (m *LoggingMiddleware) OnStreamClose(streamID uint32) error {
	fmt.Printf("[LOG] Stream %d closed\n", streamID)
	return nil
}