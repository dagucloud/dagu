// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec030_git_worktree_action_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
	"github.com/stretchr/testify/require"
)

const (
	actionOutputsFile = "action-outputs.txt"
	actionStderrFile  = "action-stderr.txt"
)

type repositoryFixture struct {
	path       string
	baseCommit string
}

type addResult struct {
	Path            string
	Branch          string
	Commit          string
	WorktreeCreated bool
	BranchCreated   bool
}

type removeResult struct {
	Path            string
	Branch          string
	WorktreeRemoved bool
	BranchDeleted   bool
}

type worktreeEntry struct {
	path   string
	head   string
	branch string
}

func initRepository(t *testing.T, dagu *harness.Runner) repositoryFixture {
	t.Helper()

	repoPath := dagu.ProjectPath("repo")
	gitRun(t, "", "init", repoPath)
	gitRun(t, repoPath, "symbolic-ref", "HEAD", "refs/heads/main")
	gitRun(t, repoPath, "config", "user.name", "Dagu Conformance")
	gitRun(t, repoPath, "config", "user.email", "dagu-conformance@example.com")
	dagu.WriteFile("repo/base.txt", "base\n")
	gitRun(t, repoPath, "add", "--", "base.txt")
	gitRun(t, repoPath, "commit", "-m", "initial")

	return repositoryFixture{
		path:       repoPath,
		baseCommit: gitOutput(t, repoPath, "rev-parse", "HEAD"),
	}
}

func commitFile(t *testing.T, repoPath, name, content, message string) string {
	t.Helper()

	path := filepath.Join(repoPath, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	gitRun(t, repoPath, "add", "--", filepath.FromSlash(name))
	gitRun(t, repoPath, "commit", "-m", message)
	return gitOutput(t, repoPath, "rev-parse", "HEAD")
}

func createLinkedWorktree(t *testing.T, repoPath, path, branch, startPoint string) {
	t.Helper()
	gitRun(t, repoPath, "worktree", "add", "-b", branch, path, startPoint)
}

func cloneBare(t *testing.T, source, target string) {
	t.Helper()
	gitRun(t, "", "clone", "--bare", source, target)
}

func gitRun(t *testing.T, repoPath string, args ...string) {
	t.Helper()
	output, err := gitCommand(t, repoPath, args...).CombinedOutput()
	require.NoError(t, err, "git %s\n%s", strings.Join(args, " "), output)
}

func gitOutput(t *testing.T, repoPath string, args ...string) string {
	t.Helper()
	output, err := gitCommand(t, repoPath, args...).Output()
	require.NoError(t, err, "git %s", strings.Join(args, " "))
	return strings.TrimSpace(string(output))
}

func gitCommand(t *testing.T, repoPath string, args ...string) *exec.Cmd {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	require.NoError(t, err, "Git is required by the worktree conformance fixture setup")

	commandArgs := append([]string(nil), args...)
	if repoPath != "" {
		commandArgs = append([]string{"-C", repoPath}, commandArgs...)
	}
	cmd := exec.Command(gitPath, commandArgs...) //nolint:gosec // Arguments are controlled by conformance tests.
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Dagu Conformance",
		"GIT_AUTHOR_EMAIL=dagu-conformance@example.com",
		"GIT_COMMITTER_NAME=Dagu Conformance",
		"GIT_COMMITTER_EMAIL=dagu-conformance@example.com",
	)
	return cmd
}

func refExists(t *testing.T, repoPath, ref string) bool {
	t.Helper()
	err := gitCommand(t, repoPath, "show-ref", "--verify", "--quiet", ref).Run()
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false
	}
	require.NoError(t, err)
	return false
}

