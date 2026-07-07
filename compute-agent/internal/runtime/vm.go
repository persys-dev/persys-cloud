package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/digitalocean/go-libvirt"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	"github.com/sirupsen/logrus"
)

const managedDiskMarkerSuffix = ".persys-managed"
const vmShutdownGracePeriod = 30 * time.Second
const vmShutdownPollInterval = 500 * time.Millisecond
const maxCloudInitPayloadBytes = 256 * 1024

// VMRuntime manages KVM virtual machine workloads via libvirt
type VMRuntime struct {
	conn     *libvirt.Libvirt
	logger   *logrus.Entry
	seedMu   sync.RWMutex
	seedInfo map[string]cloudInitSeedInfo

	usageMu      sync.Mutex
	usageSamples map[string]vmUsageSample
}

// vmUsageSample is the previous CPU-time sample for a domain, used to derive
// an instantaneous CPU percentage from libvirt's cumulative cpu_time counter
// (DomainGetInfo only reports a running total, not a rate).
type vmUsageSample struct {
	cpuTimeNs uint64
	sampledAt time.Time
}

type cloudInitSeedInfo struct {
	Path       string
	Checksum   string
	SizeBytes  int
	PreparedAt time.Time
}

func NewVMRuntime(uri string, logger *logrus.Logger) (*VMRuntime, error) {
	// For simplicity, this connects to the local libvirt UNIX socket.
	// In production, you'd want to handle remote connections properly and respect the uri parameter.
	socketPath := "/var/run/libvirt/libvirt-sock"
	netConn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to dial libvirt socket %s: %w", socketPath, err)
	}

	// Create a custom dialer that uses our pre-established connection
	dialer := &unixDialer{conn: netConn}
	conn := libvirt.NewWithDialer(dialer)

	// Perform libvirt handshake/initialization
	if err := conn.Connect(); err != nil {
		netConn.Close()
		return nil, fmt.Errorf("failed to handshake with libvirt: %w", err)
	}

	return &VMRuntime{
		conn:         conn,
		logger:       logger.WithField("runtime", "vm"),
		seedInfo:     make(map[string]cloudInitSeedInfo),
		usageSamples: make(map[string]vmUsageSample),
	}, nil
}

// unixDialer implements socket.Dialer for libvirt connections
type unixDialer struct {
	conn net.Conn
}

func (ud *unixDialer) Dial() (net.Conn, error) {
	if ud.conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}
	return ud.conn, nil
}

func (v *VMRuntime) Type() models.WorkloadType {
	return models.WorkloadTypeVM
}

func (v *VMRuntime) Create(ctx context.Context, workload *models.Workload) error {
	spec, err := v.parseSpec(workload.Spec)
	if err != nil {
		return fmt.Errorf("failed to parse VM spec: %w", err)
	}

	// Create cloud-init ISO if needed
	if spec.CloudInit != "" || spec.CloudInitConfig != nil {
		isoPath, seedInfo, err := v.createCloudInitISO(workload.ID, spec)
		if err != nil {
			return fmt.Errorf("failed to create cloud-init ISO: %w", err)
		}
		if seedInfo != nil {
			v.setSeedInfo(workload.ID, *seedInfo)
		}
		if isoPath != "" {
			// Add ISO to disks - mark as bootable CD-ROM
			spec.Disks = append(spec.Disks, models.DiskConfig{
				Path:   isoPath,
				Device: "hdb",
				Format: "raw",   // ISO files use raw format
				Type:   "cdrom", // String value
				Boot:   true,    // Mark as bootable
				SizeGB: 0,
			})
		}
	}
	if spec.CloudInit == "" && spec.CloudInitConfig == nil {
		v.clearSeedInfo(workload.ID)
	}

	// Create disk files first
	for _, diskCfg := range spec.Disks {
		if diskCfg.Type != models.DiskTypeCDROM {
			if isNetworkDiskPath(diskCfg.Path) {
				continue
			}
			if err := v.createDisk(&diskCfg); err != nil {
				return fmt.Errorf("failed to create disk %s: %w", diskCfg.Path, err)
			}
		}
	}

	// Generate libvirt XML
	domainXML, err := v.generateDomainXML(workload.ID, spec)
	if err != nil {
		return fmt.Errorf("failed to generate domain XML: %w", err)
	}

	// Define the domain
	domain, err := v.conn.DomainDefineXML(domainXML)
	if err != nil {
		return fmt.Errorf("failed to define domain: %w", err)
	}

	v.logger.Infof("Created VM domain: %s (UUID: %s)", workload.ID, domain.UUID)
	return nil
}

func (v *VMRuntime) Start(ctx context.Context, id string) error {
	domain, found, err := v.lookupDomain(id)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("failed to start domain %s: domain not found", id)
	}

	state, _, err := v.conn.DomainGetState(domain, 0)
	if err != nil {
		return fmt.Errorf("failed to get domain state for %s: %w", id, err)
	}

	switch state {
	case int32(libvirt.DomainRunning):
		v.logger.Infof("VM already running, skipping start: %s", id)
		return nil
	case int32(libvirt.DomainPaused):
		if err := v.conn.DomainResume(domain); err != nil {
			return fmt.Errorf("failed to resume paused domain %s: %w", id, err)
		}
		v.logger.Infof("Resumed paused VM: %s", id)
		return nil
	default:
		if err := v.conn.DomainCreate(domain); err != nil {
			return fmt.Errorf("failed to start domain %s: %w", id, err)
		}
		v.logger.Infof("Started VM: %s", id)
		return nil
	}
}

