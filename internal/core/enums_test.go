package core

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

const schemaPath = "../../docs/spec/decision-log.schema.json"

// schemaEnum walks a path of object keys in the decoded schema and returns
// the "enum" array found there.
func schemaEnum(t *testing.T, root map[string]any, path ...string) []string {
	t.Helper()
	node := root
	for _, key := range path {
		next, ok := node[key].(map[string]any)
		if !ok {
			t.Fatalf("schema path %v: key %q not found", path, key)
		}
		node = next
	}
	raw, ok := node["enum"].([]any)
	if !ok {
		t.Fatalf("schema path %v has no enum", path)
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

func strs[T ~string](vs []T) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, string(v))
	}
	return out
}

func TestEnumsMatchDecisionLogSchema(t *testing.T) {
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	cycle := []string{"definitions", "cycleRecord", "properties"}
	at := func(p ...string) []string { return append(slices.Clone(cycle), p...) }

	tests := []struct {
		name string
		path []string
		got  []string
	}{
		{"decision", at("decision"), strs(DecisionKinds())},
		{"reason_code", at("reason_code"), strs(ReasonCodes())},
		{"signal quality", at("observation", "properties", "signals", "items", "properties", "quality"), strs(Qualities())},
		{"instance state", at("capacity", "properties", "instances", "items", "properties", "state"), strs(InstanceStates())},
		{"breaker state", at("breaker", "properties", "state"), strs(BreakerStates())},
		{"action type", at("action", "properties", "type"), strs(ActionTypes())},
		{"profile", at("profile"), strs(Profiles())},
		{"mode", at("mode"), strs(Modes())},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := schemaEnum(t, schema, tt.path...)
			got := slices.Clone(tt.got)
			slices.Sort(want)
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Fatalf("Go enum %v != schema enum %v", got, want)
			}
		})
	}
}

func TestReasonCodeDecisionMapping(t *testing.T) {
	for _, r := range ReasonCodes() {
		kind, ok := r.Decision()
		if !ok {
			t.Fatalf("%s not mapped to a decision", r)
		}
		var prefix string
		switch kind {
		case IncreaseCapacity:
			prefix = "INCREASE_"
		case ReduceCapacity:
			prefix = "REDUCE_"
		default:
			prefix = "MAINTAIN_"
		}
		if len(r) < len(prefix) || string(r[:len(prefix)]) != prefix {
			t.Errorf("%s mapped to %s, prefix mismatch", r, kind)
		}
	}
	if _, ok := ReasonCode("INVENTED").Decision(); ok {
		t.Fatal("unknown reason code must not map to a decision")
	}
}
