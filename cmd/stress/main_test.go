package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// fakeTransport answers stress requests without network access.
type fakeTransport struct {
	mu       sync.Mutex
	requests []*http.Request
	status   map[string]int // host -> status; missing host fails the request
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r)
	status, ok := f.status[r.URL.Hostname()]
	if !ok {
		return nil, errors.New("connection refused")
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
		Header:     http.Header{},
		Request:    r,
	}, nil
}

func TestParseTargets(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr string
	}{
		{"two private addresses", "10.0.1.12, 10.0.2.34", 2, ""},
		{"trailing comma", "10.0.1.12,", 1, ""},
		{"empty", "", 0, "required"},
		{"only commas", ",,", 0, "empty"},
		{"hostname", "my-alb-123.us-east-1.elb.amazonaws.com", 0, "not an IP"},
		{"public address", "54.12.3.4", 0, "not a private address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTargets(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || len(got) != tt.want {
				t.Fatalf("got %v, %v; want %d targets", got, err, tt.want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		status   map[string]int
		wantCode int
		wantOut  string
		wantReqs int
	}{
		{
			name:     "all targets accepted",
			args:     []string{"-targets", "10.0.1.12,10.0.2.34", "-duration", "2m"},
			status:   map[string]int{"10.0.1.12": http.StatusAccepted, "10.0.2.34": http.StatusAccepted},
			wantCode: 0, wantOut: "10.0.2.34", wantReqs: 2,
		},
		{
			name:     "one target busy",
			args:     []string{"-targets", "10.0.1.12,10.0.2.34"},
			status:   map[string]int{"10.0.1.12": http.StatusAccepted, "10.0.2.34": http.StatusConflict},
			wantCode: 1, wantOut: "409", wantReqs: 2,
		},
		{
			name:     "one target unreachable",
			args:     []string{"-targets", "10.0.1.12"},
			status:   map[string]int{},
			wantCode: 1, wantOut: "ERROR", wantReqs: 1,
		},
		{"missing targets", []string{}, nil, 2, "", 0},
		{"public target", []string{"-targets", "54.12.3.4"}, nil, 2, "", 0},
		{"bad port", []string{"-targets", "10.0.1.12", "-port", "70000"}, nil, 2, "", 0},
		{"bad duration", []string{"-targets", "10.0.1.12", "-duration", "0s"}, nil, 2, "", 0},
		{"bad flag", []string{"-nope"}, nil, 2, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ft := &fakeTransport{status: tt.status}
			var out bytes.Buffer
			if code := run(t.Context(), tt.args, &out, io.Discard, ft); code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d\n%s", code, tt.wantCode, &out)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Fatalf("output = %q, want it to contain %q", out.String(), tt.wantOut)
			}
			if len(ft.requests) != tt.wantReqs {
				t.Fatalf("requests = %d, want %d", len(ft.requests), tt.wantReqs)
			}
		})
	}
}

func TestRun_RequestShape(t *testing.T) {
	ft := &fakeTransport{status: map[string]int{"10.0.1.12": http.StatusAccepted}}
	if code := run(t.Context(), []string{"-targets", "10.0.1.12", "-port", "9000", "-duration", "90s"}, io.Discard, io.Discard, ft); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	r := ft.requests[0]
	if r.Method != http.MethodPost || r.URL.Host != "10.0.1.12:9000" || r.URL.Path != "/admin/stress" || r.URL.Query().Get("duration") != "1m30s" {
		t.Fatalf("request = %s %s", r.Method, r.URL)
	}
	if r.Header.Get("X-Forwarded-For") != "" {
		t.Fatal("the stress tool must call instances directly, without proxy headers")
	}
}
