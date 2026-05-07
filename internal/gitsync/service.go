// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/workspace"
)

// Service defines the interface for Git sync operations.
type Service interface {
	// Pull fetches and merges changes from the remote repository.
	Pull(ctx context.Context) (*SyncResult, error)

	// Publish commits and pushes a single DAG to the remote.
	Publish(ctx context.Context, dagID, message string, force bool) (*SyncResult, error)

	// PublishAll commits and pushes the specified DAGs.
	PublishAll(ctx context.Context, message string, dagIDs []string) (*SyncResult, error)

	// Discard discards local changes for a DAG.
	Discard(ctx context.Context, dagID string) error

	// GetStatus returns the overall sync status.
	GetStatus(ctx context.Context) (*OverallStatus, error)

	// GetDAGStatus returns the sync status for a specific DAG.
	GetDAGStatus(ctx context.Context, dagID string) (*DAGState, error)

	// GetDAGDiff returns the diff between local and remote versions of a DAG.
	GetDAGDiff(ctx context.Context, dagID string) (*DAGDiff, error)

	// Forget removes state entries for missing/untracked/conflict items.
	Forget(ctx context.Context, itemIDs []string) ([]string, error)

	// Cleanup removes all missing entries from state.
	Cleanup(ctx context.Context) ([]string, error)

	// Delete removes an item from remote (git rm + commit + push), local disk, and state.
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
	DAGID   string `json:"dagId,omitempty"`
	Message string `json:"message"`
}

