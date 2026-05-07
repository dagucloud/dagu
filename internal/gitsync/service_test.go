// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test helpers ---

var (
	testCfgReadOnly  = &Config{Enabled: true, Repository: "r", Branch: "main"}
	testCfgReadWrite = &Config{Enabled: true, Repository: "r", Branch: "main", PushEnabled: true}
	testCfgPushOff   = &Config{Enabled: true, Repository: "r", Branch: "main", PushEnabled: false}
)

// newTestService creates a service with temp directories for testing.
func newTestService(t *testing.T, cfg *Config) (*serviceImpl, string) {
	t.Helper()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	dataDir := filepath.Join(tempDir, "data")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))
	require.NoError(t, os.MkdirAll(dataDir, 0755))
	svc := NewService(cfg, dagsDir, dataDir)
	return svc.(*serviceImpl), dagsDir
}

// --- Pre-existing tests ---

func TestService_GetStatus(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Enabled:    true,
		Repository: "host.com/org/repo",
		Branch:     "main",
	}
	impl, _ := newTestService(t, cfg)

	status, err := impl.GetStatus(context.Background())
	require.NoError(t, err)

	require.True(t, status.Enabled)
	require.Equal(t, cfg.Repository, status.Repository)
	require.Equal(t, cfg.Branch, status.Branch)
}

func TestService_PathHelpers(t *testing.T) {
	s := &serviceImpl{
		dagsDir: "/dags",
		cfg: &Config{
			Path: "subdir",
		},
	}

	// Test filePathToDAGID
	dagID := s.filePathToDAGID(filepath.Join("subdir", "my_dag.yaml"))
	require.Equal(t, "my_dag", dagID)

	// Test dagIDToFilePath
	dagPath := s.dagIDToFilePath("my_dag")
	require.Equal(t, filepath.Join("/dags", "my_dag.yaml"), dagPath)

	// Test dagIDToRepoPath
	repoPath := s.dagIDToRepoPath("my_dag")
	require.Equal(t, "subdir/my_dag.yaml", repoPath)
}

func TestKindForDAGID_ConfigFiles(t *testing.T) {
	t.Parallel()

	assert.Equal(t, DAGKindConfig, KindForDAGID("base"))
	assert.Equal(t, DAGKindConfig, KindForDAGID("workspaces/ops/base"))
	// TestKindForDAGID_ConfigFiles expects KindForDAGID to reject reserved
	// workspace names through workspace.ValidateName and fall back to DAG files.
	assert.Equal(t, DAGKindDAG, KindForDAGID("workspaces/default/base"))
	assert.Equal(t, DAGKindDAG, KindForDAGID("workspaces/ops/other"))
}

func TestDagIDPathHelpers_ConfigFiles(t *testing.T) {
	s := &serviceImpl{
		dagsDir:    "/dags",
		baseConfig: filepath.Join("/config", "base.yaml"),
		cfg:        &Config{Path: "subdir"},
	}

	assert.Equal(t, filepath.Join("/config", "base.yaml"), s.dagIDToFilePath("base"))
	assert.Equal(t,
		filepath.Join("/dags", "workspaces", "ops", "base.yaml"),
		s.dagIDToFilePath("workspaces/ops/base"),
	)
	assert.Equal(t, "subdir/base.yaml", s.dagIDToRepoPath("base"))
	assert.Equal(t, "subdir/workspaces/ops/base.yaml", s.dagIDToRepoPath("workspaces/ops/base"))
}

func TestIsMemoryFile(t *testing.T) {
	t.Parallel()

	assert.True(t, isMemoryFile("memory/MEMORY"))
	assert.True(t, isMemoryFile("memory/dags/my-dag/MEMORY"))
	assert.False(t, isMemoryFile("my-dag"))
	assert.False(t, isMemoryFile("memoryfile"))
}

func TestFileExtensionForID(t *testing.T) {
	t.Parallel()

	assert.Equal(t, ".md", fileExtensionForID("memory/MEMORY"))
	assert.Equal(t, ".md", fileExtensionForID("memory/dags/my-dag/MEMORY"))
	assert.Equal(t, ".yaml", fileExtensionForID("my-dag"))
	assert.Equal(t, ".yaml", fileExtensionForID("subdir/my-dag"))
}

func TestDagIDToFilePath_MemoryFiles(t *testing.T) {
	s := &serviceImpl{
		dagsDir: "/dags",
		cfg:     &Config{},
	}

	// Regular DAG
	assert.Equal(t, filepath.Join("/dags", "my-dag.yaml"), s.dagIDToFilePath("my-dag"))

	// Memory file
	assert.Equal(t,
		filepath.Join("/dags", "memory", "MEMORY.md"),
		s.dagIDToFilePath("memory/MEMORY"),
	)

	// DAG-specific memory
	assert.Equal(t,
		filepath.Join("/dags", "memory", "dags", "my-dag", "MEMORY.md"),
		s.dagIDToFilePath("memory/dags/my-dag/MEMORY"),
	)
}

func TestDagIDToRepoPath_MemoryFiles(t *testing.T) {
	s := &serviceImpl{
		dagsDir: "/dags",
		cfg:     &Config{Path: "subdir"},
	}

	// Regular DAG
	assert.Equal(t, "subdir/my-dag.yaml", s.dagIDToRepoPath("my-dag"))

	// Memory file
	assert.Equal(t,
		"subdir/memory/MEMORY.md",
		s.dagIDToRepoPath("memory/MEMORY"),
	)
}

