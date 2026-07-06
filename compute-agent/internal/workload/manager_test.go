package workload

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	errors2 "github.com/persys-dev/persys-cloud/compute-agent/internal/errors"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/resources"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/retry"
	"github.com/persys-dev/persys-cloud/compute-agent/internal/runtime"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type serialStore struct {
	mu        sync.Mutex
	workloads map[string]*models.Workload
	statuses  map[string]*models.WorkloadStatus
}

func newSerialStore() *serialStore {
	return &serialStore{
		workloads: make(map[string]*models.Workload),
		statuses:  make(map[string]*models.WorkloadStatus),
	}
}

func cloneWorkload(w *models.Workload) *models.Workload {
	if w == nil {
		return nil
	}
	cp := *w
	if w.Spec != nil {
		cp.Spec = make(map[string]interface{}, len(w.Spec))
		for k, v := range w.Spec {
			cp.Spec[k] = v
		}
	}
	return &cp
}

func cloneStatus(s *models.WorkloadStatus) *models.WorkloadStatus {
	if s == nil {
		return nil
	}
	cp := *s
	if s.Metadata != nil {
		cp.Metadata = make(map[string]string, len(s.Metadata))
		for k, v := range s.Metadata {
			cp.Metadata[k] = v
		}
	}
	return &cp
}

func (s *serialStore) SaveWorkload(workload *models.Workload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workloads[workload.ID] = cloneWorkload(workload)
	return nil
}

func (s *serialStore) GetWorkload(id string) (*models.Workload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workloads[id]
	if !ok {
		return nil, fmt.Errorf("workload not found")
	}
	return cloneWorkload(w), nil
}

func (s *serialStore) DeleteWorkload(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workloads, id)
	delete(s.statuses, id)
	return nil
}

func (s *serialStore) ListWorkloads() ([]*models.Workload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*models.Workload, 0, len(s.workloads))
	for _, w := range s.workloads {
		out = append(out, cloneWorkload(w))
	}
	return out, nil
}

func (s *serialStore) SaveStatus(status *models.WorkloadStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[status.ID] = cloneStatus(status)
	return nil
}

func (s *serialStore) GetStatus(id string) (*models.WorkloadStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.statuses[id]
	if !ok {
		return nil, fmt.Errorf("status not found")
	}
	return cloneStatus(st), nil
}

func (s *serialStore) ListStatuses() ([]*models.WorkloadStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*models.WorkloadStatus, 0, len(s.statuses))
	for _, st := range s.statuses {
		out = append(out, cloneStatus(st))
	}
	return out, nil
}

func (s *serialStore) Close() error { return nil }

type conflictRuntime struct {
	createCalls int32
	exists      int32
}

func (r *conflictRuntime) Create(ctx context.Context, workload *models.Workload) error {
	atomic.AddInt32(&r.createCalls, 1)
	time.Sleep(25 * time.Millisecond)
	if !atomic.CompareAndSwapInt32(&r.exists, 0, 1) {
		return fmt.Errorf("container name conflict")
	}
	return nil
}

func (r *conflictRuntime) Start(ctx context.Context, id string) error { return nil }
func (r *conflictRuntime) Stop(ctx context.Context, id string) error  { return nil }
func (r *conflictRuntime) Delete(ctx context.Context, id string) error {
	atomic.StoreInt32(&r.exists, 0)
	return nil
}
func (r *conflictRuntime) Status(ctx context.Context, id string) (models.ActualState, string, error) {
	if atomic.LoadInt32(&r.exists) == 1 {
		return models.ActualStateRunning, "running", nil
	}
	return models.ActualStateUnknown, "container not found", nil
}
func (r *conflictRuntime) List(ctx context.Context) ([]string, error) {
	if atomic.LoadInt32(&r.exists) == 1 {
		return []string{"demo-c1"}, nil
	}
	return []string{}, nil
}
func (r *conflictRuntime) Type() models.WorkloadType { return models.WorkloadTypeContainer }
func (r *conflictRuntime) Healthy(ctx context.Context) error {
	return nil
}

// MockStore implements the state.Store interface for testing
type MockStore struct {
	mock.Mock
}

func (m *MockStore) SaveWorkload(workload *models.Workload) error {
	args := m.Called(workload)
	return args.Error(0)
}