// OverallStatus represents the overall sync status.
type OverallStatus struct {
	Enabled        bool                 `json:"enabled"`
	Repository     string               `json:"repository,omitempty"`
	Branch         string               `json:"branch,omitempty"`
	Summary        SummaryStatus        `json:"summary"`
	LastSyncAt     *time.Time           `json:"lastSyncAt,omitempty"`
	LastSyncCommit string               `json:"lastSyncCommit,omitempty"`
	LastSyncStatus string               `json:"lastSyncStatus,omitempty"`
	LastError      *string              `json:"lastError,omitempty"`
	DAGs           map[string]*DAGState `json:"dags,omitempty"`
	Counts         StatusCounts         `json:"counts"`
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

// DAGDiff represents the diff between local and remote versions of a DAG.
type DAGDiff struct {
	DAGID         string     `json:"dagId"`
	Status        SyncStatus `json:"status"`
	LocalContent  string     `json:"localContent"`
	RemoteContent string     `json:"remoteContent,omitempty"`
	RemoteCommit  string     `json:"remoteCommit,omitempty"`
	RemoteAuthor  string     `json:"remoteAuthor,omitempty"`
	RemoteMessage string     `json:"remoteMessage,omitempty"`
}

// fileExtensionForID returns the file extension for a given ID.
func fileExtensionForID(id string) string {
	if isMemoryFile(id) || isSkillFile(id) || isSoulFile(id) || isDocFile(id) {
		return ".md"
	}
	return ".yaml"
}

// serviceImpl implements the Service interface.
type serviceImpl struct {
	cfg          *Config
	dagsDir      string
	baseConfig   string
	dataDir      string
	stateManager *StateManager
	gitClient    *GitClient
	mu           sync.Mutex
	stopCh       chan struct{}
	running      bool
}

// NewService creates a new Git sync service.
func NewService(cfg *Config, dagsDir, dataDir string, baseConfigPath ...string) Service {
	repoPath := filepath.Join(dataDir, "gitsync", "repo")
	baseConfig := ""
	if len(baseConfigPath) > 0 {
		baseConfig = baseConfigPath[0]
	}
	return &serviceImpl{
		cfg:          cfg,
		dagsDir:      dagsDir,
		baseConfig:   baseConfig,
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

	// Sync files to DAGs directory and save state with sync metadata
	syncedDAGs, conflicts, err := s.syncFilesToDAGsDir(ctx, pullResult, currentCommit)
	if err != nil {
		result.Success = false
		result.Message = "Failed to sync files"
		result.Errors = append(result.Errors, SyncError{Message: err.Error()})
		s.updateLastSyncError(err)
		return result, err
	}

	result.Synced = syncedDAGs
	result.Conflicts = conflicts
	result.Success = true
	result.Message = s.buildPullMessage(pullResult.AlreadyUpToDate, syncedDAGs, conflicts)

	return result, nil
}

// syncFilesToDAGsDir syncs files from the repo to the DAGs directory.
// It updates sync metadata and saves state in a single write.
func (s *serviceImpl) syncFilesToDAGsDir(_ context.Context, pullResult *PullResult, commitHash string) ([]string, []string, error) {
	var synced []string
	var conflicts []string

	extensions := []string{".yaml", ".yml", ".md"}
	files, err := s.gitClient.ListFiles(extensions)
	if err != nil {
		return nil, nil, err
	}

	state, _ := s.stateManager.GetState()

	// Reconcile: detect missing/reappeared files before processing
	s.reconcile(state)

	// Refresh hashes to detect local modifications before checking for conflicts
	s.refreshLocalHashes(state)

	// Build set of DAG IDs present in remote repo for reconcileAfterPull
	repoFileSet := make(map[string]struct{}, len(files))
	for _, file := range files {
		repoFileSet[s.filePathToDAGID(file)] = struct{}{}
	}

	for _, file := range files {
		dagID := s.filePathToDAGID(file)

		// Only allow .md files from memory/, skills/, souls/, or docs/ directories
		if filepath.Ext(file) == ".md" && !isMemoryFile(dagID) && !isSkillFile(dagID) && !isSoulFile(dagID) && !isDocFile(dagID) {
			continue
		}
		repoFilePath := s.gitClient.GetFilePath(file)
		dagFilePath := s.dagIDToFilePath(dagID)

		// Read repo file content
		repoContent, err := os.ReadFile(repoFilePath) //nolint:gosec // path constructed from internal repo
		if err != nil {
			continue
		}
		repoHash := ComputeContentHash(repoContent)

		// Check if local file exists
		localContent, err := os.ReadFile(dagFilePath) //nolint:gosec // path constructed from internal dagsDir
		dagState := state.DAGs[dagID]

		if err != nil {
			// Before creating a new local file, check if this content matches
			// a missing item's hash (prevents duplicates after move+pull)
			for otherID, otherState := range state.DAGs {
				if otherID != dagID && otherState.Status == StatusMissing && otherState.LastSyncedHash == repoHash {
					// Auto-forget the stale missing entry
					delete(state.DAGs, otherID)
					break
				}
			}

			// Local file doesn't exist, create it
			if err := s.ensureDir(filepath.Dir(dagFilePath)); err != nil {
				continue
			}
			if err := os.WriteFile(dagFilePath, repoContent, 0600); err != nil {
				continue
			}
			now := time.Now()
			newState := &DAGState{
				Status:         StatusSynced,
				Kind:           KindForDAGID(dagID),
				BaseCommit:     pullResult.CurrentCommit,
				LastSyncedHash: repoHash,
				LastSyncedAt:   &now,
				LocalHash:      repoHash,
				ModifiedAt:     &now, // Added ModifiedAt for new files
			}
			if fi, err := os.Stat(dagFilePath); err == nil {
				updateStatCache(newState, fi)
			}
			state.DAGs[dagID] = newState
			synced = append(synced, dagID)
			continue
		}

		localHash := ComputeContentHash(localContent)

		// If local and remote content already match, ensure state reflects synced.
		if localHash == repoHash {
			if dagState == nil || dagState.Status != StatusSynced || dagState.BaseCommit != pullResult.CurrentCommit || dagState.LastSyncedHash != repoHash {
				now := time.Now()
				newState := &DAGState{
					Status:         StatusSynced,
					Kind:           KindForDAGID(dagID),
					BaseCommit:     pullResult.CurrentCommit,
					LastSyncedHash: repoHash,
					LastSyncedAt:   &now,
					LocalHash:      repoHash,
				}
				if fi, err := os.Stat(dagFilePath); err == nil {
					updateStatCache(newState, fi)
				}
				state.DAGs[dagID] = newState
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
				state.DAGs[dagID] = &DAGState{
					Status:             StatusConflict,
					Kind:               KindForDAGID(dagID),
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
					updateStatCache(state.DAGs[dagID], fi)
				}
				conflicts = append(conflicts, dagID)
			}
			// Local modified but remote unchanged - preserve local changes
			continue
		}

		// Only update local file if remote changed (and local wasn't modified)
		if localHash != repoHash {
			if err := os.WriteFile(dagFilePath, repoContent, 0600); err != nil {
				continue
			}
			now := time.Now()
			newState := &DAGState{
				Status:         StatusSynced,
				Kind:           KindForDAGID(dagID),
				BaseCommit:     pullResult.CurrentCommit,
				LastSyncedHash: repoHash,
				LastSyncedAt:   &now,
				LocalHash:      repoHash,
			}
			if fi, err := os.Stat(dagFilePath); err == nil {
				updateStatCache(newState, fi)
			}
			state.DAGs[dagID] = newState
			synced = append(synced, dagID)
		}
	}

	// Auto-forget items absent from both remote and local
	s.reconcileAfterPull(state, repoFileSet)

	// Scan for local DAGs not in the repo
	_ = s.scanLocalDAGs(state)

	// Update sync metadata and save state in a single write
	s.updateSuccessStateWithCommit(state, commitHash)

	return synced, conflicts, nil
}

// reconcileAfterPull removes state entries for items that are absent from both
// the remote repository and the local filesystem (auto-forget on pull).
func (s *serviceImpl) reconcileAfterPull(state *State, repoFileSet map[string]struct{}) {
	var toDelete []string
	for dagID, dagState := range state.DAGs {
		// Skip untracked items — they're local-only by definition
		if dagState.Status == StatusUntracked {
			continue
		}

		// Skip items present in remote
		if _, inRepo := repoFileSet[dagID]; inRepo {
			continue
		}

		// Check if local file exists
		filePath, err := s.safeDAGIDToFilePath(dagID)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filePath); err == nil {
			continue // file exists locally
		}

		// Item not in repo AND not on local disk — auto-forget
		toDelete = append(toDelete, dagID)
	}

	for _, dagID := range toDelete {
		delete(state.DAGs, dagID)
	}
}

// scanLocalDAGs scans the local DAGs directory and marks any DAGs not in state as untracked.
func (s *serviceImpl) scanLocalDAGs(state *State) error {
	extensions := map[string]bool{".yaml": true, ".yml": true}

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
		if isConfigFile(dagID) {
			continue
		}

		// Skip if already tracked
		if _, exists := state.DAGs[dagID]; exists {
			continue
		}

		// Read local file to compute hash
		filePath := filepath.Join(s.dagsDir, entry.Name())
		content, err := os.ReadFile(filePath) //nolint:gosec // path constructed from internal dagsDir
		if err != nil {
			continue
		}

		now := time.Now()
		ds := &DAGState{
			Status:     StatusUntracked,
			Kind:       DAGKindDAG,
			LocalHash:  ComputeContentHash(content),
			ModifiedAt: &now,
		}
		if fi, err := os.Stat(filePath); err == nil {
			updateStatCache(ds, fi)
		}
		state.DAGs[dagID] = ds
	}

	// Scan memory directory for .md files
	s.scanMemoryFiles(state)

	// Scan skills directory for SKILL.md files
	s.scanSkillFiles(state)

	// Scan souls directory for .md files
	s.scanSoulFiles(state)

	// Scan docs directory for .md files
	s.scanDocFiles(state)

	// Scan global and workspace base config files
	s.scanConfigFiles(state)

	return nil
}

