package resources

import (
	"fmt"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/sirupsen/logrus"
)

// Utilization represents system resource utilization metrics
type Utilization struct {
	MemoryPercent float64            // Memory utilization percentage
	CPUPercent    float64            // CPU utilization percentage
	DiskPercent   map[string]float64 // Disk utilization by mount point
	Timestamp     time.Time
}

// Monitor tracks system resource utilization
type Monitor struct {
	thresholds *Thresholds
	logger     *logrus.Entry
	mu         sync.RWMutex
	lastCheck  *Utilization
}

// Thresholds define resource availability thresholds
type Thresholds struct {
	// MemoryThreshold is the percentage at which memory is considered low (default 80%)
	MemoryThreshold float64
	// CPUThreshold is the percentage at which CPU is considered low (default 80%)
	CPUThreshold float64
	// DiskThreshold is the percentage at which disk is considered low (default 80%)
	DiskThreshold float64
}

// DefaultThresholds returns default resource thresholds
func DefaultThresholds() *Thresholds {
	return &Thresholds{
		MemoryThreshold: 80.0,
		CPUThreshold:    80.0,
		DiskThreshold:   90.0,
	}
}

// NewMonitor creates a new resource monitor
func NewMonitor(thresholds *Thresholds, logger *logrus.Logger) *Monitor {
	if thresholds == nil {
		thresholds = DefaultThresholds()
	}
	return &Monitor{
		thresholds: thresholds,
		logger:     logger.WithField("component", "resource-monitor"),
		lastCheck: &Utilization{
			DiskPercent: make(map[string]float64),
		},
	}
}

// GetUtilization returns current system resource utilization
func (m *Monitor) GetUtilization(mountPoints []string) (*Utilization, error) {
	util := &Utilization{
		Timestamp:   time.Now(),
		DiskPercent: make(map[string]float64),
	}

	// Get memory utilization
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		return nil, fmt.Errorf("failed to get memory stats: %w", err)
	}
	util.MemoryPercent = vmStat.UsedPercent

	// Get CPU utilization
	cpuPercent, err := cpu.Percent(time.Second, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get CPU stats: %w", err)
	}
	if len(cpuPercent) > 0 {
		util.CPUPercent = cpuPercent[0]
	}

	// Get disk utilization for specified mount points (or all if empty)
	if len(mountPoints) == 0 {
		mountPoints = []string{"/", "/var", "/home"}
	}

	for _, mountPoint := range mountPoints {
		usage, err := disk.Usage(mountPoint)
		if err != nil {
			m.logger.Warnf("Failed to get disk usage for %s: %v", mountPoint, err)
			continue
		}
		util.DiskPercent[mountPoint] = usage.UsedPercent
	}

	m.mu.Lock()
	m.lastCheck = util
	m.mu.Unlock()

	return util, nil
}

// IsResourceAvailable checks if system has sufficient resources for a new workload
// It checks against the configured thresholds
func (m *Monitor) IsResourceAvailable() (bool, []string, error) {
	util, err := m.GetUtilization([]string{"/"})
	if err != nil {
		return false, nil, fmt.Errorf("failed to check resource availability: %w", err)
	}

	var issues []string

	if util.MemoryPercent > m.thresholds.MemoryThreshold {
		issues = append(issues, fmt.Sprintf("memory utilization too high: %.1f%% (threshold: %.1f%%)",
			util.MemoryPercent, m.thresholds.MemoryThreshold))
	}

	if util.CPUPercent > m.thresholds.CPUThreshold {
		issues = append(issues, fmt.Sprintf("CPU utilization too high: %.1f%% (threshold: %.1f%%)",
			util.CPUPercent, m.thresholds.CPUThreshold))
	}

	for mountPoint, percent := range util.DiskPercent {
		if percent > m.thresholds.DiskThreshold {
			issues = append(issues, fmt.Sprintf("disk utilization too high on %s: %.1f%% (threshold: %.1f%%)",
				mountPoint, percent, m.thresholds.DiskThreshold))
		}
	}

	if len(issues) > 0 {
		return false, issues, nil
	}

	return true, nil, nil
}

// CheckMemoryAvailable checks if sufficient memory is available
func (m *Monitor) CheckMemoryAvailable(requestedMB int64) (bool, string) {
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		return false, fmt.Sprintf("failed to check memory: %v", err)
	}

	availableMB := int64(vmStat.Available / 1024 / 1024)
	if availableMB < requestedMB {
		return false, fmt.Sprintf("insufficient memory: requested %dMB, available %dMB",
			requestedMB, availableMB)
	}

	return true, ""
}

// CheckCPUAvailable checks if CPU resources are available
func (m *Monitor) CheckCPUAvailable() (int, error) {
	// Get current CPU utilization
	cpuPercent, err := cpu.Percent(time.Second, true)
	if err != nil {
		return 0, fmt.Errorf("failed to check CPU utilization: %w", err)
	}

	// Count available CPUs (those below threshold)
	available := 0
	for _, percent := range cpuPercent {
		if percent < m.thresholds.CPUThreshold {
			available++
		}
	}

	return available, nil
}

// CheckDiskAvailable checks if sufficient disk space is available
func (m *Monitor) CheckDiskAvailable(mountPoint string, requiredGB int64) (bool, string) {
	usage, err := disk.Usage(mountPoint)
	if err != nil {
		return false, fmt.Sprintf("failed to check disk: %v", err)
	}

	availableGB := int64(usage.Free / 1024 / 1024 / 1024)
	if availableGB < requiredGB {
		return false, fmt.Sprintf("insufficient disk space on %s: requested %dGB, available %dGB",
			mountPoint, requiredGB, availableGB)
	}

	return true, ""
}

// GetLastUtilization returns the last recorded utilization snapshot
func (m *Monitor) GetLastUtilization() *Utilization {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastCheck
}

// LogResourceStatus logs the current resource status
func (m *Monitor) LogResourceStatus() {
	util, err := m.GetUtilization([]string{"/"})
	if err != nil {
		m.logger.Errorf("Failed to get resource status: %v", err)
		return
	}

	m.logger.Infof("System resources - Memory: %.1f%%, CPU: %.1f%%, Disk: %.1f%%",
		util.MemoryPercent, util.CPUPercent, util.DiskPercent["/"])
}
