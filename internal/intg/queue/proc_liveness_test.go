// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/test/intgharness"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	queueTestProcHeartbeatInterval = 150 * time.Millisecond
	queueTestProcStaleThreshold    = time.Second
)

func TestSchedulerProcHeartbeat_QueuedRun(t *testing.T) {
	releaseFile := filepath.Join(t.TempDir(), "release")
	f := newFixture(t, fmt.Sprintf(`
name: queued-proc-heartbeat
steps:
  - name: sleep
    run: |
%s
`, indentQueueTestScript(intgharness.PortableCommands().WaitForFile(releaseFile), 6)), WithProcConfig(queueTestProcHeartbeatInterval, queueTestProcStaleThreshold)).
		Enqueue(1).
		StartScheduler(60 * time.Second)
	defer f.Stop()
	released := false
	defer func() {
		if !released {
			_ = os.WriteFile(releaseFile, []byte("release"), 0o600)
		}
	}()

	runID := f.runIDs[0]
	f.WaitForStatus(runID, ir.Running, 30*time.Second)

	f.RequireRunHeartbeatAdvance(runID, 10*time.Second)

	require.NoError(t, os.WriteFile(releaseFile, []byte("release"), 0o600))
	released = true
	f.WaitForStatus(runID, ir.Succeeded, 20*time.Second)
}

func TestSchedulerRepairsStaleLocalRunAndCleansProcFile(t *testing.T) {
	f := newFixture(t, `
name: scheduler-stale-repair
steps:
  - name: step1
    run: echo never
`, WithProcConfig(50*time.Millisecond, 100*time.Millisecond), WithZombieConfig(50*time.Millisecond, 1))
	defer f.Stop()

	dagRunID := uuid.Must(uuid.NewV7()).String()
	ref := ir.NewDAGRunRef(f.dag.Name, dagRunID)
	attempt, err := f.th.DAGRunStore.CreateAttempt(f.th.Context, f.dag, time.Now(), dagRunID, dagrun.CreateAttemptOptions{})
	require.NoError(t, err)

	status := ir.NewStatusBuilder(f.dag).Create(
		dagRunID,
		ir.Running,
		0,
		time.Now().Add(-2*time.Second),
		ir.WithAttemptID(attempt.ID()),
		ir.WithHierarchyRefs(ref, ir.DAGRunRef{}),
	)
	require.NotEmpty(t, status.Nodes)
	status.Nodes[0].Status = ir.NodeRunning

	require.NoError(t, attempt.Open(f.th.Context))
	require.NoError(t, attempt.Write(f.th.Context, status))
	require.NoError(t, attempt.Close(f.th.Context))

	procFile := test.CreateStaleLegacyProcFileWithAttempt(
		t,
		f.th.Config.Paths.ProcDir,
		f.dag.ProcGroup(),
		ref,
		attempt.ID(),
		time.Now().Add(-2*time.Second),
		time.Second,
	)

	f.StartScheduler(10 * time.Second)
	f.WaitForStatus(dagRunID, ir.Failed, 10*time.Second)

	repaired, err := f.Status(dagRunID)
	require.NoError(t, err)
	require.Equal(t, ir.NodeFailed, repaired.Nodes[0].Status)
	require.Contains(t, repaired.Nodes[0].Error, "stale local process detected")

	f.RequireProcFileMissing(procFile, 5*time.Second)
}

func TestQueueStaleProcFileDoesNotBlockDrain(t *testing.T) {
	f := newFixture(t, `
name: queue-stale-cleanup
max_active_runs: 1
steps:
  - name: echo
    run: echo hello
`, WithProcConfig(queueTestProcHeartbeatInterval, queueTestProcStaleThreshold), WithZombieConfig(50*time.Millisecond, 3)).
		Enqueue(1)
	defer f.Stop()

	fakeRunID := uuid.Must(uuid.NewV7()).String()
	fakeRef := ir.NewDAGRunRef(f.dag.Name, fakeRunID)
	staleStartedAt := time.Now().Add(-30 * time.Second)
	procFile := test.CreateStaleLegacyProcFile(
		t,
		f.th.Config.Paths.ProcDir,
		f.dag.ProcGroup(),
		fakeRef,
		staleStartedAt,
		30*time.Second,
	)

	f.RequireProcEntryStale(fakeRunID, 5*time.Second)

	f.StartScheduler(30 * time.Second)
	f.WaitForStatus(f.runIDs[0], ir.Succeeded, 20*time.Second)

	f.RequireProcFileMissing(procFile, 15*time.Second)
}