// scanConfigFiles scans global and workspace base config files and adds them as untracked.
func (s *serviceImpl) scanConfigFiles(state *State) {
	s.scanConfigFile(state, baseConfigID, s.resolveBaseConfigPath(), DAGKindConfig)

	workspaceConfigDir := workspace.BaseConfigDir(s.dagsDir)
	entries, err := os.ReadDir(workspaceConfigDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || workspace.ValidateName(entry.Name()) != nil {
			continue
		}
		itemID := path.Join(workspace.BaseConfigDirName, entry.Name(), workspace.BaseConfigStem())
		s.scanConfigFile(state, itemID, workspace.BaseConfigPath(s.dagsDir, entry.Name()), DAGKindConfig)
	}
}

func (s *serviceImpl) scanConfigFile(state *State, itemID, filePath string, kind DAGKind) {
	if filePath == "" {
		return
	}
	if _, exists := state.DAGs[itemID]; exists {
		return
	}

	content, err := os.ReadFile(filePath) //nolint:gosec // path configured by server
	if err != nil {
		return
	}

	now := time.Now()
	ds := &DAGState{
		Status:     StatusUntracked,
		Kind:       kind,
		LocalHash:  ComputeContentHash(content),
		ModifiedAt: &now,
	}
	if fi, err := os.Stat(filePath); err == nil {
		updateStatCache(ds, fi)
	}
	state.DAGs[itemID] = ds
}

// scanMemoryFiles scans the memory directory for .md files and adds them as untracked.
func (s *serviceImpl) scanMemoryFiles(state *State) {
	memDir := filepath.Join(s.dagsDir, agentMemoryDir)

	_ = filepath.WalkDir(memDir, func(filePath string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(filePath) != ".md" {
			return nil
		}

		// Compute dagID relative to dagsDir, without extension
		relPath, err := filepath.Rel(s.dagsDir, filePath)
		if err != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)
		dagID := strings.TrimSuffix(relPath, path.Ext(relPath))

		// Skip if already tracked
		if _, exists := state.DAGs[dagID]; exists {
			return nil
		}

		content, err := os.ReadFile(filePath) //nolint:gosec // path constructed from internal dagsDir
		if err != nil {
			return nil
		}

		now := time.Now()
		ds := &DAGState{
			Status:     StatusUntracked,
			Kind:       DAGKindMemory,
			LocalHash:  ComputeContentHash(content),
			ModifiedAt: &now,
		}
		if fi, err := os.Stat(filePath); err == nil {
			updateStatCache(ds, fi)
		}
		state.DAGs[dagID] = ds
		return nil
	})
}

// scanSkillFiles scans the skills directory for SKILL.md files and adds them as untracked.
func (s *serviceImpl) scanSkillFiles(state *State) {
	skillDir := filepath.Join(s.dagsDir, agentSkillsDir)

	entries, err := os.ReadDir(skillDir)
	if err != nil {
		return // skills directory may not exist yet
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillMDPath := filepath.Join(skillDir, entry.Name(), "SKILL.md")
		content, err := os.ReadFile(skillMDPath) //nolint:gosec // path constructed from internal dagsDir
		if err != nil {
			continue
		}

		dagID := path.Join(agentSkillsDir, entry.Name(), "SKILL")
		if _, exists := state.DAGs[dagID]; exists {
			continue
		}

		now := time.Now()
		ds := &DAGState{
			Status:     StatusUntracked,
			Kind:       DAGKindSkill,
			LocalHash:  ComputeContentHash(content),
			ModifiedAt: &now,
		}
		if fi, err := os.Stat(skillMDPath); err == nil {
			updateStatCache(ds, fi)
		}
		state.DAGs[dagID] = ds
	}
}

