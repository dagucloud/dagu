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
		name             string
		modifyLocal      bool
		preserveMetadata bool
		expectConflict   bool
	}{
		{name: "unchanged local item"},
		{name: "modified local item", modifyLocal: true, expectConflict: true},
		{name: "modified local item with unchanged metadata", modifyLocal: true, preserveMetadata: true, expectConflict: true},
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
				localPath := filepath.Join(dagsDir, "task.yaml")
				info, err := os.Stat(localPath)
				require.NoError(t, err)
				content := "steps:\n  - command: local\n"
				if tc.preserveMetadata {
					content = "local: []\n"
				}
				require.NoError(t, os.WriteFile(localPath, []byte(content), 0600))
				if tc.preserveMetadata {
					require.NoError(t, os.Chtimes(localPath, info.ModTime(), info.ModTime()))
				}
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
	info, err := os.Stat(localPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(localPath, []byte("echo edited\n"), 0600))
	require.NoError(t, os.Chtimes(localPath, info.ModTime(), info.ModTime()))
	removePullExternalTestFile(t, remoteRepo, "scripts/run.sh", "remove script")

	result, err := svc.Pull(ctx)
	require.NoError(t, err)
	assert.Contains(t, result.Conflicts, "scripts/run.sh")
	content, err := os.ReadFile(localPath)
	require.NoError(t, err)
	assert.Equal(t, "echo edited\n", string(content))

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

func TestPullPreservesPercentInSupportingFileNames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote")
	remoteRepo := initPullExternalTestRepo(t, remotePath)
	commitPullExternalTestFile(t, remoteRepo, remotePath, "assets/100%.txt", "percent\n", "percent")
	commitPullExternalTestFile(t, remoteRepo, remotePath, "assets/literal%2F.txt", "escape\n", "escape")

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
	assert.Contains(t, result.Synced, "assets/100%.txt")
	assert.Contains(t, result.Synced, "assets/literal%2F.txt")
	assert.FileExists(t, filepath.Join(dagsDir, "assets", "100%.txt"))
	assert.FileExists(t, filepath.Join(dagsDir, "assets", "literal%2F.txt"))

	status, err := svc.GetStatus(ctx)
	require.NoError(t, err)
	assert.Contains(t, status.Items, "assets/100%.txt")
	assert.Contains(t, status.Items, "assets/literal%2F.txt")
}

