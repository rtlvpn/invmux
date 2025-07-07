module github.com/rtlvpn/invmux

go 1.19

require (
	golang.org/x/net v0.15.0
	golang.org/x/sync v0.3.0
)

// Optional dependencies for advanced features
// Uncomment these for full DNS tunneling support:
// github.com/miekg/dns v1.1.55
// github.com/klauspost/compress v1.16.7

// For enhanced encryption middleware:
// golang.org/x/crypto v0.13.0

// For metrics and monitoring:
// github.com/prometheus/client_golang v1.16.0 
