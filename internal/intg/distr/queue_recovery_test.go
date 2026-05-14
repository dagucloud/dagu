// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/require"
)

func TestExecution_QueuedDispatch_ConsumesOneThousandItems(t *testing.T) {
	const (
		queueName = "bulk-q"
	)
	totalRuns := queuedDispatchBulkRunCount()

	f := newTestFixture(t, `
type: graph
name: shared-nothing-bulk-queue-test
queue: bulk-q
worker_selector:
  tier: "queue"
steps:
  - name: step1
    run: echo "executed"
`,
		withWorkerCount(queuedDispatchWorkerCount()),
		withWorkerMaxActiveRuns(queuedDispatchWorkerMaxActiveRuns()),
		withLabels(map[string]string{"tier": "queue"}),
		withConfigMutator(func(c *config.Config) {
			c.Queues.Enabled = true
			c.Queues.Config = []config.QueueConfig{{
				Name:          queueName,
				MaxActiveRuns: 100,
			}}
		}),
	)
	defer f.cleanup()

	for range totalRuns {
		require.NoError(t, f.enqueueDirect())
	}

	requireQueuedItemCountEventually(t, f, totalRuns, 60*time.Second)

	f.startScheduler(30 * time.Second)

	requireAllQueuedRunsConsumed(t, f, totalRuns, queuedDispatchBulkTimeout())
}

func queuedDispatchBulkRunCount() int {
	if runtime.GOOS == "windows" {
		return 5
	}
	return 1000
}

func queuedDispatchWorkerCount() int {
	if runtime.GOOS == "windows" {
		return 2
	}
	return 10
}

func queuedDispatchWorkerMaxActiveRuns() int {
	if runtime.GOOS == "windows" {
		return 1
	}
	return 10
}

func queuedDispatchBulkTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 5 * time.Minute
	}
	return 10 * time.Minute
}

func TestExecution_QueuedDispatch_RecoversWhenWorkerRegistersLater(t *testing.T) {
	f := newTestFixture(t, `
type: graph
name: shared-nothing-late-worker-test
worker_selector:
  tier: "queue"
steps:
  - name: step1
    run: echo "executed"
`, withWorkerCount(0), withWorkerMaxActiveRuns(1))
	defer f.cleanup()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	requireQueuedRunStillPending(t, f, 2*time.Second)

	f.setupSharedNothingWorker("late-worker", map[string]string{"tier": "queue"}, "")

	status := f.waitForStatus(core.Succeeded, 20*time.Second)
	require.Equal(t, "late-worker", status.WorkerID)
	requireQueueEventuallyEmpty(t, f)
}

func TestExecution_QueuedDispatch_RecoversWhenMatchingWorkerRegistersLater(t *testing.T) {
	f := newTestFixture(t, `
type: graph
name: shared-nothing-selector-recovery-test
worker_selector:
  tier: "queue"
steps:
  - name: step1
    run: echo "executed"
`, withWorkerCount(0), withWorkerMaxActiveRuns(1))
	defer f.cleanup()

	f.setupSharedNothingWorker("mismatched-worker", map[string]string{"tier": "other"}, "")

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	requireQueuedRunStillPending(t, f, 2*time.Second)

	f.setupSharedNothingWorker("matching-worker", map[string]string{"tier": "queue"}, "")

	status := f.waitForStatus(core.Succeeded, 20*time.Second)
	require.Equal(t, "matching-worker", status.WorkerID)
	requireQueueEventuallyEmpty(t, f)
}

func requireQueuedRunStillPending(t *testing.T, f *testFixture, duration time.Duration) {
	t.Helper()

	require.Eventually(t, func() bool {
		status, err := f.latestStatus()
		if err != nil {
			return false
		}
		if status.Status != core.Queued {
			return false
		}
		count, err := queuedItemCount(f)
		if err != nil {
			return false
		}
		return count == 1
	}, 10*time.Second, 100*time.Millisecond, "run should remain queued while no usable worker exists")

	require.Never(t, func() bool {
		status, err := f.latestStatus()
		if err != nil {
			return false
		}
		count, err := queuedItemCount(f)
		if err != nil {
			return false
		}
		return status.Status != core.Queued || count != 1
	}, duration, 100*time.Millisecond, "queued run should not disappear before a matching worker is available")
}

func requireQueueEventuallyEmpty(t *testing.T, f *testFixture) {
	t.Helper()

	requireQueuedItemCountEventually(t, f, 0, 10*time.Second)
}

func queuedItemCount(f *testFixture) (int, error) {
	items, err := f.coord.QueueStore.ListByDAGName(f.coord.Context, f.dagWrapper.ProcGroup(), f.dagWrapper.Name)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

func requireQueuedItemCountEventually(t *testing.T, f *testFixture, expected int, timeout time.Duration) {
	t.Helper()

	require.Eventually(t, func() bool {
		count, err := queuedItemCount(f)
		return err == nil && count == expected
	}, timeout, 100*time.Millisecond, "expected %d queued items", expected)
}

func requireAllQueuedRunsConsumed(t *testing.T, f *testFixture, expectedRuns int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	lastSummary := "no status collected"
	for time.Now().Before(deadline) {
		counts, total, err := dagRunStatusCounts(f)
		if err != nil {
			lastSummary = err.Error()
			time.Sleep(time.Second)
			continue
		}
		queueCount, err := queuedItemCount(f)
		if err != nil {
			lastSummary = err.Error()
			time.Sleep(time.Second)
			continue
		}

		lastSummary = formatStatusSummary(counts, total, queueCount)

		if total == expectedRuns &&
			queueCount == 0 &&
			counts[core.Succeeded] == expectedRuns &&
			counts[core.Queued] == 0 &&
			counts[core.Running] == 0 &&
			counts[core.NotStarted] == 0 &&
			counts[core.Failed] == 0 &&
			counts[core.Aborted] == 0 &&
			counts[core.Waiting] == 0 &&
			counts[core.Rejected] == 0 &&
			counts[core.PartiallySucceeded] == 0 {
			return
		}

		time.Sleep(time.Second)
	}

	t.Fatalf("expected %d queued runs to be fully consumed; last state: %s", expectedRuns, lastSummary)
}

func dagRunStatusCounts(f *testFixture) (map[core.Status]int, int, error) {
	statuses, err := f.coord.DAGRunStore.ListStatuses(
		f.coord.Context,
		exec.WithExactName(f.dagWrapper.Name),
		exec.WithoutLimit(),
	)
	if err != nil {
		return nil, 0, err
	}

	counts := make(map[core.Status]int)
	for _, status := range statuses {
		if status == nil {
			continue
		}
		counts[status.Status]++
	}

	return counts, len(statuses), nil
}

func formatStatusSummary(counts map[core.Status]int, total, queueCount int) string {
	return "total=" + itoa(total) +
		" queue=" + itoa(queueCount) +
		" succeeded=" + itoa(counts[core.Succeeded]) +
		" queued=" + itoa(counts[core.Queued]) +
		" running=" + itoa(counts[core.Running]) +
		" not_started=" + itoa(counts[core.NotStarted]) +
		" failed=" + itoa(counts[core.Failed]) +
		" aborted=" + itoa(counts[core.Aborted]) +
		" waiting=" + itoa(counts[core.Waiting]) +
		" rejected=" + itoa(counts[core.Rejected]) +
		" partial=" + itoa(counts[core.PartiallySucceeded])
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