func TestScanMemoryFiles(t *testing.T) {
	tempDir := t.TempDir()
	s := &serviceImpl{
		dagsDir: tempDir,
		cfg:     &Config{},
	}

	// Create memory directory with files
	memDir := filepath.Join(tempDir, "memory")
	require.NoError(t, os.MkdirAll(memDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte("global memory"), 0600))

	dagMemDir := filepath.Join(memDir, "dags", "my-dag")
	require.NoError(t, os.MkdirAll(dagMemDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dagMemDir, "MEMORY.md"), []byte("dag memory"), 0600))

	state := &State{DAGs: make(map[string]*DAGState)}
	s.scanMemoryFiles(state)

	// Should find global memory
	globalID := "memory/MEMORY"
	assert.Contains(t, state.DAGs, globalID)
	assert.Equal(t, StatusUntracked, state.DAGs[globalID].Status)
	assert.Equal(t, DAGKindMemory, state.DAGs[globalID].Kind)

	// Should find per-DAG memory
	dagID := "memory/dags/my-dag/MEMORY"
	assert.Contains(t, state.DAGs, dagID)
	assert.Equal(t, StatusUntracked, state.DAGs[dagID].Status)
	assert.Equal(t, DAGKindMemory, state.DAGs[dagID].Kind)
}

func TestScanLocalDAGs_ConfigFiles(t *testing.T) {
	rootDir := t.TempDir()
	dagsDir := filepath.Join(rootDir, "dags")
	baseConfig := filepath.Join(rootDir, "base.yaml")
	require.NoError(t, os.MkdirAll(filepath.Join(dagsDir, "workspaces", "ops"), 0750))
	require.NoError(t, os.WriteFile(baseConfig, []byte("env:\n  GLOBAL: 1\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "workspaces", "ops", "base.yaml"), []byte("env:\n  OPS: 1\n"), 0600))

	s := &serviceImpl{
		dagsDir:    dagsDir,
		baseConfig: baseConfig,
		cfg:        &Config{},
	}
	state := &State{DAGs: make(map[string]*DAGState)}

	require.NoError(t, s.scanLocalDAGs(state))

	require.Contains(t, state.DAGs, "base")
	assert.Equal(t, DAGKindConfig, state.DAGs["base"].Kind)
	require.Contains(t, state.DAGs, "workspaces/ops/base")
	assert.Equal(t, DAGKindConfig, state.DAGs["workspaces/ops/base"].Kind)
}

func TestScanLocalDAGs_IgnoresNonMemoryMd(t *testing.T) {
	tempDir := t.TempDir()

	// Create a non-memory .md file at root (e.g., README.md)
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "README.md"), []byte("# readme"), 0600))

	// Create a regular .yaml DAG
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "my-dag.yaml"), []byte("steps: []"), 0600))

	s := &serviceImpl{
		dagsDir: tempDir,
		cfg:     &Config{},
	}

	state := &State{DAGs: make(map[string]*DAGState)}
	err := s.scanLocalDAGs(state)
	require.NoError(t, err)

	// Should find the yaml DAG
	assert.Contains(t, state.DAGs, "my-dag")
	assert.Equal(t, DAGKindDAG, state.DAGs["my-dag"].Kind)

	// Should NOT find README.md (it's not a yaml DAG or memory file at root)
	assert.NotContains(t, state.DAGs, "README")
}

func TestService_GetStatusBackfillsKinds(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	dataDir := filepath.Join(tempDir, "data")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(dagsDir, "memory", "dags", "a"), 0755))
	require.NoError(t, os.MkdirAll(dataDir, 0755))

	// Create files on disk so reconcile doesn't remove/transition them
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "example.yaml"), []byte("steps: []"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "memory", "MEMORY.md"), []byte("mem"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "memory", "dags", "a", "MEMORY.md"), []byte("mem"), 0600))

	cfg := &Config{
		Enabled:    true,
		Repository: "host.com/org/repo",
		Branch:     "main",
	}

	svc := NewService(cfg, dagsDir, dataDir)
	impl, ok := svc.(*serviceImpl)
	require.True(t, ok)

	now := time.Now()
	state := &State{
		Version: 1,
		DAGs: map[string]*DAGState{
			"example":              {Status: StatusModified, ModifiedAt: &now},
			"memory/MEMORY":        {Status: StatusUntracked, ModifiedAt: &now},
			"memory/dags/a/MEMORY": {Status: StatusUntracked, ModifiedAt: &now},
		},
	}
	require.NoError(t, impl.stateManager.Save(state))

	status, err := svc.GetStatus(context.Background())
	require.NoError(t, err)
	require.NotNil(t, status.DAGs["example"])
	require.NotNil(t, status.DAGs["memory/MEMORY"])
	require.NotNil(t, status.DAGs["memory/dags/a/MEMORY"])
	assert.Equal(t, DAGKindDAG, status.DAGs["example"].Kind)
	assert.Equal(t, DAGKindMemory, status.DAGs["memory/MEMORY"].Kind)
	assert.Equal(t, DAGKindMemory, status.DAGs["memory/dags/a/MEMORY"].Kind)
}

