# Enhanced InvMux - Advanced Inverse Connection Multiplexer for Go

InvMux is a powerful, production-ready Go library that implements inverse multiplexing for network connections with pluggable interfaces and advanced features. While traditional multiplexers like [smux](https://github.com/xtaci/smux) allow multiple logical connections over a single physical connection, InvMux does the opposite - it intelligently distributes a single logical connection across multiple physical connections of any type.

## 🚀 Key Features

### Core Capabilities
- **Inverse Multiplexing**: Split logical connections across multiple physical connections
- **Pluggable Connection Providers**: Support for TCP, UDP, DNS tunneling, HTTP, and custom protocols
- **Advanced Load Balancing**: Multiple distribution strategies with intelligent routing
- **Stream Middleware**: Extensible middleware system for compression, encryption, and custom processing
- **Health Monitoring**: Automatic connection health monitoring and failover
- **Comprehensive Metrics**: Detailed performance and usage statistics

### Connection Types Supported
- **TCP/TLS**: Reliable, high-performance connections
- **UDP**: Fast, low-latency connections for real-time applications  
- **DNS Tunneling**: Steganographic communication through DNS queries (firewall/censorship resistant)
- **HTTP/WebSocket**: Web-compatible connections
- **Custom Protocols**: Easy to implement custom connection providers

### Advanced Features
- **Auto-Healing**: Automatic connection recovery and reconnection
- **Quality of Service**: Stream prioritization and QoS classes
- **Out-of-Order Processing**: Handle packets arriving out of sequence
- **Connection Bonding**: Aggregate bandwidth from multiple connections
- **Intelligent Failover**: Seamless switching when connections fail
- **Pluggable Architecture**: Every component is replaceable/customizable

## 📦 Installation

```bash
go get github.com/yourusername/invmux
```

## 🏁 Quick Start

### Basic Usage with Enhanced Features

```go
package main

import (
    "fmt"
    "time"
    "github.com/yourusername/invmux"
)

func main() {
    // Create enhanced configuration
    config := invmux.DefaultEnhancedConfig()
    config.AutoHealing = true
    config.ConnectionBalancer = invmux.NewLatencyBasedBalancer()
    
    // Add middleware for compression and encryption
    config.Middleware = []invmux.StreamMiddleware{
        invmux.NewCompressionMiddleware(),
        invmux.NewEncryptionMiddleware([]byte("secret-key-1234")),
    }
    
    // Create session
    session := invmux.NewEnhancedSession(config)
    defer session.Close()
    
    // Add multiple connection types
    session.AddConnectionByAddress("tcp://server1.example.com:8080")
    session.AddConnectionByAddress("udp://server2.example.com:8080") 
    session.AddConnectionByAddress("dns://tunnel.example.com@8.8.8.8")
    
    // Open a stream and use it like a regular net.Conn
    stream, err := session.OpenStream()
    if err != nil {
        panic(err)
    }
    defer stream.Close()
    
    // Set QoS and priority
    stream.SetPriority(100)
    stream.SetQoSClass("high-priority")
    
    // Use the stream
    stream.Write([]byte("Hello, distributed world!"))
    
    response := make([]byte, 1024)
    n, _ := stream.Read(response)
    fmt.Printf("Response: %s\n", response[:n])
    
    // Get detailed metrics
    metrics := session.GetMetrics()
    fmt.Printf("Total data sent: %d bytes\n", metrics.BytesSent)
}
```

### DNS Tunneling Example

```go
// DNS tunneling for censorship resistance
config := invmux.DefaultEnhancedConfig()
session := invmux.NewEnhancedSession(config)

// Add DNS tunnel connection
err := session.AddConnectionByAddress("dns://tunnel.example.com@8.8.8.8:53")
if err != nil {
    log.Fatal(err)
}

// Data will be tunneled through DNS queries
stream, _ := session.OpenStream()
stream.Write([]byte("This message travels through DNS!"))
```

## 🔧 Configuration

### Enhanced Configuration Options

```go
config := &invmux.EnhancedConfig{
    // Base configuration
    Config: &invmux.Config{
        Version:                    1,
        WindowSize:                 8192,
        MaxChunkSize:               4096,
        KeepAliveInterval:          10 * time.Second,
        ConnectionWriteTimeout:     5 * time.Second,
        EnableOutOfOrderProcessing: true,
    },
    
    // Advanced features
    AutoHealing:              true,
    AutoReconnect:            true,
    ConnectionRedundancy:     2,
    PreferReliableConnections: true,
    UseAdaptiveDistribution:  true,
    
    // Pluggable components
    ConnectionPool:     invmux.NewDefaultConnectionPool(),
    ConnectionBalancer: invmux.NewLatencyBasedBalancer(),
    ErrorHandler:       invmux.NewDefaultErrorHandler(),
    HealthMonitor:      invmux.NewDefaultHealthMonitor(),
    
    // Middleware stack
    Middleware: []invmux.StreamMiddleware{
        invmux.NewCompressionMiddleware(),
        invmux.NewEncryptionMiddleware(key),
    },
    
    // Health monitoring thresholds
    HealthThresholds: invmux.HealthThresholds{
        MaxLatency:          200 * time.Millisecond,
        MinBandwidth:        10240, // 10KB/s minimum
        MaxPacketLoss:       0.02,  // 2% max
        MinHealthScore:      0.8,   // 80% minimum
        HealthCheckInterval: 5 * time.Second,
    },
}
```

## 🔌 Pluggable Architecture

### Connection Providers

InvMux supports multiple connection types through pluggable providers:

```go
// Built-in providers
tcpProvider := invmux.NewTCPProvider(10 * time.Second)
udpProvider := invmux.NewUDPProvider(5 * time.Second)
dnsProvider := invmux.NewDNSTunnelProvider("8.8.8.8:53", "tunnel.example.com", 30 * time.Second)

// Register providers
pool := invmux.NewDefaultConnectionPool()
pool.AddProvider(tcpProvider)
pool.AddProvider(udpProvider)
pool.AddProvider(dnsProvider)
```

### Custom Connection Provider

```go
type CustomProvider struct {
    name string
}

func (p *CustomProvider) Name() string { return p.name }

func (p *CustomProvider) SupportsAddress(address string) bool {
    return strings.HasPrefix(address, "custom://")
}

func (p *CustomProvider) Dial(ctx context.Context, address string) (invmux.PluggableConnection, error) {
    // Implement custom connection logic
    return customConn, nil
}

// Register your custom provider
config.ConnectionPool.AddProvider(&CustomProvider{name: "custom"})
```

### Load Balancing Strategies

Choose from multiple load balancing strategies:

```go
// Round-robin (default)
config.ConnectionBalancer = invmux.NewRoundRobinBalancer()

// Latency-based routing
config.ConnectionBalancer = invmux.NewLatencyBasedBalancer()

// Weighted distribution
weightedBalancer := invmux.NewWeightedBalancer()
weightedBalancer.SetWeight("tcp-connection-1", 5)  // 5x weight
weightedBalancer.SetWeight("udp-connection-1", 2)  // 2x weight
config.ConnectionBalancer = weightedBalancer
```

### Custom Middleware

Create custom middleware for processing stream data:

```go
type LoggingMiddleware struct{}

func (m *LoggingMiddleware) Name() string { return "logger" }

func (m *LoggingMiddleware) ProcessWrite(streamID uint32, data []byte) ([]byte, error) {
    log.Printf("Stream %d writing %d bytes", streamID, len(data))
    return data, nil
}

func (m *LoggingMiddleware) ProcessRead(streamID uint32, data []byte) ([]byte, error) {
    log.Printf("Stream %d reading %d bytes", streamID, len(data))
    return data, nil
}

func (m *LoggingMiddleware) OnStreamOpen(streamID uint32) error {
    log.Printf("Stream %d opened", streamID)
    return nil
}

func (m *LoggingMiddleware) OnStreamClose(streamID uint32) error {
    log.Printf("Stream %d closed", streamID)
    return nil
}

// Add to configuration
config.Middleware = append(config.Middleware, &LoggingMiddleware{})
```

## 📊 Monitoring and Metrics

### Session Metrics

```go
metrics := session.GetMetrics()
fmt.Printf("Connections: %d active, %d total\n", 
    metrics.ConnectionsActive, metrics.ConnectionsTotal)
fmt.Printf("Streams: %d active, %d total\n", 
    metrics.StreamsActive, metrics.StreamsTotal)
fmt.Printf("Data: %d bytes sent, %d bytes received\n", 
    metrics.BytesSent, metrics.BytesReceived)
fmt.Printf("Performance: avg latency %v, avg bandwidth %d bytes/s\n", 
    metrics.AverageLatency, metrics.AverageBandwidth)
```

### Stream Metrics

```go
streamMetrics := stream.GetMetrics()
fmt.Printf("Stream %d: %d bytes read, %d bytes written, priority %d, QoS %s\n",
    streamMetrics.StreamID, streamMetrics.BytesRead, streamMetrics.BytesWritten,
    streamMetrics.Priority, streamMetrics.QoSClass)
```

### Connection Health

```go
connections := session.GetConnections()
for _, conn := range connections {
    metadata := conn.Metadata()
    quality := conn.Quality()
    fmt.Printf("Connection: type=%s, healthy=%t, latency=%v, score=%.2f\n",
        metadata.Type, quality.IsHealthy, quality.Latency, quality.HealthScore)
}
```

## 🌐 Real-World Use Cases

### 1. Bandwidth Aggregation
Combine multiple internet connections (DSL + Cable + 4G) for increased throughput:

```go
session.AddConnectionByAddress("tcp://isp1.example.com:8080")
session.AddConnectionByAddress("tcp://isp2.example.com:8080") 
session.AddConnectionByAddress("tcp://mobile.example.com:8080")
```

### 2. Censorship Resistance
Use DNS tunneling to bypass firewalls and deep packet inspection:

```go
// Primary connection
session.AddConnectionByAddress("tcp://server.example.com:443")

// Backup DNS tunnel for censorship circumvention
session.AddConnectionByAddress("dns://tunnel.example.com@8.8.8.8")
```

### 3. High Availability
Automatic failover between multiple paths:

```go
config.AutoHealing = true
config.ConnectionRedundancy = 3

session.AddConnectionByAddress("tcp://primary.example.com:8080")
session.AddConnectionByAddress("tcp://backup1.example.com:8080")
session.AddConnectionByAddress("tcp://backup2.example.com:8080")
```

### 4. Latency Optimization
Route traffic through the lowest latency path:

```go
config.ConnectionBalancer = invmux.NewLatencyBasedBalancer()

session.AddConnectionByAddress("tcp://us-east.example.com:8080")
session.AddConnectionByAddress("tcp://us-west.example.com:8080")
session.AddConnectionByAddress("tcp://eu.example.com:8080")
```

## 🔒 Security Features

### Encryption Middleware
Built-in encryption support with pluggable algorithms:

```go
// AES encryption (implement with crypto/aes)
aesKey := make([]byte, 32) // 256-bit key
config.Middleware = append(config.Middleware, 
    invmux.NewEncryptionMiddleware(aesKey))
```

### DNS Tunneling Security
DNS tunneling includes several security considerations:
- Encoded payloads to avoid detection
- Configurable query patterns
- Support for various DNS record types
- Built-in anti-fingerprinting measures

## ⚡ Performance Optimizations

### Buffer Management
Fine-tune buffer sizes for your use case:

```go
config.ReadBufferSize = 128 * 1024   // 128KB read buffer
config.WriteBufferSize = 128 * 1024  // 128KB write buffer
config.MaxChunkSize = 32 * 1024      // 32KB chunks
```

### Connection Pooling
Reuse connections efficiently:

```go
config.ConnectionPool.SetAutoHealing(true)
```

### Out-of-Order Processing
Handle packet reordering efficiently:

```go
config.EnableOutOfOrderProcessing = true
config.OutOfOrderWindowSize = 1024
```

## 🐛 Error Handling

### Custom Error Handler

```go
type CustomErrorHandler struct{}

func (h *CustomErrorHandler) HandleConnectionError(conn invmux.PluggableConnection, err error) invmux.ErrorAction {
    if strings.Contains(err.Error(), "timeout") {
        return invmux.ErrorActionRetry
    }
    if strings.Contains(err.Error(), "connection refused") {
        return invmux.ErrorActionReconnect
    }
    return invmux.ErrorActionRemoveConnection
}

func (h *CustomErrorHandler) HandleSessionError(session *invmux.EnhancedSession, err error) invmux.ErrorAction {
    log.Printf("Session error: %v", err)
    return invmux.ErrorActionIgnore
}

func (h *CustomErrorHandler) HandleStreamError(stream *invmux.EnhancedStream, err error) invmux.ErrorAction {
    log.Printf("Stream %d error: %v", stream.StreamID(), err)
    return invmux.ErrorActionIgnore
}

config.ErrorHandler = &CustomErrorHandler{}
```

## 🧪 Testing

Run the examples:

```bash
# Basic example
go run examples/advanced/advanced_example.go

# Enhanced features example
go run examples/advanced/enhanced_example.go
```

Run tests:

```bash
go test -v ./...
```

## 📚 Advanced Topics

### Connection Provider Architecture
The pluggable connection provider system allows supporting any transport:
- Traditional network protocols (TCP, UDP, SCTP)
- Tunneling protocols (DNS, HTTP, WebSocket)
- Custom protocols (radio, satellite, mesh networks)
- Virtual connections (in-memory, file-based)

### Middleware Pipeline
Middleware processes data in a pipeline:
1. **Write Path**: Original Data → Middleware 1 → Middleware 2 → Network
2. **Read Path**: Network → Middleware 2 → Middleware 1 → Original Data

Common middleware types:
- **Compression**: Reduce bandwidth usage
- **Encryption**: Secure data transmission  
- **Protocol Translation**: Convert between protocols
- **Rate Limiting**: Control traffic flow
- **Logging/Analytics**: Monitor usage patterns

### Health Monitoring
The health monitoring system continuously evaluates:
- **Latency**: Round-trip time measurements
- **Bandwidth**: Throughput capacity
- **Packet Loss**: Reliability metrics
- **Error Rate**: Connection stability
- **Activity**: Last successful communication

Unhealthy connections are automatically:
- Removed from load balancing rotation
- Scheduled for reconnection attempts
- Replaced with backup connections

## 🤝 Contributing

We welcome contributions! Please see our contributing guidelines for details.

## 📄 License

This project is licensed under the MIT License - see the LICENSE file for details.

## 🔗 Related Projects

- [smux](https://github.com/xtaci/smux) - Stream multiplexing library for Go
- [yamux](https://github.com/hashicorp/yamux) - Golang connection multiplexing library
- [dnstt](https://www.bamsoftware.com/software/dnstt/) - DNS tunneling tool

## 📞 Support

- Create an issue for bug reports or feature requests
- Join our discussions for questions and community support
- Check the examples directory for usage patterns

---

**InvMux** - Bringing the power of inverse multiplexing to Go applications with unprecedented flexibility and performance. 