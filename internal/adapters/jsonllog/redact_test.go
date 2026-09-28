package jsonllog

import "testing"

func TestRedact(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"account id alone", "123456789012", "REDACTED"},
		{"account id in arn", "arn:aws:iam::123456789012:role/LabRole", "arn:aws:iam::REDACTED:role/LabRole"},
		{"long-term access key", "key AKIAABCDEFGHIJKLMNOP end", "key REDACTED end"},
		{"temporary access key", "ASIAABCDEFGHIJKLMNOP", "REDACTED"},
		{"eleven digits kept", "12345678901", "12345678901"},
		{"thirteen digits kept", "1234567890123", "1234567890123"},
		{"instance id kept", "i-0123456789abcdef0", "i-0123456789abcdef0"},
		{"timestamp kept", "2026-09-28T12:00:00Z", "2026-09-28T12:00:00Z"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redact(tt.input); got != tt.want {
				t.Fatalf("redact(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRedactValue(t *testing.T) {
	in := map[string]any{
		"a":    "123456789012",
		"n":    42,
		"list": []any{"AKIAABCDEFGHIJKLMNOP", 1.5, true},
		"strs": []string{"ASIAABCDEFGHIJKLMNOP"},
		"deep": map[string]any{"b": "x 123456789012 y"},
	}
	out := redactValue(in).(map[string]any)
	if out["a"] != "REDACTED" || out["n"] != 42 {
		t.Fatalf("top level = %v", out)
	}
	if out["list"].([]any)[0] != "REDACTED" || out["list"].([]any)[1] != 1.5 {
		t.Fatalf("list = %v", out["list"])
	}
	if out["strs"].([]string)[0] != "REDACTED" {
		t.Fatalf("strs = %v", out["strs"])
	}
	if out["deep"].(map[string]any)["b"] != "x REDACTED y" {
		t.Fatalf("deep = %v", out["deep"])
	}
	if in["a"] != "123456789012" {
		t.Fatal("redactValue must not mutate its input")
	}
}