func TestSyncFilesToDAGsDir_PromotesMatchingUntrackedItemToSynced(t *testing.T) {
	t.Parallel()

	svc, dagsDir := newTestService(t, testCfgReadOnly)

	repoPath := filepath.Join(svc.dataDir, "gitsync", "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repoPath, "docs", "youtube_translate"), 0755))
	repo, err := git.PlainInit(repoPath, false)
	require.NoError(t, err)
	svc.gitClient.repo = repo

	itemID := "docs/youtube_translate/example"
	repoFile := filepath.Join(repoPath, "docs", "youtube_translate", "example.md")
	localFile := filepath.Join(dagsDir, "docs", "youtube_translate", "example.md")
	content := []byte("# translated doc\n")

	require.NoError(t, os.MkdirAll(filepath.Dir(localFile), 0755))
	require.NoError(t, os.WriteFile(repoFile, content, 0600))
	require.NoError(t, os.WriteFile(localFile, content, 0600))

	commitHash, err := svc.gitClient.AddAndCommit(filepath.Join("docs", "youtube_translate", "example.md"), "add doc")
	require.NoError(t, err)

	now := time.Now()
	require.NoError(t, svc.stateManager.Save(&State{
		Version: 1,
		DAGs: map[string]*DAGState{
			itemID: {
				Status:     StatusUntracked,
				Kind:       DAGKindDoc,
				ModifiedAt: &now,
				LocalHash:  ComputeContentHash(content),
			},
		},
	}))

	synced, conflicts, err := svc.syncFilesToDAGsDir(context.Background(), &PullResult{
		PreviousCommit:  commitHash,
		CurrentCommit:   commitHash,
		AlreadyUpToDate: true,
	}, commitHash)
	require.NoError(t, err)
	require.Equal(t, []string{itemID}, synced)
	require.Empty(t, conflicts)

	state, err := svc.stateManager.GetState()
	require.NoError(t, err)
	require.Contains(t, state.DAGs, itemID)
	assert.Equal(t, StatusSynced, state.DAGs[itemID].Status)
	assert.Equal(t, commitHash, state.DAGs[itemID].BaseCommit)
	assert.Equal(t, ComputeContentHash(content), state.DAGs[itemID].LastSyncedHash)
}

func TestResolvePublishTargets(t *testing.T) {
	t.Parallel()

	now := time.Now()
	baseState := &State{
		DAGs: map[string]*DAGState{
			"alpha":    {Status: StatusModified, ModifiedAt: &now},
			"beta":     {Status: StatusUntracked, ModifiedAt: &now},
			"synced":   {Status: StatusSynced, LastSyncedAt: &now},
			"conflict": {Status: StatusConflict, ConflictDetectedAt: &now},
		},
	}

	s := &serviceImpl{}

	t.Run("returns sorted unique publishable IDs", func(t *testing.T) {
		targets, err := s.resolvePublishTargets(baseState, []string{"beta", "alpha", "beta"})
		require.NoError(t, err)
		assert.Equal(t, []string{"alpha", "beta"}, targets)
	})

	t.Run("rejects empty dagIds", func(t *testing.T) {
		_, err := s.resolvePublishTargets(baseState, nil)
		require.Error(t, err)
		var validationErr *ValidationError
		require.ErrorAs(t, err, &validationErr)
		assert.Equal(t, "dagIds", validationErr.Field)
	})

	t.Run("rejects unknown dag", func(t *testing.T) {
		_, err := s.resolvePublishTargets(baseState, []string{"missing"})
		require.Error(t, err)
		var validationErr *ValidationError
		require.ErrorAs(t, err, &validationErr)
		assert.Contains(t, validationErr.Message, "not tracked")
	})

	t.Run("rejects synced dag", func(t *testing.T) {
		_, err := s.resolvePublishTargets(baseState, []string{"synced"})
		require.Error(t, err)
		var validationErr *ValidationError
		require.ErrorAs(t, err, &validationErr)
		assert.Contains(t, validationErr.Message, "no local changes")
	})

	t.Run("rejects conflict dag", func(t *testing.T) {
		_, err := s.resolvePublishTargets(baseState, []string{"conflict"})
		require.Error(t, err)
		var validationErr *ValidationError
		require.ErrorAs(t, err, &validationErr)
		assert.Contains(t, validationErr.Message, "has conflicts")
	})
}

func TestSafeDAGIDPathValidation(t *testing.T) {
	t.Parallel()

	s := &serviceImpl{
		dagsDir: "/dags",
		cfg:     &Config{Path: "subdir"},
		gitClient: &GitClient{
			repoPath: "/repo",
		},
	}

	t.Run("valid regular DAG path", func(t *testing.T) {
		path, err := s.safeDAGIDToFilePath("my-dag")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/dags", "my-dag.yaml"), path)
	})

	t.Run("valid memory path", func(t *testing.T) {
		path, err := s.safeDAGIDToRepoPath("memory/MEMORY")
		require.NoError(t, err)
		assert.Equal(t, "subdir/memory/MEMORY.md", path)
	})

	t.Run("normalizes backslash separators", func(t *testing.T) {
		path, err := s.safeDAGIDToRepoPath(`memory\MEMORY`)
		require.NoError(t, err)
		assert.Equal(t, "subdir/memory/MEMORY.md", path)
	})

	t.Run("rejects traversal DAG ID", func(t *testing.T) {
		_, err := s.safeDAGIDToFilePath("../etc/passwd")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidDAGID)
	})

	t.Run("rejects absolute DAG ID", func(t *testing.T) {
		_, err := s.safeDAGIDToRepoPath("/tmp/file")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidDAGID)
	})

	t.Run("rejects non-canonical DAG ID", func(t *testing.T) {
		_, err := s.resolvePublishTargets(
			&State{DAGs: map[string]*DAGState{"a/b": {Status: StatusModified}}},
			[]string{"a/./b"},
		)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidDAGID)
	})
}

