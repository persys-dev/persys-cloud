package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	"github.com/sirupsen/logrus"
)

// DockerRuntime manages Docker container workloads
type DockerRuntime struct {
	client *client.Client
	logger *logrus.Entry
}

const (
	managedLabelKey      = "persys.managed"
	managedWorkloadIDKey = "persys.workload_id"
	managedRevisionKey   = "persys.revision_id"
)

// NewDockerRuntime creates a new Docker runtime
func NewDockerRuntime(endpoint string, logger *logrus.Logger) (*DockerRuntime, error) {
	var cli *client.Client
	var err error

	if endpoint != "" {
		cli, err = client.NewClientWithOpts(
			client.WithHost(endpoint),
			client.WithAPIVersionNegotiation(),
		)
	} else {
		cli, err = client.NewClientWithOpts(
			client.FromEnv,
			client.WithAPIVersionNegotiation(),
		)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	return &DockerRuntime{
		client: cli,
		logger: logger.WithField("runtime", "docker"),
	}, nil
}

func (d *DockerRuntime) Type() models.WorkloadType {
	return models.WorkloadTypeContainer
}

func (d *DockerRuntime) Create(ctx context.Context, workload *models.Workload) error {
	spec, err := d.parseSpec(workload.Spec)
	if err != nil {
		return fmt.Errorf("failed to parse container spec: %w", err)
	}

	// Pull image with retry logic
	d.logger.Infof("Pulling image: %s (this may take a while)", spec.Image)
	err = d.pullImageWithRetry(ctx, spec.Image, 3)
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	d.logger.Infof("Successfully pulled image: %s", spec.Image)

	// Build container config
	labels := make(map[string]string, len(spec.Labels)+2)
	for k, v := range spec.Labels {
		labels[k] = v
	}
	// Force ownership labels so GC/reconciliation only targets agent-managed containers.
	labels[managedLabelKey] = "true"
	labels[managedWorkloadIDKey] = workload.ID
	labels[managedRevisionKey] = strings.TrimSpace(workload.RevisionID)

	reused, err := d.ensureContainerReadyForCreate(ctx, workload)
	if err != nil {
		return err
	}
	if reused {
		return nil
	}

	containerConfig := &container.Config{
		Image:  spec.Image,
		Cmd:    spec.Command,
		Env:    d.buildEnv(spec.Env),
		Labels: labels,
	}

	if len(spec.Args) > 0 {
		containerConfig.Cmd = append(containerConfig.Cmd, spec.Args...)
	}

	// Build host config
	hostConfig := &container.HostConfig{
		RestartPolicy: d.buildRestartPolicy(spec.RestartPolicy),
		Mounts:        d.buildMounts(spec.Volumes),
		PortBindings:  d.buildPortBindings(spec.Ports),
	}

	if spec.Resources != nil {
		hostConfig.Resources = container.Resources{
			CPUShares: spec.Resources.CPUShares,
			Memory:    spec.Resources.MemoryBytes,
		}
	}

	// Add exposed ports to config
	if len(spec.Ports) > 0 {
		containerConfig.ExposedPorts = d.buildExposedPorts(spec.Ports)
	}

	// Create container
	resp, err := d.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, workload.ID)
	if err != nil {
		if !isContainerNameConflict(err) {
			return fmt.Errorf("failed to create container: %w", err)
		}

		d.logger.Warnf("Container create reported name conflict for %s; inspecting existing container", workload.ID)
		reused, conflictErr := d.ensureContainerReadyForCreate(ctx, workload)
		if conflictErr != nil {
			return conflictErr
		}
		if reused {
			return nil
		}

		resp, err = d.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, workload.ID)
		if err != nil {
			return fmt.Errorf("failed to create container after resolving name conflict: %w", err)
		}
	}

	d.logger.Infof("Created container: %s (%s)", workload.ID, resp.ID)
	return nil
}

func (d *DockerRuntime) Start(ctx context.Context, id string) error {
	if err := d.client.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}
	d.logger.Infof("Started container: %s", id)
	return nil
}

func (d *DockerRuntime) Stop(ctx context.Context, id string) error {
	timeout := 30
	if err := d.client.ContainerStop(ctx, id, container.StopOptions{
		Timeout: &timeout,
	}); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}
	d.logger.Infof("Stopped container: %s", id)
	return nil
}

func (d *DockerRuntime) Delete(ctx context.Context, id string) error {
	// Stop first if running
	_ = d.Stop(ctx, id)

	if err := d.client.ContainerRemove(ctx, id, container.RemoveOptions{
		Force: true,
	}); err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}
	d.logger.Infof("Deleted container: %s", id)
	return nil
}

