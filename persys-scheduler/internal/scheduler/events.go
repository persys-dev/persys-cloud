package scheduler

import (
	"context"
	"time"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
)

var eventsLogger = logging.C("scheduler.events")

var knownSchedulerEventTypes = map[string]struct{}{
	// Topology
	"NodeJoined": {},
	"NodeLost":   {},
	"NodeLeft":   {},
	// Workload lifecycle
	"WorkloadScheduled": {},
	"WorkloadFailed":    {},
	"DriftDetected":     {},
	"RetryTriggered":    {},
	"Rescheduled":       {},
	"Relocated": {},
	"RescheduleSkipped": {},
	// Control-plane mode (etcd health / recovery)
	"SchedulerModeChanged": {},
	// Leader election
	"LeaderElected": {},
	"LeaderLost":    {},
	// Operator node control
	"NodeDraining":     {},
	"NodeReady":        {},
	"NodeTainted":      {},
	"NodeUntainted":    {},
	"NodeLabelSet":     {},
	"NodeLabelDeleted": {},
}

func isKnownSchedulerEventType(eventType string) bool {
	_, ok := knownSchedulerEventTypes[eventType]
	return ok
}

// eventWatchResyncBackoff mirrors nodeWatchResyncBackoff (node_watch.go) —
// how long WatchEvents waits before retrying after its Redis stream watch
// ends for any reason (connection loss, Redis restart, etc).
const eventWatchResyncBackoff = 2 * time.Second

// WatchEvents streams cluster-wide scheduler events to onEvent as they're
// emitted (see Scheduler.emitEvent), starting from the current set of
// recent events (up to limit) and then following new ones live — with no
// gap and no duplicate delivery across that replay-to-live boundary,
// since readEventsFromStream returns the exact stream ID to resume from.
// Blocks until ctx is cancelled, onEvent returns an error (typically
// because the receiving gRPC stream's client disconnected), or an
// unrecoverable Redis error occurs.
//
// Because emitEvent writes to the single Redis Stream shared by every
// scheduler replica (see redis_store.go), watching via any one replica's
// Redis client sees every event emitted cluster-wide — this is what makes
// the events genuinely cluster-wide rather than per-replica, the same
// property the previous etcd-watch-based version had, without the etcd
// load.
//
// If Redis is unavailable when this is called, replay returns no history
// (readEventsFromStream degrades gracefully) and the live-watch loop
// below retries on eventWatchResyncBackoff until Redis comes back — a
// client connected during a Redis outage just sees the stream resume once
// it recovers, rather than an error.
func (s *Scheduler) WatchEvents(ctx context.Context, limit int64, onEvent func(models.SchedulerEvent) error) error {
	recent, newestID := s.readEventsFromStream(limit)
	// recent is newest-first; deliver oldest-first so a client sees a
	// sensible chronological replay before switching to live events.
	for i := len(recent) - 1; i >= 0; i-- {
		if err := onEvent(recent[i]); err != nil {
			return err
		}
	}
	lastID := newestID
	if lastID == "" {
		lastID = "$" // nothing replayed; start from only new events
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		nextID, err := s.watchEventStream(ctx, lastID, onEvent)
		lastID = nextID
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			eventsLogger.WithError(err).Debug("event stream watch ended, resyncing")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(eventWatchResyncBackoff):
		}
	}
}
