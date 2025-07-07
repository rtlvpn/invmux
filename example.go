package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/yourname/invmux"
)

func main() {
	// Create a session with default configuration
	config := invmux.DefaultConfig()
	
	// Add some middleware
	config.Middleware = []invmux.Middleware{
		invmux.NewPassthroughMiddleware(),
		invmux.NewCompressionMiddleware(),
	}
	
	// Use weighted load balancing
	config.LoadBalancer = invmux.NewWeightedBalancer()
	
	session := invmux.NewSession(config)
	defer session.Close()

	// Example: Add TCP connections
	registry := invmux.DefaultRegistry()
	
	// Create connections
	conn1, err := registry.Dial(context.Background(), "tcp://localhost:8080")
	if err != nil {
		log.Printf("Failed to create connection 1: %v", err)
	} else {
		conn1.SetPriority(100) // High priority
		session.AddConnection(conn1)
	}
	
	conn2, err := registry.Dial(context.Background(), "tcp://localhost:8081") 
	if err != nil {
		log.Printf("Failed to create connection 2: %v", err)
	} else {
		conn2.SetPriority(80) // Lower priority
		session.AddConnection(conn2)
	}

	// Example 1: Client side - open a stream and write data
	go func() {
		stream, err := session.OpenStream()
		if err != nil {
			log.Printf("Failed to open stream: %v", err)
			return
		}
		defer stream.Close()

		// Write some data
		data := []byte("Hello from multiplexed stream!")
		_, err = stream.Write(data)
		if err != nil {
			log.Printf("Failed to write data: %v", err)
			return
		}

		// Read response
		response := make([]byte, 1024)
		n, err := stream.Read(response)
		if err != nil && err != io.EOF {
			log.Printf("Failed to read response: %v", err)
			return
		}

		fmt.Printf("Received response: %s\n", string(response[:n]))
	}()

	// Example 2: Server side - accept incoming streams
	go func() {
		for {
			stream, err := session.AcceptStream()
			if err != nil {
				log.Printf("Failed to accept stream: %v", err)
				return
			}

			// Handle the stream in a goroutine
			go handleIncomingStream(stream)
		}
	}()

	// Let the examples run
	time.Sleep(10 * time.Second)
	
	// Print session statistics
	fmt.Printf("Session has %d active streams and %d connections\n", 
		session.NumStreams(), session.NumConnections())
}

func handleIncomingStream(stream invmux.Stream) {
	defer stream.Close()

	// Read data from the stream
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil && err != io.EOF {
		log.Printf("Failed to read from stream: %v", err)
		return
	}

	fmt.Printf("Stream %d received: %s\n", stream.ID(), string(buffer[:n]))

	// Echo the data back
	response := fmt.Sprintf("Echo: %s", string(buffer[:n]))
	_, err = stream.Write([]byte(response))
	if err != nil {
		log.Printf("Failed to write response: %v", err)
		return
	}
}