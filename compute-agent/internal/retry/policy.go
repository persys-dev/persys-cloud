package retry

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// FailureReason represents why a workload failed
type FailureReason string

const (
	// Creation/startup failures
	FailureReasonImagePullTimeout FailureReason = "IMAGE_PULL_TIMEOUT"
	FailureReasonImagePullAuth    FailureReason = "IMAGE_PULL_AUTH_FAILED"
	FailureReasonInvalidImage     FailureReason = "INVALID_IMAGE"

	// Network failures
	FailureReasonNetworkTimeout     FailureReason = "NETWORK_TIMEOUT"
	FailureReasonNetworkUnreachable FailureReason = "NETWORK_UNREACHABLE"
	FailureReasonDNSResolution      FailureReason = "DNS_RESOLUTION_FAILED"

	// Resource failures
	FailureReasonInsufficientResources FailureReason = "INSUFFICIENT_RESOURCES"
	FailureReasonStorageFull           FailureReason = "STORAGE_FULL"
	FailureReasonResourceQuotaExceeded FailureReason = "RESOURCE_QUOTA_EXCEEDED"

	// Specification failures
	FailureReasonInvalidSpec   FailureReason = "INVALID_SPECIFICATION"
	FailureReasonInvalidConfig FailureReason = "INVALID_CONFIGURATION"
	FailureReasonPortConflict  FailureReason = "PORT_BIND_CONFLICT"

	// Runtime failures
	FailureReasonRuntimeError     FailureReason = "RUNTIME_ERROR"
	FailureReasonRuntimeUnhealthy FailureReason = "RUNTIME_UNHEALTHY"

	// Unknown
	FailureReasonUnknown FailureReason = "UNKNOWN_ERROR"
)

// IsTransient returns true if the failure reason suggests a transient error that may succeed on retry
func (f FailureReason) IsTransient() bool {
	switch f {
	case FailureReasonImagePullTimeout,
		FailureReasonNetworkTimeout,
		FailureReasonNetworkUnreachable,
		FailureReasonDNSResolution,
		FailureReasonRuntimeUnhealthy,
		FailureReasonRuntimeError,
		FailureReasonUnknown:
		return true
	default:
		return false
	}
}

// IsPermanent returns true if the failure is permanent and shouldn't be retried
func (f FailureReason) IsPermanent() bool {
	switch f {
	case FailureReasonInvalidImage,
		FailureReasonInvalidSpec,
		FailureReasonInvalidConfig,
		FailureReasonPortConflict:
		return true
	default:
		return false
	}
}

// RetryPolicy defines retry behavior
type RetryPolicy struct {
	// MaxAttempts is the maximum number of retry attempts (0 = no retries)
	MaxAttempts int
	// InitialDelay is the initial retry delay
	InitialDelay time.Duration
	// MaxDelay is the maximum delay between retries
	MaxDelay time.Duration
	// BackoffMultiplier is the multiplier for exponential backoff
	BackoffMultiplier float64
	// OnlyRetryTransient determines if only transient errors are retried
	OnlyRetryTransient bool
}

// DefaultRetryPolicy returns a sensible default retry policy
func DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxAttempts:        3,
		InitialDelay:       5 * time.Second,
		MaxDelay:           2 * time.Minute,
		BackoffMultiplier:  2.0,
		OnlyRetryTransient: true,
	}
}

// FailureHistory records a single failure event
type FailureHistory struct {
	Timestamp  time.Time
	Reason     FailureReason
	ErrorMsg   string
	AttemptNum int
}

// RetryTracker tracks retry attempts and history
type RetryTracker struct {
	policy         *RetryPolicy
	failureHistory []FailureHistory
	nextRetryTime  time.Time
}

// NewRetryTracker creates a new retry tracker
func NewRetryTracker(policy *RetryPolicy) *RetryTracker {
	if policy == nil {
		policy = DefaultRetryPolicy()
	}
	return &RetryTracker{
		policy:         policy,
		failureHistory: make([]FailureHistory, 0),
	}
}

// RecordFailure records a failure attempt and calculates next retry time
func (rt *RetryTracker) RecordFailure(reason FailureReason, errorMsg string) (*RetryResult, error) {
	attemptNum := len(rt.failureHistory) + 1

	history := FailureHistory{
		Timestamp:  time.Now(),
		Reason:     reason,
		ErrorMsg:   errorMsg,
		AttemptNum: attemptNum,
	}

	rt.failureHistory = append(rt.failureHistory, history)

	// Check if we should retry
	shouldRetry, err := rt.ShouldRetry(reason, attemptNum)
	if err != nil {
		return nil, err
	}

	result := &RetryResult{
		Retryable:      shouldRetry,
		FailureReason:  reason,
		Attempt:        attemptNum,
		TotalAttempts:  rt.policy.MaxAttempts,
		FailureHistory: rt.failureHistory,
	}

	if shouldRetry {
		delay := rt.calculateNextDelay(attemptNum)
		rt.nextRetryTime = time.Now().Add(delay)
		result.NextRetryTime = rt.nextRetryTime
		result.RetryAfter = delay
	}

	return result, nil
}