func (v *VMRuntime) Stop(ctx context.Context, id string) error {
	domain, found, err := v.lookupDomain(id)
	if err != nil {
		return err
	}
	if !found {
		v.logger.Infof("VM already absent while stopping, treating as stopped: %s", id)
		return nil
	}

	state, _, err := v.conn.DomainGetState(domain, 0)
	if err != nil {
		return fmt.Errorf("failed to get domain state for %s: %w", id, err)
	}

	if state == int32(libvirt.DomainShutoff) || state == int32(libvirt.DomainShutdown) {
		v.logger.Infof("VM already stopped, skipping stop: %s", id)
		return nil
	}

	// Attempt graceful shutdown first. Paused guests often ignore ACPI shutdown.
	if err := v.conn.DomainShutdown(domain); err != nil {
		v.logger.Warnf("Graceful shutdown failed for %s, forcing stop: %v", id, err)
		if err := v.conn.DomainDestroy(domain); err != nil && !isDomainNotFoundErr(err) {
			return fmt.Errorf("failed to force stop domain %s: %w", id, err)
		}
		v.logger.Infof("Force stopped VM: %s", id)
		return nil
	}

	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	deadlineCtx, cancel := context.WithTimeout(waitCtx, vmShutdownGracePeriod)
	defer cancel()

	ticker := time.NewTicker(vmShutdownPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-deadlineCtx.Done():
			v.logger.Warnf("Timed out waiting for graceful shutdown for %s, forcing stop", id)
			if err := v.conn.DomainDestroy(domain); err != nil && !isDomainNotFoundErr(err) {
				return fmt.Errorf("failed to force stop domain %s after timeout: %w", id, err)
			}
			v.logger.Infof("Force stopped VM after timeout: %s", id)
			return nil
		case <-ticker.C:
			curState, _, stateErr := v.conn.DomainGetState(domain, 0)
			if stateErr != nil {
				if isDomainNotFoundErr(stateErr) {
					v.logger.Infof("VM disappeared during stop, treating as stopped: %s", id)
					return nil
				}
				return fmt.Errorf("failed to verify shutdown state for %s: %w", id, stateErr)
			}
			if curState == int32(libvirt.DomainShutoff) || curState == int32(libvirt.DomainShutdown) {
				v.logger.Infof("Stopped VM: %s", id)
				return nil
			}
		}
	}
}

func (v *VMRuntime) Delete(ctx context.Context, id string) error {
	domain, found, err := v.lookupDomain(id)
	if err != nil {
		return err
	}
	if !found {
		// Domain may already be gone, but cleanup deterministic artifacts.
		v.cleanupDeterministicVMArtifacts(id)
		v.logger.Infof("VM domain already absent, cleaned deterministic artifacts: %s", id)
		return nil
	}

	diskPaths := []string{}
	domainXML, xmlErr := v.conn.DomainGetXMLDesc(domain, libvirt.DomainXMLFlags(0))
	if xmlErr != nil {
		v.logger.Warnf("Failed to fetch domain XML for %s during delete: %v", id, xmlErr)
	} else {
		paths, parseErr := parseDiskSourceFilePaths(domainXML)
		if parseErr != nil {
			v.logger.Warnf("Failed to parse domain XML for %s during delete: %v", id, parseErr)
		} else {
			diskPaths = paths
		}
	}

	// Force-stop any active/paused domain before undefine.
	state, _, err := v.conn.DomainGetState(domain, 0)
	if err == nil && state != int32(libvirt.DomainShutdown) && state != int32(libvirt.DomainShutoff) {
		if destroyErr := v.conn.DomainDestroy(domain); destroyErr != nil && !isDomainNotFoundErr(destroyErr) {
			return fmt.Errorf("failed to destroy domain before undefine: %w", destroyErr)
		}
	}

	// Undefine the domain
	if err := v.conn.DomainUndefine(domain); err != nil && !isDomainNotFoundErr(err) {
		return fmt.Errorf("failed to undefine domain: %w", err)
	}

	v.cleanupDiskArtifacts(id, diskPaths)
	v.cleanupDeterministicVMArtifacts(id)
	v.clearSeedInfo(id)
	v.clearUsageSample(id)

	v.logger.Infof("Deleted VM: %s", id)
	return nil
}

func (v *VMRuntime) Status(ctx context.Context, id string) (models.ActualState, string, error) {
	domain, found, err := v.lookupDomain(id)
	if err != nil {
		return models.ActualStateUnknown, "", err
	}
	if !found {
		return models.ActualStateUnknown, "domain not found", nil
	}

	state, reason, err := v.conn.DomainGetState(domain, 0)
	if err != nil {
		return models.ActualStateUnknown, "", fmt.Errorf("failed to get domain state: %w", err)
	}

	var actualState models.ActualState
	var message string

	switch state {
	case int32(libvirt.DomainRunning):
		actualState = models.ActualStateRunning
		message = fmt.Sprintf("running (reason=%s)", vmDomainReasonText(state, reason))
	case int32(libvirt.DomainPaused):
		// Paused is treated as transitional so upper layers avoid destructive re-apply loops.
		actualState = models.ActualStatePending
		message = fmt.Sprintf("paused (runtime frozen, reason=%s)", vmDomainReasonText(state, reason))
	case int32(libvirt.DomainShutdown), int32(libvirt.DomainShutoff):
		actualState = models.ActualStateStopped
		message = fmt.Sprintf("shutdown (reason=%s)", vmDomainReasonText(state, reason))
	case int32(libvirt.DomainCrashed):
		actualState = models.ActualStateFailed
		message = fmt.Sprintf("crashed (reason=%s)", vmDomainReasonText(state, reason))
	case int32(libvirt.DomainBlocked), int32(libvirt.DomainNostate):
		actualState = models.ActualStatePending
		message = fmt.Sprintf("%s (reason=%s)", vmDomainStateName(state), vmDomainReasonText(state, reason))
	default:
		actualState = models.ActualStateUnknown
		message = fmt.Sprintf("unknown state=%d reason=%d", state, reason)
	}

	return actualState, message, nil
}

