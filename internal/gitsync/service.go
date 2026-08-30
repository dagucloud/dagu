// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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

	// Move renames a tracked item.
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
	Deleted   []string    `json:"deleted,omitempty"`
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
	ItemID           string       `json:"dagId"`
	Kind             SyncItemKind `json:"kind"`
	FileExtension    string       `json:"fileExtension"`
	Status           SyncStatus   `json:"status"`
	Binary           bool         `json:"binary,omitempty"`
	LocalContent     string       `json:"localContent"`
	RemoteContent    string       `json:"remoteContent,omitempty"`
	LocalSize        *int64       `json:"localSize,omitempty"`
	RemoteSize       *int64       `json:"remoteSize,omitempty"`
	RemoteCommit     string       `json:"remoteCommit,omitempty"`
	RemoteAuthor     string       `json:"remoteAuthor,omitempty"`
	RemoteMessage    string       `json:"remoteMessage,omitempty"`
	RemoteDeleted    bool         `json:"remoteDeleted,omitempty"`
	LocalExecutable  *bool        `json:"localExecutable,omitempty"`
	RemoteExecutable *bool        `json:"remoteExecutable,omitempty"`
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
	syncResult, err := s.syncFilesToLocal(ctx, pullResult, currentCommit)
	if err != nil {
		result.Success = false
		result.Message = "Failed to sync files"
		result.Errors = append(result.Errors, SyncError{Message: err.Error()})
		s.updateLastSyncError(err)
		return result, err
	}

	result.Synced = syncResult.synced
	result.Deleted = syncResult.deleted
	result.Conflicts = syncResult.conflicts
	result.Success = true
	result.Message = s.buildPullMessage(pullResult.AlreadyUpToDate, syncResult.synced, syncResult.deleted, syncResult.conflicts)

	return result, nil
}

// syncFilesToLocal syncs repository files to their local storage roots.
// It updates sync metadata and saves state in a single write.
type repoSyncItem struct {
	id         string
	repoPath   string
	kind       SyncItemKind
	extension  string
	executable bool
}

type localSyncResult struct {
	synced    []string
	deleted   []string
	conflicts []string
}

func (s *serviceImpl) syncFilesToLocal(_ context.Context, pullResult *PullResult, commitHash string) (localSyncResult, error) {
	var synced []string
	var deleted []string
	var conflicts []string

	trackedFiles, err := s.gitClient.ListTrackedFiles()
	if err != nil {
		return localSyncResult{}, err
	}
	items := make([]repoSyncItem, 0, len(trackedFiles))
	itemByID := make(map[string]repoSyncItem, len(trackedFiles))
	for _, trackedFile := range trackedFiles {
		item, ok := s.repoFileItem(trackedFile)
		if !ok {
			continue
		}
		if existing, exists := itemByID[item.id]; exists {
			message := fmt.Sprintf("sync item ID collides for %q and %q", existing.repoPath, item.repoPath)
			if existing.kind == SyncItemKindDAG && item.kind == SyncItemKindDAG {
				message = "DAG exists with both .yaml and .yml extensions"
			}
			return localSyncResult{}, &ValidationError{Field: item.id, Message: message}
		}
		itemByID[item.id] = item
		items = append(items, item)
	}

	state, _ := s.stateManager.GetState()
	s.ensureSyncItemFileExtensions(state)

	// Reconcile: detect missing/reappeared files before processing
	s.reconcile(state)

	// Refresh hashes to detect local modifications before checking for conflicts
	s.refreshLocalHashes(state)

	repoFileSet := make(map[string]struct{}, len(items))
	for _, item := range items {
		repoFileSet[item.id] = struct{}{}
	}
	deleted, deleteConflicts, err := s.syncRemoteFileDeletes(state, repoFileSet, pullResult.CurrentCommit)
	if err != nil {
		return localSyncResult{}, err
	}
	conflicts = append(conflicts, deleteConflicts...)

	for _, item := range items {
		repoFilePath, err := s.safeRepoPathToFilePath(item.repoPath)
		if err != nil {
			return localSyncResult{}, err
		}

		itemState := state.Items[item.id]
		if itemState != nil && itemState.Kind != item.kind {
			// A remote kind change replaces the old item. Preserve local edits
			// as a deletion conflict; unchanged items can switch immediately.
			if itemState.Status == StatusSynced {
				matchesBase, err := s.localItemMatchesBase(item.id, itemState)
				if err != nil {
					return localSyncResult{}, fmt.Errorf("failed to verify sync item %q before replacement: %w", item.id, err)
				}
				if !matchesBase {
					s.markRemoteDeleteConflict(itemState, pullResult.CurrentCommit)
					conflicts = append(conflicts, item.id)
					continue
				}
			}
			if itemState.Status != StatusSynced && itemState.Status != StatusMissing {
				s.markRemoteDeleteConflict(itemState, pullResult.CurrentCommit)
				conflicts = append(conflicts, item.id)
				continue
			}
			if err := s.removeItemFile(item.id, itemState); err != nil && !os.IsNotExist(err) {
				return localSyncResult{}, fmt.Errorf("failed to replace sync item %q: %w", item.id, err)
			}
			delete(state.Items, item.id)
			itemState = nil
		}
		// Unchanged fast path: the item was synced against this exact commit
		// and refreshLocalHashes above found no local drift, so neither side
		// needs to be read. This keeps pulls from re-reading every file
		// (binary attachments in particular) on each auto-sync cycle.
		if itemState != nil && itemState.Status == StatusSynced && itemState.BaseCommit == pullResult.CurrentCommit {
			continue
		}
		localExtension := item.extension
		if itemState != nil && (item.kind == SyncItemKindDAG || item.kind == SyncItemKindWikiPage) {
			previousExtension := s.syncItemFileExtension(item.id, itemState)
			if previousExtension != item.extension {
				if err := s.migrateLocalDAGExtension(item.id, previousExtension, item.extension); err != nil {
					return localSyncResult{}, err
				}
				itemState.FileExtension = item.extension
			}
		}

		localPath, err := s.safeItemFilePath(item.id, item.kind, localExtension)
		if err != nil {
			return localSyncResult{}, err
		}

		repoHash, err := safeHashFileWithinBase(s.gitClient.repoPath, repoFilePath)
		if err != nil {
			return localSyncResult{}, err
		}
		remoteExecutable := item.kind == SyncItemKindFile && item.executable

		localHash, err := s.hashItemFile(item.id, item.kind, localPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return localSyncResult{}, fmt.Errorf("failed to write synced item %q: destination cannot be read: %w", item.id, err)
			}
			// Before creating a new local file, check if this content matches
			// a missing item's hash (prevents duplicates after move+pull).
			// Only same-kind entries qualify: identical bytes across kinds
			// must not forget an unrelated item.
			for otherID, otherState := range state.Items {
				if otherID != item.id &&
					otherState.Status == StatusMissing &&
					otherState.LastSyncedHash == repoHash &&
					otherState.Kind == item.kind {
					// Auto-forget the stale missing entry
					delete(state.Items, otherID)
					break
				}
			}

			if err := s.copyRepoItemFile(item.id, item.kind, repoFilePath, localPath, remoteExecutable); err != nil {
				return localSyncResult{}, fmt.Errorf("failed to write synced item %q: %w", item.id, err)
			}
			now := time.Now()
			newState := newItemState(item, pullResult.CurrentCommit, repoHash, now)
			if fi, err := os.Stat(localPath); err == nil {
				updateStatCache(newState, fi)
			}
			state.Items[item.id] = newState
			synced = append(synced, item.id)
			continue
		}

		localExecutable := remoteExecutable
		if itemState != nil {
			localExecutable = itemState.LastSyncedExecutable
		}
		if item.kind == SyncItemKindFile {
			if info, statErr := os.Stat(localPath); statErr == nil {
				localExecutable = executableMode(info.Mode(), localExecutable)
			}
		} else {
			localExecutable = false
		}
		localMatchesRemote := localHash == repoHash && localExecutable == remoteExecutable

		if localMatchesRemote {
			if itemState == nil || itemState.Status != StatusSynced || itemState.BaseCommit != pullResult.CurrentCommit || itemState.LastSyncedHash != repoHash || itemState.LastSyncedExecutable != remoteExecutable {
				now := time.Now()
				newState := newItemState(item, pullResult.CurrentCommit, repoHash, now)
				if fi, err := os.Stat(localPath); err == nil {
					updateStatCache(newState, fi)
				}
				state.Items[item.id] = newState
				synced = append(synced, item.id)
			}
			continue
		}

		remoteChanged := itemState == nil || itemState.RemoteDeleted || itemState.LastSyncedHash != repoHash ||
			(item.kind == SyncItemKindFile && itemState.LastSyncedExecutable != remoteExecutable)
		localChanged := itemState != nil && (itemState.Status == StatusModified || itemState.Status == StatusConflict)
		if itemState == nil && item.kind == SyncItemKindFile {
			localChanged = true
		}
		if localChanged {
			if remoteChanged {
				var remoteAuthor, remoteMessage string
				if commitInfo, err := s.gitClient.GetCommitInfo(pullResult.CurrentCommit); err == nil && commitInfo != nil {
					remoteAuthor = commitInfo.Author
					remoteMessage = commitInfo.Message
				}
				now := time.Now()
				baseCommit := pullResult.PreviousCommit
				lastHash := repoHash
				lastSyncedAt := (*time.Time)(nil)
				lastExecutable := remoteExecutable
				if itemState != nil {
					baseCommit = itemState.BaseCommit
					lastHash = itemState.LastSyncedHash
					lastSyncedAt = itemState.LastSyncedAt
					lastExecutable = itemState.LastSyncedExecutable
				}
				state.Items[item.id] = &SyncItemState{
					Status:               StatusConflict,
					Kind:                 item.kind,
					FileExtension:        localExtension,
					BaseCommit:           baseCommit,
					LastSyncedHash:       lastHash,
					LastSyncedAt:         lastSyncedAt,
					LocalHash:            localHash,
					LastSyncedExecutable: lastExecutable,
					LocalExecutable:      localExecutable,
					RemoteExecutable:     remoteExecutable,
					RemoteCommit:         pullResult.CurrentCommit,
					RemoteAuthor:         remoteAuthor,
					RemoteMessage:        remoteMessage,
					ConflictDetectedAt:   &now,
				}
				if fi, err := os.Stat(localPath); err == nil {
					updateStatCache(state.Items[item.id], fi)
				}
				conflicts = append(conflicts, item.id)
			}
			continue
		}

		if err := s.copyRepoItemFile(item.id, item.kind, repoFilePath, localPath, remoteExecutable); err != nil {
			return localSyncResult{}, fmt.Errorf("failed to write synced item %q: %w", item.id, err)
		}
		now := time.Now()
		newState := newItemState(item, pullResult.CurrentCommit, repoHash, now)
		if fi, err := os.Stat(localPath); err == nil {
			updateStatCache(newState, fi)
		}
		state.Items[item.id] = newState
		synced = append(synced, item.id)
	}

	// Existing DAG and Wiki reconciliation remains unchanged.
	s.reconcileAfterPull(state, repoFileSet)
	_ = s.scanLocalItems(state)
	s.updateSuccessStateWithCommit(state, commitHash)

	return localSyncResult{synced: synced, deleted: deleted, conflicts: conflicts}, nil
}

