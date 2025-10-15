package conf

import (
	"net"
	"strings"
)

// GetValidIP returns the first valid non-loopback IPv4 address.
// It prefers non-internal addresses (public IPs) over internal addresses (private IPs).
// Returns "127.0.0.1" if no valid IP is found.
func GetValidIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}

	// First pass: look for public IPs
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		// Skip loopback
		if ipNet.IP.IsLoopback() {
			continue
		}

		// Get IPv4 address
		ipv4 := ipNet.IP.To4()
		if ipv4 == nil {
			continue
		}

		// Check if it's a public IP (not private)
		if !isPrivateIP(ipv4) {
			return ipv4.String()
		}
	}

	// Second pass: look for private IPs (if no public IP found)
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		// Skip loopback
		if ipNet.IP.IsLoopback() {
			continue
		}

		// Get IPv4 address
		ipv4 := ipNet.IP.To4()
		if ipv4 == nil {
			continue
		}

		// Return any private IP
		return ipv4.String()
	}

	// Fallback to localhost
	return "127.0.0.1"
}

// isPrivateIP checks if the IP is a private (RFC 1918) address
func isPrivateIP(ip net.IP) bool {
	// 10.0.0.0/8
	if ip[0] == 10 {
		return true
	}
	// 172.16.0.0/12
	if ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 {
		return true
	}
	// 192.168.0.0/16
	if ip[0] == 192 && ip[1] == 168 {
		return true
	}
	return false
}

// BuildAPIBaseURL constructs the base URL for API access.
// If apiDomain is configured, it uses that.
// Otherwise, it auto-detects the IP and constructs the URL using apiAddress port.
func BuildAPIBaseURL(apiDomain, apiAddress string) string {
	// Use configured apiDomain if provided
	if apiDomain != "" {
		return strings.TrimRight(apiDomain, "/")
	}

	// Auto-detect IP and extract port from apiAddress
	ip := GetValidIP()
	port := "9997" // default port

	// Extract port from apiAddress (format: ":9997" or "0.0.0.0:9997")
	if apiAddress != "" {
		parts := strings.Split(apiAddress, ":")
		if len(parts) > 0 {
			portStr := parts[len(parts)-1]
			if portStr != "" {
				port = portStr
			}
		}
	}

	return "http://" + ip + ":" + port
}
