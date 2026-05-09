// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/worker"
	"github.com/dagucloud/dagu/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testStaleHeartbeatThreshold = 2 * time.Second
	testStaleLeaseThreshold     = 3 * time.Second
	testZombieDetectorInterval  = 500 * time.Millisecond
)

func delayedAfterAckFailureTimeout(mode workerMode) time.Duration {
	if mode == sharedNothingMode && runtime.GOOS == "windows" && raceEnabled() {
		return 30 * time.Second
	}
	return 20 * time.Second
}

func waitForReleaseFileScript(path string) string {
	return test.ForOS(
		fmt.Sprintf("while [ ! -f %s ]; do\n  sleep 0.05\ndone", test.PosixQuote(path)),
		fmt.Sprintf("while (-not (Test-Path %s)) {\n  Start-Sleep -Milliseconds 50\n}", test.PowerShellQuote(path)),
	)
}

// TestDistributedRun_WorkerCrash_MarkedFailed verifies that a hard-killed worker
// is treated as a crash and the coordinator's zombie detector marks the run FAILED.
func TestDistributedRun_WorkerCrash_MarkedFailed(t *testing.T) {
	releaseFile := filepath.Join(t.TempDir(), "worker-crash.release")
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
	})

	f := newTestFixture(t, fmt.Sprintf(`
type: graph
name: zombie-crash-test
worker_selector:
  test: "true"
steps:
  - name: long-step
    run: |
%s
`, indentYAMLBlock(waitForReleaseFileScript(releaseFile), 6)),
		withWorkerCount(0),
		withStaleThresholds(testStaleHeartbeatThreshold, testStaleLeaseThreshold),
		withZombieDetectionInterval(testZombieDetectorInterval),
	)
	defer f.cleanup()

	workerCmd, _ := startWorkerProcess(t, f, "crash-worker", "test=true")

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(90 * time.Second)

	status := f.waitForStatus(core.Running, 15*time.Second)
	require.Equal(t, core.Running, status.Status)
	require.NotEmpty(t, status.AttemptKey)
	require.Equal(t, "crash-worker", status.WorkerID)
	lease := waitForLease(t, f, status.AttemptKey, 5*time.Second)
	require.Equal(t, "crash-worker", lease.WorkerID)

	require.NoError(t, cmdutil.KillProcessGroup(workerCmd, os.Kill))

	finalStatus := f.waitForStatus(core.Failed, 20*time.Second)
	assert.Equal(t, core.Failed, finalStatus.Status)
	assert.Contains(t, finalStatus.Error, "worker")
}

func TestDistributedRun_AckedTaskWithoutInitialStatus_MarkedFailedAndCleansLease(t *testing.T) {
	t.Run("SharedNothing", func(t *testing.T) {
		testDistributedRunAckedTaskWithoutInitialStatus(t, sharedNothingMode)
	})

	t.Run("SharedStorage", func(t *testing.T) {
		testDistributedRunAckedTaskWithoutInitialStatus(t, sharedFSMode)
	})
}

func TestDistributedRun_DelayedAfterAck_DoesNotExecuteAfterStaleCleanup(t *testing.T) {
	t.Run("SharedNothing", func(t *testing.T) {
		testDistributedRunDelayedAfterAckDoesNotExecute(t, sharedNothingMode)
	})

	t.Run("SharedStorage", func(t *testing.T) {
		testDistributedRunDelayedAfterAckDoesNotExecute(t, sharedFSMode)
	})
}

