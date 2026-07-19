// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec030_git_worktree_action_test

import (
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestGitWorktreeAutomaticCleanupPolicies(t *testing.T) {
	t.Parallel()

	t.Run("never preserves an owned worktree", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		path := dagu.ProjectPath("wt/cleanup-never")

		result := startWithParams(
			dagu,
			"runtime_add_cleanup.yaml",
			"working_dir=./repo",
			"branch=cleanup-never",
			"path=../wt/cleanup-never",
			"cleanup=never",
		)
		result.ExpectExitCode(0)
		actual := readAddResult(t, dagu)
		require.True(t, actual.WorktreeCreated)
		require.True(t, actual.BranchCreated)
		require.DirExists(t, path)
		requireLinkedWorktree(t, repo.path, path, "cleanup-never", repo.baseCommit)
	})

	t.Run("on success removes an owned worktree and preserves its branch", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		path := dagu.ProjectPath("wt/cleanup-success")

		result := startWithParams(
			dagu,
			"runtime_add_cleanup.yaml",
			"working_dir=./repo",
			"branch=cleanup-success",
			"path=../wt/cleanup-success",
			"cleanup=on_success",
		)
		result.ExpectExitCode(0)
		actual := readAddResult(t, dagu)
		require.True(t, actual.WorktreeCreated)
		require.True(t, actual.BranchCreated)
		require.NoDirExists(t, path)
		require.True(t, refExists(t, repo.path, "refs/heads/cleanup-success"))
		requireNoLinkedWorktree(t, repo.path, path, "cleanup-success")
	})

	t.Run("on success preserves an owned worktree after failure", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		path := dagu.ProjectPath("wt/cleanup-failed")

		result := startWithParams(
			dagu,
			"runtime_add_cleanup_failure.yaml",
			"working_dir=./repo",
			"branch=cleanup-failed",
			"path=../wt/cleanup-failed",
			"cleanup=on_success",
		)
		result.ExpectNonZeroExitCode()
		require.DirExists(t, path)
		require.True(t, refExists(t, repo.path, "refs/heads/cleanup-failed"))
		requireLinkedWorktree(t, repo.path, path, "cleanup-failed", repo.baseCommit)
	})

	t.Run("on finish removes an owned worktree after success", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		path := dagu.ProjectPath("wt/cleanup-finish-success")

		result := startWithParams(
			dagu,
			"runtime_add_cleanup.yaml",
			"working_dir=./repo",
			"branch=cleanup-finish-success",
			"path=../wt/cleanup-finish-success",
			"cleanup=on_finish",
		)
		result.ExpectExitCode(0)
		actual := readAddResult(t, dagu)
		require.True(t, actual.WorktreeCreated)
		require.NoDirExists(t, path)
		require.True(t, refExists(t, repo.path, "refs/heads/cleanup-finish-success"))
		requireNoLinkedWorktree(t, repo.path, path, "cleanup-finish-success")
	})

	t.Run("on finish removes an owned worktree after failure", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		path := dagu.ProjectPath("wt/cleanup-finish-failed")

		result := startWithParams(
			dagu,
			"runtime_add_cleanup_failure.yaml",
			"working_dir=./repo",
			"branch=cleanup-finish-failed",
			"path=../wt/cleanup-finish-failed",
			"cleanup=on_finish",
		)
		result.ExpectNonZeroExitCode()
		require.NoDirExists(t, path)
		require.True(t, refExists(t, repo.path, "refs/heads/cleanup-finish-failed"))
		requireNoLinkedWorktree(t, repo.path, path, "cleanup-finish-failed")
	})
}

func TestGitWorktreeAutomaticCleanupDoesNotOwnReusedWorktree(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	path := dagu.ProjectPath("wt/reused-cleanup")
	createLinkedWorktree(t, repo.path, path, "reused-cleanup", repo.baseCommit)

	result := startWithParams(
		dagu,
		"runtime_add_cleanup.yaml",
		"working_dir=./repo",
		"branch=reused-cleanup",
		"path=../wt/reused-cleanup",
		"cleanup=on_finish",
	)
	result.ExpectExitCode(0)
	actual := readAddResult(t, dagu)
	require.False(t, actual.WorktreeCreated)
	require.False(t, actual.BranchCreated)
	require.DirExists(t, path)
	requireLinkedWorktree(t, repo.path, path, "reused-cleanup", repo.baseCommit)
}

func TestGitWorktreeAutomaticCleanupRunsAfterLifecycleHandlers(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	path := dagu.ProjectPath("wt/cleanup-handler")

	result := dagu.Run("start", "runtime_add_cleanup_handler.yaml")
	result.ExpectExitCode(0)
	actual := readAddResult(t, dagu)
	require.True(t, actual.WorktreeCreated)
	dagu.ExpectFileContains("handler-saw-worktree.txt", "worktree visible to exit handler")
	require.NoDirExists(t, path)
	require.True(t, refExists(t, repo.path, "refs/heads/cleanup-handler"))
	requireNoLinkedWorktree(t, repo.path, path, "cleanup-handler")
}

func TestGitWorktreeAutomaticCleanupRefusesDirtyWorktree(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	path := dagu.ProjectPath("wt/dirty-cleanup")

	result := startWithParams(
		dagu,
		"runtime_add_cleanup_dirty.yaml",
		"working_dir=./repo",
		"branch=dirty-cleanup",
		"path=../wt/dirty-cleanup",
		"cleanup=on_success",
	)
	result.ExpectNonZeroExitCode()
	require.FileExists(t, dagu.ProjectPath("wt/dirty-cleanup/untracked.txt"))
	require.True(t, refExists(t, repo.path, "refs/heads/dirty-cleanup"))
	requireLinkedWorktree(t, repo.path, path, "dirty-cleanup", repo.baseCommit)
}

func TestGitWorktreeAutomaticCleanupRejectsResolvedUnknownPolicy(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	path := dagu.ProjectPath("wt/invalid-cleanup")
	requireValidWorkflow(dagu, "runtime_add_cleanup.yaml")

	result := startWithParams(
		dagu,
		"runtime_add_cleanup.yaml",
		"working_dir=./repo",
		"branch=invalid-cleanup",
		"path=../wt/invalid-cleanup",
		"cleanup=sometimes",
	)
	result.ExpectNonZeroExitCode()
	require.False(t, refExists(t, repo.path, "refs/heads/invalid-cleanup"))
	requireNoLinkedWorktree(t, repo.path, path, "invalid-cleanup")
}