func (d *DockerRuntime) Status(ctx context.Context, id string) (models.ActualState, string, error) {
	info, err := d.client.ContainerInspect(ctx, id)
	if err != nil {
		if client.IsErrNotFound(err) {
			return models.ActualStateUnknown, "container not found", nil
		}
		return models.ActualStateUnknown, "", fmt.Errorf("failed to inspect container: %w", err)
	}

	state := models.ActualStateUnknown
	message := info.State.Status

	switch {
	case info.State.Running:
		state = models.ActualStateRunning
	case info.State.Paused:
		state = models.ActualStateStopped
		message = "paused"
	case info.State.Restarting:
		state = models.ActualStatePending
		message = "restarting"
	case info.State.Dead:
		state = models.ActualStateFailed
		message = fmt.Sprintf("dead: %s", info.State.Error)
	case info.State.ExitCode != 0:
		state = models.ActualStateFailed
		message = fmt.Sprintf("exited with code %d: %s", info.State.ExitCode, info.State.Error)
	default:
		state = models.ActualStateStopped
	}

	return state, message, nil
}

func (d *DockerRuntime) List(ctx context.Context) ([]string, error) {
	filter := filters.NewArgs()
	filter.Add("label", managedLabelKey+"=true")

	containers, err := d.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filter,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	var ids []string
	for _, c := range containers {
		// Use names if available, otherwise use ID
		if len(c.Names) > 0 {
			name := strings.TrimPrefix(c.Names[0], "/")
			ids = append(ids, name)
		} else {
			ids = append(ids, c.ID[:12])
		}
	}

	return ids, nil
}

func (d *DockerRuntime) Healthy(ctx context.Context) error {
	_, err := d.client.Ping(ctx)
	return err
}

// StatusMetadata returns container-specific status details, including stderr when exited.
func (d *DockerRuntime) StatusMetadata(ctx context.Context, id string) (map[string]string, error) {
	info, err := d.client.ContainerInspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	metadata := map[string]string{
		"container.id":    shortContainerID(info.ID),
		"container.state": containerStateText(&info),
	}
	if info.Config != nil {
		if image := strings.TrimSpace(info.Config.Image); image != "" {
			metadata["container.image"] = image
		}
	}
	if info.State != nil {
		metadata["container.exit_code"] = fmt.Sprintf("%d", info.State.ExitCode)
		if started := strings.TrimSpace(info.State.StartedAt); started != "" {
			metadata["container.started_at"] = started
		}
		if finished := strings.TrimSpace(info.State.FinishedAt); finished != "" {
			metadata["container.finished_at"] = finished
		}
		if stateErr := strings.TrimSpace(info.State.Error); stateErr != "" {
			metadata["container.runtime_error"] = stateErr
		}
	}

	if info.State != nil && (info.State.ExitCode != 0 || info.State.Dead) {
		stderrText, stderrErr := d.getContainerStderr(ctx, id)
		if stderrErr != nil {
			metadata["container.stderr_error"] = stderrErr.Error()
		} else if strings.TrimSpace(stderrText) != "" {
			metadata["container.stderr"] = stderrText
		}
	}

	return metadata, nil
}

// UsageStats returns a point-in-time resource usage snapshot for the container,
// implementing runtime.UsageStatsProvider. It uses Docker's one-shot stats
// endpoint (no 1-second priming wait), so it's safe to call on every status
// refresh/heartbeat cycle without materially slowing it down.
func (d *DockerRuntime) UsageStats(ctx context.Context, id string) (*models.WorkloadUsage, error) {
	stats, err := d.client.ContainerStatsOneShot(ctx, id)
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil, fmt.Errorf("container not found: %s", id)
		}
		return nil, fmt.Errorf("failed to fetch container stats: %w", err)
	}
	defer stats.Body.Close()

	var raw types.StatsJSON
	if err := json.NewDecoder(stats.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to decode container stats: %w", err)
	}

	usage := &models.WorkloadUsage{
		WorkloadID:  id,
		Type:        string(models.WorkloadTypeContainer),
		CPUPercent:  dockerCPUPercent(&raw),
		MemoryBytes: int64(dockerMemoryUsageNoCache(&raw.MemoryStats)),
		CollectedAt: time.Now(),
		Source:      "docker.stats",
	}

	var rxTotal, txTotal uint64
	for _, netStats := range raw.Networks {
		rxTotal += netStats.RxBytes
		txTotal += netStats.TxBytes
	}
	usage.NetRXBytes = int64(rxTotal)
	usage.NetTXBytes = int64(txTotal)

	var readTotal, writeTotal uint64
	for _, entry := range raw.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(entry.Op) {
		case "read":
			readTotal += entry.Value
		case "write":
			writeTotal += entry.Value
		}
	}
	usage.DiskReadBytes = int64(readTotal)
	usage.DiskWriteBytes = int64(writeTotal)

	return usage, nil
}

