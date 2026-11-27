// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestFirstStartMovesDefaultProjectState proves Spec 102 "First Start" and
// "What Moves" for an install without workspaces. A run finished on v2 and a
// run still running when the first start with projects on begins: the first
// start cancels the live run and waits for its process to exit, then moves
// the default project's runs and logs into its state directory, where its
// commands read them as before.
func TestFirstStartMovesDefaultProjectState(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	home := filepath.Join(t.TempDir(), "dagu")
	v2 := []string{"DAGU_HOME=" + home}
	v3 := append([]string{"DAGU_EXPERIMENTAL_PROJECTS=true"}, v2...)
	finished := writeDAGFile(t, filepath.Join(home, "dags", "finished.yaml"), "steps:\n  - id: greet\n    run: echo moved-with-its-run\n")
	live := writeDAGFile(t, filepath.Join(home, "dags", "live.yaml"), "steps:\n  - id: wait\n    run: sleep 120\n")

	dagu.RunWithEnv(v2, "start", finished).ExpectExitCode(0)
	running := dagu.StartWithEnv(v2, "start", live)
	deadline := time.Now().Add(harness.WaitTimeout(t))
	for !strings.Contains(dagu.RunWithEnv(v2, "history", "live").Stdout(), "Running") {
		require.True(t, time.Now().Before(deadline), "the run did not start: %s", running.FailureOutput())
		time.Sleep(100 * time.Millisecond)
	}

	history := dagu.RunWithEnv(v3, "history", "finished")
	history.ExpectExitCode(0)
	require.Contains(t, history.Stdout(), "Succeeded")
	// The run's process wrote its end in the run's record before the
	// record moved.
	history = dagu.RunWithEnv(v3, "history", "live")
	history.ExpectExitCode(0)
	require.Contains(t, history.Stdout(), "Aborted")

	status := dagu.RunWithEnv(v3, "status", "finished")
	status.ExpectExitCode(0)
	require.Contains(t, status.Stdout(), "moved-with-its-run")
	state := filepath.Join(home, "data", "projects", "default")
	require.Contains(t, status.Stdout(), filepath.Join(state, "logs", "finished"))
	for _, dir := range []string{"runs/finished", "runs/live", "logs/live"} {
		require.DirExists(t, filepath.Join(state, filepath.FromSlash(dir)))
	}
	for _, dir := range []string{"data/dag-runs", "logs/finished"} {
		require.NoDirExists(t, filepath.Join(home, filepath.FromSlash(dir)))
	}
}

func writeDAGFile(t *testing.T, path, content string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
