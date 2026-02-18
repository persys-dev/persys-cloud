package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	controlv1 "github.com/persys-dev/persys-cloud/persys-scheduler/internal/controlv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func main() {
	op := flag.String("op", "", "operation: register-node | heartbeat | apply-container | apply-vm | delete-workload | retry-workload | list-nodes | get-node | list-workloads | get-workload | cluster-summary")
	schedulerAddr := flag.String("scheduler", "127.0.0.1:8085", "scheduler gRPC address")
	timeout := flag.Duration("timeout", 20*time.Second, "rpc timeout")

	nodeID := flag.String("node-id", fmt.Sprintf("node-%d", time.Now().Unix()), "node id")
	nodeEndpoint := flag.String("node-endpoint", "127.0.0.1:50051", "agent gRPC endpoint for register-node")
	clusterID := flag.String("cluster-id", "default", "cluster id for register-node")
	agentVersion := flag.String("agent-version", "dev", "agent version for register-node")
	cpuTotal := flag.Int64("cpu-total", 4000, "node total millicores")
	memTotal := flag.Int64("mem-total", 8192, "node total memory MB")
	supportedTypes := flag.String("supported-types", "container,compose", "supported workload types CSV for register-node (e.g. container,compose,vm)")

	cpuAllocated := flag.Int64("cpu-allocated", 1000, "heartbeat allocated millicores")
	cpuUsed := flag.Int64("cpu-used", 400, "heartbeat used millicores")
	memAllocated := flag.Int64("mem-allocated", 1024, "heartbeat allocated memory MB")
	memUsed := flag.Int64("mem-used", 512, "heartbeat used memory MB")
	diskAllocated := flag.Int64("disk-allocated", 10, "heartbeat allocated disk GB")
	diskUsed := flag.Int64("disk-used", 3, "heartbeat used disk GB")

	workloadID := flag.String("workload-id", fmt.Sprintf("wl-%d", time.Now().Unix()), "workload id")
	revisionID := flag.String("revision-id", "r1", "workload revision id")
	desiredState := flag.String("desired-state", "Running", "workload desired state")
	wCPU := flag.Int64("w-cpu", 250, "workload requested millicores")
	wMem := flag.Int64("w-mem", 256, "workload requested memory MB")
	wDisk := flag.Int64("w-disk", 2, "workload requested disk GB")

	containerImage := flag.String("container-image", "alpine:latest", "container image")
	containerCmd := flag.String("container-cmd", "sleep,60", "container command CSV")
	containerRestart := flag.String("container-restart", "unless-stopped", "container restart policy")

	vmVCPUs := flag.Int("vm-vcpus", 2, "vm vcpus")
	vmMemory := flag.Int64("vm-memory", 2048, "vm memory MB")
	vmImage := flag.String("vm-image", "ubuntu-22.04", "vm os image")
	vmDiskPool := flag.String("vm-disk-pool", "local", "vm disk pool")
	vmDiskSize := flag.Int64("vm-disk-size", 20, "vm primary disk size GB")
	vmBridge := flag.String("vm-bridge", "br0", "vm network bridge")
	vmDHCP := flag.Bool("vm-dhcp", true, "vm network dhcp")
	filterStatus := flag.String("status", "", "optional status filter for list-nodes/list-workloads")
	filterNodeID := flag.String("filter-node-id", "", "optional node id filter for list-workloads")

	flag.Parse()
	if *op == "" {
		log.Fatalf("missing -op")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, *schedulerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		log.Fatalf("failed to connect scheduler %s: %v", *schedulerAddr, err)
	}
	defer conn.Close()

	client := controlv1.NewAgentControlClient(conn)

	switch *op {
	case "register-node":
		resp, err := client.RegisterNode(ctx, &controlv1.RegisterNodeRequest{
			NodeId: *nodeID,
			Capabilities: &controlv1.NodeCapabilities{
				CpuTotalMillicores:     *cpuTotal,
				MemoryTotalMb:          *memTotal,
				SupportedWorkloadTypes: splitCSV(*supportedTypes),
			},
			AgentVersion: *agentVersion,
			GrpcEndpoint: *nodeEndpoint,
			ClusterId:    *clusterID,
			Timestamp:    timestamppb.Now(),
		})
		if err != nil {
			log.Fatalf("register-node failed: %v", err)
		}
		log.Printf("register-node accepted=%v reason=%q heartbeat_interval=%d", resp.GetAccepted(), resp.GetReason(), resp.GetHeartbeatIntervalSeconds())
	case "heartbeat":
		resp, err := client.Heartbeat(ctx, &controlv1.HeartbeatRequest{
			NodeId: *nodeID,
			Usage: &controlv1.NodeUsage{
				CpuAllocatedMillicores: *cpuAllocated,
				CpuUsedMillicores:      *cpuUsed,
				MemoryAllocatedMb:      *memAllocated,
				MemoryUsedMb:           *memUsed,
				DiskAllocatedGb:        *diskAllocated,
				DiskUsedGb:             *diskUsed,
			},
			Timestamp: timestamppb.Now(),
		})
		if err != nil {
			log.Fatalf("heartbeat failed: %v", err)
		}
		log.Printf("heartbeat acknowledged=%v drain_node=%v", resp.GetAcknowledged(), resp.GetDrainNode())
	case "apply-container":
		resp, err := client.ApplyWorkload(ctx, &controlv1.ApplyWorkloadRequest{
			WorkloadId:   *workloadID,
			RevisionId:   *revisionID,
			DesiredState: *desiredState,
			Spec: &controlv1.WorkloadSpec{
				Type: "container",
				Resources: &controlv1.ResourceRequirements{
					CpuMillicores: *wCPU,
					MemoryMb:      *wMem,
					DiskGb:        *wDisk,
				},
				Workload: &controlv1.WorkloadSpec_Container{
					Container: &controlv1.ContainerSpec{
						Image:         *containerImage,
						Command:       splitCSV(*containerCmd),
						RestartPolicy: *containerRestart,
					},
				},
			},
		})
		if err != nil {
			log.Fatalf("apply-container failed: %v", err)
		}
		log.Printf("apply-container success=%v failure_reason=%s error=%q", resp.GetSuccess(), resp.GetFailureReason().String(), resp.GetErrorMessage())
	case "apply-vm":
		resp, err := client.ApplyWorkload(ctx, &controlv1.ApplyWorkloadRequest{
			WorkloadId:   *workloadID,
			RevisionId:   *revisionID,
			DesiredState: *desiredState,
			Spec: &controlv1.WorkloadSpec{
				Type: "vm",
				Resources: &controlv1.ResourceRequirements{
					CpuMillicores: *wCPU,
					MemoryMb:      *wMem,
					DiskGb:        *wDisk,
				},
				Workload: &controlv1.WorkloadSpec_Vm{
					Vm: &controlv1.VMSpec{
						Vcpus:    int32(*vmVCPUs),
						MemoryMb: *vmMemory,
						Disks: []*controlv1.DiskConfig{
							{
								PoolName: *vmDiskPool,
								SizeGb:   *vmDiskSize,
							},
						},
						Networks: []*controlv1.NetworkConfig{
							{
								Bridge: *vmBridge,
								Dhcp:   *vmDHCP,
							},
						},
						OsImage: *vmImage,
					},
				},
			},
		})
		if err != nil {
			log.Fatalf("apply-vm failed: %v", err)
		}
		log.Printf("apply-vm success=%v failure_reason=%s error=%q", resp.GetSuccess(), resp.GetFailureReason().String(), resp.GetErrorMessage())
	case "delete-workload":
		resp, err := client.DeleteWorkload(ctx, &controlv1.DeleteWorkloadRequest{
			WorkloadId: *workloadID,
		})
		if err != nil {
			log.Fatalf("delete-workload failed: %v", err)
		}
		log.Printf("delete-workload success=%v error=%q", resp.GetSuccess(), resp.GetErrorMessage())
	case "retry-workload":
		resp, err := client.RetryWorkload(ctx, &controlv1.RetryWorkloadRequest{
			WorkloadId: *workloadID,
		})
		if err != nil {
			log.Fatalf("retry-workload failed: %v", err)
		}
		log.Printf("retry-workload accepted=%v", resp.GetAccepted())
	case "list-nodes":
		resp, err := client.ListNodes(ctx, &controlv1.ListNodesRequest{Status: *filterStatus})
		if err != nil {
			log.Fatalf("list-nodes failed: %v", err)
		}
		printJSON(resp)
	case "get-node":
		resp, err := client.GetNode(ctx, &controlv1.GetNodeRequest{NodeId: *nodeID})
		if err != nil {
			log.Fatalf("get-node failed: %v", err)
		}
		printJSON(resp)
	case "list-workloads":
		resp, err := client.ListWorkloads(ctx, &controlv1.ListWorkloadsRequest{
			NodeId: *filterNodeID,
			Status: *filterStatus,
		})
		if err != nil {
			log.Fatalf("list-workloads failed: %v", err)
		}
		printJSON(resp)
	case "get-workload":
		resp, err := client.GetWorkload(ctx, &controlv1.GetWorkloadRequest{WorkloadId: *workloadID})
		if err != nil {
			log.Fatalf("get-workload failed: %v", err)
		}
		printJSON(resp)
	case "cluster-summary":
		resp, err := client.GetClusterSummary(ctx, &controlv1.GetClusterSummaryRequest{})
		if err != nil {
			log.Fatalf("cluster-summary failed: %v", err)
		}
		printJSON(resp)
	default:
		log.Fatalf("unsupported -op %q", *op)
	}
	fmt.Println("OK")
}

func splitCSV(s string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if start < i {
				parts = append(parts, s[start:i])
			}
			start = i + 1
		}
	}
	return parts
}

func printJSON(v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Fatalf("json marshal failed: %v", err)
	}
	fmt.Println(string(b))
}
