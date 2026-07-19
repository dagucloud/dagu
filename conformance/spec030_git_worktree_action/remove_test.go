// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec030_git_worktree_action_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestGitWorktreeRemoveSelectorsResolveParameterInputs(t *testing.T) {
	t.Parallel()

	t.Run("branch", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		gitRun(t, repo.path, "config", "remote.origin.url", "forbidden://must-not-contact")
		path := dagu.ProjectPath("wt/by-branch")
		createLinkedWorktree(t, repo.path, path, "by-branch", repo.baseCommit)

		result := startWithParams(dagu, "runtime_remove_branch.yaml", "working_dir=./repo", "branch=by-branch")
		result.ExpectExitCode(0)
		actual := readRemoveResult(t, dagu)
		require.Equal(t, path, actual.Path)
		require.Equal(t, "by-branch", actual.Branch)
		require.True(t, actual.WorktreeRemoved)
		require.False(t, actual.BranchDeleted)
		require.NoDirExists(t, path)
		require.True(t, refExists(t, repo.path, "refs/heads/by-branch"))
		requireNoLinkedWorktree(t, repo.path, path, "by-branch")
	})

	t.Run("path", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		path := dagu.ProjectPath("wt/by-path")
		createLinkedWorktree(t, repo.path, path, "by-path", repo.baseCommit)

		result := startWithParams(dagu, "runtime_remove_path.yaml", "working_dir=./repo", "path=../wt/by-path")
		result.ExpectExitCode(0)
		actual := readRemoveResult(t, dagu)
		require.Equal(t, path, actual.Path)
		require.Equal(t, "by-path", actual.Branch)
		require.True(t, actual.WorktreeRemoved)
		require.False(t, actual.BranchDeleted)
		require.NoDirExists(t, path)
		require.True(t, refExists(t, repo.path, "refs/heads/by-path"))
		requireNoLinkedWorktree(t, repo.path, path, "by-path")
	})

	t.Run("matching branch and path", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		path := dagu.ProjectPath("wt/by-both")
		createLinkedWorktree(t, repo.path, path, "by-both", repo.baseCommit)

		params := []string{"working_dir=./repo", "branch=by-both", "path=../wt/by-both"}
		result := startWithParams(dagu, "runtime_remove_both.yaml", params...)
		result.ExpectExitCode(0)
		actual := readRemoveResult(t, dagu)
		require.Equal(t, path, actual.Path)
		require.Equal(t, "by-both", actual.Branch)
		require.True(t, actual.WorktreeRemoved)
		require.False(t, actual.BranchDeleted)

		resetActionFiles(t, dagu)
		repeated := startWithParams(dagu, "runtime_remove_both.yaml", params...)
		repeated.ExpectExitCode(0)
		repeatedResult := readRemoveResult(t, dagu)
		require.Equal(t, path, repeatedResult.Path)
		require.Equal(t, "by-both", repeatedResult.Branch)
		require.False(t, repeatedResult.WorktreeRemoved)
		require.False(t, repeatedResult.BranchDeleted)
		require.True(t, refExists(t, repo.path, "refs/heads/by-both"))
		requireNoLinkedWorktree(t, repo.path, path, "by-both")
	})
}

func TestGitWorktreeRemoveMissingTargetsIsIdempotent(t *testing.T) {
	t.Parallel()

	t.Run("missing branch has empty result path", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		_ = initRepository(t, dagu)
		result := startWithParams(dagu, "runtime_remove_branch.yaml", "working_dir=./repo", "branch=missing")
		result.ExpectExitCode(0)
		actual := readRemoveResult(t, dagu)
		require.Empty(t, actual.Path)
		require.Equal(t, "missing", actual.Branch)
		require.False(t, actual.WorktreeRemoved)
		require.False(t, actual.BranchDeleted)
	})

	t.Run("missing explicit path is returned", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		_ = initRepository(t, dagu)
		result := startWithParams(dagu, "runtime_remove_path.yaml", "working_dir=./repo", "path=../wt/missing")
		result.ExpectExitCode(0)
		actual := readRemoveResult(t, dagu)
		require.Equal(t, dagu.ProjectPath("wt/missing"), actual.Path)
		require.Empty(t, actual.Branch)
		require.False(t, actual.WorktreeRemoved)
		require.False(t, actual.BranchDeleted)
	})

	t.Run("unregistered directory is preserved", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		_ = initRepository(t, dagu)
		dagu.WriteFile("wt/unregistered/keep.txt", "keep\n")
		result := startWithParams(dagu, "runtime_remove_path.yaml", "working_dir=./repo", "path=../wt/unregistered")
		result.ExpectExitCode(0)
		actual := readRemoveResult(t, dagu)
		require.Equal(t, dagu.ProjectPath("wt/unregistered"), actual.Path)
		require.False(t, actual.WorktreeRemoved)
		dagu.ExpectFileContent("wt/unregistered/keep.txt", "keep\n")
	})

	t.Run("primary branch without deletion is not a linked target", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		result := startWithParams(dagu, "runtime_remove_branch.yaml", "working_dir=./repo", "branch=main")
		result.ExpectExitCode(0)
		actual := readRemoveResult(t, dagu)
		require.Empty(t, actual.Path)
		require.Equal(t, "main", actual.Branch)
		require.False(t, actual.WorktreeRemoved)
		require.False(t, actual.BranchDeleted)
		require.DirExists(t, repo.path)
		require.True(t, refExists(t, repo.path, "refs/heads/main"))
	})
}