// StatusMetadata returns additional VM status context including IPs and interfaces.
func (v *VMRuntime) StatusMetadata(ctx context.Context, id string) (map[string]string, error) {
	domain, err := v.conn.DomainLookupByName(id)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup domain: %w", err)
	}
	metadata := make(map[string]string)

	state, reason, err := v.conn.DomainGetState(domain, 0)
	if err == nil {
		metadata["vm.domain_state"] = vmDomainStateName(state)
		metadata["vm.domain_state_code"] = strconv.Itoa(int(state))
		metadata["vm.domain_reason"] = vmDomainReasonText(state, reason)
		metadata["vm.domain_reason_code"] = strconv.Itoa(int(reason))
	}

	// Prefer DHCP lease source first, then guest agent fallback.
	ifaces, err := v.conn.DomainInterfaceAddresses(domain, uint32(libvirt.DomainInterfaceAddressesSrcLease), 0)
	if err != nil || len(ifaces) == 0 {
		ifaces, err = v.conn.DomainInterfaceAddresses(domain, uint32(libvirt.DomainInterfaceAddressesSrcAgent), 0)
		if err != nil {
			metadata["vm.network_lookup_error"] = err.Error()
			return metadata, nil
		}
	}
	var ips []string
	var macs []string
	var ifaceParts []string

	for _, iface := range ifaces {
		mac := ""
		if len(iface.Hwaddr) > 0 {
			mac = iface.Hwaddr[0]
		}
		if mac != "" {
			macs = append(macs, mac)
		}

		var ifaceIPs []string
		for _, addr := range iface.Addrs {
			if addr.Addr == "" {
				continue
			}
			ips = append(ips, addr.Addr)
			ifaceIPs = append(ifaceIPs, addr.Addr)
		}

		if iface.Name != "" {
			ifaceParts = append(ifaceParts, fmt.Sprintf("%s:%s", iface.Name, strings.Join(ifaceIPs, "|")))
		}
	}

	if len(ips) > 0 {
		metadata["vm.ip_addresses"] = strings.Join(ips, ",")
		metadata["vm.primary_ip"] = ips[0]
	}
	if len(macs) > 0 {
		metadata["vm.mac_addresses"] = strings.Join(macs, ",")
	}
	if len(ifaceParts) > 0 {
		metadata["vm.interfaces"] = strings.Join(ifaceParts, ",")
	}
	if seedInfo, ok := v.getSeedInfo(id); ok {
		metadata["vm.cloud_init_seed_path"] = seedInfo.Path
		metadata["vm.cloud_init_seed_checksum"] = seedInfo.Checksum
		metadata["vm.cloud_init_seed_size_bytes"] = strconv.Itoa(seedInfo.SizeBytes)
		metadata["vm.cloud_init_seed_prepared_at"] = seedInfo.PreparedAt.UTC().Format(time.RFC3339)
	}

	return metadata, nil
}

// UsageStats returns a point-in-time resource usage snapshot for the VM,
// implementing runtime.UsageStatsProvider.
//
// CPU: libvirt's DomainGetInfo only reports cumulative CPU time (ns) since the
// domain started, not a rate, so we track the previous sample per-domain and
// derive a percentage from the delta. The first call after startup (or after
// an agent restart) has no prior sample and reports 0% - this is expected and
// resolves itself on the next poll.
// Memory: taken from libvirt's RSS memory stat (actual resident memory),
// which is the closest analog to what Docker's stats API reports.
// Disk/Network: summed across every disk/interface device declared in the
// domain's live XML description.
func (v *VMRuntime) UsageStats(ctx context.Context, id string) (*models.WorkloadUsage, error) {
	domain, found, err := v.lookupDomain(id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("domain not found: %s", id)
	}

	state, _, _, nrVirtCPU, cpuTimeNs, err := v.conn.DomainGetInfo(domain)
	if err != nil {
		return nil, fmt.Errorf("failed to get domain info for %s: %w", id, err)
	}
	if libvirt.DomainState(state) != libvirt.DomainRunning {
		return nil, fmt.Errorf("domain %s is not running", id)
	}

	now := time.Now()
	var cpuPercent float64

	v.usageMu.Lock()
	if prev, ok := v.usageSamples[id]; ok && cpuTimeNs >= prev.cpuTimeNs {
		elapsedNs := now.Sub(prev.sampledAt).Nanoseconds()
		if elapsedNs > 0 {
			cpus := float64(nrVirtCPU)
			if cpus == 0 {
				cpus = 1
			}
			cpuDeltaNs := float64(cpuTimeNs - prev.cpuTimeNs)
			cpuPercent = (cpuDeltaNs / float64(elapsedNs)) / cpus * 100.0
		}
	}
	v.usageSamples[id] = vmUsageSample{cpuTimeNs: cpuTimeNs, sampledAt: now}
	v.usageMu.Unlock()

	usage := &models.WorkloadUsage{
		WorkloadID:  id,
		Type:        string(models.WorkloadTypeVM),
		CPUPercent:  cpuPercent,
		CollectedAt: now,
		Source:      "libvirt.domain_stats",
	}

	if memStats, memErr := v.conn.DomainMemoryStats(domain, uint32(libvirt.DomainMemoryStatNr), 0); memErr == nil {
		for _, stat := range memStats {
			if libvirt.DomainMemoryStatTags(stat.Tag) == libvirt.DomainMemoryStatRss {
				usage.MemoryBytes = int64(stat.Val) * 1024 // libvirt reports memory stats in KiB
				break
			}
		}
	} else {
		v.logger.Debugf("Failed to get memory stats for %s: %v", id, memErr)
	}

	domainXML, xmlErr := v.conn.DomainGetXMLDesc(domain, libvirt.DomainXMLFlags(0))
	if xmlErr != nil {
		v.logger.Debugf("Failed to fetch domain XML for %s during usage collection: %v", id, xmlErr)
		return usage, nil
	}

	diskDevices, ifaceDevices, parseErr := parseDiskAndInterfaceDevices(domainXML)
	if parseErr != nil {
		v.logger.Debugf("Failed to parse domain XML devices for %s: %v", id, parseErr)
		return usage, nil
	}

	var readBytes, writeBytes int64
	for _, dev := range diskDevices {
		if _, rdBytes, _, wrBytes, _, blkErr := v.conn.DomainBlockStats(domain, dev); blkErr == nil {
			if rdBytes > 0 {
				readBytes += rdBytes
			}
			if wrBytes > 0 {
				writeBytes += wrBytes
			}
		}
	}
	usage.DiskReadBytes = readBytes
	usage.DiskWriteBytes = writeBytes

	var rxBytes, txBytes int64
	for _, dev := range ifaceDevices {
		if rx, _, _, _, tx, _, _, _, ifaceErr := v.conn.DomainInterfaceStats(domain, dev); ifaceErr == nil {
			if rx > 0 {
				rxBytes += rx
			}
			if tx > 0 {
				txBytes += tx
			}
		}
	}
	usage.NetRXBytes = rxBytes
	usage.NetTXBytes = txBytes

	return usage, nil
}

