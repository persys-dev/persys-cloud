package retry

import (
	"errors"
	"testing"
)

func TestClassifyError_UsesKeywordMatching(t *testing.T) {
	reason := ClassifyError(assertErr{"network timeout connecting to host"})
	if reason != FailureReasonNetworkTimeout {
		t.Fatalf("expected %s, got %s", FailureReasonNetworkTimeout, reason)
	}
}

type assertErr struct{ msg string }

func (e assertErr) Error() string { return e.msg }

func TestContains_MatchesKeyword(t *testing.T) {
	if !contains("failed to pull image manifest", "manifest") {
		t.Fatalf("expected contains to match keyword")
	}
}

func TestContains_CaseInsensitive(t *testing.T) {
	if !contains("Connection Refused", "connection refused") {
		t.Fatalf("expected contains to be case-insensitive")
	}
}

func TestClassifyError_ImagePullTimeout(t *testing.T) {
	err := errors.New("Image pull timed out while fetching manifest")
	got := ClassifyError(err)
	if got != FailureReasonImagePullTimeout {
		t.Fatalf("expected %s, got %s", FailureReasonImagePullTimeout, got)
	}
}

func TestClassifyError_PortBindConflict(t *testing.T) {
	err := errors.New("Error starting userland proxy: listen tcp4 0.0.0.0:8080: bind: address already in use")
	got := ClassifyError(err)
	if got != FailureReasonPortConflict {
		t.Fatalf("expected %s, got %s", FailureReasonPortConflict, got)
	}
}

func TestShouldRetry_UnknownErrorIsRetryable(t *testing.T) {
	tracker := NewRetryTracker(DefaultRetryPolicy())
	result, err := tracker.RecordFailure(FailureReasonUnknown, "unclassified runtime error")
	if err != nil {
		t.Fatalf("expected unknown error to be retryable, got error: %v", err)
	}
	if result == nil || !result.Retryable {
		t.Fatalf("expected unknown error to be retryable, got %#v", result)
	}
}

func TestShouldRetry_RuntimeErrorIsRetryable(t *testing.T) {
	tracker := NewRetryTracker(DefaultRetryPolicy())
	result, err := tracker.RecordFailure(FailureReasonRuntimeError, "container exited unexpectedly")
	if err != nil {
		t.Fatalf("expected runtime error to be retryable, got error: %v", err)
	}
	if result == nil || !result.Retryable {
		t.Fatalf("expected runtime error to be retryable, got %#v", result)
	}
}