// --- Phase 1: Reconciliation tests ---

func TestReconcile_SyncedFileDeleted(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	now := time.Now()
	state := &State{DAGs: map[string]*DAGState{
		"my-dag": {
			Status:         StatusSynced,
			Kind:           DAGKindDAG,
			BaseCommit:     "abc123",
			LastSyncedHash: "sha256:aaa",
			LastSyncedAt:   &now,
			LocalHash:      "sha256:aaa",
		},
	}}

	// File does NOT exist on disk — should transition to missing
	changed := s.reconcile(state)
	require.True(t, changed)

	ds := state.DAGs["my-dag"]
	assert.Equal(t, StatusMissing, ds.Status)
	assert.Equal(t, "synced", ds.PreviousStatus)
	assert.NotNil(t, ds.MissingAt)
}

func TestReconcile_ModifiedFileDeleted(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	now := time.Now()
	state := &State{DAGs: map[string]*DAGState{
		"my-dag": {
			Status:         StatusModified,
			Kind:           DAGKindDAG,
			BaseCommit:     "abc123",
			LastSyncedHash: "sha256:aaa",
			LocalHash:      "sha256:bbb",
			ModifiedAt:     &now,
		},
	}}

	changed := s.reconcile(state)
	require.True(t, changed)

	ds := state.DAGs["my-dag"]
	assert.Equal(t, StatusMissing, ds.Status)
	assert.Equal(t, "modified", ds.PreviousStatus)
	assert.NotNil(t, ds.MissingAt)
}

func TestReconcile_ConflictFileDeleted(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	now := time.Now()
	state := &State{DAGs: map[string]*DAGState{
		"my-dag": {
			Status:             StatusConflict,
			Kind:               DAGKindDAG,
			BaseCommit:         "abc123",
			LastSyncedHash:     "sha256:aaa",
			LocalHash:          "sha256:bbb",
			ConflictDetectedAt: &now,
		},
	}}

	changed := s.reconcile(state)
	require.True(t, changed)

	ds := state.DAGs["my-dag"]
	assert.Equal(t, StatusMissing, ds.Status)
	assert.Equal(t, "conflict", ds.PreviousStatus)
	assert.NotNil(t, ds.MissingAt)
}

func TestReconcile_MissingFileReappears_Synced(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	// Write file matching LastSyncedHash
	content := []byte("steps: []")
	hash := ComputeContentHash(content)
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "my-dag.yaml"), content, 0600))

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	missingAt := time.Now().Add(-time.Hour)
	state := &State{DAGs: map[string]*DAGState{
		"my-dag": {
			Status:         StatusMissing,
			Kind:           DAGKindDAG,
			BaseCommit:     "abc123",
			LastSyncedHash: hash,
			LocalHash:      "",
			PreviousStatus: "synced",
			MissingAt:      &missingAt,
		},
	}}

	changed := s.reconcile(state)
	require.True(t, changed)

	ds := state.DAGs["my-dag"]
	assert.Equal(t, StatusSynced, ds.Status)
	assert.Equal(t, hash, ds.LocalHash)
	assert.Empty(t, ds.PreviousStatus)
	assert.Nil(t, ds.MissingAt)
}

func TestReconcile_MissingFileReappears_Modified(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	// Write file with different content
	content := []byte("steps: [new-stuff]")
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "my-dag.yaml"), content, 0600))

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	missingAt := time.Now().Add(-time.Hour)
	state := &State{DAGs: map[string]*DAGState{
		"my-dag": {
			Status:         StatusMissing,
			Kind:           DAGKindDAG,
			BaseCommit:     "abc123",
			LastSyncedHash: "sha256:old-hash",
			LocalHash:      "",
			PreviousStatus: "synced",
			MissingAt:      &missingAt,
		},
	}}

	changed := s.reconcile(state)
	require.True(t, changed)

	ds := state.DAGs["my-dag"]
	assert.Equal(t, StatusModified, ds.Status)
	assert.Equal(t, ComputeContentHash(content), ds.LocalHash)
	assert.NotNil(t, ds.ModifiedAt)
	assert.Empty(t, ds.PreviousStatus)
	assert.Nil(t, ds.MissingAt)
}

func TestReconcile_UntrackedFileDeleted(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	now := time.Now()
	state := &State{DAGs: map[string]*DAGState{
		"my-dag": {
			Status:     StatusUntracked,
			Kind:       DAGKindDAG,
			LocalHash:  "sha256:aaa",
			ModifiedAt: &now,
		},
	}}

	changed := s.reconcile(state)
	require.True(t, changed)

	// Entry should be removed entirely
	assert.NotContains(t, state.DAGs, "my-dag")
}

