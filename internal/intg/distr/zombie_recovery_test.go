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

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/worker"
	"github.com/dagucloud/dagu/v2/internal/test/intgharness"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testStaleHeartbeatThreshold = 2 * time.Second
	testStaleLeaseThreshold     = 3 * time.Second
	testZombieDetectorInterval  = 500 * time.Millisecond
)

func delayedAfterAckFailureTimeout() time.Duration {
	if runtime.GOOS == "windows" && raceEnabled() {
		return 30 * time.Second
	}
	return 20 * time.Second
}

func waitForReleaseFileScript(path string) string {
	return intgharness.PortableCommands().WaitForFile(path)
}

// TestDistributedRun_WorkerCrash_MarkedFailed verifies that a hard-killed
// worker causes its claim-bound parent and inline sub-DAG to fail.
func TestDistributedRun_WorkerCrash_MarkedFailed(t *testing.T) {
	heartbeatThreshold := testStaleHeartbeatThreshold
	leaseThreshold := testStaleLeaseThreshold
	runningTimeout := 15 * time.Second
	failureTimeout := 20 * time.Second
	schedulerTimeout := 45 * time.Second
	if runtime.GOOS == "windows" {
		heartbeatThreshold = 12 * time.Second
		leaseThreshold = 20 * time.Second
		runningTimeout = 30 * time.Second
		failureTimeout = 90 * time.Second
		schedulerTimeout = 90 * time.Second
	}

	releaseFile := filepath.Join(t.TempDir(), "worker-crash.release")
	f := newTestFixture(t, fmt.Sprintf(`
name: worker-crash-parent
worker_selector:
  test: "true"
steps:
  - name: call-child
    call: worker-crash-child
---
name: worker-crash-child
steps:
  - name: wait
    command: |
%s
`, indentYAMLBlock(waitForReleaseFileScript(releaseFile), 6)),
		withWorkerCount(0),
		withStaleThresholds(heartbeatThreshold, leaseThreshold),
		withZombieDetectionInterval(testZombieDetectorInterval),
	)
	workerCmd, _ := startWorkerProcess(t, f, "crash-worker", "test=true")
	defer func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0o600)
		_ = cmdutil.TerminateProcessGroup(workerCmd, cmdutil.ForceTermination())
		f.cleanup()
	}()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(schedulerTimeout)

	var status dagrun.DAGRunStatus
	var subRuns []dagrun.DAGRunStatus
	require.Eventually(t, func() bool {
		var ok bool
		status, subRuns, ok = readRunningInlineSubDAGs(f, 1)
		return ok
	}, distrTestTimeout(runningTimeout), 100*time.Millisecond, "inline sub-DAG should be running before its worker stops")
	require.Equal(t, "crash-worker", status.WorkerID)
	lease := waitForLease(t, f, status.AttemptKey, 5*time.Second)
	require.Equal(t, "crash-worker", lease.WorkerID)

	require.NoError(t, cmdutil.TerminateProcessGroup(workerCmd, cmdutil.ForceTermination()))

	expectedReason := dispatch.DistributedLeaseExpiredReason("crash-worker")
	rootRef := dagrun.NewDAGRunRef(status.Name, status.DAGRunID)
	childStatus := waitForSubDAGRunStatus(t, f, rootRef, subRuns[0].DAGRunID, ir.Failed, failureTimeout)
	require.Equal(t, expectedReason, childStatus.Error)

	finalStatus := f.waitForStatus(ir.Failed, failureTimeout)
	require.Equal(t, expectedReason, finalStatus.Error)
}

func TestDistributedRun_AckedTaskWithoutInitialStatus_MarkedFailedAndCleansLease(t *testing.T) {
	testDistributedRunAckedTaskWithoutInitialStatus(t)
}

func TestDistributedRun_DelayedAfterAck_DoesNotExecuteAfterStaleCleanup(t *testing.T) {
	testDistributedRunDelayedAfterAckDoesNotExecute(t)
}

