// Advanced example demonstrates how to use the invmux library with custom configuration
package main

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/rtlvpn/invmux"
)

func main() {
	// In a real application, these connections would be between two separate machines
	// For demonstration, we'll use local connections
	conn1a, conn1b := net.Pipe()
	conn2a, conn2b := net.Pipe()
	conn3a, conn3b := net.Pipe()

	fmt.Println("=== InvMux Advanced Example ===")
	fmt.Println("This example demonstrates different distribution policies and connection statistics")

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

		// Distribution policy - we'll change this during the example
		DistributionPolicy: invmux.RoundRobin,

		// Connection weights for weighted distribution
		ConnectionWeights: map[string]int{
			// We'll set these after creating connections
		},
	}

	// Server uses default config for this example
	serverConfig := invmux.DefaultConfig()
	// Match client policy
	serverConfig.DistributionPolicy = invmux.RoundRobin

	// Create sessions
	clientSession := invmux.NewSession(clientConfig)
	serverSession := invmux.NewSession(serverConfig)

	// Add physical connections to sessions
	fmt.Println("\nAdding physical connections...")

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

	// Let's demonstrate each distribution policy

	// 1. Round Robin (default)
	fmt.Println("\n=== Round Robin Distribution ===")
	demoDistributionPolicy(clientSession, invmux.RoundRobin)

	// Wait a bit to let everything finish
	time.Sleep(1 * time.Second)

	// 2. Lowest Latency
	fmt.Println("\n=== Lowest Latency Distribution ===")
	demoDistributionPolicy(clientSession, invmux.LowestLatency)

	// Wait a bit to let everything finish
	time.Sleep(1 * time.Second)

	// 3. Highest Bandwidth
	fmt.Println("\n=== Highest Bandwidth Distribution ===")
	demoDistributionPolicy(clientSession, invmux.HighestBandwidth)

	// Wait a bit to let everything finish
	time.Sleep(1 * time.Second)

	// 4. Weighted Round Robin
	fmt.Println("\n=== Weighted Round Robin Distribution ===")

	// Get connection stats to set weights
	stats := clientSession.GetConnectionStats()
	if len(stats) >= 3 {
		// Set different weights for each connection
		clientSession.SetConnectionWeight(stats[0].ID, 1) // 1x traffic
		clientSession.SetConnectionWeight(stats[1].ID, 2) // 2x traffic
		clientSession.SetConnectionWeight(stats[2].ID, 5) // 5x traffic

		fmt.Println("Set connection weights: 1, 2, and 5")
	}

	demoDistributionPolicy(clientSession, invmux.WeightedRoundRobin)

	// Display connection statistics
	fmt.Println("\n=== Connection Statistics ===")
	displayConnectionStats(clientSession)

	// Close sessions
	fmt.Println("\nClosing sessions...")
	clientSession.Close()
	serverSession.Close()

	fmt.Println("Advanced example completed successfully")
}

// demoDistributionPolicy demonstrates a specific distribution policy
func demoDistributionPolicy(session *invmux.Session, policy invmux.DistributionPolicy) {
	// Set the distribution policy
	session.SetDistributionPolicy(policy)

	// Send some test data using multiple streams
	for i := 0; i < 3; i++ {
		go func(streamNum int) {
			// Open a stream
			clientStream, err := session.OpenStream()
			if err != nil {
				fmt.Printf("Failed to open stream %d: %v\n", streamNum, err)
				return
			}

			fmt.Printf("Client opened stream %d with ID: %d\n", streamNum, clientStream.StreamID())

			// Set a deadline
			clientStream.SetDeadline(time.Now().Add(10 * time.Second))

			// Write data to the stream - multiple chunks to demonstrate distribution
			for j := 0; j < 5; j++ {
				message := fmt.Sprintf("Hello from stream %d, message %d! This will be distributed according to the policy.", streamNum, j)
				_, err = clientStream.Write([]byte(message))
				if err != nil {
					fmt.Printf("Stream %d write error: %v\n", streamNum, err)
					return
				}
				time.Sleep(50 * time.Millisecond)
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

	// Wait for streams to complete
	time.Sleep(1 * time.Second)
}

// displayConnectionStats shows detailed statistics for all connections
func displayConnectionStats(session *invmux.Session) {
	// Get all connection stats
	stats := session.GetConnectionStats()
	fmt.Printf("Total connections: %d\n", len(stats))

	for i, stat := range stats {
		fmt.Printf("\nConnection #%d:\n", i+1)
		fmt.Printf("  ID: %s\n", stat.ID)
		fmt.Printf("  Latency: %v\n", stat.Latency)
		fmt.Printf("  Bandwidth: %d bytes/sec\n", stat.Bandwidth)
		fmt.Printf("  Bytes Sent: %d\n", stat.BytesSent)
		fmt.Printf("  Bytes Received: %d\n", stat.BytesReceived)
		fmt.Printf("  Weight: %d\n", stat.Weight)
		fmt.Printf("  Last Activity: %v\n", stat.LastActivity)
	}

	// Show connections sorted by latency
	fmt.Println("\nConnections sorted by latency (lowest first):")
	latencyStats := session.SortConnectionsByLatency()
	for i, stat := range latencyStats {
		fmt.Printf("  %d. ID: %s, Latency: %v\n", i+1, stat.ID, stat.Latency)
	}

	// Show connections sorted by bandwidth
	fmt.Println("\nConnections sorted by bandwidth (highest first):")
	bandwidthStats := session.SortConnectionsByBandwidth()
	for i, stat := range bandwidthStats {
		fmt.Printf("  %d. ID: %s, Bandwidth: %d bytes/sec\n", i+1, stat.ID, stat.Bandwidth)
	}
}
