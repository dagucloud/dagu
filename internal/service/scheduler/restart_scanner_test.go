// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

func TestDecideRestart(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	wait := 15 * time.Second
	finished := func(status ir.Status, ago time.Duration) *ir.DAGRunStatus {
		return &ir.DAGRunStatus{
			DAGRunID:   "run-1",
			Status:     status,
			FinishedAt: now.Add(-ago).Format(time.RFC3339),
		}
	}

	tests := []struct {
		name        string
		latest      *ir.DAGRunStatus
		start       bool
		reason      string
		previousEnd time.Time
		stuck       bool
	}{
		{name: "FirstRun", latest: &ir.DAGRunStatus{}, start: true, reason: "first_run"},
		{
			name:   "WaitElapsed",
			latest: finished(ir.Failed, 20*time.Second),
			start:  true, reason: "previous_run_ended", previousEnd: now.Add(-20 * time.Second),
		},
		{
			name:   "WaitJustElapsed",
			latest: finished(ir.Succeeded, wait+time.Second),
			start:  true, reason: "previous_run_ended", previousEnd: now.Add(-wait - time.Second),
		},
		// The end is stored to the second, so the run may have ended up to a
		// second after it.
		{name: "WaitWithinTheEndSecond", latest: finished(ir.Succeeded, wait), reason: "waiting"},
		{name: "Waiting", latest: finished(ir.Failed, 5*time.Second), reason: "waiting"},
		{
			name:   "AfterOutage",
			latest: finished(ir.Failed, 3*time.Hour),
			start:  true, reason: "previous_run_ended", previousEnd: now.Add(-3 * time.Hour),
		},
		{name: "Running", latest: finished(ir.Running, 0), reason: "run_active"},
		{name: "Queued", latest: finished(ir.Queued, 0), reason: "run_active"},
		{name: "WaitingApproval", latest: finished(ir.Waiting, 0), reason: "run_active"},
		{
			name:   "NotStarted",
			latest: &ir.DAGRunStatus{DAGRunID: "run-1", Status: ir.NotStarted, StartedAt: now.Add(-time.Minute).Format(time.RFC3339)},
			reason: "run_not_started",
		},
		// A run that does not start within the timeout may have nothing left to
		// start it.
		{
			name:   "LeftNotStarted",
			latest: &ir.DAGRunStatus{DAGRunID: "run-1", Status: ir.NotStarted, StartedAt: now.Add(-restartNotStartedTimeout).Format(time.RFC3339)},
			start:  true, reason: "run_never_started", stuck: true,
		},
		{
			name:   "NotStartedWithoutTime",
			latest: &ir.DAGRunStatus{DAGRunID: "run-1", Status: ir.NotStarted},
			start:  true, reason: "run_never_started", stuck: true,
		},
		{
			name:   "LeftQueued",
			latest: &ir.DAGRunStatus{DAGRunID: "run-1", Status: ir.Queued, CreatedAt: now.Add(-restartNotStartedTimeout).UnixMilli()},
			start:  true, reason: "run_never_started", stuck: true,
		},
		{
			name:   "Aborted",
			latest: finished(ir.Aborted, 20*time.Second),
			start:  true, reason: "previous_run_ended", previousEnd: now.Add(-20 * time.Second),
		},
		{
			name:   "Rejected",
			latest: finished(ir.Rejected, 20*time.Second),
			start:  true, reason: "previous_run_ended", previousEnd: now.Add(-20 * time.Second),
		},
		{
			name:   "PartiallySucceeded",
			latest: finished(ir.PartiallySucceeded, 20*time.Second),
			start:  true, reason: "previous_run_ended", previousEnd: now.Add(-20 * time.Second),
		},
		{
			name:   "EndUnknown",
			latest: &ir.DAGRunStatus{DAGRunID: "run-1", Status: ir.Failed},
			start:  true, reason: "previous_run_end_unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := decideRestart(wait, tt.latest, now)
			assert.Equal(t, tt.start, got.start)
			assert.Equal(t, tt.reason, got.reason)
			assert.True(t, tt.previousEnd.Equal(got.previousEnd), "previousEnd = %v, want %v", got.previousEnd, tt.previousEnd)
			assert.Equal(t, tt.stuck, got.stuck)
		})
	}
}