func (m *MockStore) GetWorkload(id string) (*models.Workload, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Workload), args.Error(1)
}

func (m *MockStore) DeleteWorkload(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockStore) ListWorkloads() ([]*models.Workload, error) {
	args := m.Called()
	return args.Get(0).([]*models.Workload), args.Error(1)
}

func (m *MockStore) SaveStatus(status *models.WorkloadStatus) error {
	args := m.Called(status)
	return args.Error(0)
}

func (m *MockStore) GetStatus(id string) (*models.WorkloadStatus, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WorkloadStatus), args.Error(1)
}

func (m *MockStore) ListStatuses() ([]*models.WorkloadStatus, error) {
	args := m.Called()
	return args.Get(0).([]*models.WorkloadStatus), args.Error(1)
}

func (m *MockStore) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockRuntime implements the runtime.Runtime interface for testing
type MockRuntime struct {
	mock.Mock
}

type runtimeWithMetadata struct {
	*MockRuntime
	metadata map[string]string
	err      error
}

func (r *runtimeWithMetadata) StatusMetadata(ctx context.Context, id string) (map[string]string, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.metadata, nil
}

func (m *MockRuntime) Create(ctx context.Context, workload *models.Workload) error {
	args := m.Called(ctx, workload)
	return args.Error(0)
}

func (m *MockRuntime) Start(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockRuntime) Stop(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockRuntime) Delete(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockRuntime) Status(ctx context.Context, id string) (models.ActualState, string, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(models.ActualState), args.String(1), args.Error(2)
}

func (m *MockRuntime) List(ctx context.Context) ([]string, error) {
	args := m.Called(ctx)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockRuntime) Type() models.WorkloadType {
	args := m.Called()
	return args.Get(0).(models.WorkloadType)
}

func (m *MockRuntime) Healthy(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func TestApplyWorkload_NewWorkload(t *testing.T) {
	// Setup
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel) // Suppress logs in tests

	manager := NewManager(mockStore, runtimeMgr, logger)

	// Create test workload
	workload := &models.Workload{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateRunning,
		Spec: map[string]interface{}{
			"image": "nginx:latest",
		},
	}

	// Setup expectations
	mockStore.On("GetWorkload", "test-workload").Return(nil, assert.AnError)
	mockStore.On("SaveWorkload", mock.AnythingOfType("*models.Workload")).Return(nil)
	mockRuntime.On("Create", mock.Anything, workload).Return(nil)
	mockRuntime.On("Start", mock.Anything, "test-workload").Return(nil)
	mockRuntime.On("Status", mock.Anything, "test-workload").Return(
		models.ActualStateRunning, "running", nil,
	)
	mockStore.On("SaveStatus", mock.AnythingOfType("*models.WorkloadStatus")).Return(nil)

	// Execute
	ctx := context.Background()
	status, skipped, err := manager.ApplyWorkload(ctx, workload)

	// Assert
	assert.NoError(t, err)
	assert.False(t, skipped)
	assert.NotNil(t, status)
	assert.Equal(t, "test-workload", status.ID)
	assert.Equal(t, models.ActualStateRunning, status.ActualState)

	// Verify all expectations were met
	mockStore.AssertExpectations(t)
	mockRuntime.AssertExpectations(t)
}

func TestApplyWorkload_SameRevision_Skipped(t *testing.T) {
	// Setup
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager := NewManager(mockStore, runtimeMgr, logger)

	// Existing workload with same revision
	existing := &models.Workload{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateRunning,
		Spec: map[string]interface{}{
			"image": "nginx:latest",
		},
	}

	existingStatus := &models.WorkloadStatus{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateRunning,
		ActualState:  models.ActualStateRunning,
		Message:      "running",
	}

	// Setup expectations
	mockStore.On("GetWorkload", "test-workload").Return(existing, nil)
	mockStore.On("GetStatus", "test-workload").Return(existingStatus, nil)

	// Execute - apply same workload again
	ctx := context.Background()
	status, skipped, err := manager.ApplyWorkload(ctx, existing)

	// Assert
	assert.NoError(t, err)
	assert.True(t, skipped)
	assert.NotNil(t, status)
	assert.Equal(t, "rev-1", status.RevisionID)

	// Verify no runtime operations were called (skipped)
	mockRuntime.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	mockRuntime.AssertNotCalled(t, "Start", mock.Anything, mock.Anything)

	mockStore.AssertExpectations(t)
}

