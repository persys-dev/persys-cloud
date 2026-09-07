package scheduler

import (
	"fmt"
	"strings"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
)

const (
	metaAllowMove        = "persys.scheduling.allow_move"
	metaPersistenceClass = "persys.scheduling.persistence_class"
	metaPinnedReason     = "persys.scheduling.pinned_reason"
	persistenceEphemeral = "ephemeral"
	persistenceNodeLocal = "node-local"
	persistenceShared    = "shared-storage"
)

func isMetadataTruthy(meta map[string]interface{}, key string) bool {
	if meta == nil {
		return false
	}
	v, ok := meta[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return s == "true" || s == "1" || s == "yes"
	default:
		return strings.EqualFold(strings.TrimSpace(fmt.Sprint(t)), "true")
	}
}

func metaStringRaw(meta map[string]interface{}, key string) string {
	if meta == nil {
		return ""
	}
	v, ok := meta[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func workloadAllowsMove(w models.Workload) bool {
	return isMetadataTruthy(w.Metadata, metaAllowMove) || isMetadataTruthy(w.Metadata, "allow_move")
}

func workloadPersistenceClass(w models.Workload) string {
	if explicit := strings.ToLower(metaStringRaw(w.Metadata, metaPersistenceClass)); explicit != "" {
		switch explicit {
		case persistenceEphemeral, persistenceNodeLocal, persistenceShared:
			return explicit
		}
	}
	t := strings.ToLower(strings.TrimSpace(w.Type))
	if t == "vm" || t == "microvm" {
		if isMetadataTruthy(w.Metadata, "persys.storage.shared") {
			return persistenceShared
		}
		return persistenceNodeLocal
	}
	if isMetadataTruthy(w.Metadata, "persys.storage.local") {
		return persistenceNodeLocal
	}
	if isMetadataTruthy(w.Metadata, "persys.storage.shared") {
		return persistenceShared
	}
	if w.Metadata != nil {
		for k, v := range w.Metadata {
			ks := strings.ToLower(k)
			if strings.Contains(ks, "host_path") || strings.Contains(ks, "bind_mount") {
				if strings.TrimSpace(fmt.Sprint(v)) != "" {
					return persistenceNodeLocal
				}
			}
		}
	}
	return persistenceEphemeral
}

func isNodeLocalWorkload(w models.Workload) bool {
	return workloadPersistenceClass(w) == persistenceNodeLocal && !workloadAllowsMove(w)
}