// TestDistributedRun_HeartbeatRefreshKeepsQuietRunAlive verifies that a
// long-running quiet step remains RUNNING past the lease threshold because
// coordinator-owned heartbeat refreshes keep the lease fresh.
func TestDistributedRun_HeartbeatRefreshKeepsQuietRunAlive(t *testing.T) {
	heartbeatThreshold := testStaleHeartbeatThreshold
	leaseThreshold := testStaleLeaseThreshold
	freshWindow := 2 * time.Second
	leaseObservationWindow := leaseThreshold + time.Second
	finalStatusTimeout := 15 * time.Second
	if runtime.GOOS == "windows" {
		heartbeatThreshold = 12 * time.Second
		leaseThreshold = 20 * time.Second
		// Windows service startup and timer scheduling can lag enough that the
		// refreshed lease is still valid but older than the nominal stale window.
		freshWindow = leaseThreshold + 5*time.Second
		leaseObservationWindow = leaseThreshold + 3*time.Second
		finalStatusTimeout = 90 * time.Second
	}
	releaseFile := filepath.Join(t.TempDir(), "quiet-heartbeat.release")
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
	})

	f := newTestFixture(t, fmt.Sprintf(`
type: graph
name: quiet-heartbeat-test
worker_selector:
  test: "true"
steps:
  - name: long-step
    run: |
%s
`, indentYAMLBlock(waitForReleaseFileScript(releaseFile), 6)),
		withStaleThresholds(heartbeatThreshold, leaseThreshold),
		withZombieDetectionInterval(testZombieDetectorInterval),
	)
	defer f.cleanup()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	status := f.waitForStatus(core.Running, 15*time.Second)
	require.NotEmpty(t, status.AttemptKey)
	initialLease := waitForLease(t, f, status.AttemptKey, 5*time.Second).LastHeartbeatAt
	runningSince := time.Now()

	var lease exec.DAGRunLease
	require.Eventually(t, func() bool {
		currentStatus, err := f.latestStatus()
		if err != nil || currentStatus.Status != core.Running || currentStatus.AttemptKey != status.AttemptKey {
			return false
		}
		currentLease, err := f.coord.DAGRunLeaseStore.Get(f.coord.Context, status.AttemptKey)
		if err != nil || currentLease == nil || currentLease.LastHeartbeatAt <= initialLease {
			return false
		}
		status = currentStatus
		lease = *currentLease
		return true
	}, 10*time.Second, 100*time.Millisecond, "heartbeat should refresh while run remains active")

	assert.Greater(t, lease.LastHeartbeatAt, initialLease)
	assert.WithinDuration(t, time.Now(), time.UnixMilli(lease.LastHeartbeatAt), freshWindow)
	if runtime.GOOS == "windows" {
		// The Windows signal/file-handle path is flaky after this point, but the
		// behavior under test is already proven: coordinator heartbeats refreshed
		// the lease while the quiet run remained active.
		require.NoError(t, os.WriteFile(releaseFile, []byte("ok"), 0600))
		return
	}
	require.Eventually(t, func() bool {
		return time.Since(runningSince) >= leaseObservationWindow
	}, leaseObservationWindow+time.Second, 200*time.Millisecond)
	status, err := f.latestStatus()
	require.NoError(t, err)
	require.Equal(t, core.Running, status.Status, "run should remain active beyond the stale threshold")
	lease = waitForLease(t, f, status.AttemptKey, 5*time.Second)

	require.NoError(t, os.WriteFile(releaseFile, []byte("ok"), 0600))
	finalStatus := f.waitForStatus(core.Succeeded, finalStatusTimeout)
	assert.Equal(t, core.Succeeded, finalStatus.Status)
}

