package state

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/platform"
	bolt "go.etcd.io/bbolt"
)

func (s *boltStore) SaveVolume(handle *platform.VolumeHandle) error {
	if handle == nil {
		return fmt.Errorf("volume handle is required")
	}
	if strings.TrimSpace(handle.ID) == "" {
		return fmt.Errorf("volume handle id is required")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(volumeBucket))
		now := time.Now().UTC()
		handle.UpdatedAt = now
		if handle.CreatedAt.IsZero() {
			handle.CreatedAt = now
		}
		data, err := json.Marshal(handle)
		if err != nil {
			return fmt.Errorf("failed to marshal volume handle: %w", err)
		}
		return bucket.Put([]byte(handle.ID), data)
	})
}

func (s *boltStore) GetVolume(id string) (*platform.VolumeHandle, error) {
	var out *platform.VolumeHandle
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(volumeBucket))
		data := bucket.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("volume not found")
		}
		out = &platform.VolumeHandle{}
		return json.Unmarshal(data, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *boltStore) DeleteVolume(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(volumeBucket)).Delete([]byte(id))
	})
}

func (s *boltStore) ListVolumes() ([]*platform.VolumeHandle, error) {
	out := []*platform.VolumeHandle{}
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(volumeBucket))
		return bucket.ForEach(func(_, v []byte) error {
			var handle platform.VolumeHandle
			if err := json.Unmarshal(v, &handle); err != nil {
				return err
			}
			out = append(out, &handle)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *boltStore) SaveAttachment(attachment *platform.VolumeAttachment) error {
	if attachment == nil {
		return fmt.Errorf("volume attachment is required")
	}
	if strings.TrimSpace(attachment.ID) == "" {
		return fmt.Errorf("volume attachment id is required")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(attachmentBucket))
		now := time.Now().UTC()
		attachment.UpdatedAt = now
		if attachment.CreatedAt.IsZero() {
			attachment.CreatedAt = now
		}
		data, err := json.Marshal(attachment)
		if err != nil {
			return fmt.Errorf("failed to marshal volume attachment: %w", err)
		}
		return bucket.Put([]byte(attachment.ID), data)
	})
}

func (s *boltStore) GetAttachment(id string) (*platform.VolumeAttachment, error) {
	var out *platform.VolumeAttachment
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(attachmentBucket))
		data := bucket.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("attachment not found")
		}
		out = &platform.VolumeAttachment{}
		return json.Unmarshal(data, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *boltStore) DeleteAttachment(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(attachmentBucket)).Delete([]byte(id))
	})
}

func (s *boltStore) ListAttachments(workloadID string) ([]*platform.VolumeAttachment, error) {
	filterWorkload := strings.TrimSpace(workloadID)
	out := []*platform.VolumeAttachment{}
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(attachmentBucket))
		return bucket.ForEach(func(_, v []byte) error {
			var attachment platform.VolumeAttachment
			if err := json.Unmarshal(v, &attachment); err != nil {
				return err
			}
			if filterWorkload != "" && attachment.WorkloadID != filterWorkload {
				return nil
			}
			out = append(out, &attachment)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

var _ ManagedVolumeStore = (*boltStore)(nil)
