// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCodexHarnessAddsSkipGitRepoCheckByDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses shell script as a fake codex binary")
	}

	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	require.NoError(t, os.WriteFile(codexPath, []byte(`#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "--skip-git-repo-check" ]; then
    printf 'codex ok'
    exit 0
  fi
done
echo "missing --skip-git-repo-check" >&2
exit 1
`), 0o755))

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	th := test.SetupCommand(t, test.WithBuiltExecutable())
	th.CreateDAGFile(t, "harness_codex_defaults.yaml", `
steps:
  - action: harness.run
    with:
      prompt: hello
      provider: codex
    output: RESULT
`)

	dagRunID := uuid.Must(uuid.NewV7()).String()
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args:        []string{"start", "--run-id", dagRunID, "harness_codex_defaults"},
		ExpectedOut: []string{"DAG run finished"},
	})

	ref := exec.NewDAGRunRef("harness_codex_defaults", dagRunID)
	attempt, err := th.DAGRunStore.FindAttempt(context.Background(), ref)
	require.NoError(t, err)

	status, err := attempt.ReadStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, status.Status)
	require.Len(t, status.Nodes, 1)

	stdout, err := os.ReadFile(status.Nodes[0].Stdout)
	require.NoError(t, err)
	require.Equal(t, "codex ok", string(stdout))
}

func TestHarnessMultilineCommandPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses cat as a fake harness provider")
	}

	th := test.SetupCommand(t)
	th.CreateDAGFile(t, "harness_multiline_prompt.yaml", `
harnesses:
  passthrough:
    binary: cat
    prompt_mode: stdin

steps:
  - name: review
    action: harness.run
    with:
      provider: passthrough
      prompt: |
        hey
        you
`)

	dagRunID := uuid.Must(uuid.NewV7()).String()
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args:        []string{"start", "--run-id", dagRunID, "harness_multiline_prompt"},
		ExpectedOut: []string{"DAG run finished"},
	})

	ref := exec.NewDAGRunRef("harness_multiline_prompt", dagRunID)
	attempt, err := th.DAGRunStore.FindAttempt(context.Background(), ref)
	require.NoError(t, err)

	status, err := attempt.ReadStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, status.Status)
	require.Len(t, status.Nodes, 1)
	require.Equal(t, "harness", status.Nodes[0].Step.ExecutorConfig.Type)

	stdout, err := os.ReadFile(status.Nodes[0].Stdout)
	require.NoError(t, err)
	require.Equal(t, "hey\nyou", string(stdout))
}

func TestRetryRestoresHarnessConfigFromBaseConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses cat as a fake harness provider")
	}

	baseDir := t.TempDir()
	baseConfigPath := filepath.Join(baseDir, "base.yaml")
	require.NoError(t, os.WriteFile(baseConfigPath, []byte(`
harnesses:
  passthrough:
    binary: cat
    prompt_mode: stdin
harness:
  provider: passthrough
`), 0o600))

	th := test.SetupCommand(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Paths.BaseConfig = baseConfigPath
	}))

	th.CreateDAGFile(t, "harness_retry.yaml", `
steps:
  - name: review
    action: harness.run
    with:
      prompt: Review the repository
      stdin: |
        summarize the current branch
`)

	dagRunID := uuid.Must(uuid.NewV7()).String()
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args:        []string{"start", "--run-id", dagRunID, "harness_retry"},
		ExpectedOut: []string{"DAG run finished"},
	})

	ctx := context.Background()
	ref := exec.NewDAGRunRef("harness_retry", dagRunID)
	attempt, err := th.DAGRunStore.FindAttempt(ctx, ref)
	require.NoError(t, err)

	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, status.Status)
	require.Len(t, status.Nodes, 1)
	require.Equal(t, "harness", status.Nodes[0].Step.ExecutorConfig.Type)
	require.Equal(t, "passthrough", status.Nodes[0].Step.ExecutorConfig.Config["provider"])

	stdout, err := os.ReadFile(status.Nodes[0].Stdout)
	require.NoError(t, err)
	require.Contains(t, string(stdout), "Review the repository")
	require.Contains(t, string(stdout), "summarize the current branch")

	status.Nodes[0].Status = core.NodeFailed
	require.NoError(t, th.DAGRunMgr.UpdateStatus(ctx, ref, *status))

	th.RunCommand(t, cmd.Retry(), test.CmdTest{
		Args:        []string{"retry", "--run-id", dagRunID, "harness_retry"},
		ExpectedOut: []string{"DAG run finished"},
	})

	retriedAttempt, err := th.DAGRunStore.FindAttempt(ctx, ref)
	require.NoError(t, err)

	retriedStatus, err := retriedAttempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, retriedStatus.Status)
	require.Len(t, retriedStatus.Nodes, 1)
	require.Equal(t, core.NodeSucceeded, retriedStatus.Nodes[0].Status)

	retriedStdout, err := os.ReadFile(retriedStatus.Nodes[0].Stdout)
	require.NoError(t, err)
	require.Contains(t, string(retriedStdout), "Review the repository")
	require.Contains(t, string(retriedStdout), "summarize the current branch")
}
