package main

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestRun_ServesAndShutsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan string, 1)
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"-addr", "127.0.0.1:0"}, io.Discard, ready) }()

	var addr string
	select {
	case addr = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start")
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestRun_BadFlag(t *testing.T) {
	if code := run(t.Context(), []string{"-nope"}, io.Discard, nil); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestRun_ListenError(t *testing.T) {
	if code := run(t.Context(), []string{"-addr", "256.0.0.1:1"}, io.Discard, nil); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}
