// Package types contains SDK-only types that are not generated from protobuf.
package types

// PersysStack is a lightweight SDK representation for multi-workload stack manifests.
type PersysStack struct {
	APIVersion string                   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string                   `json:"kind" yaml:"kind"`
	Metadata   map[string]string        `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Spec       map[string]interface{}   `json:"spec,omitempty" yaml:"spec,omitempty"`
	Workloads  []map[string]interface{} `json:"workloads,omitempty" yaml:"workloads,omitempty"`
}

// GitSource identifies a remote Git manifest source.
type GitSource struct{ URL, Ref, Path string }
