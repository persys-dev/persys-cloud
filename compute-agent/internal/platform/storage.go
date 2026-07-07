package platform

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// StorageProvider handles lifecycle for one storage backend (local/nfs/ceph-rbd).
type StorageProvider interface {
	Driver() string
	Validate(ctx context.Context, spec VolumeSpec) error
	Provision(ctx context.Context, spec VolumeSpec) (*VolumeHandle, error)
	Delete(ctx context.Context, handle *VolumeHandle) error
	Attach(ctx context.Context, handle *VolumeHandle, workloadID string, mountPath string, readOnly bool) (*VolumeAttachment, error)
	Detach(ctx context.Context, attachment *VolumeAttachment) error
}

// VolumeManager orchestrates provider-level volume operations.
type VolumeManager interface {
	ResolveProvider(driver string) (StorageProvider, error)
	Provision(ctx context.Context, spec VolumeSpec) (*VolumeHandle, error)
	Attach(ctx context.Context, spec VolumeSpec, handle *VolumeHandle, workloadID string) (*VolumeAttachment, error)
	Detach(ctx context.Context, attachment *VolumeAttachment) error
	Delete(ctx context.Context, handle *VolumeHandle) error
}

// ProviderRegistry stores and resolves storage providers by driver.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]StorageProvider
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[string]StorageProvider),
	}
}

func (r *ProviderRegistry) RegisterStorageProvider(provider StorageProvider) {
	if provider == nil {
		return
	}
	driver := normalizeDriver(provider.Driver())
	if driver == "" {
		return
	}
	r.mu.Lock()
	r.providers[driver] = provider
	r.mu.Unlock()
}

func (r *ProviderRegistry) StorageProvider(driver string) (StorageProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[normalizeDriver(driver)]
	return provider, ok
}

type DefaultVolumeManager struct {
	registry *ProviderRegistry
}

func NewDefaultVolumeManager(registry *ProviderRegistry) *DefaultVolumeManager {
	return &DefaultVolumeManager{registry: registry}
}

func (m *DefaultVolumeManager) ResolveProvider(driver string) (StorageProvider, error) {
	if m == nil || m.registry == nil {
		return nil, fmt.Errorf("storage provider registry is not configured")
	}
	provider, ok := m.registry.StorageProvider(driver)
	if !ok {
		return nil, fmt.Errorf("storage driver %q is not registered", driver)
	}
	return provider, nil
}

func (m *DefaultVolumeManager) Provision(ctx context.Context, spec VolumeSpec) (*VolumeHandle, error) {
	provider, err := m.ResolveProvider(spec.Driver)
	if err != nil {
		return nil, err
	}
	if err := provider.Validate(ctx, spec); err != nil {
		return nil, err
	}
	return provider.Provision(ctx, spec)
}

func (m *DefaultVolumeManager) Attach(ctx context.Context, spec VolumeSpec, handle *VolumeHandle, workloadID string) (*VolumeAttachment, error) {
	provider, err := m.ResolveProvider(spec.Driver)
	if err != nil {
		return nil, err
	}
	return provider.Attach(ctx, handle, workloadID, spec.MountPath, spec.ReadOnly)
}

func (m *DefaultVolumeManager) Detach(ctx context.Context, attachment *VolumeAttachment) error {
	if attachment == nil {
		return nil
	}
	provider, err := m.ResolveProvider(attachmentDriver(attachment))
	if err != nil {
		return err
	}
	return provider.Detach(ctx, attachment)
}

func (m *DefaultVolumeManager) Delete(ctx context.Context, handle *VolumeHandle) error {
	if handle == nil {
		return nil
	}
	provider, err := m.ResolveProvider(handle.Driver)
	if err != nil {
		return err
	}
	return provider.Delete(ctx, handle)
}

func attachmentDriver(attachment *VolumeAttachment) string {
	if attachment == nil {
		return ""
	}
	if attachment.Metadata == nil {
		return ""
	}
	return attachment.Metadata["driver"]
}

func normalizeDriver(driver string) string {
	return strings.ToLower(strings.TrimSpace(driver))
}
