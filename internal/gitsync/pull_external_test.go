// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/gitsync"
)

func TestPullCreatesMissingDAGsDirOnInitialSync(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)
	commitHash := commitPullExternalTestFile(t, remoteRepo, remotePath, "initial.yaml", "steps: []\n", "initial")

	dataDir := filepath.Join(root, "data")
	repoPath := filepath.Join(dataDir, "gitsync", "repo")
	_, err := git.PlainCloneContext(ctx, repoPath, false, &git.CloneOptions{
		URL:           remotePath,
		ReferenceName: plumbing.NewBranchReferenceName("main"),
		SingleBranch:  true,
		Depth:         1,
	})
	require.NoError(t, err)

	dagsDir := filepath.Join(root, "dags")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:    true,
		Repository: remotePath,
		Branch:     "main",
	}, dagsDir, dataDir)

	result, err := svc.Pull(ctx)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Contains(t, result.Synced, "initial")

	content, err := os.ReadFile(filepath.Join(dagsDir, "initial.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "steps: []\n", string(content))

	status, err := svc.GetStatus(ctx)
	require.NoError(t, err)
	require.Contains(t, status.DAGs, "initial")
	assert.Equal(t, gitsync.StatusSynced, status.DAGs["initial"].Status)
	assert.Equal(t, commitHash.String(), status.DAGs["initial"].BaseCommit)
}

func TestPullReturnsErrorWhenMissingDAGsDirCannotBeCreated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)
	commitPullExternalTestFile(t, remoteRepo, remotePath, "initial.yaml", "steps: []\n", "initial")

	dataDir := filepath.Join(root, "data")
	repoPath := filepath.Join(dataDir, "gitsync", "repo")
	_, err := git.PlainCloneContext(ctx, repoPath, false, &git.CloneOptions{
		URL:           remotePath,
		ReferenceName: plumbing.NewBranchReferenceName("main"),
		SingleBranch:  true,
		Depth:         1,
	})
	require.NoError(t, err)

	blockingFile := filepath.Join(root, "dags-parent")
	require.NoError(t, os.WriteFile(blockingFile, []byte("not a directory\n"), 0600))
	dagsDir := filepath.Join(blockingFile, "dags")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:    true,
		Repository: remotePath,
		Branch:     "main",
	}, dagsDir, dataDir)

	result, err := svc.Pull(ctx)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Equal(t, "Failed to sync files", result.Message)
	assert.Contains(t, err.Error(), "failed to write")
}

func initPullExternalTestRepo(t *testing.T, repoPath string) *git.Repository {
	t.Helper()

	repo, err := git.PlainInit(repoPath, false)
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewSymbolicReference(
		plumbing.HEAD,
		plumbing.NewBranchReferenceName("main"),
	)))
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{repoPath}})
	require.NoError(t, err)
	return repo
}

func commitPullExternalTestFile(t *testing.T, repo *git.Repository, repoPath, filePath, content, message string) plumbing.Hash {
	t.Helper()

	fullPath := filepath.Join(repoPath, filePath)
	require.NoError(t, os.WriteFile(fullPath, []byte(content), 0644))

	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add(filePath)
	require.NoError(t, err)

	hash, err := wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test User",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)
	return hash
}
