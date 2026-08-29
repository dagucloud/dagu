// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// SyncStatus represents the synchronization status of a tracked item.
type SyncStatus string

const (
	// StatusSynced indicates the item is in sync with remote.
	StatusSynced SyncStatus = "synced"

	// StatusModified indicates the item has local modifications.
	StatusModified SyncStatus = "modified"

	// StatusUntracked indicates the item exists only locally.
	StatusUntracked SyncStatus = "untracked"

	// StatusConflict indicates a conflict between local and remote versions.
	StatusConflict SyncStatus = "conflict"

	// StatusMissing indicates a previously tracked file is no longer on disk.
	StatusMissing SyncStatus = "missing"
)

const (
	wikiDir       = "wiki"
	legacyDocsDir = "docs"
	baseConfigID  = "base"

	// wikiPageAssetsDirName is the reserved subtree holding page attachments.
	wikiPageAssetsDirName = ".attachments"
)

// SyncItemKind identifies a supported Git Sync item type.
type SyncItemKind string

const (
	SyncItemKindDAG      SyncItemKind = "dag"
	SyncItemKindWikiPage SyncItemKind = "doc"
	SyncItemKindFile     SyncItemKind = "file"
	// SyncItemKindWikiPageAsset is a binary page attachment. Its ID keeps the
	// file extension so names inside one attachment
	// directory differ only by extension.
	SyncItemKindWikiPageAsset SyncItemKind = "doc-asset"
)

// SyncItemKindForID derives the item type from its normalized ID.
func SyncItemKindForID(id string) SyncItemKind {
	id = normalizeDAGIDSeparators(id)
	if hasWikiPrefix(id, wikiPageAssetsDirName+"/") {
		return SyncItemKindWikiPageAsset
	}
	if hasWikiPrefix(id, "") {
		return SyncItemKindWikiPage
	}
	return SyncItemKindDAG
}

func hasWikiPrefix(id, suffix string) bool {
	return strings.HasPrefix(id, wikiDir+"/"+suffix) ||
		strings.HasPrefix(id, legacyDocsDir+"/"+suffix)
}

func isWikiPageFile(id string) bool {
	return SyncItemKindForID(id) == SyncItemKindWikiPage
}

func isWikiPageAssetFile(id string) bool {
	return SyncItemKindForID(id) == SyncItemKindWikiPageAsset
}

func wikiRepoDirForID(id string) string {
	id = normalizeDAGIDSeparators(id)
	if strings.HasPrefix(id, legacyDocsDir+"/") {
		return legacyDocsDir
	}
	return wikiDir
}

func isBaseConfigID(id string) bool {
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

	// Items contains sync state keyed by normalized item ID.
	Items map[string]*SyncItemState `json:"dags"`
}

// SyncItemState represents the sync state for a single item.
type SyncItemState struct {
	// Status is the current sync status.
	Status SyncStatus `json:"status"`

	// Kind identifies the tracked item type.
	Kind SyncItemKind `json:"kind,omitempty"`

	// FileExtension is the extension used by the tracked file.
	FileExtension string `json:"fileExtension,omitempty"`

	// BaseCommit is the commit hash when the item was last synced.
	BaseCommit string `json:"baseCommit,omitempty"`

	// LastSyncedHash is the content hash when the item was last synced.
	LastSyncedHash string `json:"lastSyncedHash,omitempty"`

	// LastSyncedAt is when the item was last synced.
	LastSyncedAt *time.Time `json:"lastSyncedAt,omitempty"`

	// ModifiedAt is when the item was last modified locally.
	ModifiedAt *time.Time `json:"modifiedAt,omitempty"`

	// LocalHash is the current local content hash.
	LocalHash string `json:"localHash,omitempty"`

	// RemoteCommit is the commit hash of the conflicting remote version.
	RemoteCommit string `json:"remoteCommit,omitempty"`

	// RemoteAuthor is the author of the conflicting remote commit.
	RemoteAuthor string `json:"remoteAuthor,omitempty"`

	// RemoteMessage is the commit message of the conflicting remote commit.
	RemoteMessage string `json:"remoteMessage,omitempty"`

	// LastSyncedExecutable is the executable bit at the last successful sync.
	LastSyncedExecutable bool `json:"lastSyncedExecutable,omitempty"`

	// LocalExecutable is the current local executable bit.
	LocalExecutable bool `json:"localExecutable,omitempty"`

	// RemoteExecutable is the executable bit of a conflicting remote file.
	RemoteExecutable bool `json:"remoteExecutable,omitempty"`

	// RemoteDeleted indicates that a conflicting remote file was deleted.
	RemoteDeleted bool `json:"remoteDeleted,omitempty"`

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
				Items:   make(map[string]*SyncItemState),
			}
			return m.state, nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	if state.Items == nil {
		state.Items = make(map[string]*SyncItemState)
	}
	normalizeTrackedItems(&state)

	m.state = &state
	return m.state, nil
}

func normalizeTrackedItems(state *State) {
	for itemID, itemState := range state.Items {
		if itemState == nil {
			delete(state.Items, itemID)
			continue
		}
		switch itemState.Kind {
		case "":
			itemState.Kind = SyncItemKindForID(itemID)
		case SyncItemKindDAG, SyncItemKindWikiPage, SyncItemKindWikiPageAsset, SyncItemKindFile:
		default:
			delete(state.Items, itemID)
		}
	}
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
	hash, _ := computeContentHash(bytes.NewReader(content))
	return hash
}

func computeContentHash(reader io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, reader); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