func TestGenerateRestartRunID(t *testing.T) {
	t.Parallel()

	ended := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

	id := GenerateRestartRunID("svc", "run-1", ended)
	assert.Equal(t, id, GenerateRestartRunID("svc", "run-1", ended), "same previous run, same ID")
	assert.NotEqual(t, id, GenerateRestartRunID("svc", "run-2", ended), "another previous run, another ID")
	assert.NotEqual(t, id, GenerateRestartRunID("other", "run-1", ended), "another DAG, another ID")
	assert.True(t, strings.HasPrefix(id, restartPrefix))
	assert.True(t, strings.HasSuffix(id, "20261013T120000"))
	assert.LessOrEqual(t, len(id), maxRunIDLen)

	first := GenerateRestartRunID("svc", "", time.Time{})
	assert.Equal(t, first, GenerateRestartRunID("svc", "", time.Time{}), "the first run has a stable ID")
	assert.NotEqual(t, first, GenerateRestartRunID("other", "", time.Time{}))
	assert.Equal(t, len(restartPrefix)+hashLen, len(first), "no time part without a previous end")
}

// restartHarness drives a RestartScanner with in-memory fakes.
type restartHarness struct {
	t          *testing.T
	ctx        context.Context
	logs       *syncBuffer
	now        time.Time
	entries    []DAGEntry
	suspended  map[string]bool
	running    bool
	onWorker   bool
	queued     bool
	dispatched bool
	latest     ir.DAGRunStatus
	latestErr  error
	stopped    map[string]bool
	stopErr    error
	stopChecks int
	reads      int
	queueReads int
	failWith   error
	enqueued   []enqueuedRun
	failed     []string
	scanner    *RestartScanner
}

type enqueuedRun struct {
	dag         string
	runID       string
	triggerType ir.TriggerType
}

func newRestartHarness(t *testing.T, dags ...*ir.DAG) *restartHarness {
	t.Helper()
	logs := &syncBuffer{buf: new(bytes.Buffer)}
	h := &restartHarness{
		t:    t,
		logs: logs,
		ctx: logger.WithFixedLogger(t.Context(), logger.NewLogger(
			logger.WithDebug(), logger.WithFormat("text"), logger.WithWriter(logs),
		)),
		now:       time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC),
		suspended: map[string]bool{},
		stopped:   map[string]bool{},
	}
	for _, dag := range dags {
		h.entries = append(h.entries, DAGEntry{DefinitionID: dag.Name, DAG: dag})
	}
	h.scanner = NewRestartScanner(RestartScannerConfig{
		Entries: func() []DAGEntry { return h.entries },
		IsSuspended: func(_ context.Context, name string) (bool, error) {
			h.reads++
			return h.suspended[name], nil
		},
		IsRunning: func(context.Context, *ir.DAG) (bool, error) {
			h.reads++
			return h.running, nil
		},
		HasDistributedRun: func(context.Context, *ir.DAG) (bool, error) {
			h.reads++
			return h.onWorker, nil
		},
		IsQueued: func(context.Context, *ir.DAG) (bool, error) {
			h.reads++
			h.queueReads++
			return h.queued, nil
		},
		GetLatestStatus: func(context.Context, *ir.DAG) (ir.DAGRunStatus, error) {
			h.reads++
			return h.latest, h.latestErr
		},
		IsStopRequested: func(_ context.Context, _ *ir.DAG, runID string) (bool, error) {
			h.stopChecks++
			return h.stopped[runID], h.stopErr
		},
		IsDispatchPending: func(context.Context, *ir.DAGRunStatus) (bool, error) {
			h.reads++
			return h.dispatched, nil
		},
		// The store fails the run only while it is the latest attempt with the
		// status the scanner read.
		FailStuckRun: func(_ context.Context, _ *ir.DAG, status *ir.DAGRunStatus, at time.Time) error {
			if h.latest.AttemptID == status.AttemptID && h.latest.Status == status.Status {
				h.failed = append(h.failed, status.DAGRunID)
				h.latest.Status = ir.Failed
				h.latest.FinishedAt = at.Format(time.RFC3339)
			}
			return nil
		},
		Enqueue: func(_ context.Context, entry DAGEntry, runID string, triggerType ir.TriggerType, _ time.Time) error {
			if h.failWith != nil {
				return h.failWith
			}
			h.enqueued = append(h.enqueued, enqueuedRun{dag: entry.DAG.Name, runID: runID, triggerType: triggerType})
			return nil
		},
		Clock: func() time.Time { return h.now },
	})
	return h
}