func TestDeleteDetectsSameMetadataEdit(t *testing.T) {
	t.Parallel()

	env := newPullExternalPushTest(t, []pullExternalTestFile{
		{path: "scripts/run.sh", content: "echo remote\n"},
	})
	localPath := filepath.Join(env.dagsDir, "scripts", "run.sh")
	info, err := os.Stat(localPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(localPath, []byte("echo edited\n"), 0600))
	require.NoError(t, os.Chtimes(localPath, info.ModTime(), info.ModTime()))

	err = env.svc.Delete(env.ctx, "scripts/run.sh", "delete script", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local modifications")
	assert.FileExists(t, localPath)
	assert.Equal(t, "echo remote\n", pullExternalFileContent(t, env.remoteRepo, "scripts/run.sh"))
}

func TestDeleteDetectsReappearedEdit(t *testing.T) {
	t.Parallel()

	env := newPullExternalPushTest(t, []pullExternalTestFile{
		{path: "scripts/run.sh", content: "echo remote\n"},
	})
	localPath := filepath.Join(env.dagsDir, "scripts", "run.sh")
	require.NoError(t, os.Remove(localPath))
	_, err := env.svc.GetStatus(env.ctx)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(localPath, []byte("echo edited\n"), 0600))

	err = env.svc.Delete(env.ctx, "scripts/run.sh", "delete script", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local modifications")
	assert.Equal(t, "echo edited\n", readPullExternalTestFile(t, localPath))
	assert.Equal(t, "echo remote\n", pullExternalFileContent(t, env.remoteRepo, "scripts/run.sh"))
}

func TestRejectedPushPreservesDestructiveTargets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []pullExternalTestFile
		run   func(t *testing.T, env *pullExternalPushTest)
	}{
		{
			name: "move",
			files: []pullExternalTestFile{
				{path: "scripts/run.sh", content: "echo run\n"},
			},
			run: func(t *testing.T, env *pullExternalPushTest) {
				err := env.svc.Move(env.ctx, "scripts/run.sh", "scripts/job.sh", "move script", false)
				require.Error(t, err)
				assert.Equal(t, "echo run\n", pullExternalFileContent(t, env.remoteRepo, "scripts/run.sh"))
				assertPullExternalHeadFileMissing(t, env.remoteRepo, "scripts/job.sh")
				assert.FileExists(t, filepath.Join(env.dagsDir, "scripts", "run.sh"))
				assert.NoFileExists(t, filepath.Join(env.dagsDir, "scripts", "job.sh"))
				status, statusErr := env.svc.GetStatus(env.ctx)
				require.NoError(t, statusErr)
				assert.Contains(t, status.Items, "scripts/run.sh")
				assert.NotContains(t, status.Items, "scripts/job.sh")
			},
		},
		{
			name: "delete",
			files: []pullExternalTestFile{
				{path: "scripts/run.sh", content: "echo run\n"},
			},
			run: func(t *testing.T, env *pullExternalPushTest) {
				err := env.svc.Delete(env.ctx, "scripts/run.sh", "delete script", false)
				require.Error(t, err)
				assert.Equal(t, "echo run\n", pullExternalFileContent(t, env.remoteRepo, "scripts/run.sh"))
				assert.FileExists(t, filepath.Join(env.dagsDir, "scripts", "run.sh"))
				status, statusErr := env.svc.GetStatus(env.ctx)
				require.NoError(t, statusErr)
				assert.Contains(t, status.Items, "scripts/run.sh")
			},
		},
		{
			name: "batch delete",
			files: []pullExternalTestFile{
				{path: "scripts/run.sh", content: "echo run\n"},
				{path: "scripts/job.sh", content: "echo job\n"},
			},
			run: func(t *testing.T, env *pullExternalPushTest) {
				_, err := env.svc.DeleteBatch(env.ctx, []string{"scripts/run.sh", "scripts/job.sh"}, "delete scripts", false)
				require.Error(t, err)
				assert.Equal(t, "echo run\n", pullExternalFileContent(t, env.remoteRepo, "scripts/run.sh"))
				assert.Equal(t, "echo job\n", pullExternalFileContent(t, env.remoteRepo, "scripts/job.sh"))
				assert.FileExists(t, filepath.Join(env.dagsDir, "scripts", "run.sh"))
				assert.FileExists(t, filepath.Join(env.dagsDir, "scripts", "job.sh"))
				status, statusErr := env.svc.GetStatus(env.ctx)
				require.NoError(t, statusErr)
				assert.Contains(t, status.Items, "scripts/run.sh")
				assert.Contains(t, status.Items, "scripts/job.sh")
			},
		},
		{
			name: "delete all missing",
			files: []pullExternalTestFile{
				{path: "scripts/run.sh", content: "echo run\n"},
			},
			run: func(t *testing.T, env *pullExternalPushTest) {
				require.NoError(t, os.Remove(filepath.Join(env.dagsDir, "scripts", "run.sh")))
				_, err := env.svc.GetStatus(env.ctx)
				require.NoError(t, err)

				_, err = env.svc.DeleteAllMissing(env.ctx, "delete missing scripts")
				require.Error(t, err)
				assert.Equal(t, "echo run\n", pullExternalFileContent(t, env.remoteRepo, "scripts/run.sh"))
				status, statusErr := env.svc.GetStatus(env.ctx)
				require.NoError(t, statusErr)
				assert.Contains(t, status.Items, "scripts/run.sh")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newPullExternalPushTest(t, tc.files)
			env.advanceRemote(t)
			tc.run(t, env)
			assertPullExternalCloneAt(t, env.dataDir, env.baseHead)
		})
	}
}