func listWorktrees(t *testing.T, repoPath string) []worktreeEntry {
	t.Helper()
	output := gitOutput(t, repoPath, "worktree", "list", "--porcelain")
	blocks := strings.Split(output, "\n\n")
	entries := make([]worktreeEntry, 0, len(blocks))
	for _, block := range blocks {
		if strings.TrimSpace(block) == "" {
			continue
		}
		entry := worktreeEntry{}
		for line := range strings.SplitSeq(block, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				entry.path = canonicalPath(t, strings.TrimPrefix(line, "worktree "))
			case strings.HasPrefix(line, "HEAD "):
				entry.head = strings.TrimPrefix(line, "HEAD ")
			case strings.HasPrefix(line, "branch "):
				entry.branch = strings.TrimPrefix(line, "branch ")
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func requireLinkedWorktree(t *testing.T, repoPath, path, branch, commit string) {
	t.Helper()
	wantPath := canonicalPath(t, path)
	wantBranch := "refs/heads/" + branch
	entries := listWorktrees(t, repoPath)
	for _, entry := range entries {
		if entry.path == wantPath {
			require.Equal(t, wantBranch, entry.branch)
			require.Equal(t, commit, entry.head)
			return
		}
	}
	t.Fatalf("linked worktree %s was not registered: %+v", wantPath, entries)
}

func requireNoLinkedWorktree(t *testing.T, repoPath, path, branch string) {
	t.Helper()
	wantPath := canonicalPath(t, path)
	wantBranch := "refs/heads/" + branch
	for _, entry := range listWorktrees(t, repoPath) {
		require.NotEqual(t, wantPath, entry.path, "worktree path remains registered")
		if branch != "" {
			require.NotEqual(t, wantBranch, entry.branch, "worktree branch remains registered")
		}
	}
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	path, err := filepath.Abs(path)
	require.NoError(t, err)
	path = filepath.Clean(path)

	existing := path
	var suffix []string
	for {
		_, statErr := os.Lstat(existing)
		if statErr == nil {
			break
		}
		require.ErrorIs(t, statErr, os.ErrNotExist)
		parent := filepath.Dir(existing)
		require.NotEqual(t, existing, parent, "no existing ancestor for %s", path)
		suffix = append([]string{filepath.Base(existing)}, suffix...)
		existing = parent
	}

	resolved, err := filepath.EvalSymlinks(existing)
	require.NoError(t, err)
	return filepath.Join(append([]string{resolved}, suffix...)...)
}

func startWithParams(dagu *harness.Runner, fixture string, params ...string) *harness.Result {
	return dagu.Run("start", "--params", strings.Join(params, " "), fixture)
}

func requireValidWorkflow(dagu *harness.Runner, fixture string) {
	result := dagu.Run("validate", fixture)
	result.ExpectExitCode(0)
	result.ExpectStdout("")
	result.ExpectStderr("")
}

func readAddResult(t *testing.T, dagu *harness.Runner) addResult {
	t.Helper()
	values := readPublishedOutputs(t, dagu, "path", "branch", "commit", "worktree_created", "branch_created")
	worktreeCreated, err := strconv.ParseBool(values["worktree_created"])
	require.NoError(t, err)
	branchCreated, err := strconv.ParseBool(values["branch_created"])
	require.NoError(t, err)
	result := addResult{
		Path:            values["path"],
		Branch:          values["branch"],
		Commit:          values["commit"],
		WorktreeCreated: worktreeCreated,
		BranchCreated:   branchCreated,
	}
	require.NotEmpty(t, result.Path)
	require.NotEmpty(t, result.Branch)
	require.NotEmpty(t, result.Commit)
	return result
}

func readRemoveResult(t *testing.T, dagu *harness.Runner) removeResult {
	t.Helper()
	values := readPublishedOutputs(t, dagu, "path", "branch", "worktree_removed", "branch_deleted")
	worktreeRemoved, err := strconv.ParseBool(values["worktree_removed"])
	require.NoError(t, err)
	branchDeleted, err := strconv.ParseBool(values["branch_deleted"])
	require.NoError(t, err)
	return removeResult{
		Path:            values["path"],
		Branch:          values["branch"],
		WorktreeRemoved: worktreeRemoved,
		BranchDeleted:   branchDeleted,
	}
}

func readPublishedOutputs(t *testing.T, dagu *harness.Runner, requiredFields ...string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(dagu.ProjectPath(actionOutputsFile))
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(string(data), "\n"), "captured outputs must end in one newline: %q", data)

	fields := make(map[string]string, len(requiredFields))
	for line := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
		name, value, ok := strings.Cut(line, "=")
		require.True(t, ok, "invalid captured output line %q", line)
		require.NotContains(t, fields, name, "duplicate captured output %q", name)
		fields[name] = value
	}
	for _, field := range requiredFields {
		require.Contains(t, fields, field)
	}
	return fields
}

func requireNoPublishedOutputs(t *testing.T, dagu *harness.Runner) {
	t.Helper()
	data, err := os.ReadFile(dagu.ProjectPath(actionOutputsFile))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	require.NoError(t, err)
	require.Empty(t, data)
}

func resetActionFiles(t *testing.T, dagu *harness.Runner) {
	t.Helper()
	for _, name := range []string{actionOutputsFile, actionStderrFile} {
		err := os.Remove(dagu.ProjectPath(name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			require.NoError(t, err)
		}
	}
}