// scanSoulFiles scans the souls directory for .md files and adds them as untracked.
func (s *serviceImpl) scanSoulFiles(state *State) {
	soulDir := filepath.Join(s.dagsDir, agentSoulsDir)

	entries, err := os.ReadDir(soulDir)
	if err != nil {
		return // souls directory may not exist yet
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}

		soulPath := filepath.Join(soulDir, entry.Name())
		content, err := os.ReadFile(soulPath) //nolint:gosec // path constructed from internal dagsDir
		if err != nil {
			continue
		}

		dagID := path.Join(agentSoulsDir, strings.TrimSuffix(entry.Name(), ".md"))
		if _, exists := state.DAGs[dagID]; exists {
			continue
		}

		now := time.Now()
		ds := &DAGState{
			Status:     StatusUntracked,
			Kind:       DAGKindSoul,
			LocalHash:  ComputeContentHash(content),
			ModifiedAt: &now,
		}
		if fi, err := os.Stat(soulPath); err == nil {
			updateStatCache(ds, fi)
		}
		state.DAGs[dagID] = ds
	}
}

// scanDocFiles scans the docs directory for .md files and adds them as untracked.
func (s *serviceImpl) scanDocFiles(state *State) {
	docDir := filepath.Join(s.dagsDir, agentDocsDir)

	_ = filepath.WalkDir(docDir, func(filePath string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(filePath) != ".md" {
			return nil
		}

		// Compute dagID relative to dagsDir, without extension
		relPath, err := filepath.Rel(s.dagsDir, filePath)
		if err != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)
		dagID := strings.TrimSuffix(relPath, path.Ext(relPath))

		// Skip if already tracked
		if _, exists := state.DAGs[dagID]; exists {
			return nil
		}

		content, err := os.ReadFile(filePath) //nolint:gosec // path constructed from internal dagsDir
		if err != nil {
			return nil
		}

		now := time.Now()
		ds := &DAGState{
			Status:     StatusUntracked,
			Kind:       DAGKindDoc,
			LocalHash:  ComputeContentHash(content),
			ModifiedAt: &now,
		}
		if fi, err := os.Stat(filePath); err == nil {
			updateStatCache(ds, fi)
		}
		state.DAGs[dagID] = ds
		return nil
	})
}

