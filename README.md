# InvMux - Clean Multiplexer with Pluggable Architecture

InvMux is a Go library that implements connection multiplexing with a clean, pluggable architecture inspired by [smux](https://github.com/xtaci/smux). It provides a transport-agnostic way to multiplex streams over multiple connections with proper middleware support and load balancing.

## 🚀 Key Features

- **Clean Interface Design**: Simple, transport-agnostic interfaces
- **Pluggable Architecture**: Easy to extend with custom connection types
- **Middleware Support**: Process data through configurable middleware layers
- **Load Balancing**: Multiple strategies for distributing traffic
- **Health Monitoring**: Automatic connection health tracking
- **No Transport Lock-in**: Works with any connection type that implements the Connection interface

## 📦 Installation

```bash
go get github.com/yourusername/invmux
```

## 🏁 Quick Start

### Basic Usage

```go
package main

import (
    "context"
    "fmt"
    "log"
    "github.com/yourusername/invmux"
)

func main() {
    // Create a session with default configuration
    config := invmux.DefaultConfig()
    session := invmux.NewSession(config)
    defer session.Close()
    
    // Add connections using the connection registry
    registry := invmux.DefaultRegistry()
    
    conn1, err := registry.Dial(context.Background(), "tcp://localhost:8080")
    if err != nil {
        log.Fatal(err)
    }
    session.AddConnection(conn1)
    
    conn2, err := registry.Dial(context.Background(), "tcp://localhost:8081")
    if err != nil {
        log.Fatal(err)
    }
    session.AddConnection(conn2)
    
    // Open a stream and use it
    stream, err := session.OpenStream()
    if err != nil {
        log.Fatal(err)
    }
    defer stream.Close()
    
    // Write data
    _, err = stream.Write([]byte("Hello, multiplexed world!"))
    if err != nil {
        log.Fatal(err)
    }
    
    // Read response
    response := make([]byte, 1024)
    n, err := stream.Read(response)
    if err != nil {
        log.Fatal(err)
    }
    
    fmt.Printf("Response: %s\n", response[:n])
}
```

### Server Example

```go
func main() {
    config := invmux.DefaultConfig()
    session := invmux.NewSession(config)
    defer session.Close()
    
    // Add connections...
    
    // Accept incoming streams
    for {
        stream, err := session.AcceptStream()
        if err != nil {
            log.Printf("Error accepting stream: %v", err)
            continue
        }
        
        go handleStream(stream)
    }
}

func handleStream(stream invmux.Stream) {
    defer stream.Close()
    
    buffer := make([]byte, 1024)
    n, err := stream.Read(buffer)
    if err != nil {
        return
    }
    
    // Echo back
    stream.Write(buffer[:n])
}
```

## 🔧 Configuration

### Basic Configuration

```go
config := &invmux.Config{
    WindowSize:        8192,
    MaxChunkSize:      4096,
    KeepAlive:         30 * time.Second,
    WriteTimeout:      10 * time.Second,
    MaxStreams:        1024,
    AcceptBacklog:     256,
    LoadBalancer:      invmux.NewRoundRobinBalancer(),
    ErrorHandler:      invmux.NewDefaultErrorHandler(),
    HealthMonitor:     invmux.NewDefaultHealthMonitor(),
    ConnectionManager: invmux.NewDefaultConnectionManager(),
    Middleware:        []invmux.Middleware{},
}
```

### Middleware Configuration

```go
config.Middleware = []invmux.Middleware{
    invmux.NewPassthroughMiddleware(),
    invmux.NewCompressionMiddleware(),
    // Add your custom middleware here
}
```

## 🔌 Pluggable Architecture

### Connection Factories

The system uses connection factories to create connections:

```go
// Built-in factories
registry := invmux.NewConnectionRegistry()
registry.Register(invmux.NewTCPFactory(10 * time.Second))
registry.Register(invmux.NewUDPFactory(10 * time.Second))

// Use the registry
conn, err := registry.Dial(ctx, "tcp://example.com:8080")
```

### Custom Connection Factory

```go
type MyConnectionFactory struct {
    name string
}

func (f *MyConnectionFactory) Name() string {
    return f.name
}

func (f *MyConnectionFactory) CanHandle(address string) bool {
    return strings.HasPrefix(address, "my://")
}

func (f *MyConnectionFactory) Dial(ctx context.Context, address string) (invmux.Connection, error) {
    // Implement your custom connection logic
    return myConnection, nil
}

func (f *MyConnectionFactory) Listen(ctx context.Context, address string) (invmux.Listener, error) {
    // Implement your custom listener logic
    return myListener, nil
}

// Register your factory
registry.Register(&MyConnectionFactory{name: "my-protocol"})
```

### Custom Middleware

```go
type LoggingMiddleware struct {
    name string
}

func (m *LoggingMiddleware) Name() string {
    return m.name
}

func (m *LoggingMiddleware) ProcessOutbound(streamID uint32, data []byte) ([]byte, error) {
    log.Printf("Stream %d sending %d bytes", streamID, len(data))
    return data, nil
}

func (m *LoggingMiddleware) ProcessInbound(streamID uint32, data []byte) ([]byte, error) {
    log.Printf("Stream %d received %d bytes", streamID, len(data))
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
config.Middleware = append(config.Middleware, &LoggingMiddleware{name: "logger"})
```

## ⚖️ Load Balancing

Choose from multiple load balancing strategies:

```go
// Round-robin (default)
config.LoadBalancer = invmux.NewRoundRobinBalancer()

// Weighted load balancing
config.LoadBalancer = invmux.NewWeightedBalancer()

// Random load balancing
config.LoadBalancer = invmux.NewRandomBalancer()
```

### Custom Load Balancer

```go
type MyLoadBalancer struct {
    name string
}

func (b *MyLoadBalancer) Name() string {
    return b.name
}

func (b *MyLoadBalancer) Select(connections []invmux.Connection, streamID uint32, data []byte) (invmux.Connection, error) {
    // Implement your selection logic
    return connections[0], nil
}

func (b *MyLoadBalancer) OnConnectionAdded(conn invmux.Connection) {
    // Handle new connection
}

func (b *MyLoadBalancer) OnConnectionRemoved(conn invmux.Connection) {
    // Handle removed connection
}

func (b *MyLoadBalancer) UpdateStats(conn invmux.Connection, quality *invmux.Quality) {
    // Update with connection stats
}
```

## 📊 Monitoring and Health

### Connection Quality

```go
// Get connection quality metrics
quality := conn.Quality()
fmt.Printf("Latency: %v, Bandwidth: %d, Score: %.2f\n", 
    quality.Latency, quality.Bandwidth, quality.Score)
```

### Session Statistics

```go
fmt.Printf("Active streams: %d\n", session.NumStreams())
fmt.Printf("Active connections: %d\n", session.NumConnections())
```

### Health Monitoring

```go
// Configure health thresholds
thresholds := &invmux.HealthThresholds{
    MaxLatency:    500 * time.Millisecond,
    MinBandwidth:  1024,
    MaxPacketLoss: 0.05,
    MaxErrorRate:  0.02,
    MinScore:      0.7,
    MaxIdleTime:   60 * time.Second,
    CheckInterval: 10 * time.Second,
}

healthMonitor := invmux.NewDefaultHealthMonitor()
healthMonitor.SetThresholds(thresholds)
```

## � Error Handling

### Custom Error Handler

```go
type MyErrorHandler struct{}

func (h *MyErrorHandler) HandleConnectionError(conn invmux.Connection, err error) invmux.ErrorAction {
    if strings.Contains(err.Error(), "timeout") {
        return invmux.ErrorActionRetry
    }
    return invmux.ErrorActionReconnect
}

func (h *MyErrorHandler) HandleStreamError(streamID uint32, err error) invmux.ErrorAction {
    log.Printf("Stream %d error: %v", streamID, err)
    return invmux.ErrorActionRetry
}

func (h *MyErrorHandler) HandleSessionError(err error) invmux.ErrorAction {
    log.Printf("Session error: %v", err)
    return invmux.ErrorActionFailover
}

config.ErrorHandler = &MyErrorHandler{}
```

## 🧪 Testing

Run the examples:

```bash
go run example.go
```

Run tests:

```bash
go test -v ./...
```

## 🏗️ Architecture

InvMux follows a clean, layered architecture:

1. **Session Layer**: Manages multiplexing and stream lifecycle
2. **Connection Layer**: Abstract connection interface
3. **Middleware Layer**: Process data through configurable pipeline
4. **Transport Layer**: Pluggable connection factories

### Key Interfaces

```go
type Session interface {
    OpenStream() (Stream, error)
    AcceptStream() (Stream, error)
    AddConnection(conn Connection) error
    RemoveConnection(connID string) error
    Close() error
    // ...
}

type Connection interface {
    io.ReadWriteCloser
    Quality() *Quality
    Priority() int
    SetPriority(priority int)
    ID() string
    Type() string
}

type Middleware interface {
    ProcessOutbound(streamID uint32, data []byte) ([]byte, error)
    ProcessInbound(streamID uint32, data []byte) ([]byte, error)
    OnStreamOpen(streamID uint32) error
    OnStreamClose(streamID uint32) error
}
```

## 🤝 Contributing

We welcome contributions! Please:

1. Fork the repository
2. Create a feature branch
3. Add tests for new functionality
4. Submit a pull request

## 📄 License

This project is licensed under the MIT License.

## 🔗 Related Projects

- [smux](https://github.com/xtaci/smux) - Stream multiplexing library for Go
- [yamux](https://github.com/hashicorp/yamux) - Golang connection multiplexing library

---

**InvMux** - Clean, pluggable multiplexing for Go applications. 