func TestApplyWorkload_SameRevision_DesiredStateChange_StopWithoutRecreate(t *testing.T) {
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	manager := NewManager(mockStore, runtimeMgr, logger)

	existing := &models.Workload{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateRunning,
		Spec:         map[string]interface{}{"image": "nginx:latest"},
	}
	request := &models.Workload{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateStopped,
		Spec:         map[string]interface{}{"image": "nginx:latest"},
	}
	status := &models.WorkloadStatus{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateRunning,
		ActualState:  models.ActualStateRunning,
		Message:      "running",
	}

	mockStore.On("GetWorkload", "test-workload").Return(existing, nil)
	mockStore.On("GetStatus", "test-workload").Return(status, nil)
	mockStore.On("SaveWorkload", mock.MatchedBy(func(w *models.Workload) bool {
		return w.ID == "test-workload" && w.DesiredState == models.DesiredStateStopped && w.RevisionID == "rev-1"
	})).Return(nil).Once()
	mockRuntime.On("Status", mock.Anything, "test-workload").Return(models.ActualStateRunning, "running", nil).Once()
	mockRuntime.On("Stop", mock.Anything, "test-workload").Return(nil).Once()
	mockRuntime.On("Status", mock.Anything, "test-workload").Return(models.ActualStateStopped, "stopped", nil).Once()
	mockStore.On("SaveStatus", mock.MatchedBy(func(s *models.WorkloadStatus) bool {
		return s.ID == "test-workload" &&
			s.RevisionID == "rev-1" &&
			s.DesiredState == models.DesiredStateStopped &&
			s.ActualState == models.ActualStateStopped
	})).Return(nil).Once()

	got, skipped, err := manager.ApplyWorkload(context.Background(), request)
	assert.NoError(t, err)
	assert.False(t, skipped)
	assert.Equal(t, models.DesiredStateStopped, got.DesiredState)
	assert.Equal(t, models.ActualStateStopped, got.ActualState)

	mockRuntime.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	mockRuntime.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything)
	mockRuntime.AssertNotCalled(t, "Start", mock.Anything, mock.Anything)
	mockStore.AssertExpectations(t)
	mockRuntime.AssertExpectations(t)
}

func TestApplyWorkload_SameRevision_DesiredStateChange_StartWithoutRecreate(t *testing.T) {
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	manager := NewManager(mockStore, runtimeMgr, logger)

	existing := &models.Workload{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateStopped,
		Spec:         map[string]interface{}{"image": "nginx:latest"},
	}
	request := &models.Workload{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateRunning,
		Spec:         map[string]interface{}{"image": "nginx:latest"},
	}
	status := &models.WorkloadStatus{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateStopped,
		ActualState:  models.ActualStateStopped,
		Message:      "stopped",
	}

	mockStore.On("GetWorkload", "test-workload").Return(existing, nil)
	mockStore.On("GetStatus", "test-workload").Return(status, nil)
	mockStore.On("SaveWorkload", mock.MatchedBy(func(w *models.Workload) bool {
		return w.ID == "test-workload" && w.DesiredState == models.DesiredStateRunning && w.RevisionID == "rev-1"
	})).Return(nil).Once()
	mockRuntime.On("Status", mock.Anything, "test-workload").Return(models.ActualStateStopped, "stopped", nil).Once()
	mockRuntime.On("Start", mock.Anything, "test-workload").Return(nil).Once()
	mockRuntime.On("Status", mock.Anything, "test-workload").Return(models.ActualStateRunning, "running", nil).Once()
	mockStore.On("SaveStatus", mock.MatchedBy(func(s *models.WorkloadStatus) bool {
		return s.ID == "test-workload" &&
			s.RevisionID == "rev-1" &&
			s.DesiredState == models.DesiredStateRunning &&
			s.ActualState == models.ActualStateRunning
	})).Return(nil).Once()

	got, skipped, err := manager.ApplyWorkload(context.Background(), request)
	assert.NoError(t, err)
	assert.False(t, skipped)
	assert.Equal(t, models.DesiredStateRunning, got.DesiredState)
	assert.Equal(t, models.ActualStateRunning, got.ActualState)

	mockRuntime.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	mockRuntime.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything)
	mockRuntime.AssertNotCalled(t, "Stop", mock.Anything, mock.Anything)
	mockStore.AssertExpectations(t)
	mockRuntime.AssertExpectations(t)
}