func TestDistributedSubDAG_StaleLeaseMarkedFailedAndCleansLease(t *testing.T) {
	testDistributedSubDAGStaleLeaseMarkedFailedAndCleansLease(t)
}

// TestDistributedRun_HeartbeatRefreshKeepsQuietRunAlive verifies that inline
// sub-DAGs remain running past the lease threshold while their claim is alive.
func TestDistributedRun_HeartbeatRefreshKeepsQuietRunAlive(t *testing.T) {
	heartbeatThreshold := testStaleHeartbeatThreshold
	leaseThreshold := testStaleLeaseThreshold
	freshWindow := 2 * time.Second
	leaseObservationWindow := leaseThreshold + time.Second
	finalStatusTimeout := 15 * time.Second
	runningTimeout := 15 * time.Second
	heartbeatRefreshTimeout := 10 * time.Second
	schedulerTimeout := 30 * time.Second
	if runtime.GOOS == "windows" {
		heartbeatThreshold = 12 * time.Second
		leaseThreshold = 20 * time.Second
		freshWindow = leaseThreshold + 5*time.Second
		leaseObservationWindow = leaseThreshold + 3*time.Second
		finalStatusTimeout = 90 * time.Second
		runningTimeout = 30 * time.Second
		heartbeatRefreshTimeout = 30 * time.Second
		schedulerTimeout = 90 * time.Second
	}
	releaseFile := filepath.Join(t.TempDir(), "quiet-heartbeat.release")
	f := newTestFixture(t, fmt.Sprintf(`
name: quiet-heartbeat-parent
worker_selector:
  test: "true"
steps:
  - name: call-child-a
    call: quiet-heartbeat-child-a
  - name: call-child-b
    call: quiet-heartbeat-child-b
---
name: quiet-heartbeat-child-a
steps:
  - name: wait
    command: |
%s
---
name: quiet-heartbeat-child-b
steps:
  - name: wait
    command: |
%s
`, indentYAMLBlock(waitForReleaseFileScript(releaseFile), 6), indentYAMLBlock(waitForReleaseFileScript(releaseFile), 6)),
		withStaleThresholds(heartbeatThreshold, leaseThreshold),
		withZombieDetectionInterval(testZombieDetectorInterval),
	)
	defer func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0o600)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			status, err := f.latestStatus()
			if err == nil && !status.Status.IsActive() {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		f.cleanup()
	}()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(schedulerTimeout)

	var status dagrun.DAGRunStatus
	var subRuns []dagrun.DAGRunStatus
	require.Eventually(t, func() bool {
		var ok bool
		status, subRuns, ok = readRunningInlineSubDAGs(f, 2)
		return ok
	}, distrTestTimeout(runningTimeout), 100*time.Millisecond, "both inline sub-DAGs should be running")
	require.Len(t, subRuns, 2)

	initialLease := waitForLease(t, f, status.AttemptKey, 5*time.Second).LastHeartbeatAt

	var lease dispatch.DAGRunLease
	require.Eventually(t, func() bool {
		currentStatus, _, ok := readRunningInlineSubDAGs(f, 2)
		if !ok || currentStatus.AttemptKey != status.AttemptKey {
			return false
		}
		currentLease, err := f.coord.DAGRunLeaseStore.Get(f.coord.Context, status.AttemptKey)
		if err != nil || currentLease == nil || currentLease.LastHeartbeatAt <= initialLease {
			return false
		}
		status = currentStatus
		lease = *currentLease
		return true
	}, distrTestTimeout(heartbeatRefreshTimeout), 100*time.Millisecond, "heartbeat should refresh while inline sub-DAGs remain active")

	assert.Greater(t, lease.LastHeartbeatAt, initialLease)
	assert.WithinDuration(t, time.Now(), time.UnixMilli(lease.LastHeartbeatAt), freshWindow)

	time.Sleep(leaseObservationWindow)
	status, subRuns, ok := readRunningInlineSubDAGs(f, 2)
	require.True(t, ok, "inline sub-DAGs should remain active beyond the stale threshold")
	require.Len(t, subRuns, 2)
	lease = waitForLease(t, f, status.AttemptKey, 5*time.Second)

	require.NoError(t, os.WriteFile(releaseFile, []byte("ok"), 0o600))
	finalStatus := f.waitForStatus(ir.Succeeded, finalStatusTimeout)
	assert.Equal(t, ir.Succeeded, finalStatus.Status)
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
			dagrun.WithExactName("queue-concurrency-test"),
			dagrun.WithoutLimit(),
		)
		if err != nil || len(statuses) < 2 {
			return false
		}

		var running, queued int
		for _, st := range statuses {
			switch st.Status {
			case ir.Running:
				running++
			case ir.Queued:
				queued++
			case ir.NotStarted, ir.Failed, ir.Aborted, ir.Succeeded, ir.PartiallySucceeded, ir.Waiting, ir.Rejected:
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
				dagrun.WithExactName("queue-concurrency-test"),
				dagrun.WithoutLimit(),
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
				if st.Status == ir.Running {
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
			dagrun.WithExactName("queue-concurrency-test"),
			dagrun.WithoutLimit(),
		)
		if err != nil || len(statuses) < 2 {
			return false
		}

		succeeded := 0
		for _, st := range statuses {
			if st.Status == ir.Succeeded {
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

	status := f.waitForStatus(ir.Succeeded, 20*time.Second)
	require.Equal(t, ir.Succeeded, status.Status)

	activeStatuses, err := f.coord.DAGRunStore.ListStatuses(f.coord.Context,
		dagrun.WithStatuses([]ir.Status{ir.Running}),
		dagrun.WithoutLimit(),
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

	status := f.waitForStatus(ir.Running, 20*time.Second)
	require.Equal(t, ir.Running, status.Status)
	require.NotEmpty(t, status.AttemptKey)

	var lease *dispatch.DAGRunLease
	require.Eventually(t, func() bool {
		var err error
		lease, err = f.coord.DAGRunLeaseStore.Get(f.coord.Context, status.AttemptKey)
		return err == nil && lease != nil
	}, distrTestTimeout(5*time.Second), 100*time.Millisecond, "shared lease should exist while run is active")
	leaseObservedAt := time.Now()
	assert.Equal(t, status.AttemptKey, lease.AttemptKey)
	assert.Equal(t, status.AttemptID, lease.AttemptID)
	assert.Equal(t, "worker-1", lease.WorkerID)
	assert.Equal(t, "test-coordinator", lease.Owner.ID)
	assert.WithinDuration(t, leaseObservedAt, time.UnixMilli(lease.LastHeartbeatAt), distrTestTimeout(5*time.Second))

	require.NoError(t, os.WriteFile(releaseFile, []byte("ok"), 0600))
	finalStatus := f.waitForStatus(ir.Succeeded, 20*time.Second)
	require.Equal(t, ir.Succeeded, finalStatus.Status)

	require.Eventually(t, func() bool {
		_, err := f.coord.DAGRunLeaseStore.Get(f.coord.Context, status.AttemptKey)
		return errors.Is(err, dispatch.ErrDAGRunLeaseNotFound)
	}, distrTestTimeout(10*time.Second), 100*time.Millisecond, "shared lease should be removed after completion")
}

func testDistributedRunAckedTaskWithoutInitialStatus(t *testing.T) {
	t.Helper()

	opts := []fixtureOption{
		withWorkerCount(0),
		withWorkerMaxActiveRuns(1),
		withStaleThresholds(testStaleHeartbeatThreshold, testStaleLeaseThreshold),
		withZombieDetectionInterval(testZombieDetectorInterval),
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
		crashWorker   *worker.Worker
		abandonedTask = make(chan *coordinatorv1.Task, 1)
	)
	afterAckHook := func(ctx context.Context, task *coordinatorv1.Task) bool {
		select {
		case abandonedTask <- task:
		default:
		}
		<-ctx.Done()
		return true
	}

	crashWorker = f.setupWorkerWithAfterAckHook("crash-worker", labels, "", afterAckHook)
	require.NotNil(t, crashWorker)

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	var task *coordinatorv1.Task
	select {
	case task = <-abandonedTask:
	case <-time.After(distrTestTimeout(15 * time.Second)):
		t.Fatal("timed out waiting for worker to accept and abandon task")
	}
	require.NotNil(t, task)
	require.Equal(t, "ack-orphan-test", task.Target)

	lease := waitForAnyLease(t, f, 5*time.Second)
	require.Equal(t, "crash-worker", lease.WorkerID)
	require.Equal(t, lease.AttemptKey, task.AttemptKey)

	queuedStatus, err := f.latestStatus()
	require.NoError(t, err)
	require.Equal(t, ir.Queued, queuedStatus.Status)
	require.Equal(t, lease.AttemptKey, queuedStatus.AttemptKey)

	stopCtx, cancel := context.WithTimeout(context.Background(), distrTestTimeout(5*time.Second))
	defer cancel()
	require.NoError(t, crashWorker.Stop(stopCtx))

	finalStatus := f.waitForStatus(ir.Failed, delayedAfterAckFailureTimeout())
	require.Equal(t, ir.Failed, finalStatus.Status)
	assert.Equal(t, lease.AttemptKey, finalStatus.AttemptKey)
	assert.Contains(t, finalStatus.Error, "distributed run lease expired")
	assert.Contains(t, finalStatus.Error, "accepted the task claim")
	assert.Contains(t, finalStatus.Error, "owner coordinator")

	require.Eventually(t, func() bool {
		_, err := f.coord.DAGRunLeaseStore.Get(f.coord.Context, lease.AttemptKey)
		return errors.Is(err, dispatch.ErrDAGRunLeaseNotFound)
	}, 10*time.Second, 100*time.Millisecond, "stale distributed lease should be removed after failure")

	crashWorker.SetAfterTaskAckHook(nil)
}

func testDistributedRunDelayedAfterAckDoesNotExecute(t *testing.T) {
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

	delayedWorker := f.setupWorkerWithAfterAckHook("delayed-worker", map[string]string{"test": "true"}, "", afterAckHook)
	require.NotNil(t, delayedWorker)

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	lease := waitForAnyLease(t, f, 5*time.Second)
	require.Equal(t, "delayed-worker", lease.WorkerID)

	failedStatus := f.waitForStatus(ir.Failed, delayedAfterAckFailureTimeout())
	require.Equal(t, ir.Failed, failedStatus.Status)
	require.Equal(t, lease.AttemptKey, failedStatus.AttemptKey)

	close(release)

	require.Eventually(t, func() bool {
		current, err := f.latestStatus()
		if err != nil {
			return false
		}
		if current.Status != ir.Failed {
			return false
		}
		_, err = os.Stat(markerPath)
		return errors.Is(err, os.ErrNotExist)
	}, 5*time.Second, 100*time.Millisecond, "stale worker should not execute after resuming")

	require.Eventually(t, func() bool {
		_, err := f.coord.DAGRunLeaseStore.Get(f.coord.Context, lease.AttemptKey)
		return errors.Is(err, dispatch.ErrDAGRunLeaseNotFound)
	}, 10*time.Second, 100*time.Millisecond, "stale lease should remain deleted after worker resumes")

	delayedWorker.SetAfterTaskAckHook(nil)
}

func testDistributedSubDAGStaleLeaseMarkedFailedAndCleansLease(t *testing.T) {
	t.Helper()

	opts := []fixtureOption{
		withWorkerCount(0),
		withWorkerMaxActiveRuns(1),
		withStaleThresholds(testStaleHeartbeatThreshold, testStaleLeaseThreshold),
		withZombieDetectionInterval(testZombieDetectorInterval),
	}

	f := newTestFixture(t, `
name: stale-subdag-parent
steps:
  - name: call-child
    call: stale-subdag-child
---
name: stale-subdag-child
worker_selector:
  test: "true"
steps:
  - name: child-step
    command: echo "should not execute"
`, opts...)
	defer f.cleanup()

	abandonedTask := make(chan *coordinatorv1.Task, 1)
	abandonOnce := sync.Once{}
	afterAckHook := func(_ context.Context, task *coordinatorv1.Task) bool {
		triggered := false
		abandonOnce.Do(func() {
			triggered = true
			abandonedTask <- task
		})
		return triggered
	}

	crashWorker := f.setupWorkerWithAfterAckHook("subdag-crash-worker", map[string]string{"test": "true"}, "", afterAckHook)
	require.NotNil(t, crashWorker)
	defer crashWorker.SetAfterTaskAckHook(nil)

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(45 * time.Second)

	var task *coordinatorv1.Task
	select {
	case task = <-abandonedTask:
	case <-time.After(distrTestTimeout(15 * time.Second)):
		t.Fatal("timed out waiting for worker to accept and abandon sub-DAG task")
	}
	require.NotNil(t, task)
	require.Equal(t, "stale-subdag-child", task.Target)
	require.NotEmpty(t, task.AttemptKey)
	require.NotEmpty(t, task.AttemptId)
	require.NotEmpty(t, task.RootDagRunName)
	require.NotEmpty(t, task.RootDagRunId)
	require.NotEmpty(t, task.DagRunId)

	rootRef := dagrun.NewDAGRunRef(task.RootDagRunName, task.RootDagRunId)
	subRunRef := dagrun.NewDAGRunRef(task.Target, task.DagRunId)
	lease := waitForLease(t, f, task.AttemptKey, 5*time.Second)
	require.Equal(t, subRunRef, lease.DAGRun)
	require.Equal(t, rootRef, lease.Root)
	require.Equal(t, task.AttemptId, lease.AttemptID)

	var initialSubStatus dagrun.DAGRunStatus
	require.Eventually(t, func() bool {
		subStatus, err := readSubDAGRunStatus(f, rootRef, subRunRef.ID)
		if err != nil || subStatus == nil {
			return false
		}
		if subStatus.AttemptKey != task.AttemptKey {
			return false
		}
		initialSubStatus = *subStatus
		return subStatus.Status == ir.Queued ||
			subStatus.Status == ir.NotStarted ||
			subStatus.Status == ir.Running
	}, distrTestTimeout(5*time.Second), 100*time.Millisecond, "sub-DAG attempt should be persisted before stale lease cleanup")
	require.Equal(t, rootRef, initialSubStatus.Root)

	finalSubStatus := waitForSubDAGRunStatus(t, f, rootRef, subRunRef.ID, ir.Failed, 25*time.Second)
	expectedReason := dispatch.DistributedLeaseExpiredReason("subdag-crash-worker")
	require.Equal(t, task.AttemptKey, finalSubStatus.AttemptKey)
	require.Equal(t, task.AttemptId, finalSubStatus.AttemptID)
	require.Equal(t, expectedReason, finalSubStatus.Error)
	if len(finalSubStatus.Nodes) > 0 {
		require.Equal(t, ir.NodeFailed, finalSubStatus.Nodes[0].Status)
		require.Equal(t, expectedReason, finalSubStatus.Nodes[0].Error)
	}

	finalParentStatus := f.waitForStatus(ir.Failed, 15*time.Second)
	require.NotEmpty(t, finalParentStatus.Nodes)
	require.Equal(t, ir.NodeFailed, finalParentStatus.Nodes[0].Status)
	require.Len(t, finalParentStatus.Nodes[0].SubRuns, 1)
	require.Equal(t, subRunRef.ID, finalParentStatus.Nodes[0].SubRuns[0].DAGRunID)

	require.Eventually(t, func() bool {
		_, err := f.coord.DAGRunLeaseStore.Get(f.coord.Context, task.AttemptKey)
		return errors.Is(err, dispatch.ErrDAGRunLeaseNotFound)
	}, 10*time.Second, 100*time.Millisecond, "stale sub-DAG lease should be removed after failure")

	require.Eventually(t, func() bool {
		_, err := f.coord.ActiveDistributedRunStore.Get(f.coord.Context, task.AttemptKey)
		return errors.Is(err, dispatch.ErrActiveRunNotFound)
	}, 10*time.Second, 100*time.Millisecond, "stale sub-DAG active-run index should be removed after failure")
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
			_ = cmdutil.TerminateProcessGroup(cmd, cmdutil.ForceTermination())
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

func waitForLease(t *testing.T, f *testFixture, attemptKey string, timeout time.Duration) dispatch.DAGRunLease {
	t.Helper()

	timeout = distrTestTimeout(timeout)

	var lease *dispatch.DAGRunLease
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

func readRunningInlineSubDAGs(f *testFixture, expected int) (dagrun.DAGRunStatus, []dagrun.DAGRunStatus, bool) {
	status, err := f.latestStatus()
	if err != nil || status.Status != ir.Running || status.AttemptKey == "" || len(status.Nodes) != expected {
		return dagrun.DAGRunStatus{}, nil, false
	}

	rootRef := dagrun.NewDAGRunRef(status.Name, status.DAGRunID)
	subRuns := make([]dagrun.DAGRunStatus, 0, expected)
	for _, node := range status.Nodes {
		if node == nil || node.Status != ir.NodeRunning || len(node.SubRuns) != 1 {
			return dagrun.DAGRunStatus{}, nil, false
		}
		subStatus, err := readSubDAGRunStatus(f, rootRef, node.SubRuns[0].DAGRunID)
		if err != nil || subStatus == nil || subStatus.Status != ir.Running ||
			subStatus.AttemptKey == "" || subStatus.ClaimKey != status.AttemptKey {
			return dagrun.DAGRunStatus{}, nil, false
		}
		subRuns = append(subRuns, *subStatus)
	}

	return status, subRuns, true
}

func waitForSubDAGRunStatus(
	t *testing.T,
	f *testFixture,
	rootRef dagrun.DAGRunRef,
	subRunID string,
	expected ir.Status,
	timeout time.Duration,
) dagrun.DAGRunStatus {
	t.Helper()

	timeout = distrTestTimeout(timeout)
	var status *dagrun.DAGRunStatus
	var schedulerErr error
	require.Eventually(t, func() bool {
		schedulerErr = f.pollSchedulerErr()
		if schedulerErr != nil {
			return true
		}
		var err error
		status, err = readSubDAGRunStatus(f, rootRef, subRunID)
		return err == nil && status != nil && status.Status == expected
	}, timeout, 100*time.Millisecond, "timeout waiting for sub-DAG %s status %s", subRunID, expected)
	require.NoError(t, schedulerErr)
	require.NotNil(t, status)

	return *status
}

func readSubDAGRunStatus(
	f *testFixture,
	rootRef dagrun.DAGRunRef,
	subRunID string,
) (*dagrun.DAGRunStatus, error) {
	attempt, err := f.coord.DAGRunStore.FindSubAttempt(f.coord.Context, rootRef, subRunID)
	if err != nil {
		return nil, err
	}
	return attempt.ReadStatus(f.coord.Context)
}

func waitForAnyLease(t *testing.T, f *testFixture, timeout time.Duration) dispatch.DAGRunLease {
	t.Helper()

	timeout = distrTestTimeout(timeout)

	var lease dispatch.DAGRunLease
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