func newItemState(item repoSyncItem, commitHash, contentHash string, now time.Time) *SyncItemState {
	return &SyncItemState{
		Status:               StatusSynced,
		Kind:                 item.kind,
		FileExtension:        item.extension,
		BaseCommit:           commitHash,
		LastSyncedHash:       contentHash,
		LastSyncedAt:         &now,
		LocalHash:            contentHash,
		LastSyncedExecutable: item.executable,
		LocalExecutable:      item.executable,
	}
}

func (s *serviceImpl) repoFileItem(file TrackedFile) (repoSyncItem, bool) {
	repoPath := filepath.ToSlash(file.Path)
	relPath := repoPath
	if s.cfg.Path != "" {
		prefix := strings.TrimSuffix(filepath.ToSlash(path.Clean(s.cfg.Path)), "/") + "/"
		relPath = strings.TrimPrefix(repoPath, prefix)
	}
	if relPath == "" || relPath == "." {
		return repoSyncItem{}, false
	}

	extension := path.Ext(relPath)
	baseID := strings.TrimSuffix(relPath, extension)
	assetPrefix := path.Join(s.repoWikiDir, wikiPageAssetsDirName) + "/"
	if strings.HasPrefix(relPath, assetPrefix) {
		if !isValidAssetItemID(relPath) {
			return repoSyncItem{}, false
		}
		return repoSyncItem{id: relPath, repoPath: repoPath, kind: SyncItemKindWikiPageAsset}, true
	}
	wikiPrefix := s.repoWikiDir + "/"
	if strings.HasPrefix(relPath, wikiPrefix) {
		if strings.EqualFold(extension, wikiPageExtension) {
			if !isSyncableRepoFile(relPath, baseID) {
				return repoSyncItem{}, false
			}
			return repoSyncItem{id: baseID, repoPath: repoPath, kind: SyncItemKindWikiPage, extension: extension}, true
		}
		if extension == dagYAMLExtension || extension == dagYMLExtension {
			return repoSyncItem{}, false
		}
	}
	if extension == dagYAMLExtension || extension == dagYMLExtension {
		if isBaseConfigID(baseID) {
			return repoSyncItem{}, false
		}
		return repoSyncItem{id: baseID, repoPath: repoPath, kind: SyncItemKindDAG, extension: extension}, true
	}
	return repoSyncItem{id: relPath, repoPath: repoPath, kind: SyncItemKindFile, executable: file.Executable}, true
}

func (s *serviceImpl) trackedItemRepoPaths() (map[string][]string, error) {
	trackedFiles, err := s.gitClient.ListTrackedFiles()
	if err != nil {
		return nil, err
	}

	paths := make(map[string][]string)
	for _, trackedFile := range trackedFiles {
		item, ok := s.repoFileItem(trackedFile)
		if ok {
			paths[item.id] = append(paths[item.id], item.repoPath)
		}
	}
	return paths, nil
}

