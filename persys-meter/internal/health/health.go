// Package health serves /healthz, /readyz, and /metrics over plain HTTP -
// intentionally simple (no mTLS/auth) since this is meant for k8s liveness/
// readiness probes and Prometheus scraping from inside the cluster network,
// not for external clients.
package health

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// Checker reports whether the service is ready to do work (e.g. Redis and
// ClickHouse are both reachable).
type Checker interface {
	Ready(ctx context.Context) error
}

// Serve starts the health/metrics HTTP server in a background goroutine and
// returns the *http.Server so the caller can Shutdown() it gracefully.
func Serve(addr string, checker Checker, logger *logrus.Entry) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		// Liveness: the process is up and serving HTTP. Doesn't check
		// dependencies - that's what /readyz is for. A live-but-not-ready
		// process shouldn't be killed by a liveness probe, just pulled out
		// of rotation by a readiness probe.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		if err := checker.Ready(ctx); err != nil {
			logger.WithError(err).Warn("readiness check failed")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready: " + err.Error()))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.WithError(err).Error("health server stopped unexpectedly")
		}
	}()

	logger.WithField("addr", addr).Info("health/metrics server listening")
	return srv
}