func TestFailedPublishRemainsModified(t *testing.T) {
	for _, tc := range []struct {
		name    string
		publish func(context.Context, gitsync.Service) error
	}{
		{
			name: "single",
			publish: func(ctx context.Context, svc gitsync.Service) error {
				_, err := svc.Publish(ctx, "scripts/run.sh", "publish script", false)
				return err
			},
		},
		{
			name: "batch",
			publish: func(ctx context.Context, svc gitsync.Service) error {
				_, err := svc.PublishAll(ctx, "publish scripts", []string{"scripts/run.sh"})
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newPullExternalPushTest(t, []pullExternalTestFile{
				{path: "scripts/run.sh", content: "echo remote\n"},
			})
			localPath := filepath.Join(env.dagsDir, "scripts", "run.sh")
			require.NoError(t, os.WriteFile(localPath, []byte("echo edited\n"), 0600))
			status, err := env.svc.GetStatus(env.ctx)
			require.NoError(t, err)
			require.Equal(t, gitsync.StatusModified, status.Items["scripts/run.sh"].Status)

			failedCtx, cancel := context.WithCancel(env.ctx)
			cancel()
			err = tc.publish(failedCtx, env.svc)
			require.ErrorIs(t, err, context.Canceled)
			assertPullExternalCloneAt(t, env.dataDir, env.baseHead)

			_, err = env.svc.Pull(env.ctx)
			require.NoError(t, err)
			status, err = env.svc.GetStatus(env.ctx)
			require.NoError(t, err)
			assert.Equal(t, gitsync.StatusModified, status.Items["scripts/run.sh"].Status)
			assert.Equal(t, "echo remote\n", pullExternalFileContent(t, env.remoteRepo, "scripts/run.sh"))

			require.NoError(t, tc.publish(env.ctx, env.svc))
			assert.Equal(t, "echo edited\n", pullExternalFileContent(t, env.remoteRepo, "scripts/run.sh"))
		})
	}
}

func TestForcePublishResolvesRemoteKindChange(t *testing.T) {
	for _, tc := range []struct {
		name            string
		initialPath     string
		replacementPath string
		localContent    string
		kind            gitsync.SyncItemKind
	}{
		{
			name:            "DAG replaces supporting file",
			initialPath:     "task",
			replacementPath: "task.yaml",
			localContent:    "local file\n",
			kind:            gitsync.SyncItemKindFile,
		},
		{
			name:            "supporting file replaces DAG",
			initialPath:     "task.yaml",
			replacementPath: "task",
			localContent:    "steps:\n  - command: local\n",
			kind:            gitsync.SyncItemKindDAG,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newPullExternalPushTest(t, []pullExternalTestFile{{
				path:    tc.initialPath,
				content: "initial\n",
			}})
			require.NoError(t, os.WriteFile(
				filepath.Join(env.dagsDir, filepath.FromSlash(tc.initialPath)),
				[]byte(tc.localContent),
				0600,
			))

			worktree, err := env.seedRepo.Worktree()
			require.NoError(t, err)
			_, err = worktree.Remove(tc.initialPath)
			require.NoError(t, err)
			commitPullExternalTestFile(
				t, env.seedRepo, env.seedPath, tc.replacementPath, "remote replacement\n", "replace kind",
			)
			require.NoError(t, env.seedRepo.Push(&git.PushOptions{
				RemoteName: "upstream",
				RefSpecs: []gitconfig.RefSpec{
					"refs/heads/main:refs/heads/main",
				},
			}))

			result, err := env.svc.Pull(env.ctx)
			require.NoError(t, err)
			assert.Contains(t, result.Conflicts, "task")

			_, err = env.svc.Publish(env.ctx, "task", "keep local kind", true)
			require.NoError(t, err)
			assert.Equal(t, tc.localContent, pullExternalFileContent(t, env.remoteRepo, tc.initialPath))
			assertPullExternalHeadFileMissing(t, env.remoteRepo, tc.replacementPath)

			_, err = env.svc.Pull(env.ctx)
			require.NoError(t, err)
			status, err := env.svc.GetStatus(env.ctx)
			require.NoError(t, err)
			assert.Equal(t, gitsync.StatusSynced, status.Items["task"].Status)
			assert.Equal(t, tc.kind, status.Items["task"].Kind)
		})
	}
}

