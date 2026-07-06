package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	pb "github.com/persys-dev/persys-cloud/compute-agent/pkg/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// Parse command line flags
	serverAddr := flag.String("server", "localhost:50051", "Agent server address")
	certFile := flag.String("cert", "", "Client certificate file")
	keyFile := flag.String("key", "", "Client key file")
	caFile := flag.String("ca", "", "CA certificate file")
	insecure := flag.Bool("insecure", false, "Disable TLS")
	action := flag.String("action", "health", "Action to perform: health, apply, apply-compose, apply-vm, status, list, delete, list-actions")
	workloadID := flag.String("id", "", "Workload ID")
	workloadType := flag.String("type", "container", "Workload type: container, compose, vm")
	actionType := flag.String("action-type", "", "Filter list-actions by action type (apply_workload, delete_workload, ...)")
	actionStatus := flag.String("action-status", "", "Filter list-actions by status (pending, running, completed, failed)")
	actionLimit := flag.Int("action-limit", 0, "Limit list-actions results (0 = all)")
	newestFirst := flag.Bool("newest-first", true, "Sort list-actions by newest first")
	waitForResult := flag.Bool("wait", true, "For apply actions, poll workload status until terminal state")
	waitTimeout := flag.Duration("wait-timeout", 45*time.Second, "Maximum time to wait for terminal workload state")
	revisionID := flag.String("revision", "rev-1", "Workload revision ID")
	desiredState := flag.String("desired-state", "running", "Desired state: running or stopped")
	specFile := flag.String("spec-file", "", "Optional JSON file for workload spec (container/compose/vm)")

	containerImage := flag.String("container-image", "nginx:latest", "Container image")
	containerCommand := flag.String("container-command", "", "Container command as comma-separated values")
	containerArgs := flag.String("container-args", "", "Container args as comma-separated values")
	containerEnv := flag.String("container-env", "", "Container env as comma-separated key=value pairs")
	containerLabels := flag.String("container-labels", "", "Container labels as comma-separated key=value pairs")
	containerPorts := flag.String("container-ports", "8080:80/tcp", "Container ports as comma-separated host:container/proto")
	containerVolumes := flag.String("container-volumes", "", "Container volumes as comma-separated /host:/container[:ro|rw]")
	containerCPUShares := flag.Int64("container-cpu-shares", 0, "Container CPU shares")
	containerMemoryMB := flag.Int64("container-memory-mb", 0, "Container memory limit in MB")
	containerMemorySwapMB := flag.Int64("container-memory-swap-mb", 0, "Container memory swap limit in MB")
	containerRestartPolicy := flag.String("container-restart-policy", "unless-stopped", "Container restart policy")
	containerRestartMaxRetry := flag.Int("container-restart-max-retry", 0, "Container restart max retry count")

	vmName := flag.String("vm-name", "", "VM name (defaults to workload ID)")
	vmVCPUs := flag.Int("vm-vcpus", 2, "VM vCPU count")
	vmMemoryMB := flag.Int64("vm-memory-mb", 2048, "VM memory in MB")
	vmCloudInitFile := flag.String("vm-cloud-init-file", "", "Path to cloud-init user-data file")
	vmCloudInitInline := flag.String("vm-cloud-init", "", "Inline cloud-init user-data")
	vmDisks := flag.String("vm-disks", "", "VM disks as ';' entries: path=...,device=...,format=...,size_gb=...,type=...,boot=true|false")
	vmNetworks := flag.String("vm-networks", "", "VM networks as ';' entries: network=...,mac=...,ip=...")
	vmMetadata := flag.String("vm-metadata", "environment=test,owner=demo,created-by=test-client", "VM metadata as comma-separated key=value")
	flag.Parse()

	// Create gRPC connection
	var opts []grpc.DialOption

	if *insecure {
		opts = append(opts, grpc.WithInsecure())
	} else {
		// Load TLS credentials
		tlsConfig, err := loadTLSConfig(*certFile, *keyFile, *caFile)
		if err != nil {
			log.Fatalf("Failed to load TLS config: %v", err)
		}
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	}

	conn, err := grpc.Dial(*serverAddr, opts...)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewAgentServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Execute action
	switch *action {
	case "health":
		healthCheck(ctx, client)
	case "apply":
		switch *workloadType {
		case "container":
			applyWorkload(ctx, client, *workloadID, &applyOptions{
				revisionID: *revisionID,
				desired:    parseDesiredState(*desiredState),
				wait:       *waitForResult,
				waitTime:   *waitTimeout,
				specFile:   *specFile,
				container: containerOptions{
					image:             *containerImage,
					command:           *containerCommand,
					args:              *containerArgs,
					env:               *containerEnv,
					labels:            *containerLabels,
					ports:             *containerPorts,
					volumes:           *containerVolumes,
					cpuShares:         *containerCPUShares,
					memoryMB:          *containerMemoryMB,
					memorySwapMB:      *containerMemorySwapMB,
					restartPolicy:     *containerRestartPolicy,
					restartMaxRetries: *containerRestartMaxRetry,
				},
			})
		case "compose":
			applyComposeWorkload(ctx, client, *workloadID, *waitForResult, *waitTimeout)
		case "vm":
			applyVMWorkload(ctx, client, *workloadID, &applyOptions{
				revisionID: *revisionID,
				desired:    parseDesiredState(*desiredState),
				wait:       *waitForResult,
				waitTime:   *waitTimeout,
				specFile:   *specFile,
				vm: vmOptions{
					name:          *vmName,
					vcpus:         *vmVCPUs,
					memoryMB:      *vmMemoryMB,
					cloudInitFile: *vmCloudInitFile,
					cloudInitText: *vmCloudInitInline,
					disks:         *vmDisks,
					networks:      *vmNetworks,
					metadata:      *vmMetadata,
				},
			})
		default:
			log.Fatalf("Unknown workload type: %s", *workloadType)
		}
	case "apply-compose":
		applyComposeWorkload(ctx, client, *workloadID, *waitForResult, *waitTimeout)
	case "apply-vm":
		applyVMWorkload(ctx, client, *workloadID, &applyOptions{
			revisionID: *revisionID,
			desired:    parseDesiredState(*desiredState),
			wait:       *waitForResult,
			waitTime:   *waitTimeout,
			specFile:   *specFile,
			vm: vmOptions{
				name:          *vmName,
				vcpus:         *vmVCPUs,
				memoryMB:      *vmMemoryMB,
				cloudInitFile: *vmCloudInitFile,
				cloudInitText: *vmCloudInitInline,
				disks:         *vmDisks,
				networks:      *vmNetworks,
				metadata:      *vmMetadata,
			},
		})
	case "status":
		getStatus(ctx, client, *workloadID)
	case "list":
		listWorkloads(ctx, client)
	case "delete":
		deleteWorkload(ctx, client, *workloadID)
	case "list-actions":
		listActions(ctx, client, *workloadID, *actionType, *actionStatus, *actionLimit, *newestFirst)
	default:
		log.Fatalf("Unknown action: %s", *action)
	}
}

