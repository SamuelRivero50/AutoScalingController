package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStresser records calls instead of burning CPU.
type fakeStresser struct {
	mu      sync.Mutex
	started []time.Duration
	busy    bool
}

func (f *fakeStresser) Start(d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy {
		return errBusy
	}
	f.busy = true
	f.started = append(f.started, d)
	return nil
}

func (f *fakeStresser) Active() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy
}

func TestNewHandler(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		header     map[string]string
		wantStatus int
		wantBody   string
	}{
		{"root", http.MethodGet, "/", nil, http.StatusOK, "hello world"},
		{"health", http.MethodGet, "/health", nil, http.StatusOK, "ok"},
		{"unknown path", http.MethodGet, "/nope", nil, http.StatusNotFound, ""},
		{"stress requires post", http.MethodGet, "/admin/stress?duration=1m", nil, http.StatusMethodNotAllowed, ""},
		{"stress started", http.MethodPost, "/admin/stress?duration=2m", nil, http.StatusAccepted, `"status":"started"`},
		{"stress missing duration", http.MethodPost, "/admin/stress", nil, http.StatusBadRequest, "required"},
		{"stress invalid duration", http.MethodPost, "/admin/stress?duration=soon", nil, http.StatusBadRequest, "invalid duration"},
		{"stress zero duration", http.MethodPost, "/admin/stress?duration=0s", nil, http.StatusBadRequest, "must be in"},
		{"stress above maximum", http.MethodPost, "/admin/stress?duration=16m", nil, http.StatusBadRequest, "must be in"},
		{"stress through the load balancer", http.MethodPost, "/admin/stress?duration=1m", map[string]string{"X-Forwarded-For": "203.0.113.7"}, http.StatusForbidden, "not through the load balancer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHandler(&fakeStresser{}, slog.New(slog.DiscardHandler))
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, nil)
			for k, v := range tt.header {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestStressHandler_OneRunAtATime(t *testing.T) {
	s := &fakeStresser{}
	h := newHandler(s, slog.New(slog.DiscardHandler))
	post := func() int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/stress?duration=30s", nil))
		return rec.Code
	}
	if code := post(); code != http.StatusAccepted {
		t.Fatalf("first stress = %d, want 202", code)
	}
	if code := post(); code != http.StatusConflict {
		t.Fatalf("second stress = %d, want 409 while the first is active", code)
	}
	if len(s.started) != 1 || s.started[0] != 30*time.Second {
		t.Fatalf("started = %v, want one 30s run", s.started)
	}
}

func TestHealth_IndependentOfStress(t *testing.T) {
	s := &fakeStresser{busy: true}
	h := newHandler(s, slog.New(slog.DiscardHandler))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil))
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusOK || strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("health during stress = %d %q, want 200 ok", rec.Code, body)
	}
}
