// Package jsonllog implements the DecisionLogger port as append-only JSON
// Lines files, one per UTC day, flushed to disk after every record
// (docs/spec/decision-log.md, ADR-0008). It is used identically by the
// simulator and the real controller.
package jsonllog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

var _ ports.DecisionLogger = (*Writer)(nil)

// FileName returns the log file name for the UTC day of t.
func FileName(t time.Time) string {
	return "decisions-" + t.UTC().Format(time.DateOnly) + ".jsonl"
}

// Writer appends records to dir/decisions-YYYY-MM-DD.jsonl. It is safe for
// concurrent use.
type Writer struct {
	dir string

	mu   sync.Mutex
	day  string
	file *os.File
}

// New returns a Writer for dir, creating the directory if needed.
func New(dir string) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	return &Writer{dir: dir}, nil
}

// LogCycle appends one cycle record.
func (w *Writer) LogCycle(ctx context.Context, r ports.CycleRecord) error {
	return w.append(ctx, r.TS, toCycleDTO(r))
}

// LogEvent appends one event record.
func (w *Writer) LogEvent(ctx context.Context, r ports.EventRecord) error {
	return w.append(ctx, r.TS, toEventDTO(r))
}

// Close closes the current file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *Writer) append(ctx context.Context, ts time.Time, record any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode record: %w", err)
	}
	line = append(line, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	f, err := w.fileFor(ts)
	if err != nil {
		return err
	}
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("write record: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("flush record: %w", err)
	}
	return nil
}

// fileFor returns the open file for the day of ts, rotating when the day
// changes. The caller holds w.mu.
func (w *Writer) fileFor(ts time.Time) (*os.File, error) {
	name := FileName(ts)
	if w.file != nil && w.day == name {
		return w.file, nil
	}
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return nil, fmt.Errorf("close %s: %w", w.day, err)
		}
		w.file = nil
	}
	// #nosec G304 -- name is a fixed date-based file name under the
	// operator-configured log directory, never external input.
	f, err := os.OpenFile(filepath.Join(w.dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	w.file, w.day = f, name
	return f, nil
}