type applyOptions struct {
	revisionID string
	desired    pb.DesiredState
	wait       bool
	waitTime   time.Duration
	specFile   string
	container  containerOptions
	vm         vmOptions
}

type containerOptions struct {
	image             string
	command           string
	args              string
	env               string
	labels            string
	ports             string
	volumes           string
	cpuShares         int64
	memoryMB          int64
	memorySwapMB      int64
	restartPolicy     string
	restartMaxRetries int
}

type vmOptions struct {
	name          string
	vcpus         int
	memoryMB      int64
	cloudInitFile string
	cloudInitText string
	disks         string
	networks      string
	metadata      string
}

func loadTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	// Load client certificate
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}

	// Load CA certificate
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}

	return tlsConfig, nil
}

func healthCheck(ctx context.Context, client pb.AgentServiceClient) {
	resp, err := client.HealthCheck(ctx, &pb.HealthCheckRequest{})
	if err != nil {
		log.Fatalf("Health check failed: %v", err)
	}

	fmt.Printf("Agent Health Check:\n")
	fmt.Printf("  Healthy: %v\n", resp.Healthy)
	fmt.Printf("  Version: %s\n", resp.Version)
	fmt.Printf("  Runtime Status:\n")
	for runtime, status := range resp.RuntimeStatus {
		fmt.Printf("    %s: %s\n", runtime, status)
	}

	fmt.Printf("  CPU Utilization: %v\n", resp.CpuUtilization)
	fmt.Printf("  Memory Utilization: %v\n", resp.MemoryUtilization)
	fmt.Printf("  Disk Utilization: %v\n", resp.DiskUtilization)
}

