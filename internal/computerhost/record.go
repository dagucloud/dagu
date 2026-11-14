// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package computerhost keeps the state of computer steps that the server,
// CLI and executor share.
package computerhost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	// AgentProvider identifies computer steps in agent sessions.
	AgentProvider = "computer"
	// DataDirName is the directory under the Dagu data directory that holds
	// computer step state.
	DataDirName = "computer"
)

const (
	recordsDirName = "sessions"
	recordFileExt  = ".json"
	recordFileMode = 0o600
	recordDirMode  = 0o700
)

// Record describes a computer step that paused for input. It exists only
// while the step waits. A DAG run holds one record per step.
type Record struct {
	DAGName  string `json:"dagName"`
	DAGRunID string `json:"dagRunId"`
	StepName string `json:"stepName"`
	// Generation is the agent-session generation that paused.
	Generation int `json:"generation"`
	// Deadline bounds how long the step waits to be resumed.
	Deadline time.Time `json:"deadline"`
	// Cursor is the index of the next operation a resumed step runs.
	Cursor int `json:"cursor"`
	// Outputs holds values extracted before the step paused.
	Outputs map[string]any `json:"outputs,omitempty"`
	// ReplayPending and ReplayUsed carry the step's replay cache changes
	// across the pause: the act operations it recorded or dropped, applied
	// only if the step succeeds, and the recordings it replayed, as they
	// were read.
	ReplayPending map[string]json.RawMessage `json:"replayPending,omitempty"`
	ReplayUsed    map[string]json.RawMessage `json:"replayUsed,omitempty"`
}

// Waiting reports whether the record still accepts a resume at now.
func (r Record) Waiting(now time.Time) bool {
	return now.Before(r.Deadline)
}

// recordFileName names the file of a step's record. Hashing the names keeps
// the file inside the store whatever the DAG run ID and step name hold.
func recordFileName(dagRunID, stepName string) string {
	sum := sha256.Sum256([]byte(dagRunID + "\x00" + stepName))
	return hex.EncodeToString(sum[:16]) + recordFileExt
}

// Store persists records as private files.
type Store struct {
	dir string
}

// NewStore returns a store rooted under the computer data directory.
func NewStore(computerDataDir string) *Store {
	return &Store{dir: filepath.Join(computerDataDir, recordsDirName)}
}

// Save writes the record of its step, replacing any previous version, and
// removes records whose deadline passed.
func (s *Store) Save(record Record) error {
	if record.DAGRunID == "" || record.StepName == "" {
		return errors.New("computer session record needs a DAG run ID and a step name")
	}
	if err := os.MkdirAll(s.dir, recordDirMode); err != nil {
		return fmt.Errorf("create computer session directory: %w", err)
	}
	s.removeExpired(time.Now())
	return fileutil.WriteJSONAtomic(s.path(record.DAGRunID, record.StepName), record, recordFileMode)
}

// Load returns the record of a step of a DAG run. A missing record reports
// os.ErrNotExist.
func (s *Store) Load(dagRunID, stepName string) (Record, error) {
	return s.load(s.path(dagRunID, stepName))
}

// Delete removes the record of a step of a DAG run. A missing record is not
// an error.
func (s *Store) Delete(dagRunID, stepName string) error {
	if err := os.Remove(s.path(dagRunID, stepName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) load(path string) (Record, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is inside the store
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("decode computer session record %s: %w", filepath.Base(path), err)
	}
	return record, nil
}

// removeExpired deletes records no step can resume any more.
func (s *Store) removeExpired(now time.Time) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), recordFileExt) {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		if record, err := s.load(path); err == nil && !record.Waiting(now) {
			_ = os.Remove(path)
		}
	}
}

func (s *Store) path(dagRunID, stepName string) string {
	return filepath.Join(s.dir, recordFileName(dagRunID, stepName))
}
