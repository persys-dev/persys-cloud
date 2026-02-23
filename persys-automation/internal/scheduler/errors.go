package scheduler

import "errors"

var (
	ErrSchedulerAddressRequired = errors.New("scheduler address is required")
	ErrAppendCAFailed           = errors.New("append scheduler CA failed")
)