func applyWorkload(ctx context.Context, client pb.AgentServiceClient, workloadID string, opts *applyOptions) {
	if workloadID == "" {
		workloadID = "example-nginx"
	}

	containerSpec := &pb.ContainerSpec{
		Image:  opts.container.image,
		Env:    parseKeyValueCSV(opts.container.env),
		Labels: parseKeyValueCSV(opts.container.labels),
		RestartPolicy: &pb.RestartPolicy{
			Policy:        opts.container.restartPolicy,
			MaxRetryCount: int32(opts.container.restartMaxRetries),
		},
	}
	containerSpec.Command = parseCSVList(opts.container.command)
	containerSpec.Args = parseCSVList(opts.container.args)
	containerSpec.Ports = parsePortMappings(opts.container.ports)
	containerSpec.Volumes = parseVolumeMounts(opts.container.volumes)
	if opts.container.cpuShares > 0 || opts.container.memoryMB > 0 || opts.container.memorySwapMB > 0 {
		containerSpec.Resources = &pb.ResourceLimits{
			CpuShares:       opts.container.cpuShares,
			MemoryBytes:     opts.container.memoryMB * 1024 * 1024,
			MemorySwapBytes: opts.container.memorySwapMB * 1024 * 1024,
		}
	}

	if opts.specFile != "" {
		loadSpecFromFile(opts.specFile, containerSpec)
	}

	req := &pb.ApplyWorkloadRequest{
		Id:           workloadID,
		Type:         pb.WorkloadType_WORKLOAD_TYPE_CONTAINER,
		RevisionId:   opts.revisionID,
		DesiredState: opts.desired,
		Spec: &pb.WorkloadSpec{
			Spec: &pb.WorkloadSpec_Container{
				Container: containerSpec,
			},
		},
	}

	resp, err := client.ApplyWorkload(ctx, req)
	if err != nil {
		log.Fatalf("Failed to apply workload: %v", err)
	}

	fmt.Printf("Workload Applied:\n")
	fmt.Printf("  Applied: %v\n", resp.Applied)
	fmt.Printf("  Skipped: %v\n", resp.Skipped)
	fmt.Printf("  Message: %s\n", resp.Message)
	if resp.Status != nil {
		printStatus(resp.Status)
		maybeWaitForTerminalStatus(ctx, client, workloadID, resp.Status, opts.wait, opts.waitTime)
	}
}

func applyComposeWorkload(ctx context.Context, client pb.AgentServiceClient, workloadID string, waitForResult bool, waitTimeout time.Duration) {
	// Example docker-compose.yml
	composeYAML := `version: '3.8'
services:
  web:
    image: nginx:latest
    ports:
      - "8080:80"
  redis:
    image: redis:alpine
`

	encodedYAML := base64.StdEncoding.EncodeToString([]byte(composeYAML))

	req := &pb.ApplyWorkloadRequest{
		Id:           workloadID,
		Type:         pb.WorkloadType_WORKLOAD_TYPE_COMPOSE,
		RevisionId:   "rev-1",
		DesiredState: pb.DesiredState_DESIRED_STATE_RUNNING,
		Spec: &pb.WorkloadSpec{
			Spec: &pb.WorkloadSpec_Compose{
				Compose: &pb.ComposeSpec{
					ProjectName: workloadID,
					ComposeYaml: encodedYAML,
					Env: map[string]string{
						"COMPOSE_PROJECT_NAME": workloadID,
					},
				},
			},
		},
	}

	resp, err := client.ApplyWorkload(ctx, req)
	if err != nil {
		log.Fatalf("Failed to apply compose workload: %v", err)
	}

	fmt.Printf("Compose Workload Applied:\n")
	fmt.Printf("  Applied: %v\n", resp.Applied)
	fmt.Printf("  Skipped: %v\n", resp.Skipped)
	fmt.Printf("  Message: %s\n", resp.Message)
	if resp.Status != nil {
		printStatus(resp.Status)
		maybeWaitForTerminalStatus(ctx, client, workloadID, resp.Status, waitForResult, waitTimeout)
	}
}

