package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/task"
	pb "github.com/persys-dev/persys-cloud/compute-agent/pkg/api/v1"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
	"github.com/sirupsen/logrus"
)

func TestStatusToProto_MetadataPreserved(t *testing.T) {
	s := &Server{}
	status := &models.WorkloadStatus{
		ID:           "w1",
		Type:         models.WorkloadTypeVM,
		RevisionID:   "r1",
		DesiredState: models.DesiredStateRunning,
		ActualState:  models.ActualStateRunning,
		Message:      "ok",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Metadata: map[string]string{
			"vm.primary_ip": "10.0.0.4",
		},
	}
	pbStatus := s.statusToProto(status)
	if pbStatus.Metadata["vm.primary_ip"] != "10.0.0.4" {
		t.Fatal("expected metadata to be preserved")
	}
}

func TestListActions_FiltersByWorkloadID(t *testing.T) {
	q := task.NewQueue(2, logrus.New())
	if err := q.Submit(&task.Task{
		ID:         "apply-w1-1",
		WorkloadID: "w1",
		Type:       task.TaskTypeApplyWorkload,
	}); err != nil {
		t.Fatalf("failed to submit task 1: %v", err)
	}
	if err := q.Submit(&task.Task{
		ID:         "delete-w2-1",
		WorkloadID: "w2",
		Type:       task.TaskTypeDeleteWorkload,
	}); err != nil {
		t.Fatalf("failed to submit task 2: %v", err)
	}

	s := &Server{taskQueue: q}
	resp, err := s.ListActions(context.Background(), &pb.ListActionsRequest{
		WorkloadId: "w1",
	})
	if err != nil {
		t.Fatalf("ListActions failed: %v", err)
	}
	if len(resp.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(resp.Actions))
	}
	if resp.Actions[0].TaskId != "apply-w1-1" {
		t.Fatalf("unexpected task id: %s", resp.Actions[0].TaskId)
	}
	if resp.Actions[0].WorkloadId != "w1" {
		t.Fatalf("unexpected workload id: %s", resp.Actions[0].WorkloadId)
	}
	if resp.Actions[0].ActionType != string(task.TaskTypeApplyWorkload) {
		t.Fatalf("unexpected action type: %s", resp.Actions[0].ActionType)
	}
}