func TestApplyWorkload_SameRevisionFailedStatus_NotSkipped(t *testing.T) {
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	manager := NewManager(mockStore, runtimeMgr, logger)

	workload := &models.Workload{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateRunning,
		Spec:         map[string]interface{}{"image": "nginx:latest"},
	}

	existingStatus := &models.WorkloadStatus{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateRunning,
		ActualState:  models.ActualStateFailed,
		Message:      "admission rejected due to resource constraints",
	}

	mockStore.On("GetWorkload", "test-workload").Return(workload, nil)
	mockStore.On("GetStatus", "test-workload").Return(existingStatus, nil).Once()
	mockStore.On("SaveWorkload", mock.AnythingOfType("*models.Workload")).Return(nil).Once()
	mockStore.On("SaveStatus", mock.MatchedBy(func(s *models.WorkloadStatus) bool {
		return s.ID == "test-workload" && s.ActualState == models.ActualStatePending
	})).Return(nil).Once()
	mockRuntime.On("Create", mock.Anything, workload).Return(nil).Once()
	mockRuntime.On("Start", mock.Anything, "test-workload").Return(nil).Once()
	mockRuntime.On("Status", mock.Anything, "test-workload").Return(
		models.ActualStateRunning, "running", nil,
	).Once()
	mockStore.On("SaveStatus", mock.MatchedBy(func(s *models.WorkloadStatus) bool {
		return s.ID == "test-workload" && s.ActualState == models.ActualStateRunning
	})).Return(nil).Once()

	status, skipped, err := manager.ApplyWorkload(context.Background(), workload)
	assert.NoError(t, err)
	assert.False(t, skipped)
	assert.NotNil(t, status)
	assert.Equal(t, models.ActualStateRunning, status.ActualState)

	mockRuntime.AssertCalled(t, "Create", mock.Anything, workload)
	mockStore.AssertExpectations(t)
	mockRuntime.AssertExpectations(t)
}

func TestApplyWorkload_DifferentRevision_Recreated(t *testing.T) {
	// Setup
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager := NewManager(mockStore, runtimeMgr, logger)

	// Existing workload with different revision
	existing := &models.Workload{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateRunning,
		Spec: map[string]interface{}{
			"image": "nginx:1.20",
		},
	}

	// New workload with updated revision
	updated := &models.Workload{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-2",
		DesiredState: models.DesiredStateRunning,
		Spec: map[string]interface{}{
			"image": "nginx:1.21",
		},
	}

	// Setup expectations
	mockStore.On("GetWorkload", "test-workload").Return(existing, nil)
	mockRuntime.On("Delete", mock.Anything, "test-workload").Return(nil)
	mockStore.On("SaveWorkload", mock.AnythingOfType("*models.Workload")).Return(nil)
	mockRuntime.On("Create", mock.Anything, updated).Return(nil)
	mockRuntime.On("Start", mock.Anything, "test-workload").Return(nil)
	mockRuntime.On("Status", mock.Anything, "test-workload").Return(
		models.ActualStateRunning, "running", nil,
	)
	mockStore.On("SaveStatus", mock.AnythingOfType("*models.WorkloadStatus")).Return(nil)

	// Execute
	ctx := context.Background()
	status, skipped, err := manager.ApplyWorkload(ctx, updated)

	// Assert
	assert.NoError(t, err)
	assert.False(t, skipped)
	assert.NotNil(t, status)
	assert.Equal(t, "rev-2", status.RevisionID)

	// Verify delete was called (recreation)
	mockRuntime.AssertCalled(t, "Delete", mock.Anything, "test-workload")

	mockStore.AssertExpectations(t)
	mockRuntime.AssertExpectations(t)
}