func applyVMWorkload(ctx context.Context, client pb.AgentServiceClient, workloadID string, opts *applyOptions) {
	if workloadID == "" {
		workloadID = "example-vm"
	}

	cloudInit := opts.vm.cloudInitText
	if opts.vm.cloudInitFile != "" {
		b, err := os.ReadFile(opts.vm.cloudInitFile)
		if err != nil {
			log.Fatalf("Failed to read vm cloud-init file: %v", err)
		}
		cloudInit = string(b)
	}
	if cloudInit == "" {
		cloudInit = fmt.Sprintf("#!/bin/bash\necho 'vm %s initialized' > /tmp/cloud-init.log\n", workloadID)
	}

	vmSpec := &pb.VMSpec{
		Name:      workloadID,
		Vcpus:     int32(opts.vm.vcpus),
		MemoryMb:  opts.vm.memoryMB,
		CloudInit: cloudInit,
		Metadata:  parseKeyValueCSV(opts.vm.metadata),
	}
	if opts.vm.name != "" {
		vmSpec.Name = opts.vm.name
	}
	vmSpec.Disks = parseVMDiskConfigs(opts.vm.disks, workloadID)
	vmSpec.Networks = parseVMNetworkConfigs(opts.vm.networks)
	if opts.specFile != "" {
		loadSpecFromFile(opts.specFile, vmSpec)
	}

	req := &pb.ApplyWorkloadRequest{
		Id:           workloadID,
		Type:         pb.WorkloadType_WORKLOAD_TYPE_VM,
		RevisionId:   opts.revisionID,
		DesiredState: opts.desired,
		Spec: &pb.WorkloadSpec{
			Spec: &pb.WorkloadSpec_Vm{
				Vm: vmSpec,
			},
		},
	}

	resp, err := client.ApplyWorkload(ctx, req)
	if err != nil {
		log.Fatalf("Failed to apply VM workload: %v", err)
	}

	fmt.Printf("VM Workload Applied:\n")
	fmt.Printf("  Applied: %v\n", resp.Applied)
	fmt.Printf("  Skipped: %v\n", resp.Skipped)
	fmt.Printf("  Message: %s\n", resp.Message)
	if resp.Status != nil {
		printStatus(resp.Status)
		maybeWaitForTerminalStatus(ctx, client, workloadID, resp.Status, opts.wait, opts.waitTime)
	}
}

func getStatus(ctx context.Context, client pb.AgentServiceClient, workloadID string) {
	if workloadID == "" {
		log.Fatal("Workload ID required for status check")
	}

	resp, err := client.GetWorkloadStatus(ctx, &pb.GetWorkloadStatusRequest{
		Id: workloadID,
	})
	if err != nil {
		log.Fatalf("Failed to get status: %v", err)
	}

	printStatus(resp.Status)
}

func parseDesiredState(s string) pb.DesiredState {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "stopped":
		return pb.DesiredState_DESIRED_STATE_STOPPED
	default:
		return pb.DesiredState_DESIRED_STATE_RUNNING
	}
}

func parseCSVList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func parseKeyValueCSV(s string) map[string]string {
	result := map[string]string{}
	for _, entry := range parseCSVList(s) {
		kv := strings.SplitN(entry, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		if key != "" {
			result[key] = val
		}
	}
	return result
}

func parsePortMappings(s string) []*pb.PortMapping {
	var out []*pb.PortMapping
	for _, item := range parseCSVList(s) {
		parts := strings.SplitN(item, "/", 2)
		portPart := parts[0]
		proto := "tcp"
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			proto = strings.TrimSpace(parts[1])
		}

		hc := strings.SplitN(portPart, ":", 2)
		if len(hc) != 2 {
			continue
		}
		hostPort, err1 := strconv.Atoi(strings.TrimSpace(hc[0]))
		containerPort, err2 := strconv.Atoi(strings.TrimSpace(hc[1]))
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, &pb.PortMapping{
			HostPort:      int32(hostPort),
			ContainerPort: int32(containerPort),
			Protocol:      proto,
		})
	}
	return out
}

