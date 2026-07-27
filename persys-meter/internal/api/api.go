// Package api exposes an HTTP/JSON query surface over the usage data
// persys-meter ingests - both the live in-memory cache (for "what's this
// workload doing right now") and ClickHouse (for history and the
// aggregated summaries billing/quota enforcement is expected to build on).
//
// This is intentionally a read-only query API. It does not define or
// enforce quotas: quota *policy* (what the limits are, what happens when
// one is exceeded - deny new workloads? throttle? alert?) is a product
// decision that belongs to whichever service makes admission/enforcement
// decisions (persys-scheduler, or a dedicated billing service), not this
// one. What this API gives that caller is an accurate, queryable answer to
// "how much has this workload actually used" - the necessary input to any
// such decision, not the decision itself.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/persys-dev/persys-cloud/persys-meter/internal/cache"
	"github.com/persys-dev/persys-cloud/persys-meter/internal/store"
	"github.com/sirupsen/logrus"
)

// defaultHistoryWindow is used when a /history or /summary request omits
// ?from - a week is a reasonable default for "recent history" without
// forcing every caller to compute a timestamp for the common case.
const defaultHistoryWindow = 7 * 24 * time.Hour

// API holds the dependencies every handler needs.
type API struct {
	cache       *cache.LatestCache
	query       store.QueryStore
	logger      *logrus.Entry
	token       string        // if non-empty, required as "Bearer <token>" on every request
	cacheMaxAge time.Duration // how stale a cache entry can be and still be considered "live"
}

// New constructs an API. token may be empty to disable auth entirely (fine
// for local dev; for anything else, put this behind the same
// network/mTLS boundary the rest of Persys uses internally, or set a
// token - see README). cacheMaxAge should match whatever value is passed
// to metrics.NewWorkloadCollector, so the API and the Prometheus metrics
// agree on what counts as a "live" workload.
func New(c *cache.LatestCache, q store.QueryStore, logger *logrus.Entry, token string, cacheMaxAge time.Duration) *API {
	return &API{cache: c, query: q, logger: logger, token: token, cacheMaxAge: cacheMaxAge}
}

// Handler builds the HTTP handler for the API, including auth middleware.
// Uses Go 1.22's method+pattern ServeMux (e.g. "GET /v1/workloads/{id}")
// rather than pulling in a router dependency for a handful of routes.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/workloads", a.listWorkloads)
	mux.HandleFunc("GET /v1/workloads/{workloadID}", a.getWorkload)
	mux.HandleFunc("GET /v1/workloads/{workloadID}/history", a.getHistory)
	mux.HandleFunc("GET /v1/workloads/{workloadID}/summary", a.getSummary)
	mux.HandleFunc("GET /v1/nodes/{nodeID}/workloads", a.listWorkloadsByNode)

	return a.withAuth(mux)
}

