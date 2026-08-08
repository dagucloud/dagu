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

	"github.com/dagucloud/dagu/v2/internal/gitsync"
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
	}, dagsDir, filepath.Join(dagsDir, "docs"), dataDir)

	result, err := svc.Pull(ctx)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Contains(t, result.Synced, "initial")

	content, err := os.ReadFile(filepath.Join(dagsDir, "initial.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "steps: []\n", string(content))

	status, err := svc.GetStatus(ctx)
	require.NoError(t, err)
	require.Contains(t, status.Items, "initial")
	assert.Equal(t, gitsync.StatusSynced, status.Items["initial"].Status)
	assert.Equal(t, commitHash.String(), status.Items["initial"].BaseCommit)
}

func TestPullPreservesShortYAMLExtension(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)
	commitPullExternalTestFile(t, remoteRepo, remotePath, "short.yml", "steps: []\n", "initial")

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
	}, dagsDir, filepath.Join(dagsDir, "docs"), dataDir)

	result, err := svc.Pull(ctx)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Contains(t, result.Synced, "short")

	content, err := os.ReadFile(filepath.Join(dagsDir, "short.yml"))
	require.NoError(t, err)
	assert.Equal(t, "steps: []\n", string(content))

	status, err := svc.GetStatus(ctx)
	require.NoError(t, err)
	require.Contains(t, status.Items, "short")
	assert.Equal(t, ".yml", status.Items["short"].FileExtension)
}

func TestPullWritesDocumentsToConfiguredDocsDir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)
	commitPullExternalTestFile(t, remoteRepo, remotePath, "docs/operations/deploy.md", "# Deploy\n", "initial")

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
	docsPath := filepath.Join(root, "content")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:    true,
		Repository: remotePath,
		Branch:     "main",
	}, dagsDir, docsPath, dataDir)

	result, err := svc.Pull(ctx)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Contains(t, result.Synced, "docs/operations/deploy")

	content, err := os.ReadFile(filepath.Join(docsPath, "operations", "deploy.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Deploy\n", string(content))
	_, err = os.Stat(filepath.Join(dagsDir, "docs", "operations", "deploy.md"))
	assert.ErrorIs(t, err, os.ErrNotExist)
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
	}, dagsDir, filepath.Join(dagsDir, "docs"), dataDir)

	result, err := svc.Pull(ctx)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Equal(t, "Failed to sync files", result.Message)
	assert.Contains(t, err.Error(), "failed to write")
}

func TestPullSyncsDocAttachments(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)

	pngBytes := string([]byte{0x89, 'P', 'N', 'G', 0x00, 0x01, 0xFF})
	commitPullExternalTestFile(t, remoteRepo, remotePath, "docs/guides/setup.md", "# Setup\n", "doc")
	commitPullExternalTestFile(t, remoteRepo, remotePath, "docs/.attachments/guides/setup/logo.png", pngBytes, "asset")
	// Hostile or malformed asset paths must never reach the local disk:
	// a reserved extension and a file with no doc segment.
	commitPullExternalTestFile(t, remoteRepo, remotePath, "docs/.attachments/guides/setup/evil.md", "# evil\n", "evil")
	commitPullExternalTestFile(t, remoteRepo, remotePath, "docs/.attachments/stray.png", "stray", "stray")

	dataDir := filepath.Join(root, "data")
	_, err := git.PlainCloneContext(ctx, filepath.Join(dataDir, "gitsync", "repo"), false, &git.CloneOptions{
		URL:           remotePath,
		ReferenceName: plumbing.NewBranchReferenceName("main"),
		SingleBranch:  true,
		Depth:         1,
	})
	require.NoError(t, err)

	dagsDir := filepath.Join(root, "dags")
	docsDir := filepath.Join(dagsDir, "docs")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:    true,
		Repository: remotePath,
		Branch:     "main",
	}, dagsDir, docsDir, dataDir)

	result, err := svc.Pull(ctx)
	require.NoError(t, err)
	require.True(t, result.Success)

	assetID := "docs/.attachments/guides/setup/logo.png"
	assert.Contains(t, result.Synced, assetID)

	localAsset := filepath.Join(docsDir, ".attachments", "guides", "setup", "logo.png")
	content, err := os.ReadFile(localAsset)
	require.NoError(t, err)
	assert.Equal(t, pngBytes, string(content))

	_, err = os.Lstat(filepath.Join(docsDir, ".attachments", "guides", "setup", "evil.md"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Lstat(filepath.Join(docsDir, ".attachments", "stray.png"))
	assert.True(t, os.IsNotExist(err))

	status, err := svc.GetStatus(ctx)
	require.NoError(t, err)
	assetState := status.Items[assetID]
	require.NotNil(t, assetState)
	assert.Equal(t, gitsync.SyncItemKindDocAsset, assetState.Kind)
	assert.Equal(t, gitsync.StatusSynced, assetState.Status)
	assert.NotContains(t, status.Items, "docs/.attachments/guides/setup/evil")
	assert.NotContains(t, status.Items, "docs/.attachments/guides/setup/evil.md")
	assert.NotContains(t, status.Items, "docs/.attachments/stray.png")

	// A second pull is idempotent.
	result, err = svc.Pull(ctx)
	require.NoError(t, err)
	require.True(t, result.Success)

	// Local modification surfaces as modified, and the diff withholds the
	// binary content while reporting sizes.
	require.NoError(t, os.WriteFile(localAsset, []byte("changed-bytes"), 0600))
	status, err = svc.GetStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, gitsync.StatusModified, status.Items[assetID].Status)

	diff, err := svc.GetSyncItemDiff(ctx, assetID)
	require.NoError(t, err)
	assert.True(t, diff.Binary)
	assert.Empty(t, diff.LocalContent)
	assert.Empty(t, diff.RemoteContent)
	assert.Equal(t, int64(len("changed-bytes")), diff.LocalSize)
	assert.Equal(t, int64(len(pngBytes)), diff.RemoteSize)
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
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
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
