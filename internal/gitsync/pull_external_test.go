// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
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

func TestPullSyncsTrackedSupportingFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)
	commitPullExternalTestFile(t, remoteRepo, remotePath, "workflow.yaml", "steps: []\n", "workflow")
	commitPullExternalTestFile(t, remoteRepo, remotePath, "README.md", "# Workflows\n", "readme")
	commitPullExternalExecutableFile(t, remoteRepo, remotePath, "scripts/run.sh", "#!/bin/sh\necho ok\n", "script")
	commitPullExternalTestFile(t, remoteRepo, remotePath, "data/blob.bin", string([]byte{0x00, 0x01, 0xFF}), "binary")

	dataDir := filepath.Join(root, "data")
	clonePullExternalTestRepo(ctx, t, dataDir, remotePath)
	dagsDir := filepath.Join(root, "dags")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:    true,
		Repository: remotePath,
		Branch:     "main",
	}, dagsDir, filepath.Join(dagsDir, "wiki"), dataDir)

	result, err := svc.Pull(ctx)
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Contains(t, result.Synced, "README.md")
	assert.Contains(t, result.Synced, "scripts/run.sh")

	content, err := os.ReadFile(filepath.Join(dagsDir, "scripts", "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\necho ok\n", string(content))
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dagsDir, "scripts", "run.sh"))
		require.NoError(t, err)
		assert.NotZero(t, info.Mode().Perm()&0100)
	}

	status, err := svc.GetStatus(ctx)
	require.NoError(t, err)
	require.Contains(t, status.Items, "scripts/run.sh")
	assert.Equal(t, gitsync.SyncItemKindFile, status.Items["scripts/run.sh"].Kind)

	localBinary := filepath.Join(dagsDir, "data", "blob.bin")
	require.NoError(t, os.WriteFile(localBinary, []byte{0x00, 0x02}, 0600))
	status, err = svc.GetStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, gitsync.StatusModified, status.Items["data/blob.bin"].Status)
	diff, err := svc.GetSyncItemDiff(ctx, "data/blob.bin")
	require.NoError(t, err)
	assert.True(t, diff.Binary)
	assert.Empty(t, diff.LocalContent)
	assert.Empty(t, diff.RemoteContent)
	require.NotNil(t, diff.LocalSize)
	require.NotNil(t, diff.RemoteSize)
	assert.Equal(t, int64(2), *diff.LocalSize)
	assert.Equal(t, int64(3), *diff.RemoteSize)
}

func TestPullHandlesRemoteKindChange(t *testing.T) {
	for _, tc := range []struct {
		name           string
		modifyLocal    bool
		expectConflict bool
	}{
		{name: "unchanged local item"},
		{name: "modified local item", modifyLocal: true, expectConflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			remotePath := filepath.Join(root, "remote")
			remoteRepo := initPullExternalTestRepo(t, remotePath)
			commitPullExternalTestFile(t, remoteRepo, remotePath, "task.yaml", "steps: []\n", "workflow")

			dataDir := filepath.Join(root, "data")
			clonePullExternalTestRepo(ctx, t, dataDir, remotePath)
			dagsDir := filepath.Join(root, "dags")
			svc := gitsync.NewService(&gitsync.Config{
				Enabled:    true,
				Repository: remotePath,
				Branch:     "main",
			}, dagsDir, filepath.Join(dagsDir, "wiki"), dataDir)

			_, err := svc.Pull(ctx)
			require.NoError(t, err)
			if tc.modifyLocal {
				require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "task.yaml"), []byte("steps:\n  - command: local\n"), 0600))
			}

			worktree, err := remoteRepo.Worktree()
			require.NoError(t, err)
			_, err = worktree.Remove("task.yaml")
			require.NoError(t, err)
			commitPullExternalTestFile(t, remoteRepo, remotePath, "task", "supporting file\n", "change kind")

			result, err := svc.Pull(ctx)
			require.NoError(t, err)
			if tc.expectConflict {
				assert.Contains(t, result.Conflicts, "task")
				forgotten, err := svc.Forget(ctx, []string{"task"})
				require.NoError(t, err)
				assert.Equal(t, []string{"task"}, forgotten)
				return
			}

			assert.Contains(t, result.Synced, "task")
			_, err = os.Stat(filepath.Join(dagsDir, "task.yaml"))
			assert.True(t, os.IsNotExist(err))
			content, err := os.ReadFile(filepath.Join(dagsDir, "task"))
			require.NoError(t, err)
			assert.Equal(t, "supporting file\n", string(content))
			status, err := svc.GetStatus(ctx)
			require.NoError(t, err)
			assert.Equal(t, gitsync.SyncItemKindFile, status.Items["task"].Kind)
		})
	}
}