// dockerCPUPercent mirrors the CPU% calculation used by `docker stats`:
// (delta of container CPU time / delta of total system CPU time) * online CPUs.
// Returns 0 rather than erroring when there isn't yet a valid previous sample
// (system_cpu_usage delta <= 0), which can happen on the very first sample
// after a container starts.
func dockerCPUPercent(v *types.StatsJSON) float64 {
	cpuDelta := float64(v.CPUStats.CPUUsage.TotalUsage) - float64(v.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(v.CPUStats.SystemUsage) - float64(v.PreCPUStats.SystemUsage)
	if systemDelta <= 0 || cpuDelta < 0 {
		return 0
	}

	onlineCPUs := float64(v.CPUStats.OnlineCPUs)
	if onlineCPUs == 0 {
		onlineCPUs = float64(len(v.CPUStats.CPUUsage.PercpuUsage))
	}
	if onlineCPUs == 0 {
		onlineCPUs = 1
	}

	return (cpuDelta / systemDelta) * onlineCPUs * 100.0
}

// dockerMemoryUsageNoCache subtracts reclaimable page cache from the raw cgroup
// usage counter, matching `docker stats`' behavior. Without this, memory usage
// is dominated by page cache and looks alarmingly high for any container doing
// file I/O. Handles both cgroup v1 ("total_inactive_file") and v2
// ("inactive_file") stat keys.
func dockerMemoryUsageNoCache(mem *types.MemoryStats) uint64 {
	if v, ok := mem.Stats["total_inactive_file"]; ok && v < mem.Usage {
		return mem.Usage - v
	}
	if v, ok := mem.Stats["inactive_file"]; ok && v < mem.Usage {
		return mem.Usage - v
	}
	return mem.Usage
}

func (d *DockerRuntime) ensureContainerReadyForCreate(ctx context.Context, workload *models.Workload) (bool, error) {
	info, err := d.client.ContainerInspect(ctx, workload.ID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect container %s before create: %w", workload.ID, err)
	}

	labels := map[string]string{}
	if info.Config != nil && info.Config.Labels != nil {
		labels = info.Config.Labels
	}
	state := containerStateText(&info)

	isManaged := strings.EqualFold(strings.TrimSpace(labels[managedLabelKey]), "true")
	managedWorkloadID := strings.TrimSpace(labels[managedWorkloadIDKey])
	existingRevision := strings.TrimSpace(labels[managedRevisionKey])
	desiredRevision := strings.TrimSpace(workload.RevisionID)

	if isManaged && managedWorkloadID == workload.ID {
		if desiredRevision == "" || existingRevision == "" || existingRevision == desiredRevision {
			d.logger.WithFields(logrus.Fields{
				"workload_id":       workload.ID,
				"container_id":      shortContainerID(info.ID),
				"status":            state,
				"existing_revision": existingRevision,
			}).Warn("Container already exists for managed workload; reusing existing container")
			return true, nil
		}

		d.logger.WithFields(logrus.Fields{
			"workload_id":       workload.ID,
			"container_id":      shortContainerID(info.ID),
			"status":            state,
			"existing_revision": existingRevision,
			"desired_revision":  desiredRevision,
		}).Warn("Found stale managed container with revision mismatch; removing before recreate")

		if err := d.client.ContainerRemove(ctx, workload.ID, container.RemoveOptions{Force: true}); err != nil {
			return false, fmt.Errorf(
				"failed to remove stale managed container %s (id=%s status=%s existing_revision=%s desired_revision=%s): %w",
				workload.ID,
				shortContainerID(info.ID),
				state,
				existingRevision,
				desiredRevision,
				err,
			)
		}

		return false, nil
	}

	return false, fmt.Errorf(
		"container name %q is already in use (id=%s status=%s managed=%t managed_workload_id=%q managed_revision=%q)",
		workload.ID,
		shortContainerID(info.ID),
		state,
		isManaged,
		managedWorkloadID,
		existingRevision,
	)
}

// Helper functions

func isContainerNameConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "is already in use by container") ||
		strings.Contains(msg, "already exists")
}