func (s *serviceImpl) syncRemoteFileDeletes(state *State, repoFileSet map[string]struct{}, remoteCommit string) ([]string, []string, error) {
	var deleted []string
	var conflicts []string
	for itemID, itemState := range state.Items {
		if itemState.Kind != SyncItemKindFile {
			continue
		}
		if _, exists := repoFileSet[itemID]; exists {
			continue
		}
		if itemState.Status == StatusMissing {
			delete(state.Items, itemID)
			continue
		}
		if itemState.Status == StatusSynced {
			matchesBase, err := s.localItemMatchesBase(itemID, itemState)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to verify remotely deleted file %q: %w", itemID, err)
			}
			if !matchesBase {
				s.markRemoteDeleteConflict(itemState, remoteCommit)
				conflicts = append(conflicts, itemID)
				continue
			}
			if err := s.removeItemFile(itemID, itemState); err != nil && !os.IsNotExist(err) {
				return nil, nil, fmt.Errorf("failed to remove remotely deleted file %q: %w", itemID, err)
			}
			delete(state.Items, itemID)
			deleted = append(deleted, itemID)
			continue
		}
		s.markRemoteDeleteConflict(itemState, remoteCommit)
		conflicts = append(conflicts, itemID)
	}
	return deleted, conflicts, nil
}

func (s *serviceImpl) markRemoteDeleteConflict(itemState *SyncItemState, remoteCommit string) {
	info, _ := s.gitClient.GetCommitInfo(remoteCommit)
	now := time.Now()
	itemState.Status = StatusConflict
	itemState.RemoteDeleted = true
	itemState.RemoteCommit = remoteCommit
	itemState.ConflictDetectedAt = &now
	if info != nil {
		itemState.RemoteAuthor = info.Author
		itemState.RemoteMessage = info.Message
	}
}

