package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// TaskType defines the type of task
type TaskType string

const (
	TaskTypeApplyWorkload  TaskType = "apply_workload"
	TaskTypeDeleteWorkload TaskType = "delete_workload"
	TaskTypeReconcile      TaskType = "reconcile"
)

// TaskStatus represents the current status of a task
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// Task represents an async operation
type Task struct {
	ID         string
	WorkloadID string
	Type       TaskType
	Status     TaskStatus
	Error      string
	Result     interface{}
	CreatedAt  time.Time
	StartedAt  time.Time
	EndedAt    time.Time
	mu         sync.RWMutex
}

// TaskSnapshot is an immutable view of a task for external consumers.
type TaskSnapshot struct {
	ID         string
	WorkloadID string
	Type       TaskType
	Status     TaskStatus
	Error      string
	CreatedAt  time.Time
	StartedAt  time.Time
	EndedAt    time.Time
}

// Handler is a function that executes a task
type Handler func(ctx context.Context, task *Task) error

// MetricsObserver captures queue/task telemetry without coupling this package
// to a specific metrics backend.
type MetricsObserver interface {
	SetTaskQueueDepth(depth int)
	ObserveTaskExecution(taskType string, duration time.Duration, failed bool)
}

// Queue manages async task execution
type Queue struct {
	handlers   map[TaskType]Handler
	tasks      map[string]*Task
	taskCh     chan *Task
	stopCh     chan struct{}
	logger     *logrus.Entry
	mu         sync.RWMutex
	maxWorkers int
	stopOnce   sync.Once
	observer   MetricsObserver
}

// NewQueue creates a new task queue
func NewQueue(maxWorkers int, logger *logrus.Logger) *Queue {
	if maxWorkers <= 0 {
		maxWorkers = 10
	}
	return &Queue{
		handlers:   make(map[TaskType]Handler),
		tasks:      make(map[string]*Task),
		taskCh:     make(chan *Task, maxWorkers*2),
		stopCh:     make(chan struct{}),
		logger:     logger.WithField("component", "task-queue"),
		maxWorkers: maxWorkers,
	}
}

// RegisterHandler registers a handler for a task type
func (q *Queue) RegisterHandler(taskType TaskType, handler Handler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[taskType] = handler
}

// SetMetricsObserver attaches optional queue/task metrics emission.
func (q *Queue) SetMetricsObserver(observer MetricsObserver) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.observer = observer
}

// Start begins processing tasks
func (q *Queue) Start(ctx context.Context) {
	q.logger.Infof("Starting task queue with %d workers", q.maxWorkers)
	q.observeQueueDepth()

	// Start worker goroutines
	for i := 0; i < q.maxWorkers; i++ {
		go q.worker(ctx, i)
	}

	// Monitor for shutdown signal
	select {
	case <-q.stopCh:
		q.logger.Info("Stopping task queue")
		return
	case <-ctx.Done():
		q.logger.Info("Context cancelled, stopping task queue")
		return
	}
}

// Stop stops the task queue
func (q *Queue) Stop() {
	q.stopOnce.Do(func() {
		close(q.stopCh)
	})
}

// worker processes tasks from the queue
func (q *Queue) worker(ctx context.Context, id int) {
	for {
		select {
		case task := <-q.taskCh:
			if task == nil {
				return
			}
			q.observeQueueDepth()
			q.executeTask(ctx, task)

		case <-q.stopCh:
			q.logger.Debugf("Worker %d stopping", id)
			return
		case <-ctx.Done():
			q.logger.Debugf("Worker %d context cancelled", id)
			return
		}
	}
}

// executeTask runs a task with its registered handler
func (q *Queue) executeTask(ctx context.Context, task *Task) {
	start := time.Now()
	task.mu.Lock()
	task.Status = TaskStatusRunning
	task.StartedAt = time.Now()
	task.mu.Unlock()

	// Get handler
	q.mu.RLock()
	handler, ok := q.handlers[task.Type]
	q.mu.RUnlock()

	if !ok {
		task.mu.Lock()
		task.Status = TaskStatusFailed
		task.Error = fmt.Sprintf("no handler registered for task type: %s", task.Type)
		task.EndedAt = time.Now()
		task.mu.Unlock()
		q.logger.Errorf("Task %s failed: %s", task.ID, task.Error)
		q.observeTaskExecution(task.Type, time.Since(start), true)
		return
	}

	// Execute handler
	err := handler(ctx, task)

	task.mu.Lock()
	task.EndedAt = time.Now()
	if err != nil {
		task.Status = TaskStatusFailed
		task.Error = err.Error()
		q.logger.Errorf("Task %s failed: %v", task.ID, err)
	} else {
		task.Status = TaskStatusCompleted
		q.logger.Infof("Task %s completed in %s", task.ID, time.Since(task.StartedAt))
	}
	task.mu.Unlock()
	q.observeTaskExecution(task.Type, time.Since(start), err != nil)
}