// refreshLocalHashes recalculates hashes for all tracked DAGs and updates status if modified.
func (s *serviceImpl) refreshLocalHashes(state *State) bool {
	changed := false
	for dagID, dagState := range state.DAGs {
		// Skip untracked (no remote to compare), conflict (already detected), and missing (file absent)
		if dagState.Status == StatusUntracked || dagState.Status == StatusConflict || dagState.Status == StatusMissing {
			continue
		}

		// Read current local file
		filePath, err := s.safeDAGIDToFilePath(dagID)
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

// ensureDAGKinds backfills missing kind values for backward-compatible state files.
func (s *serviceImpl) ensureDAGKinds(state *State) bool {
	changed := false
	for dagID, dagState := range state.DAGs {
		if dagState == nil || dagState.Kind != "" {
			continue
		}
		dagState.Kind = KindForDAGID(dagID)
		changed = true
	}
	return changed
}

// updateStatCache updates the stat cache fields on a DAGState from file info.
func updateStatCache(dagState *DAGState, info os.FileInfo) {
	modTime := info.ModTime()
	size := info.Size()
	dagState.LastStatModTime = &modTime
	dagState.LastStatSize = &size
}

// statMatchesCache returns true if the file info matches the cached stat values.
func statMatchesCache(dagState *DAGState, info os.FileInfo) bool {
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

	for dagID, dagState := range state.DAGs {
		filePath, err := s.safeDAGIDToFilePath(dagID)
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
		delete(state.DAGs, dagID)
	}

	return changed
}

// Publish commits and pushes a single DAG to the remote.
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

	dagState := state.DAGs[dagID]
	if dagState == nil {
		return nil, &DAGNotFoundError{DAGID: dagID}
	}

	if err := s.validatePublishable(dagState, dagID, force); err != nil {
		return nil, err
	}

	dagFilePath, err := s.safeDAGIDToFilePath(dagID)
	if err != nil {
		return nil, err
	}
	repoFilePath, err := s.safeDAGIDToRepoPath(dagID)
	if err != nil {
		return nil, err
	}
	repoAbsPath := s.gitClient.GetFilePath(repoFilePath)

	if err := s.gitClient.Open(); err != nil {
		return nil, err
	}

	content, err := os.ReadFile(dagFilePath) //nolint:gosec // path constructed from internal dagsDir
	if err != nil {
		return nil, fmt.Errorf("failed to read DAG file: %w", err)
	}

	if err := s.ensureDir(filepath.Dir(repoAbsPath)); err != nil {
		return nil, err
	}

	if err := os.WriteFile(repoAbsPath, content, 0600); err != nil {
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

	// Update DAG state to synced
	contentHash := ComputeContentHash(content)
	newState := s.newSyncedDAGState(dagID, commitHash, contentHash)
	if fi, err := os.Stat(dagFilePath); err == nil {
		updateStatCache(newState, fi)
	}
	state.DAGs[dagID] = newState
	s.updateSuccessStateWithCommit(state, commitHash)

	result.Success = true
	result.Message = fmt.Sprintf("Published %s", dagID)
	result.Synced = []string{dagID}

	return result, nil
}

// PublishAll commits and pushes the specified DAGs.
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
		dagFilePath, err := s.safeDAGIDToFilePath(dagID)
		if err != nil {
			return nil, err
		}
		repoFilePath, err := s.safeDAGIDToRepoPath(dagID)
		if err != nil {
			return nil, err
		}
		repoAbsPath := s.gitClient.GetFilePath(repoFilePath)

		content, err := os.ReadFile(dagFilePath) //nolint:gosec // path constructed from internal dagsDir
		if err != nil {
			result.Errors = append(result.Errors, SyncError{DAGID: dagID, Message: err.Error()})
			continue
		}

		if err := s.ensureDir(filepath.Dir(repoAbsPath)); err != nil {
			result.Errors = append(result.Errors, SyncError{DAGID: dagID, Message: err.Error()})
			continue
		}

		if err := os.WriteFile(repoAbsPath, content, 0600); err != nil {
			result.Errors = append(result.Errors, SyncError{DAGID: dagID, Message: err.Error()})
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
		message = fmt.Sprintf("Update %d DAG(s)", len(successfulDAGs))
	}
	commitHash, err := s.gitClient.CommitStaged(message)
	if err != nil {
		return nil, err
	}

	// Push
	if err := s.gitClient.Push(ctx); err != nil {
		return nil, err
	}

	// Update state only for successfully published DAGs
	for _, dagID := range successfulDAGs {
		dagFilePath, err := s.safeDAGIDToFilePath(dagID)
		if err != nil {
			return nil, err
		}
		content, _ := os.ReadFile(dagFilePath) //nolint:gosec // path constructed from internal dagsDir
		contentHash := ComputeContentHash(content)
		newState := s.newSyncedDAGState(dagID, commitHash, contentHash)
		if fi, err := os.Stat(dagFilePath); err == nil {
			updateStatCache(newState, fi)
		}
		state.DAGs[dagID] = newState
		result.Synced = append(result.Synced, dagID)
	}

	s.updateSuccessStateWithCommit(state, commitHash)

	result.Success = true
	result.Message = fmt.Sprintf("Published %d DAG(s)", len(result.Synced))

	return result, nil
}

// Discard discards local changes for a DAG.
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

	dagState := state.DAGs[dagID]
	if dagState == nil {
		return &DAGNotFoundError{DAGID: dagID}
	}

	// Open repo
	if err := s.gitClient.Open(); err != nil {
		return err
	}

	repoFilePath, err := s.safeDAGIDToRepoPath(dagID)
	if err != nil {
		return err
	}
	dagFilePath, err := s.safeDAGIDToFilePath(dagID)
	if err != nil {
		return err
	}

	// Get content from repo
	repoContent, err := os.ReadFile(s.gitClient.GetFilePath(repoFilePath))
	if err != nil {
		return fmt.Errorf("failed to read repo file: %w", err)
	}

	// Write to DAGs directory
	if err := os.WriteFile(dagFilePath, repoContent, 0600); err != nil {
		return fmt.Errorf("failed to write DAG file: %w", err)
	}

	// Update state
	contentHash := ComputeContentHash(repoContent)
	newState := s.newSyncedDAGState(dagID, dagState.BaseCommit, contentHash)
	if fi, err := os.Stat(dagFilePath); err == nil {
		updateStatCache(newState, fi)
	}
	state.DAGs[dagID] = newState
	_ = s.stateManager.Save(state) // Best effort - discard was successful, state will sync on next operation

	return nil
}

// Forget removes state entries for missing/untracked/conflict items.
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
		dagState, exists := state.DAGs[itemID]
		if !exists {
			return nil, &DAGNotFoundError{DAGID: itemID}
		}

		switch dagState.Status {
		case StatusSynced, StatusModified:
			return nil, fmt.Errorf("%w: %q is %s — only missing, untracked, or conflict items can be forgotten",
				ErrCannotForget, itemID, dagState.Status)
		case StatusMissing, StatusUntracked, StatusConflict:
			toForget = append(toForget, itemID)
		}
	}

	// Phase 2: delete all validated entries.
	var forgotten []string
	for _, itemID := range toForget {
		delete(state.DAGs, itemID)
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
	for dagID, dagState := range state.DAGs {
		if dagState.Status == StatusMissing {
			delete(state.DAGs, dagID)
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

// Delete removes an item from remote (git rm + commit + push), local disk, and state.
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

	dagState, exists := state.DAGs[itemID]
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
			Message: "item has local modifications — use force to delete anyway",
		}
	}

	// Ensure repo is ready
	if err := s.gitClient.Open(); err != nil {
		return err
	}

	// Delete local file if it exists
	localPath, err := s.safeDAGIDToFilePath(itemID)
	if err != nil {
		return err
	}
	if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove local file %q: %w", itemID, err)
	}

	// Stage removal in repo
	repoPath, err := s.safeDAGIDToRepoPath(itemID)
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
	delete(state.DAGs, itemID)
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

		dagState, exists := state.DAGs[itemID]
		if !exists {
			return nil, &DAGNotFoundError{DAGID: itemID}
		}

		if dagState.Status == StatusUntracked {
			return nil, ErrCannotDeleteUntracked
		}

		if (dagState.Status == StatusModified || dagState.Status == StatusConflict) && !force {
			return nil, &ValidationError{
				Field:   itemID,
				Message: "item has local modifications — use force to delete anyway",
			}
		}

		localPath, err := s.safeDAGIDToFilePath(itemID)
		if err != nil {
			return nil, err
		}
		repoPath, err := s.safeDAGIDToRepoPath(itemID)
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
		message = fmt.Sprintf("Delete %d item(s)", len(targets))
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
		delete(state.DAGs, t.itemID)
		deleted = append(deleted, t.itemID)
	}
	sort.Strings(deleted)
	s.updateSuccessStateWithCommit(state, commitHash)

	return deleted, nil
}