// withAuth requires "Authorization: Bearer <token>" on every request when a
// token is configured. A no-op passthrough otherwise.
func (a *API) withAuth(next http.Handler) http.Handler {
	if a.token == "" {
		return next
	}
	want := "Bearer " + a.token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- handlers ---

// workloadView is the JSON shape for a single workload's live state -
// cache.Entry reshaped so the API's response format isn't just whatever
// internal struct layout the cache happens to use today.
type workloadView struct {
	WorkloadID     string    `json:"workload_id"`
	NodeID         string    `json:"node_id"`
	RevisionID     string    `json:"revision_id,omitempty"`
	WorkloadType   string    `json:"workload_type"`
	CPUPercent     float64   `json:"cpu_percent"`
	MemoryBytes    int64     `json:"memory_bytes"`
	DiskReadBytes  int64     `json:"disk_read_bytes"`
	DiskWriteBytes int64     `json:"disk_write_bytes"`
	NetRXBytes     int64     `json:"net_rx_bytes"`
	NetTXBytes     int64     `json:"net_tx_bytes"`
	ReportedAt     time.Time `json:"reported_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	SampleAgeSec   float64   `json:"sample_age_seconds"`
}

func toView(e cache.Entry) workloadView {
	return workloadView{
		WorkloadID:     e.Record.WorkloadID,
		NodeID:         e.Record.NodeID,
		RevisionID:     e.Record.RevisionID,
		WorkloadType:   e.Record.WorkloadType,
		CPUPercent:     e.Record.CPUPercent,
		MemoryBytes:    e.Record.MemoryBytes,
		DiskReadBytes:  e.Record.DiskReadBytes,
		DiskWriteBytes: e.Record.DiskWriteBytes,
		NetRXBytes:     e.Record.NetRXBytes,
		NetTXBytes:     e.Record.NetTXBytes,
		ReportedAt:     e.Record.ReportedAt,
		LastSeenAt:     e.UpdatedAt,
		SampleAgeSec:   time.Since(e.UpdatedAt).Seconds(),
	}
}

// GET /v1/workloads
// Lists every workload with a non-stale sample. Optional ?workload_type=
// filters by type (e.g. "container", "vm").
func (a *API) listWorkloads(w http.ResponseWriter, r *http.Request) {
	entries := a.cache.All(a.cacheMaxAge)
	workloadType := r.URL.Query().Get("workload_type")

	views := make([]workloadView, 0, len(entries))
	for _, e := range entries {
		if workloadType != "" && e.Record.WorkloadType != workloadType {
			continue
		}
		views = append(views, toView(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"workloads": views, "count": len(views)})
}

// GET /v1/nodes/{nodeID}/workloads
func (a *API) listWorkloadsByNode(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("nodeID")
	entries := a.cache.ByNode(nodeID, a.cacheMaxAge)

	views := make([]workloadView, 0, len(entries))
	for _, e := range entries {
		views = append(views, toView(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"node_id": nodeID, "workloads": views, "count": len(views)})
}

// GET /v1/workloads/{workloadID}
// Live latest sample for one workload. 404 if we have no (non-stale)
// sample for it - which is also what you'd see for a workload ID that
// never existed, since this cache has no concept of "known but idle".
func (a *API) getWorkload(w http.ResponseWriter, r *http.Request) {
	workloadID := r.PathValue("workloadID")
	entry, ok := a.cache.Get(workloadID)
	if !ok || time.Since(entry.UpdatedAt) > a.cacheMaxAge {
		writeError(w, http.StatusNotFound, "no current usage sample for workload "+workloadID)
		return
	}
	writeJSON(w, http.StatusOK, toView(entry))
}

// GET /v1/workloads/{workloadID}/history?from=RFC3339&to=RFC3339&limit=N
// Raw historical samples from ClickHouse, newest first.
func (a *API) getHistory(w http.ResponseWriter, r *http.Request) {
	workloadID := r.PathValue("workloadID")

	from, to, err := parseWindow(r, defaultHistoryWindow)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	limit := 1000
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	records, err := a.query.History(ctx, workloadID, from, to, limit)
	if err != nil {
		a.logger.WithError(err).WithField("workload_id", workloadID).Error("history query failed")
		writeError(w, http.StatusInternalServerError, "failed to query usage history")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"workload_id": workloadID,
		"from":        from,
		"to":          to,
		"count":       len(records),
		"samples":     records,
	})
}

// GET /v1/workloads/{workloadID}/summary?from=RFC3339&to=RFC3339
// Aggregated usage over a window - the primitive for billing/quota
// enforcement. See store.UsageSummary's doc comments for accuracy caveats
// (counter resets across restarts, CPU-seconds approximation) before
// wiring this into anything that charges money or denies workloads.
func (a *API) getSummary(w http.ResponseWriter, r *http.Request) {
	workloadID := r.PathValue("workloadID")

	from, to, err := parseWindow(r, defaultHistoryWindow)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	summary, err := a.query.Summary(ctx, workloadID, from, to)
	if err != nil {
		a.logger.WithError(err).WithField("workload_id", workloadID).Error("summary query failed")
		writeError(w, http.StatusInternalServerError, "failed to compute usage summary")
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// --- helpers ---

// parseWindow reads ?from and ?to (RFC3339) from the request, defaulting to
// [now-defaultWindow, now] when omitted, and validates from < to.
func parseWindow(r *http.Request, defaultWindow time.Duration) (from, to time.Time, err error) {
	now := time.Now().UTC()
	to = now
	from = now.Add(-defaultWindow)

	if raw := r.URL.Query().Get("to"); raw != "" {
		to, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return from, to, errors.New("to must be an RFC3339 timestamp")
		}
	}
	if raw := r.URL.Query().Get("from"); raw != "" {
		from, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return from, to, errors.New("from must be an RFC3339 timestamp")
		}
	}
	if !from.Before(to) {
		return from, to, errors.New("from must be before to")
	}
	return from, to, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
