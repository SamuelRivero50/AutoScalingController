package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// maxStressDuration bounds a single stress run.
const maxStressDuration = 15 * time.Minute

// newHandler returns the test application's routes (docs/spec/app.md §2).
func newHandler(s Stresser, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleRoot)
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("POST /admin/stress", stressHandler(s, logger))
	return mux
}

func handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "hello world\n") // a failed write to the client is not actionable
}

// handleHealth never touches the stress code path, so it stays responsive
// while the CPU is busy (docs/spec/lifecycle-and-failures.md §5).
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n") // a failed write to the client is not actionable
}

type stressResponse struct {
	Status   string `json:"status"`
	Duration string `json:"duration,omitempty"`
	Error    string `json:"error,omitempty"`
}

func stressHandler(s Stresser, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Defense in depth: the ALB blocks /admin/* and adds
		// X-Forwarded-For to what it forwards, so a request carrying the
		// header came through a proxy, not directly from the controller host.
		if r.Header.Get("X-Forwarded-For") != "" {
			writeJSON(w, http.StatusForbidden, stressResponse{Status: "rejected", Error: "stress is only accepted directly, not through the load balancer"})
			return
		}
		d, err := parseDuration(r.URL.Query().Get("duration"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, stressResponse{Status: "rejected", Error: err.Error()})
			return
		}
		if err := s.Start(d); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errBusy) {
				status = http.StatusConflict
			}
			writeJSON(w, status, stressResponse{Status: "rejected", Error: err.Error()})
			return
		}
		logger.InfoContext(r.Context(), "stress started", "duration", d.String(), "remote", r.RemoteAddr)
		writeJSON(w, http.StatusAccepted, stressResponse{Status: "started", Duration: d.String()})
	}
}

func parseDuration(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, errors.New("duration query parameter is required, e.g. ?duration=5m")
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	if d <= 0 || d > maxStressDuration {
		return 0, fmt.Errorf("duration %s must be in (0, %s]", d, maxStressDuration)
	}
	return d, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // a failed write to the client is not actionable
}