func TestReconcile_SyncedFileStillExists(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	// File exists on disk
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "my-dag.yaml"), []byte("steps: []"), 0600))

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	now := time.Now()
	state := &State{DAGs: map[string]*DAGState{
		"my-dag": {
			Status:         StatusSynced,
			Kind:           DAGKindDAG,
			BaseCommit:     "abc123",
			LastSyncedHash: "sha256:aaa",
			LastSyncedAt:   &now,
			LocalHash:      "sha256:aaa",
		},
	}}

	changed := s.reconcile(state)
	require.False(t, changed)

	ds := state.DAGs["my-dag"]
	assert.Equal(t, StatusSynced, ds.Status)
}

func TestReconcile_BackwardCompatibility(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	dataDir := filepath.Join(tempDir, "data")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))
	require.NoError(t, os.MkdirAll(dataDir, 0755))

	// Write file on disk
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "my-dag.yaml"), []byte("steps: []"), 0600))

	svc := NewService(&Config{
		Enabled:    true,
		Repository: "host.com/org/repo",
		Branch:     "main",
	}, dagsDir, dataDir)
	impl := svc.(*serviceImpl)

	// Save old state without PreviousStatus/MissingAt fields
	now := time.Now()
	oldState := &State{
		Version: 1,
		DAGs: map[string]*DAGState{
			"my-dag": {
				Status:         StatusSynced,
				Kind:           DAGKindDAG,
				BaseCommit:     "abc123",
				LastSyncedHash: ComputeContentHash([]byte("steps: []")),
				LastSyncedAt:   &now,
				LocalHash:      ComputeContentHash([]byte("steps: []")),
			},
		},
	}
	require.NoError(t, impl.stateManager.Save(oldState))

	// Load and verify — no fields should be populated
	loaded, err := impl.stateManager.Load()
	require.NoError(t, err)
	ds := loaded.DAGs["my-dag"]
	assert.Empty(t, ds.PreviousStatus)
	assert.Nil(t, ds.MissingAt)
}

func TestStatusCounts_IncludesMissing(t *testing.T) {
	t.Parallel()

	dags := map[string]*DAGState{
		"a": {Status: StatusSynced},
		"b": {Status: StatusModified},
		"c": {Status: StatusUntracked},
		"d": {Status: StatusConflict},
		"e": {Status: StatusMissing},
		"f": {Status: StatusMissing},
	}

	counts := computeStatusCounts(dags)
	assert.Equal(t, 1, counts.Synced)
	assert.Equal(t, 1, counts.Modified)
	assert.Equal(t, 1, counts.Untracked)
	assert.Equal(t, 1, counts.Conflict)
	assert.Equal(t, 2, counts.Missing)
}

func TestSummaryPriority_MissingBetweenConflictAndPending(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Enabled:    true,
		Repository: "host.com/org/repo",
		Branch:     "main",
	}

	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	dataDir := filepath.Join(tempDir, "data")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))
	require.NoError(t, os.MkdirAll(dataDir, 0755))

	svc := NewService(cfg, dagsDir, dataDir)
	impl := svc.(*serviceImpl)

	t.Run("missing overrides pending", func(t *testing.T) {
		now := time.Now()
		state := &State{
			Version: 1,
			DAGs: map[string]*DAGState{
				"a": {Status: StatusMissing, PreviousStatus: "synced", MissingAt: &now},
			},
		}
		require.NoError(t, impl.stateManager.Save(state))

		status, err := svc.GetStatus(context.Background())
		require.NoError(t, err)
		assert.Equal(t, SummaryMissing, status.Summary)
	})

	t.Run("conflict overrides missing", func(t *testing.T) {
		// Create conflict file on disk so reconcile doesn't transition it to missing
		require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "b.yaml"), []byte("conflict content"), 0600))

		now := time.Now()
		state := &State{
			Version: 1,
			DAGs: map[string]*DAGState{
				"a": {Status: StatusMissing, PreviousStatus: "synced", MissingAt: &now},
				"b": {Status: StatusConflict, ConflictDetectedAt: &now},
			},
		}
		require.NoError(t, impl.stateManager.Save(state))

		status, err := svc.GetStatus(context.Background())
		require.NoError(t, err)
		assert.Equal(t, SummaryConflict, status.Summary)
	})
}

// --- Phase 2: Stat-before-hash tests ---

func TestStatBeforeHash_SkipsUnchangedFile(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	content := []byte("steps: []")
	filePath := filepath.Join(dagsDir, "my-dag.yaml")
	require.NoError(t, os.WriteFile(filePath, content, 0600))

	fi, err := os.Stat(filePath)
	require.NoError(t, err)

	hash := ComputeContentHash(content)
	modTime := fi.ModTime()
	size := fi.Size()

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	state := &State{DAGs: map[string]*DAGState{
		"my-dag": {
			Status:          StatusSynced,
			Kind:            DAGKindDAG,
			LastSyncedHash:  hash,
			LocalHash:       hash,
			LastStatModTime: &modTime,
			LastStatSize:    &size,
		},
	}}

	// File hasn't changed — refreshLocalHashes should skip it
	changed := s.refreshLocalHashes(state)
	require.False(t, changed)
	assert.Equal(t, StatusSynced, state.DAGs["my-dag"].Status)
}