// DeleteAllMissing removes all missing items from remote, local, and state.
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

	// Collect missing items
	var missingIDs []string
	var repoPaths []string
	for dagID, dagState := range state.DAGs {
		if dagState.Status != StatusMissing {
			continue
		}
		repoPath, err := s.safeDAGIDToRepoPath(dagID)
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
		message = fmt.Sprintf("Delete %d missing item(s)", len(missingIDs))
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
		delete(state.DAGs, dagID)
	}
	s.updateSuccessStateWithCommit(state, commitHash)

	return missingIDs, nil
}

// Move atomically renames an item across local filesystem, remote repository, and sync state.
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

	// Validate same kind
	oldKind := KindForDAGID(oldID)
	newKind := KindForDAGID(newID)
	if oldKind != newKind {
		return &ValidationError{
			Field:   "newItemId",
			Message: fmt.Sprintf("cannot move across kinds: source is %s, destination is %s", oldKind, newKind),
		}
	}
	if oldKind == DAGKindConfig {
		return &ValidationError{
			Field:   "itemId",
			Message: "config items cannot be moved",
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.stateManager.GetState()
	if err != nil {
		return err
	}

	oldState, exists := state.DAGs[oldID]
	if !exists {
		return &DAGNotFoundError{DAGID: oldID}
	}

	// Reject untracked source — not tracked in remote
	if oldState.Status == StatusUntracked {
		return &ValidationError{
			Field:   oldID,
			Message: "untracked items cannot be moved — publish first",
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
	if destState, destExists := state.DAGs[newID]; destExists {
		if destState.Status != StatusUntracked {
			return &ValidationError{
				Field:   "newItemId",
				Message: fmt.Sprintf("destination %q is already tracked with status %s", newID, destState.Status),
			}
		}
	}

	// Resolve file paths
	oldLocalPath, err := s.safeDAGIDToFilePath(oldID)
	if err != nil {
		return err
	}
	newLocalPath, err := s.safeDAGIDToFilePath(newID)
	if err != nil {
		return err
	}
	oldRepoPath, err := s.safeDAGIDToRepoPath(oldID)
	if err != nil {
		return err
	}
	newRepoPath, err := s.safeDAGIDToRepoPath(newID)
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
		if oldKind == DAGKindSkill {
			// Skills are directories — move entire directory
			oldDir := filepath.Dir(oldLocalPath)
			newDir := filepath.Dir(newLocalPath)
			if err := s.ensureDir(filepath.Dir(newDir)); err != nil {
				return err
			}
			if err := os.Rename(oldDir, newDir); err != nil {
				return fmt.Errorf("failed to move skill directory: %w", err)
			}
			// Read content from new location
			content, err = os.ReadFile(newLocalPath) //nolint:gosec // path constructed from internal dagsDir
			if err != nil {
				// Rollback: move directory back
				_ = os.Rename(newDir, oldDir)
				return fmt.Errorf("failed to read moved skill file: %w", err)
			}
		} else {
			// Read old file content
			content, err = os.ReadFile(oldLocalPath) //nolint:gosec // path constructed from internal dagsDir
			if err != nil {
				return fmt.Errorf("failed to read source file: %w", err)
			}
			// Write to new location
			if err := s.ensureDir(filepath.Dir(newLocalPath)); err != nil {
				return err
			}
			if err := os.WriteFile(newLocalPath, content, 0600); err != nil {
				return fmt.Errorf("failed to write destination file: %w", err)
			}
			// Remove old file
			_ = os.Remove(oldLocalPath)
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
	if err := s.ensureDir(filepath.Dir(newRepoAbsPath)); err != nil {
		return err
	}
	if err := os.WriteFile(newRepoAbsPath, content, 0600); err != nil {
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
	newDAGState := s.newSyncedDAGState(newID, commitHash, contentHash)
	if fi, err := os.Stat(newLocalPath); err == nil {
		updateStatCache(newDAGState, fi)
	}

	// If destination was untracked, remove the old untracked entry
	delete(state.DAGs, newID)
	// Remove old entry and add new
	delete(state.DAGs, oldID)
	state.DAGs[newID] = newDAGState
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

	// Scan for new local DAGs not yet tracked
	prevCount := len(state.DAGs)
	_ = s.scanLocalDAGs(state)
	newDAGs := len(state.DAGs) > prevCount

	// Reconcile: detect missing/reappeared files
	reconciled := s.reconcile(state)

	// Refresh hashes for existing DAGs to detect local modifications
	hashesChanged := s.refreshLocalHashes(state)
	kindsUpdated := s.ensureDAGKinds(state)

	// Save state if anything changed (best effort - read-only operation)
	if newDAGs || hashesChanged || kindsUpdated || reconciled {
		_ = s.stateManager.Save(state)
	}

	status.LastSyncAt = state.LastSyncAt
	status.LastSyncCommit = state.LastSyncCommit
	status.LastSyncStatus = state.LastSyncStatus
	status.LastError = state.LastError
	status.DAGs = state.DAGs

	status.Counts = computeStatusCounts(state.DAGs)

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

// GetDAGStatus returns the sync status for a specific DAG.
func (s *serviceImpl) GetDAGStatus(_ context.Context, dagID string) (*DAGState, error) {
	if err := s.validateEnabled(); err != nil {
		return nil, err
	}

	state, err := s.stateManager.GetState()
	if err != nil {
		return nil, err
	}

	dagState := state.DAGs[dagID]
	if dagState == nil {
		return nil, &DAGNotFoundError{DAGID: dagID}
	}
	if dagState.Kind == "" {
		dagState.Kind = KindForDAGID(dagID)
		_ = s.stateManager.Save(state)
	}

	return dagState, nil
}

// GetDAGDiff returns the diff between local and remote versions of a DAG.
func (s *serviceImpl) GetDAGDiff(_ context.Context, dagID string) (*DAGDiff, error) {
	if err := s.validateEnabled(); err != nil {
		return nil, err
	}

	state, err := s.stateManager.GetState()
	if err != nil {
		return nil, err
	}

	dagState := state.DAGs[dagID]
	if dagState == nil {
		return nil, &DAGNotFoundError{DAGID: dagID}
	}

	diff := &DAGDiff{
		DAGID:  dagID,
		Status: dagState.Status,
	}

	// Missing items have no local file — handle before os.ReadFile below.
	if dagState.Status == StatusMissing {
		diff.LocalContent = ""
		diff.RemoteContent = s.fetchRemoteContent(dagID, dagState.BaseCommit)
		diff.RemoteCommit = dagState.BaseCommit
		return diff, nil
	}

	localPath, err := s.safeDAGIDToFilePath(dagID)
	if err != nil {
		return nil, err
	}
	localContent, err := os.ReadFile(localPath) //nolint:gosec // path constructed from internal dagsDir
	if err != nil {
		return nil, fmt.Errorf("failed to read local file: %w", err)
	}

	diff.LocalContent = string(localContent)

	switch dagState.Status {
	case StatusSynced:
		diff.RemoteContent = string(localContent)
		diff.RemoteCommit = dagState.BaseCommit

	case StatusModified:
		diff.RemoteContent = s.fetchRemoteContent(dagID, dagState.BaseCommit)
		diff.RemoteCommit = dagState.BaseCommit

	case StatusConflict:
		diff.RemoteContent = s.fetchRemoteContent(dagID, dagState.RemoteCommit)
		diff.RemoteCommit = dagState.RemoteCommit
		diff.RemoteAuthor = dagState.RemoteAuthor
		diff.RemoteMessage = dagState.RemoteMessage

	case StatusUntracked:
		// No remote version for untracked files

	case StatusMissing:
		// Handled above before reading local file
	}

	return diff, nil
}

// fetchRemoteContent retrieves the content of a DAG file from a specific commit.
func (s *serviceImpl) fetchRemoteContent(dagID, commitHash string) string {
	if commitHash == "" {
		return ""
	}
	if err := s.gitClient.Open(); err != nil {
		return ""
	}
	repoPath, err := s.safeDAGIDToRepoPath(dagID)
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
	// Remove extension
	ext := path.Ext(filePath)
	dagID := strings.TrimSuffix(filePath, ext)
	return dagID
}

// resolvePublishTargets validates and canonicalizes DAG IDs for batch publish.
func (s *serviceImpl) resolvePublishTargets(state *State, dagIDs []string) ([]string, error) {
	if len(dagIDs) == 0 {
		return nil, &ValidationError{
			Field:   "dagIds",
			Message: "at least one DAG ID is required",
		}
	}

	resolved := make([]string, 0, len(dagIDs))
	seen := make(map[string]struct{}, len(dagIDs))
	for i, dagID := range dagIDs {
		if strings.TrimSpace(dagID) == "" {
			return nil, &ValidationError{
				Field:   fmt.Sprintf("dagIds[%d]", i),
				Message: "DAG ID cannot be empty",
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

		dagState, exists := state.DAGs[dagID]
		if !exists {
			return nil, &ValidationError{
				Field:   "dagIds",
				Message: fmt.Sprintf("DAG %q is not tracked by git sync", dagID),
			}
		}

		switch dagState.Status {
		case StatusModified, StatusUntracked:
			resolved = append(resolved, dagID)
		case StatusConflict:
			return nil, &ValidationError{
				Field:   "dagIds",
				Message: fmt.Sprintf("DAG %q has conflicts and cannot be batch-published", dagID),
			}
		case StatusSynced:
			return nil, &ValidationError{
				Field:   "dagIds",
				Message: fmt.Sprintf("DAG %q has no local changes", dagID),
			}
		case StatusMissing:
			return nil, &ValidationError{
				Field:   "dagIds",
				Message: fmt.Sprintf("DAG %q is missing from disk and cannot be published", dagID),
			}
		default:
			return nil, &ValidationError{
				Field:   "dagIds",
				Message: fmt.Sprintf("DAG %q is in unsupported status %q", dagID, dagState.Status),
			}
		}
	}

	if len(resolved) == 0 {
		return nil, &ValidationError{
			Field:   "dagIds",
			Message: "no publishable DAG IDs provided",
		}
	}

	sort.Strings(resolved)
	return resolved, nil
}

func (s *serviceImpl) dagIDToFilePath(dagID string) string {
	// Decode if URL encoded
	decoded, err := url.PathUnescape(dagID)
	if err == nil {
		dagID = decoded
	}
	dagID = normalizeDAGIDSeparators(dagID)
	if dagID == baseConfigID {
		return s.resolveBaseConfigPath()
	}
	if workspaceName, ok := workspaceBaseConfigNameFromID(dagID); ok {
		return workspace.BaseConfigPath(s.dagsDir, workspaceName)
	}
	ext := fileExtensionForID(dagID)
	return filepath.Join(s.dagsDir, filepath.FromSlash(dagID+ext))
}

func (s *serviceImpl) dagIDToRepoPath(dagID string) string {
	// Decode if URL encoded
	decoded, err := url.PathUnescape(dagID)
	if err == nil {
		dagID = decoded
	}
	dagID = normalizeDAGIDSeparators(dagID)
	ext := fileExtensionForID(dagID)
	repoPath := dagID + ext
	if s.cfg.Path != "" {
		return path.Join(filepath.ToSlash(s.cfg.Path), repoPath)
	}
	return repoPath
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

func (s *serviceImpl) resolveBaseConfigPath() string {
	if s.baseConfig != "" {
		return s.baseConfig
	}
	return filepath.Join(s.dagsDir, workspace.BaseConfigFileName)
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
		return "", &InvalidDAGIDError{DAGID: dagID, Reason: "must point to a DAG ID, not current/parent directory"}
	}
	if strings.HasPrefix(clean, "../") {
		return "", &InvalidDAGIDError{DAGID: dagID, Reason: "path traversal is not allowed"}
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

func (s *serviceImpl) safeDAGIDToFilePath(dagID string) (string, error) {
	normalized, err := normalizeDAGID(dagID)
	if err != nil {
		return "", err
	}
	if normalized == baseConfigID {
		return s.resolveBaseConfigPath(), nil
	}
	if workspaceName, ok := workspaceBaseConfigNameFromID(normalized); ok {
		return safeJoinWithinBase(s.dagsDir, filepath.FromSlash(path.Join(workspace.BaseConfigDirName, workspaceName, workspace.BaseConfigFileName)))
	}
	ext := fileExtensionForID(normalized)
	return safeJoinWithinBase(s.dagsDir, filepath.FromSlash(normalized+ext))
}

func (s *serviceImpl) safeDAGIDToRepoPath(dagID string) (string, error) {
	normalized, err := normalizeDAGID(dagID)
	if err != nil {
		return "", err
	}

	ext := fileExtensionForID(normalized)
	repoPath := normalized + ext
	if s.cfg.Path != "" {
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

func (s *serviceImpl) ensureDir(dir string) error {
	return os.MkdirAll(dir, 0750)
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

// validatePublishable checks if a DAG can be published.
func (s *serviceImpl) validatePublishable(dagState *DAGState, dagID string, force bool) error {
	if dagState.Status == StatusMissing {
		return &ValidationError{
			Field:   dagID,
			Message: "item is missing from disk and cannot be published",
		}
	}
	if dagState.Status == StatusConflict && !force {
		return &ConflictError{
			DAGID:         dagID,
			RemoteCommit:  dagState.RemoteCommit,
			RemoteAuthor:  dagState.RemoteAuthor,
			RemoteMessage: dagState.RemoteMessage,
		}
	}
	if dagState.Status == StatusSynced {
		return ErrNoChanges
	}
	return nil
}

// ensureRepoReady ensures the repository is cloned and opened.
func (s *serviceImpl) ensureRepoReady(ctx context.Context) error {
	if !s.gitClient.IsCloned() {
		return s.gitClient.Clone(ctx)
	}
	return s.gitClient.Open()
}

// newSyncedDAGState creates a new DAGState in synced status.
func (s *serviceImpl) newSyncedDAGState(dagID, commitHash, contentHash string) *DAGState {
	now := time.Now()
	return &DAGState{
		Status:         StatusSynced,
		Kind:           KindForDAGID(dagID),
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
	return fmt.Sprintf("Synced %d DAG(s)", len(synced))
}

// computeStatusCounts computes the counts for each DAG status.
func computeStatusCounts(dags map[string]*DAGState) StatusCounts {
	var counts StatusCounts
	for _, dagState := range dags {
		switch dagState.Status {
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
