// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewContext_DAGRunStoreUsesConfiguredArtifactDirForCleanup(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	artifactRoot := filepath.Join(home, "custom-artifacts")
	dagRunsDir := filepath.Join(home, "custom-runs")
	configPath := writeArtifactTestConfig(t, home, dagRunsDir, artifactRoot)

	command := &cobra.Command{Use: "status"}
	initFlags(command)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)

	dag := &core.DAG{
		Name:      "cleanup-artifact-test",
		Location:  filepath.Join(home, "dags", "cleanup-artifact-test.yaml"),
		Artifacts: &core.ArtifactsConfig{Enabled: true},
	}
	const dagRunID = "run-cleanup-1"

	attempt, err := ctx.DAGRunStore.CreateAttempt(ctx.Context, dag, time.Now(), dagRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx.Context))

	archiveDir := filepath.Join(artifactRoot, dag.Name, "dag-run_custom_"+dagRunID)
	require.NoError(t, os.MkdirAll(archiveDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, "artifact.txt"), []byte("artifact"), 0o600))

	status := exec.InitialStatus(dag)
	status.DAGRunID = dagRunID
	status.Status = core.Succeeded
	status.ArchiveDir = archiveDir
	require.NoError(t, attempt.Write(ctx.Context, status))
	require.NoError(t, attempt.Close(ctx.Context))

	require.DirExists(t, archiveDir)

	err = ctx.DAGRunStore.RemoveDAGRun(ctx.Context, exec.NewDAGRunRef(dag.Name, dagRunID))
	require.NoError(t, err)
	assert.NoDirExists(t, archiveDir)
}

func TestRunDry_DoesNotCreateArtifactDirectory(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	artifactRoot := filepath.Join(home, "dry-artifacts")
	configPath := writeArtifactTestConfig(t, home, filepath.Join(home, "dag-runs"), artifactRoot)
	dagFile := filepath.Join(home, "dry-artifact-test.yaml")
	require.NoError(t, os.WriteFile(dagFile, []byte(`
name: dry-artifact-test
artifacts:
  enabled: true
steps:
  - name: step1
    run: echo "should-not-run"
`), 0o600))

	command := &cobra.Command{Use: "dry"}
	initFlags(command, dryFlags...)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)

	err = runDry(ctx, []string{dagFile})
	require.NoError(t, err)

	_, statErr := os.Stat(artifactRoot)
	assert.True(t, os.IsNotExist(statErr), "dry-run should not create artifact directories")
}

func writeArtifactTestConfig(t *testing.T, home, dagRunsDir, artifactDir string) string {
	t.Helper()

	configPath := filepath.Join(home, "config.yaml")
	content := fmt.Sprintf(`
paths:
  dag_runs_dir: %q
  artifact_dir: %q
`, dagRunsDir, artifactDir)
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0o600))
	return configPath
}