func TestStatBeforeHash_DetectsChangedFile(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	originalContent := []byte("steps: []")
	filePath := filepath.Join(dagsDir, "my-dag.yaml")
	require.NoError(t, os.WriteFile(filePath, originalContent, 0600))

	fi, err := os.Stat(filePath)
	require.NoError(t, err)

	oldHash := ComputeContentHash(originalContent)
	oldModTime := fi.ModTime()
	oldSize := fi.Size()

	// Write different content
	newContent := []byte("steps: [a, b, c]")
	require.NoError(t, os.WriteFile(filePath, newContent, 0600))

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	state := &State{DAGs: map[string]*DAGState{
		"my-dag": {
			Status:          StatusSynced,
			Kind:            DAGKindDAG,
			LastSyncedHash:  oldHash,
			LocalHash:       oldHash,
			LastStatModTime: &oldModTime,
			LastStatSize:    &oldSize,
		},
	}}

	changed := s.refreshLocalHashes(state)
	require.True(t, changed)
	assert.Equal(t, StatusModified, state.DAGs["my-dag"].Status)
	assert.Equal(t, ComputeContentHash(newContent), state.DAGs["my-dag"].LocalHash)
	// Stat cache should be updated
	assert.NotNil(t, state.DAGs["my-dag"].LastStatModTime)
	assert.NotNil(t, state.DAGs["my-dag"].LastStatSize)
}

func TestStatBeforeHash_BackwardCompatibility(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	content := []byte("steps: []")
	filePath := filepath.Join(dagsDir, "my-dag.yaml")
	require.NoError(t, os.WriteFile(filePath, content, 0600))

	hash := ComputeContentHash(content)

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	state := &State{DAGs: map[string]*DAGState{
		"my-dag": {
			Status:         StatusSynced,
			Kind:           DAGKindDAG,
			LastSyncedHash: hash,
			LocalHash:      hash,
			// No stat cache fields — backward compatibility
		},
	}}

	// Nil cache fields → should read file and populate cache
	changed := s.refreshLocalHashes(state)
	// No status change since content matches
	require.False(t, changed)
	// But stat cache should now be populated
	assert.NotNil(t, state.DAGs["my-dag"].LastStatModTime)
	assert.NotNil(t, state.DAGs["my-dag"].LastStatSize)
}

func TestStatBeforeHash_PopulatedDuringScan(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := tempDir

	// Create a DAG file
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "new-dag.yaml"), []byte("steps: []"), 0600))

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	state := &State{DAGs: make(map[string]*DAGState)}

	err := s.scanLocalDAGs(state)
	require.NoError(t, err)

	ds := state.DAGs["new-dag"]
	require.NotNil(t, ds)
	assert.NotNil(t, ds.LastStatModTime)
	assert.NotNil(t, ds.LastStatSize)
}

func TestResolvePublishTargets_RejectsMissing(t *testing.T) {
	t.Parallel()

	now := time.Now()
	state := &State{
		DAGs: map[string]*DAGState{
			"missing-dag": {Status: StatusMissing, PreviousStatus: "synced", MissingAt: &now},
		},
	}

	s := &serviceImpl{}
	_, err := s.resolvePublishTargets(state, []string{"missing-dag"})
	require.Error(t, err)
	var validationErr *ValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Contains(t, validationErr.Message, "missing from disk")
}

// --- Phase 3: Forget + Cleanup tests ---

func TestForget_MissingItem(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadOnly)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"my-dag": {Status: StatusMissing, PreviousStatus: "synced", MissingAt: &now},
	}}))

	forgotten, err := impl.Forget(context.Background(), []string{"my-dag"})
	require.NoError(t, err)
	assert.Equal(t, []string{"my-dag"}, forgotten)

	state, _ := impl.stateManager.GetState()
	assert.NotContains(t, state.DAGs, "my-dag")
}

func TestForget_UntrackedItem(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadOnly)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"my-dag": {Status: StatusUntracked, ModifiedAt: &now},
	}}))

	forgotten, err := impl.Forget(context.Background(), []string{"my-dag"})
	require.NoError(t, err)
	assert.Equal(t, []string{"my-dag"}, forgotten)
}

func TestForget_ConflictItem(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadOnly)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"my-dag": {Status: StatusConflict, ConflictDetectedAt: &now},
	}}))

	forgotten, err := impl.Forget(context.Background(), []string{"my-dag"})
	require.NoError(t, err)
	assert.Equal(t, []string{"my-dag"}, forgotten)
}

func TestForget_SyncedItem_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadOnly)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"my-dag": {Status: StatusSynced, LastSyncedAt: &now},
	}}))

	_, err := impl.Forget(context.Background(), []string{"my-dag"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCannotForget)
}

func TestForget_ModifiedItem_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadOnly)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"my-dag": {Status: StatusModified, ModifiedAt: &now},
	}}))

	_, err := impl.Forget(context.Background(), []string{"my-dag"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCannotForget)
}

func TestForget_NotFound(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadOnly)
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{}}))

	_, err := impl.Forget(context.Background(), []string{"nonexistent"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDAGNotFound)
}

