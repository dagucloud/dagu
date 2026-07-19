// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec030_git_worktree_action_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestGitWorktreeAddGeneratesBranchWhenOmitted(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)

	first := startWithParams(dagu, "runtime_add_generated.yaml", "working_dir=./repo")
	first.ExpectExitCode(0)
	dagu.ExpectFileContent(actionStderrFile, "")

	firstResult := readAddResult(t, dagu)
	require.True(t, strings.HasPrefix(firstResult.Branch, "dagu/"))
	gitRun(t, repo.path, "check-ref-format", "--branch", firstResult.Branch)
	firstPath := filepath.Join(repo.path+".worktrees", filepath.FromSlash(firstResult.Branch))
	require.Equal(t, firstPath, firstResult.Path)
	require.Equal(t, repo.baseCommit, firstResult.Commit)
	require.True(t, firstResult.WorktreeCreated)
	require.True(t, firstResult.BranchCreated)
	requireLinkedWorktree(t, repo.path, firstPath, firstResult.Branch, repo.baseCommit)

	resetActionStreams(t, dagu)
	second := startWithParams(dagu, "runtime_add_generated.yaml", "working_dir=./repo")
	second.ExpectExitCode(0)
	secondResult := readAddResult(t, dagu)
	secondPath := filepath.Join(repo.path+".worktrees", filepath.FromSlash(secondResult.Branch))
	require.True(t, strings.HasPrefix(secondResult.Branch, "dagu/"))
	require.NotEqual(t, firstResult.Branch, secondResult.Branch)
	require.Equal(t, secondPath, secondResult.Path)
	requireLinkedWorktree(t, repo.path, secondPath, secondResult.Branch, repo.baseCommit)
}

func TestGitWorktreeAddGeneratedBranchResolvesBase(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	commitFile(t, repo.path, "second.txt", "second\n", "second")

	result := startWithParams(
		dagu,
		"runtime_add_generated_base.yaml",
		"working_dir=./repo",
		"base="+repo.baseCommit,
	)
	result.ExpectExitCode(0)
	actual := readAddResult(t, dagu)
	require.True(t, strings.HasPrefix(actual.Branch, "dagu/"))
	require.Equal(t, repo.baseCommit, actual.Commit)
	require.True(t, actual.WorktreeCreated)
	require.True(t, actual.BranchCreated)
	requireLinkedWorktree(t, repo.path, actual.Path, actual.Branch, repo.baseCommit)
}

func TestGitWorktreeAddResolvesParameterInputsAndPaths(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	gitRun(t, repo.path, "config", "remote.origin.url", "forbidden://must-not-contact")

	result := startWithParams(
		dagu,
		"runtime_add.yaml",
		"working_dir=./repo",
		"branch=feature-x",
		"path=../wt/../wt/feature-x",
	)
	result.ExpectExitCode(0)
	dagu.ExpectFileContent(actionStderrFile, "")

	wantPath := dagu.ProjectPath("wt/feature-x")
	actual := readAddResult(t, dagu)
	require.Equal(t, wantPath, actual.Path)
	require.Equal(t, "feature-x", actual.Branch)
	require.Equal(t, repo.baseCommit, actual.Commit)
	require.True(t, actual.WorktreeCreated)
	require.True(t, actual.BranchCreated)
	require.FileExists(t, filepath.Join(wantPath, "base.txt"))
	require.Equal(t, "feature-x", gitOutput(t, wantPath, "symbolic-ref", "--short", "HEAD"))
	require.Equal(t, repo.baseCommit, gitOutput(t, repo.path, "rev-parse", "refs/heads/feature-x"))
	requireLinkedWorktree(t, repo.path, wantPath, "feature-x", repo.baseCommit)
}

func TestGitWorktreeAddUsesDefaultPath(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	result := startWithParams(
		dagu,
		"runtime_add_default.yaml",
		"working_dir=./repo",
		"branch=feature/auth",
	)
	result.ExpectExitCode(0)

	wantPath := dagu.ProjectPath("repo.worktrees/feature/auth")
	actual := readAddResult(t, dagu)
	require.Equal(t, wantPath, actual.Path)
	require.Equal(t, "feature/auth", actual.Branch)
	require.Equal(t, repo.baseCommit, actual.Commit)
	requireLinkedWorktree(t, repo.path, wantPath, "feature/auth", repo.baseCommit)
}

