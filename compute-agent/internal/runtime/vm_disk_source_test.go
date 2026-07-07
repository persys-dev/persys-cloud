package runtime

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
)

func TestDiskSourceFromPath(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		wantNetwork    bool
		wantProtocol   string
		wantSourceName string
		wantHost       string
	}{
		{
			name:        "local file path",
			path:        "/var/lib/libvirt/images/root.qcow2",
			wantNetwork: false,
		},
		{
			name:           "ceph rbd path",
			path:           "rbd:rbd/vm-root",
			wantNetwork:    true,
			wantProtocol:   "rbd",
			wantSourceName: "rbd/vm-root",
		},
		{
			name:           "nfs url path",
			path:           "nfs://10.0.0.12/exports/vm-root",
			wantNetwork:    true,
			wantProtocol:   "nfs",
			wantSourceName: "/exports/vm-root",
			wantHost:       "10.0.0.12",
		},
		{
			name:           "nfs legacy path",
			path:           "nfs-gw.internal:/exports/vm-root",
			wantNetwork:    true,
			wantProtocol:   "nfs",
			wantSourceName: "/exports/vm-root",
			wantHost:       "nfs-gw.internal",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			source, ok := diskSourceFromPath(tt.path)
			if ok != tt.wantNetwork {
				t.Fatalf("diskSourceFromPath(%q) network=%v want=%v", tt.path, ok, tt.wantNetwork)
			}
			if !tt.wantNetwork {
				return
			}
			if source.Protocol != tt.wantProtocol {
				t.Fatalf("protocol=%q want=%q", source.Protocol, tt.wantProtocol)
			}
			if source.Name != tt.wantSourceName {
				t.Fatalf("name=%q want=%q", source.Name, tt.wantSourceName)
			}
			if tt.wantHost != "" {
				if len(source.Hosts) != 1 || source.Hosts[0].Name != tt.wantHost {
					t.Fatalf("hosts=%v want single host=%q", source.Hosts, tt.wantHost)
				}
			}
		})
	}
}

func TestGenerateDomainXML_MixesFileAndNetworkDisks(t *testing.T) {
	rt := &VMRuntime{}
	spec := &models.VMSpec{
		Name:     "vm-test",
		VCPUs:    2,
		MemoryMB: 1024,
		Disks: []models.DiskConfig{
			{
				Path:   "rbd:rbd/vm-test-root",
				Device: "vda",
				Format: "raw",
				Type:   models.DiskTypeDisk,
			},
			{
				Path:   "/var/lib/libvirt/images/vm-test-data.qcow2",
				Device: "vdb",
				Format: "qcow2",
				Type:   models.DiskTypeDisk,
			},
		},
	}

	xmlData, err := rt.generateDomainXML("vm-test", spec)
	if err != nil {
		t.Fatalf("generateDomainXML returned error: %v", err)
	}

	type source struct {
		File     string `xml:"file,attr"`
		Protocol string `xml:"protocol,attr"`
		Name     string `xml:"name,attr"`
	}
	type disk struct {
		Type   string `xml:"type,attr"`
		Source source `xml:"source"`
	}
	type domain struct {
		Devices struct {
			Disks []disk `xml:"disk"`
		} `xml:"devices"`
	}

	var parsed domain
	if err := xml.Unmarshal([]byte(xmlData), &parsed); err != nil {
		t.Fatalf("failed to parse generated xml: %v", err)
	}

	if len(parsed.Devices.Disks) < 2 {
		t.Fatalf("expected at least 2 disks, got %d", len(parsed.Devices.Disks))
	}

	var foundNetwork bool
	var foundFile bool
	for _, d := range parsed.Devices.Disks {
		if d.Type == "network" && d.Source.Protocol == "rbd" && d.Source.Name == "rbd/vm-test-root" {
			foundNetwork = true
		}
		if d.Type == "file" && strings.Contains(d.Source.File, "vm-test-data.qcow2") {
			foundFile = true
		}
	}

	if !foundNetwork {
		t.Fatalf("expected generated xml to include rbd network disk")
	}
	if !foundFile {
		t.Fatalf("expected generated xml to include file-backed disk")
	}
}