func TestDeleteWorkload(t *testing.T) {
	// Setup
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager := NewManager(mockStore, runtimeMgr, logger)

	workload := &models.Workload{
		ID:   "test-workload",
		Type: models.WorkloadTypeContainer,
	}

	// Setup expectations
	mockStore.On("GetWorkload", "test-workload").Return(workload, nil)
	mockRuntime.On("Delete", mock.Anything, "test-workload").Return(nil)
	mockStore.On("DeleteWorkload", "test-workload").Return(nil)

	// Execute
	ctx := context.Background()
	err := manager.DeleteWorkload(ctx, "test-workload")

	// Assert
	assert.NoError(t, err)

	mockStore.AssertExpectations(t)
	mockRuntime.AssertExpectations(t)
}

func TestReconcileWorkload_StartStopped(t *testing.T) {
	// Setup
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager := NewManager(mockStore, runtimeMgr, logger)

	workload := &models.Workload{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		DesiredState: models.DesiredStateRunning,
	}

	status := &models.WorkloadStatus{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		DesiredState: models.DesiredStateRunning,
		ActualState:  models.ActualStateStopped,
	}

	// Setup expectations
	mockStore.On("GetWorkload", "test-workload").Return(workload, nil)
	mockStore.On("GetStatus", "test-workload").Return(status, nil)
	mockRuntime.On("Status", mock.Anything, "test-workload").Return(
		models.ActualStateStopped, "stopped", nil,
	).Once()
	mockRuntime.On("Start", mock.Anything, "test-workload").Return(nil)
	mockRuntime.On("Status", mock.Anything, "test-workload").Return(
		models.ActualStateRunning, "running", nil,
	).Once()
	mockStore.On("SaveStatus", mock.AnythingOfType("*models.WorkloadStatus")).Return(nil)

	// Execute
	ctx := context.Background()
	err := manager.ReconcileWorkload(ctx, "test-workload")

	// Assert
	assert.NoError(t, err)

	// Verify Start was called
	mockRuntime.AssertCalled(t, "Start", mock.Anything, "test-workload")

	mockStore.AssertExpectations(t)
	mockRuntime.AssertExpectations(t)
}

func TestApplyWorkload_ResourceUnavailable_FailsBeforeCreate(t *testing.T) {
	// Setup
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager := NewManager(mockStore, runtimeMgr, logger)
	manager.resourceMonitor = nil // ensure default path first

	workload := &models.Workload{
		ID:           "test-workload",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateRunning,
		Spec: map[string]interface{}{
			"image": "nginx:latest",
		},
	}

	// Configure existing lookup
	mockStore.On("GetWorkload", "test-workload").Return(nil, errors.New("not found"))
	mockStore.On("SaveWorkload", mock.AnythingOfType("*models.Workload")).Return(nil).Once()
	mockStore.On("GetStatus", "test-workload").Return(nil, errors.New("not found")).Once()
	mockStore.On("SaveStatus", mock.MatchedBy(func(s *models.WorkloadStatus) bool {
		return s.ID == "test-workload" && s.ActualState == models.ActualStateFailed
	})).Return(nil).Once()

	// Attach a real monitor with impossible thresholds so it fails deterministically
	manager.SetResourceMonitor(resources.NewMonitor(&resources.Thresholds{
		MemoryThreshold: -1,
		CPUThreshold:    -1,
		DiskThreshold:   -1,
	}, logger))

	// Execute
	ctx := context.Background()
	status, skipped, err := manager.ApplyWorkload(ctx, workload)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, status)
	assert.False(t, skipped)

	var workloadErr *errors2.WorkloadError
	assert.ErrorAs(t, err, &workloadErr)
	assert.Equal(t, errors2.ErrCodeResourceQuotaExceeded, workloadErr.Code)

	// Verify no runtime operations were called
	mockRuntime.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	mockRuntime.AssertNotCalled(t, "Start", mock.Anything, mock.Anything)

	mockStore.AssertExpectations(t)
}