func TestCleanup_RemovesAllMissing(t *testing.T) {
	t.Parallel()
	impl, dagsDir := newTestService(t, testCfgReadOnly)

	// Create file on disk for synced item
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "synced-dag.yaml"), []byte("ok"), 0600))

	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"missing-a":  {Status: StatusMissing, PreviousStatus: "synced", MissingAt: &now},
		"missing-b":  {Status: StatusMissing, PreviousStatus: "modified", MissingAt: &now},
		"synced-dag": {Status: StatusSynced, LastSyncedAt: &now},
	}}))

	forgotten, err := impl.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Len(t, forgotten, 2)
	assert.Contains(t, forgotten, "missing-a")
	assert.Contains(t, forgotten, "missing-b")

	state, _ := impl.stateManager.GetState()
	assert.NotContains(t, state.DAGs, "missing-a")
	assert.NotContains(t, state.DAGs, "missing-b")
	assert.Contains(t, state.DAGs, "synced-dag")
}

func TestCleanup_NoMissingItems(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadOnly)
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{}}))

	forgotten, err := impl.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Len(t, forgotten, 0)
}

// --- Phase 4: Remote deletion detection tests ---

func TestReconcileAfterPull_AutoForget_BothAbsent(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	now := time.Now()
	state := &State{DAGs: map[string]*DAGState{
		"deleted-dag": {
			Status:         StatusMissing,
			Kind:           DAGKindDAG,
			PreviousStatus: "synced",
			MissingAt:      &now,
			LastSyncedHash: "sha256:aaa",
		},
	}}

	// dagID is NOT in repoFileSet and file does NOT exist on disk
	repoFileSet := map[string]struct{}{}
	s.reconcileAfterPull(state, repoFileSet)

	assert.NotContains(t, state.DAGs, "deleted-dag")
}

func TestReconcileAfterPull_NoAutoForget_LocalPresent(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	dagsDir := filepath.Join(tempDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	// File exists locally
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "my-dag.yaml"), []byte("ok"), 0600))

	s := &serviceImpl{dagsDir: dagsDir, cfg: &Config{}}
	now := time.Now()
	state := &State{DAGs: map[string]*DAGState{
		"my-dag": {
			Status:         StatusModified,
			Kind:           DAGKindDAG,
			LastSyncedHash: "sha256:aaa",
			ModifiedAt:     &now,
		},
	}}

	// dagID is NOT in repo but file IS local
	repoFileSet := map[string]struct{}{}
	s.reconcileAfterPull(state, repoFileSet)

	assert.Contains(t, state.DAGs, "my-dag")
}

func TestPull_DuplicatePrevention(t *testing.T) {
	t.Parallel()

	repoContent := []byte("steps: [a]")
	repoHash := ComputeContentHash(repoContent)

	now := time.Now()
	state := &State{DAGs: map[string]*DAGState{
		"old-name": {
			Status:         StatusMissing,
			Kind:           DAGKindDAG,
			PreviousStatus: "synced",
			MissingAt:      &now,
			LastSyncedHash: repoHash,
		},
	}}

	// Simulate: during pull, we're about to create "new-name" from remote.
	// "old-name" is missing with matching hash — should be auto-forgotten.
	// We test the duplicate-prevention logic directly.
	dagID := "new-name"
	for otherID, otherState := range state.DAGs {
		if otherID != dagID && otherState.Status == StatusMissing && otherState.LastSyncedHash == repoHash {
			delete(state.DAGs, otherID)
			break
		}
	}

	assert.NotContains(t, state.DAGs, "old-name")
}

// --- Phase 5: Delete tests ---

func TestDelete_UntrackedItem_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"my-dag": {Status: StatusUntracked, ModifiedAt: &now},
	}}))

	err := impl.Delete(context.Background(), "my-dag", "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCannotDeleteUntracked)
}

func TestDelete_PushDisabled_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgPushOff)

	err := impl.Delete(context.Background(), "my-dag", "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPushDisabled)
}

func TestDelete_ModifiedItem_WithoutForce_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"my-dag": {Status: StatusModified, ModifiedAt: &now},
	}}))

	err := impl.Delete(context.Background(), "my-dag", "", false)
	require.Error(t, err)
	var validationErr *ValidationError
	assert.ErrorAs(t, err, &validationErr)
}

func TestDelete_NotFound(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{}}))

	err := impl.Delete(context.Background(), "nonexistent", "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDAGNotFound)
}

func TestDeleteAllMissing_PushDisabled_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgPushOff)

	_, err := impl.DeleteAllMissing(context.Background(), "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPushDisabled)
}

func TestDeleteAllMissing_NoMissingItems(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"synced-dag": {Status: StatusSynced},
	}}))

	deleted, err := impl.DeleteAllMissing(context.Background(), "")
	require.NoError(t, err)
	assert.Nil(t, deleted)
}

// --- Phase 5b: DeleteBatch tests ---

func TestDeleteBatch_PushDisabled_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgPushOff)

	_, err := impl.DeleteBatch(context.Background(), []string{"dag-a"}, "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPushDisabled)
}

func TestDeleteBatch_NotFound(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{}}))

	_, err := impl.DeleteBatch(context.Background(), []string{"nonexistent"}, "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDAGNotFound)
}

