// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"cmp"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serviceRuns returns the runs of the fixture DAG, newest first.
func (f *testFixture) serviceRuns() []ir.DAGRunStatus {
	f.t.Helper()
	runs, err := f.readServiceRuns()
	require.NoError(f.t, err)
	return runs
}

func (f *testFixture) readServiceRuns() ([]ir.DAGRunStatus, error) {
	runs, err := f.coord.DAGRunRepository.RecentStatuses(f.coord.Context, f.dagWrapper.Name, 100)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(runs, func(a, b ir.DAGRunStatus) int {
		return cmp.Compare(b.CreatedAt, a.CreatedAt)
	})
	return runs, nil
}

// waitForServiceRuns waits until the runs of the fixture DAG satisfy cond. The
// condition runs on another goroutine, where a failing require would only stop
// that goroutine, so a read error ends the wait and fails the test after it.
func (f *testFixture) waitForServiceRuns(label string, timeout time.Duration, cond func([]ir.DAGRunStatus) bool) []ir.DAGRunStatus {
	f.t.Helper()
	var (
		runs    []ir.DAGRunStatus
		readErr error
	)
	f.requireEventuallyNoSchedulerError(label, timeout, 100*time.Millisecond, func() bool {
		runs, readErr = f.readServiceRuns()
		return readErr != nil || cond(runs)
	})
	require.NoError(f.t, readErr)
	return runs
}

func serviceWorkerDAGYAML(name, releaseFile string) string {
	return fmt.Sprintf(`name: %s
restart_policy: always
restart_wait_sec: 1
hist_retention_runs: 100
worker_selector:
  test: "true"
steps:
  - name: wait
    command: |
%s
`, name, indentYAMLBlock(waitForReleaseFileScript(releaseFile), 6))
}

func activeRunCount(runs []ir.DAGRunStatus) int {
	n := 0
	for _, run := range runs {
		if run.Status.IsActive() {
			n++
		}
	}
	return n
}

// A service DAG runs on a worker. When that worker is killed, its run fails
// when the lease expires, and the scheduler restarts the DAG on another worker.
// At no point do two runs of the DAG overlap.
func TestDistributedRun_ServiceDAGRestartsOnAnotherWorkerAfterCrash(t *testing.T) {
	heartbeatThreshold := testStaleHeartbeatThreshold
	leaseThreshold := testStaleLeaseThreshold
	runningTimeout := 20 * time.Second
	failureTimeout := 30 * time.Second
	schedulerTimeout := 90 * time.Second
	if runtime.GOOS == "windows" {
		heartbeatThreshold = 12 * time.Second
		leaseThreshold = 20 * time.Second
		runningTimeout = 40 * time.Second
		failureTimeout = 90 * time.Second
		schedulerTimeout = 120 * time.Second
	}

	releaseFile := filepath.Join(t.TempDir(), "service.release")
	f := newTestFixture(t, serviceWorkerDAGYAML("service-worker-crash", releaseFile),
		withWorkerCount(0),
		withStaleThresholds(heartbeatThreshold, leaseThreshold),
		withZombieDetectionInterval(testZombieDetectorInterval),
	)
	workers := map[string]*osexec.Cmd{}
	for _, id := range []string{"service-worker-a", "service-worker-b"} {
		cmd, _ := startWorkerProcess(t, f, id, "test=true")
		workers[id] = cmd
	}
	defer func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0o600)
		for _, cmd := range workers {
			_ = cmdutil.TerminateProcessGroup(cmd, cmdutil.ForceTermination())
		}
		f.cleanup()
	}()

	f.startScheduler(schedulerTimeout)

	runs := f.waitForServiceRuns("the first run on a worker", runningTimeout, func(runs []ir.DAGRunStatus) bool {
		return len(runs) == 1 && runs[0].Status == ir.Running && runs[0].WorkerID != ""
	})
	first := runs[0]
	require.Contains(t, workers, first.WorkerID)
	waitForLease(t, f, first.AttemptKey, 5*time.Second)

	var survivor string
	for id := range workers {
		if id != first.WorkerID {
			survivor = id
		}
	}
	require.NoError(t, cmdutil.TerminateProcessGroup(workers[first.WorkerID], cmdutil.ForceTermination()))

	// An overlap ends the wait at once, to be reported after it.
	runs = f.waitForServiceRuns("the crashed run failed and a restart running", failureTimeout, func(runs []ir.DAGRunStatus) bool {
		return activeRunCount(runs) > 1 || len(runs) >= 2 && runs[0].Status == ir.Running && runs[0].DAGRunID != first.DAGRunID
	})
	require.LessOrEqual(t, activeRunCount(runs), 1, "two runs of a service DAG overlap")

	restarted := runs[0]
	assert.Equal(t, ir.TriggerTypeRestart, restarted.TriggerType)
	assert.Equal(t, survivor, restarted.WorkerID, "the restart runs on the worker that is still alive")

	crashed := runs[len(runs)-1]
	require.Equal(t, first.DAGRunID, crashed.DAGRunID)
	assert.Equal(t, ir.Failed, crashed.Status)
	assert.Equal(t, dispatch.DistributedLeaseExpiredReason(first.WorkerID), crashed.Error)

	time.Sleep(restartQuiet)
	assert.Len(t, f.serviceRuns(), 2, "one restart follows the crashed run")
}

// The scheduler that keeps a service DAG alive is replaced while a run is
// active. The new scheduler takes over without starting a second run, and
// restarts the DAG once the run has ended.
func TestDistributedRun_ServiceDAGKeepsOneRunAcrossSchedulerFailover(t *testing.T) {
	releaseFile := filepath.Join(t.TempDir(), "service.release")
	f := newTestFixture(t, serviceWorkerDAGYAML("service-failover", releaseFile))
	defer func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0o600)
		f.cleanup()
	}()

	f.startScheduler(90 * time.Second)
	runs := f.waitForServiceRuns("the first run", distrTestTimeout(20*time.Second), func(runs []ir.DAGRunStatus) bool {
		return len(runs) == 1 && runs[0].Status == ir.Running
	})
	first := runs[0]

	f.stopScheduler()
	f.startScheduler(90 * time.Second)

	time.Sleep(restartQuiet)
	runs = f.serviceRuns()
	require.Len(t, runs, 1, "the new scheduler starts no run while one is active")
	require.Equal(t, first.DAGRunID, runs[0].DAGRunID)
	require.Equal(t, ir.Running, runs[0].Status)

	require.NoError(t, os.WriteFile(releaseFile, []byte("ok"), 0o600))
	runs = f.waitForServiceRuns("a restart after the run ended", distrTestTimeout(30*time.Second), func(runs []ir.DAGRunStatus) bool {
		return len(runs) >= 2
	})
	oldest := runs[len(runs)-1]
	assert.Equal(t, first.DAGRunID, oldest.DAGRunID)
	assert.Equal(t, ir.Succeeded, oldest.Status)
	assert.Equal(t, ir.TriggerTypeRestart, runs[len(runs)-2].TriggerType)
}

// restartQuiet is long enough for a restart to be enqueued and dispatched once
// the one second restart wait of the fixtures has elapsed.
const restartQuiet = 5 * time.Second
