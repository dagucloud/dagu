// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pruneArtifactRunDir creates an artifact directory under root the way a run
// does, with a timestamp old enough to clear every cutoff the command uses.
func pruneArtifactRunDir(t *testing.T, root, dagName, dagRunID string) string {
	t.Helper()

	dir, err := artifactpath.NewRunDir(
		context.Background(), root, "", dagName, dagRunID, time.Now().UTC().Add(-72*time.Hour))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "out.txt"), []byte("x"), 0o600))
	return dir
}

func TestPruneArtifactsCommand(t *testing.T) {
	t.Run("RemovesOrphanedArtifactDir", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dir := pruneArtifactRunDir(t, th.Config.Paths.ArtifactDir, "gone-dag", "gone-run")

		th.RunCommand(t, cmd.PruneArtifacts(), test.CmdTest{
			Args: []string{"prune-artifacts", "--yes"},
		})

		assert.NoDirExists(t, dir)
	})

	t.Run("KeepsArtifactsOfSurvivingRuns", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dag := &ir.DAG{Name: "live-dag"}
		attempt, err := th.DAGRunRepository.CreateAttempt(
			th.Context, dag, time.Now(), "live-run", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, attempt.Open(th.Context))
		require.NoError(t, attempt.Write(th.Context, ir.DAGRunStatus{
			Name:     dag.Name,
			DAGRunID: "live-run",
			Status:   ir.Running,
		}))
		require.NoError(t, attempt.Close(th.Context))

		live := pruneArtifactRunDir(t, th.Config.Paths.ArtifactDir, dag.Name, "live-run")
		stale := pruneArtifactRunDir(t, th.Config.Paths.ArtifactDir, "gone-dag", "gone-run")

		th.RunCommand(t, cmd.PruneArtifacts(), test.CmdTest{
			Args: []string{"prune-artifacts", "--yes"},
		})

		assert.DirExists(t, live)
		assert.NoDirExists(t, stale)
	})

	t.Run("DryRunRemovesNothing", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dir := pruneArtifactRunDir(t, th.Config.Paths.ArtifactDir, "gone-dag", "gone-run")

		th.RunCommand(t, cmd.PruneArtifacts(), test.CmdTest{
			Args: []string{"prune-artifacts", "--dry-run"},
		})

		assert.DirExists(t, dir)
	})

	// Not parallel: the subtest replaces the process-global os.Stdin to answer
	// the confirmation prompt.
	t.Run("QuietStillRequiresConfirmation", func(t *testing.T) {
		th := test.SetupCommand(t)
		dir := pruneArtifactRunDir(t, th.Config.Paths.ArtifactDir, "gone-dag", "gone-run")

		stdin, input, err := os.Pipe()
		require.NoError(t, err)
		originalStdin := os.Stdin
		os.Stdin = stdin
		t.Cleanup(func() {
			os.Stdin = originalStdin
			require.NoError(t, stdin.Close())
		})
		_, err = input.WriteString("n\n")
		require.NoError(t, err)
		require.NoError(t, input.Close())

		th.RunCommand(t, cmd.PruneArtifacts(), test.CmdTest{
			Args: []string{"prune-artifacts", "--quiet"},
		})

		assert.DirExists(t, dir)
	})

	// --root reclaims a tree left at a previous data directory, which is the
	// orphan case the configured root can never reach.
	t.Run("PrunesExternalRoot", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		oldRoot := t.TempDir()
		dir := pruneArtifactRunDir(t, oldRoot, "gone-dag", "gone-run")

		th.RunCommand(t, cmd.PruneArtifacts(), test.CmdTest{
			Args: []string{"prune-artifacts", "--root", oldRoot, "--yes"},
		})

		assert.NoDirExists(t, dir)
	})

	// The log directory shares the pre-date artifact layout; passing it as
	// --root must not remove the logs of runs whose records are gone.
	t.Run("RejectsLogDirRoot", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		logDir := th.Config.Paths.LogDir
		logRunDir := filepath.Join(logDir, "gone-dag",
			"dag-run_"+time.Now().UTC().Add(-72*time.Hour).Format("20060102_150405Z")+"_gone-run")
		require.NoError(t, os.MkdirAll(logRunDir, 0o750))

		err := th.RunCommandWithError(t, cmd.PruneArtifacts(), test.CmdTest{
			Args: []string{"prune-artifacts", "--root", logDir, "--yes"},
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "refusing to sweep")
		assert.DirExists(t, logRunDir)
	})

	t.Run("RejectsInvalidOlderThan", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		for _, value := range []string{"abc", "0h"} {
			err := th.RunCommandWithError(t, cmd.PruneArtifacts(), test.CmdTest{
				Args: []string{"prune-artifacts", "--older-than", value, "--yes"},
			})

			require.Error(t, err, value)
			assert.Contains(t, err.Error(), "invalid --older-than", value)
		}
	})
}
