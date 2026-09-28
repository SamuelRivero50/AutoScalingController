// Package filestate implements the StateStore port as a single local JSON
// file written atomically (temp file in the same directory, fsync, rename),
// so a reader never sees a partial file (ADR-0009).
package filestate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

var _ ports.StateStore = (*Store)(nil)

// Store persists state to one file.
type Store struct {
	path string
}

// New returns a Store backed by path. The parent directory is created on
// the first Save.
func New(path string) *Store {
	return &Store{path: path}
}

// Load reads the state file. It returns ports.ErrStateNotFound when the file
// does not exist and an error wrapping ports.ErrStateCorrupt when it cannot
// be decoded or has an unknown format version.
func (s *Store) Load(ctx context.Context) (ports.State, error) {
	if err := ctx.Err(); err != nil {
		return ports.State{}, err
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return ports.State{}, ports.ErrStateNotFound
	}
	if err != nil {
		return ports.State{}, fmt.Errorf("read state: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var d stateDTO
	if err := dec.Decode(&d); err != nil {
		return ports.State{}, fmt.Errorf("%w: decode state: %w", ports.ErrStateCorrupt, err)
	}
	if d.Version != formatVersion {
		return ports.State{}, fmt.Errorf("%w: format version %d, want %d", ports.ErrStateCorrupt, d.Version, formatVersion)
	}
	return fromDTO(d), nil
}

// Save writes the state atomically.
func (s *Store) Save(ctx context.Context, st ports.State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(toDTO(st))
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp state: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName) // best effort: the temp file is garbage either way
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("flush temp state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	committed = true
	return nil
}
