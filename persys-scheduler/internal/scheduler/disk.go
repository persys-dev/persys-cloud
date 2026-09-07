package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
)

// CreateDiskRequest is the control-plane API for standalone disk inventory.
type CreateDiskRequest struct {
	Name         string `json:"name"`
	Driver       string `json:"driver"` // local | ceph-rbd | nfs
	SizeGB       int64  `json:"size_gb"`
	FSType       string `json:"fs_type,omitempty"`
	AccessMode   string `json:"access_mode,omitempty"`
	RetainPolicy string `json:"retain_policy,omitempty"`
	// NodeID optional: only for local pre-pin. Normally local disks get node_id
	// from workload placement.
	NodeID    string `json:"node_id,omitempty"`
	MountPath string `json:"mount_path,omitempty"`
}

// DiskView is the API view of a managed volume record (standalone or derived).
type DiskView struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Driver        string    `json:"driver"`
	SizeGB        int64     `json:"size_gb"`
	FSType        string    `json:"fs_type,omitempty"`
	AccessMode    string    `json:"access_mode,omitempty"`
	RetainPolicy  string    `json:"retain_policy,omitempty"`
	Phase         string    `json:"phase"`
	LastError     string    `json:"last_error,omitempty"`
	NodeID        string    `json:"node_id,omitempty"`
	Device        string    `json:"device,omitempty"`
	Standalone    bool      `json:"standalone"`
	MountPath     string    `json:"mount_path,omitempty"`
	WorkloadRefs  []string  `json:"workload_refs,omitempty"`
	AttachedNodes []string  `json:"attached_nodes,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

func diskViewFromRecord(r models.ManagedVolumeRecord) DiskView {
	return DiskView{
		ID:            r.ID,
		Name:          r.Name,
		Driver:        r.Driver,
		SizeGB:        r.SizeGB,
		FSType:        r.FSType,
		AccessMode:    r.AccessMode,
		RetainPolicy:  r.RetainPolicy,
		Phase:         r.Phase,
		LastError:     r.LastError,
		NodeID:        r.NodeID,
		Device:        r.Device,
		Standalone:    r.Standalone,
		MountPath:     r.MountPath,
		WorkloadRefs:  r.WorkloadRefs,
		AttachedNodes: r.AttachedNodes,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

func canonicalDiskDriver(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	switch d {
	case "ceph_rbd", "rbd", "ceph-rbd":
		return "ceph-rbd"
	case "local", "nfs":
		return d
	default:
		return d
	}
}

// CreateDisk registers a standalone disk in etcd.
//
// ceph-rbd / nfs: phase Available (image/export provisioned on first agent attach).
// local: phase Pending until bound to the workload placement node.
func (s *Scheduler) CreateDisk(req CreateDiskRequest) (*DiskView, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	driver := canonicalDiskDriver(req.Driver)
	if driver == "" {
		return nil, fmt.Errorf("driver is required (local|ceph-rbd|nfs)")
	}
	if req.SizeGB <= 0 {
		req.SizeGB = 1
	}
	retain := strings.TrimSpace(req.RetainPolicy)
	if retain == "" {
		retain = "Delete"
	}
	fs := strings.TrimSpace(req.FSType)
	if fs == "" && driver != "local" {
		fs = "ext4"
	}
	access := strings.TrimSpace(req.AccessMode)
	if access == "" {
		access = "ReadWriteOnce"
	}
	mount := strings.TrimSpace(req.MountPath)
	if mount == "" {
		mount = "/data"
	}

	id := "disk-" + uuid.NewString()
	phase := "Available"
	if driver == "local" {
		phase = "Pending"
	}

	device := ""
	if driver == "ceph-rbd" {
		device = fmt.Sprintf("rbd:%s", name)
	}

	now := time.Now().UTC()
	rec := models.ManagedVolumeRecord{
		ID:           id,
		Name:         name,
		Driver:       driver,
		SizeGB:       req.SizeGB,
		AccessMode:   access,
		FSType:       fs,
		RetainPolicy: retain,
		Phase:        phase,
		NodeID:       strings.TrimSpace(req.NodeID),
		Device:       device,
		Standalone:   true,
		MountPath:    mount,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.saveManagedVolumeRecord(rec); err != nil {
		return nil, err
	}
	view := diskViewFromRecord(rec)
	return &view, nil
}

// ListDisks returns all managed volume records.
func (s *Scheduler) ListDisks() ([]DiskView, error) {
	recs, err := s.listManagedVolumeRecords()
	if err != nil {
		return nil, err
	}
	out := make([]DiskView, 0, len(recs))
	for _, r := range recs {
		out = append(out, diskViewFromRecord(r))
	}
	return out, nil
}

// GetDisk returns one disk by id.
func (s *Scheduler) GetDisk(id string) (*DiskView, error) {
	rec, ok, err := s.getManagedVolumeRecord(strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if !ok || rec == nil {
		return nil, fmt.Errorf("disk %q not found", id)
	}
	v := diskViewFromRecord(*rec)
	return &v, nil
}

// DeleteDisk removes inventory. Refuses if still referenced unless force.
func (s *Scheduler) DeleteDisk(id string, force bool) error {
	rec, ok, err := s.getManagedVolumeRecord(strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if !ok || rec == nil {
		return nil
	}
	if !force && len(rec.WorkloadRefs) > 0 {
		return fmt.Errorf("disk %q is bound to workloads %v; detach or pass force", id, rec.WorkloadRefs)
	}
	return s.deleteManagedVolumeRecord(rec.ID)
}

// ExpandDiskRefsToManagedVolumes resolves disk ids into ManagedVolumeSpec for the agent.
// Local disks: stamp NodeID from assignedNode when empty.
func (s *Scheduler) ExpandDiskRefsToManagedVolumes(diskIDs []string, assignedNode string, workloadID string) ([]models.ManagedVolumeSpec, error) {
	out := make([]models.ManagedVolumeSpec, 0, len(diskIDs))
	for _, id := range diskIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		rec, ok, err := s.getManagedVolumeRecord(id)
		if err != nil {
			return nil, err
		}
		if !ok || rec == nil {
			return nil, fmt.Errorf("disk %q not found", id)
		}
		refs := append([]string{}, rec.WorkloadRefs...)
		found := false
		for _, r := range refs {
			if r == workloadID {
				found = true
				break
			}
		}
		if !found && workloadID != "" {
			refs = append(refs, workloadID)
		}
		rec.WorkloadRefs = refs
		if rec.Driver == "local" && strings.TrimSpace(rec.NodeID) == "" && assignedNode != "" {
			rec.NodeID = assignedNode
			rec.Phase = "Provisioned"
		}
		if assignedNode != "" {
			nfound := false
			for _, n := range rec.AttachedNodes {
				if n == assignedNode {
					nfound = true
					break
				}
			}
			if !nfound {
				rec.AttachedNodes = append(rec.AttachedNodes, assignedNode)
			}
			if rec.Phase == "Available" || rec.Phase == "Pending" || rec.Phase == "Provisioned" {
				rec.Phase = "Attached"
			}
		}
		if err := s.saveManagedVolumeRecord(*rec); err != nil {
			return nil, err
		}

		mount := rec.MountPath
		if mount == "" {
			mount = "/data"
		}
		out = append(out, models.ManagedVolumeSpec{
			Name:         rec.Name,
			Driver:       rec.Driver,
			SizeGB:       rec.SizeGB,
			AccessMode:   rec.AccessMode,
			FSType:       rec.FSType,
			MountPath:    mount,
			RetainPolicy: rec.RetainPolicy,
		})
	}
	return out, nil
}

// AttachDiskIDsToWorkload reads persys.disk.ids (or disk_ids) from metadata
// and appends expanded managed volume specs onto the workload.
func (s *Scheduler) AttachDiskIDsToWorkload(workload *models.Workload) error {
	if workload == nil {
		return nil
	}
	ids := parseDiskIDsFromWorkload(*workload)
	if len(ids) == 0 {
		return nil
	}
	specs, err := s.ExpandDiskRefsToManagedVolumes(ids, strings.TrimSpace(workload.NodeID), workload.ID)
	if err != nil {
		return err
	}
	if workload.VM != nil {
		workload.VM.ManagedVolumes = append(workload.VM.ManagedVolumes, specs...)
		return nil
	}
	workload.ManagedVolumes = append(workload.ManagedVolumes, specs...)
	return nil
}

func parseDiskIDsFromWorkload(w models.Workload) []string {
	raw := metaString(w.Metadata, "persys.disk.ids", "disk_ids")
	if raw == "" {
		raw = metaStringFromStringMap(w.Labels, "persys.disk.ids", "disk_ids")
	}
	if raw == "" && w.VM != nil {
		raw = metaStringFromStringMap(w.VM.Metadata, "persys.disk.ids", "disk_ids")
	}
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func metaString(m map[string]interface{}, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func metaStringFromStringMap(m map[string]string, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if s := strings.TrimSpace(m[k]); s != "" {
			return s
		}
	}
	return ""
}