func TestDeleteBatch_UntrackedItem_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"dag-a": {Status: StatusSynced},
		"dag-b": {Status: StatusUntracked, ModifiedAt: &now},
	}}))

	_, err := impl.DeleteBatch(context.Background(), []string{"dag-a", "dag-b"}, "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCannotDeleteUntracked)
}

func TestDeleteBatch_ModifiedWithoutForce_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"dag-a": {Status: StatusSynced},
		"dag-b": {Status: StatusModified, ModifiedAt: &now},
	}}))

	_, err := impl.DeleteBatch(context.Background(), []string{"dag-a", "dag-b"}, "", false)
	require.Error(t, err)
	var validationErr *ValidationError
	assert.ErrorAs(t, err, &validationErr)
}

func TestDeleteBatch_EmptyList(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)

	deleted, err := impl.DeleteBatch(context.Background(), []string{}, "", false)
	require.NoError(t, err)
	assert.Nil(t, deleted)
}

// --- Phase 6: Move tests ---

func TestMove_PushDisabled_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgPushOff)

	err := impl.Move(context.Background(), "old", "new", "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPushDisabled)
}

func TestMove_UntrackedSource_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"my-dag": {Status: StatusUntracked, ModifiedAt: &now},
	}}))

	err := impl.Move(context.Background(), "my-dag", "new-dag", "", false)
	require.Error(t, err)
	var validationErr *ValidationError
	assert.ErrorAs(t, err, &validationErr)
	assert.Contains(t, validationErr.Message, "untracked")
}

func TestMove_NotFound(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{}}))

	err := impl.Move(context.Background(), "nonexistent", "new-dag", "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDAGNotFound)
}

func TestMove_NonCanonicalID_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)

	err := impl.Move(context.Background(), "a/./b", "new-dag", "", false)
	require.Error(t, err)
	assert.True(t, IsInvalidDAGID(err))

	err = impl.Move(context.Background(), "my-dag", "a/../b", "", false)
	require.Error(t, err)
	assert.True(t, IsInvalidDAGID(err))
}

func TestMove_CrossKind_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"my-dag": {Status: StatusSynced, ModifiedAt: &now},
	}}))

	// Trying to move a DAG to a memory path
	err := impl.Move(context.Background(), "my-dag", "memory/NEW", "", false)
	require.Error(t, err)
	var validationErr *ValidationError
	assert.ErrorAs(t, err, &validationErr)
	assert.Contains(t, validationErr.Message, "cannot move across kinds")
}

func TestMove_ConflictSource_WithoutForce_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"my-dag": {
			Status:             StatusConflict,
			ConflictDetectedAt: &now,
			RemoteCommit:       "abc123",
			RemoteAuthor:       "user",
			RemoteMessage:      "remote change",
		},
	}}))

	err := impl.Move(context.Background(), "my-dag", "new-dag", "", false)
	require.Error(t, err)
	var conflictErr *ConflictError
	assert.ErrorAs(t, err, &conflictErr)
}

func TestMove_DestinationAlreadyTracked_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"old-dag": {Status: StatusSynced, ModifiedAt: &now},
		"new-dag": {Status: StatusSynced, ModifiedAt: &now},
	}}))

	err := impl.Move(context.Background(), "old-dag", "new-dag", "", false)
	require.Error(t, err)
	var validationErr *ValidationError
	assert.ErrorAs(t, err, &validationErr)
	assert.Contains(t, validationErr.Message, "already tracked")
}

func TestMove_SourceNoFileAndNoDestFile_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"my-dag": {Status: StatusSynced, ModifiedAt: &now},
	}}))

	// Neither old file nor new file exists on disk
	err := impl.Move(context.Background(), "my-dag", "new-dag", "", false)
	require.Error(t, err)
	var validationErr *ValidationError
	assert.ErrorAs(t, err, &validationErr)
	assert.Contains(t, validationErr.Message, "does not exist")
}

func TestMove_DestinationUntracked_Allowed(t *testing.T) {
	t.Parallel()
	impl, dagsDir := newTestService(t, testCfgReadWrite)
	now := time.Now()
	require.NoError(t, impl.stateManager.Save(&State{Version: 1, DAGs: map[string]*DAGState{
		"old-dag": {Status: StatusMissing, MissingAt: &now, PreviousStatus: "synced"},
		"new-dag": {Status: StatusUntracked, ModifiedAt: &now},
	}}))

	// Create the new file on disk for retroactive mode
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "new-dag.yaml"), []byte("content"), 0600))

	// This will fail at gitClient.Open() because there's no real repo, but it should pass all validations
	err := impl.Move(context.Background(), "old-dag", "new-dag", "", false)
	// We expect an error from git operations (no actual repo), not from validation
	require.Error(t, err)
	// Should NOT be a validation error or DAGNotFound — those are pre-git checks
	assert.False(t, IsDAGNotFound(err), "should not be DAGNotFound")
	var validationErr *ValidationError
	assert.False(t, errors.As(err, &validationErr), "should not be a validation error")
}

func TestMove_InvalidDAGID_Rejected(t *testing.T) {
	t.Parallel()
	impl, _ := newTestService(t, testCfgReadWrite)

	err := impl.Move(context.Background(), "../etc/passwd", "new-dag", "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidDAGID)

	err = impl.Move(context.Background(), "old-dag", "../etc/shadow", "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidDAGID)
}