// parseDiskAndInterfaceDevices extracts the libvirt device names (e.g. "vda",
// "vnet0") for every disk and network interface declared in a domain's live
// XML description, for use with DomainBlockStats/DomainInterfaceStats.
func parseDiskAndInterfaceDevices(domainXML string) (disks []string, interfaces []string, err error) {
	type devTarget struct {
		Dev string `xml:"dev,attr"`
	}
	type domainDisk struct {
		Target devTarget `xml:"target"`
	}
	type domainInterface struct {
		Target devTarget `xml:"target"`
	}
	type domainDevices struct {
		Disks      []domainDisk      `xml:"disk"`
		Interfaces []domainInterface `xml:"interface"`
	}
	type domainForUsage struct {
		Devices domainDevices `xml:"devices"`
	}

	var parsed domainForUsage
	if unmarshalErr := xml.Unmarshal([]byte(domainXML), &parsed); unmarshalErr != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal domain xml: %w", unmarshalErr)
	}

	for _, disk := range parsed.Devices.Disks {
		if dev := strings.TrimSpace(disk.Target.Dev); dev != "" {
			disks = append(disks, dev)
		}
	}
	for _, iface := range parsed.Devices.Interfaces {
		if dev := strings.TrimSpace(iface.Target.Dev); dev != "" {
			interfaces = append(interfaces, dev)
		}
	}
	return disks, interfaces, nil
}

func vmDomainStateName(state int32) string {
	switch libvirt.DomainState(state) {
	case libvirt.DomainRunning:
		return "running"
	case libvirt.DomainBlocked:
		return "blocked"
	case libvirt.DomainPaused:
		return "paused"
	case libvirt.DomainShutdown:
		return "shutdown"
	case libvirt.DomainShutoff:
		return "shutoff"
	case libvirt.DomainCrashed:
		return "crashed"
	case libvirt.DomainPmsuspended:
		return "pmsuspended"
	case libvirt.DomainNostate:
		return "nostate"
	default:
		return "unknown"
	}
}

func vmDomainReasonText(state, reason int32) string {
	switch libvirt.DomainState(state) {
	case libvirt.DomainPaused:
		switch libvirt.DomainPausedReason(reason) {
		case libvirt.DomainPausedUser:
			return "user"
		case libvirt.DomainPausedMigration:
			return "migration"
		case libvirt.DomainPausedSave:
			return "save"
		case libvirt.DomainPausedDump:
			return "dump"
		case libvirt.DomainPausedIoerror:
			return "io-error"
		case libvirt.DomainPausedWatchdog:
			return "watchdog"
		case libvirt.DomainPausedFromSnapshot:
			return "from-snapshot"
		case libvirt.DomainPausedShuttingDown:
			return "shutting-down"
		case libvirt.DomainPausedSnapshot:
			return "snapshot"
		case libvirt.DomainPausedCrashed:
			return "crashed"
		case libvirt.DomainPausedStartingUp:
			return "starting-up"
		case libvirt.DomainPausedPostcopy:
			return "postcopy"
		case libvirt.DomainPausedPostcopyFailed:
			return "postcopy-failed"
		default:
			return "unknown"
		}
	case libvirt.DomainShutoff:
		switch libvirt.DomainShutoffReason(reason) {
		case libvirt.DomainShutoffShutdown:
			return "shutdown"
		case libvirt.DomainShutoffDestroyed:
			return "destroyed"
		case libvirt.DomainShutoffCrashed:
			return "crashed"
		case libvirt.DomainShutoffMigrated:
			return "migrated"
		case libvirt.DomainShutoffSaved:
			return "saved"
		case libvirt.DomainShutoffFailed:
			return "failed"
		case libvirt.DomainShutoffFromSnapshot:
			return "from-snapshot"
		case libvirt.DomainShutoffDaemon:
			return "daemon"
		default:
			return "unknown"
		}
	case libvirt.DomainRunning:
		switch libvirt.DomainRunningReason(reason) {
		case libvirt.DomainRunningBooted:
			return "booted"
		case libvirt.DomainRunningMigrated:
			return "migrated"
		case libvirt.DomainRunningRestored:
			return "restored"
		case libvirt.DomainRunningFromSnapshot:
			return "from-snapshot"
		case libvirt.DomainRunningUnpaused:
			return "unpaused"
		case libvirt.DomainRunningMigrationCanceled:
			return "migration-canceled"
		case libvirt.DomainRunningSaveCanceled:
			return "save-canceled"
		case libvirt.DomainRunningWakeup:
			return "wakeup"
		case libvirt.DomainRunningCrashed:
			return "crashed"
		case libvirt.DomainRunningPostcopy:
			return "postcopy"
		default:
			return "unknown"
		}
	case libvirt.DomainShutdown:
		switch libvirt.DomainShutdownReason(reason) {
		case libvirt.DomainShutdownUser:
			return "user"
		default:
			return "unknown"
		}
	case libvirt.DomainCrashed:
		switch libvirt.DomainCrashedReason(reason) {
		case libvirt.DomainCrashedPanicked:
			return "panicked"
		default:
			return "unknown"
		}
	case libvirt.DomainPmsuspended:
		switch libvirt.DomainPMSuspendedReason(reason) {
		case libvirt.DomainPmsuspendedUnknown:
			return "unknown"
		default:
			return "unknown"
		}
	case libvirt.DomainNostate:
		switch libvirt.DomainNostateReason(reason) {
		case libvirt.DomainNostateUnknown:
			return "unknown"
		default:
			return "unknown"
		}
	case libvirt.DomainBlocked:
		switch libvirt.DomainBlockedReason(reason) {
		case libvirt.DomainBlockedUnknown:
			return "unknown"
		default:
			return "unknown"
		}
	default:
		return "unknown"
	}
}

