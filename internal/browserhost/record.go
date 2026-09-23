// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost

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
	// AgentProvider identifies browser steps in agent sessions.
	AgentProvider = "browser"
	// DataDirName is the directory under the Dagu data directory that holds
	// browser state.
	DataDirName = "browser"
)

// State describes who currently holds a browser session.
type State string

const (
	// StateRunning means a step execution is driving the browser.
	StateRunning State = "running"
	// StateDetached means the browser waits for a step to resume it.
	StateDetached State = "detached"
)

const (
	recordsDirName = "sessions"
	recordFileExt  = ".json"
	recordFileMode = 0o600
	recordDirMode  = 0o700
)

// Record is the durable description of one browser session kept by a step.
type Record struct {
	ID       string `json:"id"`
	DAGName  string `json:"dagName"`
	DAGRunID string `json:"dagRunId"`
	StepName string `json:"stepName"`
	// Generation is the agent-session generation that owns the browser.
	Generation int   `json:"generation"`
	State      State `json:"state"`
	// Deadline bounds how long a detached browser waits to be resumed.
	Deadline time.Time `json:"deadline,omitzero"`

	CDPURL          string `json:"cdpUrl"`
	ExtensionID     string `json:"extensionId,omitempty"`
	ExtensionDir    string `json:"extensionDir,omitempty"`
	UserDataDir     string `json:"userDataDir,omitempty"`
	OwnsUserDataDir bool   `json:"ownsUserDataDir,omitempty"`
	Profile         string `json:"profile,omitempty"`
	DownloadsDir    string `json:"downloadsDir,omitempty"`

	// BrowserPID and BrowserStartedAt identify the browser process, so it can
	// be ended when it no longer answers on its DevTools address.
	BrowserPID       int   `json:"browserPid,omitempty"`
	BrowserStartedAt int64 `json:"browserStartedAt,omitempty"`

	// OwnerPID and OwnerStartedAt identify the process driving a running
	// browser, so a crashed owner can be detected even after PID reuse.
	OwnerPID       int   `json:"ownerPid,omitempty"`
	OwnerStartedAt int64 `json:"ownerStartedAt,omitempty"`

	// Cursor is the index of the next operation a resumed step runs.
	Cursor int `json:"cursor"`
	// Outputs holds values extracted before the step detached.
	Outputs map[string]any `json:"outputs,omitempty"`
}

// RecordID returns the record identifier for a step of a DAG run.
func RecordID(dagRunID, stepName string) string {
	sum := sha256.Sum256([]byte(dagRunID + "\x00" + stepName))
	return hex.EncodeToString(sum[:16])
}

// Store persists browser session records as private files.
type Store struct {
	dir string
}

// NewStore returns a store rooted under the browser data directory.
func NewStore(browserDataDir string) *Store {
	return &Store{dir: filepath.Join(browserDataDir, recordsDirName)}
}

// Save writes the record, replacing any previous version.
func (s *Store) Save(record Record) error {
	if record.ID == "" {
		return errors.New("browser session record id is required")
	}
	if err := os.MkdirAll(s.dir, recordDirMode); err != nil {
		return fmt.Errorf("create browser session directory: %w", err)
	}
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
		return Record{}, fmt.Errorf("decode browser session record %s: %w", id, err)
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

// List returns every readable record.
func (s *Store) List() ([]Record, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), recordFileExt)
		if entry.IsDir() || !ok {
			continue
		}
		record, err := s.Load(id)
		if err != nil {
			// A record being replaced or removed concurrently is skipped.
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+recordFileExt)
}
