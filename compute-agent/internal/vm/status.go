package vm

import (
	"fmt"
	"time"
)

// CredentialsProvider defines methods to extract credentials and network info from running VMs
type CredentialsProvider interface {
	// GetIPAddresses returns all IP addresses assigned to the VM
	GetIPAddresses(vmName string) ([]string, error)

	// GetNetworkInterfaces returns network interface details
	GetNetworkInterfaces(vmName string) ([]NetworkInterface, error)

	// GetCloudInitCredentials extracts credentials from cloud-init user data
	GetCloudInitCredentials(userDataBase64 string) (*Credentials, error)

	// WaitForNetwork waits for VM to acquire network connectivity
	WaitForNetwork(vmName string, timeout time.Duration) ([]string, error)
}

// NetworkInterface represents a VM network interface
type NetworkInterface struct {
	Name       string // eth0, eth1, etc.
	MACAddress string
	IPAddress  string
	Gateway    string
	DNS        []string
}

// Credentials represents VM login credentials
type Credentials struct {
	Username  string
	Password  string
	SSHKeys   []string
	Hostname  string
	ExtraData map[string]string
}

// VMStatus represents comprehensive VM status information
type VMStatus struct {
	Name              string
	State             string
	CPUs              int
	MemoryMB          int64
	IPAddresses       []string
	NetworkInterfaces []NetworkInterface
	Credentials       *Credentials
	LaunchTime        time.Time
	LastHeartbeat     time.Time
}

// VMInfo provides detailed information about a virtual machine
type VMInfo struct {
	// Basic info
	Name      string
	UUID      string
	State     string
	AutoStart bool

	// Resources
	VCPUs       int
	MemoryMB    int64
	DisksSizeGB []int64

	// Network
	MacAddresses []string
	IPAddresses  []string

	// Cloud-init
	HasCloudInit bool
	HostName     string
	Users        []CloudInitUser

	// Metadata
	CreatedAt  time.Time
	ModifiedAt time.Time
}

// CloudInitUser represents a cloud-init user configuration
type CloudInitUser struct {
	Name     string
	Password string
	Groups   []string
	SSHKeys  []string
	Shell    string
}

// CloudInitDataExtractor provides functionality to extract data from cloud-init
type CloudInitDataExtractor struct {
}

// NewCloudInitDataExtractor creates a new extractor instance
func NewCloudInitDataExtractor() *CloudInitDataExtractor {
	return &CloudInitDataExtractor{}
}

// ExtractUsers parses cloud-init user-data and extracts user information
// This is a placeholder that would parse YAML or shell scripts
func (e *CloudInitDataExtractor) ExtractUsers(userDataScript string) ([]CloudInitUser, error) {
	// This would parse the cloud-init user-data format
	// For now, return placeholder indicating the capability exists
	return nil, fmt.Errorf("cloud-init parsing requires full implementation based on format")
}

// ExtractHostname extracts hostname from cloud-init config
func (e *CloudInitDataExtractor) ExtractHostname(userDataScript string) (string, error) {
	// Parse cloud-init to extract hostname setting
	return "", fmt.Errorf("cloud-init parsing requires full implementation")
}

// ExtractSSHKeys extracts SSH public keys from cloud-init
func (e *CloudInitDataExtractor) ExtractSSHKeys(userDataScript string) ([]string, error) {
	// Parse cloud-init to extract ssh_authorized_keys
	return nil, fmt.Errorf("cloud-init parsing requires full implementation")
}

// NetworkWaiter provides network availability checking
type NetworkWaiter struct {
	maxAttempts int
	retryDelay  time.Duration
}

// NewNetworkWaiter creates a new network waiter
func NewNetworkWaiter(maxAttempts int, retryDelay time.Duration) *NetworkWaiter {
	return &NetworkWaiter{
		maxAttempts: maxAttempts,
		retryDelay:  retryDelay,
	}
}

// WaitForIP waits for a VM to acquire an IP address
func (w *NetworkWaiter) WaitForIP(vmName string, getIPFunc func() ([]string, error)) ([]string, error) {
	for attempt := 0; attempt < w.maxAttempts; attempt++ {
		ips, err := getIPFunc()
		if err == nil && len(ips) > 0 {
			return ips, nil
		}

		if attempt < w.maxAttempts-1 {
			time.Sleep(w.retryDelay)
		}
	}

	return nil, fmt.Errorf("timeout waiting for %s to acquire IP address after %d attempts",
		vmName, w.maxAttempts)
}

// VMStatusBuilder helps build comprehensive VM status
type VMStatusBuilder struct {
	status *VMStatus
}

// NewVMStatusBuilder creates a new status builder
func NewVMStatusBuilder(name string) *VMStatusBuilder {
	return &VMStatusBuilder{
		status: &VMStatus{
			Name:              name,
			IPAddresses:       make([]string, 0),
			NetworkInterfaces: make([]NetworkInterface, 0),
		},
	}
}

// WithState sets the VM state
func (b *VMStatusBuilder) WithState(state string) *VMStatusBuilder {
	b.status.State = state
	return b
}

// WithResources sets resource information
func (b *VMStatusBuilder) WithResources(cpus int, memoryMB int64) *VMStatusBuilder {
	b.status.CPUs = cpus
	b.status.MemoryMB = memoryMB
	return b
}

// WithIPs sets IP addresses
func (b *VMStatusBuilder) WithIPs(ips []string) *VMStatusBuilder {
	b.status.IPAddresses = ips
	return b
}

// WithNetworkInterfaces sets network interfaces
func (b *VMStatusBuilder) WithNetworkInterfaces(interfaces []NetworkInterface) *VMStatusBuilder {
	b.status.NetworkInterfaces = interfaces
	return b
}

// WithCredentials sets credentials
func (b *VMStatusBuilder) WithCredentials(creds *Credentials) *VMStatusBuilder {
	b.status.Credentials = creds
	return b
}

// WithTimestamps sets creation and heartbeat times
func (b *VMStatusBuilder) WithTimestamps(created, heartbeat time.Time) *VMStatusBuilder {
	b.status.LaunchTime = created
	b.status.LastHeartbeat = heartbeat
	return b
}

// Build returns the constructed VMStatus
func (b *VMStatusBuilder) Build() *VMStatus {
	return b.status
}