func TestApplyWorkload_CreateFailure_PersistsFailedStatus(t *testing.T) {
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	manager := NewManager(mockStore, runtimeMgr, logger)

	workload := &models.Workload{
		ID:           "failed-create",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateRunning,
		Spec:         map[string]interface{}{"image": "nginx:latest"},
	}

	mockStore.On("GetWorkload", "failed-create").Return(nil, errors.New("not found"))
	mockStore.On("SaveWorkload", mock.AnythingOfType("*models.Workload")).Return(nil)
	mockStore.On("SaveStatus", mock.MatchedBy(func(s *models.WorkloadStatus) bool {
		return s.ID == "failed-create" && s.ActualState == models.ActualStatePending
	})).Return(nil).Once()
	mockRuntime.On("Create", mock.Anything, workload).Return(errors.New("image pull failed")).Once()
	mockStore.On("GetStatus", "failed-create").Return(&models.WorkloadStatus{
		ID:           "failed-create",
		Type:         models.WorkloadTypeContainer,
		DesiredState: models.DesiredStateRunning,
		ActualState:  models.ActualStatePending,
	}, nil).Once()
	mockStore.On("SaveStatus", mock.MatchedBy(func(s *models.WorkloadStatus) bool {
		return s.ID == "failed-create" && s.ActualState == models.ActualStateFailed
	})).Return(nil).Once()

	status, skipped, err := manager.ApplyWorkload(context.Background(), workload)
	assert.Error(t, err)
	assert.Nil(t, status)
	assert.False(t, skipped)
	mockStore.AssertNotCalled(t, "DeleteWorkload", "failed-create")
	mockStore.AssertExpectations(t)
	mockRuntime.AssertExpectations(t)
}

func TestReconcileWorkload_FailedWorkload_RespectsRetryBackoff(t *testing.T) {
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager := NewManager(mockStore, runtimeMgr, logger)
	manager.SetRetryPolicy(&retry.RetryPolicy{
		MaxAttempts:        3,
		InitialDelay:       1 * time.Hour,
		MaxDelay:           1 * time.Hour,
		BackoffMultiplier:  2,
		OnlyRetryTransient: true,
	})

	workload := &models.Workload{
		ID:           "failed-workload",
		Type:         models.WorkloadTypeContainer,
		DesiredState: models.DesiredStateRunning,
	}

	status := &models.WorkloadStatus{
		ID:           "failed-workload",
		Type:         models.WorkloadTypeContainer,
		DesiredState: models.DesiredStateRunning,
		ActualState:  models.ActualStateFailed,
		Message:      "network timeout while pulling image",
	}

	mockStore.On("GetWorkload", "failed-workload").Return(workload, nil)
	mockStore.On("GetStatus", "failed-workload").Return(status, nil)
	mockRuntime.On("Status", mock.Anything, "failed-workload").Return(
		models.ActualStateFailed, "network timeout while pulling image", nil,
	).Once()

	mockStore.On("SaveStatus", mock.MatchedBy(func(s *models.WorkloadStatus) bool {
		if s.Metadata == nil {
			return false
		}
		return s.Metadata["retry_attempts"] == "1" && s.Metadata["failure_reason"] == "IMAGE_PULL_TIMEOUT"
	})).Return(nil).Once()

	err := manager.ReconcileWorkload(context.Background(), "failed-workload")
	assert.NoError(t, err)

	mockRuntime.AssertNotCalled(t, "Delete", mock.Anything, "failed-workload")
	mockRuntime.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	mockRuntime.AssertNotCalled(t, "Start", mock.Anything, "failed-workload")
	mockStore.AssertExpectations(t)
}

func TestReconcileWorkload_FailedWorkload_DoesNotConsumeAttemptsBeforeBackoffWindow(t *testing.T) {
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager := NewManager(mockStore, runtimeMgr, logger)
	manager.SetRetryPolicy(&retry.RetryPolicy{
		MaxAttempts:        3,
		InitialDelay:       1 * time.Hour,
		MaxDelay:           1 * time.Hour,
		BackoffMultiplier:  2,
		OnlyRetryTransient: true,
	})

	workload := &models.Workload{
		ID:           "failed-workload",
		Type:         models.WorkloadTypeContainer,
		DesiredState: models.DesiredStateRunning,
	}

	status := &models.WorkloadStatus{
		ID:           "failed-workload",
		Type:         models.WorkloadTypeContainer,
		DesiredState: models.DesiredStateRunning,
		ActualState:  models.ActualStateFailed,
		Message:      "network timeout while pulling image",
	}

	mockStore.On("GetWorkload", "failed-workload").Return(workload, nil).Twice()
	mockStore.On("GetStatus", "failed-workload").Return(status, nil).Twice()
	mockRuntime.On("Status", mock.Anything, "failed-workload").Return(
		models.ActualStateFailed, "network timeout while pulling image", nil,
	).Twice()

	mockStore.On("SaveStatus", mock.MatchedBy(func(s *models.WorkloadStatus) bool {
		if s.Metadata == nil {
			return false
		}
		return s.Metadata["retry_attempts"] == "1"
	})).Return(nil).Twice()

	err := manager.ReconcileWorkload(context.Background(), "failed-workload")
	assert.NoError(t, err)

	err = manager.ReconcileWorkload(context.Background(), "failed-workload")
	assert.NoError(t, err)

	mockRuntime.AssertNotCalled(t, "Delete", mock.Anything, "failed-workload")
	mockRuntime.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	mockRuntime.AssertNotCalled(t, "Start", mock.Anything, "failed-workload")
	mockStore.AssertExpectations(t)
}