func TestForceDeleteResolvesRemoteKindChange(t *testing.T) {
	for _, tc := range []struct {
		name            string
		initialPath     string
		replacementPath string
		batch           bool
	}{
		{
			name:            "single delete supporting file replaced by DAG",
			initialPath:     "task",
			replacementPath: "task.yaml",
		},
		{
			name:            "batch delete DAG replaced by supporting file",
			initialPath:     "task.yaml",
			replacementPath: "task",
			batch:           true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newPullExternalPushTest(t, []pullExternalTestFile{{
				path:    tc.initialPath,
				content: "initial\n",
			}})
			require.NoError(t, os.WriteFile(
				filepath.Join(env.dagsDir, filepath.FromSlash(tc.initialPath)),
				[]byte("local change\n"),
				0600,
			))

			worktree, err := env.seedRepo.Worktree()
			require.NoError(t, err)
			_, err = worktree.Remove(tc.initialPath)
			require.NoError(t, err)
			commitPullExternalTestFile(
				t, env.seedRepo, env.seedPath, tc.replacementPath, "remote replacement\n", "replace kind",
			)
			require.NoError(t, env.seedRepo.Push(&git.PushOptions{
				RemoteName: "upstream",
				RefSpecs: []gitconfig.RefSpec{
					"refs/heads/main:refs/heads/main",
				},
			}))

			result, err := env.svc.Pull(env.ctx)
			require.NoError(t, err)
			assert.Contains(t, result.Conflicts, "task")

			if tc.batch {
				deleted, err := env.svc.DeleteBatch(env.ctx, []string{"task"}, "delete task", true)
				require.NoError(t, err)
				assert.Equal(t, []string{"task"}, deleted)
			} else {
				require.NoError(t, env.svc.Delete(env.ctx, "task", "delete task", true))
			}

			assertPullExternalHeadFileMissing(t, env.remoteRepo, tc.initialPath)
			assertPullExternalHeadFileMissing(t, env.remoteRepo, tc.replacementPath)
			for _, filePath := range []string{tc.initialPath, tc.replacementPath} {
				_, err := os.Stat(filepath.Join(env.dagsDir, filepath.FromSlash(filePath)))
				assert.True(t, os.IsNotExist(err))
			}

			result, err = env.svc.Pull(env.ctx)
			require.NoError(t, err)
			assert.NotContains(t, result.Synced, "task")
			status, err := env.svc.GetStatus(env.ctx)
			require.NoError(t, err)
			assert.NotContains(t, status.Items, "task")
		})
	}
}