func (v *VMRuntime) List(ctx context.Context) ([]string, error) {
	domains, _, err := v.conn.ConnectListAllDomains(1, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to list domains: %w", err)
	}

	var names []string
	for _, domain := range domains {
		names = append(names, domain.Name)
	}

	return names, nil
}

func (v *VMRuntime) Healthy(ctx context.Context) error {
	// Check connection by attempting to list domains
	_, _, err := v.conn.ConnectListAllDomains(1, 0)
	if err != nil {
		return fmt.Errorf("failed to check libvirt connection: %w", err)
	}
	return nil
}

// Helper functions

func (v *VMRuntime) parseSpec(specMap map[string]interface{}) (*models.VMSpec, error) {
	data, err := json.Marshal(specMap)
	if err != nil {
		return nil, err
	}

	var spec models.VMSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}

	return &spec, nil
}

// createDisk creates a QCOW2 disk image for the VM
func (v *VMRuntime) createDisk(diskCfg *models.DiskConfig) error {
	if diskCfg.Format == "" {
		diskCfg.Format = "qcow2"
	}
	// Ensure the directory exists
	diskDir := filepath.Dir(diskCfg.Path)
	if err := os.MkdirAll(diskDir, 0755); err != nil {
		return fmt.Errorf("failed to create disk directory %s: %w", diskDir, err)
	}

	// Skip if disk already exists
	if _, err := os.Stat(diskCfg.Path); err == nil {
		v.logger.Warnf("Disk already exists at %s, skipping creation", diskCfg.Path)
		return nil
	}

	// Create QCOW2 disk using qemu-img
	// Format: qemu-img create -f qcow2 /path/to/disk.qcow2 20G
	cmd := exec.Command(
		"qemu-img",
		"create",
		"-f", diskCfg.Format,
		diskCfg.Path,
		fmt.Sprintf("%dG", diskCfg.SizeGB),
	)

	// Run the command - this will succeed or fail based on permissions
	output, err := cmd.CombinedOutput()
	if err != nil {
		v.logger.Errorf("qemu-img output: %s", string(output))
		return fmt.Errorf("failed to create disk image: %w", err)
	}

	v.logger.Infof("Created QCOW2 disk: %s (%dGB)", diskCfg.Path, diskCfg.SizeGB)

	if err := os.WriteFile(managedDiskMarkerPath(diskCfg.Path), []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
		v.logger.Warnf("Failed to create managed-disk marker for %s: %v", diskCfg.Path, err)
	}

	return nil
}

func (v *VMRuntime) lookupDomain(id string) (libvirt.Domain, bool, error) {
	domain, err := v.conn.DomainLookupByName(id)
	if err != nil {
		if isDomainNotFoundErr(err) {
			return libvirt.Domain{}, false, nil
		}
		return libvirt.Domain{}, false, fmt.Errorf("failed to lookup domain %s: %w", id, err)
	}
	return domain, true, nil
}

func isDomainNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "domain not found") ||
		strings.Contains(msg, "no domain with matching name")
}

