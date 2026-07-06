package node

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"strings"
)

// GenerateUniqueNodeID generates a unique node identifier combining hostname and machine ID.
// Priority order:
// 1. PERSYS_NODE_ID environment variable (if explicitly set)
// 2. Hostname + machine-id (stored at /etc/machine-id)
// 3. Hostname + primary MAC address
// 4. Generated UUID-based fallback
func GenerateUniqueNodeID() (string, error) {
	// First check for explicit override
	if nodeID := os.Getenv("PERSYS_NODE_ID"); nodeID != "" && nodeID != "auto" {
		return nodeID, nil
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	// Try to read /etc/machine-id (Linux systems)
	machineID, err := readMachineID()
	if err == nil && machineID != "" {
		// Use first 12 chars of machine-id for uniqueness
		nodeID := fmt.Sprintf("%s-%s", hostname, machineID[:min(12, len(machineID))])
		return nodeID, nil
	}

	// Fall back to primary MAC address
	macAddr, err := getPrimaryMACAddress()
	if err == nil && macAddr != "" {
		// Hash MAC for shorter ID
		hash := sha256.Sum256([]byte(macAddr))
		nodeID := fmt.Sprintf("%s-%x", hostname, hash[:6])
		return nodeID, nil
	}

	// Final fallback - just return hostname (not ideal but better than nothing)
	// In production, machine-id should always be available
	return fmt.Sprintf("%s-unknown", hostname), fmt.Errorf("could not generate unique identifier: no machine-id or MAC address found")
}

// readMachineID reads the system machine ID from /etc/machine-id
func readMachineID() (string, error) {
	data, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// getPrimaryMACAddress returns the MAC address of the primary network interface
func getPrimaryMACAddress() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	// Look for first non-loopback interface that is up
	for _, iface := range interfaces {
		// Skip loopback
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		// Skip down interfaces
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		// Return MAC of first viable interface
		return iface.HardwareAddr.String(), nil
	}

	return "", fmt.Errorf("no suitable network interface found")
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetNodeIDHash returns a hash of the node ID for use in naming schemes
func GetNodeIDHash(nodeID string) string {
	hash := sha256.Sum256([]byte(nodeID))
	return fmt.Sprintf("%x", hash[:8])
}

// ValidateNodeID checks if a node ID is valid (non-empty, no spaces)
func ValidateNodeID(nodeID string) error {
	if nodeID == "" {
		return fmt.Errorf("node ID cannot be empty")
	}
	if strings.ContainsAny(nodeID, " \t\n\r") {
		return fmt.Errorf("node ID contains whitespace: %s", nodeID)
	}
	if len(nodeID) > 255 {
		return fmt.Errorf("node ID too long: %d (max 255)", len(nodeID))
	}
	return nil
}
