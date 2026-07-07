package task

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestQueue_SubmitAndCompleteTask(t *testing.T) {
	q := NewQueue(1, logrus.New())
	q.RegisterHandler(TaskTypeApplyWorkload, func(ctx context.Context, task *Task) error { return nil })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Start(ctx)
	defer q.Stop()

	task := &Task{ID: "t1", Type: TaskTypeApplyWorkload}
	if err := q.Submit(task); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	result, err := q.WaitForTask(waitCtx, "t1")
	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	if result.Status != TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.Status)
	}
}

func TestQueue_Submit_FullQueueDoesNotLeaveGhostTask(t *testing.T) {
	q := NewQueue(1, logrus.New())

	if err := q.Submit(&Task{ID: "t1", Type: TaskTypeApplyWorkload}); err != nil {
		t.Fatalf("submit t1 failed: %v", err)
	}
	if err := q.Submit(&Task{ID: "t2", Type: TaskTypeApplyWorkload}); err != nil {
		t.Fatalf("submit t2 failed: %v", err)
	}

	err := q.Submit(&Task{ID: "t3", Type: TaskTypeApplyWorkload})
	if err == nil {
		t.Fatal("expected submit to fail when queue is full")
	}

	if got := q.GetTask("t3"); got != nil {
		t.Fatal("expected failed submission task to be removed from task map")
	}
}