// withEvents gives the scanner an event store whose reads return the events
// added since the previous read.
func (h *restartHarness) withEvents() *fakeRunEvents {
	events := &fakeRunEvents{}
	h.scanner.events = events
	return events
}

type fakeRunEvents struct {
	pending []*eventstore.Event
	headErr error
}

func (f *fakeRunEvents) add(dagName string, status ir.Status) {
	f.pending = append(f.pending, &eventstore.Event{DAGName: dagName, Status: status.String()})
}

func (f *fakeRunEvents) DAGRunHeadCursor(context.Context) (eventstore.DAGRunCursor, error) {
	return eventstore.DAGRunCursor{}, f.headErr
}

func (f *fakeRunEvents) ReadDAGRunEvents(context.Context, eventstore.DAGRunCursor) ([]*eventstore.Event, eventstore.DAGRunCursor, error) {
	events := f.pending
	f.pending = nil
	return events, eventstore.DAGRunCursor{}, nil
}

func (h *restartHarness) scan() {
	h.t.Helper()
	h.scanner.scan(h.ctx)
}

func (h *restartHarness) endRun(status ir.Status, ago time.Duration) {
	h.latest = ir.DAGRunStatus{
		DAGRunID:   "run-1",
		AttemptID:  "attempt-1",
		Status:     status,
		FinishedAt: h.now.Add(-ago).Format(time.RFC3339),
	}
}

// waitBeforeRestart ends runID with status after it ran for ran, then scans
// second by second and reports how long after the end the next run is queued.
func (h *restartHarness) waitBeforeRestart(runID string, status ir.Status, ran time.Duration) time.Duration {
	h.t.Helper()
	h.latest = ir.DAGRunStatus{
		DAGRunID:   runID,
		AttemptID:  runID,
		Status:     status,
		StartedAt:  h.now.Add(-ran).Format(time.RFC3339),
		FinishedAt: h.now.Format(time.RFC3339),
	}
	ended, queued := h.now, len(h.enqueued)
	for len(h.enqueued) == queued {
		require.Less(h.t, h.now.Sub(ended), time.Hour, "no restart within an hour")
		h.now = h.now.Add(time.Second)
		h.scan()
	}
	return h.now.Sub(ended)
}

func (h *restartHarness) logged(msg string) int {
	return strings.Count(h.logs.String(), msg)
}

func serviceDAG(name string, policy ir.RestartPolicy) *ir.DAG {
	return &ir.DAG{Name: name, RestartPolicy: policy, RestartWait: 10 * time.Second}
}