func TestReconcileWorkload_RecreatesMissingRuntimeWorkload(t *testing.T) {
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager := NewManager(mockStore, runtimeMgr, logger)
	manager.SetRetryPolicy(&retry.RetryPolicy{
		MaxAttempts:        3,
		InitialDelay:       0,
		MaxDelay:           0,
		BackoffMultiplier:  1,
		OnlyRetryTransient: false,
	})

	workload := &models.Workload{
		ID:           "missing-workload",
		Type:         models.WorkloadTypeContainer,
		DesiredState: models.DesiredStateRunning,
	}

	status := &models.WorkloadStatus{
		ID:           "missing-workload",
		Type:         models.WorkloadTypeContainer,
		DesiredState: models.DesiredStateRunning,
		ActualState:  models.ActualStateUnknown,
		Message:      "container not found",
	}

	mockStore.On("GetWorkload", "missing-workload").Return(workload, nil)
	mockStore.On("GetStatus", "missing-workload").Return(status, nil)
	mockRuntime.On("Status", mock.Anything, "missing-workload").Return(
		models.ActualStateUnknown, "container not found", nil,
	).Once()
	mockRuntime.On("Delete", mock.Anything, "missing-workload").Return(nil).Once()
	mockRuntime.On("Create", mock.Anything, workload).Return(nil).Once()
	mockRuntime.On("Start", mock.Anything, "missing-workload").Return(nil).Once()
	mockRuntime.On("Status", mock.Anything, "missing-workload").Return(
		models.ActualStateRunning, "running", nil,
	).Once()
	mockStore.On("SaveStatus", mock.MatchedBy(func(s *models.WorkloadStatus) bool {
		return s.ActualState == models.ActualStateRunning
	})).Return(nil).Once()

	err := manager.ReconcileWorkload(context.Background(), "missing-workload")
	assert.NoError(t, err)
	mockStore.AssertExpectations(t)
	mockRuntime.AssertExpectations(t)
}

func TestReconcileWorkload_MissingRuntime_ResourceUnavailable_DoesNotRecreate(t *testing.T) {
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)

	runtimeMgr := runtime.NewManager()
	mockRuntime.On("Type").Return(models.WorkloadTypeContainer)
	runtimeMgr.Register(mockRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager := NewManager(mockStore, runtimeMgr, logger)
	manager.SetResourceMonitor(resources.NewMonitor(&resources.Thresholds{
		MemoryThreshold: -1,
		CPUThreshold:    -1,
		DiskThreshold:   -1,
	}, logger))

	workload := &models.Workload{
		ID:           "missing-workload",
		Type:         models.WorkloadTypeContainer,
		DesiredState: models.DesiredStateRunning,
	}

	status := &models.WorkloadStatus{
		ID:           "missing-workload",
		Type:         models.WorkloadTypeContainer,
		DesiredState: models.DesiredStateRunning,
		ActualState:  models.ActualStateUnknown,
		Message:      "container not found",
	}

	mockStore.On("GetWorkload", "missing-workload").Return(workload, nil)
	mockStore.On("GetStatus", "missing-workload").Return(status, nil)
	mockRuntime.On("Status", mock.Anything, "missing-workload").Return(
		models.ActualStateUnknown, "container not found", nil,
	).Once()
	mockStore.On("SaveStatus", mock.MatchedBy(func(s *models.WorkloadStatus) bool {
		return s.ID == "missing-workload" &&
			s.ActualState == models.ActualStateFailed &&
			s.Metadata != nil &&
			s.Metadata["failure_reason"] == string(retry.FailureReasonResourceQuotaExceeded)
	})).Return(nil).Once()

	err := manager.ReconcileWorkload(context.Background(), "missing-workload")
	assert.NoError(t, err)

	mockRuntime.AssertNotCalled(t, "Delete", mock.Anything, "missing-workload")
	mockRuntime.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	mockRuntime.AssertNotCalled(t, "Start", mock.Anything, "missing-workload")
	mockStore.AssertExpectations(t)
	mockRuntime.AssertExpectations(t)
}

