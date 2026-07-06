package workload

import (
	"testing"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/retry"
	"github.com/persys-dev/persys-cloud/compute-agent/pkg/models"
)

func TestMarkRetryTerminalSetsMetadata(t *testing.T) {
	status := &models.WorkloadStatus{}
	tracker := retry.NewRetryTracker(retry.DefaultRetryPolicy())
	_, _ = tracker.RecordFailure(retry.FailureReasonRuntimeError, "runtime failed")

	markRetryTerminal(status, tracker, "max retries reached")

	if !isRetryTerminal(status) {
		t.Fatalf("expected status to be marked retry terminal")
	}
	if got := status.Metadata[retryTerminalReasonMetadataKey]; got != "max retries reached" {
		t.Fatalf("unexpected terminal reason: %q", got)
	}
	if got := status.Metadata["retry_attempts"]; got == "" {
		t.Fatalf("expected retry_attempts metadata to be set")
	}
}