func TestGitWorktreeAddDefaultPathsDoNotCollide(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)

	nested := startWithParams(
		dagu,
		"runtime_add_default.yaml",
		"working_dir=./repo",
		"branch=feature/auth",
	)
	nested.ExpectExitCode(0)
	nestedResult := readAddResult(t, dagu)

	resetActionStreams(t, dagu)
	flat := startWithParams(
		dagu,
		"runtime_add_default.yaml",
		"working_dir=./repo",
		"branch=feature-auth",
	)
	flat.ExpectExitCode(0)
	flatResult := readAddResult(t, dagu)

	require.Equal(t, dagu.ProjectPath("repo.worktrees/feature/auth"), nestedResult.Path)
	require.Equal(t, dagu.ProjectPath("repo.worktrees/feature-auth"), flatResult.Path)
	require.NotEqual(t, nestedResult.Path, flatResult.Path)
	requireLinkedWorktree(t, repo.path, nestedResult.Path, "feature/auth", repo.baseCommit)
	requireLinkedWorktree(t, repo.path, flatResult.Path, "feature-auth", repo.baseCommit)
}

func TestGitWorktreePathsResolveFromRepositoryRoot(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	dagu.Mkdir("workspace")
	add := dagu.Run("start", "runtime_add_step_working_dir.yaml")
	add.ExpectExitCode(0)

	worktreePath := dagu.ProjectPath("workspace/wt/step-directory")
	addResult := readAddResult(t, dagu)
	require.Equal(t, worktreePath, addResult.Path)
	requireLinkedWorktree(t, repo.path, worktreePath, "step-directory", repo.baseCommit)

	resetActionStreams(t, dagu)
	remove := dagu.Run("start", "runtime_remove_step_working_dir.yaml")
	remove.ExpectExitCode(0)
	removeResult := readRemoveResult(t, dagu)
	require.Equal(t, worktreePath, removeResult.Path)
	require.True(t, removeResult.WorktreeRemoved)
	require.NoDirExists(t, worktreePath)
}

func TestGitWorktreeActionsDetectRepositoryFromSubdirectory(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	dagu.Mkdir("repo/services/api")

	add := dagu.Run("start", "runtime_add_subdirectory.yaml")
	add.ExpectExitCode(0)
	worktreePath := dagu.ProjectPath("wt/from-subdirectory")
	addResult := readAddResult(t, dagu)
	require.Equal(t, worktreePath, addResult.Path)
	require.True(t, addResult.WorktreeCreated)
	requireLinkedWorktree(t, repo.path, worktreePath, "from-subdirectory", repo.baseCommit)

	resetActionStreams(t, dagu)
	remove := dagu.Run("start", "runtime_remove_subdirectory.yaml")
	remove.ExpectExitCode(0)
	removeResult := readRemoveResult(t, dagu)
	require.Equal(t, worktreePath, removeResult.Path)
	require.Equal(t, "from-subdirectory", removeResult.Branch)
	require.True(t, removeResult.WorktreeRemoved)
	require.NoDirExists(t, worktreePath)
}

func TestGitWorktreeActionsDetectRepositoryFromLinkedWorktree(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	linkedPath := dagu.ProjectPath("linked")
	createLinkedWorktree(t, repo.path, linkedPath, "linked-base", repo.baseCommit)

	add := startWithParams(
		dagu,
		"runtime_add.yaml",
		"working_dir=./linked",
		"branch=from-linked",
		"path=../wt/from-linked",
	)
	add.ExpectExitCode(0)
	worktreePath := dagu.ProjectPath("wt/from-linked")
	addResult := readAddResult(t, dagu)
	require.Equal(t, worktreePath, addResult.Path)
	requireLinkedWorktree(t, repo.path, worktreePath, "from-linked", repo.baseCommit)

	resetActionStreams(t, dagu)
	remove := startWithParams(
		dagu,
		"runtime_remove_branch.yaml",
		"working_dir=./linked",
		"branch=from-linked",
	)
	remove.ExpectExitCode(0)
	removeResult := readRemoveResult(t, dagu)
	require.Equal(t, worktreePath, removeResult.Path)
	require.True(t, removeResult.WorktreeRemoved)
	requireNoLinkedWorktree(t, repo.path, worktreePath, "from-linked")
}