func TestGetStatus_SurfacesRuntimeMetadata(t *testing.T) {
	mockStore := new(MockStore)
	baseRuntime := new(MockRuntime)
	wrappedRuntime := &runtimeWithMetadata{
		MockRuntime: baseRuntime,
		metadata: map[string]string{
			"vm.primary_ip":   "10.0.0.5",
			"vm.ip_addresses": "10.0.0.5,fd00::5",
		},
	}

	runtimeMgr := runtime.NewManager()
	baseRuntime.On("Type").Return(models.WorkloadTypeVM)
	runtimeMgr.Register(wrappedRuntime)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	manager := NewManager(mockStore, runtimeMgr, logger)

	workload := &models.Workload{ID: "vm-1", Type: models.WorkloadTypeVM}
	status := &models.WorkloadStatus{ID: "vm-1", Type: models.WorkloadTypeVM, Metadata: map[string]string{}}

	mockStore.On("GetStatus", "vm-1").Return(status, nil)
	mockStore.On("GetWorkload", "vm-1").Return(workload, nil)
	baseRuntime.On("Status", mock.Anything, "vm-1").Return(models.ActualStateRunning, "running", nil)
	mockStore.On("SaveStatus", mock.MatchedBy(func(s *models.WorkloadStatus) bool {
		return s.Metadata["vm.primary_ip"] == "10.0.0.5"
	})).Return(nil)

	got, err := manager.GetStatus(context.Background(), "vm-1")
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.5", got.Metadata["vm.primary_ip"])
	assert.Equal(t, "10.0.0.5,fd00::5", got.Metadata["vm.ip_addresses"])

	mockStore.AssertExpectations(t)
	baseRuntime.AssertExpectations(t)
}

func TestApplyWorkload_ConcurrentSameRevision_OneAppliesOneSkips(t *testing.T) {
	store := newSerialStore()
	rt := &conflictRuntime{}
	runtimeMgr := runtime.NewManager()
	runtimeMgr.Register(rt)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	manager := NewManager(store, runtimeMgr, logger)

	workload := &models.Workload{
		ID:           "demo-c1",
		Type:         models.WorkloadTypeContainer,
		RevisionID:   "rev-1",
		DesiredState: models.DesiredStateRunning,
		Spec:         map[string]interface{}{"image": "nginx:latest"},
	}

	// Simulate previously failed/missing runtime status, which should trigger re-apply path.
	store.workloads[workload.ID] = cloneWorkload(workload)
	store.statuses[workload.ID] = &models.WorkloadStatus{
		ID:           workload.ID,
		Type:         workload.Type,
		RevisionID:   workload.RevisionID,
		DesiredState: workload.DesiredState,
		ActualState:  models.ActualStateUnknown,
		Message:      "container not found",
	}

	type result struct {
		skipped bool
		err     error
	}

	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	run := func() {
		defer wg.Done()
		<-start
		_, skipped, err := manager.ApplyWorkload(context.Background(), workload)
		results <- result{skipped: skipped, err: err}
	}

	go run()
	go run()
	close(start)
	wg.Wait()
	close(results)

	successCount := 0
	skippedCount := 0
	for r := range results {
		assert.NoError(t, r.err)
		if r.skipped {
			skippedCount++
		} else {
			successCount++
		}
	}

	assert.Equal(t, 1, successCount)
	assert.Equal(t, 1, skippedCount)
	assert.Equal(t, int32(1), atomic.LoadInt32(&rt.createCalls))
}
