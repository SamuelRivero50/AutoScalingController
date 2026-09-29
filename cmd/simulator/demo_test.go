package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/clock"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/evaluation"
	"github.com/SamuelRivero50/AutoScalingController/internal/testutil/logschema"
)

func TestDial(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		set  float64
		want float64
	}{
		{name: "in range", set: 2.5, want: 2.5},
		{name: "negative clamps to zero", set: -1, want: 0},
		{name: "above max clamps", set: 99, want: maxDialLoad},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var d dial
			d.Set(tt.set)
			if got := d.Get(); got != tt.want {
				t.Fatalf("Get() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		line    string
		current float64
		want    command
		wantErr bool
	}{
		{name: "number", line: "2.5", want: command{set: true, load: 2.5}},
		{name: "number with spaces", line: "  3 ", want: command{set: true, load: 3}},
		{name: "preset", line: "Overload", want: command{set: true, load: loadPresets["overload"]}},
		{name: "idle preset", line: "idle", want: command{set: true, load: 0}},
		{name: "plus", line: "+", current: 1, want: command{set: true, load: 1.5}},
		{name: "minus floors at zero", line: "-", current: 0.2, want: command{set: true, load: 0}},
		{name: "plus caps at max", line: "+", current: maxDialLoad, want: command{set: true, load: maxDialLoad}},
		{name: "quit", line: "quit", want: command{quit: true}},
		{name: "q", line: "q", want: command{quit: true}},
		{name: "help", line: "help", want: command{help: true}},
		{name: "status", line: "status", want: command{status: true}},
		{name: "empty line shows status", line: "", want: command{status: true}},
		{name: "unknown word", line: "faster", wantErr: true},
		{name: "negative number", line: "-2", wantErr: true},
		{name: "too high", line: "11", wantErr: true},
		{name: "nan", line: "NaN", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseCommand(tt.line, tt.current)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseCommand(%q) error = %v, wantErr %v", tt.line, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("parseCommand(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}
}

func TestReadCommands(t *testing.T) {
	t.Parallel()
	var d dial
	d.Set(1)
	var out bytes.Buffer
	quits := 0
	readCommands(strings.NewReader("3\nstatus\nbogus\nhelp\n+\nquit\n5\n"), &d, &out, func() { quits++ })

	if quits != 1 {
		t.Fatalf("quit called %d times, want 1", quits)
	}
	if got := d.Get(); got != 3.5 {
		t.Fatalf("load = %v, want 3.5 (input after quit must be ignored)", got)
	}
	for _, want := range []string{"load set to 3.00", "load is 3.00", "unknown command", "Commands:", "load set to 3.50"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestReadCommands_EndOfInputKeepsRunning(t *testing.T) {
	t.Parallel()
	var d dial
	quits := 0
	readCommands(strings.NewReader("2\n"), &d, &bytes.Buffer{}, func() { quits++ })
	if quits != 0 || d.Get() != 2 {
		t.Fatalf("quits = %d, load = %v; want 0 and 2", quits, d.Get())
	}
}

func TestRunDemo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	opts := demoOptions{Seed: 1, Load: 3, Initial: 1, LogDir: dir, Cycles: 30, Clock: clock.NewFake(start)}
	if err := runDemo(t.Context(), opts, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"live demo: demo profile, one cycle every 10s", "#1 ", "INCREASE_CAPACITY", "INCREASE_CPU_HIGH", "SET_DESIRED_CAPACITY="} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}

	if n := logschema.ValidateDir(t, dir); n == 0 {
		t.Fatal("no decision records written")
	}
	runs, err := evaluation.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	rep := evaluation.Evaluate(runs[0])
	if rep.Profile != core.ProfileDemo || rep.Cycles != 30 || rep.Interval != evaluation.Duration(10*time.Second) {
		t.Fatalf("report = profile %s, %d cycles every %v", rep.Profile, rep.Cycles, time.Duration(rep.Interval))
	}
	if rep.Changes.ScaleOuts == 0 {
		t.Fatalf("load 3 on one instance did not scale out: %+v", rep.Changes)
	}
}

func TestRunDemo_Quit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// No cycle limit: only the quit command ends the demo.
	opts := demoOptions{Seed: 1, Load: 1, Initial: 1, LogDir: dir, Clock: clock.NewFake(time.Unix(0, 0))}
	if err := runDemo(t.Context(), opts, strings.NewReader("quit\n"), &bytes.Buffer{}); err != nil {
		t.Fatalf("quitting must end the demo cleanly, got %v", err)
	}
	runs, err := evaluation.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	last := runs[0].Events[len(runs[0].Events)-1]
	if last.EventType != "CONTROLLER_STOPPED" {
		t.Fatalf("last event = %s, want CONTROLLER_STOPPED", last.EventType)
	}
}

func TestRunDemo_BadLogDir(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := demoOptions{Seed: 1, Load: 1, Initial: 1, LogDir: filepath.Join(file, "logs"), Clock: clock.NewFake(time.Unix(0, 0))}
	if err := runDemo(t.Context(), opts, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("runDemo with an unusable log directory succeeded")
	}
}