func TestGitWorktreeAddResolvesBaseOrder(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		base      func(repositoryFixture, string) string
		want      func(repositoryFixture, string) string
		newBranch string
	}{
		{
			name:      "commit hash",
			base:      func(repo repositoryFixture, _ string) string { return repo.baseCommit },
			want:      func(repo repositoryFixture, _ string) string { return repo.baseCommit },
			newBranch: "base-hash",
		},
		{
			name:      "local branch",
			base:      func(_ repositoryFixture, _ string) string { return "local-start" },
			want:      func(repo repositoryFixture, _ string) string { return repo.baseCommit },
			newBranch: "base-local",
		},
		{
			name:      "origin tracking branch",
			base:      func(_ repositoryFixture, _ string) string { return "remote-start" },
			want:      func(repo repositoryFixture, _ string) string { return repo.baseCommit },
			newBranch: "base-remote",
		},
		{
			name:      "tag",
			base:      func(_ repositoryFixture, _ string) string { return "tag-start" },
			want:      func(repo repositoryFixture, _ string) string { return repo.baseCommit },
			newBranch: "base-tag",
		},
		{
			name:      "annotated tag",
			base:      func(_ repositoryFixture, _ string) string { return "annotated-start" },
			want:      func(repo repositoryFixture, _ string) string { return repo.baseCommit },
			newBranch: "base-annotated-tag",
		},
		{
			name:      "local branch precedes origin and tag",
			base:      func(_ repositoryFixture, _ string) string { return "priority" },
			want:      func(repo repositoryFixture, _ string) string { return repo.baseCommit },
			newBranch: "base-local-priority",
		},
		{
			name:      "origin precedes tag",
			base:      func(_ repositoryFixture, _ string) string { return "remote-priority" },
			want:      func(repo repositoryFixture, _ string) string { return repo.baseCommit },
			newBranch: "base-remote-priority",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			repo := initRepository(t, dagu)
			secondCommit := commitFile(t, repo.path, "second.txt", "second\n", "second")
			gitRun(t, repo.path, "branch", "local-start", repo.baseCommit)
			gitRun(t, repo.path, "update-ref", "refs/remotes/origin/remote-start", repo.baseCommit)
			gitRun(t, repo.path, "tag", "tag-start", repo.baseCommit)
			gitRun(t, repo.path, "tag", "-a", "annotated-start", "-m", "annotated start", repo.baseCommit)
			gitRun(t, repo.path, "branch", "priority", repo.baseCommit)
			gitRun(t, repo.path, "update-ref", "refs/remotes/origin/priority", secondCommit)
			gitRun(t, repo.path, "tag", "priority", secondCommit)
			gitRun(t, repo.path, "update-ref", "refs/remotes/origin/remote-priority", repo.baseCommit)
			gitRun(t, repo.path, "tag", "remote-priority", secondCommit)
			gitRun(t, repo.path, "config", "remote.origin.url", "forbidden://must-not-contact")

			pathName := "wt/" + tc.newBranch
			result := startWithParams(
				dagu,
				"runtime_add_base.yaml",
				"working_dir=./repo",
				"branch="+tc.newBranch,
				"path=../"+pathName,
				"base="+tc.base(repo, secondCommit),
			)
			result.ExpectExitCode(0)

			wantCommit := tc.want(repo, secondCommit)
			actual := readAddResult(t, dagu)
			require.Equal(t, wantCommit, actual.Commit)
			require.True(t, actual.WorktreeCreated)
			require.True(t, actual.BranchCreated)
			require.Equal(t, wantCommit, gitOutput(t, repo.path, "rev-parse", "refs/heads/"+tc.newBranch))
			requireLinkedWorktree(t, repo.path, dagu.ProjectPath(pathName), tc.newBranch, wantCommit)
		})
	}
}

func TestGitWorktreeAddExistingBranchIgnoresBase(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	gitRun(t, repo.path, "branch", "existing", repo.baseCommit)
	_ = commitFile(t, repo.path, "main-only.txt", "main\n", "advance main")

	result := startWithParams(
		dagu,
		"runtime_add_base.yaml",
		"working_dir=./repo",
		"branch=existing",
		"path=../wt/existing",
		"base=does-not-exist",
	)
	result.ExpectExitCode(0)

	actual := readAddResult(t, dagu)
	require.Equal(t, repo.baseCommit, actual.Commit)
	require.True(t, actual.WorktreeCreated)
	require.False(t, actual.BranchCreated)
	require.Equal(t, repo.baseCommit, gitOutput(t, repo.path, "rev-parse", "refs/heads/existing"))
}