func TestSupportingFileNamedBaseOperations(t *testing.T) {
	env := newPullExternalPushTest(t, []pullExternalTestFile{{
		path:    "base",
		content: "initial\n",
	}})
	localPath := filepath.Join(env.dagsDir, "base")
	require.NoError(t, os.WriteFile(localPath, []byte("edited\n"), 0600))
	status, err := env.svc.GetStatus(env.ctx)
	require.NoError(t, err)
	require.Equal(t, gitsync.StatusModified, status.Items["base"].Status)

	_, err = env.svc.PublishAll(env.ctx, "publish base", []string{"base"})
	require.NoError(t, err)
	assert.Equal(t, "edited\n", pullExternalFileContent(t, env.remoteRepo, "base"))

	require.NoError(t, env.svc.Move(env.ctx, "base", "renamed", "move base", false))
	assertPullExternalHeadFileMissing(t, env.remoteRepo, "base")
	assert.Equal(t, "edited\n", pullExternalFileContent(t, env.remoteRepo, "renamed"))
	assert.NoFileExists(t, localPath)
	assert.Equal(t, "edited\n", readPullExternalTestFile(t, filepath.Join(env.dagsDir, "renamed")))
	status, err = env.svc.GetStatus(env.ctx)
	require.NoError(t, err)
	assert.NotContains(t, status.Items, "base")
	assert.Equal(t, gitsync.SyncItemKindFile, status.Items["renamed"].Kind)
	assert.Equal(t, gitsync.StatusSynced, status.Items["renamed"].Status)
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
	commitPullExternalTestFile(t, seedRepo, seedPath, "scripts/missing.sh", "echo missing\n", "missing script")
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

	missingPath := filepath.Join(dagsDir, "scripts", "missing.sh")
	require.NoError(t, os.Remove(missingPath))
	_, err = svc.GetStatus(ctx)
	require.NoError(t, err)

	secretPath := filepath.Join(root, "secret")
	require.NoError(t, os.WriteFile(secretPath, []byte("secret\n"), 0600))
	linkPath := filepath.Join(dagsDir, "scripts", "leak.sh")
	require.NoError(t, os.Symlink(secretPath, linkPath))

	err = svc.Move(ctx, "scripts/missing.sh", "scripts/leak.sh", "reject symlink", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
	assert.Equal(t, "echo missing\n", pullExternalFileContent(t, remoteRepo, "scripts/missing.sh"))
	assertPullExternalHeadFileMissing(t, remoteRepo, "scripts/leak.sh")
	secret, err := os.ReadFile(secretPath)
	require.NoError(t, err)
	assert.Equal(t, "secret\n", string(secret))

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

	existingPath := filepath.Join(dagsDir, "scripts", "existing.sh")
	require.NoError(t, os.WriteFile(existingPath, []byte("keep local\n"), 0600))
	_, err = svc.GetStatus(ctx)
	require.NoError(t, err)
	err = svc.Move(ctx, "scripts/run.sh", "scripts/existing.sh", "reject existing destination", false)
	require.Error(t, err)
	assert.Equal(t, "keep local\n", readPullExternalTestFile(t, existingPath))
	assert.Equal(t, "echo published\n", readPullExternalTestFile(t, localPath))
	assertPullExternalHeadFileMissing(t, remoteRepo, "scripts/existing.sh")

	require.NoError(t, svc.Move(ctx, "scripts/run.sh", "scripts/job.sh", "move script", false))
	assertPullExternalHeadFileMissing(t, remoteRepo, "scripts/run.sh")
	file = pullExternalHeadFile(t, remoteRepo, "scripts/job.sh")
	assert.Equal(t, filemode.Executable, file.Mode)

	require.NoError(t, svc.Delete(ctx, "scripts/job.sh", "delete script", false))
	assertPullExternalHeadFileMissing(t, remoteRepo, "scripts/job.sh")
	assert.NoFileExists(t, filepath.Join(dagsDir, "scripts", "job.sh"))
}

func TestSupportingFileExecutableModePreserved(t *testing.T) {
	t.Parallel()

	env := newPullExternalPushTest(t, []pullExternalTestFile{
		{path: "scripts/single.sh", content: "echo initial\n", executable: true},
		{path: "scripts/batch.sh", content: "echo initial\n", executable: true},
	})

	status, err := env.svc.GetStatus(env.ctx)
	require.NoError(t, err)
	assert.Equal(t, gitsync.StatusSynced, status.Items["scripts/single.sh"].Status)
	assert.Equal(t, gitsync.StatusSynced, status.Items["scripts/batch.sh"].Status)

	require.NoError(t, os.WriteFile(
		filepath.Join(env.dagsDir, "scripts", "single.sh"),
		[]byte("echo single\n"),
		0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(env.dagsDir, "scripts", "batch.sh"),
		[]byte("echo batch\n"),
		0600,
	))
	_, err = env.svc.GetStatus(env.ctx)
	require.NoError(t, err)

	_, err = env.svc.Publish(env.ctx, "scripts/single.sh", "publish single", false)
	require.NoError(t, err)
	_, err = env.svc.PublishAll(env.ctx, "publish batch", []string{"scripts/batch.sh"})
	require.NoError(t, err)

	assert.Equal(t, filemode.Executable, pullExternalHeadFile(t, env.remoteRepo, "scripts/single.sh").Mode)
	assert.Equal(t, filemode.Executable, pullExternalHeadFile(t, env.remoteRepo, "scripts/batch.sh").Mode)

	require.NoError(t, env.svc.Move(
		env.ctx,
		"scripts/single.sh",
		"scripts/moved.sh",
		"move executable",
		false,
	))
	assertPullExternalHeadFileMissing(t, env.remoteRepo, "scripts/single.sh")
	assert.Equal(t, filemode.Executable, pullExternalHeadFile(t, env.remoteRepo, "scripts/moved.sh").Mode)
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
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Chmod(localAsset, 0000))
		defer func() {
			_ = os.Chmod(localAsset, 0600)
		}()
	}

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

