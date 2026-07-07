package errors

import (
	"fmt"
)

// ErrorCode represents standardized error codes for different failure scenarios
type ErrorCode string

const (
	// Creation errors
	ErrCodeImagePull    ErrorCode = "IMAGE_PULL_FAILED"
	ErrCodeCreateFailed ErrorCode = "CREATE_FAILED"
	ErrCodeStartFailed  ErrorCode = "START_FAILED"
	ErrCodeDeleteFailed ErrorCode = "DELETE_FAILED"
	ErrCodeStatusFailed ErrorCode = "STATUS_CHECK_FAILED"

	// Resource errors
	ErrCodeInsufficientMemory    ErrorCode = "INSUFFICIENT_MEMORY"
	ErrCodeInsufficientCPU       ErrorCode = "INSUFFICIENT_CPU"
	ErrCodeInsufficientDisk      ErrorCode = "INSUFFICIENT_DISK"
	ErrCodeResourceQuotaExceeded ErrorCode = "RESOURCE_QUOTA_EXCEEDED"

	// Network errors
	ErrCodeNetworkTimeout ErrorCode = "NETWORK_TIMEOUT"
	ErrCodeNetworkFailed  ErrorCode = "NETWORK_FAILED"
	ErrCodeDNSFailed      ErrorCode = "DNS_RESOLUTION_FAILED"

	// Storage errors
	ErrCodeVolumeNotFound    ErrorCode = "VOLUME_NOT_FOUND"
	ErrCodeVolumeMountFailed ErrorCode = "VOLUME_MOUNT_FAILED"
	ErrCodeStorageQuotaFull  ErrorCode = "STORAGE_QUOTA_FULL"

	// Runtime errors
	ErrCodeRuntimeNotAvailable ErrorCode = "RUNTIME_NOT_AVAILABLE"
	ErrCodeRuntimeUnhealthy    ErrorCode = "RUNTIME_UNHEALTHY"
	ErrCodeContainerNotFound   ErrorCode = "CONTAINER_NOT_FOUND"
	ErrCodeDomainNotFound      ErrorCode = "VM_DOMAIN_NOT_FOUND"

	// Validation errors
	ErrCodeInvalidSpec   ErrorCode = "INVALID_SPEC"
	ErrCodeInvalidImage  ErrorCode = "INVALID_IMAGE"
	ErrCodeInvalidConfig ErrorCode = "INVALID_CONFIGURATION"

	// System errors
	ErrCodeSystemError ErrorCode = "SYSTEM_ERROR"
	ErrCodeUnknown     ErrorCode = "UNKNOWN_ERROR"
)

// WorkloadError represents a detailed workload operation error
type WorkloadError struct {
	Code         ErrorCode
	Message      string
	Cause        error
	WorkloadID   string
	WorkloadType string
	Details      map[string]interface{}
}

// Error implements the error interface
func (e *WorkloadError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s (workload: %s): %v", e.Code, e.Message, e.WorkloadID, e.Cause)
	}
	return fmt.Sprintf("[%s] %s (workload: %s)", e.Code, e.Message, e.WorkloadID)
}

// Unwrap returns the underlying cause error
func (e *WorkloadError) Unwrap() error {
	return e.Cause
}

// UserFriendlyMessage returns a message suitable for client display
func (e *WorkloadError) UserFriendlyMessage() string {
	msg := fmt.Sprintf("Failed to %s workload '%s'", getActionFromCode(e.Code), e.WorkloadID)

	if e.Details != nil {
		if reason, ok := e.Details["reason"]; ok {
			msg = fmt.Sprintf("%s: %s", msg, reason)
		}
	}

	if e.Cause != nil {
		msg = fmt.Sprintf("%s (%v)", msg, e.Cause)
	}

	return msg
}

// NewWorkloadError creates a new workload error
func NewWorkloadError(code ErrorCode, message, workloadID, workloadType string, cause error) *WorkloadError {
	return &WorkloadError{
		Code:         code,
		Message:      message,
		Cause:        cause,
		WorkloadID:   workloadID,
		WorkloadType: workloadType,
		Details:      make(map[string]interface{}),
	}
}

// WithDetail adds a detail to the error
func (e *WorkloadError) WithDetail(key string, value interface{}) *WorkloadError {
	e.Details[key] = value
	return e
}

// getActionFromCode returns a human-readable action name for an error code
func getActionFromCode(code ErrorCode) string {
	switch code {
	case ErrCodeImagePull:
		return "pull image for"
	case ErrCodeCreateFailed:
		return "create"
	case ErrCodeStartFailed:
		return "start"
	case ErrCodeDeleteFailed:
		return "delete"
	case ErrCodeStatusFailed:
		return "check status of"
	default:
		return "process"
	}
}
