package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
	}{
		{"single scenario", []string{"-scenario", "S1"}, 0, "S1   PASS"},
		{"lowercase id", []string{"-scenario", "s1"}, 0, "S1   PASS"},
		{"unknown scenario", []string{"-scenario", "S99"}, 2, ""},
		{"bad flag", []string{"-nope"}, 2, ""},
		{"demo one cycle", []string{"-demo", "-cycles", "1"}, 0, "live demo: demo profile"},
		{"demo negative load", []string{"-demo", "-load", "-1"}, 2, ""},
		{"demo no instances", []string{"-demo", "-initial", "0"}, 2, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append([]string{"-out", t.TempDir()}, tt.args...)
			if code := run(t.Context(), args, strings.NewReader(""), &stdout, &stderr); code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, tt.wantCode, &stdout, &stderr)
			}
			if tt.wantOut != "" && !strings.Contains(stdout.String(), tt.wantOut) {
				t.Fatalf("stdout = %q, want it to contain %q", stdout.String(), tt.wantOut)
			}
		})
	}
}

func TestRun_WritesLogsPerScenario(t *testing.T) {
	dir := t.TempDir()
	if code := run(t.Context(), []string{"-scenario", "S1", "-seed", "9", "-out", dir}, strings.NewReader(""), io.Discard, io.Discard); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	logs, err := filepath.Glob(filepath.Join(dir, "S1-seed9", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatalf("no JSONL log written under %s", dir)
	}
	data, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"cycle"`) {
		t.Fatal("log holds no cycle record")
	}
}
