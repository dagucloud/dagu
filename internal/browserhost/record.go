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

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
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
	// StateRunning means a step execution, or a browser session command, is
	// driving the browser.
	StateRunning State = "running"
	// StateDetached means the browser waits for a step to resume it.
	StateDetached State = "detached"
	// StateInteractive means a browser session's browser waits for the
	// session's next command.
	StateInteractive State = "interactive"
	// StateEnded means a browser session's browser is closed. Its record
	// keeps the session's history until its deadline.
	StateEnded State = "ended"
)

const (
	recordsDirName     = "sessions"
	interactiveDirName = "interactive"
	recordFileExt      = ".json"
	recordFileMode     = 0o600
	recordDirMode      = 0o700
	sessionLockSuffix  = ".lock"
)

// SessionRetention is how long a browser session's history is kept after
// its browser closed on its own.
const SessionRetention = 24 * time.Hour

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

	// CertProxyURL locates the proxy presenting the browser's client
	// certificates, which runs while the browser does; CertProxyPID and
	// CertProxyStartedAt identify its process, so it can be ended with the
	// browser.
	CertProxyURL       string `json:"certProxyUrl,omitempty"`
	CertProxyPID       int    `json:"certProxyPid,omitempty"`
	CertProxyStartedAt int64  `json:"certProxyStartedAt,omitempty"`

	// WriteGateURL locates the gate deciding the browser's writes;
	// WriteGatePID and WriteGateStartedAt identify its process. A browser
	// whose gate is gone sends writes nothing decides, so it is closed.
	// WriteGateSeq is the last of the gate's events reported.
	WriteGateURL       string `json:"writeGateUrl,omitempty"`
	WriteGatePID       int    `json:"writeGatePid,omitempty"`
	WriteGateStartedAt int64  `json:"writeGateStartedAt,omitempty"`
	WriteGateSeq       int64  `json:"writeGateSeq,omitempty"`

	// OwnerPID and OwnerStartedAt identify the process driving a running
	// browser, so a crashed owner can be detected even after PID reuse.
	OwnerPID       int   `json:"ownerPid,omitempty"`
	OwnerStartedAt int64 `json:"ownerStartedAt,omitempty"`

	// Cursor is the index of the next operation a resumed step runs.
	Cursor int `json:"cursor"`
	// Outputs holds values extracted before the step detached.
	Outputs map[string]any `json:"outputs,omitempty"`
	// ReplayPending and ReplayUsed carry the step's replay cache changes
	// across the wait: the act operations it recorded or dropped, applied
	// only if the step succeeds, and the recordings it replayed, as they
	// were read.
	ReplayPending map[string]json.RawMessage `json:"replayPending,omitempty"`
	ReplayUsed    map[string]json.RawMessage `json:"replayUsed,omitempty"`

	// WorkDir holds the files a browser session owns, such as its
	// screenshots and downloads. It is removed with the browser.
	WorkDir string `json:"workDir,omitempty"`
	// Interactive is a browser session's own state, which the browser
	// runtime defines.
	Interactive json.RawMessage `json:"interactive,omitempty"`
}

// RecordID returns the record identifier for a step of a DAG run. item
// identifies the foreach item whose body runs the step, so the items of one
// foreach each have a record of their own; it is empty for a step outside a
// foreach body.
func RecordID(dagRunID, item, stepName string) string {
	sum := sha256.Sum256([]byte(dagRunID + "\x00" + item + "\x00" + stepName))
	return hex.EncodeToString(sum[:16])
}

// Store persists browser session records as private files.
type Store struct {
	dir string
}

// NewStore returns the store of the browsers steps keep, rooted under the
// browser data directory.
func NewStore(browserDataDir string) *Store {
	return &Store{dir: filepath.Join(browserDataDir, recordsDirName)}
}

// NewInteractiveStore returns the store of browser sessions, rooted under
// the browser data directory. It is kept apart from the steps' store, whose
// sweeps release records in states they do not know.
func NewInteractiveStore(browserDataDir string) *Store {
	return &Store{dir: filepath.Join(browserDataDir, interactiveDirName)}
}

// WorkDir returns the directory for the files the session id owns.
func (s *Store) WorkDir(id string) string {
	return filepath.Join(s.dir, id)
}

// SessionLock returns the lock held by whatever uses the browser of the
// session id: one of its commands, or a sweep closing it.
func (s *Store) SessionLock(id string) dirlock.DirLock {
	return dirlock.New(s.lockDir(id), nil)
}

// Purge removes the record of id with its work and lock directories.
func (s *Store) Purge(id string) error {
	return errors.Join(os.RemoveAll(s.WorkDir(id)), os.RemoveAll(s.lockDir(id)), s.Delete(id))
}

func (s *Store) lockDir(id string) string {
	return filepath.Join(s.dir, id+sessionLockSuffix)
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
