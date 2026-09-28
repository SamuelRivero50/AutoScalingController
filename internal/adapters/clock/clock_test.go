package clock

import (
	"context"
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func TestSystem_Now(t *testing.T) {
	if loc := (System{}).Now().Location(); loc != time.UTC {
		t.Fatalf("location = %v, want UTC", loc)
	}
}

func TestSystem_Sleep(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := (System{}).Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep on cancelled ctx = %v, want context.Canceled", err)
	}
	if err := (System{}).Sleep(t.Context(), time.Millisecond); err != nil {
		t.Fatalf("Sleep = %v, want nil", err)
	}
}

func TestFake_Advance(t *testing.T) {
	tests := []struct {
		name string
		by   time.Duration
		want time.Time
	}{
		{"forward", 90 * time.Minute, t0.Add(90 * time.Minute)},
		{"zero is a no-op", 0, t0},
		{"negative is ignored", -time.Hour, t0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewFake(t0)
			f.Advance(tt.by)
			if got := f.Now(); !got.Equal(tt.want) {
				t.Fatalf("Now() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFake_Sleep(t *testing.T) {
	f := NewFake(t0)
	if err := f.Sleep(t.Context(), 24*time.Hour); err != nil {
		t.Fatalf("Sleep = %v", err)
	}
	if got := f.Now(); !got.Equal(t0.Add(24 * time.Hour)) {
		t.Fatalf("Now() = %v after sleeping a day", got)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep on cancelled ctx = %v, want context.Canceled", err)
	}
	if got := f.Now(); !got.Equal(t0.Add(24 * time.Hour)) {
		t.Fatal("a cancelled Sleep must not advance the clock")
	}
}

func TestFake_ConcurrentUse(t *testing.T) {
	f := NewFake(t0)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			f.Advance(time.Second)
		}
	}()
	for range 1000 {
		_ = f.Now()
	}
	<-done
	if got := f.Now(); !got.Equal(t0.Add(1000 * time.Second)) {
		t.Fatalf("Now() = %v, want 1000s later", got)
	}
}

func TestNewFake_ConvertsToUTC(t *testing.T) {
	local := time.Date(2026, 9, 28, 7, 0, 0, 0, time.FixedZone("COT", -5*3600))
	if loc := NewFake(local).Now().Location(); loc != time.UTC {
		t.Fatalf("location = %v, want UTC", loc)
	}
}
