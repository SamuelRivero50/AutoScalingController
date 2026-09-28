// Package logschema validates decision-log lines against
// docs/spec/decision-log.schema.json. It is imported only by test code, so
// the JSON Schema library never ships in the controller binary.
package logschema

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaPath returns the absolute path of the schema, resolved from this
// source file so it works from any test package directory.
func schemaPath() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("logschema: cannot resolve the schema path")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "docs", "spec", "decision-log.schema.json")
}

// Compile loads and compiles the decision-log schema with format assertion
// enabled (date-time fields are checked).
func Compile(t testing.TB) *jsonschema.Schema {
	t.Helper()
	f, err := os.Open(schemaPath())
	if err != nil {
		t.Fatalf("open schema: %v", err)
	}
	defer f.Close()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err := c.AddResource("decision-log.schema.json", doc); err != nil {
		t.Fatalf("add schema: %v", err)
	}
	sch, err := c.Compile("decision-log.schema.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return sch
}

// ValidateDir validates every line of every *.jsonl file in dir and returns
// the number of lines checked. It fails the test on the first invalid line.
func ValidateDir(t testing.TB, dir string) int {
	t.Helper()
	sch := Compile(t)
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	lines := 0
	for _, path := range files {
		data, err := os.ReadFile(path) //nolint:gosec // test-only helper reading log files it just wrote
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for n := 1; sc.Scan(); n++ {
			if err := ValidateLine(sch, sc.Bytes()); err != nil {
				t.Fatalf("%s line %d invalid: %v\n%s", filepath.Base(path), n, err, sc.Text())
			}
			lines++
		}
		if err := sc.Err(); err != nil {
			t.Fatalf("scan %s: %v", path, err)
		}
	}
	return lines
}

// ValidateLine validates one JSON line.
func ValidateLine(sch *jsonschema.Schema, line []byte) error {
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(line)))
	if err != nil {
		return fmt.Errorf("parse line: %w", err)
	}
	return sch.Validate(inst)
}