func containerStateText(info *types.ContainerJSON) string {
	if info == nil || info.State == nil {
		return "unknown"
	}
	if strings.TrimSpace(info.State.Status) != "" {
		return strings.TrimSpace(info.State.Status)
	}
	if info.State.Running {
		return "running"
	}
	if info.State.Paused {
		return "paused"
	}
	if info.State.Restarting {
		return "restarting"
	}
	return "unknown"
}

func shortContainerID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func (d *DockerRuntime) getContainerStderr(ctx context.Context, id string) (string, error) {
	reader, err := d.client.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: false,
		ShowStderr: true,
		Timestamps: false,
		Details:    false,
		Follow:     false,
	})
	if err != nil {
		return "", fmt.Errorf("failed to read container logs: %w", err)
	}
	defer reader.Close()

	raw, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read container stderr stream: %w", err)
	}
	if len(raw) == 0 {
		return "", nil
	}

	var stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(io.Discard, &stderr, bytes.NewReader(raw)); err != nil {
		// Non-multiplexed stream (e.g. TTY-enabled); treat raw output as stderr.
		return strings.TrimSpace(string(raw)), nil
	}
	return strings.TrimSpace(stderr.String()), nil
}

func (d *DockerRuntime) pullImageWithRetry(ctx context.Context, image string, maxRetries int) error {
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		d.logger.Debugf("Pulling image %s (attempt %d/%d)", image, attempt, maxRetries)

		reader, err := d.client.ImagePull(ctx, image, types.ImagePullOptions{})
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				backoff := time.Duration(attempt) * 5 * time.Second
				d.logger.Warnf("Image pull failed (attempt %d/%d): %v, retrying in %s",
					attempt, maxRetries, err, backoff)
				select {
				case <-time.After(backoff):
					// Continue to next attempt
				case <-ctx.Done():
					return fmt.Errorf("context cancelled during image pull retry: %w", ctx.Err())
				}
			} else {
				d.logger.Errorf("Image pull failed after %d attempts: %v", maxRetries, err)
			}
			continue
		}
		defer reader.Close()

		// Consume and log pull output
		_, pullErr := io.Copy(io.Discard, reader)
		if pullErr != nil {
			lastErr = pullErr
			if attempt < maxRetries {
				backoff := time.Duration(attempt) * 5 * time.Second
				d.logger.Warnf("Error reading pull output (attempt %d/%d): %v, retrying in %s",
					attempt, maxRetries, pullErr, backoff)
				select {
				case <-time.After(backoff):
					// Continue to next attempt
				case <-ctx.Done():
					return fmt.Errorf("context cancelled during image pull: %w", ctx.Err())
				}
			}
			continue
		}

		// Success
		d.logger.Infof("Image pull successful: %s", image)
		return nil
	}

	return lastErr
}

func (d *DockerRuntime) parseSpec(specMap map[string]interface{}) (*models.ContainerSpec, error) {
	data, err := json.Marshal(specMap)
	if err != nil {
		return nil, err
	}

	var spec models.ContainerSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}

	return &spec, nil
}

func (d *DockerRuntime) buildEnv(env map[string]string) []string {
	var result []string
	for k, v := range env {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}

func (d *DockerRuntime) buildMounts(volumes []models.VolumeMount) []mount.Mount {
	var mounts []mount.Mount
	for _, v := range volumes {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   v.HostPath,
			Target:   v.ContainerPath,
			ReadOnly: v.ReadOnly,
		})
	}
	return mounts
}

func (d *DockerRuntime) buildPortBindings(ports []models.PortMapping) nat.PortMap {
	portMap := nat.PortMap{}
	for _, p := range ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		containerPort := nat.Port(fmt.Sprintf("%d/%s", p.ContainerPort, proto))
		portMap[containerPort] = []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: fmt.Sprintf("%d", p.HostPort),
			},
		}
	}
	return portMap
}

func (d *DockerRuntime) buildExposedPorts(ports []models.PortMapping) nat.PortSet {
	exposed := nat.PortSet{}
	for _, p := range ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		port := nat.Port(fmt.Sprintf("%d/%s", p.ContainerPort, proto))
		exposed[port] = struct{}{}
	}
	return exposed
}

func (d *DockerRuntime) buildRestartPolicy(policy *models.RestartPolicy) container.RestartPolicy {
	if policy == nil {
		return container.RestartPolicy{Name: container.RestartPolicyMode("no")}
	}

	return container.RestartPolicy{
		Name:              container.RestartPolicyMode(policy.Policy),
		MaximumRetryCount: policy.MaxRetryCount,
	}
}