// localItemMatchesBase refreshes itemState and reports whether the local file matches its synced content.
func (s *serviceImpl) localItemMatchesBase(itemID string, itemState *SyncItemState) (bool, error) {
	fileExtension := s.syncItemFileExtension(itemID, itemState)
	filePath, err := s.safeItemFilePath(itemID, itemState.Kind, fileExtension)
	if err != nil {
		return false, err
	}

	localHash, info, err := s.hashItemFileInfo(itemID, itemState.Kind, filePath)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	localExecutable := itemState.Kind == SyncItemKindFile && executableMode(info.Mode(), itemState.LastSyncedExecutable)
	itemState.LocalHash = localHash
	itemState.LocalExecutable = localExecutable
	updateStatCache(itemState, info)

	matchesBase := localHash == itemState.LastSyncedHash &&
		(itemState.Kind != SyncItemKindFile || localExecutable == itemState.LastSyncedExecutable)
	if !matchesBase && (itemState.Status == StatusSynced || itemState.Status == StatusMissing) {
		itemState.Status = StatusModified
		itemState.ModifiedAt = new(time.Now())
		itemState.PreviousStatus = ""
		itemState.MissingAt = nil
	} else if matchesBase && itemState.Status == StatusMissing {
		itemState.Status = StatusSynced
		itemState.PreviousStatus = ""
		itemState.MissingAt = nil
	}

	return matchesBase, nil
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
		fileExtension := s.syncItemFileExtension(dagID, dagState)
		filePath, err := s.safeItemFilePath(dagID, dagState.Kind, fileExtension)
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
		pageID := strings.TrimSuffix(filepath.ToSlash(relPath), ext)
		if wiki.ValidatePageID(pageID) != nil {
			return nil
		}
		itemID := path.Join(s.repoWikiDir, pageID)
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
		fileExtension := s.syncItemFileExtension(dagID, dagState)
		filePath, err := s.safeItemFilePath(dagID, dagState.Kind, fileExtension)
		if err != nil {
			continue
		}

		// Stat-before-hash: skip expensive read+hash if mtime+size unchanged
		info, statErr := os.Stat(filePath)
		if statErr != nil {
			// File might be deleted, skip for now
			continue
		}

		localExecutable := dagState.Kind == SyncItemKindFile && executableMode(info.Mode(), dagState.LastSyncedExecutable)
		if statMatchesCache(dagState, info) && dagState.LocalExecutable == localExecutable {
			continue
		}

		currentHash, err := s.hashItemFile(dagID, dagState.Kind, filePath)
		if err != nil {
			continue
		}

		updateStatCache(dagState, info)

		// Update LocalHash if changed
		if dagState.LocalHash != currentHash {
			dagState.LocalHash = currentHash
			changed = true
		}
		if dagState.LocalExecutable != localExecutable {
			dagState.LocalExecutable = localExecutable
			changed = true
		}

		matchesBase := currentHash == dagState.LastSyncedHash &&
			(dagState.Kind != SyncItemKindFile || localExecutable == dagState.LastSyncedExecutable)
		if dagState.Status == StatusSynced && !matchesBase {
			dagState.Status = StatusModified
			dagState.ModifiedAt = new(time.Now())
			changed = true
		} else if dagState.Status == StatusModified && matchesBase {
			// User reverted changes manually - back to synced
			dagState.Status = StatusSynced
			dagState.ModifiedAt = nil
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
		fileExtension := s.syncItemFileExtension(dagID, dagState)
		filePath, err := s.safeItemFilePath(dagID, dagState.Kind, fileExtension)
		if err != nil {
			continue
		}

		info, statErr := os.Stat(filePath)
		fileExists := statErr == nil

		switch dagState.Status {
		case StatusMissing:
			if fileExists {
				// File reappeared — hash it and decide new status
				currentHash, err := s.hashItemFile(dagID, dagState.Kind, filePath)
				if err != nil {
					continue
				}
				localExecutable := dagState.Kind == SyncItemKindFile && executableMode(info.Mode(), dagState.LastSyncedExecutable)
				if currentHash == dagState.LastSyncedHash &&
					(dagState.Kind != SyncItemKindFile || localExecutable == dagState.LastSyncedExecutable) {
					dagState.Status = StatusSynced
				} else {
					dagState.Status = StatusModified
					now := time.Now()
					dagState.ModifiedAt = &now
				}
				dagState.LocalHash = currentHash
				dagState.LocalExecutable = localExecutable
				updateStatCache(dagState, info)
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
	dagFilePath, err := s.safeItemFilePath(dagID, dagState.Kind, fileExtension)
	if err != nil {
		return nil, err
	}
	repoFilePath, err := s.safeItemRepoPath(dagID, dagState.Kind, fileExtension)
	if err != nil {
		return nil, err
	}
	repoAbsPath := s.gitClient.GetFilePath(repoFilePath)

	if err := s.gitClient.Open(); err != nil {
		return nil, err
	}
	var replacementPaths []string
	if dagState.RemoteDeleted {
		trackedPaths, err := s.trackedItemRepoPaths()
		if err != nil {
			return nil, err
		}
		for _, trackedPath := range trackedPaths[dagID] {
			if trackedPath != repoFilePath {
				replacementPaths = append(replacementPaths, trackedPath)
			}
		}
	}

	content, err := s.readItemFile(dagID, dagState.Kind, dagFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read sync item file: %w", err)
	}

	executable := dagState.Kind == SyncItemKindFile && dagState.LastSyncedExecutable
	if info, err := os.Stat(dagFilePath); err == nil && dagState.Kind == SyncItemKindFile {
		executable = executableMode(info.Mode(), executable)
	}
	perm := os.FileMode(0600)
	if executable {
		perm = 0700
	}
	if message == "" {
		message = fmt.Sprintf("Update %s", dagID)
	}
	commitHash, err := s.gitClient.commitAndPush(ctx, message, func() error {
		if err := safeWriteFileWithinBase(s.gitClient.repoPath, repoAbsPath, content, perm); err != nil {
			return fmt.Errorf("failed to write to repo: %w", err)
		}
		if err := s.gitClient.RemoveFiles(replacementPaths); err != nil {
			return err
		}
		return s.gitClient.addFileMode(repoFilePath, executable)
	})
	if err != nil {
		return nil, err
	}

	// Update the item state to synced.
	contentHash := ComputeContentHash(content)
	newState := s.newSyncedItemState(dagState.Kind, fileExtension, commitHash, contentHash, executable)
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

	successfulDAGs := make([]string, 0, len(publishTargets))
	if message == "" {
		message = "Update sync items"
	}
	commitHash, err := s.gitClient.commitAndPush(ctx, message, func() error {
		for _, dagID := range publishTargets {
			dagState := state.Items[dagID]
			fileExtension := s.syncItemFileExtension(dagID, dagState)
			dagFilePath, err := s.safeItemFilePath(dagID, dagState.Kind, fileExtension)
			if err != nil {
				return err
			}
			repoFilePath, err := s.safeItemRepoPath(dagID, dagState.Kind, fileExtension)
			if err != nil {
				return err
			}
			repoAbsPath := s.gitClient.GetFilePath(repoFilePath)

			content, err := s.readItemFile(dagID, dagState.Kind, dagFilePath)
			if err != nil {
				result.Errors = append(result.Errors, SyncError{ItemID: dagID, Message: err.Error()})
				continue
			}

			executable := dagState.Kind == SyncItemKindFile && dagState.LastSyncedExecutable
			if info, err := os.Stat(dagFilePath); err == nil && dagState.Kind == SyncItemKindFile {
				executable = executableMode(info.Mode(), executable)
			}
			perm := os.FileMode(0600)
			if executable {
				perm = 0700
			}
			if err := safeWriteFileWithinBase(s.gitClient.repoPath, repoAbsPath, content, perm); err != nil {
				result.Errors = append(result.Errors, SyncError{ItemID: dagID, Message: err.Error()})
				continue
			}
			if err := s.gitClient.addFileMode(repoFilePath, executable); err != nil {
				return err
			}

			successfulDAGs = append(successfulDAGs, dagID)
		}

		if len(successfulDAGs) == 0 {
			return fmt.Errorf("all files failed to copy: %d error(s)", len(result.Errors))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Update state only for successfully published items.
	for _, dagID := range successfulDAGs {
		dagState := state.Items[dagID]
		fileExtension := s.syncItemFileExtension(dagID, dagState)
		dagFilePath, err := s.safeItemFilePath(dagID, dagState.Kind, fileExtension)
		if err != nil {
			return nil, err
		}
		content, _ := s.readItemFile(dagID, dagState.Kind, dagFilePath)
		contentHash := ComputeContentHash(content)
		executable := dagState.Kind == SyncItemKindFile && dagState.LastSyncedExecutable
		if info, err := os.Stat(dagFilePath); err == nil && dagState.Kind == SyncItemKindFile {
			executable = executableMode(info.Mode(), executable)
		}
		newState := s.newSyncedItemState(dagState.Kind, fileExtension, commitHash, contentHash, executable)
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
	if dagState.RemoteDeleted {
		if err := s.removeItemFile(dagID, dagState); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove remotely deleted sync item: %w", err)
		}
		delete(state.Items, dagID)
		return s.stateManager.Save(state)
	}

	fileExtension := s.syncItemFileExtension(dagID, dagState)
	repoFilePath, err := s.safeItemRepoPath(dagID, dagState.Kind, fileExtension)
	if err != nil {
		return err
	}
	dagFilePath, err := s.safeItemFilePath(dagID, dagState.Kind, fileExtension)
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
	executable := dagState.LastSyncedExecutable
	commitHash := dagState.BaseCommit
	if dagState.Status == StatusConflict {
		executable = dagState.RemoteExecutable
		commitHash = dagState.RemoteCommit
	}
	if err := s.writeItemFile(dagID, dagState.Kind, dagFilePath, repoContent, executable); err != nil {
		return fmt.Errorf("failed to write sync item file: %w", err)
	}

	// Update state
	contentHash := ComputeContentHash(repoContent)
	newState := s.newSyncedItemState(dagState.Kind, fileExtension, commitHash, contentHash, executable)
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
	if dagState.Status == StatusSynced || dagState.Status == StatusMissing {
		matchesBase, err := s.localItemMatchesBase(itemID, dagState)
		if err != nil {
			return fmt.Errorf("failed to verify sync item %q before deletion: %w", itemID, err)
		}
		if !matchesBase && !force {
			if err := s.stateManager.Save(state); err != nil {
				return err
			}
			return &ValidationError{
				Field:   itemID,
				Message: "sync item has local modifications — use force to delete anyway",
			}
		}
	}

	// Reject local changes without force.
	if (dagState.Status == StatusModified || dagState.Status == StatusConflict) && !force {
		return &ValidationError{
			Field:   itemID,
			Message: "sync item has local modifications — use force to delete anyway",
		}
	}

	// Ensure repo is ready
	if err := s.gitClient.Open(); err != nil {
		return err
	}
	fileExtension := s.syncItemFileExtension(itemID, dagState)
	repoPath, err := s.safeItemRepoPath(itemID, dagState.Kind, fileExtension)
	if err != nil {
		return err
	}
	var replacementPaths []string
	if dagState.RemoteDeleted {
		trackedPaths, err := s.trackedItemRepoPaths()
		if err != nil {
			return err
		}
		replacementPaths = trackedPaths[itemID]
		if len(replacementPaths) == 0 {
			if err := s.removeItemFile(itemID, dagState); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove local file %q: %w", itemID, err)
			}
			delete(state.Items, itemID)
			return s.stateManager.Save(state)
		}
	}

	if message == "" {
		message = fmt.Sprintf("Delete %s", itemID)
	}
	commitHash, err := s.gitClient.commitAndPush(ctx, message, func() error {
		if dagState.RemoteDeleted {
			if err := s.gitClient.RemoveFiles(replacementPaths); err != nil {
				return fmt.Errorf("failed to stage removal of %q: %w", itemID, err)
			}
			return nil
		}
		if err := s.gitClient.RemoveFile(repoPath); err != nil && dagState.Status != StatusMissing {
			return fmt.Errorf("failed to stage removal of %q: %w", itemID, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.removeItemFile(itemID, dagState); err != nil && !os.IsNotExist(err) {
		s.markRemoteDeleteConflict(dagState, commitHash)
		s.updateSuccessStateWithCommit(state, commitHash)
		return fmt.Errorf("remote deletion committed but local file %q could not be removed: %w", itemID, err)
	}

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
		itemID           string
		status           SyncStatus
		repoPath         string
		replacementPaths []string
		remoteDeleted    bool
	}
	var targets []deleteTarget
	seen := make(map[string]struct{}, len(itemIDs))
	hasRemoteDeleted := false

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
		if dagState.Status == StatusSynced || dagState.Status == StatusMissing {
			matchesBase, err := s.localItemMatchesBase(itemID, dagState)
			if err != nil {
				return nil, fmt.Errorf("failed to verify sync item %q before deletion: %w", itemID, err)
			}
			if !matchesBase && !force {
				if err := s.stateManager.Save(state); err != nil {
					return nil, err
				}
				return nil, &ValidationError{
					Field:   itemID,
					Message: "sync item has local modifications — use force to delete anyway",
				}
			}
		}

		if (dagState.Status == StatusModified || dagState.Status == StatusConflict) && !force {
			return nil, &ValidationError{
				Field:   itemID,
				Message: "sync item has local modifications — use force to delete anyway",
			}
		}

		fileExtension := s.syncItemFileExtension(itemID, dagState)
		repoPath, err := s.safeItemRepoPath(itemID, dagState.Kind, fileExtension)
		if err != nil {
			return nil, err
		}

		targets = append(targets, deleteTarget{
			itemID:        itemID,
			status:        dagState.Status,
			repoPath:      repoPath,
			remoteDeleted: dagState.RemoteDeleted,
		})
		hasRemoteDeleted = hasRemoteDeleted || dagState.RemoteDeleted
	}

	if len(targets) == 0 {
		return nil, nil
	}

	// Phase 2: execute deletion.
	if err := s.gitClient.Open(); err != nil {
		return nil, err
	}

	if hasRemoteDeleted {
		trackedPaths, err := s.trackedItemRepoPaths()
		if err != nil {
			return nil, err
		}
		for i := range targets {
			if targets[i].remoteDeleted {
				targets[i].replacementPaths = trackedPaths[targets[i].itemID]
			}
		}
	}

	staged := false
	for _, t := range targets {
		if !t.remoteDeleted || len(t.replacementPaths) > 0 {
			staged = true
		}
	}

	commitHash := state.LastSyncCommit
	if staged {
		if message == "" {
			message = fmt.Sprintf("Delete %d sync item(s)", len(targets))
		}
		commitHash, err = s.gitClient.commitAndPush(ctx, message, func() error {
			for _, t := range targets {
				if t.remoteDeleted {
					if len(t.replacementPaths) == 0 {
						continue
					}
					if err := s.gitClient.RemoveFiles(t.replacementPaths); err != nil {
						return fmt.Errorf("failed to stage removal of %q: %w", t.itemID, err)
					}
					continue
				}
				if err := s.gitClient.RemoveFile(t.repoPath); err != nil && t.status != StatusMissing {
					return fmt.Errorf("failed to stage removal of %q: %w", t.itemID, err)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	var deleted []string
	var removeErr error
	for _, t := range targets {
		itemState := state.Items[t.itemID]
		if err := s.removeItemFile(t.itemID, itemState); err != nil && !os.IsNotExist(err) {
			if !t.remoteDeleted || len(t.replacementPaths) > 0 {
				s.markRemoteDeleteConflict(itemState, commitHash)
			}
			removeErr = errors.Join(removeErr, fmt.Errorf("failed to remove local file %q: %w", t.itemID, err))
			continue
		}
		delete(state.Items, t.itemID)
		deleted = append(deleted, t.itemID)
	}
	sort.Strings(deleted)
	if staged {
		s.updateSuccessStateWithCommit(state, commitHash)
	} else if err := s.stateManager.Save(state); err != nil {
		return nil, err
	}
	if removeErr != nil {
		return deleted, fmt.Errorf("remote deletion committed but local cleanup failed: %w", removeErr)
	}

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
		repoPath, err := s.safeItemRepoPath(dagID, dagState.Kind, s.syncItemFileExtension(dagID, dagState))
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

	if message == "" {
		message = fmt.Sprintf("Delete %d missing sync item(s)", len(missingIDs))
	}
	commitHash, err := s.gitClient.commitAndPush(ctx, message, func() error {
		// Missing items may already be absent from the repository.
		_ = s.gitClient.RemoveFiles(repoPaths)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// On success: delete all entries
	for _, dagID := range missingIDs {
		delete(state.Items, dagID)
	}
	s.updateSuccessStateWithCommit(state, commitHash)

	return missingIDs, nil
}

// Move renames a tracked item.
func (s *serviceImpl) Move(ctx context.Context, oldID, newID, message string, force bool) error {
	if err := s.validatePushEnabled(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.stateManager.GetState()
	if err != nil {
		return err
	}

	oldState, exists := state.Items[oldID]
	kind := SyncItemKindForID(oldID)
	if exists && oldState.Kind != "" {
		kind = oldState.Kind
	}
	normalized, err := normalizeItemID(oldID, kind)
	if err != nil {
		return err
	}
	if normalized != oldID {
		return &InvalidDAGIDError{DAGID: oldID, Reason: fmt.Sprintf("must be normalized as %q", normalized)}
	}
	normalized, err = normalizeItemID(newID, kind)
	if err != nil {
		return err
	}
	if normalized != newID {
		return &InvalidDAGIDError{DAGID: newID, Reason: fmt.Sprintf("must be normalized as %q", normalized)}
	}
	if !exists {
		if SyncItemKindForID(oldID) != SyncItemKindForID(newID) {
			return &ValidationError{Field: "newItemId", Message: "source and destination must have the same item type"}
		}
		return &DAGNotFoundError{DAGID: oldID}
	}
	if oldState.Kind == SyncItemKindWikiPageAsset && !isValidAssetItemID(newID) {
		return &ValidationError{Field: "newItemId", Message: "destination is not a valid attachment path"}
	}
	if oldState.Kind == SyncItemKindWikiPage && !isWikiPageFile(newID) {
		return &ValidationError{Field: "newItemId", Message: "source and destination must have the same item type"}
	}
	if oldState.Kind == SyncItemKindDAG && SyncItemKindForID(newID) != SyncItemKindDAG {
		return &ValidationError{Field: "newItemId", Message: "source and destination must have the same item type"}
	}
	if oldState.Kind == SyncItemKindFile {
		item, ok := s.repoFileItem(TrackedFile{Path: newID})
		if !ok || item.kind != SyncItemKindFile {
			return &ValidationError{Field: "newItemId", Message: "destination must remain a supporting file"}
		}
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
	oldLocalPath, err := s.safeItemFilePath(oldID, oldState.Kind, fileExtension)
	if err != nil {
		return err
	}
	newLocalPath, err := s.safeItemFilePath(newID, oldState.Kind, fileExtension)
	if err != nil {
		return err
	}
	oldRepoPath, err := s.safeItemRepoPath(oldID, oldState.Kind, fileExtension)
	if err != nil {
		return err
	}
	newRepoPath, err := s.safeItemRepoPath(newID, oldState.Kind, fileExtension)
	if err != nil {
		return err
	}
	for itemID, itemState := range state.Items {
		if itemID == oldID || itemID == newID {
			continue
		}
		itemPath, err := s.safeItemFilePath(itemID, itemState.Kind, itemState.FileExtension)
		if err == nil && filepath.Clean(itemPath) == filepath.Clean(newLocalPath) {
			return &ValidationError{Field: "newItemId", Message: "destination collides with another sync item"}
		}
	}

	// Read through the no-follow path before changing local or Git state.
	content, fileInfo, readErr := s.readItemFileInfo(oldID, oldState.Kind, oldLocalPath)
	oldFileExists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("failed to read source file: %w", readErr)
	}
	if !oldFileExists {
		if oldState.Status != StatusMissing {
			return &ValidationError{
				Field:   oldID,
				Message: "source file does not exist on disk and destination file is not present",
			}
		}
		content, fileInfo, readErr = s.readItemFileInfo(newID, oldState.Kind, newLocalPath)
		if os.IsNotExist(readErr) {
			return &ValidationError{
				Field:   oldID,
				Message: "source file does not exist on disk and destination file is not present",
			}
		}
		if readErr != nil {
			return fmt.Errorf("failed to read destination file: %w", readErr)
		}
	} else if _, _, err := s.inspectItemFile(newID, oldState.Kind, newLocalPath, false); err == nil {
		return &ValidationError{Field: "newItemId", Message: "destination file already exists"}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to inspect destination file: %w", err)
	}

	if err := s.gitClient.Open(); err != nil {
		return err
	}

	executable := oldState.Kind == SyncItemKindFile && executableMode(fileInfo.Mode(), oldState.LastSyncedExecutable)
	newRepoAbsPath := s.gitClient.GetFilePath(newRepoPath)
	perm := os.FileMode(0600)
	if executable {
		perm = 0700
	}
	if message == "" {
		message = fmt.Sprintf("Move %s to %s", oldID, newID)
	}
	commitHash, err := s.gitClient.commitAndPush(ctx, message, func() error {
		if err := safeWriteFileWithinBase(s.gitClient.repoPath, newRepoAbsPath, content, perm); err != nil {
			return fmt.Errorf("failed to write to repo: %w", err)
		}
		// The source can already be absent in retroactive moves.
		_ = s.gitClient.RemoveFile(oldRepoPath)
		return s.gitClient.addFileMode(newRepoPath, executable)
	})
	if err != nil {
		return err
	}
	if oldFileExists {
		if err := s.renameItemFile(oldID, newID, oldState.Kind, oldLocalPath, newLocalPath); err != nil {
			return fmt.Errorf("remote move committed but local rename failed; pull to reconcile: %w", err)
		}
	}

	contentHash := ComputeContentHash(content)
	newItemState := s.newSyncedItemState(oldState.Kind, fileExtension, commitHash, contentHash, executable)
	if fi, _, err := s.inspectItemFile(newID, oldState.Kind, newLocalPath, false); err == nil {
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
	repoWikiDir, err := s.selectRepoWikiDir()
	if err != nil {
		status.Summary = SummaryError
		status.LastError = new(err.Error())
		return status, nil
	}
	s.repoWikiDir = repoWikiDir

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
		Kind:          itemState.Kind,
		FileExtension: s.syncItemFileExtension(itemID, itemState),
		Status:        itemState.Status,
		RemoteDeleted: itemState.RemoteDeleted,
	}
	localPath, err := s.safeItemFilePath(itemID, itemState.Kind, diff.FileExtension)
	if err != nil {
		return nil, err
	}

	knownBinary := itemState.Kind == SyncItemKindWikiPageAsset
	localPresent := itemState.Status != StatusMissing
	var localInfo os.FileInfo
	var localBinary bool
	if localPresent {
		localInfo, localBinary, err = s.inspectItemFile(itemID, itemState.Kind, localPath, !knownBinary)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect local file: %w", err)
		}
	}

	var remoteSize int64
	var remoteBinary bool
	remotePresent := false
	switch itemState.Status {
	case StatusSynced:
		diff.RemoteCommit = itemState.BaseCommit
		remotePresent = localPresent
		if localInfo != nil {
			remoteSize = localInfo.Size()
			remoteBinary = localBinary
		}

	case StatusModified:
		diff.RemoteCommit = itemState.BaseCommit
		remotePresent = itemState.BaseCommit != ""

	case StatusConflict:
		diff.RemoteCommit = itemState.RemoteCommit
		diff.RemoteAuthor = itemState.RemoteAuthor
		diff.RemoteMessage = itemState.RemoteMessage
		remotePresent = !itemState.RemoteDeleted && itemState.RemoteCommit != ""

	case StatusUntracked:
		// No remote version for untracked files

	case StatusMissing:
		diff.RemoteCommit = itemState.BaseCommit
		remotePresent = itemState.BaseCommit != ""
	}

	if remotePresent && itemState.Status != StatusSynced {
		remoteSize, remoteBinary, err = s.inspectRemoteItem(
			itemID,
			itemState.Kind,
			diff.FileExtension,
			diff.RemoteCommit,
			!knownBinary,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect remote file: %w", err)
		}
	}

	if itemState.Kind == SyncItemKindFile {
		localExecutable := itemState.LastSyncedExecutable
		if localInfo != nil {
			localExecutable = executableMode(localInfo.Mode(), localExecutable)
		}
		diff.LocalExecutable = &localExecutable
		remoteExecutable := itemState.LastSyncedExecutable
		if itemState.Status == StatusConflict {
			remoteExecutable = itemState.RemoteExecutable
		}
		if !itemState.RemoteDeleted && itemState.Status != StatusUntracked {
			diff.RemoteExecutable = &remoteExecutable
		}
	}

	diff.Binary = knownBinary || localBinary || remoteBinary
	if diff.Binary {
		if localPresent {
			size := localInfo.Size()
			diff.LocalSize = &size
		}
		if remotePresent {
			size := remoteSize
			diff.RemoteSize = &size
		}
		return diff, nil
	}

	var localContent []byte
	if localPresent {
		localContent, err = s.readItemFile(itemID, itemState.Kind, localPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read local file: %w", err)
		}
	}

	var remoteContent []byte
	if remotePresent {
		if itemState.Status == StatusSynced {
			remoteContent = localContent
		} else {
			remoteContent, err = s.fetchRemoteContent(itemID, itemState.Kind, diff.FileExtension, diff.RemoteCommit)
			if err != nil {
				return nil, fmt.Errorf("failed to read remote file: %w", err)
			}
		}
	}
	diff.LocalContent = string(localContent)
	diff.RemoteContent = string(remoteContent)

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

func isBinaryReader(reader io.Reader) (bool, error) {
	buffered := bufio.NewReader(reader)
	for {
		r, size, err := buffered.ReadRune()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if r == 0 || r == utf8.RuneError && size == 1 {
			return true, nil
		}
	}
}

func (s *serviceImpl) inspectRemoteItem(
	itemID string,
	kind SyncItemKind,
	fileExtension string,
	commitHash string,
	detectBinary bool,
) (int64, bool, error) {
	if err := s.gitClient.Open(); err != nil {
		return 0, false, err
	}
	repoPath, err := s.safeItemRepoPath(itemID, kind, fileExtension)
	if err != nil {
		return 0, false, err
	}
	return s.gitClient.inspectFileAtCommit(repoPath, commitHash, detectBinary)
}

// fetchRemoteContent retrieves item content from a specific commit.
func (s *serviceImpl) fetchRemoteContent(itemID string, kind SyncItemKind, fileExtension, commitHash string) ([]byte, error) {
	if commitHash == "" {
		return nil, nil
	}
	if err := s.gitClient.Open(); err != nil {
		return nil, err
	}
	repoPath, err := s.safeItemRepoPath(itemID, kind, fileExtension)
	if err != nil {
		return nil, err
	}
	content, err := s.gitClient.GetFileContentAtCommit(repoPath, commitHash)
	if err != nil {
		return nil, err
	}
	return content, nil
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

		kind := SyncItemKindForID(dagID)
		if dagState := state.Items[dagID]; dagState != nil && dagState.Kind != "" {
			kind = dagState.Kind
		}
		normalized, err := normalizeItemID(dagID, kind)
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

func normalizeDAGID(dagID string) (string, error) {
	if dagID == "" {
		return "", &InvalidDAGIDError{DAGID: dagID, Reason: "cannot be empty"}
	}

	normalized := normalizeDAGIDSeparators(dagID)
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
	file, err := safeOpenFileWithinBase(baseDir, targetPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()
	return io.ReadAll(file)
}

func safeReadFileInfoWithinBase(baseDir, targetPath string) ([]byte, os.FileInfo, error) {
	file, err := safeOpenFileWithinBase(baseDir, targetPath)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = file.Close()
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, err
	}
	return content, info, nil
}

func safeInspectFileWithinBase(baseDir, targetPath string, detectBinary bool) (os.FileInfo, bool, error) {
	if !detectBinary {
		info, err := safeFileInfoWithinBase(baseDir, targetPath)
		return info, false, err
	}
	file, err := safeOpenFileWithinBase(baseDir, targetPath)
	if err != nil {
		return nil, false, err
	}
	defer func() {
		_ = file.Close()
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	binary, err := isBinaryReader(file)
	if err != nil {
		return nil, false, err
	}
	return info, binary, nil
}

func safeFileInfoWithinBase(baseDir, targetPath string) (os.FileInfo, error) {
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
	cleanPath := filepath.Clean(relPath)
	if err := ensureSafeRootDirPath(root, filepath.Dir(cleanPath), targetPath, "inspect", false); err != nil {
		return nil, err
	}
	info, err := root.Lstat(cleanPath)
	if err != nil {
		return nil, err
	}
	if err := rejectUnsafeFileInfo(info, targetPath, "inspect"); err != nil {
		return nil, err
	}
	return info, nil
}

func safeHashFileWithinBase(baseDir, targetPath string) (string, error) {
	file, err := safeOpenFileWithinBase(baseDir, targetPath)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()
	return computeContentHash(file)
}

func safeHashFileInfoWithinBase(baseDir, targetPath string) (string, os.FileInfo, error) {
	file, err := safeOpenFileWithinBase(baseDir, targetPath)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		_ = file.Close()
	}()
	info, err := file.Stat()
	if err != nil {
		return "", nil, err
	}
	hash, err := computeContentHash(file)
	if err != nil {
		return "", nil, err
	}
	return hash, info, nil
}

func safeOpenFileWithinBase(baseDir, targetPath string) (*os.File, error) {
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
	if err := validateOpenedRootFile(root, relPath, targetPath, "read", file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func safeWriteFileWithinBase(baseDir, targetPath string, content []byte, perm os.FileMode) error {
	return safeWriteStreamWithinBase(baseDir, targetPath, bytes.NewReader(content), perm)
}

func safeCopyFileWithinBases(sourceBase, sourcePath, targetBase, targetPath string, perm os.FileMode) error {
	source, err := safeOpenFileWithinBase(sourceBase, sourcePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = source.Close()
	}()
	return safeWriteStreamWithinBase(targetBase, targetPath, source, perm)
}

func safeWriteStreamWithinBase(baseDir, targetPath string, content io.Reader, perm os.FileMode) error {
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
	if _, err := io.Copy(file, content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(perm); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func safeRemoveFileWithinBase(baseDir, targetPath string) error {
	relPath, err := relativePathWithinBase(baseDir, targetPath)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		return err
	}
	defer func() {
		_ = root.Close()
	}()
	if err := rejectUnsafeRootPath(root, relPath, targetPath, "remove", true); err != nil {
		return err
	}
	return root.Remove(relPath)
}

func safeRenameFileWithinBase(baseDir, oldPath, newPath string) error {
	oldRel, err := relativePathWithinBase(baseDir, oldPath)
	if err != nil {
		return err
	}
	newRel, err := relativePathWithinBase(baseDir, newPath)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		return err
	}
	defer func() {
		_ = root.Close()
	}()
	if err := rejectUnsafeRootPath(root, oldRel, oldPath, "rename", false); err != nil {
		return err
	}
	if err := ensureSafeRootDirPath(root, filepath.Dir(newRel), newPath, "rename", true); err != nil {
		return err
	}
	if _, err := root.Lstat(newRel); err == nil {
		return fmt.Errorf("destination file already exists: %s", newPath)
	} else if !os.IsNotExist(err) {
		return err
	}
	return root.Rename(oldRel, newRel)
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

func (s *serviceImpl) writeItemFile(itemID string, kind SyncItemKind, filePath string, content []byte, executable bool) error {
	if _, err := normalizeItemID(itemID, kind); err != nil {
		return err
	}
	perm := os.FileMode(0600)
	if kind == SyncItemKindFile && executable {
		perm = 0700
	}
	return safeWriteFileWithinBase(s.itemBaseDir(kind), filePath, content, perm)
}

func (s *serviceImpl) readItemFile(itemID string, kind SyncItemKind, filePath string) ([]byte, error) {
	if _, err := normalizeItemID(itemID, kind); err != nil {
		return nil, err
	}
	return safeReadFileWithinBase(s.itemBaseDir(kind), filePath)
}

func (s *serviceImpl) readItemFileInfo(itemID string, kind SyncItemKind, filePath string) ([]byte, os.FileInfo, error) {
	if _, err := normalizeItemID(itemID, kind); err != nil {
		return nil, nil, err
	}
	return safeReadFileInfoWithinBase(s.itemBaseDir(kind), filePath)
}

func (s *serviceImpl) inspectItemFile(
	itemID string,
	kind SyncItemKind,
	filePath string,
	detectBinary bool,
) (os.FileInfo, bool, error) {
	if _, err := normalizeItemID(itemID, kind); err != nil {
		return nil, false, err
	}
	return safeInspectFileWithinBase(s.itemBaseDir(kind), filePath, detectBinary)
}

func (s *serviceImpl) hashItemFile(itemID string, kind SyncItemKind, filePath string) (string, error) {
	if _, err := normalizeItemID(itemID, kind); err != nil {
		return "", err
	}
	return safeHashFileWithinBase(s.itemBaseDir(kind), filePath)
}

func (s *serviceImpl) hashItemFileInfo(itemID string, kind SyncItemKind, filePath string) (string, os.FileInfo, error) {
	if _, err := normalizeItemID(itemID, kind); err != nil {
		return "", nil, err
	}
	return safeHashFileInfoWithinBase(s.itemBaseDir(kind), filePath)
}

func (s *serviceImpl) copyRepoItemFile(itemID string, kind SyncItemKind, repoPath, filePath string, executable bool) error {
	if _, err := normalizeItemID(itemID, kind); err != nil {
		return err
	}
	perm := os.FileMode(0600)
	if kind == SyncItemKindFile && executable {
		perm = 0700
	}
	return safeCopyFileWithinBases(s.gitClient.repoPath, repoPath, s.itemBaseDir(kind), filePath, perm)
}

func (s *serviceImpl) removeItemFile(itemID string, itemState *SyncItemState) error {
	filePath, err := s.safeItemFilePath(itemID, itemState.Kind, itemState.FileExtension)
	if err != nil {
		return err
	}
	return safeRemoveFileWithinBase(s.itemBaseDir(itemState.Kind), filePath)
}

func (s *serviceImpl) renameItemFile(oldID, newID string, kind SyncItemKind, oldPath, newPath string) error {
	if _, err := normalizeItemID(oldID, kind); err != nil {
		return err
	}
	if _, err := normalizeItemID(newID, kind); err != nil {
		return err
	}
	return safeRenameFileWithinBase(s.itemBaseDir(kind), oldPath, newPath)
}

func (s *serviceImpl) safeItemFilePath(itemID string, kind SyncItemKind, extension string) (string, error) {
	normalized, err := normalizeItemID(itemID, kind)
	if err != nil {
		return "", err
	}
	baseDir := s.itemBaseDir(kind)
	localID := normalized
	switch kind {
	case SyncItemKindWikiPage, SyncItemKindWikiPageAsset:
		localID = strings.TrimPrefix(normalized, wikiRepoDirForID(normalized)+"/")
	case SyncItemKindFile:
		extension = ""
	case SyncItemKindDAG:
	default:
		return "", &ValidationError{Field: itemID, Message: "unsupported sync item kind"}
	}
	return safeJoinWithinBase(baseDir, filepath.FromSlash(localID+normalizeItemExtension(kind, extension)))
}

func (s *serviceImpl) safeItemRepoPath(itemID string, kind SyncItemKind, extension string) (string, error) {
	normalized, err := normalizeItemID(itemID, kind)
	if err != nil {
		return "", err
	}
	repoPath := normalized + normalizeItemExtension(kind, extension)
	if s.cfg != nil && s.cfg.Path != "" {
		repoPath = path.Join(filepath.ToSlash(s.cfg.Path), repoPath)
	}
	safePath, err := safeJoinWithinBase(s.gitClient.repoPath, filepath.FromSlash(repoPath))
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(s.gitClient.repoPath, safePath)
	if err != nil {
		return "", &InvalidDAGIDError{DAGID: itemID, Reason: "cannot resolve repository path"}
	}
	return filepath.ToSlash(relPath), nil
}

func normalizeItemExtension(kind SyncItemKind, extension string) string {
	switch kind {
	case SyncItemKindFile, SyncItemKindWikiPageAsset:
		return ""
	case SyncItemKindWikiPage:
		if strings.EqualFold(extension, wikiPageExtension) {
			return extension
		}
		return wikiPageExtension
	case SyncItemKindDAG:
		return normalizeDAGFileExtension(extension)
	default:
		return ""
	}
}

func normalizeItemID(itemID string, kind SyncItemKind) (string, error) {
	if kind != SyncItemKindFile {
		return normalizeDAGID(itemID)
	}
	if itemID == "" {
		return "", &InvalidDAGIDError{DAGID: itemID, Reason: "cannot be empty"}
	}
	normalized := normalizeDAGIDSeparators(itemID)
	if path.IsAbs(normalized) || looksLikeWindowsAbsolutePath(normalized) {
		return "", &InvalidDAGIDError{DAGID: itemID, Reason: "absolute paths are not allowed"}
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", &InvalidDAGIDError{DAGID: itemID, Reason: "path traversal is not allowed"}
	}
	return clean, nil
}

func isExecutable(mode os.FileMode) bool {
	return mode.Perm()&0100 != 0
}

func executableMode(mode os.FileMode, fallback bool) bool {
	// Windows does not expose Git's executable bit through file modes.
	if runtime.GOOS == "windows" {
		return fallback
	}

	return isExecutable(mode)
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

func (s *serviceImpl) itemBaseDir(kind SyncItemKind) string {
	if kind == SyncItemKindWikiPage || kind == SyncItemKindWikiPageAsset {
		return s.localWikiDir()
	}
	return s.dagsDir
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
	kind := SyncItemKindForID(dagID)
	if dagState != nil && dagState.Kind != "" {
		kind = dagState.Kind
	}

	if kind == SyncItemKindFile {
		if dagState != nil {
			dagState.FileExtension = ""
		}
		return ""
	}
	if kind == SyncItemKindWikiPageAsset {
		if dagState != nil {
			dagState.Kind = SyncItemKindWikiPageAsset
			dagState.FileExtension = ""
		}
		return ""
	}
	if kind == SyncItemKindWikiPage {
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

	if s.gitClient != nil {
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
func (s *serviceImpl) newSyncedItemState(
	kind SyncItemKind,
	fileExtension, commitHash, contentHash string,
	executable bool,
) *SyncItemState {
	now := time.Now()
	return &SyncItemState{
		Status:               StatusSynced,
		Kind:                 kind,
		FileExtension:        normalizeItemExtension(kind, fileExtension),
		BaseCommit:           commitHash,
		LastSyncedHash:       contentHash,
		LastSyncedAt:         &now,
		LocalHash:            contentHash,
		LastSyncedExecutable: executable,
		LocalExecutable:      executable,
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
func (s *serviceImpl) buildPullMessage(alreadyUpToDate bool, synced, deleted, conflicts []string) string {
	if len(conflicts) > 0 {
		return fmt.Sprintf("Pulled with %d conflict(s)", len(conflicts))
	}
	if alreadyUpToDate && len(deleted) == 0 {
		return "Already up to date"
	}
	return fmt.Sprintf("Synced %d and deleted %d sync item(s)", len(synced), len(deleted))
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
