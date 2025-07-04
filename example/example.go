// Example demonstrates how to use the invmux library
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

	// Create sessions
	// In a real application, clientSession and serverSession would be on different machines
	clientSession := invmux.NewSession(nil) // nil uses default config
	serverSession := invmux.NewSession(nil)

	// Add physical connections to sessions
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

	// Server: Accept streams
	go func() {
		for {
			stream, err := serverSession.AcceptStream()
			if err != nil {
				fmt.Println("Accept error:", err)
				return
			}

			fmt.Println("Server accepted stream:", stream.StreamID())

			// Echo back data
			go func(s *invmux.Stream) {
				buf := make([]byte, 1024)
				for {
					n, err := s.Read(buf)
					if err != nil {
						if err != io.EOF {
							fmt.Println("Server read error:", err)
						}
						break
					}

					fmt.Printf("Server received: %s\n", buf[:n])

					_, err = s.Write(buf[:n])
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

	// Client: Open stream and send data
	clientStream, err := clientSession.OpenStream()
	if err != nil {
		fmt.Println("Failed to open stream:", err)
		return
	}

	fmt.Println("Client opened stream:", clientStream.StreamID())

	// Write data to the stream
	message := "Hello from invmux! This message will be split across multiple physical connections."
	_, err = clientStream.Write([]byte(message))
	if err != nil {
		fmt.Println("Write error:", err)
		return
	}

	// Read response
	response := make([]byte, 1024)
	n, err := clientStream.Read(response)
	if err != nil && err != io.EOF {
		fmt.Println("Read error:", err)
		return
	}

	fmt.Printf("Client received response: %s\n", response[:n])

	// Close the stream
	clientStream.Close()

	// Wait a bit to let everything finish
	time.Sleep(1 * time.Second)

	// Close sessions
	clientSession.Close()
	serverSession.Close()

	fmt.Println("Example completed successfully")
}