func TestPullSyncsSupportingFilesUnderConfiguredPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)
	commitPullExternalTestFile(t, remoteRepo, remotePath, "workflows/main.yaml", "steps: []\n", "workflow")
	commitPullExternalTestFile(t, remoteRepo, remotePath, "workflows/scripts/run.sh", "echo ok\n", "script")
	commitPullExternalTestFile(t, remoteRepo, remotePath, "outside.txt", "outside\n", "outside")

	dataDir := filepath.Join(root, "data")
	clonePullExternalTestRepo(ctx, t, dataDir, remotePath)
	dagsDir := filepath.Join(root, "dags")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:    true,
		Repository: remotePath,
		Branch:     "main",
		Path:       "workflows",
	}, dagsDir, filepath.Join(dagsDir, "wiki"), dataDir)

	result, err := svc.Pull(ctx)
	require.NoError(t, err)
	assert.Contains(t, result.Synced, "scripts/run.sh")
	assert.FileExists(t, filepath.Join(dagsDir, "scripts", "run.sh"))
	assert.NoFileExists(t, filepath.Join(dagsDir, "workflows", "scripts", "run.sh"))
	assert.NoFileExists(t, filepath.Join(dagsDir, "outside.txt"))
}

func TestPullRemovesUnchangedSupportingFileDeletedRemotely(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)
	commitPullExternalTestFile(t, remoteRepo, remotePath, "scripts/run.sh", "echo ok\n", "script")

	dataDir := filepath.Join(root, "data")
	clonePullExternalTestRepo(ctx, t, dataDir, remotePath)
	dagsDir := filepath.Join(root, "dags")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:    true,
		Repository: remotePath,
		Branch:     "main",
	}, dagsDir, filepath.Join(dagsDir, "wiki"), dataDir)

	_, err := svc.Pull(ctx)
	require.NoError(t, err)
	removePullExternalTestFile(t, remoteRepo, "scripts/run.sh", "remove script")

	result, err := svc.Pull(ctx)
	require.NoError(t, err)
	assert.Contains(t, result.Deleted, "scripts/run.sh")
	assert.NoFileExists(t, filepath.Join(dagsDir, "scripts", "run.sh"))

	status, err := svc.GetStatus(ctx)
	require.NoError(t, err)
	assert.NotContains(t, status.Items, "scripts/run.sh")
}

func TestPullPreservesModifiedSupportingFileDeletedRemotely(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)
	commitPullExternalTestFile(t, remoteRepo, remotePath, "scripts/run.sh", "echo remote\n", "script")

	dataDir := filepath.Join(root, "data")
	clonePullExternalTestRepo(ctx, t, dataDir, remotePath)
	dagsDir := filepath.Join(root, "dags")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:    true,
		Repository: remotePath,
		Branch:     "main",
	}, dagsDir, filepath.Join(dagsDir, "wiki"), dataDir)

	_, err := svc.Pull(ctx)
	require.NoError(t, err)
	localPath := filepath.Join(dagsDir, "scripts", "run.sh")
	require.NoError(t, os.WriteFile(localPath, []byte("echo local\n"), 0600))
	removePullExternalTestFile(t, remoteRepo, "scripts/run.sh", "remove script")

	result, err := svc.Pull(ctx)
	require.NoError(t, err)
	assert.Contains(t, result.Conflicts, "scripts/run.sh")
	content, err := os.ReadFile(localPath)
	require.NoError(t, err)
	assert.Equal(t, "echo local\n", string(content))

	diff, err := svc.GetSyncItemDiff(ctx, "scripts/run.sh")
	require.NoError(t, err)
	assert.True(t, diff.RemoteDeleted)
	assert.Equal(t, gitsync.SyncItemKindFile, diff.Kind)
	assert.Empty(t, diff.RemoteContent)

	require.NoError(t, svc.Discard(ctx, "scripts/run.sh"))
	assert.NoFileExists(t, localPath)
	status, err := svc.GetStatus(ctx)
	require.NoError(t, err)
	assert.NotContains(t, status.Items, "scripts/run.sh")
}

func TestSupportingFileModeChangeIsModified(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose the Git executable bit as a POSIX mode")
	}
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)
	commitPullExternalExecutableFile(t, remoteRepo, remotePath, "scripts/run.sh", "echo ok\n", "script")

	dataDir := filepath.Join(root, "data")
	clonePullExternalTestRepo(ctx, t, dataDir, remotePath)
	dagsDir := filepath.Join(root, "dags")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:    true,
		Repository: remotePath,
		Branch:     "main",
	}, dagsDir, filepath.Join(dagsDir, "wiki"), dataDir)

	_, err := svc.Pull(ctx)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(filepath.Join(dagsDir, "scripts", "run.sh"), 0600))

	status, err := svc.GetStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, gitsync.StatusModified, status.Items["scripts/run.sh"].Status)
	diff, err := svc.GetSyncItemDiff(ctx, "scripts/run.sh")
	require.NoError(t, err)
	require.NotNil(t, diff.LocalExecutable)
	require.NotNil(t, diff.RemoteExecutable)
	assert.False(t, *diff.LocalExecutable)
	assert.True(t, *diff.RemoteExecutable)
}

