// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/workspace"
)

// SyncStatus represents the synchronization status of a DAG.
type SyncStatus string

const (
	// StatusSynced indicates the DAG is in sync with remote.
	StatusSynced SyncStatus = "synced"

	// StatusModified indicates the DAG has local modifications.
	StatusModified SyncStatus = "modified"

	// StatusUntracked indicates the DAG exists only locally.
	StatusUntracked SyncStatus = "untracked"

	// StatusConflict indicates a conflict between local and remote versions.
	StatusConflict SyncStatus = "conflict"

	// StatusMissing indicates a previously tracked file is no longer on disk.
	StatusMissing SyncStatus = "missing"
)

const (
	agentMemoryDir = "memory"
	agentSkillsDir = "skills"
	agentSoulsDir  = "souls"
	agentDocsDir   = "docs"
	baseConfigID   = "base"
)

// DAGKind represents the type of tracked item in git sync.
type DAGKind string

const (
	// DAGKindDAG indicates a regular DAG definition file.
	DAGKindDAG DAGKind = "dag"

	// DAGKindMemory indicates an agent memory file under memory/.
	DAGKindMemory DAGKind = "memory"

	// DAGKindSkill indicates an agent skill file under skills/.
	DAGKindSkill DAGKind = "skill"

	// DAGKindSoul indicates an agent soul file under souls/.
	DAGKindSoul DAGKind = "soul"

	// DAGKindDoc indicates a document file under docs/.
	DAGKindDoc DAGKind = "doc"

	// DAGKindConfig indicates a global or workspace base config file.
	DAGKindConfig DAGKind = "config"
)

// KindForDAGID returns the DAG kind derived from a DAG ID.
func KindForDAGID(id string) DAGKind {
	id = normalizeDAGIDSeparators(id)
	if isConfigFile(id) {
		return DAGKindConfig
	}
	if strings.HasPrefix(id, agentMemoryDir+"/") {
		return DAGKindMemory
	}
	if strings.HasPrefix(id, agentSkillsDir+"/") {
		return DAGKindSkill
	}
	if strings.HasPrefix(id, agentSoulsDir+"/") {
		return DAGKindSoul
	}
	if strings.HasPrefix(id, agentDocsDir+"/") {
		return DAGKindDoc
	}
	return DAGKindDAG
}

func isConfigFile(id string) bool {
	id = normalizeDAGIDSeparators(id)
	if id == baseConfigID {
		return true
	}
	_, ok := workspaceBaseConfigNameFromID(id)
	return ok
}

func workspaceBaseConfigNameFromID(id string) (string, bool) {
	parts := strings.Split(normalizeDAGIDSeparators(id), "/")
	if len(parts) != 3 {
		return "", false
	}
	if parts[0] != workspace.BaseConfigDirName || parts[2] != workspace.BaseConfigStem() {
		return "", false
	}
	if err := workspace.ValidateName(parts[1]); err != nil {
		return "", false
	}
	return parts[1], true
}

func normalizeDAGIDSeparators(id string) string {
	return strings.ReplaceAll(id, "\\", "/")
}

// isMemoryFile returns true if the file ID belongs to the memory directory.
func isMemoryFile(id string) bool {
	return KindForDAGID(id) == DAGKindMemory
}

// isSkillFile returns true if the file ID belongs to the skills directory.
func isSkillFile(id string) bool {
	return KindForDAGID(id) == DAGKindSkill
}

// isSoulFile returns true if the file ID belongs to the souls directory.
func isSoulFile(id string) bool {
	return KindForDAGID(id) == DAGKindSoul
}

// isDocFile returns true if the file ID belongs to the docs directory.
func isDocFile(id string) bool {
	return KindForDAGID(id) == DAGKindDoc
}