func TestGitWorktreeRemoveDirtyRequiresForce(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	path := dagu.ProjectPath("wt/dirty")
	createLinkedWorktree(t, repo.path, path, "dirty", repo.baseCommit)
	require.NoError(t, os.WriteFile(filepath.Join(path, "base.txt"), []byte("modified\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(path, "untracked.txt"), []byte("untracked\n"), 0o644))
	requireValidWorkflow(dagu, "runtime_remove_branch.yaml")

	refused := startWithParams(dagu, "runtime_remove_branch.yaml", "working_dir=./repo", "branch=dirty")
	refused.ExpectNonZeroExitCode()
	refused.ExpectStdout("")
	refused.ExpectStderrNotEmpty()
	requireNoPublishedOutputs(t, dagu)
	require.DirExists(t, path)
	require.Equal(t, "modified\n", readFile(t, filepath.Join(path, "base.txt")))
	require.Equal(t, "untracked\n", readFile(t, filepath.Join(path, "untracked.txt")))
	requireLinkedWorktree(t, repo.path, path, "dirty", repo.baseCommit)

	resetActionFiles(t, dagu)
	forced := startWithParams(dagu, "runtime_remove_force.yaml", "working_dir=./repo", "branch=dirty")
	forced.ExpectExitCode(0)
	actual := readRemoveResult(t, dagu)
	require.True(t, actual.WorktreeRemoved)
	require.False(t, actual.BranchDeleted)
	require.NoDirExists(t, path)
	require.True(t, refExists(t, repo.path, "refs/heads/dirty"))
	requireNoLinkedWorktree(t, repo.path, path, "dirty")
}

func TestGitWorktreeRemoveUnregistersStaleTarget(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	path := dagu.ProjectPath("wt/stale")
	createLinkedWorktree(t, repo.path, path, "stale", repo.baseCommit)
	require.NoError(t, os.Rename(path, path+".moved"))
	requireLinkedWorktree(t, repo.path, path, "stale", repo.baseCommit)

	result := startWithParams(dagu, "runtime_remove_branch.yaml", "working_dir=./repo", "branch=stale")
	result.ExpectExitCode(0)
	actual := readRemoveResult(t, dagu)
	require.Equal(t, path, actual.Path)
	require.Equal(t, "stale", actual.Branch)
	require.True(t, actual.WorktreeRemoved)
	require.False(t, actual.BranchDeleted)
	require.True(t, refExists(t, repo.path, "refs/heads/stale"))
	require.DirExists(t, path+".moved")
	requireNoLinkedWorktree(t, repo.path, path, "stale")
}

func TestGitWorktreeRemoveDeletesBranches(t *testing.T) {
	t.Parallel()

	t.Run("merged branch", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		path := dagu.ProjectPath("wt/merged")
		createLinkedWorktree(t, repo.path, path, "merged", repo.baseCommit)

		result := startWithParams(dagu, "runtime_remove_delete.yaml", "working_dir=./repo", "branch=merged")
		result.ExpectExitCode(0)
		actual := readRemoveResult(t, dagu)
		require.Equal(t, "merged", actual.Branch)
		require.True(t, actual.WorktreeRemoved)
		require.True(t, actual.BranchDeleted)
		require.NoDirExists(t, path)
		require.False(t, refExists(t, repo.path, "refs/heads/merged"))
		requireNoLinkedWorktree(t, repo.path, path, "merged")
	})

	t.Run("unmerged branch is refused before worktree removal", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		path := dagu.ProjectPath("wt/unmerged")
		createLinkedWorktree(t, repo.path, path, "unmerged", repo.baseCommit)
		branchCommit := commitFile(t, path, "branch-only.txt", "branch\n", "unmerged change")
		requireValidWorkflow(dagu, "runtime_remove_delete.yaml")

		result := startWithParams(dagu, "runtime_remove_delete.yaml", "working_dir=./repo", "branch=unmerged")
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoPublishedOutputs(t, dagu)
		require.DirExists(t, path)
		require.True(t, refExists(t, repo.path, "refs/heads/unmerged"))
		requireLinkedWorktree(t, repo.path, path, "unmerged", branchCommit)
	})

	t.Run("forced deletion permits unmerged branch", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		path := dagu.ProjectPath("wt/forced-unmerged")
		createLinkedWorktree(t, repo.path, path, "forced-unmerged", repo.baseCommit)
		_ = commitFile(t, path, "branch-only.txt", "branch\n", "unmerged change")

		result := startWithParams(dagu, "runtime_remove_force_delete.yaml", "working_dir=./repo", "branch=forced-unmerged")
		result.ExpectExitCode(0)
		actual := readRemoveResult(t, dagu)
		require.Equal(t, "forced-unmerged", actual.Branch)
		require.True(t, actual.WorktreeRemoved)
		require.True(t, actual.BranchDeleted)
		require.NoDirExists(t, path)
		require.False(t, refExists(t, repo.path, "refs/heads/forced-unmerged"))
		requireNoLinkedWorktree(t, repo.path, path, "forced-unmerged")
	})

	t.Run("branch without registered worktree", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		gitRun(t, repo.path, "branch", "disposable", repo.baseCommit)

		result := startWithParams(dagu, "runtime_remove_delete.yaml", "working_dir=./repo", "branch=disposable")
		result.ExpectExitCode(0)
		actual := readRemoveResult(t, dagu)
		require.Empty(t, actual.Path)
		require.False(t, actual.WorktreeRemoved)
		require.True(t, actual.BranchDeleted)
		require.False(t, refExists(t, repo.path, "refs/heads/disposable"))
	})

	t.Run("missing branch remains idempotent", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		_ = initRepository(t, dagu)
		result := startWithParams(dagu, "runtime_remove_delete.yaml", "working_dir=./repo", "branch=missing")
		result.ExpectExitCode(0)
		actual := readRemoveResult(t, dagu)
		require.False(t, actual.WorktreeRemoved)
		require.False(t, actual.BranchDeleted)
	})

}