// Submit enqueues a task for async execution
func (q *Queue) Submit(task *Task) error {
	if task.ID == "" {
		return fmt.Errorf("task ID cannot be empty")
	}

	task.mu.Lock()
	task.Status = TaskStatusPending
	task.CreatedAt = time.Now()
	task.mu.Unlock()

	q.mu.Lock()
	q.tasks[task.ID] = task
	q.mu.Unlock()

	select {
	case q.taskCh <- task:
		q.logger.Debugf("Task %s submitted (type: %s)", task.ID, task.Type)
		q.observeQueueDepth()
		return nil
	case <-q.stopCh:
		q.mu.Lock()
		delete(q.tasks, task.ID)
		q.mu.Unlock()
		return fmt.Errorf("task queue is stopping")
	default:
		q.mu.Lock()
		delete(q.tasks, task.ID)
		q.mu.Unlock()
		return fmt.Errorf("task queue is full, try again later")
	}
}

func (q *Queue) observeQueueDepth() {
	q.mu.RLock()
	obs := q.observer
	q.mu.RUnlock()
	if obs != nil {
		obs.SetTaskQueueDepth(len(q.taskCh))
	}
}

func (q *Queue) observeTaskExecution(taskType TaskType, duration time.Duration, failed bool) {
	q.mu.RLock()
	obs := q.observer
	q.mu.RUnlock()
	if obs != nil {
		obs.ObserveTaskExecution(string(taskType), duration, failed)
	}
}

// GetTask retrieves a task by ID
func (q *Queue) GetTask(id string) *Task {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.tasks[id]
}

// GetTaskStatus returns the current status of a task
func (q *Queue) GetTaskStatus(id string) (TaskStatus, string, error) {
	task := q.GetTask(id)
	if task == nil {
		return "", "", fmt.Errorf("task not found: %s", id)
	}

	task.mu.RLock()
	defer task.mu.RUnlock()
	return task.Status, task.Error, nil
}

// WaitForTask blocks until a task is completed or context is cancelled
func (q *Queue) WaitForTask(ctx context.Context, id string) (*Task, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			task := q.GetTask(id)
			if task == nil {
				return nil, fmt.Errorf("task not found: %s", id)
			}

			task.mu.RLock()
			status := task.Status
			task.mu.RUnlock()

			if status == TaskStatusCompleted || status == TaskStatusFailed {
				return task, nil
			}

		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// ListTasks returns all tasks matching a status (or all if status is empty)
func (q *Queue) ListTasks(status TaskStatus) []*Task {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var result []*Task
	for _, task := range q.tasks {
		if status == "" {
			result = append(result, task)
			continue
		}

		task.mu.RLock()
		if task.Status == status {
			result = append(result, task)
		}
		task.mu.RUnlock()
	}

	return result
}

// ListTaskSnapshots returns immutable task snapshots matching a status (or all if status is empty).
func (q *Queue) ListTaskSnapshots(status TaskStatus) []TaskSnapshot {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var result []TaskSnapshot
	for _, task := range q.tasks {
		task.mu.RLock()
		if status == "" || task.Status == status {
			result = append(result, TaskSnapshot{
				ID:         task.ID,
				WorkloadID: task.WorkloadID,
				Type:       task.Type,
				Status:     task.Status,
				Error:      task.Error,
				CreatedAt:  task.CreatedAt,
				StartedAt:  task.StartedAt,
				EndedAt:    task.EndedAt,
			})
		}
		task.mu.RUnlock()
	}

	return result
}

// CleanupOldTasks removes completed/failed tasks older than ttl
func (q *Queue) CleanupOldTasks(ttl time.Duration) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	removed := 0

	for id, task := range q.tasks {
		task.mu.RLock()
		isCompleted := task.Status == TaskStatusCompleted || task.Status == TaskStatusFailed
		age := now.Sub(task.EndedAt)
		task.mu.RUnlock()

		if isCompleted && age > ttl {
			delete(q.tasks, id)
			removed++
		}
	}

	if removed > 0 {
		q.logger.Debugf("Cleaned up %d old tasks", removed)
	}

	return removed
}

// GetStats returns queue statistics
type QueueStats struct {
	TotalTasks     int
	PendingTasks   int
	RunningTasks   int
	CompletedTasks int
	FailedTasks    int
}

func (q *Queue) GetStats() QueueStats {
	q.mu.RLock()
	defer q.mu.RUnlock()

	stats := QueueStats{
		TotalTasks: len(q.tasks),
	}

	for _, task := range q.tasks {
		task.mu.RLock()
		switch task.Status {
		case TaskStatusPending:
			stats.PendingTasks++
		case TaskStatusRunning:
			stats.RunningTasks++
		case TaskStatusCompleted:
			stats.CompletedTasks++
		case TaskStatusFailed:
			stats.FailedTasks++
		}
		task.mu.RUnlock()
	}

	return stats
}