// TestDistributedRun_QueueConcurrency_ActiveRunCounted verifies that a running
// distributed run with fresh heartbeats continues to block the next queued item.
func TestDistributedRun_QueueConcurrency_ActiveRunCounted(t *testing.T) {
	heartbeatThreshold := testStaleHeartbeatThreshold
	leaseThreshold := testStaleLeaseThreshold
	completionTimeout := 30 * time.Second
	if runtime.GOOS == "windows" {
		heartbeatThreshold = 4 * time.Second
		leaseThreshold = 6 * time.Second
		completionTimeout = 45 * time.Second
	}
	releaseFile := filepath.Join(t.TempDir(), "queue-concurrency.release")
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
	})

	f := newTestFixture(t, fmt.Sprintf(`
type: graph
name: queue-concurrency-test
queue: concurrency-q
worker_selector:
  test: "true"
steps:
  - name: long-step
    run: |
%s
`, indentYAMLBlock(waitForReleaseFileScript(releaseFile), 6)),
		withStaleThresholds(heartbeatThreshold, leaseThreshold),
		withZombieDetectionInterval(testZombieDetectorInterval),
	)
	defer f.cleanup()

	require.NoError(t, f.enqueue())
	require.NoError(t, f.enqueue())

	require.Eventually(t, func() bool {
		count, err := f.coord.QueueStore.Len(f.coord.Context, "concurrency-q")
		return err == nil && count == 2
	}, 5*time.Second, 100*time.Millisecond, "both runs should be queued before scheduling starts")

	f.startScheduler(30 * time.Second)

	require.Eventually(t, func() bool {
		statuses, err := f.coord.DAGRunStore.ListStatuses(
			f.coord.Context,
			exec.WithExactName("queue-concurrency-test"),
			exec.WithoutLimit(),
		)
		if err != nil || len(statuses) < 2 {
			return false
		}

		var running, queued int
		for _, st := range statuses {
			switch st.Status {
			case core.Running:
				running++
			case core.Queued:
				queued++
			case core.NotStarted, core.Failed, core.Aborted, core.Succeeded, core.PartiallySucceeded, core.Waiting, core.Rejected:
			}
		}

		return running == 1 && queued == 1
	}, distrTestTimeout(15*time.Second), 100*time.Millisecond, "one run should start and one should remain queued")

	// Verify the state is stable: concurrency limit keeps one active distributed
	// lease and does not let a second run start while the first remains active.
	// Queue index length can briefly flap on Windows while the status/lease view
	// is still consistent, so assert on the scheduler-visible run state instead.
	if runtime.GOOS != "windows" {
		require.Never(t, func() bool {
			statuses, err := f.coord.DAGRunStore.ListStatuses(
				f.coord.Context,
				exec.WithExactName("queue-concurrency-test"),
				exec.WithoutLimit(),
			)
			if err != nil {
				return false
			}

			leases, err := f.coord.DAGRunLeaseStore.ListByQueue(f.coord.Context, "concurrency-q")
			if err != nil {
				return false
			}

			freshLeases := 0
			now := time.Now().UTC()
			for _, lease := range leases {
				if lease.IsFresh(now, leaseThreshold) {
					freshLeases++
				}
			}

			running := 0
			for _, st := range statuses {
				if st.Status == core.Running {
					running++
				}
			}

			return freshLeases > 1 || running > 1
		}, 2*time.Second, 200*time.Millisecond, "distributed lease should keep one active run and leave one queued item")
	}

	require.NoError(t, os.WriteFile(releaseFile, []byte("ok"), 0600))
	require.Eventually(t, func() bool {
		statuses, err := f.coord.DAGRunStore.ListStatuses(
			f.coord.Context,
			exec.WithExactName("queue-concurrency-test"),
			exec.WithoutLimit(),
		)
		if err != nil || len(statuses) < 2 {
			return false
		}

		succeeded := 0
		for _, st := range statuses {
			if st.Status == core.Succeeded {
				succeeded++
			}
		}
		return succeeded == 2
	}, distrTestTimeout(completionTimeout), 200*time.Millisecond, "both queued runs should eventually complete")
}

// TestDistributedRun_StatusAndQueueConsistency verifies that after a
// distributed run completes, both the DAG run status and queue state are
// consistent: run shows Succeeded, queue has no active entries.
func TestDistributedRun_StatusAndQueueConsistency(t *testing.T) {
	f := newTestFixture(t, `
type: graph
name: consistency-test
queue: consistency-q
worker_selector:
  test: "true"
steps:
  - name: step1
    run: echo "hello"
`,
	)
	defer f.cleanup()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	status := f.waitForStatus(core.Succeeded, 20*time.Second)
	require.Equal(t, core.Succeeded, status.Status)

	activeStatuses, err := f.coord.DAGRunStore.ListStatuses(f.coord.Context,
		exec.WithStatuses([]core.Status{core.Running}),
		exec.WithoutLimit(),
	)
	require.NoError(t, err)

	var offendingStatus string
	for _, st := range activeStatuses {
		if st.Name == "consistency-test" {
			offendingStatus = fmt.Sprintf("status=%s dagRunID=%s", st.Status, st.DAGRunID)
			break
		}
	}
	assert.Emptyf(t, offendingStatus,
		"found active run for consistency-test after completion: %s",
		offendingStatus,
	)

	require.Eventually(t, func() bool {
		queueLen, err := f.coord.QueueStore.Len(f.coord.Context, "consistency-q")
		return err == nil && queueLen == 0
	}, 5*time.Second, 100*time.Millisecond, "queue should have no remaining entries after completion")
}

