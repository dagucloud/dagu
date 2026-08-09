// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/wiki"
)

// Service defines the interface for Git sync operations.
type Service interface {
	// Pull fetches and merges changes from the remote repository.
	Pull(ctx context.Context) (*SyncResult, error)

	// Publish commits and pushes a single sync item to the remote.
	Publish(ctx context.Context, itemID, message string, force bool) (*SyncResult, error)

	// PublishAll commits and pushes the specified sync items.
	PublishAll(ctx context.Context, message string, itemIDs []string) (*SyncResult, error)

	// Discard discards local changes for a sync item.
	Discard(ctx context.Context, itemID string) error

	// GetStatus returns the overall sync status.
	GetStatus(ctx context.Context) (*OverallStatus, error)

	// GetSyncItemStatus returns the sync status for a specific item.
	GetSyncItemStatus(ctx context.Context, itemID string) (*SyncItemState, error)

	// GetSyncItemDiff returns the diff between local and remote versions of an item.
	GetSyncItemDiff(ctx context.Context, itemID string) (*SyncItemDiff, error)

	// Forget removes state entries for missing, untracked, or conflicting items.
	Forget(ctx context.Context, itemIDs []string) ([]string, error)

	// Cleanup removes all missing entries from state.
	Cleanup(ctx context.Context) ([]string, error)

	// Delete removes an item from remote, local disk, and state.
	Delete(ctx context.Context, itemID, message string, force bool) error

	// DeleteBatch removes multiple items from remote, local disk, and state in a single commit.
	DeleteBatch(ctx context.Context, itemIDs []string, message string, force bool) ([]string, error)

	// DeleteAllMissing removes all missing items from remote, local, and state.
	DeleteAllMissing(ctx context.Context, message string) ([]string, error)

	// Move atomically renames an item across local filesystem, remote repository, and sync state.
	Move(ctx context.Context, oldID, newID, message string, force bool) error

	// GetConfig returns the current configuration.
	GetConfig(ctx context.Context) (*Config, error)

	// UpdateConfig updates the configuration.
	UpdateConfig(ctx context.Context, cfg *Config) error

	// TestConnection tests the connection to the remote repository.
	TestConnection(ctx context.Context) (*ConnectionResult, error)

	// Start starts the auto-sync background worker.
	Start(ctx context.Context) error

	// Stop stops the auto-sync background worker.
	Stop() error
}