func TestRestartScanner(t *testing.T) {
	t.Parallel()

	t.Run("FirstRun", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.scan()
		require.Len(t, h.enqueued, 1)
		assert.Equal(t, enqueuedRun{
			dag:         "svc",
			runID:       GenerateRestartRunID("svc", "", time.Time{}),
			triggerType: ir.TriggerTypeRestart,
		}, h.enqueued[0])
	})

	t.Run("Wait", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.endRun(ir.Failed, 4*time.Second)
		h.scan()
		assert.Empty(t, h.enqueued, "the wait has not elapsed")
		assert.Contains(t, h.logs.String(), "reason=waiting")

		h.now = h.now.Add(7 * time.Second)
		h.scan()
		require.Len(t, h.enqueued, 1)
		assert.Equal(t, GenerateRestartRunID("svc", "run-1", h.now.Add(-11*time.Second)), h.enqueued[0].runID)
	})

	t.Run("Succeeded", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.endRun(ir.Succeeded, time.Minute)
		h.scan()
		assert.Len(t, h.enqueued, 1)
	})

	// The next run of a DAG that waits out restart_wait_sec cannot become due
	// before the wait ends, so the scanner reads no store until then.
	t.Run("NoReadsWhileWaiting", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.endRun(ir.Failed, 0)
		h.scan()
		reads := h.reads
		for range 10 {
			h.now = h.now.Add(time.Second)
			h.scan()
		}
		assert.Equal(t, reads, h.reads)
		assert.Empty(t, h.enqueued)

		h.now = h.now.Add(time.Second)
		h.scan()
		assert.Len(t, h.enqueued, 1)
	})

	t.Run("EditedWaitApplies", func(t *testing.T) {
		t.Parallel()
		dag := serviceDAG("svc", ir.RestartPolicyAlways)
		h := newRestartHarness(t, dag)
		h.endRun(ir.Failed, 0)
		h.scan()

		dag.RestartWait = 2 * time.Second
		h.now = h.now.Add(3 * time.Second)
		h.scan()
		assert.Len(t, h.enqueued, 1, "a shorter wait applies before the old one ends")
	})

	// A DAG that keeps failing soon after it starts must not restart every
	// restart_wait_sec forever: each such run in a row doubles the wait.
	t.Run("Backoff", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		// The end is stored to the second, so a wait of 10s queues the next run
		// 11s after the end.
		assert.Equal(t, 11*time.Second, h.waitBeforeRestart("run-1", ir.Failed, 2*time.Second))
		assert.Equal(t, 21*time.Second, h.waitBeforeRestart("run-2", ir.Failed, 2*time.Second))
		assert.Equal(t, 41*time.Second, h.waitBeforeRestart("run-3", ir.Failed, 2*time.Second))
		assert.Equal(t, 2, h.logged("keeps failing soon after it starts"))

		assert.Equal(t, 11*time.Second, h.waitBeforeRestart("run-4", ir.Succeeded, time.Second), "a success resets the wait")
		assert.Equal(t, 11*time.Second, h.waitBeforeRestart("run-5", ir.Failed, 2*time.Second))
		assert.Equal(t, 21*time.Second, h.waitBeforeRestart("run-6", ir.Failed, 2*time.Second))
		assert.Equal(t, 11*time.Second, h.waitBeforeRestart("run-7", ir.Failed, time.Minute), "a long run resets the wait")
	})

	t.Run("BackoffLimit", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		var waited time.Duration
		for i := range 8 {
			waited = h.waitBeforeRestart(fmt.Sprintf("run-%d", i), ir.Failed, time.Second)
		}
		assert.Equal(t, restartBackoffMaxWait+time.Second, waited)

		long := serviceDAG("slow", ir.RestartPolicyAlways)
		long.RestartWait = 10 * time.Minute
		h = newRestartHarness(t, long)
		h.waitBeforeRestart("run-1", ir.Failed, time.Second)
		assert.Equal(t, long.RestartWait+time.Second, h.waitBeforeRestart("run-2", ir.Failed, time.Second),
			"a wait above the limit does not grow")
	})

	t.Run("Busy", func(t *testing.T) {
		t.Parallel()
		for name, setup := range map[string]func(*restartHarness){
			"LiveProcess": func(h *restartHarness) { h.running = true },
			// A run on a worker leaves no local process, and an older one can
			// outlive the latest run.
			"RunOnWorker": func(h *restartHarness) {
				h.endRun(ir.Succeeded, time.Hour)
				h.onWorker = true
			},
			"QueuedItem": func(h *restartHarness) { h.queued = true },
			"ActiveRun":  func(h *restartHarness) { h.endRun(ir.Running, 0) },
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
				setup(h)
				h.scan()
				assert.Empty(t, h.enqueued)
				assert.Equal(t, 1, h.logged("Not restarting DAG"))
			})
		}
	})

	// A queued run that nothing will start, such as one whose queue item was
	// never written, is failed, and the DAG restarts after it.
	t.Run("LostQueuedRun", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.latest = ir.DAGRunStatus{
			DAGRunID: "run-1", AttemptID: "attempt-1", Status: ir.Queued,
			CreatedAt: h.now.UnixMilli(),
		}
		h.scan()
		assert.Empty(t, h.failed, "a run is not failed within the timeout")

		h.now = h.now.Add(restartNotStartedTimeout)
		h.scan()
		require.Equal(t, []string{"run-1"}, h.failed)
		assert.Empty(t, h.enqueued, "the failed run is followed after the wait")
		failedAt := h.now

		h.now = h.now.Add(11 * time.Second)
		h.scan()
		require.Len(t, h.enqueued, 1)
		assert.Equal(t, GenerateRestartRunID("svc", "run-1", failedAt), h.enqueued[0].runID)
	})

	// A run that has not started yet is left alone while anything can still
	// start it.
	t.Run("StuckRunBusy", func(t *testing.T) {
		t.Parallel()
		for name, setup := range map[string]func(*restartHarness){
			"LiveProcess":     func(h *restartHarness) { h.running = true },
			"RunOnWorker":     func(h *restartHarness) { h.onWorker = true },
			"QueuedItem":      func(h *restartHarness) { h.queued = true },
			"WaitsForAWorker": func(h *restartHarness) { h.dispatched = true },
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
				h.latest = ir.DAGRunStatus{
					DAGRunID: "run-1", AttemptID: "attempt-1", Status: ir.Queued,
					CreatedAt: h.now.Add(-restartNotStartedTimeout).UnixMilli(),
				}
				setup(h)
				h.scan()
				assert.Empty(t, h.failed)
				assert.Empty(t, h.enqueued)
			})
		}
	})

	// A queue is read item by item, so it is read only for a DAG about to
	// restart.
	t.Run("QueueReadOnlyBeforeRestart", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.endRun(ir.Running, 0)
		h.scan()
		h.endRun(ir.Failed, 0)
		h.scan()
		assert.Zero(t, h.queueReads)

		h.now = h.now.Add(11 * time.Second)
		h.scan()
		assert.Equal(t, 1, h.queueReads)
		assert.Len(t, h.enqueued, 1)
	})

	// With events, a DAG whose run is active is read again only once an event
	// says the run ended.
	t.Run("EventWakesDAG", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		events := h.withEvents()
		h.endRun(ir.Running, 0)
		h.scan()
		reads := h.reads
		for range 5 {
			h.now = h.now.Add(time.Second)
			events.add("svc", ir.Running)
			h.scan()
		}
		assert.Equal(t, reads, h.reads, "an active run is not read again")

		h.endRun(ir.Failed, 0)
		events.add("other", ir.Failed)
		events.add("svc", ir.Failed)
		h.scan()
		assert.Greater(t, h.reads, reads, "the end of the run is read")
		assert.Empty(t, h.enqueued)

		h.now = h.now.Add(11 * time.Second)
		h.scan()
		assert.Len(t, h.enqueued, 1)
	})

	// Events can be lost, so a DAG whose run is active is read again after a
	// while anyway.
	t.Run("LostEvent", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.withEvents()
		h.endRun(ir.Running, 0)
		h.scan()
		h.endRun(ir.Failed, 0)

		h.now = h.now.Add(restartReconcileInterval - time.Second)
		h.scan()
		assert.Empty(t, h.enqueued)

		h.now = h.now.Add(time.Second)
		h.scan()
		assert.Len(t, h.enqueued, 1)
	})

	t.Run("EventsUnavailable", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.withEvents().headErr = errors.New("event store unreadable")
		h.endRun(ir.Running, 0)
		h.scan()
		h.endRun(ir.Failed, time.Minute)
		h.now = h.now.Add(time.Second)
		h.scan()
		assert.Len(t, h.enqueued, 1, "every scan checks the DAG")
		assert.Equal(t, 1, h.logged("failed to read DAG-run events"), "errors are throttled")
	})

	t.Run("Suspended", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.suspended["svc"] = true
		h.scan()
		assert.Empty(t, h.enqueued)

		h.suspended["svc"] = false
		h.scan()
		assert.Len(t, h.enqueued, 1, "resuming restarts the DAG")
	})

	t.Run("UnlessStopped", func(t *testing.T) {
		t.Parallel()
		for _, tt := range []struct {
			name      string
			policy    ir.RestartPolicy
			status    ir.Status
			stopped   bool
			restarted bool
		}{
			{"StoppedRunStaysStopped", ir.RestartPolicyUnlessStopped, ir.Aborted, true, false},
			{"OtherAbortRestarts", ir.RestartPolicyUnlessStopped, ir.Aborted, false, true},
			// A stopped distributed run whose worker is gone ends as failed.
			{"StoppedFailedRunStaysStopped", ir.RestartPolicyUnlessStopped, ir.Failed, true, false},
			{"FailedRunRestarts", ir.RestartPolicyUnlessStopped, ir.Failed, false, true},
			{"AlwaysIgnoresStops", ir.RestartPolicyAlways, ir.Aborted, true, true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				h := newRestartHarness(t, serviceDAG("svc", tt.policy))
				h.endRun(tt.status, time.Hour)
				h.stopped["run-1"] = tt.stopped
				h.scan()
				assert.Equal(t, tt.restarted, len(h.enqueued) == 1)
			})
		}
	})

	t.Run("StopIsCheckedOnce", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyUnlessStopped))
		h.endRun(ir.Aborted, time.Hour)
		h.stopped["run-1"] = true
		h.scan()
		h.scan()
		h.scan()
		assert.Empty(t, h.enqueued)
		assert.Equal(t, 1, h.stopChecks, "a stopped run is remembered")

		h.entries = nil
		h.scan()
		h.entries = []DAGEntry{{DefinitionID: "svc", DAG: serviceDAG("svc", ir.RestartPolicyUnlessStopped)}}
		h.scan()
		assert.Equal(t, 2, h.stopChecks, "a DAG that left the scheduler is forgotten")
	})

	t.Run("NewAttemptOfAStoppedRun", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyUnlessStopped))
		h.endRun(ir.Aborted, time.Hour)
		h.stopped["run-1"] = true
		h.scan()
		assert.Empty(t, h.enqueued)

		// A retry of the run adds an attempt, which ends aborted without a stop.
		h.latest.AttemptID = "attempt-2"
		h.stopped["run-1"] = false
		h.scan()
		assert.Len(t, h.enqueued, 1, "the stop of an earlier attempt does not hold")
	})

	t.Run("StopCheckFails", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyUnlessStopped))
		h.endRun(ir.Aborted, time.Hour)
		h.stopErr = errors.New("store unavailable")
		h.scan()
		assert.Empty(t, h.enqueued, "a run that may have been stopped is not restarted")
		assert.Equal(t, 1, h.logged("Restart scanner failed to process DAG"))
	})

	t.Run("LatestStatusFails", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.latestErr = errors.New("status unreadable")
		h.scan()
		assert.Empty(t, h.enqueued, "an unreadable latest run is not taken for no run")
		assert.Equal(t, 1, h.logged("Restart scanner failed to process DAG"))
	})

	t.Run("NoPolicy", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("plain", ir.RestartPolicyNo), serviceDAG("svc", ir.RestartPolicyAlways))
		h.entries = append(h.entries, DAGEntry{})
		h.scan()
		require.Len(t, h.enqueued, 1)
		assert.Equal(t, "svc", h.enqueued[0].dag)
	})

	t.Run("EnqueueFails", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.failWith = errors.New("disk full")
		h.scan()
		h.now = h.now.Add(time.Second)
		h.scan()
		assert.Empty(t, h.enqueued)
		assert.Equal(t, 1, h.logged("Restart scanner failed to process DAG"), "errors are throttled")

		h.now = h.now.Add(restartErrorLogInterval)
		h.scan()
		assert.Equal(t, 2, h.logged("Restart scanner failed to process DAG"))

		h.failWith = nil
		h.scan()
		assert.Len(t, h.enqueued, 1, "the next scan tries again")
	})

	// Restarts go through queues, so with queues disabled the DAG never runs:
	// an error that keeps being logged, not a warning logged once.
	t.Run("QueuesDisabled", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.scanner.cfg.Enqueue = nil
		h.scan()
		h.scan()
		assert.Empty(t, h.enqueued)
		assert.Equal(t, 1, h.logged("queues are disabled"), "errors are throttled")

		h.now = h.now.Add(restartErrorLogInterval)
		h.scan()
		assert.Equal(t, 2, h.logged("queues are disabled"))
		assert.Equal(t, 2, h.logged("level=ERROR"))
	})
}

func TestRestartIsSchedulerManaged(t *testing.T) {
	t.Parallel()
	assert.True(t, isSchedulerManagedTriggerType(ir.TriggerTypeRestart))
}
