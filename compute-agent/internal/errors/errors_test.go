package errors

import (
	stdErrors "errors"
	"testing"
)

func TestWorkloadError_UserFriendlyMessage(t *testing.T) {
	err := NewWorkloadError(ErrCodeCreateFailed, "create failed", "w1", "container", stdErrors.New("boom"))
	msg := err.UserFriendlyMessage()
	if msg == "" {
		t.Fatal("expected non-empty user message")
	}
}

func TestWorkloadError_WithDetail(t *testing.T) {
	err := NewWorkloadError(ErrCodeStartFailed, "x", "w1", "container", nil).WithDetail("k", "v")
	if err.Details["k"] != "v" {
		t.Fatal("expected detail to be set")
	}
}
