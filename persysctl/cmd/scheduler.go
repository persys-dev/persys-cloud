package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	controlv1 "github.com/persys-dev/persys-cloud/pkg/scheduler/controlv1"
	agentv1 "github.com/persys-dev/persys-cloud/pkg/agent/api/v1"
	"github.com/spf13/cobra"
)

var (
	schedulerApplyID       string
	schedulerApplyType     string
	schedulerApplySpecFile string
	schedulerApplyRevision string
	schedulerApplyDesired  string
	schedulerNodeID        string
	schedulerWorkloadID    string
	schedulerStatus        string
	schedulerFilterNodeID  string
)

var schedulerCmd = &cobra.Command{
	Use:   "scheduler",
	Short: "Scheduler control-plane RPCs",
}

var schedulerSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Get scheduler cluster summary",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.GetClusterSummary()
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var schedulerApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply workload to scheduler from spec file",
	Run: func(cmd *cobra.Command, args []string) {
		spec, err := buildSchedulerWorkloadSpec(schedulerApplyType, schedulerApplySpecFile)
		cobra.CheckErr(err)

		req := &controlv1.ApplyWorkloadRequest{
			WorkloadId:   schedulerApplyID,
			RevisionId:   schedulerApplyRevision,
			DesiredState: normalizeDesiredState(schedulerApplyDesired),
			Spec:         spec,
		}

		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.ApplySchedulerWorkload(req)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var schedulerApplyContainerCmd = &cobra.Command{
	Use:   "apply-container",
	Short: "Smoke-compatible alias for scheduler apply container",
	Run: func(cmd *cobra.Command, args []string) {
		schedulerApplyType = "container"
		schedulerApplyCmd.Run(cmd, args)
	},
}

var schedulerApplyVMCmd = &cobra.Command{
	Use:   "apply-vm",
	Short: "Smoke-compatible alias for scheduler apply vm",
	Run: func(cmd *cobra.Command, args []string) {
		schedulerApplyType = "vm"
		schedulerApplyCmd.Run(cmd, args)
	},
}

var schedulerDeleteWorkloadCmd = &cobra.Command{
	Use:   "delete-workload",
	Short: "Delete workload via scheduler RPC",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.DeleteWorkload(schedulerWorkloadID)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var schedulerRetryWorkloadCmd = &cobra.Command{
	Use:   "retry-workload",
	Short: "Retry workload via scheduler RPC",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.RetryWorkload(schedulerWorkloadID)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var schedulerListNodesCmd = &cobra.Command{
	Use:   "list-nodes",
	Short: "List nodes via scheduler RPC",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.SchedulerListNodes(schedulerStatus)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var schedulerGetNodeCmd = &cobra.Command{
	Use:   "get-node",
	Short: "Get node via scheduler RPC",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.GetNode(schedulerNodeID)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var schedulerListWorkloadsCmd = &cobra.Command{
	Use:   "list-workloads",
	Short: "List workloads via scheduler RPC",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.SchedulerListWorkloads(schedulerFilterNodeID, schedulerStatus)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

var schedulerGetWorkloadCmd = &cobra.Command{
	Use:   "get-workload",
	Short: "Get workload via scheduler RPC",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, err := newClientWithTrace()
		cobra.CheckErr(err)
		defer c.Close()

		resp, err := c.GetWorkload(schedulerWorkloadID)
		cobra.CheckErr(err)
		printProto(resp)
	},
}

func init() {
	rootCmd.AddCommand(schedulerCmd)
	schedulerCmd.AddCommand(schedulerSummaryCmd)
	schedulerCmd.AddCommand(schedulerApplyCmd)
	schedulerCmd.AddCommand(schedulerApplyContainerCmd)
	schedulerCmd.AddCommand(schedulerApplyVMCmd)
	schedulerCmd.AddCommand(schedulerDeleteWorkloadCmd)
	schedulerCmd.AddCommand(schedulerRetryWorkloadCmd)
	schedulerCmd.AddCommand(schedulerListNodesCmd)
	schedulerCmd.AddCommand(schedulerGetNodeCmd)
	schedulerCmd.AddCommand(schedulerListWorkloadsCmd)
	schedulerCmd.AddCommand(schedulerGetWorkloadCmd)

	schedulerApplyCmd.Flags().StringVar(&schedulerApplyID, "id", "", "Workload ID")
	schedulerApplyCmd.Flags().StringVar(&schedulerApplyType, "type", "container", "Workload type: container|compose|vm")
	schedulerApplyCmd.Flags().StringVar(&schedulerApplySpecFile, "spec-file", "", "Path to JSON spec file")
	schedulerApplyCmd.Flags().StringVar(&schedulerApplyRevision, "revision", "rev-1", "Workload revision ID")
	schedulerApplyCmd.Flags().StringVar(&schedulerApplyDesired, "desired-state", "running", "Desired state: running|stopped")
	cobra.CheckErr(schedulerApplyCmd.MarkFlagRequired("id"))
	cobra.CheckErr(schedulerApplyCmd.MarkFlagRequired("spec-file"))

	schedulerApplyContainerCmd.Flags().StringVar(&schedulerApplyID, "id", "", "Workload ID")
	schedulerApplyContainerCmd.Flags().StringVar(&schedulerApplySpecFile, "spec-file", "", "Path to JSON spec file")
	schedulerApplyContainerCmd.Flags().StringVar(&schedulerApplyRevision, "revision", "rev-1", "Workload revision ID")
	schedulerApplyContainerCmd.Flags().StringVar(&schedulerApplyDesired, "desired-state", "running", "Desired state: running|stopped")
	cobra.CheckErr(schedulerApplyContainerCmd.MarkFlagRequired("id"))
	cobra.CheckErr(schedulerApplyContainerCmd.MarkFlagRequired("spec-file"))

	schedulerApplyVMCmd.Flags().StringVar(&schedulerApplyID, "id", "", "Workload ID")
	schedulerApplyVMCmd.Flags().StringVar(&schedulerApplySpecFile, "spec-file", "", "Path to JSON spec file")
	schedulerApplyVMCmd.Flags().StringVar(&schedulerApplyRevision, "revision", "rev-1", "Workload revision ID")
	schedulerApplyVMCmd.Flags().StringVar(&schedulerApplyDesired, "desired-state", "running", "Desired state: running|stopped")
	cobra.CheckErr(schedulerApplyVMCmd.MarkFlagRequired("id"))
	cobra.CheckErr(schedulerApplyVMCmd.MarkFlagRequired("spec-file"))

	schedulerDeleteWorkloadCmd.Flags().StringVar(&schedulerWorkloadID, "workload-id", "", "Workload ID")
	schedulerRetryWorkloadCmd.Flags().StringVar(&schedulerWorkloadID, "workload-id", "", "Workload ID")
	schedulerGetWorkloadCmd.Flags().StringVar(&schedulerWorkloadID, "workload-id", "", "Workload ID")
	cobra.CheckErr(schedulerDeleteWorkloadCmd.MarkFlagRequired("workload-id"))
	cobra.CheckErr(schedulerRetryWorkloadCmd.MarkFlagRequired("workload-id"))
	cobra.CheckErr(schedulerGetWorkloadCmd.MarkFlagRequired("workload-id"))

	schedulerGetNodeCmd.Flags().StringVar(&schedulerNodeID, "node-id", "", "Node ID")
	cobra.CheckErr(schedulerGetNodeCmd.MarkFlagRequired("node-id"))

	schedulerListNodesCmd.Flags().StringVar(&schedulerStatus, "status", "", "Optional status filter")
	schedulerListWorkloadsCmd.Flags().StringVar(&schedulerStatus, "status", "", "Optional status filter")
	schedulerListWorkloadsCmd.Flags().StringVar(&schedulerFilterNodeID, "filter-node-id", "", "Optional node id filter")
}

func buildSchedulerWorkloadSpec(typ, specFile string) (*controlv1.WorkloadSpec, error) {
	body, err := os.ReadFile(specFile)
	if err != nil {
		return nil, fmt.Errorf("read spec file: %w", err)
	}

	t := strings.ToLower(strings.TrimSpace(typ))
	switch t {
	case "container", "docker-container":
		if spec, ok := parseSchedulerContainerSpec(body); ok {
			return spec, nil
		}
		return parseAgentContainerSpecForScheduler(body)
	case "compose", "docker-compose", "git-compose":
		if spec, ok := parseSchedulerComposeSpec(body); ok {
			return spec, nil
		}
		return parseAgentComposeSpecForScheduler(body)
	case "vm":
		if spec, ok := parseSchedulerVMSpec(body); ok {
			return spec, nil
		}
		return parseAgentVMSpecForScheduler(body)
	default:
		return nil, fmt.Errorf("unsupported --type %q, use container|compose|vm", typ)
	}
}

func parseSchedulerContainerSpec(body []byte) (*controlv1.WorkloadSpec, bool) {
	container := &controlv1.ContainerSpec{}
	if err := json.Unmarshal(body, container); err != nil || container.GetImage() == "" {
		return nil, false
	}
	return &controlv1.WorkloadSpec{
		Type:      "container",
		Workload:  &controlv1.WorkloadSpec_Container{Container: container},
		Resources: &controlv1.ResourceRequirements{},
	}, true
}

func parseAgentContainerSpecForScheduler(body []byte) (*controlv1.WorkloadSpec, error) {
	container := &agentv1.ContainerSpec{}
	if err := json.Unmarshal(body, container); err != nil {
		return nil, fmt.Errorf("parse container spec: %w", err)
	}
	if container.GetImage() == "" {
		return nil, fmt.Errorf("container image is required")
	}

	return &controlv1.WorkloadSpec{
		Type: "container",
		Resources: &controlv1.ResourceRequirements{
			CpuMillicores: container.GetResources().GetCpuShares(),
			MemoryMb:      container.GetResources().GetMemoryBytes() / 1024 / 1024,
		},
		Metadata: container.GetLabels(),
		Workload: &controlv1.WorkloadSpec_Container{Container: &controlv1.ContainerSpec{
			Image:          container.GetImage(),
			Command:        append(append([]string{}, container.GetCommand()...), container.GetArgs()...),
			Env:            container.GetEnv(),
			Volumes:        toControlVolumes(container.GetVolumes()),
			ManagedVolumes: toControlManagedVolumes(container.GetManagedVolumes()),
			Ports:          toControlPorts(container.GetPorts()),
			RestartPolicy:  container.GetRestartPolicy().GetPolicy(),
		}},
	}, nil
}

func parseSchedulerComposeSpec(body []byte) (*controlv1.WorkloadSpec, bool) {
	compose := &controlv1.ComposeSpec{}
	if err := json.Unmarshal(body, compose); err != nil {
		return nil, false
	}
	if compose.GetSourceType() == "" && compose.GetInlineYaml() == "" && compose.GetGitRepo() == "" {
		return nil, false
	}
	return &controlv1.WorkloadSpec{
		Type:      "compose",
		Workload:  &controlv1.WorkloadSpec_Compose{Compose: compose},
		Resources: &controlv1.ResourceRequirements{},
	}, true
}

func parseAgentComposeSpecForScheduler(body []byte) (*controlv1.WorkloadSpec, error) {
	compose := &agentv1.ComposeSpec{}
	if err := json.Unmarshal(body, compose); err != nil {
		return nil, fmt.Errorf("parse compose spec: %w", err)
	}

	inlineYAML := compose.GetComposeYaml()
	if decoded, err := base64.StdEncoding.DecodeString(compose.GetComposeYaml()); err == nil {
		inlineYAML = string(decoded)
	}

	return &controlv1.WorkloadSpec{
		Type: "compose",
		Workload: &controlv1.WorkloadSpec_Compose{Compose: &controlv1.ComposeSpec{
			SourceType: "inline",
			InlineYaml: inlineYAML,
			Env:        compose.GetEnv(),
		}},
		Resources: &controlv1.ResourceRequirements{},
	}, nil
}

func parseSchedulerVMSpec(body []byte) (*controlv1.WorkloadSpec, bool) {
	vm := &controlv1.VMSpec{}
	if err := json.Unmarshal(body, vm); err != nil {
		return nil, false
	}
	if vm.GetVcpus() == 0 && vm.GetMemoryMb() == 0 && vm.GetOsImage() == "" {
		return nil, false
	}
	return &controlv1.WorkloadSpec{
		Type:      "vm",
		Workload:  &controlv1.WorkloadSpec_Vm{Vm: vm},
		Resources: &controlv1.ResourceRequirements{},
	}, true
}

func parseAgentVMSpecForScheduler(body []byte) (*controlv1.WorkloadSpec, error) {
	vm := &agentv1.VMSpec{}
	if err := json.Unmarshal(body, vm); err != nil {
		return nil, fmt.Errorf("parse vm spec: %w", err)
	}
	metadata := map[string]string{}
	for k, v := range vm.GetMetadata() {
		metadata[k] = v
	}
	// Preserve full VM disk/network details for scheduler paths that still use reduced control VM disk schema.
	metadata["persys.vm_spec_b64"] = base64.StdEncoding.EncodeToString(body)

	cloudInit := &controlv1.CloudInitConfig{}
	if vm.GetCloudInitConfig() != nil {
		cloudInit = &controlv1.CloudInitConfig{
			UserData:      vm.GetCloudInitConfig().GetUserData(),
			MetaData:      vm.GetCloudInitConfig().GetMetaData(),
			NetworkConfig: vm.GetCloudInitConfig().GetNetworkConfig(),
			VendorData:    vm.GetCloudInitConfig().GetVendorData(),
		}
	} else if strings.TrimSpace(vm.GetCloudInit()) != "" {
		// Backward compatibility for legacy VM specs that still use single cloud_init string.
		cloudInit.UserData = vm.GetCloudInit()
	}

	return &controlv1.WorkloadSpec{
		Type: "vm",
		Resources: &controlv1.ResourceRequirements{
			CpuMillicores: int64(vm.GetVcpus()) * 1000,
			MemoryMb:      vm.GetMemoryMb(),
		},
		Metadata: metadata,
		Workload: &controlv1.WorkloadSpec_Vm{Vm: &controlv1.VMSpec{
			Vcpus:          vm.GetVcpus(),
			MemoryMb:       vm.GetMemoryMb(),
			OsImage:        vm.GetName(),
			Disks:          toControlDisks(vm.GetDisks()),
			Networks:       toControlNetworks(vm.GetNetworks()),
			CloudInit:      cloudInit,
			ManagedVolumes: toControlManagedVolumes(vm.GetManagedVolumes()),
		}},
	}, nil
}

func toControlVolumes(in []*agentv1.VolumeMount) []*controlv1.VolumeMount {
	out := make([]*controlv1.VolumeMount, 0, len(in))
	for _, v := range in {
		out = append(out, &controlv1.VolumeMount{HostPath: v.GetHostPath(), ContainerPath: v.GetContainerPath(), ReadOnly: v.GetReadOnly()})
	}
	return out
}

func toControlManagedVolumes(in []*agentv1.ManagedVolumeSpec) []*controlv1.ManagedVolumeSpec {
	out := make([]*controlv1.ManagedVolumeSpec, 0, len(in))
	for _, mv := range in {
		out = append(out, &controlv1.ManagedVolumeSpec{
			Name:         mv.GetName(),
			Driver:       mv.GetDriver(),
			SizeGb:       mv.GetSizeGb(),
			AccessMode:   mv.GetAccessMode(),
			FsType:       mv.GetFsType(),
			MountPath:    mv.GetMountPath(),
			ReadOnly:     mv.GetReadOnly(),
			RetainPolicy: mv.GetRetainPolicy(),
		})
	}
	return out
}

func toControlPorts(in []*agentv1.PortMapping) []*controlv1.Port {
	out := make([]*controlv1.Port, 0, len(in))
	for _, p := range in {
		out = append(out, &controlv1.Port{HostPort: p.GetHostPort(), ContainerPort: p.GetContainerPort(), Protocol: p.GetProtocol()})
	}
	return out
}

func toControlDisks(in []*agentv1.DiskConfig) []*controlv1.DiskConfig {
	out := make([]*controlv1.DiskConfig, 0, len(in))
	for _, d := range in {
		out = append(out, &controlv1.DiskConfig{PoolName: "local", SizeGb: d.GetSizeGb()})
	}
	return out
}

func toControlNetworks(in []*agentv1.NetworkConfig) []*controlv1.NetworkConfig {
	out := make([]*controlv1.NetworkConfig, 0, len(in))
	for _, n := range in {
		out = append(out, &controlv1.NetworkConfig{Bridge: n.GetNetwork(), StaticIp: n.GetIpAddress(), Dhcp: n.GetIpAddress() == ""})
	}
	return out
}

func normalizeDesiredState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "stopped":
		return "Stopped"
	default:
		return "Running"
	}
}