type pullExternalTestFile struct {
	path       string
	content    string
	executable bool
}

type pullExternalPushTest struct {
	ctx        context.Context
	remoteRepo *git.Repository
	seedRepo   *git.Repository
	seedPath   string
	dataDir    string
	dagsDir    string
	svc        gitsync.Service
	baseHead   plumbing.Hash
}

func newPullExternalPushTest(t *testing.T, files []pullExternalTestFile) *pullExternalPushTest {
	t.Helper()

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
	var baseHead plumbing.Hash
	for _, file := range files {
		if file.executable {
			baseHead = commitPullExternalExecutableFile(t, seedRepo, seedPath, file.path, file.content, "add "+file.path)
		} else {
			baseHead = commitPullExternalTestFile(t, seedRepo, seedPath, file.path, file.content, "add "+file.path)
		}
	}
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

	return &pullExternalPushTest{
		ctx:        ctx,
		remoteRepo: remoteRepo,
		seedRepo:   seedRepo,
		seedPath:   seedPath,
		dataDir:    dataDir,
		dagsDir:    dagsDir,
		svc:        svc,
		baseHead:   baseHead,
	}
}

func (e *pullExternalPushTest) advanceRemote(t *testing.T) {
	t.Helper()

	commitPullExternalTestFile(t, e.seedRepo, e.seedPath, "remote.txt", "advanced\n", "advance remote")
	require.NoError(t, e.seedRepo.Push(&git.PushOptions{
		RemoteName: "upstream",
		RefSpecs: []gitconfig.RefSpec{
			"refs/heads/main:refs/heads/main",
		},
	}))
}

func assertPullExternalCloneAt(t *testing.T, dataDir string, want plumbing.Hash) {
	t.Helper()

	repo, err := git.PlainOpen(filepath.Join(dataDir, "gitsync", "repo"))
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	assert.Equal(t, want, head.Hash())
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	status, err := worktree.Status()
	require.NoError(t, err)
	assert.True(t, status.IsClean(), status.String())
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
	idx, err := repo.Storer.Index()
	require.NoError(t, err)
	entry, err := idx.Entry(filepath.ToSlash(filePath))
	require.NoError(t, err)
	entry.Mode = filemode.Executable
	require.NoError(t, repo.Storer.SetIndex(idx))

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

func pullExternalFileContent(t *testing.T, repo *git.Repository, filePath string) string {
	t.Helper()

	content, err := pullExternalHeadFile(t, repo, filePath).Contents()
	require.NoError(t, err)
	return content
}

func readPullExternalTestFile(t *testing.T, filePath string) string {
	t.Helper()

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	return string(content)
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