// createCloudInitISO creates a cloud-init ISO for VM configuration
func (v *VMRuntime) createCloudInitISO(vmID string, spec *models.VMSpec) (string, *cloudInitSeedInfo, error) {
	// Only create if cloud-init is specified
	if spec.CloudInit == "" && (spec.CloudInitConfig == nil || (spec.CloudInitConfig.UserData == "" && spec.CloudInitConfig.MetaData == "")) {
		return "", nil, nil
	}

	// Ensure CloudInitConfig exists
	if spec.CloudInitConfig == nil {
		spec.CloudInitConfig = &models.CloudInitConfig{}
	}

	// Create temporary directory for cloud-init files
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("cloud-init-%s-", vmID))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// --- Meta-data ---
	metaData := fmt.Sprintf(`{
  "instance-id": "%s",
  "hostname": "%s",
  "local-ipv4": "127.0.0.1"
}`, vmID, spec.Name)
	if strings.TrimSpace(spec.CloudInitConfig.MetaData) != "" {
		metaData = spec.CloudInitConfig.MetaData
	}
	if err := validateCloudInitField("meta-data", metaData); err != nil {
		return "", nil, err
	}

	metaDataPath := filepath.Join(tmpDir, "meta-data")
	if err := os.WriteFile(metaDataPath, []byte(metaData), 0644); err != nil {
		return "", nil, fmt.Errorf("failed to write meta-data: %w", err)
	}

	// --- User-data ---
	var userData string
	if spec.CloudInitConfig.UserData != "" {
		userData = spec.CloudInitConfig.UserData
	} else if spec.CloudInit != "" {
		userData = spec.CloudInit
	} else {
		userData = "#!/bin/bash\necho 'Cloud-init configured'\n"
	}
	if err := validateCloudInitField("user-data", userData); err != nil {
		return "", nil, err
	}

	userDataPath := filepath.Join(tmpDir, "user-data")
	if err := os.WriteFile(userDataPath, []byte(userData), 0644); err != nil {
		return "", nil, fmt.Errorf("failed to write user-data: %w", err)
	}

	// --- Network config ---
	networkConfig := strings.TrimSpace(spec.CloudInitConfig.NetworkConfig)
	if networkConfig == "" {
		// Provide a minimal default network config so cloud-init can configure network
		networkConfig = `version: 2
ethernets:
  all:
    dhcp4: true
`
	}
	if err := validateCloudInitField("network-config", networkConfig); err != nil {
		return "", nil, err
	}

	networkPath := filepath.Join(tmpDir, "network-config")
	if err := os.WriteFile(networkPath, []byte(networkConfig), 0644); err != nil {
		return "", nil, fmt.Errorf("failed to write network-config: %w", err)
	}

	// --- Vendor data (optional) ---
	paths := map[string]string{
		"user-data":      userDataPath,
		"meta-data":      metaDataPath,
		"network-config": networkPath,
	}

	if vendorData := strings.TrimSpace(spec.CloudInitConfig.VendorData); vendorData != "" {
		if err := validateCloudInitField("vendor-data", vendorData); err != nil {
			return "", nil, err
		}
		vendorPath := filepath.Join(tmpDir, "vendor-data")
		if err := os.WriteFile(vendorPath, []byte(vendorData), 0644); err != nil {
			return "", nil, fmt.Errorf("failed to write vendor-data: %w", err)
		}
		paths["vendor-data"] = vendorPath
	}

	// --- Check total payload size ---
	totalSize := len(userData) + len(metaData) + len(networkConfig) + len(spec.CloudInitConfig.VendorData)
	if totalSize > maxCloudInitPayloadBytes {
		return "", nil, cloudInitInvalidError(
			"payload size %d bytes exceeds limit %d bytes",
			totalSize, maxCloudInitPayloadBytes,
		)
	}

	// --- Generate ISO ---
	isoDir := "/var/lib/libvirt/images"
	if err := os.MkdirAll(isoDir, 0755); err != nil {
		v.logger.Warnf("Failed to create ISO directory %s, using /tmp", isoDir)
		isoDir = "/tmp"
	}
	isoPath := filepath.Join(isoDir, fmt.Sprintf("%s-cloud-init.iso", vmID))

	fileKeys := make([]string, 0, len(paths))
	for key := range paths {
		fileKeys = append(fileKeys, key)
	}
	sort.Strings(fileKeys)

	mkisofsArgs := []string{
		"-output", isoPath,
		"-volid", "cidata",
		"-joliet",
		"-rock",
		"-file-mode", "0644",
		"-dir-mode", "0755",
		"-graft-points",
	}
	for _, key := range fileKeys {
		mkisofsArgs = append(mkisofsArgs, fmt.Sprintf("%s=%s", key, paths[key]))
	}

	cmd := exec.Command("mkisofs", mkisofsArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		v.logger.Errorf("mkisofs output: %s", string(output))
		return "", nil, fmt.Errorf("failed to create cloud-init ISO: %w", err)
	}

	checksum := cloudInitSeedChecksum(fileKeys, map[string]string{
		"user-data":      userData,
		"meta-data":      metaData,
		"network-config": networkConfig,
		"vendor-data":    spec.CloudInitConfig.VendorData,
	})

	v.logger.WithFields(logrus.Fields{
		"vm_id":           vmID,
		"seed_path":       isoPath,
		"seed_checksum":   checksum,
		"seed_size_bytes": totalSize,
		"seed_files":      strings.Join(fileKeys, ","),
	}).Info("Created cloud-init seed ISO")

	return isoPath, &cloudInitSeedInfo{
		Path:       isoPath,
		Checksum:   checksum,
		SizeBytes:  totalSize,
		PreparedAt: time.Now().UTC(),
	}, nil
}

func validateCloudInitField(name, value string) error {
	if len(value) > maxCloudInitPayloadBytes {
		return cloudInitInvalidError("%s size %d bytes exceeds limit %d bytes", name, len(value), maxCloudInitPayloadBytes)
	}
	if strings.ContainsRune(value, '\x00') {
		return cloudInitInvalidError("%s contains null byte", name)
	}
	return nil
}

func cloudInitInvalidError(format string, args ...interface{}) error {
	return fmt.Errorf("cloud-init-invalid: %s", fmt.Sprintf(format, args...))
}

