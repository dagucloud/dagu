// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	osExec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentFinalizeGitWorktreesResumesPersistedPhase(t *testing.T) {
	t.Parallel()

	gitPath, err := osExec.LookPath("git")
	if err != nil {
		t.Skip("git executable is unavailable")
	}
	runGit := func(dir string, args ...string) string {
		t.Helper()
		commandArgs := args
		if dir != "" {
			commandArgs = append([]string{"-C", dir}, args...)
		}
		cmd := osExec.Command(gitPath, commandArgs...) //nolint:gosec // Test arguments are fixed or temporary paths.
		output, commandErr := cmd.CombinedOutput()
		require.NoErrorf(t, commandErr, "git %s: %s", strings.Join(commandArgs, " "), output)
		return strings.TrimSpace(string(output))
	}

	testDir := t.TempDir()
	repoRoot := filepath.Join(testDir, "repo")
	worktreePath := filepath.Join(testDir, "worktree")
	runGit("", "init", repoRoot)
	runGit(repoRoot, "config", "user.email", "test@example.com")
	runGit(repoRoot, "config", "user.name", "Test User")
	runGit(repoRoot, "commit", "--allow-empty", "-m", "initial")
	runGit(repoRoot, "worktree", "add", "-b", "resume-cleanup", worktreePath)

	commonDir := runGit(repoRoot, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repoRoot, commonDir)
	}
	commonDir, err = filepath.EvalSymlinks(commonDir)
	require.NoError(t, err)

	plan, err := runtime.NewPlan()
	require.NoError(t, err)
	runner := runtime.New(&runtime.Config{
		GitWorktreeFinalization: &coreexec.GitWorktreeFinalization{
			Status: core.Succeeded,
			Cleanups: []coreexec.GitWorktreeCleanup{{
				Policy:         core.GitWorktreeCleanupOnFinish,
				RepositoryRoot: repoRoot,
				CommonDir:      commonDir,
				Path:           worktreePath,
				Branch:         "resume-cleanup",
			}},
		},
	})
	require.True(t, runner.HasPendingGitWorktreeFinalization())

	a := &Agent{
		dagRunID: "run-1",
		dag:      &core.DAG{Name: "resume-cleanup"},
		plan:     plan,
		runner:   runner,
	}
	finishedStatus := coreexec.DAGRunStatus{Status: core.Running}
	require.NoError(t, a.finalizeGitWorktrees(context.Background(), nil, &finishedStatus))

	assert.Equal(t, core.Succeeded, finishedStatus.Status)
	assert.Nil(t, finishedStatus.GitWorktreeFinalization)
	require.NoDirExists(t, worktreePath)
}
