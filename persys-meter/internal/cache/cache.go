// Package cache holds the most recent usage sample per workload in memory.
//
// This exists alongside ClickHouse, not instead of it: ClickHouse is the
// durable, queryable history; this cache is a fast, always-current view of
// "what is every workload doing right now" - used to back the per-workload
// Prometheus gauges and the low-latency parts of the query API without
// hitting ClickHouse on every scrape/request.
package cache

import (
	"sync"
	"time"

	"github.com/persys-dev/persys-cloud/persys-meter/internal/ingest"
)

// Entry is one workload's latest known usage sample, plus when this cache
// last heard about it (which may lag the sample's own ReportedAt slightly,
// but is what staleness/eviction decisions are based on).
type Entry struct {
	Record    ingest.UsageRecord
	UpdatedAt time.Time
}

// LatestCache is a concurrency-safe map of workload ID -> latest Entry.
type LatestCache struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

// New creates an empty LatestCache.
func New() *LatestCache {
	return &LatestCache{entries: make(map[string]Entry)}
}

// Set records r as the latest known sample for its workload, overwriting
// any previous entry. Safe to call for a duplicate/redelivered event - it's
// just a redundant write of the same data.
func (c *LatestCache) Set(r ingest.UsageRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[r.WorkloadID] = Entry{Record: r, UpdatedAt: time.Now()}
}

// Get returns the latest entry for a workload, if known.
func (c *LatestCache) Get(workloadID string) (Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[workloadID]
	return e, ok
}

// All returns every entry last updated within maxAge. Pass 0 to disable
// staleness filtering and return everything regardless of age.
//
// Filtering matters here because nothing currently tells this cache when a
// workload is deleted - without a staleness cutoff, a Prometheus scrape (or
// a `/v1/workloads` API call) would keep showing a workload's last-ever
// values forever, long after it stopped existing.
func (c *LatestCache) All(maxAge time.Duration) []Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]Entry, 0, len(c.entries))
	now := time.Now()
	for _, e := range c.entries {
		if maxAge > 0 && now.Sub(e.UpdatedAt) > maxAge {
			continue
		}
		out = append(out, e)
	}
	return out
}

// ByNode returns every non-stale entry (per the same maxAge rule as All)
// whose NodeID matches nodeID.
func (c *LatestCache) ByNode(nodeID string, maxAge time.Duration) []Entry {
	all := c.All(maxAge)
	out := all[:0] // safe in-place filter: write index never exceeds read index
	for _, e := range all {
		if e.Record.NodeID == nodeID {
			out = append(out, e)
		}
	}
	return out
}

// Prune deletes entries not updated within maxAge and returns how many were
// removed. Call this periodically (see cmd/meter/main.go) to bound memory
// use for workloads that have been deleted and will never report again -
// All()'s filtering alone only hides stale entries from callers, it doesn't
// reclaim their memory.
func (c *LatestCache) Prune(maxAge time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	removed := 0
	for id, e := range c.entries {
		if now.Sub(e.UpdatedAt) > maxAge {
			delete(c.entries, id)
			removed++
		}
	}
	return removed
}

// Len reports the current number of entries, stale or not - useful as a
// cheap gauge of cache size for diagnostics.
func (c *LatestCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