func TestGitWorktreeAddReusesWorktreeWithoutChangingIt(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	params := []string{"working_dir=./repo", "branch=reused", "path=../wt/reused"}
	first := startWithParams(dagu, "runtime_add.yaml", params...)
	first.ExpectExitCode(0)
	firstResult := readAddResult(t, dagu)
	require.True(t, firstResult.WorktreeCreated)
	require.True(t, firstResult.BranchCreated)

	worktreePath := dagu.ProjectPath("wt/reused")
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, "base.txt"), []byte("modified\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, "untracked.txt"), []byte("keep\n"), 0o644))
	headBefore := gitOutput(t, worktreePath, "rev-parse", "HEAD")
	resetActionStreams(t, dagu)

	second := startWithParams(dagu, "runtime_add.yaml", params...)
	second.ExpectExitCode(0)
	secondResult := readAddResult(t, dagu)
	require.False(t, secondResult.WorktreeCreated)
	require.False(t, secondResult.BranchCreated)
	require.Equal(t, headBefore, secondResult.Commit)
	require.Equal(t, headBefore, gitOutput(t, worktreePath, "rev-parse", "HEAD"))
	require.Equal(t, "modified\n", readFile(t, filepath.Join(worktreePath, "base.txt")))
	require.Equal(t, "keep\n", readFile(t, filepath.Join(worktreePath, "untracked.txt")))
	require.NotEmpty(t, gitOutput(t, worktreePath, "status", "--porcelain"))
	requireLinkedWorktree(t, repo.path, worktreePath, "reused", headBefore)
}

func TestGitWorktreeAddAcceptsExistingEmptyDirectory(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	dagu.Mkdir("wt/empty")
	result := startWithParams(
		dagu,
		"runtime_add.yaml",
		"working_dir=./repo",
		"branch=empty-target",
		"path=../wt/empty",
	)
	result.ExpectExitCode(0)
	actual := readAddResult(t, dagu)
	require.True(t, actual.WorktreeCreated)
	requireLinkedWorktree(t, repo.path, dagu.ProjectPath("wt/empty"), "empty-target", repo.baseCommit)
}

func TestGitWorktreeAddSupportsBareRepository(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	repo := initRepository(t, dagu)
	barePath := dagu.ProjectPath("bare.git")
	cloneBare(t, repo.path, barePath)

	result := startWithParams(
		dagu,
		"runtime_add_existing.yaml",
		"working_dir=./bare.git",
		"branch=main",
		"path=../wt/bare-main",
	)
	result.ExpectExitCode(0)
	actual := readAddResult(t, dagu)
	require.Equal(t, repo.baseCommit, actual.Commit)
	require.True(t, actual.WorktreeCreated)
	require.False(t, actual.BranchCreated)
	requireLinkedWorktree(t, barePath, dagu.ProjectPath("wt/bare-main"), "main", repo.baseCommit)
}