func cloudInitSeedChecksum(sortedKeys []string, payload map[string]string) string {
	hasher := sha256.New()
	for _, key := range sortedKeys {
		value := payload[key]
		if value == "" {
			continue
		}
		_, _ = hasher.Write([]byte(key))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(value))
		_, _ = hasher.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func (v *VMRuntime) setSeedInfo(vmID string, info cloudInitSeedInfo) {
	v.seedMu.Lock()
	v.seedInfo[vmID] = info
	v.seedMu.Unlock()
}

// clearUsageSample removes the cached CPU-time sample for a deleted domain so
// the map doesn't grow unbounded across the lifetime of the agent.
func (v *VMRuntime) clearUsageSample(vmID string) {
	v.usageMu.Lock()
	delete(v.usageSamples, vmID)
	v.usageMu.Unlock()
}

func (v *VMRuntime) clearSeedInfo(vmID string) {
	v.seedMu.Lock()
	delete(v.seedInfo, vmID)
	v.seedMu.Unlock()
}

func (v *VMRuntime) getSeedInfo(vmID string) (cloudInitSeedInfo, bool) {
	v.seedMu.RLock()
	info, ok := v.seedInfo[vmID]
	v.seedMu.RUnlock()
	return info, ok
}

func managedDiskMarkerPath(diskPath string) string {
	return diskPath + managedDiskMarkerSuffix
}

func isNetworkDiskPath(path string) bool {
	_, ok := diskSourceFromPath(path)
	return ok
}

func diskSourceFromPath(path string) (DiskSource, bool) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return DiskSource{}, false
	}

	if strings.HasPrefix(trimmed, "rbd:") {
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, "rbd:"))
		if name == "" {
			return DiskSource{}, false
		}
		return DiskSource{
			Protocol: "rbd",
			Name:     name,
		}, true
	}

	if strings.HasPrefix(trimmed, "nfs://") {
		parsed, err := neturl.Parse(trimmed)
		if err != nil || parsed.Host == "" || parsed.Path == "" {
			return DiskSource{}, false
		}
		host := parsed.Hostname()
		if host == "" {
			return DiskSource{}, false
		}
		source := DiskSource{
			Protocol: "nfs",
			Name:     parsed.Path,
			Hosts:    []DiskSourceHost{{Name: host}},
		}
		if port := parsed.Port(); port != "" {
			source.Hosts[0].Port = port
		}
		return source, true
	}

	// Backward compatibility for existing NFS handles in "server:/export/path" form.
	if host, export, ok := strings.Cut(trimmed, ":/"); ok {
		host = strings.TrimSpace(host)
		export = strings.TrimSpace(export)
		if host != "" && export != "" {
			return DiskSource{
				Protocol: "nfs",
				Name:     "/" + export,
				Hosts:    []DiskSourceHost{{Name: host}},
			}, true
		}
	}

	return DiskSource{}, false
}

func expectedCloudInitISOPaths(vmID string) []string {
	return []string{
		filepath.Join("/var/lib/libvirt/images", fmt.Sprintf("%s-cloud-init.iso", vmID)),
		filepath.Join("/tmp", fmt.Sprintf("%s-cloud-init.iso", vmID)),
	}
}

func isCloudInitISOForVM(vmID, path string) bool {
	return filepath.Base(path) == fmt.Sprintf("%s-cloud-init.iso", vmID)
}

func parseDiskSourceFilePaths(domainXML string) ([]string, error) {
	type domainDiskSource struct {
		File string `xml:"file,attr"`
	}
	type domainDisk struct {
		Type   string           `xml:"type,attr"`
		Source domainDiskSource `xml:"source"`
	}
	type domainDevices struct {
		Disks []domainDisk `xml:"disk"`
	}
	type domainForCleanup struct {
		Devices domainDevices `xml:"devices"`
	}

	var parsed domainForCleanup
	if err := xml.Unmarshal([]byte(domainXML), &parsed); err != nil {
		return nil, fmt.Errorf("failed to unmarshal domain xml: %w", err)
	}

	var paths []string
	for _, disk := range parsed.Devices.Disks {
		if disk.Type != "file" || disk.Source.File == "" {
			continue
		}
		paths = append(paths, disk.Source.File)
	}
	return paths, nil
}

func (v *VMRuntime) cleanupDeterministicVMArtifacts(vmID string) {
	for _, path := range expectedCloudInitISOPaths(vmID) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			v.logger.Warnf("Failed to remove cloud-init ISO %s: %v", path, err)
		}
	}
}

func (v *VMRuntime) cleanupDiskArtifacts(vmID string, diskPaths []string) {
	for _, diskPath := range diskPaths {
		remove := false
		if isCloudInitISOForVM(vmID, diskPath) {
			remove = true
		} else {
			if _, err := os.Stat(managedDiskMarkerPath(diskPath)); err == nil {
				remove = true
			}
		}

		if !remove {
			continue
		}

		if err := os.Remove(diskPath); err != nil && !os.IsNotExist(err) {
			v.logger.Warnf("Failed to remove managed VM disk %s: %v", diskPath, err)
			continue
		}

		if err := os.Remove(managedDiskMarkerPath(diskPath)); err != nil && !os.IsNotExist(err) {
			v.logger.Warnf("Failed to remove managed-disk marker for %s: %v", diskPath, err)
		}
	}
}

// Helper functions
type Domain struct {
	XMLName    xml.Name `xml:"domain"`
	Type       string   `xml:"type,attr"`
	Name       string   `xml:"name"`
	Memory     Memory   `xml:"memory"`
	CurrentMem Memory   `xml:"currentMemory"`
	VCPU       VCPU     `xml:"vcpu"`
	OS         OS       `xml:"os"`
	Features   Features `xml:"features"`
	CPU        CPU      `xml:"cpu"`
	Clock      Clock    `xml:"clock"`
	Devices    Devices  `xml:"devices"`
}

type Memory struct {
	Unit  string `xml:"unit,attr"`
	Value int64  `xml:",chardata"`
}

type VCPU struct {
	Placement string `xml:"placement,attr"`
	Value     int    `xml:",chardata"`
}

type OS struct {
	Type OSType `xml:"type"`
}

type OSType struct {
	Arch    string `xml:"arch,attr"`
	Machine string `xml:"machine,attr"`
	Value   string `xml:",chardata"`
}

