package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/metrics"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/model"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/service"
	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/store"
)

type Handler struct {
	svc     *service.Service
	metrics *metrics.Collector
}

func New(svc *service.Service, metrics *metrics.Collector) *Handler {
	return &Handler{svc: svc, metrics: metrics}
}

func (h *Handler) API() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/internal/evaluate", h.handleEvaluate)
	mux.HandleFunc("/ai/query", h.handleAIQuery)
	mux.HandleFunc("/recommendations/pending", h.handlePendingRecommendations)
	mux.HandleFunc("/recommendations", h.handleRecommendations)
	mux.HandleFunc("/recommendations/", h.handleRecommendationActions)
	return mux
}

func (h *Handler) handleAIQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req model.AIQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	resp, err := h.svc.Query(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) Metrics() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(h.metrics.RenderPrometheus()))
	})
	return mux
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleEvaluate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type evaluateRequest struct {
		Snapshots []model.FeatureSnapshot `json:"snapshots"`
	}
	var req evaluateRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}

	recs, err := h.svc.Evaluate(r.Context(), req.Snapshots)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, recs)
}

func (h *Handler) handlePendingRecommendations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	recs, err := h.svc.ListRecommendations(r.Context(), string(model.StatusPending), r.URL.Query().Get("workload"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, recs)
}

func (h *Handler) handleRecommendations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	recs, err := h.svc.ListRecommendations(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("workload"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, recs)
}

func (h *Handler) handleRecommendationActions(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/recommendations/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	action := parts[1]

	var (
		rec model.Recommendation
		err error
	)
	switch {
	case r.Method == http.MethodPost && action == "approve":
		rec, err = h.svc.ApproveRecommendation(r.Context(), id)
	case r.Method == http.MethodPost && action == "reject":
		rec, err = h.svc.RejectRecommendation(r.Context(), id)
	case r.Method == http.MethodPost && action == "apply":
		rec, err = h.svc.ApplyRecommendation(r.Context(), id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err != nil {
		statusCode := http.StatusInternalServerError
		if errors.Is(err, store.ErrRecommendationNotFound) {
			statusCode = http.StatusNotFound
		}
		if errors.Is(err, store.ErrInvalidTransition) {
			statusCode = http.StatusConflict
		}
		http.Error(w, err.Error(), statusCode)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func writeJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}
