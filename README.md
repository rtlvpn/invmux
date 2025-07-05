# InvMux - Inverse Connection Multiplexer for Go

InvMux is a Go library that implements inverse multiplexing for network connections. While traditional multiplexers like [smux](https://github.com/xtaci/smux) allow multiple logical connections over a single physical connection, InvMux does the opposite - it splits a single logical connection across multiple physical connections.

## Features

- Split a single logical connection across multiple physical connections
- Automatic chunking and reassembly of data packets
- Bidirectional communication
- Stream-oriented API similar to net.Conn
- Multiple distribution policies (round-robin, lowest latency, highest bandwidth, weighted)
- Out-of-order packet handling and reassembly
- Configurable buffer sizes and window sizes
- Connection statistics and monitoring
- Easily add or remove physical connections at runtime

## Usage

### Basic Usage

```go
// Create sessions on both ends
clientSession := invmux.NewSession(nil) // nil uses default config
serverSession := invmux.NewSession(nil)

// Add physical connections to both sessions
// In a real application, these would be connections between two separate machines
clientSession.AddPhysicalConnection(conn1)
clientSession.AddPhysicalConnection(conn2)
serverSession.AddPhysicalConnection(conn1Remote)
serverSession.AddPhysicalConnection(conn2Remote)

// Server side: Accept streams
go func() {
    stream, err := serverSession.AcceptStream()
    if err != nil {
        // Handle error
    }
    
    // Use stream like a regular net.Conn
    io.Copy(os.Stdout, stream)
    stream.Close()
}()

// Client side: Open a stream
clientStream, err := clientSession.OpenStream()
if err != nil {
    // Handle error
}

// Use the stream like a regular net.Conn
clientStream.Write([]byte("Hello world!"))
clientStream.Close()
```

### Advanced Configuration

InvMux provides extensive configuration options:

```go
// Create a custom configuration
config := &invmux.Config{
    // Basic settings
    Version:                1,
    WindowSize:             8192,                // Size of the send window
    MaxChunkSize:           32768,               // Maximum size of each chunk
    
    // Buffer settings
    ReadBufferSize:         131072,              // 128KB read buffer
    WriteBufferSize:        131072,              // 128KB write buffer
    
    // Stream settings
    MaxStreams:             2048,                // Maximum number of streams
    AcceptBacklog:          1024,                // Backlog for accepting streams
    
    // Advanced settings
    EnableOutOfOrderProcessing: true,            // Process out-of-order packets
    OutOfOrderWindowSize:    2048,               // Size of out-of-order window
    
    // Connection management
    KeepAliveInterval:      15 * time.Second,    // Keep-alive interval
    ConnectionWriteTimeout: 5 * time.Second,     // Write timeout
    
    // Distribution policy
    DistributionPolicy:     invmux.RoundRobin,   // How to distribute chunks
    // Other options: LowestLatency, HighestBandwidth, WeightedRoundRobin
}

// Create a session with custom config
session := invmux.NewSession(config)
```

## How It Works

InvMux takes data written to a logical stream, chunks it into smaller packets, and distributes those packets across multiple physical connections. On the receiving end, the packets are reassembled in the correct order to reconstruct the original data stream.

1. When you write data to a stream, InvMux divides it into chunks
2. Each chunk is assigned a sequential ID and sent through one of the available physical connections
3. The receiving end collects chunks from all physical connections
4. The chunks are reassembled in order based on their IDs
5. The reassembled data is made available to read from the stream

### Distribution Policies

InvMux supports multiple distribution policies, all fully implemented:

- **RoundRobin**: Distributes chunks evenly across all connections in a circular fashion
- **LowestLatency**: Sends chunks through the connection with the lowest measured latency
- **HighestBandwidth**: Sends chunks through the connection with the highest measured bandwidth
- **WeightedRoundRobin**: Distributes chunks based on assigned connection weights

You can set weights for specific connections:

```go
// Set weight for a specific connection
session.SetConnectionWeight("connection-id", 10) // This connection gets 10x more traffic
```

The library automatically monitors connection performance and updates latency and bandwidth measurements for intelligent routing decisions.

### Connection Statistics

You can access detailed statistics for all connections:

```go
// Get stats for all connections
stats := session.GetConnectionStats()

// Get connections sorted by latency (lowest first)
sortedByLatency := session.SortConnectionsByLatency()

// Get connections sorted by bandwidth (highest first)
sortedByBandwidth := session.SortConnectionsByBandwidth()
```

## Performance Benefits

Using multiple physical connections can provide several benefits:

- **Increased throughput**: Combine the bandwidth of multiple connections
- **Redundancy**: If one connection fails, the others can continue
- **Bypass connection limits**: Useful when providers limit bandwidth per connection
- **Reduced latency**: Different connections may take different routes with varying latency
- **Load balancing**: Distribute traffic across multiple paths

## License

This library is released under the MIT License. 