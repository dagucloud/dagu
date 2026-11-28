// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec089_node_targeting_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/stretchr/testify/require"
)

// retryFleet is the inventory of the retry tests: three enabled web nodes.
var retryFleet = []inventory.Node{
	{Name: "web01", Host: "10.0.1.11", Labels: []string{"web"}},
	{Name: "web02", Host: "10.0.1.12", Labels: []string{"web"}},
	{Name: "web03", Host: "10.0.1.13", Labels: []string{"web"}},
}

// retriedRun is a run of a retry fixture against its own copy of retryFleet.
type retriedRun struct {
	dagu    *harness.Runner
	dataDir string
	env     []string
	runID   string
}

func newRetriedRun(t *testing.T, runID string, env ...string) *retriedRun {
	t.Helper()
	dataDir := nodesDataDirWith(t, retryFleet)
	return &retriedRun{
		dagu:    harness.NewRunner(t),
		dataDir: dataDir,
		env:     append([]string{"DAGU_DATA_DIR=" + dataDir}, env...),
		runID:   runID,
	}
}

// touch creates empty files in the project directory.
func (r *retriedRun) touch(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		require.NoError(t, os.WriteFile(r.dagu.ProjectPath(name), nil, 0o600))
	}
}

// remove deletes a file from the project directory.
func (r *retriedRun) remove(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, fileutil.Remove(r.dagu.ProjectPath(name)))
}

// start runs the fixture file with the param DIR.
func (r *retriedRun) start(t *testing.T, file string) *harness.Result {
	t.Helper()
	dir := "DIR=" + filepath.ToSlash(r.dagu.ProjectPath("."))
	return r.dagu.RunWithEnv(r.env, "start", "--run-id="+r.runID, file, "--", dir)
}

// retry retries the run. A retry appends its aggregate to the step's stdout
// file, so the earlier aggregate is removed first.
func (r *retriedRun) retry(t *testing.T, dag string, args ...string) *harness.Result {
	t.Helper()
	_ = fileutil.Remove(r.dagu.ProjectPath("nodes.json"))
	return r.dagu.RunWithEnv(r.env, append([]string{"retry", "--run-id=" + r.runID}, append(args, dag)...)...)
}

// replaceNodes replaces the nodes of the run's inventory.
func (r *retriedRun) replaceNodes(t *testing.T, nodes []inventory.Node) {
	t.Helper()

	ctx := context.Background()
	backend := file.NewBackend(config.PathsConfig{DataDir: r.dataDir})
	inventories, err := store.NewInventoryStore(
		backend.Collection(persis.CollectionInventories),
		backend.Lock(persis.CollectionInventories),
		nil,
	)
	require.NoError(t, err)
	stored, err := inventories.List(ctx)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	inv := stored[0].Inventory
	inv.Nodes = nodes
	_, err = inventories.Replace(ctx, inv, stored[0].Version)
	require.NoError(t, err)
}

// runLockStaleAge is the age at which a DAG's run lock that a stopped
// process left is taken over (Spec 089 §Retrying a step with nodes).
const runLockStaleAge = 30 * time.Second

// waitForStaleRunLock waits until no DAG's run lock under dataDir is younger
// than runLockStaleAge. A process killed while it holds the lock leaves it
// behind, and a retry before the lock is taken over fails, as the spec
// states.
func waitForStaleRunLock(t *testing.T, dataDir string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return !hasFreshRunLock(dataDir)
	}, runLockStaleAge+harness.WaitTimeout(t), 200*time.Millisecond, "the run lock was never taken over")
}

// hasFreshRunLock reports whether a DAG's run lock under dataDir is younger
// than runLockStaleAge. A run lock is the lock directory of a dag-runs
// directory.
func hasFreshRunLock(dataDir string) bool {
	fresh := false
	_ = filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		// Entries can be removed while the walk reads them.
		if err != nil || !d.IsDir() || d.Name() != dirlock.LockDirectoryName {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == "dag-runs" {
			info, err := d.Info()
			if err == nil && time.Since(info.ModTime()) <= runLockStaleAge {
				fresh = true
			}
		}
		return fs.SkipDir
	})
	return fresh
}

// changedFleet is retryFleet after web03 left it, web04 joined it, and web02
// moved to another host.
var changedFleet = []inventory.Node{
	{Name: "web01", Host: "10.0.1.11", Labels: []string{"web"}},
	{Name: "web02", Host: "10.0.2.12", Labels: []string{"web"}},
	{Name: "web04", Host: "10.0.1.14", Labels: []string{"web"}},
}