// ShouldRetry determines if we should retry based on policy and reason
func (rt *RetryTracker) ShouldRetry(reason FailureReason, attemptNum int) (bool, error) {
	// Check if max attempts exceeded
	if attemptNum > rt.policy.MaxAttempts {
		return false, fmt.Errorf("max retry attempts exceeded: %d", rt.policy.MaxAttempts)
	}

	// Check if it's a permanent failure
	if reason.IsPermanent() {
		return false, fmt.Errorf("permanent failure reason: %s", reason)
	}

	// If OnlyRetryTransient is true, check if reason is transient
	if rt.policy.OnlyRetryTransient && !reason.IsTransient() {
		return false, nil
	}

	return true, nil
}

// calculateNextDelay calculates the delay for the next retry using exponential backoff
func (rt *RetryTracker) calculateNextDelay(attemptNum int) time.Duration {
	// Calculate exponential backoff: initialDelay * (multiplier ^ (attempt - 1))
	delaySeconds := float64(rt.policy.InitialDelay.Seconds()) *
		math.Pow(rt.policy.BackoffMultiplier, float64(attemptNum-1))

	delay := time.Duration(int64(delaySeconds)) * time.Second

	// Cap at max delay
	if delay > rt.policy.MaxDelay {
		delay = rt.policy.MaxDelay
	}

	return delay
}

// CanRetryNow returns true if enough time has passed to retry
func (rt *RetryTracker) CanRetryNow() bool {
	return time.Now().After(rt.nextRetryTime)
}

// GetNextRetryTime returns when the next retry should occur
func (rt *RetryTracker) GetNextRetryTime() time.Time {
	return rt.nextRetryTime
}

// GetFailureHistory returns the full failure history
func (rt *RetryTracker) GetFailureHistory() []FailureHistory {
	return rt.failureHistory
}

// GetAttemptCount returns the number of attempts made so far
func (rt *RetryTracker) GetAttemptCount() int {
	return len(rt.failureHistory)
}

// Reset clears the retry state
func (rt *RetryTracker) Reset() {
	rt.failureHistory = make([]FailureHistory, 0)
	rt.nextRetryTime = time.Time{}
}

// RetryResult contains information about a retry decision
type RetryResult struct {
	Retryable      bool
	FailureReason  FailureReason
	Attempt        int
	TotalAttempts  int
	NextRetryTime  time.Time
	RetryAfter     time.Duration
	FailureHistory []FailureHistory
}

// RetryableError represents an error that may be retried
type RetryableError struct {
	Reason  FailureReason
	Message string
	Cause   error
}

// Error implements the error interface
func (e *RetryableError) Error() string {
	return fmt.Sprintf("[%s] %s: %v", e.Reason, e.Message, e.Cause)
}

// NewRetryableError creates a new retryable error
func NewRetryableError(reason FailureReason, message string, cause error) *RetryableError {
	return &RetryableError{
		Reason:  reason,
		Message: message,
		Cause:   cause,
	}
}

// ClassifyError attempts to classify an error into a FailureReason
func ClassifyError(err error) FailureReason {
	if err == nil {
		return FailureReasonUnknown
	}

	errMsg := err.Error()

	// Classify based on error message patterns
	if isImagePullError(errMsg) {
		if isTimeoutError(errMsg) {
			return FailureReasonImagePullTimeout
		}
		if isAuthError(errMsg) {
			return FailureReasonImagePullAuth
		}
		return FailureReasonInvalidImage
	}

	if isPortBindConflict(errMsg) {
		return FailureReasonPortConflict
	}

	if isNetworkError(errMsg) {
		if isTimeoutError(errMsg) {
			return FailureReasonNetworkTimeout
		}
		return FailureReasonNetworkUnreachable
	}

	if isDNSError(errMsg) {
		return FailureReasonDNSResolution
	}

	if isResourceError(errMsg) {
		return FailureReasonInsufficientResources
	}

	if isSpecError(errMsg) {
		return FailureReasonInvalidSpec
	}

	return FailureReasonUnknown
}

// Helper functions for error classification
func isImagePullError(msg string) bool {
	return contains(msg, "image", "pull", "repository", "manifest", "not found")
}

func isTimeoutError(msg string) bool {
	return contains(msg, "timeout", "timed out", "deadline exceeded")
}

func isAuthError(msg string) bool {
	return contains(msg, "unauthorized", "forbidden", "authentication", "credentials")
}

func isNetworkError(msg string) bool {
	return contains(msg, "connection refused", "connection reset", "network", "host")
}

func isDNSError(msg string) bool {
	return contains(msg, "dns", "name resolution", "no such host")
}

func isResourceError(msg string) bool {
	return contains(msg, "memory", "disk", "cpu", "quota", "limit")
}

func isSpecError(msg string) bool {
	return contains(msg, "invalid", "malformed", "schema", "validation")
}

func isPortBindConflict(msg string) bool {
	return contains(msg, "bind", "address already in use") ||
		contains(msg, "port", "already allocated") ||
		contains(msg, "error starting userland proxy")
}

func contains(str string, keywords ...string) bool {
	str = strings.ToLower(str)
	for _, keyword := range keywords {
		if strings.Contains(str, strings.ToLower(keyword)) {
			str = strings.ToLower(fmt.Sprintf(" %s ", str)) // Add padding for word boundaries
			for _, keyword := range keywords {
				if containsHelper(str, strings.ToLower(keyword)) {
					return true
				}
			}
		}
	}
	return false
}

func containsHelper(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