// State represents the overall sync state.
type State struct {
	// Version is the state file format version.
	Version int `json:"version"`

	// Repository is the repository URL.
	Repository string `json:"repository"`

	// Branch is the branch being synced.
	Branch string `json:"branch"`

	// LastSyncAt is the timestamp of the last successful sync.
	LastSyncAt *time.Time `json:"lastSyncAt,omitempty"`

	// LastSyncCommit is the commit hash of the last sync.
	LastSyncCommit string `json:"lastSyncCommit,omitempty"`

	// LastSyncStatus is the status of the last sync operation.
	LastSyncStatus string `json:"lastSyncStatus,omitempty"`

	// LastError is the error message from the last failed sync.
	LastError *string `json:"lastError,omitempty"`

	// DAGs contains the sync state for each DAG.
	DAGs map[string]*DAGState `json:"dags"`
}

// DAGState represents the sync state for a single DAG.
type DAGState struct {
	// Status is the current sync status.
	Status SyncStatus `json:"status"`

	// Kind identifies whether this item is a DAG or memory file.
	Kind DAGKind `json:"kind,omitempty"`

	// BaseCommit is the commit hash when the DAG was last synced.
	BaseCommit string `json:"baseCommit,omitempty"`

	// LastSyncedHash is the content hash when the DAG was last synced.
	LastSyncedHash string `json:"lastSyncedHash,omitempty"`

	// LastSyncedAt is when the DAG was last synced.
	LastSyncedAt *time.Time `json:"lastSyncedAt,omitempty"`

	// ModifiedAt is when the DAG was last modified locally.
	ModifiedAt *time.Time `json:"modifiedAt,omitempty"`

	// LocalHash is the current local content hash.
	LocalHash string `json:"localHash,omitempty"`

	// RemoteCommit is the commit hash of the conflicting remote version.
	RemoteCommit string `json:"remoteCommit,omitempty"`

	// RemoteAuthor is the author of the conflicting remote commit.
	RemoteAuthor string `json:"remoteAuthor,omitempty"`

	// RemoteMessage is the commit message of the conflicting remote commit.
	RemoteMessage string `json:"remoteMessage,omitempty"`

	// ConflictDetectedAt is when the conflict was detected.
	ConflictDetectedAt *time.Time `json:"conflictDetectedAt,omitempty"`

	// PreviousStatus is the status before transitioning to missing.
	PreviousStatus string `json:"previousStatus,omitempty"`

	// MissingAt is when the file was first detected as missing.
	MissingAt *time.Time `json:"missingAt,omitempty"`

	// LastStatModTime is the file modification time used for stat-before-hash optimization.
	LastStatModTime *time.Time `json:"lastStatModTime,omitempty"`

	// LastStatSize is the file size used for stat-before-hash optimization.
	LastStatSize *int64 `json:"lastStatSize,omitempty"`
}

// StateManager manages the sync state persistence.
type StateManager struct {
	statePath string
	mu        sync.RWMutex
	state     *State
}

// NewStateManager creates a new state manager.
func NewStateManager(dataDir string) *StateManager {
	return &StateManager{
		statePath: filepath.Join(dataDir, "gitsync", "state.json"),
	}
}

// Load loads the state from disk.
func (m *StateManager) Load() (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty state
			m.state = &State{
				Version: 1,
				DAGs:    make(map[string]*DAGState),
			}
			return m.state, nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	if state.DAGs == nil {
		state.DAGs = make(map[string]*DAGState)
	}

	m.state = &state
	return m.state, nil
}

// Save saves the state to disk.
func (m *StateManager) Save(state *State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Ensure directory exists
	dir := filepath.Dir(m.statePath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Write atomically using temp file
	tmpPath := m.statePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	if err := os.Rename(tmpPath, m.statePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	m.state = state
	return nil
}

// GetState returns the current state (from cache or loads from disk).
func (m *StateManager) GetState() (*State, error) {
	m.mu.RLock()
	if m.state != nil {
		defer m.mu.RUnlock()
		return m.state, nil
	}
	m.mu.RUnlock()

	return m.Load()
}

// ComputeContentHash computes the SHA256 hash of content bytes.
func ComputeContentHash(content []byte) string {
	h := sha256.New()
	h.Write(content)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
