// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec030_git_worktree_action_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
	"github.com/stretchr/testify/require"
)

const (
	actionStdoutFile = "action-stdout.json"
	actionStderrFile = "action-stderr.txt"
)

type repositoryFixture struct {
	path       string
	baseCommit string
}

type addResult struct {
	Operation     string `json:"operation"`
	Path          string `json:"path"`
	Branch        string `json:"branch"`
	Commit        string `json:"commit"`
	Created       bool   `json:"created"`
	BranchCreated bool   `json:"branch_created"`
}

type removeResult struct {
	Operation     string `json:"operation"`
	Path          string `json:"path"`
	Branch        string `json:"branch"`
	Removed       bool   `json:"removed"`
	BranchDeleted bool   `json:"branch_deleted"`
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
		for _, line := range strings.Split(block, "\n") {
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
	var result addResult
	readResultLine(t, dagu, &result, "operation", "path", "branch", "commit", "created", "branch_created")
	require.Equal(t, "worktree_add", result.Operation)
	require.NotEmpty(t, result.Path)
	require.NotEmpty(t, result.Branch)
	require.NotEmpty(t, result.Commit)
	return result
}

func readRemoveResult(t *testing.T, dagu *harness.Runner) removeResult {
	t.Helper()
	var result removeResult
	readResultLine(t, dagu, &result, "operation", "path", "branch", "removed", "branch_deleted")
	require.Equal(t, "worktree_remove", result.Operation)
	return result
}

func readResultLine(t *testing.T, dagu *harness.Runner, target any, requiredFields ...string) {
	t.Helper()
	data, err := os.ReadFile(dagu.ProjectPath(actionStdoutFile))
	require.NoError(t, err)
	require.True(t, bytes.HasSuffix(data, []byte("\n")), "result must end in one newline: %q", data)
	require.Equal(t, 1, bytes.Count(data, []byte("\n")), "result must occupy exactly one line: %q", data)
	document := bytes.TrimSuffix(data, []byte("\n"))
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document, &fields))
	for _, field := range requiredFields {
		require.Contains(t, fields, field)
	}
	require.NoError(t, json.Unmarshal(document, target))
}

func requireNoResultDocument(t *testing.T, dagu *harness.Runner) {
	t.Helper()
	data, err := os.ReadFile(dagu.ProjectPath(actionStdoutFile))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	require.NoError(t, err)
	require.Empty(t, data)
}

func resetActionStreams(t *testing.T, dagu *harness.Runner) {
	t.Helper()
	for _, name := range []string{actionStdoutFile, actionStderrFile} {
		err := os.Remove(dagu.ProjectPath(name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			require.NoError(t, err)
		}
	}
}
