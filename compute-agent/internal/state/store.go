package state

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/platform"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	bolt "go.etcd.io/bbolt"
)

const (
	workloadBucket   = "workloads"
	statusBucket     = "status"
	volumeBucket     = "volumes"
	attachmentBucket = "attachments"
)

// Store manages persistent state using bbolt
type Store interface {
	// Workload operations
	SaveWorkload(workload *models.Workload) error
	GetWorkload(id string) (*models.Workload, error)
	DeleteWorkload(id string) error
	ListWorkloads() ([]*models.Workload, error)

	// Status operations
	SaveStatus(status *models.WorkloadStatus) error
	GetStatus(id string) (*models.WorkloadStatus, error)
	ListStatuses() ([]*models.WorkloadStatus, error)

	// Utility
	Close() error
}

// ManagedVolumeStore provides optional persistence for provider-backed volume state.
type ManagedVolumeStore interface {
	SaveVolume(handle *platform.VolumeHandle) error
	GetVolume(id string) (*platform.VolumeHandle, error)
	DeleteVolume(id string) error
	ListVolumes() ([]*platform.VolumeHandle, error)

	SaveAttachment(attachment *platform.VolumeAttachment) error
	GetAttachment(id string) (*platform.VolumeAttachment, error)
	DeleteAttachment(id string) error
	ListAttachments(workloadID string) ([]*platform.VolumeAttachment, error)
}

type boltStore struct {
	db *bolt.DB
}

// NewBoltStore creates a new bbolt-backed state store
func NewBoltStore(path string) (Store, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{
		Timeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Initialize buckets
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(workloadBucket)); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(statusBucket)); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(volumeBucket)); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(attachmentBucket)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize buckets: %w", err)
	}

	return &boltStore{db: db}, nil
}

func (s *boltStore) SaveWorkload(workload *models.Workload) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(workloadBucket))

		workload.UpdatedAt = time.Now()
		if workload.CreatedAt.IsZero() {
			workload.CreatedAt = workload.UpdatedAt
		}

		data, err := json.Marshal(workload)
		if err != nil {
			return fmt.Errorf("failed to marshal workload: %w", err)
		}

		return bucket.Put([]byte(workload.ID), data)
	})
}

func (s *boltStore) GetWorkload(id string) (*models.Workload, error) {
	var workload *models.Workload

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(workloadBucket))
		data := bucket.Get([]byte(id))

		if data == nil {
			return fmt.Errorf("workload not found")
		}

		workload = &models.Workload{}
		return json.Unmarshal(data, workload)
	})

	if err != nil {
		return nil, err
	}

	return workload, nil
}

func (s *boltStore) DeleteWorkload(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		workloadBucket := tx.Bucket([]byte(workloadBucket))
		statusBucket := tx.Bucket([]byte(statusBucket))
		attachmentBucket := tx.Bucket([]byte(attachmentBucket))

		// Delete both workload and status
		if err := workloadBucket.Delete([]byte(id)); err != nil {
			return err
		}
		if err := statusBucket.Delete([]byte(id)); err != nil {
			return err
		}
		if attachmentBucket == nil {
			return nil
		}
		keysToDelete := make([][]byte, 0)
		if err := attachmentBucket.ForEach(func(k, v []byte) error {
			var attachment platform.VolumeAttachment
			if err := json.Unmarshal(v, &attachment); err != nil {
				return nil
			}
			if attachment.WorkloadID == id {
				keyCopy := make([]byte, len(k))
				copy(keyCopy, k)
				keysToDelete = append(keysToDelete, keyCopy)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, key := range keysToDelete {
			if err := attachmentBucket.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *boltStore) ListWorkloads() ([]*models.Workload, error) {
	var workloads []*models.Workload

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(workloadBucket))

		return bucket.ForEach(func(k, v []byte) error {
			var workload models.Workload
			if err := json.Unmarshal(v, &workload); err != nil {
				return err
			}
			workloads = append(workloads, &workload)
			return nil
		})
	})

	if err != nil {
		return nil, err
	}

	return workloads, nil
}

func (s *boltStore) SaveStatus(status *models.WorkloadStatus) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(statusBucket))

		status.UpdatedAt = time.Now()
		if status.CreatedAt.IsZero() {
			status.CreatedAt = status.UpdatedAt
		}

		data, err := json.Marshal(status)
		if err != nil {
			return fmt.Errorf("failed to marshal status: %w", err)
		}

		return bucket.Put([]byte(status.ID), data)
	})
}

func (s *boltStore) GetStatus(id string) (*models.WorkloadStatus, error) {
	var status *models.WorkloadStatus

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(statusBucket))
		data := bucket.Get([]byte(id))

		if data == nil {
			return fmt.Errorf("status not found")
		}

		status = &models.WorkloadStatus{}
		return json.Unmarshal(data, status)
	})

	if err != nil {
		return nil, err
	}

	return status, nil
}

func (s *boltStore) ListStatuses() ([]*models.WorkloadStatus, error) {
	var statuses []*models.WorkloadStatus

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(statusBucket))

		return bucket.ForEach(func(k, v []byte) error {
			var status models.WorkloadStatus
			if err := json.Unmarshal(v, &status); err != nil {
				return err
			}
			statuses = append(statuses, &status)
			return nil
		})
	})

	if err != nil {
		return nil, err
	}

	return statuses, nil
}

func (s *boltStore) Close() error {
	return s.db.Close()
}