func parseVolumeMounts(s string) []*pb.VolumeMount {
	var out []*pb.VolumeMount
	for _, item := range parseCSVList(s) {
		parts := strings.Split(item, ":")
		if len(parts) < 2 {
			continue
		}
		hostPath := strings.TrimSpace(parts[0])
		containerPath := strings.TrimSpace(parts[1])
		readOnly := false
		if len(parts) > 2 {
			mode := strings.ToLower(strings.TrimSpace(parts[2]))
			readOnly = mode == "ro"
		}
		out = append(out, &pb.VolumeMount{
			HostPath:      hostPath,
			ContainerPath: containerPath,
			ReadOnly:      readOnly,
		})
	}
	return out
}

func parseVMDiskConfigs(disks string, workloadID string) []*pb.DiskConfig {
	if strings.TrimSpace(disks) == "" {
		return []*pb.DiskConfig{
			{
				Path:   "/var/lib/libvirt/images/" + workloadID + ".qcow2",
				Device: "vda",
				Format: "qcow2",
				SizeGb: 20,
				Type:   "disk",
			},
		}
	}

	entries := strings.Split(disks, ";")
	var out []*pb.DiskConfig
	for _, raw := range entries {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		kv := parseEntryKV(raw)
		sizeGB, _ := strconv.ParseInt(kv["size_gb"], 10, 64)
		boot, _ := strconv.ParseBool(kv["boot"])
		if kv["path"] == "" || kv["device"] == "" || kv["format"] == "" || kv["type"] == "" {
			continue
		}
		out = append(out, &pb.DiskConfig{
			Path:   kv["path"],
			Device: kv["device"],
			Format: kv["format"],
			SizeGb: sizeGB,
			Type:   kv["type"],
			Boot:   boot,
		})
	}
	return out
}

func parseVMNetworkConfigs(networks string) []*pb.NetworkConfig {
	if strings.TrimSpace(networks) == "" {
		return []*pb.NetworkConfig{
			{Network: "default"},
		}
	}

	entries := strings.Split(networks, ";")
	var out []*pb.NetworkConfig
	for _, raw := range entries {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		kv := parseEntryKV(raw)
		if kv["network"] == "" {
			continue
		}
		out = append(out, &pb.NetworkConfig{
			Network:    kv["network"],
			MacAddress: kv["mac"],
			IpAddress:  kv["ip"],
		})
	}
	return out
}

func parseEntryKV(s string) map[string]string {
	result := map[string]string{}
	for _, token := range strings.Split(s, ",") {
		kv := strings.SplitN(strings.TrimSpace(token), "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		if key != "" {
			result[key] = val
		}
	}
	return result
}

func loadSpecFromFile(path string, target interface{}) {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("Failed to read spec file %s: %v", path, err)
	}
	if err := json.Unmarshal(b, target); err != nil {
		log.Fatalf("Failed to parse spec file %s: %v", path, err)
	}
}

func listWorkloads(ctx context.Context, client pb.AgentServiceClient) {
	resp, err := client.ListWorkloads(ctx, &pb.ListWorkloadsRequest{})
	if err != nil {
		log.Fatalf("Failed to list workloads: %v", err)
	}

	fmt.Printf("Workloads (%d total):\n", len(resp.Workloads))
	for i, status := range resp.Workloads {
		fmt.Printf("\n[%d] %s\n", i+1, status.Id)
		printStatus(status)
	}
}

