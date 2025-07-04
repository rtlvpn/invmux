// Advanced example demonstrates how to use the invmux library with custom configuration
package main

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/yourusername/invmux"
)

func main() {
	// In a real application, these connections would be between two separate machines
	// For demonstration, we'll use local connections
	conn1a, conn1b := net.Pipe()
	conn2a, conn2b := net.Pipe()
	conn3a, conn3b := net.Pipe()

	// Create advanced configuration
	clientConfig := &invmux.Config{
		// Basic settings
		Version:      1,
		WindowSize:   8192, // 8KB window size
		MaxChunkSize: 4096, // 4KB chunks

		// Buffer settings
		ReadBufferSize:  131072, // 128KB read buffer
		WriteBufferSize: 131072, // 128KB write buffer

		// Stream settings
		MaxStreams:    100, // Maximum 100 streams
		AcceptBacklog: 50,  // Accept backlog of 50 streams

		// Advanced settings
		EnableOutOfOrderProcessing: true, // Process out-of-order packets
		OutOfOrderWindowSize:       1024, // Size of out-of-order window

		// Connection management
		KeepAliveInterval:      15 * time.Second, // Keep-alive every 15 seconds
		ConnectionWriteTimeout: 5 * time.Second,  // 5 second write timeout

		// Distribution policy
		DistributionPolicy: invmux.HighestBandwidth, // Use highest bandwidth policy

		// Optional features - disabled for this example
		EnableCompression: false,
		EnableEncryption:  false,
	}

	// Server uses default config for this example
	serverConfig := invmux.DefaultConfig()
	serverConfig.DistributionPolicy = invmux.HighestBandwidth // Match client policy

	// Create sessions
	clientSession := invmux.NewSession(clientConfig)
	serverSession := invmux.NewSession(serverConfig)

	// Add physical connections to sessions
	fmt.Println("Adding physical connections...")

	go func() {
		clientSession.AddPhysicalConnection(conn1a)
		clientSession.AddPhysicalConnection(conn2a)
		clientSession.AddPhysicalConnection(conn3a)
	}()

	go func() {
		serverSession.AddPhysicalConnection(conn1b)
		serverSession.AddPhysicalConnection(conn2b)
		serverSession.AddPhysicalConnection(conn3b)
	}()

	// Give time for connections to be established
	time.Sleep(100 * time.Millisecond)

	fmt.Printf("Client has %d physical connections\n", clientSession.NumPhysicalConnections())
	fmt.Printf("Server has %d physical connections\n", serverSession.NumPhysicalConnections())

	// Server: Accept streams
	go func() {
		for {
			stream, err := serverSession.AcceptStream()
			if err != nil {
				fmt.Println("Accept error:", err)
				return
			}

			fmt.Println("Server accepted stream:", stream.StreamID())

			// Echo back data with stream ID prepended
			go func(s *invmux.Stream) {
				buf := make([]byte, 8192) // Larger buffer for efficiency
				for {
					n, err := s.Read(buf)
					if err != nil {
						if err != io.EOF {
							fmt.Println("Server read error:", err)
						}
						break
					}

					fmt.Printf("Server received %d bytes on stream %d\n", n, s.StreamID())

					// Echo back with stream ID prepended
					response := fmt.Sprintf("[Stream %d] %s", s.StreamID(), buf[:n])
					_, err = s.Write([]byte(response))
					if err != nil {
						fmt.Println("Server write error:", err)
						break
					}
				}
				s.Close()
				fmt.Println("Server stream closed:", s.StreamID())
			}(stream)
		}
	}()

	// Client: Open multiple streams
	for i := 0; i < 3; i++ {
		go func(streamNum int) {
			// Open a stream
			clientStream, err := clientSession.OpenStream()
			if err != nil {
				fmt.Printf("Failed to open stream %d: %v\n", streamNum, err)
				return
			}

			fmt.Printf("Client opened stream %d with ID: %d\n", streamNum, clientStream.StreamID())

			// Set a deadline
			clientStream.SetDeadline(time.Now().Add(10 * time.Second))

			// Write data to the stream
			message := fmt.Sprintf("Hello from stream %d! This message will be split across multiple physical connections.", streamNum)
			_, err = clientStream.Write([]byte(message))
			if err != nil {
				fmt.Printf("Stream %d write error: %v\n", streamNum, err)
				return
			}

			// Read response
			response := make([]byte, 8192)
			n, err := clientStream.Read(response)
			if err != nil && err != io.EOF {
				fmt.Printf("Stream %d read error: %v\n", streamNum, err)
				return
			}

			fmt.Printf("Stream %d received response: %s\n", streamNum, response[:n])

			// Close the stream
			clientStream.Close()
			fmt.Printf("Stream %d closed\n", streamNum)
		}(i)
	}

	// Wait for all streams to complete
	time.Sleep(3 * time.Second)

	// Show stream stats
	fmt.Printf("Client has %d active streams\n", clientSession.NumStreams())
	fmt.Printf("Server has %d active streams\n", serverSession.NumStreams())

	// Close sessions
	fmt.Println("Closing sessions...")
	clientSession.Close()
	serverSession.Close()

	fmt.Println("Advanced example completed successfully")
}