func TestPullRejectsSupportingFileIDCollision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)
	commitPullExternalTestFile(t, remoteRepo, remotePath, "workflow.yaml", "steps: []\n", "workflow")
	commitPullExternalTestFile(t, remoteRepo, remotePath, "workflow", "supporting data\n", "supporting file")

	dataDir := filepath.Join(root, "data")
	clonePullExternalTestRepo(ctx, t, dataDir, remotePath)
	dagsDir := filepath.Join(root, "dags")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:    true,
		Repository: remotePath,
		Branch:     "main",
	}, dagsDir, filepath.Join(dagsDir, "wiki"), dataDir)

	_, err := svc.Pull(ctx)
	require.Error(t, err)
	var validationErr *gitsync.ValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Contains(t, validationErr.Message, "collides")
	assert.NoFileExists(t, filepath.Join(dagsDir, "workflow.yaml"))
}

func TestSupportingFileWriteLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose the Git executable bit as a POSIX mode")
	}
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote.git")
	remoteRepo, err := git.PlainInit(remotePath, true)
	require.NoError(t, err)
	require.NoError(t, remoteRepo.Storer.SetReference(plumbing.NewSymbolicReference(
		plumbing.HEAD,
		plumbing.NewBranchReferenceName("main"),
	)))
	seedPath := filepath.Join(root, "seed")
	seedRepo := initPullExternalTestRepo(t, seedPath)
	commitPullExternalTestFile(t, seedRepo, seedPath, "scripts/run.sh", "echo initial\n", "script")
	_, err = seedRepo.CreateRemote(&gitconfig.RemoteConfig{Name: "upstream", URLs: []string{remotePath}})
	require.NoError(t, err)
	require.NoError(t, seedRepo.Push(&git.PushOptions{
		RemoteName: "upstream",
		RefSpecs: []gitconfig.RefSpec{
			"refs/heads/main:refs/heads/main",
		},
	}))

	dataDir := filepath.Join(root, "data")
	clonePullExternalTestRepo(ctx, t, dataDir, remotePath)
	dagsDir := filepath.Join(root, "dags")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:     true,
		Repository:  remotePath,
		Branch:      "main",
		PushEnabled: true,
		Commit: gitsync.CommitConfig{
			AuthorName:  "Test User",
			AuthorEmail: "test@example.com",
		},
	}, dagsDir, filepath.Join(dagsDir, "wiki"), dataDir)

	_, err = svc.Pull(ctx)
	require.NoError(t, err)
	localPath := filepath.Join(dagsDir, "scripts", "run.sh")
	require.NoError(t, os.WriteFile(localPath, []byte("echo published\n"), 0700))
	require.NoError(t, os.Chmod(localPath, 0700))
	_, err = svc.GetStatus(ctx)
	require.NoError(t, err)
	_, err = svc.Publish(ctx, "scripts/run.sh", "publish script", false)
	require.NoError(t, err)

	file := pullExternalHeadFile(t, remoteRepo, "scripts/run.sh")
	content, err := file.Contents()
	require.NoError(t, err)
	assert.Equal(t, "echo published\n", content)
	assert.Equal(t, filemode.Executable, file.Mode)

	require.NoError(t, svc.Move(ctx, "scripts/run.sh", "scripts/job.sh", "move script", false))
	assertPullExternalHeadFileMissing(t, remoteRepo, "scripts/run.sh")
	file = pullExternalHeadFile(t, remoteRepo, "scripts/job.sh")
	assert.Equal(t, filemode.Executable, file.Mode)

	require.NoError(t, svc.Delete(ctx, "scripts/job.sh", "delete script", false))
	assertPullExternalHeadFileMissing(t, remoteRepo, "scripts/job.sh")
	assert.NoFileExists(t, filepath.Join(dagsDir, "scripts", "job.sh"))
}

func TestPullAdoptsLegacyDocsDirectory(t *testing.T) {
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
	wikiPath := filepath.Join(root, "content")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:    true,
		Repository: remotePath,
		Branch:     "main",
	}, dagsDir, wikiPath, dataDir)

	result, err := svc.Pull(ctx)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Contains(t, result.Synced, "docs/operations/deploy")

	content, err := os.ReadFile(filepath.Join(wikiPath, "operations", "deploy.md"))
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