type Boot struct {
	Dev   string `xml:"dev,attr,omitempty"`   // OS boot device (hd, cdrom, etc.)
	Order string `xml:"order,attr,omitempty"` // Disk boot order (1, 2, etc.)
}

type Features struct {
	ACPI struct{} `xml:"acpi"`
	APIC struct{} `xml:"apic"`
}

type CPU struct {
	Mode string `xml:"mode,attr"`
}

type Clock struct {
	Offset string `xml:"offset,attr"`
}

type Devices struct {
	Emulator   string      `xml:"emulator"`
	Disks      []Disk      `xml:"disk"`
	Interfaces []Interface `xml:"interface"`
	Serial     Serial      `xml:"serial"`
	Console    Console     `xml:"console"`
	Graphics   Graphics    `xml:"graphics"`
}

type Disk struct {
	Type   string     `xml:"type,attr"`
	Device string     `xml:"device,attr"`
	Driver DiskDriver `xml:"driver"`
	Source DiskSource `xml:"source"`
	Target DiskTarget `xml:"target"`
	Boot   *Boot      `xml:"boot,omitempty"` // Boot order
}

type DiskDriver struct {
	Name string `xml:"name,attr"`
	Type string `xml:"type,attr"`
}

type DiskSource struct {
	File     string           `xml:"file,attr,omitempty"`
	Protocol string           `xml:"protocol,attr,omitempty"`
	Name     string           `xml:"name,attr,omitempty"`
	Hosts    []DiskSourceHost `xml:"host,omitempty"`
}

type DiskSourceHost struct {
	Name      string `xml:"name,attr,omitempty"`
	Port      string `xml:"port,attr,omitempty"`
	Transport string `xml:"transport,attr,omitempty"`
}

type DiskTarget struct {
	Dev string `xml:"dev,attr"`
	Bus string `xml:"bus,attr"`
}

type Interface struct {
	Type   string          `xml:"type,attr"`
	MAC    MAC             `xml:"mac"`
	Source InterfaceSource `xml:"source"`
	Model  InterfaceModel  `xml:"model"`
}

type MAC struct {
	Address string `xml:"address,attr"`
}

type InterfaceSource struct {
	Network string `xml:"network,attr,omitempty"`
	Bridge  string `xml:"bridge,attr,omitempty"`
}

type InterfaceModel struct {
	Type string `xml:"type,attr"`
}

type Serial struct {
	Type   string       `xml:"type,attr"`
	Target SerialTarget `xml:"target"`
}

type Console struct {
	Type   string        `xml:"type,attr"`
	Target ConsoleTarget `xml:"target"`
}

type SerialTarget struct {
	Port string `xml:"port,attr"`
}

type ConsoleTarget struct {
	Type string `xml:"type,attr"`
	Port string `xml:"port,attr"`
}

type Graphics struct {
	Type   string `xml:"type,attr"`
	Port   string `xml:"port,attr"`
	Listen string `xml:"listen,attr"`
}

func (v *VMRuntime) generateDomainXML(name string, spec *models.VMSpec) (string, error) {
	domain := Domain{
		Type: "kvm",
		Name: name,
		Memory: Memory{
			Unit:  "MiB",
			Value: spec.MemoryMB,
		},
		CurrentMem: Memory{
			Unit:  "MiB",
			Value: spec.MemoryMB,
		},
		VCPU: VCPU{
			Placement: "static",
			Value:     spec.VCPUs,
		},
		OS: OS{
			Type: OSType{
				Arch:    "x86_64",
				Machine: "pc-q35-5.2",
				Value:   "hvm",
			},
		},
		Features: Features{},
		CPU:      CPU{Mode: "host-passthrough"},
		Clock:    Clock{Offset: "utc"},
		Devices: Devices{
			Emulator: "/usr/bin/qemu-system-x86_64",
			Serial:   Serial{Type: "pty", Target: SerialTarget{Port: "0"}},
			Console:  Console{Type: "pty", Target: ConsoleTarget{Type: "serial", Port: "0"}},
			Graphics: Graphics{Type: "vnc", Port: "-1", Listen: "0.0.0.0"},
		},
	}

	// Add disks and CDROMs
	bootOrder := 1
	for _, diskCfg := range spec.Disks {
		device := "disk"
		bus := "virtio"

		if diskCfg.Type == "cdrom" {
			device = "cdrom"
			bus = "sata"
		}

		diskType := "file"
		source := DiskSource{File: diskCfg.Path}
		if networkSource, ok := diskSourceFromPath(diskCfg.Path); ok {
			diskType = "network"
			source = networkSource
		}

		disk := Disk{
			Type:   diskType,
			Device: device,
			Driver: DiskDriver{Name: "qemu", Type: diskCfg.Format},
			Source: source,
			Target: DiskTarget{Dev: diskCfg.Device, Bus: bus},
		}

		// Set boot order for bootable disks
		if diskCfg.Boot {
			disk.Boot = &Boot{Order: fmt.Sprintf("%d", bootOrder)}
			bootOrder++
		}

		domain.Devices.Disks = append(domain.Devices.Disks, disk)
	}

	// Add network interfaces
	for _, netCfg := range spec.Networks {
		iface := Interface{
			Type: "network",
			MAC:  MAC{Address: netCfg.MACAddress},
			Source: InterfaceSource{
				Network: netCfg.Network,
			},
			Model: InterfaceModel{Type: "virtio"},
		}
		domain.Devices.Interfaces = append(domain.Devices.Interfaces, iface)
	}

	// Marshal to XML
	xmlData, err := xml.MarshalIndent(domain, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal domain XML: %w", err)
	}

	// If cloud-init is configured, we'll inject it via metadata
	// This will be handled by libvirt cloud-init datasource if available
	return xml.Header + string(xmlData), nil
}