func TestGitWorktreeAddRuntimeErrors(t *testing.T) {
	t.Parallel()

	t.Run("working directory does not exist", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		requireValidWorkflow(dagu, "runtime_add.yaml")
		result := startWithParams(
			dagu,
			"runtime_add.yaml",
			"working_dir=./missing",
			"branch=topic",
			"path=../wt/topic",
		)
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoResultDocument(t, dagu)
	})

	t.Run("working directory is not a repository", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		dagu.Mkdir("not-repo")
		requireValidWorkflow(dagu, "runtime_add.yaml")
		result := startWithParams(
			dagu,
			"runtime_add.yaml",
			"working_dir=./not-repo",
			"branch=topic",
			"path=../wt/topic",
		)
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoResultDocument(t, dagu)
	})

	t.Run("base cannot be resolved", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		requireValidWorkflow(dagu, "runtime_add_base.yaml")
		result := startWithParams(
			dagu,
			"runtime_add_base.yaml",
			"working_dir=./repo",
			"branch=topic",
			"path=../wt/topic",
			"base=does-not-exist",
		)
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoResultDocument(t, dagu)
		require.False(t, refExists(t, repo.path, "refs/heads/topic"))
		requireNoLinkedWorktree(t, repo.path, dagu.ProjectPath("wt/topic"), "topic")
	})

	t.Run("missing explicit branch requires creation permission", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		requireValidWorkflow(dagu, "runtime_add_existing.yaml")
		result := startWithParams(
			dagu,
			"runtime_add_existing.yaml",
			"working_dir=./repo",
			"branch=missing",
			"path=../wt/missing",
		)
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoResultDocument(t, dagu)
		require.False(t, refExists(t, repo.path, "refs/heads/missing"))
		requireNoLinkedWorktree(t, repo.path, dagu.ProjectPath("wt/missing"), "missing")
	})

	t.Run("invalid branch name", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		requireValidWorkflow(dagu, "runtime_add.yaml")
		result := startWithParams(
			dagu,
			"runtime_add.yaml",
			"working_dir=./repo",
			"branch=invalid..branch",
			"path=../wt/invalid",
		)
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoResultDocument(t, dagu)
		requireNoLinkedWorktree(t, repo.path, dagu.ProjectPath("wt/invalid"), "")
	})

	t.Run("non-empty path is occupied", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		dagu.WriteFile("wt/occupied/keep.txt", "keep\n")
		requireValidWorkflow(dagu, "runtime_add.yaml")
		result := startWithParams(
			dagu,
			"runtime_add.yaml",
			"working_dir=./repo",
			"branch=occupied",
			"path=../wt/occupied",
		)
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoResultDocument(t, dagu)
		dagu.ExpectFileContent("wt/occupied/keep.txt", "keep\n")
		require.False(t, refExists(t, repo.path, "refs/heads/occupied"))
		requireNoLinkedWorktree(t, repo.path, dagu.ProjectPath("wt/occupied"), "occupied")
	})

	t.Run("branch is checked out in the primary worktree", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		requireValidWorkflow(dagu, "runtime_add_existing.yaml")
		result := startWithParams(
			dagu,
			"runtime_add_existing.yaml",
			"working_dir=./repo",
			"branch=main",
			"path=../wt/main",
		)
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoResultDocument(t, dagu)
		requireNoLinkedWorktree(t, repo.path, dagu.ProjectPath("wt/main"), "")
	})

	t.Run("branch is linked at a different path", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		otherPath := dagu.ProjectPath("wt/elsewhere")
		createLinkedWorktree(t, repo.path, otherPath, "linked", repo.baseCommit)
		requireValidWorkflow(dagu, "runtime_add_existing.yaml")
		result := startWithParams(
			dagu,
			"runtime_add_existing.yaml",
			"working_dir=./repo",
			"branch=linked",
			"path=../wt/requested",
		)
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoResultDocument(t, dagu)
		requireLinkedWorktree(t, repo.path, otherPath, "linked", repo.baseCommit)
		requireNoLinkedWorktree(t, repo.path, dagu.ProjectPath("wt/requested"), "")
	})

	t.Run("registration is stale", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		path := dagu.ProjectPath("wt/stale")
		createLinkedWorktree(t, repo.path, path, "stale", repo.baseCommit)
		require.NoError(t, os.Rename(path, path+".moved"))
		requireLinkedWorktree(t, repo.path, path, "stale", repo.baseCommit)
		requireValidWorkflow(dagu, "runtime_add_existing.yaml")

		result := startWithParams(
			dagu,
			"runtime_add_existing.yaml",
			"working_dir=./repo",
			"branch=stale",
			"path=../wt/stale",
		)
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoResultDocument(t, dagu)
		require.NoDirExists(t, path)
		require.DirExists(t, path+".moved")
		require.True(t, refExists(t, repo.path, "refs/heads/stale"))
	})

	t.Run("path belongs to a different branch", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		repo := initRepository(t, dagu)
		path := dagu.ProjectPath("wt/shared")
		createLinkedWorktree(t, repo.path, path, "other", repo.baseCommit)
		requireValidWorkflow(dagu, "runtime_add.yaml")
		result := startWithParams(
			dagu,
			"runtime_add.yaml",
			"working_dir=./repo",
			"branch=wanted",
			"path=../wt/shared",
		)
		result.ExpectNonZeroExitCode()
		result.ExpectStdout("")
		result.ExpectStderrNotEmpty()
		requireNoResultDocument(t, dagu)
		requireLinkedWorktree(t, repo.path, path, "other", repo.baseCommit)
		require.False(t, refExists(t, repo.path, "refs/heads/wanted"))
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
