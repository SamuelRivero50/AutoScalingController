// Package evaluation turns JSONL decision logs (docs/spec/decision-log.md)
// into the evaluation metrics of docs/spec/simulator.md §5. It only reads
// what the controller logged, so the same code evaluates simulator runs and
// real-AWS runs.
package evaluation

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// maxLineBytes bounds one JSONL record; cycle records are a few KB.
const maxLineBytes = 4 << 20

// Cycle is the subset of a cycle record the evaluation needs.
type Cycle struct {
	RunID       string            `json:"run_id"`
	CycleID     int64             `json:"cycle_id"`
	Mode        core.Mode         `json:"mode"`
	Profile     core.Profile      `json:"profile"`
	TS          time.Time         `json:"ts"`
	ConfigHash  string            `json:"config_hash"`
	Observation Observation       `json:"observation"`
	Capacity    Capacity          `json:"capacity"`
	Decision    core.DecisionKind `json:"decision"`
	ReasonCode  core.ReasonCode   `json:"reason_code"`
	Conditions  []Condition       `json:"-"`
	Action      Action            `json:"action"`
}

// Observation holds the signals seen in one cycle.
type Observation struct {
	Signals []Signal `json:"signals"`
}

// Signal is one observed signal.
type Signal struct {
	Name    string       `json:"name"`
	Value   *float64     `json:"value"`
	Quality core.Quality `json:"quality"`
}

// Capacity is the capacity snapshot of one cycle.
type Capacity struct {
	Desired   int        `json:"desired"`
	InService int        `json:"in_service"`
	Pending   int        `json:"pending"`
	Draining  int        `json:"draining"`
	Min       int        `json:"min"`
	Max       int        `json:"max"`
	Instances []Instance `json:"instances"`
}

// Instance is one instance of the capacity snapshot.
type Instance struct {
	ID         string             `json:"id"`
	State      core.InstanceState `json:"state"`
	LaunchedAt *time.Time         `json:"launched_at"`
}

// Condition is one evaluated justification condition.
type Condition struct {
	Name      string   `json:"name"`
	Value     *float64 `json:"value"`
	Threshold *float64 `json:"threshold"`
	Met       bool     `json:"met"`
}

// Action is the action a cycle requested and its result.
type Action struct {
	Type       core.ActionType    `json:"type"`
	Params     map[string]any     `json:"params"`
	Status     ports.ActionStatus `json:"status"`
	SkipReason *string            `json:"skip_reason"`
}

// Event is an event record.
type Event struct {
	RunID     string          `json:"run_id"`
	TS        time.Time       `json:"ts"`
	EventType ports.EventType `json:"event_type"`
	Details   map[string]any  `json:"details"`
}

// Run is every record of one controller run, in time order.
type Run struct {
	ID     string
	Cycles []Cycle
	Events []Event
}

// Condition returns the named justification condition.
func (c Cycle) Condition(name string) (Condition, bool) {
	for _, cond := range c.Conditions {
		if cond.Name == name {
			return cond, true
		}
	}
	return Condition{}, false
}

// SignalValue returns the value of a VALID signal.
func (c Cycle) SignalValue(name string) (float64, bool) {
	for _, s := range c.Observation.Signals {
		if s.Name == name && s.Quality == core.QualityValid && s.Value != nil {
			return *s.Value, true
		}
	}
	return 0, false
}

// Load reads every *.jsonl file under the given files or directories and
// groups the records by run_id. Runs are sorted by ID, records by time.
func Load(paths ...string) ([]Run, error) {
	var files []string
	for _, p := range paths {
		found, err := jsonlFiles(p)
		if err != nil {
			return nil, err
		}
		files = append(files, found...)
	}
	if len(files) == 0 {
		return nil, errors.New("evaluation: no .jsonl files found")
	}
	byID := map[string]*Run{}
	for _, f := range files {
		if err := readFile(f, byID); err != nil {
			return nil, err
		}
	}
	runs := make([]Run, 0, len(byID))
	for _, r := range byID {
		slices.SortStableFunc(r.Cycles, func(a, b Cycle) int { return cmp.Compare(a.CycleID, b.CycleID) })
		slices.SortStableFunc(r.Events, func(a, b Event) int { return a.TS.Compare(b.TS) })
		runs = append(runs, *r)
	}
	slices.SortFunc(runs, func(a, b Run) int { return strings.Compare(a.ID, b.ID) })
	return runs, nil
}

func jsonlFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("evaluation: %w", err)
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	var files []string
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("evaluation: walk %s: %w", path, err)
	}
	slices.Sort(files)
	return files, nil
}

func readFile(path string, byID map[string]*Run) error {
	f, err := os.Open(path) // #nosec G304 -- operator-supplied log path
	if err != nil {
		return fmt.Errorf("evaluation: %w", err)
	}
	defer f.Close()
	if err := decode(f, byID); err != nil {
		return fmt.Errorf("evaluation: %s: %w", path, err)
	}
	return nil
}

// decode reads JSONL records from r into byID.
func decode(r io.Reader, byID map[string]*Run) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for line := 1; sc.Scan(); line++ {
		raw := sc.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var head struct {
			Type  string `json:"type"`
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
		run := byID[head.RunID]
		if run == nil {
			run = &Run{ID: head.RunID}
			byID[head.RunID] = run
		}
		switch head.Type {
		case "cycle":
			c, err := decodeCycle(raw)
			if err != nil {
				return fmt.Errorf("line %d: %w", line, err)
			}
			run.Cycles = append(run.Cycles, c)
		case "event":
			var e Event
			if err := json.Unmarshal(raw, &e); err != nil {
				return fmt.Errorf("line %d: %w", line, err)
			}
			run.Events = append(run.Events, e)
		default:
			return fmt.Errorf("line %d: unknown record type %q", line, head.Type)
		}
	}
	return sc.Err()
}

func decodeCycle(raw []byte) (Cycle, error) {
	var c struct {
		Cycle
		Justification struct {
			Conditions []Condition `json:"conditions"`
		} `json:"justification"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return Cycle{}, err
	}
	c.Conditions = c.Justification.Conditions
	return c.Cycle, nil
}
