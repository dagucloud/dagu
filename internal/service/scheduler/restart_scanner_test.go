// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
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
	}{
		{name: "FirstRun", latest: &ir.DAGRunStatus{}, start: true, reason: "first_run"},
		{
			name:   "WaitElapsed",
			latest: finished(ir.Failed, 20*time.Second),
			start:  true, reason: "previous_run_ended", previousEnd: now.Add(-20 * time.Second),
		},
		{
			name:   "WaitJustElapsed",
			latest: finished(ir.Succeeded, wait),
			start:  true, reason: "previous_run_ended", previousEnd: now.Add(-wait),
		},
		{name: "Waiting", latest: finished(ir.Failed, 5*time.Second), reason: "waiting"},
		{
			name:   "AfterOutage",
			latest: finished(ir.Failed, 3*time.Hour),
			start:  true, reason: "previous_run_ended", previousEnd: now.Add(-3 * time.Hour),
		},
		{name: "Running", latest: finished(ir.Running, 0), reason: "run_active"},
		{name: "Queued", latest: finished(ir.Queued, 0), reason: "run_active"},
		{name: "WaitingApproval", latest: finished(ir.Waiting, 0), reason: "run_active"},
		{name: "NotStarted", latest: &ir.DAGRunStatus{DAGRunID: "run-1", Status: ir.NotStarted}, reason: "run_active"},
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
	queued     bool
	latest     ir.DAGRunStatus
	latestErr  error
	stopped    map[string]bool
	stopErr    error
	stopChecks int
	existing   map[string]bool
	failWith   error
	enqueued   []enqueuedRun
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
		existing:  map[string]bool{},
	}
	for _, dag := range dags {
		h.entries = append(h.entries, DAGEntry{DefinitionID: dag.Name, DAG: dag})
	}
	h.scanner = NewRestartScanner(RestartScannerConfig{
		Entries: func() []DAGEntry { return h.entries },
		IsSuspended: func(_ context.Context, name string) (bool, error) {
			return h.suspended[name], nil
		},
		IsRunning: func(context.Context, *ir.DAG) (bool, error) { return h.running, nil },
		IsQueued:  func(context.Context, *ir.DAG) (bool, error) { return h.queued, nil },
		GetLatestStatus: func(context.Context, *ir.DAG) (ir.DAGRunStatus, error) {
			return h.latest, h.latestErr
		},
		IsStopRequested: func(_ context.Context, _ *ir.DAG, runID string) (bool, error) {
			h.stopChecks++
			return h.stopped[runID], h.stopErr
		},
		RunExists: func(_ context.Context, _ *ir.DAG, runID string) (bool, error) {
			return h.existing[runID], nil
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

func (h *restartHarness) scan() {
	h.t.Helper()
	h.scanner.scan(h.ctx)
}

func (h *restartHarness) endRun(status ir.Status, ago time.Duration) {
	h.latest = ir.DAGRunStatus{
		DAGRunID:   "run-1",
		Status:     status,
		FinishedAt: h.now.Add(-ago).Format(time.RFC3339),
	}
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

		h.now = h.now.Add(6 * time.Second)
		h.scan()
		require.Len(t, h.enqueued, 1)
		assert.Equal(t, GenerateRestartRunID("svc", "run-1", h.now.Add(-10*time.Second)), h.enqueued[0].runID)
	})

	t.Run("Succeeded", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.endRun(ir.Succeeded, time.Minute)
		h.scan()
		assert.Len(t, h.enqueued, 1)
	})

	t.Run("Busy", func(t *testing.T) {
		t.Parallel()
		for name, setup := range map[string]func(*restartHarness){
			"LiveProcess": func(h *restartHarness) { h.running = true },
			"QueuedItem":  func(h *restartHarness) { h.queued = true },
			"ActiveRun":   func(h *restartHarness) { h.endRun(ir.Running, 0) },
			"RunExists": func(h *restartHarness) {
				h.endRun(ir.Failed, time.Minute)
				h.existing[GenerateRestartRunID("svc", "run-1", h.now.Add(-time.Minute))] = true
			},
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
			stopped   bool
			restarted bool
		}{
			{"StoppedRunStaysStopped", ir.RestartPolicyUnlessStopped, true, false},
			{"OtherAbortRestarts", ir.RestartPolicyUnlessStopped, false, true},
			{"AlwaysIgnoresStops", ir.RestartPolicyAlways, true, true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				h := newRestartHarness(t, serviceDAG("svc", tt.policy))
				h.endRun(ir.Aborted, time.Hour)
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

	t.Run("QueuesDisabled", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.scanner.cfg.Enqueue = nil
		h.scan()
		h.scan()
		assert.Empty(t, h.enqueued)
		assert.Equal(t, 1, h.logged("queues are disabled"), "the warning is logged once per DAG")
	})
}

func TestRestartIsSchedulerManaged(t *testing.T) {
	t.Parallel()
	assert.True(t, isSchedulerManagedTriggerType(ir.TriggerTypeRestart))
}