func deleteWorkload(ctx context.Context, client pb.AgentServiceClient, workloadID string) {
	if workloadID == "" {
		log.Fatal("Workload ID required for deletion")
	}

	resp, err := client.DeleteWorkload(ctx, &pb.DeleteWorkloadRequest{
		Id: workloadID,
	})
	if err != nil {
		log.Fatalf("Failed to delete workload: %v", err)
	}

	fmt.Printf("Delete Workload:\n")
	fmt.Printf("  Success: %v\n", resp.Success)
	fmt.Printf("  Message: %s\n", resp.Message)
}

func listActions(ctx context.Context, client pb.AgentServiceClient, workloadID, actionType, status string, limit int, newestFirst bool) {
	resp, err := client.ListActions(ctx, &pb.ListActionsRequest{
		WorkloadId:  workloadID,
		ActionType:  actionType,
		Status:      status,
		Limit:       int32(limit),
		NewestFirst: newestFirst,
	})
	if err != nil {
		log.Fatalf("Failed to list actions: %v", err)
	}

	fmt.Printf("Actions (%d total):\n", len(resp.Actions))
	for i, action := range resp.Actions {
		fmt.Printf("\n[%d] %s\n", i+1, action.TaskId)
		fmt.Printf("  Workload: %s\n", action.WorkloadId)
		fmt.Printf("  Type: %s\n", action.ActionType)
		fmt.Printf("  Status: %s\n", action.Status)
		if action.Error != "" {
			fmt.Printf("  Error: %s\n", action.Error)
		}
		if action.CreatedAt > 0 {
			fmt.Printf("  Created: %s\n", time.Unix(action.CreatedAt, 0).Format(time.RFC3339))
		}
		if action.StartedAt > 0 {
			fmt.Printf("  Started: %s\n", time.Unix(action.StartedAt, 0).Format(time.RFC3339))
		}
		if action.EndedAt > 0 {
			fmt.Printf("  Ended: %s\n", time.Unix(action.EndedAt, 0).Format(time.RFC3339))
		}
	}
}

func printStatus(status *pb.WorkloadStatus) {
	fmt.Printf("  ID: %s\n", status.Id)
	fmt.Printf("  Type: %s\n", status.Type)
	fmt.Printf("  Revision: %s\n", status.RevisionId)
	fmt.Printf("  Desired State: %s\n", status.DesiredState)
	fmt.Printf("  Actual State: %s\n", status.ActualState)
	fmt.Printf("  Message: %s\n", status.Message)
	fmt.Printf("  Created: %s\n", time.Unix(status.CreatedAt, 0).Format(time.RFC3339))
	fmt.Printf("  Updated: %s\n", time.Unix(status.UpdatedAt, 0).Format(time.RFC3339))
	if len(status.Metadata) > 0 {
		fmt.Printf("  Metadata:\n")
		for k, v := range status.Metadata {
			fmt.Printf("    %s: %s\n", k, v)
		}
	}
}

func maybeWaitForTerminalStatus(parentCtx context.Context, client pb.AgentServiceClient, workloadID string, status *pb.WorkloadStatus, waitForResult bool, waitTimeout time.Duration) {
	if !waitForResult || status == nil {
		return
	}

	if status.GetActualState() != pb.ActualState_ACTUAL_STATE_PENDING {
		return
	}

	fmt.Printf("\nWaiting for workload %s to finish scheduling (timeout: %s)...\n", workloadID, waitTimeout)
	if taskID := status.GetMetadata()["task_id"]; taskID != "" {
		fmt.Printf("  Task ID: %s\n", taskID)
	}

	waitCtx, cancel := context.WithTimeout(parentCtx, waitTimeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			fmt.Printf("  Wait finished without terminal status: %v\n", waitCtx.Err())
			return
		case <-ticker.C:
			resp, err := client.GetWorkloadStatus(waitCtx, &pb.GetWorkloadStatusRequest{Id: workloadID})
			if err != nil {
				fmt.Printf("  Status polling failed: %v\n", err)
				return
			}

			current := resp.GetStatus()
			if current == nil {
				fmt.Printf("  Status polling returned empty status\n")
				return
			}

			if current.ActualState != pb.ActualState_ACTUAL_STATE_PENDING {
				fmt.Printf("\nFinal Workload Status:\n")
				printStatus(current)
				return
			}
		}
	}
}