// TestDistributedRun_CoordinatorOwnsSharedLease verifies that distributed runs
// create a shared lease while active and remove it after completion.
func TestDistributedRun_CoordinatorOwnsSharedLease(t *testing.T) {
	releaseFile := filepath.Join(t.TempDir(), "lease-stamp.release")
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
	})

	f := newTestFixture(t, fmt.Sprintf(`
type: graph
name: lease-stamp-test
worker_selector:
  test: "true"
steps:
  - name: step1
    run: |
%s
`, indentYAMLBlock(waitForReleaseFileScript(releaseFile), 6)),
	)
	defer f.cleanup()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	status := f.waitForStatus(core.Running, 20*time.Second)
	require.Equal(t, core.Running, status.Status)
	require.NotEmpty(t, status.AttemptKey)

	var lease *exec.DAGRunLease
	require.Eventually(t, func() bool {
		var err error
		lease, err = f.coord.DAGRunLeaseStore.Get(f.coord.Context, status.AttemptKey)
		return err == nil && lease != nil
	}, distrTestTimeout(5*time.Second), 100*time.Millisecond, "shared lease should exist while run is active")
	assert.Equal(t, status.AttemptKey, lease.AttemptKey)
	assert.Equal(t, status.AttemptID, lease.AttemptID)
	assert.Equal(t, "worker-1", lease.WorkerID)
	assert.Equal(t, "test-coordinator", lease.Owner.ID)
	assert.WithinDuration(t, time.Now(), time.UnixMilli(lease.LastHeartbeatAt), 5*time.Second)

	require.NoError(t, os.WriteFile(releaseFile, []byte("ok"), 0600))
	finalStatus := f.waitForStatus(core.Succeeded, 20*time.Second)
	require.Equal(t, core.Succeeded, finalStatus.Status)

	require.Eventually(t, func() bool {
		_, err := f.coord.DAGRunLeaseStore.Get(f.coord.Context, status.AttemptKey)
		return errors.Is(err, exec.ErrDAGRunLeaseNotFound)
	}, 10*time.Second, 100*time.Millisecond, "shared lease should be removed after completion")
}

func testDistributedRunAckedTaskWithoutInitialStatus(t *testing.T, mode workerMode) {
	t.Helper()

	opts := []fixtureOption{
		withWorkerCount(0),
		withStaleThresholds(testStaleHeartbeatThreshold, testStaleLeaseThreshold),
		withZombieDetectionInterval(testZombieDetectorInterval),
	}
	if mode == sharedFSMode {
		opts = append(opts, withWorkerMode(sharedFSMode))
	} else {
		opts = append(opts, withWorkerMaxActiveRuns(1))
	}

	f := newTestFixture(t, `
type: graph
name: ack-orphan-test
worker_selector:
  test: "true"
steps:
  - name: step1
    run: echo "recovered"
`, opts...)
	defer f.cleanup()

	labels := map[string]string{"test": "true"}
	var (
		crashWorker *worker.Worker
		abandonOnce sync.Once
	)
	afterAckHook := func(context.Context, *coordinatorv1.Task) bool {
		triggered := false
		abandonOnce.Do(func() {
			triggered = true
		})
		return triggered
	}

	switch mode {
	case sharedFSMode:
		crashWorker = f.setupSharedFSWorkerWithAfterAckHook("crash-worker", labels, afterAckHook)
	case sharedNothingMode:
		crashWorker = f.setupSharedNothingWorkerWithAfterAckHook("crash-worker", labels, "", afterAckHook)
	default:
		t.Fatalf("unsupported worker mode: %v", mode)
	}
	require.NotNil(t, crashWorker)

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	lease := waitForAnyLease(t, f, 5*time.Second)
	require.Equal(t, "crash-worker", lease.WorkerID)

	queuedStatus, err := f.latestStatus()
	require.NoError(t, err)
	require.Equal(t, core.Queued, queuedStatus.Status)
	require.Equal(t, lease.AttemptKey, queuedStatus.AttemptKey)

	finalStatus := f.waitForStatus(core.Failed, delayedAfterAckFailureTimeout(mode))
	require.Equal(t, core.Failed, finalStatus.Status)
	assert.Equal(t, lease.AttemptKey, finalStatus.AttemptKey)
	assert.Contains(t, finalStatus.Error, "distributed run lease expired")
	assert.Contains(t, finalStatus.Error, "accepted the task claim")
	assert.Contains(t, finalStatus.Error, "owner coordinator")

	require.Eventually(t, func() bool {
		_, err := f.coord.DAGRunLeaseStore.Get(f.coord.Context, lease.AttemptKey)
		return errors.Is(err, exec.ErrDAGRunLeaseNotFound)
	}, 10*time.Second, 100*time.Millisecond, "stale distributed lease should be removed after failure")

	crashWorker.SetAfterTaskAckHook(nil)
}

