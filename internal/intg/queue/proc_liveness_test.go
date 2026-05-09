// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	queueTestProcHeartbeatInterval = 150 * time.Millisecond
	queueTestProcStaleThreshold    = time.Second
)

func TestSchedulerProcHeartbeat_QueuedRun(t *testing.T) {
	f := newFixture(t, fmt.Sprintf(`
name: queued-proc-heartbeat
steps:
  - name: sleep
    run: %s
`, test.ShellQuote(test.Sleep(6*time.Second))), WithProcConfig(queueTestProcHeartbeatInterval, queueTestProcHeartbeatInterval, queueTestProcStaleThreshold)).
		Enqueue(1).
		StartScheduler(30 * time.Second)
	defer f.Stop()

	runID := f.runIDs[0]
	f.WaitForStatus(runID, core.Running, 10*time.Second)

	ref := exec.NewDAGRunRef(f.dag.Name, runID)
	require.Eventually(t, func() bool {
		alive, err := f.th.ProcStore.IsRunAlive(f.th.Context, f.dag.ProcGroup(), ref)
		return err == nil && alive
	}, 10*time.Second, 100*time.Millisecond, "proc heartbeat should report run as alive")

	f.WaitForStatus(runID, core.Succeeded, 20*time.Second)
}

func TestSchedulerRepairsStaleLocalRunAndCleansProcFile(t *testing.T) {
	f := newFixture(t, `
name: scheduler-stale-repair
steps:
  - name: step1
    run: echo never
`, WithProcConfig(50*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond), WithZombieConfig(50*time.Millisecond, 1))
	defer f.Stop()

	dagRunID := uuid.Must(uuid.NewV7()).String()
	ref := exec.NewDAGRunRef(f.dag.Name, dagRunID)
	attempt, err := f.th.DAGRunStore.CreateAttempt(f.th.Context, f.dag, time.Now(), dagRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)

	status := transform.NewStatusBuilder(f.dag).Create(
		dagRunID,
		core.Running,
		0,
		time.Now().Add(-2*time.Second),
		transform.WithAttemptID(attempt.ID()),
		transform.WithHierarchyRefs(ref, exec.DAGRunRef{}),
	)
	require.NotEmpty(t, status.Nodes)
	status.Nodes[0].Status = core.NodeRunning

	require.NoError(t, attempt.Open(f.th.Context))
	require.NoError(t, attempt.Write(f.th.Context, status))
	require.NoError(t, attempt.Close(f.th.Context))

	procFile := test.CreateStaleProcFileWithAttempt(
		t,
		f.th.Config.Paths.ProcDir,
		f.dag.ProcGroup(),
		ref,
		attempt.ID(),
		time.Now().Add(-2*time.Second),
		time.Second,
	)

	f.StartScheduler(10 * time.Second)
	f.WaitForStatus(dagRunID, core.Failed, 10*time.Second)

	repaired, err := f.Status(dagRunID)
	require.NoError(t, err)
	require.Equal(t, core.NodeFailed, repaired.Nodes[0].Status)
	require.Contains(t, repaired.Nodes[0].Error, "stale local process detected")

	require.Eventually(t, func() bool {
		_, err := os.Stat(procFile)
		return os.IsNotExist(err)
	}, 5*time.Second, 50*time.Millisecond)
}

func TestQueueStaleProcFileDoesNotBlockDrain(t *testing.T) {
	f := newFixture(t, `
name: queue-stale-cleanup
max_active_runs: 1
steps:
  - name: echo
    run: echo hello
`, WithProcConfig(queueTestProcHeartbeatInterval, queueTestProcHeartbeatInterval, queueTestProcStaleThreshold), WithZombieConfig(50*time.Millisecond, 3)).
		Enqueue(1)
	defer f.Stop()

	fakeRunID := uuid.Must(uuid.NewV7()).String()
	fakeRef := exec.NewDAGRunRef(f.dag.Name, fakeRunID)
	staleStartedAt := time.Now().Add(-30 * time.Second)
	procFile := test.CreateStaleProcFile(
		t,
		f.th.Config.Paths.ProcDir,
		f.dag.ProcGroup(),
		fakeRef,
		staleStartedAt,
		30*time.Second,
	)

	require.Eventually(t, func() bool {
		entries, err := f.th.ProcStore.ListEntries(f.th.Context, f.dag.ProcGroup())
		if err != nil {
			return false
		}
		for _, entry := range entries {
			if entry.Meta.DAGRunID == fakeRunID {
				return !entry.Fresh
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "stale proc file should be visible before scheduler starts")

	f.StartScheduler(30 * time.Second)
	f.WaitForStatus(f.runIDs[0], core.Succeeded, 20*time.Second)

	require.Eventually(t, func() bool {
		_, err := os.Stat(procFile)
		return os.IsNotExist(err)
	}, 15*time.Second, 100*time.Millisecond)
}