// A retry of a step with nodes reruns the nodes that did not succeed and
// starts those that never started (Spec 089 §Retrying a step with nodes).
func TestNodeStepRetry(t *testing.T) {
	t.Parallel()

	// web02 fails, so web03 never starts. The retry keeps web01's run and
	// writes no new ran-web01.
	t.Run("reruns only the nodes that did not succeed", func(t *testing.T) {
		t.Parallel()

		run := newRetriedRun(t, "retry-1")
		run.touch(t, "ok-prepare", "ok-web01", "ok-web03")
		run.start(t, "retry.yaml").ExpectNonZeroExitCode()
		require.Equal(t, []string{"web01=succeeded", "web02=failed", "web03=not_started"}, nodeStatuses(t, run.dagu))

		run.remove(t, "ran-web01")
		run.touch(t, "ok-web02")
		run.retry(t, "retry").ExpectExitCode(0)
		require.Equal(t, []string{"web01=succeeded", "web02=succeeded", "web03=succeeded"}, nodeStatuses(t, run.dagu))
		run.dagu.ExpectNoFile("ran-web01")
		run.dagu.ExpectFileContent("ran-web02", "10.0.1.12")
		run.dagu.ExpectFileContent("ran-web03", "10.0.1.13")
	})

	// The retry runs the nodes the first attempt matched, with the connection
	// settings the inventory holds now. web03 is removed and does not fail
	// the step, and web04 is not added.
	t.Run("keeps the node list of the first attempt", func(t *testing.T) {
		t.Parallel()

		run := newRetriedRun(t, "retry-2")
		run.touch(t, "ok-prepare", "ok-web01")
		run.start(t, "retry.yaml").ExpectNonZeroExitCode()

		run.replaceNodes(t, changedFleet)
		run.touch(t, "ok-web02", "ok-web04")
		run.retry(t, "retry").ExpectExitCode(0)
		require.Equal(t, []string{"web01=succeeded", "web02=succeeded", "web03=removed"}, nodeStatuses(t, run.dagu))
		run.dagu.ExpectFileContent("ran-web02", "10.0.2.12")
		run.dagu.ExpectNoFile("ran-web03")
		run.dagu.ExpectNoFile("ran-web04")
	})

	// The second attempt resets deploy but fails before it, so deploy runs
	// again only in the third, still on the nodes of the first.
	t.Run("keeps the node list through an attempt that skips the step", func(t *testing.T) {
		t.Parallel()

		run := newRetriedRun(t, "retry-3")
		run.touch(t, "ok-prepare", "ok-web01")
		run.start(t, "retry.yaml").ExpectNonZeroExitCode()

		run.remove(t, "ok-prepare")
		run.retry(t, "retry", "--step=prepare", "--downstream").ExpectNonZeroExitCode()

		run.replaceNodes(t, changedFleet)
		run.touch(t, "ok-prepare", "ok-web02", "ok-web04")
		run.retry(t, "retry").ExpectExitCode(0)
		require.Equal(t, []string{"web01=succeeded", "web02=succeeded", "web03=removed"}, nodeStatuses(t, run.dagu))
		run.dagu.ExpectNoFile("ran-web04")
	})

	// A run that reuses deploy from another run carries over that run's log
	// paths, whose records name the other run's child runs. A retry that
	// resets deploy matches its selector and runs each node under this run.
	t.Run("matches the selector for a step reused from another run", func(t *testing.T) {
		t.Parallel()

		run := newRetriedRun(t, "retry-5-source")
		run.start(t, "retry_seeded.yaml").ExpectNonZeroExitCode()

		dir := "DIR=" + filepath.ToSlash(run.dagu.ProjectPath("."))
		run.runID = "retry-5"
		run.dagu.RunWithEnv(run.env, "start", "--run-id="+run.runID, "--reuse-from=retry-5-source", "retry_seeded.yaml", "--", dir).ExpectNonZeroExitCode()

		run.remove(t, "ran-web01")
		run.touch(t, "ok-verify")
		run.retry(t, "retry_seeded", "--step=deploy", "--downstream").ExpectExitCode(0)
		require.Equal(t, []string{"web01=succeeded", "web02=succeeded", "web03=succeeded"}, nodeStatuses(t, run.dagu))
		run.dagu.ExpectFileContent("ran-web01", "10.0.1.11")
	})

	// The parent run dies while web02 runs. The retry keeps web01's run,
	// reruns web02, and starts web03. A short heartbeat lets the killed run be
	// found dead at once. A kill can land while the run holds its run lock,
	// so the retry waits until no such lock is left.
	t.Run("after the parent run is killed", func(t *testing.T) {
		t.Parallel()

		run := newRetriedRun(t, "retry-4", "DAGU_PROC_HEARTBEAT_INTERVAL=200ms", "DAGU_PROC_STALE_THRESHOLD=1s")
		run.touch(t, "go-web01", "go-web03")
		dir := "DIR=" + filepath.ToSlash(run.dagu.ProjectPath("."))
		proc := run.dagu.StartWithEnv(run.env, "start", "--run-id="+run.runID, "retry_killed.yaml", "--", dir)
		require.Eventually(t, func() bool {
			return fileutil.FileExists(run.dagu.ProjectPath("started-web02"))
		}, harness.WaitTimeout(t), 50*time.Millisecond, "web02 never started")
		proc.Kill()
		require.Eventually(t, func() bool {
			status := run.dagu.RunWithEnv(run.env, "status", "--run-id="+run.runID, "retry_killed")
			return strings.Contains(status.Stdout(), "Result: Failed")
		}, harness.WaitTimeout(t), 200*time.Millisecond, "the killed run never failed")
		waitForStaleRunLock(t, run.dataDir)

		run.remove(t, "started-web01")
		run.touch(t, "go-web02")
		run.retry(t, "retry_killed").ExpectExitCode(0)
		require.Equal(t, []string{"web01=succeeded", "web02=succeeded", "web03=succeeded"}, nodeStatuses(t, run.dagu))
		run.dagu.ExpectNoFile("started-web01")
		run.dagu.ExpectFileContent("ran-web02", "ran")
		run.dagu.ExpectFileContent("ran-web03", "ran")
	})
}
