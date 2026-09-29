package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBurner_RunsForBoundedDuration(t *testing.T) {
	b := newBurner(t.Context(), 0.5)
	b.workers = 1
	b.slot = 10 * time.Millisecond

	start := time.Now()
	if err := b.Start(60 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !b.Active() {
		t.Fatal("burner must be active right after Start")
	}
	if err := b.Start(time.Second); !errors.Is(err, errBusy) {
		t.Fatalf("second Start = %v, want errBusy", err)
	}
	b.Wait()
	elapsed := time.Since(start)
	if b.Active() {
		t.Fatal("burner must be inactive after the run")
	}
	if elapsed < 50*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("run took %v, want about 60ms", elapsed)
	}
	if err := b.Start(10 * time.Millisecond); err != nil {
		t.Fatalf("Start after a finished run = %v, want nil", err)
	}
	b.Wait()
}

func TestBurner_StopsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	b := newBurner(ctx, 0.85)
	b.workers = 2
	b.slot = 10 * time.Millisecond
	if err := b.Start(time.Hour); err != nil {
		t.Fatal(err)
	}
	cancel()

	done := make(chan struct{})
	go func() {
		b.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stress did not stop after shutdown")
	}
}

func TestNewBurner_DutyBelowFull(t *testing.T) {
	b := newBurner(t.Context(), stressDuty)
	if b.duty < 0.8 || b.duty > 0.9 {
		t.Fatalf("duty = %v, want 80-90%%", b.duty)
	}
	if b.workers < 1 {
		t.Fatal("at least one worker is required")
	}
}