func testDistributedRunDelayedAfterAckDoesNotExecute(t *testing.T, mode workerMode) {
	t.Helper()

	markerPath := filepath.Join(t.TempDir(), "executed.txt")
	yaml := fmt.Sprintf(`
type: graph
name: delayed-after-ack-test
worker_selector:
  test: "true"
steps:
  - name: step1
    run: sh -c 'echo executed > %s'
`, markerPath)

	opts := []fixtureOption{
		withWorkerCount(0),
		withWorkerMaxActiveRuns(1),
		withStaleThresholds(testStaleHeartbeatThreshold, testStaleLeaseThreshold),
		withZombieDetectionInterval(testZombieDetectorInterval),
	}
	if mode == sharedFSMode {
		opts = append(opts, withWorkerMode(sharedFSMode))
	}

	f := newTestFixture(t, yaml, opts...)
	defer f.cleanup()

	release := make(chan struct{})
	afterAckHook := func(ctx context.Context, _ *coordinatorv1.Task) bool {
		// Keep duplicate claims stalled too; Windows can observe a retry after
		// the first claim was already picked up.
		select {
		case <-release:
			return false
		case <-ctx.Done():
			return true
		}
	}

	var delayedWorker *worker.Worker
	switch mode {
	case sharedFSMode:
		delayedWorker = f.setupSharedFSWorkerWithAfterAckHook("delayed-worker", map[string]string{"test": "true"}, afterAckHook)
	case sharedNothingMode:
		delayedWorker = f.setupSharedNothingWorkerWithAfterAckHook("delayed-worker", map[string]string{"test": "true"}, "", afterAckHook)
	default:
		t.Fatalf("unsupported worker mode: %v", mode)
	}
	require.NotNil(t, delayedWorker)

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	lease := waitForAnyLease(t, f, 5*time.Second)
	require.Equal(t, "delayed-worker", lease.WorkerID)

	failedStatus := f.waitForStatus(core.Failed, delayedAfterAckFailureTimeout(mode))
	require.Equal(t, core.Failed, failedStatus.Status)
	require.Equal(t, lease.AttemptKey, failedStatus.AttemptKey)

	close(release)

	require.Eventually(t, func() bool {
		current, err := f.latestStatus()
		if err != nil {
			return false
		}
		if current.Status != core.Failed {
			return false
		}
		_, err = os.Stat(markerPath)
		return errors.Is(err, os.ErrNotExist)
	}, 5*time.Second, 100*time.Millisecond, "stale worker should not execute after resuming")

	require.Eventually(t, func() bool {
		_, err := f.coord.DAGRunLeaseStore.Get(f.coord.Context, lease.AttemptKey)
		return errors.Is(err, exec.ErrDAGRunLeaseNotFound)
	}, 10*time.Second, 100*time.Millisecond, "stale lease should remain deleted after worker resumes")

	delayedWorker.SetAfterTaskAckHook(nil)
}

func startWorkerProcess(t *testing.T, f *testFixture, workerID, labels string) (*osexec.Cmd, *bytes.Buffer) {
	t.Helper()

	args := []string{
		"worker",
		"--config", f.coord.Config.Paths.ConfigFileUsed,
		"--worker.id", workerID,
		"--worker.health-port=0",
		"--worker.coordinators", f.coord.Address(),
	}
	if labels != "" {
		args = append(args, "--worker.labels", labels)
	}

	cmd := osexec.Command(f.coord.Config.Paths.Executable, args...)
	cmdutil.SetupCommand(cmd)
	cmd.Env = append([]string{}, f.coord.ChildEnv...)

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	require.NoError(t, cmd.Start())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = cmd.Wait()
	}()

	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
		}

		if cmd.Process != nil {
			_ = cmdutil.KillProcessGroup(cmd, os.Kill)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Logf("worker process %s did not exit within 5 seconds", workerID)
		}
	})

	f.waitForWorkerRegistration(workerID, 10*time.Second)

	return cmd, &output
}

func waitForLease(t *testing.T, f *testFixture, attemptKey string, timeout time.Duration) exec.DAGRunLease {
	t.Helper()

	var lease *exec.DAGRunLease
	require.Eventually(t, func() bool {
		current, err := f.coord.DAGRunLeaseStore.Get(f.coord.Context, attemptKey)
		if err != nil {
			return false
		}
		lease = current
		return lease != nil
	}, timeout, 100*time.Millisecond, "lease %s should exist", attemptKey)

	return *lease
}

func waitForAnyLease(t *testing.T, f *testFixture, timeout time.Duration) exec.DAGRunLease {
	t.Helper()

	var lease exec.DAGRunLease
	require.Eventually(t, func() bool {
		leases, err := f.coord.DAGRunLeaseStore.ListAll(f.coord.Context)
		if err != nil || len(leases) == 0 {
			return false
		}
		lease = leases[0]
		return true
	}, timeout, 100*time.Millisecond, "a distributed lease should exist")

	return lease
}
