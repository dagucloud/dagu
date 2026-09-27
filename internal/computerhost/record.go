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
// while the step waits.
type Record struct {
	ID       string `json:"id"`
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
}

// Waiting reports whether the record still accepts a resume at now.
func (r Record) Waiting(now time.Time) bool {
	return now.Before(r.Deadline)
}

// RecordID returns the record identifier for a step of a DAG run.
func RecordID(dagRunID, stepName string) string {
	sum := sha256.Sum256([]byte(dagRunID + "\x00" + stepName))
	return hex.EncodeToString(sum[:16])
}

// Store persists records as private files.
type Store struct {
	dir string
}

// NewStore returns a store rooted under the computer data directory.
func NewStore(computerDataDir string) *Store {
	return &Store{dir: filepath.Join(computerDataDir, recordsDirName)}
}

// Save writes the record, replacing any previous version, and removes
// records whose deadline passed.
func (s *Store) Save(record Record) error {
	if record.ID == "" {
		return errors.New("computer session record id is required")
	}
	if err := os.MkdirAll(s.dir, recordDirMode); err != nil {
		return fmt.Errorf("create computer session directory: %w", err)
	}
	s.removeExpired(time.Now())
	return fileutil.WriteJSONAtomic(s.path(record.ID), record, recordFileMode)
}

// Load returns the record with id. A missing record reports os.ErrNotExist.
func (s *Store) Load(id string) (Record, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("decode computer session record %s: %w", id, err)
	}
	return record, nil
}

// Delete removes the record with id. A missing record is not an error.
func (s *Store) Delete(id string) error {
	if err := os.Remove(s.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// removeExpired deletes records no step can resume any more.
func (s *Store) removeExpired(now time.Time) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), recordFileExt)
		if entry.IsDir() || !ok {
			continue
		}
		if record, err := s.Load(id); err == nil && !record.Waiting(now) {
			_ = s.Delete(id)
		}
	}
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+recordFileExt)
}