func TestPullSyncsWikiPageAttachments(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)

	pngBytes := string([]byte{0x89, 'P', 'N', 'G', 0x00, 0x01, 0xFF})
	commitPullExternalTestFile(t, remoteRepo, remotePath, "wiki/guides/setup.md", "# Setup\n", "page")
	commitPullExternalTestFile(t, remoteRepo, remotePath, "wiki/.attachments/guides/setup/logo.png", pngBytes, "asset")
	// Hostile or malformed asset paths must never reach the local disk:
	// a reserved extension and a file with no doc segment.
	commitPullExternalTestFile(t, remoteRepo, remotePath, "wiki/.attachments/guides/setup/evil.md", "# evil\n", "evil")
	commitPullExternalTestFile(t, remoteRepo, remotePath, "wiki/.attachments/stray.png", "stray", "stray")

	dataDir := filepath.Join(root, "data")
	_, err := git.PlainCloneContext(ctx, filepath.Join(dataDir, "gitsync", "repo"), false, &git.CloneOptions{
		URL:           remotePath,
		ReferenceName: plumbing.NewBranchReferenceName("main"),
		SingleBranch:  true,
		Depth:         1,
	})
	require.NoError(t, err)

	dagsDir := filepath.Join(root, "dags")
	wikiDir := filepath.Join(dagsDir, "wiki")
	svc := gitsync.NewService(&gitsync.Config{
		Enabled:    true,
		Repository: remotePath,
		Branch:     "main",
	}, dagsDir, wikiDir, dataDir)

	result, err := svc.Pull(ctx)
	require.NoError(t, err)
	require.True(t, result.Success)

	assetID := "wiki/.attachments/guides/setup/logo.png"
	assert.Contains(t, result.Synced, assetID)

	localAsset := filepath.Join(wikiDir, ".attachments", "guides", "setup", "logo.png")
	content, err := os.ReadFile(localAsset)
	require.NoError(t, err)
	assert.Equal(t, pngBytes, string(content))

	_, err = os.Lstat(filepath.Join(wikiDir, ".attachments", "guides", "setup", "evil.md"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Lstat(filepath.Join(wikiDir, ".attachments", "stray.png"))
	assert.True(t, os.IsNotExist(err))

	status, err := svc.GetStatus(ctx)
	require.NoError(t, err)
	assetState := status.Items[assetID]
	require.NotNil(t, assetState)
	assert.Equal(t, gitsync.SyncItemKindWikiPageAsset, assetState.Kind)
	assert.Equal(t, gitsync.StatusSynced, assetState.Status)
	assert.NotContains(t, status.Items, "wiki/.attachments/guides/setup/evil")
	assert.NotContains(t, status.Items, "wiki/.attachments/guides/setup/evil.md")
	assert.NotContains(t, status.Items, "wiki/.attachments/stray.png")

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
	require.NotNil(t, diff.LocalSize)
	require.NotNil(t, diff.RemoteSize)
	assert.Equal(t, int64(len("changed-bytes")), *diff.LocalSize)
	assert.Equal(t, int64(len(pngBytes)), *diff.RemoteSize)
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

func clonePullExternalTestRepo(ctx context.Context, t *testing.T, dataDir, remotePath string) {
	t.Helper()

	_, err := git.PlainCloneContext(ctx, filepath.Join(dataDir, "gitsync", "repo"), false, &git.CloneOptions{
		URL:           remotePath,
		ReferenceName: plumbing.NewBranchReferenceName("main"),
		SingleBranch:  true,
		Depth:         1,
	})
	require.NoError(t, err)
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

func commitPullExternalExecutableFile(t *testing.T, repo *git.Repository, repoPath, filePath, content, message string) plumbing.Hash {
	t.Helper()

	fullPath := filepath.Join(repoPath, filePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
	require.NoError(t, os.WriteFile(fullPath, []byte(content), 0755))
	require.NoError(t, os.Chmod(fullPath, 0755))

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

func removePullExternalTestFile(t *testing.T, repo *git.Repository, filePath, message string) plumbing.Hash {
	t.Helper()

	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Remove(filePath)
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

func pullExternalHeadFile(t *testing.T, repo *git.Repository, filePath string) *object.File {
	t.Helper()

	head, err := repo.Head()
	require.NoError(t, err)
	commit, err := repo.CommitObject(head.Hash())
	require.NoError(t, err)
	tree, err := commit.Tree()
	require.NoError(t, err)
	file, err := tree.File(filePath)
	require.NoError(t, err)
	return file
}

func assertPullExternalHeadFileMissing(t *testing.T, repo *git.Repository, filePath string) {
	t.Helper()

	head, err := repo.Head()
	require.NoError(t, err)
	commit, err := repo.CommitObject(head.Hash())
	require.NoError(t, err)
	tree, err := commit.Tree()
	require.NoError(t, err)
	_, err = tree.File(filePath)
	require.Error(t, err)
}
