// Package gitops watches local and remote sources and triggers SDK applies.
package gitops

import "time"

// WatchOptions configures a GitOps watch loop.
type WatchOptions struct {
	Path, RepoURL, Ref string
	Interval           time.Duration
}

// Event describes a detected change.
type Event struct {
	Path string
	Time time.Time
}