// SyncResult represents the result of a sync operation.
type SyncResult struct {
	Success   bool        `json:"success"`
	Message   string      `json:"message,omitempty"`
	Synced    []string    `json:"synced,omitempty"`
	Modified  []string    `json:"modified,omitempty"`
	Conflicts []string    `json:"conflicts,omitempty"`
	Errors    []SyncError `json:"errors,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// SyncError represents an error during sync.
type SyncError struct {
	ItemID  string `json:"dagId,omitempty"`
	Message string `json:"message"`
}

// OverallStatus represents the overall sync status.
type OverallStatus struct {
	Enabled        bool                      `json:"enabled"`
	Repository     string                    `json:"repository,omitempty"`
	Branch         string                    `json:"branch,omitempty"`
	Summary        SummaryStatus             `json:"summary"`
	LastSyncAt     *time.Time                `json:"lastSyncAt,omitempty"`
	LastSyncCommit string                    `json:"lastSyncCommit,omitempty"`
	LastSyncStatus string                    `json:"lastSyncStatus,omitempty"`
	LastError      *string                   `json:"lastError,omitempty"`
	Items          map[string]*SyncItemState `json:"dags,omitempty"`
	Counts         StatusCounts              `json:"counts"`
}

// SummaryStatus represents the summary status for the header badge.
type SummaryStatus string

const (
	SummarySynced   SummaryStatus = "synced"
	SummaryPending  SummaryStatus = "pending"
	SummaryConflict SummaryStatus = "conflict"
	SummaryMissing  SummaryStatus = "missing"
	SummaryError    SummaryStatus = "error"
)

// StatusCounts contains counts for each status type.
type StatusCounts struct {
	Synced    int `json:"synced"`
	Modified  int `json:"modified"`
	Untracked int `json:"untracked"`
	Conflict  int `json:"conflict"`
	Missing   int `json:"missing"`
}

// ConnectionResult represents the result of a connection test.
type ConnectionResult struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// SyncItemDiff represents the diff between local and remote versions of an item.
// Binary items carry sizes instead of content.
type SyncItemDiff struct {
	ItemID        string     `json:"dagId"`
	FileExtension string     `json:"fileExtension"`
	Status        SyncStatus `json:"status"`
	Binary        bool       `json:"binary,omitempty"`
	LocalContent  string     `json:"localContent"`
	RemoteContent string     `json:"remoteContent,omitempty"`
	LocalSize     *int64     `json:"localSize,omitempty"`
	RemoteSize    *int64     `json:"remoteSize,omitempty"`
	RemoteCommit  string     `json:"remoteCommit,omitempty"`
	RemoteAuthor  string     `json:"remoteAuthor,omitempty"`
	RemoteMessage string     `json:"remoteMessage,omitempty"`
}

const (
	dagYAMLExtension  = ".yaml"
	dagYMLExtension   = ".yml"
	wikiPageExtension = ".md"
)

func normalizeDAGFileExtension(extension string) string {
	if strings.EqualFold(extension, wikiPageExtension) {
		return wikiPageExtension
	}
	if strings.EqualFold(extension, dagYMLExtension) {
		return dagYMLExtension
	}
	return dagYAMLExtension
}

// serviceImpl implements the Service interface.
type serviceImpl struct {
	cfg          *Config
	dagsDir      string
	wikiDir      string
	repoWikiDir  string
	dataDir      string
	stateManager *StateManager
	gitClient    *GitClient
	mu           sync.Mutex
	stopCh       chan struct{}
	running      bool
}

// NewService creates a new Git sync service.
func NewService(cfg *Config, dagsDir, wikiPath, dataDir string) Service {
	repoPath := filepath.Join(dataDir, "gitsync", "repo")
	if wikiPath == "" {
		wikiPath = filepath.Join(dagsDir, wikiDir)
	}
	return &serviceImpl{
		cfg:          cfg,
		dagsDir:      dagsDir,
		wikiDir:      wikiPath,
		repoWikiDir:  wikiDir,
		dataDir:      dataDir,
		stateManager: NewStateManager(dataDir),
		gitClient:    NewGitClient(cfg, repoPath),
	}
}

// Pull fetches and merges changes from the remote repository.
func (s *serviceImpl) Pull(ctx context.Context) (*SyncResult, error) {
	if err := s.validateEnabled(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	result := &SyncResult{Timestamp: time.Now()}

	// Ensure repo is cloned and opened
	if err := s.ensureRepoReady(ctx); err != nil {
		result.Success = false
		result.Message = "Failed to prepare repository"
		result.Errors = append(result.Errors, SyncError{Message: err.Error()})
		s.updateLastSyncError(err)
		return result, err
	}

	// Fetch and pull
	pullResult, err := s.gitClient.Pull(ctx)
	if err != nil {
		result.Success = false
		result.Message = "Failed to pull changes"
		result.Errors = append(result.Errors, SyncError{Message: err.Error()})
		s.updateLastSyncError(err)
		return result, err
	}

	// Get current commit
	currentCommit, _ := s.gitClient.GetHeadCommit()

	// Sync repository files to local storage and save their metadata.
	syncedItems, conflicts, err := s.syncFilesToLocal(ctx, pullResult, currentCommit)
	if err != nil {
		result.Success = false
		result.Message = "Failed to sync files"
		result.Errors = append(result.Errors, SyncError{Message: err.Error()})
		s.updateLastSyncError(err)
		return result, err
	}

	result.Synced = syncedItems
	result.Conflicts = conflicts
	result.Success = true
	result.Message = s.buildPullMessage(pullResult.AlreadyUpToDate, syncedItems, conflicts)

	return result, nil
}

// syncFilesToLocal syncs repository files to their local storage roots.
// It updates sync metadata and saves state in a single write.
func (s *serviceImpl) syncFilesToLocal(_ context.Context, pullResult *PullResult, commitHash string) ([]string, []string, error) {
	var synced []string
	var conflicts []string

	extensions := []string{dagYAMLExtension, dagYMLExtension, wikiPageExtension}
	files, err := s.gitClient.ListFiles(extensions)
	if err != nil {
		return nil, nil, err
	}
	// Page attachments are extension-agnostic; list their subtree separately.
	assetFiles, err := s.gitClient.ListFilesUnder(path.Join(s.repoWikiDir, wikiPageAssetsDirName))
	if err != nil {
		return nil, nil, err
	}
	files = append(files, assetFiles...)

	state, _ := s.stateManager.GetState()
	s.ensureSyncItemFileExtensions(state)

	// Reconcile: detect missing/reappeared files before processing
	s.reconcile(state)

	// Refresh hashes to detect local modifications before checking for conflicts
	s.refreshLocalHashes(state)

	// Build the set of item IDs present in the remote repository.
	repoFileSet := make(map[string]struct{}, len(files))
	repoFileExtensions := make(map[string]string, len(files))
	for _, file := range files {
		dagID := s.filePathToDAGID(file)
		if !isSyncableRepoFile(file, dagID) {
			continue
		}
		repoFileSet[dagID] = struct{}{}
		if isWikiPageAssetFile(dagID) {
			// Asset IDs keep their extension, so extension bookkeeping and
			// the duplicate-extension guard do not apply.
			continue
		}
		fileExtension := normalizeDAGFileExtension(path.Ext(file))
		if existingExtension, exists := repoFileExtensions[dagID]; exists && existingExtension != fileExtension {
			return nil, nil, &ValidationError{
				Field:   dagID,
				Message: "DAG exists with both .yaml and .yml extensions",
			}
		}
		repoFileExtensions[dagID] = fileExtension
	}

	for _, file := range files {
		dagID := s.filePathToDAGID(file)
		fileExtension := ""
		if !isWikiPageAssetFile(dagID) {
			fileExtension = normalizeDAGFileExtension(path.Ext(file))
		}

		if !isSyncableRepoFile(file, dagID) {
			continue
		}
		repoFilePath, err := s.safeRepoPathToFilePath(file)
		if err != nil {
			continue
		}

		dagState := state.Items[dagID]
		// Unchanged fast path: the item was synced against this exact commit
		// and refreshLocalHashes above found no local drift, so neither side
		// needs to be read. This keeps pulls from re-reading every file
		// (binary attachments in particular) on each auto-sync cycle.
		if dagState != nil && dagState.Status == StatusSynced && dagState.BaseCommit == pullResult.CurrentCommit {
			continue
		}
		localExtension := s.syncItemFileExtension(dagID, dagState)
		localFileExtension := fileExtension
		if isWikiPageAssetFile(dagID) {
			localFileExtension = ""
		} else if isWikiPageFile(dagID) && strings.EqualFold(localExtension, fileExtension) {
			localFileExtension = localExtension
		} else if localExtension != fileExtension {
			if err := s.migrateLocalDAGExtension(dagID, localExtension, fileExtension); err != nil {
				return nil, nil, err
			}
			if dagState != nil {
				dagState.FileExtension = fileExtension
			}
		}

		dagFilePath, err := s.safeDAGIDToFilePath(dagID, localFileExtension)
		if err != nil {
			continue
		}

		repoContent, err := safeReadFileWithinBase(s.gitClient.repoPath, repoFilePath)
		if err != nil {
			continue
		}
		repoHash := ComputeContentHash(repoContent)

		// Check if local file exists
		localContent, err := s.readDAGFile(dagID, dagFilePath)

		if err != nil {
			// Before creating a new local file, check if this content matches
			// a missing item's hash (prevents duplicates after move+pull).
			// Only same-kind entries qualify: identical bytes across kinds
			// must not forget an unrelated item.
			for otherID, otherState := range state.Items {
				if otherID != dagID &&
					otherState.Status == StatusMissing &&
					otherState.LastSyncedHash == repoHash &&
					SyncItemKindForID(otherID) == SyncItemKindForID(dagID) {
					// Auto-forget the stale missing entry
					delete(state.Items, otherID)
					break
				}
			}

			// Local file doesn't exist, create it
			if err := s.writeDAGFile(dagID, dagFilePath, repoContent); err != nil {
				return nil, nil, fmt.Errorf("failed to write synced item %q: %w", dagID, err)
			}
			now := time.Now()
			newState := &SyncItemState{
				Status:         StatusSynced,
				Kind:           SyncItemKindForID(dagID),
				FileExtension:  localFileExtension,
				BaseCommit:     pullResult.CurrentCommit,
				LastSyncedHash: repoHash,
				LastSyncedAt:   &now,
				LocalHash:      repoHash,
				ModifiedAt:     &now, // Added ModifiedAt for new files
			}
			if fi, err := os.Stat(dagFilePath); err == nil {
				updateStatCache(newState, fi)
			}
			state.Items[dagID] = newState
			synced = append(synced, dagID)
			continue
		}

		localHash := ComputeContentHash(localContent)

		// If local and remote content already match, ensure state reflects synced.
		if localHash == repoHash {
			if dagState == nil || dagState.Status != StatusSynced || dagState.BaseCommit != pullResult.CurrentCommit || dagState.LastSyncedHash != repoHash {
				now := time.Now()
				newState := &SyncItemState{
					Status:         StatusSynced,
					Kind:           SyncItemKindForID(dagID),
					FileExtension:  localFileExtension,
					BaseCommit:     pullResult.CurrentCommit,
					LastSyncedHash: repoHash,
					LastSyncedAt:   &now,
					LocalHash:      repoHash,
				}
				if fi, err := os.Stat(dagFilePath); err == nil {
					updateStatCache(newState, fi)
				}
				state.Items[dagID] = newState
				synced = append(synced, dagID)
			}
			continue
		}

		// Check for locally modified files
		if dagState != nil && dagState.Status == StatusModified {
			// Local was modified, check if remote also changed
			if dagState.LastSyncedHash != repoHash {
				// Both local and remote changed - conflict
				var remoteAuthor, remoteMessage string
				if commitInfo, err := s.gitClient.GetCommitInfo(pullResult.CurrentCommit); err == nil && commitInfo != nil {
					remoteAuthor = commitInfo.Author
					remoteMessage = commitInfo.Message
				}
				now := time.Now()
				state.Items[dagID] = &SyncItemState{
					Status:             StatusConflict,
					Kind:               SyncItemKindForID(dagID),
					FileExtension:      localFileExtension,
					BaseCommit:         dagState.BaseCommit,
					LastSyncedHash:     dagState.LastSyncedHash,
					LastSyncedAt:       dagState.LastSyncedAt,
					LocalHash:          localHash,
					RemoteCommit:       pullResult.CurrentCommit,
					RemoteAuthor:       remoteAuthor,
					RemoteMessage:      remoteMessage,
					ConflictDetectedAt: &now,
				}
				if fi, err := os.Stat(dagFilePath); err == nil {
					updateStatCache(state.Items[dagID], fi)
				}
				conflicts = append(conflicts, dagID)
			}
			// Local modified but remote unchanged - preserve local changes
			continue
		}

		// Only update local file if remote changed (and local wasn't modified)
		if localHash != repoHash {
			if err := s.writeDAGFile(dagID, dagFilePath, repoContent); err != nil {
				return nil, nil, fmt.Errorf("failed to write synced item %q: %w", dagID, err)
			}
			now := time.Now()
			newState := &SyncItemState{
				Status:         StatusSynced,
				Kind:           SyncItemKindForID(dagID),
				FileExtension:  localFileExtension,
				BaseCommit:     pullResult.CurrentCommit,
				LastSyncedHash: repoHash,
				LastSyncedAt:   &now,
				LocalHash:      repoHash,
			}
			if fi, err := os.Stat(dagFilePath); err == nil {
				updateStatCache(newState, fi)
			}
			state.Items[dagID] = newState
			synced = append(synced, dagID)
		}
	}

	// Auto-forget items absent from both remote and local storage.
	s.reconcileAfterPull(state, repoFileSet)

	// Scan for local items not in the repository.
	_ = s.scanLocalItems(state)

	// Update sync metadata and save state in a single write
	s.updateSuccessStateWithCommit(state, commitHash)

	return synced, conflicts, nil
}

// reconcileAfterPull removes state entries for items that are absent from both
// the remote repository and the local filesystem (auto-forget on pull).
func (s *serviceImpl) reconcileAfterPull(state *State, repoFileSet map[string]struct{}) {
	var toDelete []string
	for dagID, dagState := range state.Items {
		// Untracked items are local-only by definition.
		if dagState.Status == StatusUntracked {
			continue
		}

		// Keep items present in the remote repository.
		if _, inRepo := repoFileSet[dagID]; inRepo {
			continue
		}

		// Check if local file exists
		filePath, err := s.safeDAGIDToFilePath(dagID, dagState.FileExtension)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filePath); err == nil {
			continue // file exists locally
		}

		// Forget items absent from both locations.
		toDelete = append(toDelete, dagID)
	}

	for _, dagID := range toDelete {
		delete(state.Items, dagID)
	}
}

// scanLocalItems marks local DAGs and Wiki pages missing from state as untracked.
func (s *serviceImpl) scanLocalItems(state *State) error {
	extensions := map[string]bool{dagYAMLExtension: true, dagYMLExtension: true}

	entries, err := os.ReadDir(s.dagsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // DAGs directory doesn't exist yet
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		ext := filepath.Ext(entry.Name())
		if !extensions[ext] {
			continue
		}

		dagID := strings.TrimSuffix(entry.Name(), ext)
		if isBaseConfigID(dagID) {
			continue
		}

		// Skip if already tracked
		if _, exists := state.Items[dagID]; exists {
			continue
		}

		// Read local file to compute hash
		filePath, err := safeJoinWithinBase(s.dagsDir, entry.Name())
		if err != nil {
			continue
		}
		content, err := safeReadFileWithinBase(s.dagsDir, filePath)
		if err != nil {
			continue
		}

		now := time.Now()
		ds := &SyncItemState{
			Status:        StatusUntracked,
			Kind:          SyncItemKindDAG,
			FileExtension: normalizeDAGFileExtension(ext),
			LocalHash:     ComputeContentHash(content),
			ModifiedAt:    &now,
		}
		if fi, err := os.Stat(filePath); err == nil {
			updateStatCache(ds, fi)
		}
		state.Items[dagID] = ds
	}

	s.scanWikiPageFiles(state)

	return nil
}

func (s *serviceImpl) scanWikiPageFiles(state *State) {
	wikiRoot := s.localWikiDir()
	_ = filepath.WalkDir(wikiRoot, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && entry.IsDir() && entry.Name() == wikiPageAssetsDirName {
			// The attachment subtree belongs to the page-asset scanner.
			return filepath.SkipDir
		}
		ext := filepath.Ext(filePath)
		if walkErr != nil || entry.IsDir() || !strings.EqualFold(ext, wikiPageExtension) {
			return nil
		}
		relPath, err := filepath.Rel(wikiRoot, filePath)
		if err != nil {
			return nil
		}
		itemID := path.Join(s.repoWikiDir, strings.TrimSuffix(filepath.ToSlash(relPath), ext))
		if _, exists := state.Items[itemID]; exists {
			return nil
		}
		content, err := safeReadFileWithinBase(wikiRoot, filePath)
		if err != nil {
			return nil
		}
		now := time.Now()
		itemState := &SyncItemState{
			Status:        StatusUntracked,
			Kind:          SyncItemKindWikiPage,
			FileExtension: ext,
			LocalHash:     ComputeContentHash(content),
			ModifiedAt:    &now,
		}
		if info, err := os.Stat(filePath); err == nil {
			updateStatCache(itemState, info)
		}
		state.Items[itemID] = itemState
		return nil
	})
	s.scanWikiPageAssetFiles(state)
}

// isValidAssetItemID reports whether an asset item ID names a valid
// attachment location under the Wiki repository root.
func isValidAssetItemID(itemID string) bool {
	normalized := normalizeDAGIDSeparators(itemID)
	rel := strings.TrimPrefix(normalized, wikiRepoDirForID(normalized)+"/"+wikiPageAssetsDirName+"/")
	if rel == normalized {
		return false
	}
	idx := strings.LastIndex(rel, "/")
	if idx <= 0 {
		return false
	}
	wikiPageID, name := rel[:idx], rel[idx+1:]
	if wiki.ValidatePageID(wikiPageID) != nil {
		return false
	}
	return wiki.ValidateAttachmentName(name) == nil
}

// scanWikiPageAssetFiles registers untracked Wiki page attachments.
func (s *serviceImpl) scanWikiPageAssetFiles(state *State) {
	assetDir := filepath.Join(s.localWikiDir(), wikiPageAssetsDirName)
	_ = filepath.WalkDir(assetDir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(s.localWikiDir(), filePath)
		if err != nil {
			return nil
		}
		itemID := path.Join(s.repoWikiDir, filepath.ToSlash(relPath))
		if !isValidAssetItemID(itemID) {
			return nil
		}
		if _, exists := state.Items[itemID]; exists {
			return nil
		}
		content, err := safeReadFileWithinBase(s.localWikiDir(), filePath)
		if err != nil {
			return nil
		}
		now := time.Now()
		itemState := &SyncItemState{
			Status:     StatusUntracked,
			Kind:       SyncItemKindWikiPageAsset,
			LocalHash:  ComputeContentHash(content),
			ModifiedAt: &now,
		}
		if info, err := os.Stat(filePath); err == nil {
			updateStatCache(itemState, info)
		}
		state.Items[itemID] = itemState
		return nil
	})
}

// refreshLocalHashes recalculates hashes for tracked items and updates modified status.
func (s *serviceImpl) refreshLocalHashes(state *State) bool {
	changed := false
	for dagID, dagState := range state.Items {
		// Skip untracked (no remote to compare), conflict (already detected), and missing (file absent)
		if dagState.Status == StatusUntracked || dagState.Status == StatusConflict || dagState.Status == StatusMissing {
			continue
		}

		// Read current local file
		filePath, err := s.safeDAGIDToFilePath(dagID, dagState.FileExtension)
		if err != nil {
			continue
		}

		// Stat-before-hash: skip expensive read+hash if mtime+size unchanged
		info, statErr := os.Stat(filePath)
		if statErr != nil {
			// File might be deleted, skip for now
			continue
		}

		if statMatchesCache(dagState, info) {
			continue
		}

		content, err := os.ReadFile(filePath) //nolint:gosec // path constructed from internal dagsDir
		if err != nil {
			continue
		}

		currentHash := ComputeContentHash(content)
		updateStatCache(dagState, info)

		// Update LocalHash if changed
		if dagState.LocalHash != currentHash {
			dagState.LocalHash = currentHash
			changed = true
		}

		// Check if status should change
		if dagState.Status == StatusSynced && currentHash != dagState.LastSyncedHash {
			dagState.Status = StatusModified
			dagState.ModifiedAt = new(time.Now())
			changed = true
		} else if dagState.Status == StatusModified && currentHash == dagState.LastSyncedHash {
			// User reverted changes manually - back to synced
			dagState.Status = StatusSynced
			changed = true
		}
	}
	return changed
}

// updateStatCache updates the stat cache fields on a SyncItemState from file info.
func updateStatCache(dagState *SyncItemState, info os.FileInfo) {
	modTime := info.ModTime()
	size := info.Size()
	dagState.LastStatModTime = &modTime
	dagState.LastStatSize = &size
}

// statMatchesCache returns true if the file info matches the cached stat values.
func statMatchesCache(dagState *SyncItemState, info os.FileInfo) bool {
	if dagState.LastStatModTime == nil || dagState.LastStatSize == nil {
		return false
	}
	return info.ModTime().Equal(*dagState.LastStatModTime) && info.Size() == *dagState.LastStatSize
}

// reconcile detects missing files and reappeared files, updating state accordingly.
// Returns true if any state was changed.
func (s *serviceImpl) reconcile(state *State) bool {
	changed := false
	var toDelete []string

	for dagID, dagState := range state.Items {
		filePath, err := s.safeDAGIDToFilePath(dagID, dagState.FileExtension)
		if err != nil {
			continue
		}

		_, statErr := os.Stat(filePath)
		fileExists := statErr == nil

		switch dagState.Status {
		case StatusMissing:
			if fileExists {
				// File reappeared — hash it and decide new status
				content, err := os.ReadFile(filePath) //nolint:gosec // path constructed from internal dagsDir
				if err != nil {
					continue
				}
				currentHash := ComputeContentHash(content)
				if currentHash == dagState.LastSyncedHash {
					dagState.Status = StatusSynced
				} else {
					dagState.Status = StatusModified
					now := time.Now()
					dagState.ModifiedAt = &now
				}
				dagState.LocalHash = currentHash
				dagState.PreviousStatus = ""
				dagState.MissingAt = nil
				changed = true
			}

		case StatusUntracked:
			if !fileExists {
				// Untracked file deleted — remove entry entirely
				toDelete = append(toDelete, dagID)
				changed = true
			}

		case StatusSynced, StatusModified, StatusConflict:
			if !fileExists {
				// Tracked file disappeared — mark as missing
				now := time.Now()
				dagState.PreviousStatus = string(dagState.Status)
				dagState.MissingAt = &now
				dagState.Status = StatusMissing
				changed = true
			}
		}
	}

	for _, dagID := range toDelete {
		delete(state.Items, dagID)
	}

	return changed
}

// Publish commits and pushes a single sync item to the remote.
func (s *serviceImpl) Publish(ctx context.Context, dagID, message string, force bool) (*SyncResult, error) {
	if err := s.validatePushEnabled(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	result := &SyncResult{Timestamp: time.Now()}

	state, err := s.stateManager.GetState()
	if err != nil {
		return nil, err
	}

	dagState := state.Items[dagID]
	if dagState == nil {
		return nil, &DAGNotFoundError{DAGID: dagID}
	}

	if err := s.validatePublishable(dagState, dagID, force); err != nil {
		return nil, err
	}

	fileExtension := s.syncItemFileExtension(dagID, dagState)
	dagFilePath, err := s.safeDAGIDToFilePath(dagID, fileExtension)
	if err != nil {
		return nil, err
	}
	repoFilePath, err := s.safeDAGIDToRepoPath(dagID, fileExtension)
	if err != nil {
		return nil, err
	}
	repoAbsPath := s.gitClient.GetFilePath(repoFilePath)

	if err := s.gitClient.Open(); err != nil {
		return nil, err
	}

	content, err := os.ReadFile(dagFilePath) //nolint:gosec // path constructed from internal dagsDir
	if err != nil {
		return nil, fmt.Errorf("failed to read sync item file: %w", err)
	}

	if err := safeWriteFileWithinBase(s.gitClient.repoPath, repoAbsPath, content, 0600); err != nil {
		return nil, fmt.Errorf("failed to write to repo: %w", err)
	}

	// Commit
	if message == "" {
		message = fmt.Sprintf("Update %s", dagID)
	}
	commitHash, err := s.gitClient.AddAndCommit(repoFilePath, message)
	if err != nil {
		return nil, err
	}

	if err := s.gitClient.Push(ctx); err != nil {
		return nil, err
	}

	// Update the item state to synced.
	contentHash := ComputeContentHash(content)
	newState := s.newSyncedItemState(dagID, fileExtension, commitHash, contentHash)
	if fi, err := os.Stat(dagFilePath); err == nil {
		updateStatCache(newState, fi)
	}
	state.Items[dagID] = newState
	s.updateSuccessStateWithCommit(state, commitHash)

	result.Success = true
	result.Message = fmt.Sprintf("Published %s", dagID)
	result.Synced = []string{dagID}

	return result, nil
}

// PublishAll commits and pushes the specified sync items.
func (s *serviceImpl) PublishAll(ctx context.Context, message string, dagIDs []string) (*SyncResult, error) {
	if err := s.validatePushEnabled(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	result := &SyncResult{Timestamp: time.Now()}

	state, err := s.stateManager.GetState()
	if err != nil {
		return nil, err
	}

	publishTargets, err := s.resolvePublishTargets(state, dagIDs)
	if err != nil {
		return nil, err
	}

	if err := s.gitClient.Open(); err != nil {
		return nil, err
	}

	// Copy files and track which succeeded
	successfulDAGs := make([]string, 0, len(publishTargets))
	stagedFiles := make([]string, 0, len(publishTargets))

	for _, dagID := range publishTargets {
		dagState := state.Items[dagID]
		fileExtension := s.syncItemFileExtension(dagID, dagState)
		dagFilePath, err := s.safeDAGIDToFilePath(dagID, fileExtension)
		if err != nil {
			return nil, err
		}
		repoFilePath, err := s.safeDAGIDToRepoPath(dagID, fileExtension)
		if err != nil {
			return nil, err
		}
		repoAbsPath := s.gitClient.GetFilePath(repoFilePath)

		content, err := os.ReadFile(dagFilePath) //nolint:gosec // path constructed from internal dagsDir
		if err != nil {
			result.Errors = append(result.Errors, SyncError{ItemID: dagID, Message: err.Error()})
			continue
		}

		if err := safeWriteFileWithinBase(s.gitClient.repoPath, repoAbsPath, content, 0600); err != nil {
			result.Errors = append(result.Errors, SyncError{ItemID: dagID, Message: err.Error()})
			continue
		}

		successfulDAGs = append(successfulDAGs, dagID)
		stagedFiles = append(stagedFiles, repoFilePath)
	}

	// Check if any files were successfully staged
	if len(successfulDAGs) == 0 {
		return nil, fmt.Errorf("all files failed to copy: %d error(s)", len(result.Errors))
	}

	// Stage only the successful files
	wt, err := s.gitClient.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}
	for _, file := range stagedFiles {
		if _, err := wt.Add(file); err != nil {
			return nil, fmt.Errorf("failed to stage file %s: %w", file, err)
		}
	}

	// Commit staged files only (do not restage ".")
	if message == "" {
		message = fmt.Sprintf("Update %d sync item(s)", len(successfulDAGs))
	}
	commitHash, err := s.gitClient.CommitStaged(message)
	if err != nil {
		return nil, err
	}

	// Push
	if err := s.gitClient.Push(ctx); err != nil {
		return nil, err
	}

	// Update state only for successfully published items.
	for _, dagID := range successfulDAGs {
		fileExtension := s.syncItemFileExtension(dagID, state.Items[dagID])
		dagFilePath, err := s.safeDAGIDToFilePath(dagID, fileExtension)
		if err != nil {
			return nil, err
		}
		content, _ := os.ReadFile(dagFilePath) //nolint:gosec // path constructed from internal dagsDir
		contentHash := ComputeContentHash(content)
		newState := s.newSyncedItemState(dagID, fileExtension, commitHash, contentHash)
		if fi, err := os.Stat(dagFilePath); err == nil {
			updateStatCache(newState, fi)
		}
		state.Items[dagID] = newState
		result.Synced = append(result.Synced, dagID)
	}

	s.updateSuccessStateWithCommit(state, commitHash)

	result.Success = true
	result.Message = fmt.Sprintf("Published %d sync item(s)", len(result.Synced))

	return result, nil
}

// Discard discards local changes for a sync item.
func (s *serviceImpl) Discard(_ context.Context, dagID string) error {
	if err := s.validateEnabled(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.stateManager.GetState()
	if err != nil {
		return err
	}

	dagState := state.Items[dagID]
	if dagState == nil {
		return &DAGNotFoundError{DAGID: dagID}
	}

	// Open repo
	if err := s.gitClient.Open(); err != nil {
		return err
	}

	fileExtension := s.syncItemFileExtension(dagID, dagState)
	repoFilePath, err := s.safeDAGIDToRepoPath(dagID, fileExtension)
	if err != nil {
		return err
	}
	dagFilePath, err := s.safeDAGIDToFilePath(dagID, fileExtension)
	if err != nil {
		return err
	}

	repoFileFullPath, err := s.safeRepoPathToFilePath(repoFilePath)
	if err != nil {
		return err
	}
	repoContent, err := safeReadFileWithinBase(s.gitClient.repoPath, repoFileFullPath)
	if err != nil {
		return fmt.Errorf("failed to read repo file: %w", err)
	}

	// Restore the local item from the repository.
	if err := s.writeDAGFile(dagID, dagFilePath, repoContent); err != nil {
		return fmt.Errorf("failed to write sync item file: %w", err)
	}

	// Update state
	contentHash := ComputeContentHash(repoContent)
	newState := s.newSyncedItemState(dagID, fileExtension, dagState.BaseCommit, contentHash)
	if fi, err := os.Stat(dagFilePath); err == nil {
		updateStatCache(newState, fi)
	}
	state.Items[dagID] = newState
	_ = s.stateManager.Save(state) // Best effort - discard was successful, state will sync on next operation

	return nil
}

// Forget removes state entries for missing, untracked, or conflicting items.
// Items in synced or modified status are rejected.
func (s *serviceImpl) Forget(_ context.Context, itemIDs []string) ([]string, error) {
	if err := s.validateEnabled(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.stateManager.GetState()
	if err != nil {
		return nil, err
	}

	// Phase 1: validate all IDs before mutating state.
	var toForget []string
	for _, itemID := range itemIDs {
		dagState, exists := state.Items[itemID]
		if !exists {
			return nil, &DAGNotFoundError{DAGID: itemID}
		}

		switch dagState.Status {
		case StatusSynced, StatusModified:
			return nil, fmt.Errorf("%w: %q is %s — only missing, untracked, or conflicting sync items can be forgotten",
				ErrCannotForget, itemID, dagState.Status)
		case StatusMissing, StatusUntracked, StatusConflict:
			toForget = append(toForget, itemID)
		}
	}

	// Phase 2: delete all validated entries.
	var forgotten []string
	for _, itemID := range toForget {
		delete(state.Items, itemID)
		forgotten = append(forgotten, itemID)
	}

	if len(forgotten) > 0 {
		if err := s.stateManager.Save(state); err != nil {
			return nil, fmt.Errorf("failed to save state: %w", err)
		}
	}

	return forgotten, nil
}

// Cleanup removes all missing entries from state.
func (s *serviceImpl) Cleanup(_ context.Context) ([]string, error) {
	if err := s.validateEnabled(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.stateManager.GetState()
	if err != nil {
		return nil, err
	}

	var forgotten []string
	for dagID, dagState := range state.Items {
		if dagState.Status == StatusMissing {
			delete(state.Items, dagID)
			forgotten = append(forgotten, dagID)
		}
	}

	if len(forgotten) > 0 {
		sort.Strings(forgotten)
		if err := s.stateManager.Save(state); err != nil {
			return nil, fmt.Errorf("failed to save state: %w", err)
		}
	}

	return forgotten, nil
}

// Delete removes a sync item from remote, local storage, and state.
func (s *serviceImpl) Delete(ctx context.Context, itemID, message string, force bool) error {
	if err := s.validatePushEnabled(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.stateManager.GetState()
	if err != nil {
		return err
	}

	dagState, exists := state.Items[itemID]
	if !exists {
		return &DAGNotFoundError{DAGID: itemID}
	}

	// Reject untracked — use forget instead
	if dagState.Status == StatusUntracked {
		return ErrCannotDeleteUntracked
	}

	// Reject modified without force
	if dagState.Status == StatusModified && !force {
		return &ValidationError{
			Field:   itemID,
			Message: "sync item has local modifications — use force to delete anyway",
		}
	}

	// Ensure repo is ready
	if err := s.gitClient.Open(); err != nil {
		return err
	}

	// Delete local file if it exists
	fileExtension := s.syncItemFileExtension(itemID, dagState)
	localPath, err := s.safeDAGIDToFilePath(itemID, fileExtension)
	if err != nil {
		return err
	}
	if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove local file %q: %w", itemID, err)
	}

	// Stage removal in repo
	repoPath, err := s.safeDAGIDToRepoPath(itemID, fileExtension)
	if err != nil {
		return err
	}

	// For missing items the file won't exist in the repo — ignore that error.
	// For other statuses, a real staging failure should be surfaced.
	if err := s.gitClient.RemoveFile(repoPath); err != nil && dagState.Status != StatusMissing {
		return fmt.Errorf("failed to stage removal of %q: %w", itemID, err)
	}

	// Commit and push
	if message == "" {
		message = fmt.Sprintf("Delete %s", itemID)
	}
	commitHash, err := s.gitClient.CommitStaged(message)
	if err != nil {
		return err
	}

	if err := s.gitClient.Push(ctx); err != nil {
		// Push failed — preserve entry for retry
		return err
	}

	// On success: delete state entry
	delete(state.Items, itemID)
	s.updateSuccessStateWithCommit(state, commitHash)

	return nil
}

// DeleteBatch removes multiple items from remote, local disk, and state in a single commit.
func (s *serviceImpl) DeleteBatch(ctx context.Context, itemIDs []string, message string, force bool) ([]string, error) {
	if err := s.validatePushEnabled(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.stateManager.GetState()
	if err != nil {
		return nil, err
	}

	// Phase 1: validate and de-duplicate all items before any mutation.
	type deleteTarget struct {
		itemID    string
		status    SyncStatus
		localPath string
		repoPath  string
	}
	var targets []deleteTarget
	seen := make(map[string]struct{}, len(itemIDs))

	for _, itemID := range itemIDs {
		if _, dup := seen[itemID]; dup {
			continue
		}
		seen[itemID] = struct{}{}

		dagState, exists := state.Items[itemID]
		if !exists {
			return nil, &DAGNotFoundError{DAGID: itemID}
		}

		if dagState.Status == StatusUntracked {
			return nil, ErrCannotDeleteUntracked
		}

		if (dagState.Status == StatusModified || dagState.Status == StatusConflict) && !force {
			return nil, &ValidationError{
				Field:   itemID,
				Message: "sync item has local modifications — use force to delete anyway",
			}
		}

		fileExtension := s.syncItemFileExtension(itemID, dagState)
		localPath, err := s.safeDAGIDToFilePath(itemID, fileExtension)
		if err != nil {
			return nil, err
		}
		repoPath, err := s.safeDAGIDToRepoPath(itemID, fileExtension)
		if err != nil {
			return nil, err
		}

		targets = append(targets, deleteTarget{
			itemID:    itemID,
			status:    dagState.Status,
			localPath: localPath,
			repoPath:  repoPath,
		})
	}

	if len(targets) == 0 {
		return nil, nil
	}

	// Phase 2: execute deletion.
	if err := s.gitClient.Open(); err != nil {
		return nil, err
	}

	// Stage removals first so a staging failure does not already delete from disk.
	for _, t := range targets {
		if err := s.gitClient.RemoveFile(t.repoPath); err != nil && t.status != StatusMissing {
			return nil, fmt.Errorf("failed to stage removal of %q: %w", t.itemID, err)
		}
	}

	// Delete local files after staging succeeds.
	for _, t := range targets {
		if err := os.Remove(t.localPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to remove local file %q: %w", t.itemID, err)
		}
	}

	if message == "" {
		message = fmt.Sprintf("Delete %d sync item(s)", len(targets))
	}
	commitHash, err := s.gitClient.CommitStaged(message)
	if err != nil {
		return nil, err
	}

	if err := s.gitClient.Push(ctx); err != nil {
		return nil, err
	}

	// On success: delete all state entries.
	var deleted []string
	for _, t := range targets {
		delete(state.Items, t.itemID)
		deleted = append(deleted, t.itemID)
	}
	sort.Strings(deleted)
	s.updateSuccessStateWithCommit(state, commitHash)

	return deleted, nil
}

// DeleteAllMissing removes all missing items from remote, local storage, and state.
func (s *serviceImpl) DeleteAllMissing(ctx context.Context, message string) ([]string, error) {
	if err := s.validatePushEnabled(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.stateManager.GetState()
	if err != nil {
		return nil, err
	}

	// Collect missing items.
	var missingIDs []string
	var repoPaths []string
	for dagID, dagState := range state.Items {
		if dagState.Status != StatusMissing {
			continue
		}
		repoPath, err := s.safeDAGIDToRepoPath(dagID, s.syncItemFileExtension(dagID, dagState))
		if err != nil {
			continue
		}
		missingIDs = append(missingIDs, dagID)
		repoPaths = append(repoPaths, repoPath)
	}

	if len(missingIDs) == 0 {
		return nil, nil
	}
	sort.Strings(missingIDs)

	if err := s.gitClient.Open(); err != nil {
		return nil, err
	}

	// Stage all removals — all items here are missing by definition,
	// so files may not exist in the repo. Ignore errors from RemoveFiles.
	_ = s.gitClient.RemoveFiles(repoPaths)

	if message == "" {
		message = fmt.Sprintf("Delete %d missing sync item(s)", len(missingIDs))
	}
	commitHash, err := s.gitClient.CommitStaged(message)
	if err != nil {
		return nil, err
	}

	if err := s.gitClient.Push(ctx); err != nil {
		return nil, err
	}

	// On success: delete all entries
	for _, dagID := range missingIDs {
		delete(state.Items, dagID)
	}
	s.updateSuccessStateWithCommit(state, commitHash)

	return missingIDs, nil
}

// Move atomically renames an item across local storage, remote repository, and sync state.
func (s *serviceImpl) Move(ctx context.Context, oldID, newID, message string, force bool) error {
	if err := s.validatePushEnabled(); err != nil {
		return err
	}

	// Validate both IDs are canonical
	normalized, err := normalizeDAGID(oldID)
	if err != nil {
		return err
	}
	if normalized != oldID {
		return &InvalidDAGIDError{DAGID: oldID, Reason: fmt.Sprintf("must be normalized as %q", normalized)}
	}
	normalized, err = normalizeDAGID(newID)
	if err != nil {
		return err
	}
	if normalized != newID {
		return &InvalidDAGIDError{DAGID: newID, Reason: fmt.Sprintf("must be normalized as %q", normalized)}
	}
	if SyncItemKindForID(oldID) != SyncItemKindForID(newID) {
		return &ValidationError{Field: "newItemId", Message: "source and destination must have the same item type"}
	}
	if isWikiPageAssetFile(newID) && !isValidAssetItemID(newID) {
		return &ValidationError{Field: "newItemId", Message: "destination is not a valid attachment path"}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.stateManager.GetState()
	if err != nil {
		return err
	}

	oldState, exists := state.Items[oldID]
	if !exists {
		return &DAGNotFoundError{DAGID: oldID}
	}

	// Reject untracked source — not tracked in remote
	if oldState.Status == StatusUntracked {
		return &ValidationError{
			Field:   oldID,
			Message: "untracked sync items cannot be moved — publish first",
		}
	}

	// Reject conflict without force
	if oldState.Status == StatusConflict && !force {
		return &ConflictError{
			DAGID:         oldID,
			RemoteCommit:  oldState.RemoteCommit,
			RemoteAuthor:  oldState.RemoteAuthor,
			RemoteMessage: oldState.RemoteMessage,
		}
	}

	// Check destination is not already tracked (except untracked in retroactive mode)
	if destState, destExists := state.Items[newID]; destExists {
		if destState.Status != StatusUntracked {
			return &ValidationError{
				Field:   "newItemId",
				Message: fmt.Sprintf("destination %q is already tracked with status %s", newID, destState.Status),
			}
		}
	}

	// Resolve file paths
	fileExtension := s.syncItemFileExtension(oldID, oldState)
	oldLocalPath, err := s.safeDAGIDToFilePath(oldID, fileExtension)
	if err != nil {
		return err
	}
	newLocalPath, err := s.safeDAGIDToFilePath(newID, fileExtension)
	if err != nil {
		return err
	}
	oldRepoPath, err := s.safeDAGIDToRepoPath(oldID, fileExtension)
	if err != nil {
		return err
	}
	newRepoPath, err := s.safeDAGIDToRepoPath(newID, fileExtension)
	if err != nil {
		return err
	}

	// Determine mode: preemptive (old file exists) vs retroactive (old missing, new exists)
	_, oldFileErr := os.Stat(oldLocalPath)
	oldFileExists := oldFileErr == nil
	_, newFileErr := os.Stat(newLocalPath)
	newFileExists := newFileErr == nil

	// Validate that at least one mode is possible
	if !oldFileExists && (oldState.Status != StatusMissing || !newFileExists) {
		return &ValidationError{
			Field:   oldID,
			Message: "source file does not exist on disk and destination file is not present",
		}
	}

	// Ensure repo is ready
	if err := s.gitClient.Open(); err != nil {
		return err
	}

	var content []byte

	if oldFileExists {
		// Preemptive mode: source exists on disk
		content, err = os.ReadFile(oldLocalPath) //nolint:gosec // path constructed from internal dagsDir
		if err != nil {
			return fmt.Errorf("failed to read source file: %w", err)
		}
		if err := s.writeDAGFile(newID, newLocalPath, content); err != nil {
			return fmt.Errorf("failed to write destination file: %w", err)
		}
		if err := os.Remove(oldLocalPath); err != nil {
			if rollbackErr := os.Remove(newLocalPath); rollbackErr != nil {
				return fmt.Errorf("failed to remove source file: %w (destination rollback failed: %v)", err, rollbackErr)
			}
			return fmt.Errorf("failed to remove source file: %w", err)
		}
	} else {
		// Retroactive mode: old is missing but new file already exists at destination
		content, err = os.ReadFile(newLocalPath) //nolint:gosec // path constructed from internal dagsDir
		if err != nil {
			return fmt.Errorf("failed to read destination file: %w", err)
		}
	}

	// Stage changes in repo
	newRepoAbsPath := s.gitClient.GetFilePath(newRepoPath)
	if err := safeWriteFileWithinBase(s.gitClient.repoPath, newRepoAbsPath, content, 0600); err != nil {
		return fmt.Errorf("failed to write to repo: %w", err)
	}

	// Stage removal of old path — may not exist in repo for edge cases.
	_ = s.gitClient.RemoveFile(oldRepoPath)

	// Stage addition of new path
	if message == "" {
		message = fmt.Sprintf("Move %s to %s", oldID, newID)
	}
	commitHash, err := s.gitClient.AddAndCommit(newRepoPath, message)
	if err != nil {
		return err
	}

	if err := s.gitClient.Push(ctx); err != nil {
		// Push failed — preserve old state entry for reconciliation
		return err
	}

	// On success: update state
	contentHash := ComputeContentHash(content)
	newItemState := s.newSyncedItemState(newID, fileExtension, commitHash, contentHash)
	if fi, err := os.Stat(newLocalPath); err == nil {
		updateStatCache(newItemState, fi)
	}

	// If destination was untracked, remove the old untracked entry
	delete(state.Items, newID)
	// Remove old entry and add new
	delete(state.Items, oldID)
	state.Items[newID] = newItemState
	s.updateSuccessStateWithCommit(state, commitHash)

	return nil
}

// GetStatus returns the overall sync status.
func (s *serviceImpl) GetStatus(_ context.Context) (*OverallStatus, error) {
	status := &OverallStatus{
		Enabled: s.cfg.Enabled,
	}

	if !s.cfg.Enabled {
		return status, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	status.Repository = s.cfg.Repository
	status.Branch = s.cfg.Branch

	state, err := s.stateManager.GetState()
	if err != nil {
		status.Summary = SummaryError
		status.LastError = new(err.Error())
		return status, nil
	}
	extensionsChanged := s.ensureSyncItemFileExtensions(state)

	// Scan for new local items not yet tracked.
	prevCount := len(state.Items)
	_ = s.scanLocalItems(state)
	newItems := len(state.Items) > prevCount

	// Reconcile: detect missing/reappeared files
	reconciled := s.reconcile(state)

	// Refresh hashes for tracked items to detect local modifications.
	hashesChanged := s.refreshLocalHashes(state)

	// Save state if anything changed (best effort - read-only operation)
	if extensionsChanged || newItems || hashesChanged || reconciled {
		_ = s.stateManager.Save(state)
	}

	status.LastSyncAt = state.LastSyncAt
	status.LastSyncCommit = state.LastSyncCommit
	status.LastSyncStatus = state.LastSyncStatus
	status.LastError = state.LastError
	status.Items = cloneSyncItemStates(state.Items)

	status.Counts = computeStatusCounts(state.Items)

	// Determine summary status (priority: error > conflict > missing > pending > synced)
	if status.Counts.Conflict > 0 {
		status.Summary = SummaryConflict
	} else if status.Counts.Missing > 0 {
		status.Summary = SummaryMissing
	} else if status.Counts.Modified > 0 || status.Counts.Untracked > 0 {
		status.Summary = SummaryPending
	} else {
		status.Summary = SummarySynced
	}

	if state.LastError != nil {
		status.Summary = SummaryError
	}

	return status, nil
}

// GetSyncItemStatus returns the sync status for a specific item.
func (s *serviceImpl) GetSyncItemStatus(_ context.Context, itemID string) (*SyncItemState, error) {
	if err := s.validateEnabled(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.stateManager.GetState()
	if err != nil {
		return nil, err
	}

	itemState := state.Items[itemID]
	if itemState == nil {
		return nil, &DAGNotFoundError{DAGID: itemID}
	}

	previousExtension := itemState.FileExtension
	s.syncItemFileExtension(itemID, itemState)
	if itemState.FileExtension != previousExtension {
		_ = s.stateManager.Save(state)
	}

	stateCopy := *itemState
	return &stateCopy, nil
}

// GetSyncItemDiff returns the diff between local and remote versions of an item.
func (s *serviceImpl) GetSyncItemDiff(_ context.Context, itemID string) (*SyncItemDiff, error) {
	if err := s.validateEnabled(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.stateManager.GetState()
	if err != nil {
		return nil, err
	}

	itemState := state.Items[itemID]
	if itemState == nil {
		return nil, &DAGNotFoundError{DAGID: itemID}
	}

	diff := &SyncItemDiff{
		ItemID:        itemID,
		FileExtension: s.syncItemFileExtension(itemID, itemState),
		Status:        itemState.Status,
	}

	// Binary attachments never load content: sizes and commit metadata only.
	// This guard must precede every content-loading branch below.
	if isWikiPageAssetFile(itemID) {
		diff.Binary = true
		if localPath, err := s.safeDAGIDToFilePath(itemID, ""); err == nil {
			if info, err := os.Stat(localPath); err == nil {
				size := info.Size()
				diff.LocalSize = &size
			}
		}
		remoteCommit := itemState.BaseCommit
		if itemState.Status == StatusConflict {
			remoteCommit = itemState.RemoteCommit
			diff.RemoteAuthor = itemState.RemoteAuthor
			diff.RemoteMessage = itemState.RemoteMessage
		}
		if remoteCommit != "" {
			if err := s.gitClient.Open(); err != nil {
				return nil, fmt.Errorf("failed to open repository for binary diff: %w", err)
			}
			repoPath, err := s.safeDAGIDToRepoPath(itemID, "")
			if err != nil {
				return nil, err
			}
			size, err := s.gitClient.GetFileSizeAtCommit(repoPath, remoteCommit)
			if err != nil {
				return nil, fmt.Errorf("failed to read remote binary metadata: %w", err)
			}
			diff.RemoteSize = &size
			diff.RemoteCommit = remoteCommit
		}
		return diff, nil
	}

	// Missing items have no local file.
	if itemState.Status == StatusMissing {
		diff.LocalContent = ""
		diff.RemoteContent = s.fetchRemoteContent(itemID, diff.FileExtension, itemState.BaseCommit)
		diff.RemoteCommit = itemState.BaseCommit
		return diff, nil
	}

	localPath, err := s.safeDAGIDToFilePath(itemID, diff.FileExtension)
	if err != nil {
		return nil, err
	}
	localContent, err := os.ReadFile(localPath) //nolint:gosec // path constructed from internal dagsDir
	if err != nil {
		return nil, fmt.Errorf("failed to read local file: %w", err)
	}

	diff.LocalContent = string(localContent)

	switch itemState.Status {
	case StatusSynced:
		diff.RemoteContent = string(localContent)
		diff.RemoteCommit = itemState.BaseCommit

	case StatusModified:
		diff.RemoteContent = s.fetchRemoteContent(itemID, diff.FileExtension, itemState.BaseCommit)
		diff.RemoteCommit = itemState.BaseCommit

	case StatusConflict:
		diff.RemoteContent = s.fetchRemoteContent(itemID, diff.FileExtension, itemState.RemoteCommit)
		diff.RemoteCommit = itemState.RemoteCommit
		diff.RemoteAuthor = itemState.RemoteAuthor
		diff.RemoteMessage = itemState.RemoteMessage

	case StatusUntracked:
		// No remote version for untracked files

	case StatusMissing:
		// Handled above before reading local file
	}

	return diff, nil
}

func cloneSyncItemStates(states map[string]*SyncItemState) map[string]*SyncItemState {
	cloned := make(map[string]*SyncItemState, len(states))
	for itemID, itemState := range states {
		if itemState == nil {
			cloned[itemID] = nil
			continue
		}
		stateCopy := *itemState
		cloned[itemID] = &stateCopy
	}
	return cloned
}

// fetchRemoteContent retrieves item content from a specific commit.
func (s *serviceImpl) fetchRemoteContent(dagID, fileExtension, commitHash string) string {
	if commitHash == "" {
		return ""
	}
	if err := s.gitClient.Open(); err != nil {
		return ""
	}
	repoPath, err := s.safeDAGIDToRepoPath(dagID, fileExtension)
	if err != nil {
		return ""
	}
	content, err := s.gitClient.GetFileContentAtCommit(repoPath, commitHash)
	if err != nil {
		return ""
	}
	return string(content)
}

// GetConfig returns the current configuration.
func (s *serviceImpl) GetConfig(_ context.Context) (*Config, error) {
	return s.cfg, nil
}

// UpdateConfig updates the configuration.
func (s *serviceImpl) UpdateConfig(_ context.Context, cfg *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cfg = cfg
	s.gitClient = NewGitClient(cfg, filepath.Join(s.dataDir, "gitsync", "repo"))

	return nil
}

// TestConnection tests the connection to the remote repository.
func (s *serviceImpl) TestConnection(ctx context.Context) (*ConnectionResult, error) {
	if !s.cfg.Enabled {
		return &ConnectionResult{
			Success: false,
			Error:   "Git sync is not enabled",
		}, nil
	}

	if !s.cfg.IsValid() {
		return &ConnectionResult{
			Success: false,
			Error:   "Git sync configuration is invalid",
		}, nil
	}

	err := s.gitClient.TestConnection(ctx)
	if err != nil {
		return &ConnectionResult{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return &ConnectionResult{
		Success: true,
		Message: "Connection successful",
	}, nil
}

// Start starts the auto-sync background worker.
func (s *serviceImpl) Start(ctx context.Context) error {
	if !s.cfg.Enabled {
		return nil
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	// Initial sync on startup
	if s.cfg.AutoSync.OnStartup {
		_, _ = s.Pull(ctx)
	}

	// Start periodic sync if interval > 0
	if s.cfg.AutoSync.Enabled && s.cfg.AutoSync.Interval > 0 {
		go s.runAutoSync(ctx)
	}

	return nil
}

// Stop stops the auto-sync background worker.
func (s *serviceImpl) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	close(s.stopCh)
	s.running = false

	return nil
}

// runAutoSync runs the auto-sync loop.
func (s *serviceImpl) runAutoSync(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.cfg.AutoSync.Interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.Pull(ctx); err != nil {
				// We don't return error here to keep the loop running,
				// but the error is already updated in s.updateLastSyncError via s.Pull
				fmt.Fprintf(os.Stderr, "Auto-sync failed: %v\n", err)
			}
		}
	}
}

// Helper methods

func (s *serviceImpl) updateLastSyncError(err error) {
	state, _ := s.stateManager.GetState()
	state.LastError = new(err.Error())
	state.LastSyncStatus = "error"
	_ = s.stateManager.Save(state)
}

func (s *serviceImpl) filePathToDAGID(filePath string) string {
	filePath = filepath.ToSlash(filePath)
	// Remove path prefix if configured
	if s.cfg.Path != "" {
		prefix := strings.TrimSuffix(filepath.ToSlash(s.cfg.Path), "/") + "/"
		filePath = strings.TrimPrefix(filePath, prefix)
	}
	// Asset IDs keep the extension: attachment names in one directory may
	// differ only by extension.
	if isWikiPageAssetFile(filePath) {
		return filePath
	}
	// Remove extension
	ext := path.Ext(filePath)
	dagID := strings.TrimSuffix(filePath, ext)
	return dagID
}

func isSyncableRepoFile(filePath, itemID string) bool {
	if isBaseConfigID(itemID) {
		return false
	}
	// Attachments are classified by location and validated by name; the
	// extension switch below never applies to them, so a .md file under the
	// asset subtree can never become a Wiki page item.
	if isWikiPageAssetFile(itemID) {
		return isValidAssetItemID(itemID)
	}
	switch strings.ToLower(path.Ext(filePath)) {
	case wikiPageExtension:
		return isWikiPageFile(itemID)
	case dagYAMLExtension, dagYMLExtension:
		return !isWikiPageFile(itemID)
	default:
		return false
	}
}

// resolvePublishTargets validates and canonicalizes item IDs for batch publish.
func (s *serviceImpl) resolvePublishTargets(state *State, dagIDs []string) ([]string, error) {
	if len(dagIDs) == 0 {
		return nil, &ValidationError{
			Field:   "dagIds",
			Message: "at least one sync item ID is required",
		}
	}

	resolved := make([]string, 0, len(dagIDs))
	seen := make(map[string]struct{}, len(dagIDs))
	for i, dagID := range dagIDs {
		if strings.TrimSpace(dagID) == "" {
			return nil, &ValidationError{
				Field:   fmt.Sprintf("dagIds[%d]", i),
				Message: "sync item ID cannot be empty",
			}
		}

		normalized, err := normalizeDAGID(dagID)
		if err != nil {
			return nil, err
		}
		if normalized != dagID {
			return nil, &InvalidDAGIDError{
				DAGID:  dagID,
				Reason: fmt.Sprintf("must be normalized as %q", normalized),
			}
		}

		if _, exists := seen[dagID]; exists {
			continue
		}
		seen[dagID] = struct{}{}

		dagState, exists := state.Items[dagID]
		if !exists {
			return nil, &ValidationError{
				Field:   "dagIds",
				Message: fmt.Sprintf("sync item %q is not tracked by git sync", dagID),
			}
		}

		switch dagState.Status {
		case StatusModified, StatusUntracked:
			resolved = append(resolved, dagID)
		case StatusConflict:
			return nil, &ValidationError{
				Field:   "dagIds",
				Message: fmt.Sprintf("sync item %q has conflicts and cannot be batch-published", dagID),
			}
		case StatusSynced:
			return nil, &ValidationError{
				Field:   "dagIds",
				Message: fmt.Sprintf("sync item %q has no local changes", dagID),
			}
		case StatusMissing:
			return nil, &ValidationError{
				Field:   "dagIds",
				Message: fmt.Sprintf("sync item %q is missing from disk and cannot be published", dagID),
			}
		default:
			return nil, &ValidationError{
				Field:   "dagIds",
				Message: fmt.Sprintf("sync item %q is in unsupported status %q", dagID, dagState.Status),
			}
		}
	}

	if len(resolved) == 0 {
		return nil, &ValidationError{
			Field:   "dagIds",
			Message: "no publishable sync item IDs provided",
		}
	}

	sort.Strings(resolved)
	return resolved, nil
}

func decodeDAGID(dagID string) (string, error) {
	decoded, err := url.PathUnescape(strings.TrimSpace(dagID))
	if err != nil {
		return "", &InvalidDAGIDError{
			DAGID:  dagID,
			Reason: "contains invalid URL escape sequence",
		}
	}
	return decoded, nil
}

func normalizeDAGID(dagID string) (string, error) {
	decoded, err := decodeDAGID(dagID)
	if err != nil {
		return "", err
	}
	if decoded == "" {
		return "", &InvalidDAGIDError{DAGID: dagID, Reason: "cannot be empty"}
	}

	normalized := normalizeDAGIDSeparators(decoded)
	if path.IsAbs(normalized) || looksLikeWindowsAbsolutePath(normalized) {
		return "", &InvalidDAGIDError{DAGID: dagID, Reason: "absolute paths are not allowed"}
	}

	clean := path.Clean(normalized)
	if clean == "." || clean == ".." {
		return "", &InvalidDAGIDError{DAGID: dagID, Reason: "must point to a sync item ID, not current/parent directory"}
	}
	if strings.HasPrefix(clean, "../") {
		return "", &InvalidDAGIDError{DAGID: dagID, Reason: "path traversal is not allowed"}
	}
	if isBaseConfigID(clean) {
		return "", &InvalidDAGIDError{DAGID: dagID, Reason: "base configuration paths are not sync item IDs"}
	}

	return clean, nil
}

func looksLikeWindowsAbsolutePath(p string) bool {
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return strings.HasPrefix(p, "//")
}

func safeJoinWithinBase(baseDir, relativePath string) (string, error) {
	if filepath.IsAbs(relativePath) {
		return "", &InvalidDAGIDError{
			DAGID:  relativePath,
			Reason: "absolute paths are not allowed",
		}
	}

	cleanRel := filepath.Clean(relativePath)
	if cleanRel == "." || cleanRel == ".." {
		return "", &InvalidDAGIDError{
			DAGID:  relativePath,
			Reason: "must be a valid relative path",
		}
	}
	if strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", &InvalidDAGIDError{
			DAGID:  relativePath,
			Reason: "path traversal is not allowed",
		}
	}

	fullPath := filepath.Join(baseDir, cleanRel)
	relToBase, err := filepath.Rel(baseDir, fullPath)
	if err != nil {
		return "", &InvalidDAGIDError{
			DAGID:  relativePath,
			Reason: "cannot resolve path safely",
		}
	}
	if relToBase == ".." || strings.HasPrefix(relToBase, ".."+string(filepath.Separator)) {
		return "", &InvalidDAGIDError{
			DAGID:  relativePath,
			Reason: "path escapes allowed base directory",
		}
	}

	return fullPath, nil
}

func ensurePathWithinBase(baseDir, targetPath string) error {
	_, err := relativePathWithinBase(baseDir, targetPath)
	return err
}

func relativePathWithinBase(baseDir, targetPath string) (string, error) {
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", &InvalidDAGIDError{
			DAGID:  targetPath,
			Reason: "cannot resolve base directory",
		}
	}
	targetAbs, err := filepath.Abs(targetPath)
	if err != nil {
		return "", &InvalidDAGIDError{
			DAGID:  targetPath,
			Reason: "cannot resolve path safely",
		}
	}
	relToBase, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return "", &InvalidDAGIDError{
			DAGID:  targetPath,
			Reason: "cannot resolve path safely",
		}
	}
	if relToBase == ".." || strings.HasPrefix(relToBase, ".."+string(filepath.Separator)) || filepath.IsAbs(relToBase) {
		return "", &InvalidDAGIDError{
			DAGID:  targetPath,
			Reason: "path escapes allowed base directory",
		}
	}
	return relToBase, nil
}

func ensureExistingPathWithinBase(baseDir, targetPath string) error {
	resolvedBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return err
	}
	resolvedTarget, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		return err
	}
	return ensurePathWithinBase(resolvedBase, resolvedTarget)
}

func safeReadFileWithinBase(baseDir, targetPath string) ([]byte, error) {
	relPath, err := relativePathWithinBase(baseDir, targetPath)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = root.Close()
	}()
	if err := rejectUnsafeRootPath(root, relPath, targetPath, "read", false); err != nil {
		return nil, err
	}
	if err := ensureExistingPathWithinBase(baseDir, targetPath); err != nil {
		return nil, err
	}
	file, err := openRootFileNoFollow(root, relPath, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()
	if err := validateOpenedRootFile(root, relPath, targetPath, "read", file); err != nil {
		return nil, err
	}
	return io.ReadAll(file)
}

func safeWriteFileWithinBase(baseDir, targetPath string, content []byte, perm os.FileMode) error {
	relPath, err := relativePathWithinBase(baseDir, targetPath)
	if err != nil {
		return err
	}
	parentDir := filepath.Dir(targetPath)
	if err := ensurePathWithinBase(baseDir, parentDir); err != nil {
		return err
	}
	if err := os.MkdirAll(baseDir, 0750); err != nil {
		return err
	}
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		return err
	}
	defer func() {
		_ = root.Close()
	}()
	parentRel := filepath.Dir(relPath)
	if err := ensureSafeRootDirPath(root, parentRel, targetPath, "write", true); err != nil {
		return err
	}
	if err := ensureExistingPathWithinBase(baseDir, parentDir); err != nil {
		return err
	}
	if err := rejectUnsafeRootPath(root, relPath, targetPath, "write", true); err != nil {
		return err
	}
	file, err := openRootFileNoFollow(root, relPath, os.O_WRONLY, 0)
	if os.IsNotExist(err) {
		file, err = openRootFileNoFollow(root, relPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	}
	if os.IsExist(err) {
		file, err = openRootFileNoFollow(root, relPath, os.O_WRONLY, 0)
	}
	if err != nil {
		return err
	}
	if err := validateOpenedRootFile(root, relPath, targetPath, "write", file); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Truncate(0); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(perm); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func rejectUnsafeRootPath(root *os.Root, relPath, targetPath, operation string, allowNotExist bool) error {
	cleanPath := filepath.Clean(relPath)
	parentRel := filepath.Dir(cleanPath)
	if err := ensureSafeRootDirPath(root, parentRel, targetPath, operation, false); err != nil {
		return err
	}
	info, err := root.Lstat(cleanPath)
	if err != nil {
		if allowNotExist && os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return rejectUnsafeFileInfo(info, targetPath, operation)
}

func ensureSafeRootDirPath(root *os.Root, relDir, targetPath, operation string, create bool) error {
	cleanDir := filepath.Clean(relDir)
	if cleanDir == "." {
		return nil
	}
	current := ""
	for segment := range strings.SplitSeq(cleanDir, string(filepath.Separator)) {
		if segment == "" || segment == "." {
			continue
		}
		if segment == ".." {
			return fmt.Errorf("refusing to %s path outside root: %s", operation, targetPath)
		}
		if current == "" {
			current = segment
		} else {
			current = filepath.Join(current, segment)
		}
		info, err := root.Lstat(current)
		if create && os.IsNotExist(err) {
			if err := root.Mkdir(current, 0750); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = root.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to %s through symlink: %s", operation, targetPath)
		}
		if !info.IsDir() {
			return fmt.Errorf("refusing to %s through non-directory path segment: %s", operation, targetPath)
		}
	}
	return nil
}

func validateOpenedRootFile(root *os.Root, relPath, targetPath, operation string, file *os.File) error {
	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}
	if err := rejectUnsafeFileInfo(fileInfo, targetPath, operation); err != nil {
		return err
	}
	cleanPath := filepath.Clean(relPath)
	parentRel := filepath.Dir(cleanPath)
	if err := ensureSafeRootDirPath(root, parentRel, targetPath, operation, false); err != nil {
		return err
	}
	pathInfo, err := root.Lstat(cleanPath)
	if err != nil {
		return err
	}
	if err := rejectUnsafeFileInfo(pathInfo, targetPath, operation); err != nil {
		return err
	}
	if !os.SameFile(pathInfo, fileInfo) {
		return fmt.Errorf("refusing to %s path changed while opening: %s", operation, targetPath)
	}
	return nil
}

func rejectUnsafeFileInfo(info os.FileInfo, targetPath, operation string) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to %s through symlink: %s", operation, targetPath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to %s non-regular file: %s", operation, targetPath)
	}
	return nil
}

func (s *serviceImpl) writeDAGFile(dagID, filePath string, content []byte) error {
	if _, err := normalizeDAGID(dagID); err != nil {
		return err
	}
	return safeWriteFileWithinBase(s.localBaseDir(dagID), filePath, content, 0600)
}

func (s *serviceImpl) readDAGFile(dagID, filePath string) ([]byte, error) {
	if _, err := normalizeDAGID(dagID); err != nil {
		return nil, err
	}
	return safeReadFileWithinBase(s.localBaseDir(dagID), filePath)
}

func (s *serviceImpl) safeDAGIDToFilePath(dagID, fileExtension string) (string, error) {
	normalized, err := normalizeDAGID(dagID)
	if err != nil {
		return "", err
	}
	baseDir := s.dagsDir
	localID := normalized
	if isWikiPageFile(normalized) || isWikiPageAssetFile(normalized) {
		baseDir = s.localWikiDir()
		localID = strings.TrimPrefix(normalized, wikiRepoDirForID(normalized)+"/")
	}
	return safeJoinWithinBase(baseDir, filepath.FromSlash(localID+normalizeLocalFileExtension(normalized, fileExtension)))
}

func (s *serviceImpl) localWikiDir() string {
	if s.wikiDir != "" {
		return s.wikiDir
	}
	return filepath.Join(s.dagsDir, wikiDir)
}

func (s *serviceImpl) localBaseDir(itemID string) string {
	if isWikiPageFile(itemID) || isWikiPageAssetFile(itemID) {
		return s.localWikiDir()
	}
	return s.dagsDir
}

func normalizeLocalFileExtension(itemID, extension string) string {
	// Asset IDs already carry their extension; nothing is appended.
	if isWikiPageAssetFile(itemID) {
		return ""
	}
	if isWikiPageFile(itemID) {
		if strings.EqualFold(extension, wikiPageExtension) {
			return extension
		}
		return wikiPageExtension
	}
	return normalizeDAGFileExtension(extension)
}

func (s *serviceImpl) safeRepoPathToFilePath(repoPath string) (string, error) {
	return safeJoinWithinBase(s.gitClient.repoPath, filepath.FromSlash(repoPath))
}

func (s *serviceImpl) safeDAGIDToRepoPath(dagID, fileExtension string) (string, error) {
	normalized, err := normalizeDAGID(dagID)
	if err != nil {
		return "", err
	}

	extension := normalizeDAGFileExtension(fileExtension)
	if isWikiPageAssetFile(normalized) {
		extension = ""
	} else if isWikiPageFile(normalized) {
		extension = wikiPageExtension
	}
	repoPath := normalized + extension
	if s.cfg != nil && s.cfg.Path != "" {
		repoPath = path.Join(filepath.ToSlash(s.cfg.Path), repoPath)
	}

	safePath, err := safeJoinWithinBase(s.gitClient.repoPath, filepath.FromSlash(repoPath))
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(s.gitClient.repoPath, safePath)
	if err != nil {
		return "", &InvalidDAGIDError{
			DAGID:  dagID,
			Reason: "cannot resolve repository path",
		}
	}
	return filepath.ToSlash(relPath), nil
}

func (s *serviceImpl) syncItemFileExtension(dagID string, dagState *SyncItemState) string {
	if isWikiPageAssetFile(dagID) {
		if dagState != nil {
			dagState.Kind = SyncItemKindWikiPageAsset
			dagState.FileExtension = ""
		}
		return ""
	}
	if isWikiPageFile(dagID) {
		if dagState != nil {
			dagState.Kind = SyncItemKindWikiPage
			if strings.EqualFold(dagState.FileExtension, wikiPageExtension) {
				return dagState.FileExtension
			}
			dagState.FileExtension = wikiPageExtension
		}
		return wikiPageExtension
	}
	if dagState != nil {
		dagState.Kind = SyncItemKindDAG
		switch {
		case strings.EqualFold(dagState.FileExtension, dagYMLExtension):
			dagState.FileExtension = dagYMLExtension
			return dagYMLExtension
		case strings.EqualFold(dagState.FileExtension, dagYAMLExtension):
			dagState.FileExtension = dagYAMLExtension
			return dagYAMLExtension
		}
	}

	for _, fileExtension := range []string{dagYAMLExtension, dagYMLExtension} {
		filePath, err := s.safeDAGIDToFilePath(dagID, fileExtension)
		if err == nil {
			if _, err := os.Stat(filePath); err == nil {
				if dagState != nil {
					dagState.FileExtension = fileExtension
				}
				return fileExtension
			}
		}
	}

	for _, fileExtension := range []string{dagYAMLExtension, dagYMLExtension} {
		repoPath, err := s.safeDAGIDToRepoPath(dagID, fileExtension)
		if err != nil {
			continue
		}
		filePath, err := s.safeRepoPathToFilePath(repoPath)
		if err == nil {
			if _, err := os.Stat(filePath); err == nil {
				if dagState != nil {
					dagState.FileExtension = fileExtension
				}
				return fileExtension
			}
		}
	}

	if dagState != nil {
		dagState.FileExtension = dagYAMLExtension
	}
	return dagYAMLExtension
}

func (s *serviceImpl) ensureSyncItemFileExtensions(state *State) bool {
	changed := false
	for dagID, dagState := range state.Items {
		if dagState == nil {
			continue
		}
		previousExtension := dagState.FileExtension
		previousKind := dagState.Kind
		s.syncItemFileExtension(dagID, dagState)
		if dagState.FileExtension != previousExtension || dagState.Kind != previousKind {
			changed = true
		}
	}
	return changed
}

func (s *serviceImpl) migrateLocalDAGExtension(dagID, oldExtension, newExtension string) error {
	oldPath, err := s.safeDAGIDToFilePath(dagID, oldExtension)
	if err != nil {
		return err
	}
	newPath, err := s.safeDAGIDToFilePath(dagID, newExtension)
	if err != nil {
		return err
	}

	content, err := s.readDAGFile(dagID, oldPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read DAG %q before changing its file extension: %w", dagID, err)
	}

	if _, err := os.Stat(newPath); err == nil {
		return &ValidationError{
			Field:   dagID,
			Message: "DAG exists with both .yaml and .yml extensions",
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to inspect DAG %q before changing its file extension: %w", dagID, err)
	}

	if err := s.writeDAGFile(dagID, newPath, content); err != nil {
		return fmt.Errorf("failed to write DAG %q with its remote file extension: %w", dagID, err)
	}
	if err := os.Remove(oldPath); err != nil {
		if rollbackErr := os.Remove(newPath); rollbackErr != nil {
			return fmt.Errorf("failed to remove old DAG file: %w (destination rollback failed: %v)", err, rollbackErr)
		}
		return fmt.Errorf("failed to remove old DAG file: %w", err)
	}

	return nil
}

// validateEnabled checks if git sync is enabled and configured.
func (s *serviceImpl) validateEnabled() error {
	if !s.cfg.Enabled {
		return ErrNotEnabled
	}
	if !s.cfg.IsValid() {
		return ErrNotConfigured
	}
	return nil
}

// validatePushEnabled checks if push operations are allowed.
func (s *serviceImpl) validatePushEnabled() error {
	if err := s.validateEnabled(); err != nil {
		return err
	}
	if !s.cfg.PushEnabled {
		return ErrPushDisabled
	}
	return nil
}

// validatePublishable checks if an item can be published.
func (s *serviceImpl) validatePublishable(itemState *SyncItemState, itemID string, force bool) error {
	if itemState.Status == StatusMissing {
		return &ValidationError{
			Field:   itemID,
			Message: "sync item is missing from disk and cannot be published",
		}
	}
	if itemState.Status == StatusConflict && !force {
		return &ConflictError{
			DAGID:         itemID,
			RemoteCommit:  itemState.RemoteCommit,
			RemoteAuthor:  itemState.RemoteAuthor,
			RemoteMessage: itemState.RemoteMessage,
		}
	}
	if itemState.Status == StatusSynced {
		return ErrNoChanges
	}
	return nil
}

// ensureRepoReady ensures the repository is cloned and opened.
func (s *serviceImpl) ensureRepoReady(ctx context.Context) error {
	if !s.gitClient.IsCloned() {
		if err := s.gitClient.Clone(ctx); err != nil {
			return err
		}
	} else if err := s.gitClient.Open(); err != nil {
		return err
	}

	repoWikiDir, err := s.selectRepoWikiDir()
	if err != nil {
		return err
	}
	s.repoWikiDir = repoWikiDir
	return nil
}

func (s *serviceImpl) selectRepoWikiDir() (string, error) {
	root := s.gitClient.repoPath
	if s.cfg != nil && s.cfg.Path != "" {
		root = filepath.Join(root, s.cfg.Path)
	}
	wikiExists := pathExists(filepath.Join(root, wikiDir))
	docsExists := pathExists(filepath.Join(root, legacyDocsDir))
	if wikiExists && docsExists {
		return "", fmt.Errorf("git sync repository contains both %q and legacy %q Wiki directories", wikiDir, legacyDocsDir)
	}
	if docsExists {
		return legacyDocsDir, nil
	}
	return wikiDir, nil
}

func pathExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return err == nil
}

// newSyncedItemState creates a new SyncItemState in synced status.
func (s *serviceImpl) newSyncedItemState(itemID, fileExtension, commitHash, contentHash string) *SyncItemState {
	now := time.Now()
	return &SyncItemState{
		Status:         StatusSynced,
		Kind:           SyncItemKindForID(itemID),
		FileExtension:  normalizeLocalFileExtension(itemID, fileExtension),
		BaseCommit:     commitHash,
		LastSyncedHash: contentHash,
		LastSyncedAt:   &now,
		LocalHash:      contentHash,
	}
}

// updateSuccessStateWithCommit updates and saves the state after a successful sync.
func (s *serviceImpl) updateSuccessStateWithCommit(state *State, commitHash string) {
	state.LastSyncAt = new(time.Now())
	state.LastSyncCommit = commitHash
	state.LastSyncStatus = "success"
	state.LastError = nil
	state.Repository = s.cfg.Repository
	state.Branch = s.cfg.Branch
	_ = s.stateManager.Save(state) // Best effort - state will be recovered on next load
}

// buildPullMessage constructs the result message for a pull operation.
func (s *serviceImpl) buildPullMessage(alreadyUpToDate bool, synced, conflicts []string) string {
	if len(conflicts) > 0 {
		return fmt.Sprintf("Pulled with %d conflict(s)", len(conflicts))
	}
	if alreadyUpToDate {
		return "Already up to date"
	}
	return fmt.Sprintf("Synced %d sync item(s)", len(synced))
}

// computeStatusCounts computes the counts for each item status.
func computeStatusCounts(items map[string]*SyncItemState) StatusCounts {
	var counts StatusCounts
	for _, itemState := range items {
		switch itemState.Status {
		case StatusSynced:
			counts.Synced++
		case StatusModified:
			counts.Modified++
		case StatusUntracked:
			counts.Untracked++
		case StatusConflict:
			counts.Conflict++
		case StatusMissing:
			counts.Missing++
		}
	}
	return counts
}