func TestGitWorktreeRemoveSupportsBareRepository(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	barePath := dagu.ProjectPath("bare.git")
	cloneBare(t, repo.path, barePath)
	path := dagu.ProjectPath("wt/bare-main")
	gitRun(t, barePath, "worktree", "add", path, "main")

	result := startWithParams(dagu, "runtime_remove_branch.yaml", "working_dir=./bare.git", "branch=main")
	result.ExpectExitCode(0)
	actual := readRemoveResult(t, dagu)
	require.Equal(t, path, actual.Path)
	require.True(t, actual.WorktreeRemoved)
	require.False(t, actual.BranchDeleted)
	require.NoDirExists(t, path)
	require.True(t, refExists(t, barePath, "refs/heads/main"))
	requireNoLinkedWorktree(t, barePath, path, "main")
}

func TestGitWorktreeRemoveRuntimeErrors(t *testing.T) {
	t.Parallel()

	t.Run("working directory does not exist", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		requireValidWorkflow(dagu, "runtime_remove_branch.yaml")
		result := startWithParams(dagu, "runtime_remove_branch.yaml", "working_dir=./missing", "branch=topic")
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoPublishedOutputs(t, dagu)
	})

	t.Run("primary working tree path", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		requireValidWorkflow(dagu, "runtime_remove_path.yaml")
		result := startWithParams(dagu, "runtime_remove_path.yaml", "working_dir=./repo", "path=.")
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoPublishedOutputs(t, dagu)
		require.DirExists(t, repo.path)
		require.True(t, refExists(t, repo.path, "refs/heads/main"))
	})

	t.Run("branch and path identify different worktrees", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		alphaPath := dagu.ProjectPath("wt/alpha")
		betaPath := dagu.ProjectPath("wt/beta")
		createLinkedWorktree(t, repo.path, alphaPath, "alpha", repo.baseCommit)
		createLinkedWorktree(t, repo.path, betaPath, "beta", repo.baseCommit)
		requireValidWorkflow(dagu, "runtime_remove_both.yaml")

		result := startWithParams(
			dagu,
			"runtime_remove_both.yaml",
			"working_dir=./repo",
			"branch=alpha",
			"path=../wt/beta",
		)
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoPublishedOutputs(t, dagu)
		requireLinkedWorktree(t, repo.path, alphaPath, "alpha", repo.baseCommit)
		requireLinkedWorktree(t, repo.path, betaPath, "beta", repo.baseCommit)
	})

	t.Run("checked-out primary branch cannot be deleted", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		requireValidWorkflow(dagu, "runtime_remove_delete.yaml")
		result := startWithParams(dagu, "runtime_remove_delete.yaml", "working_dir=./repo", "branch=main")
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoPublishedOutputs(t, dagu)
		require.DirExists(t, repo.path)
		require.True(t, refExists(t, repo.path, "refs/heads/main"))
	})
}
