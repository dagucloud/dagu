// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		name   string
		policy ir.RestartPolicy
		latest *ir.DAGRunStatus
		start  bool
		reason string
		at     time.Time
	}{
		{
			name:   "NoRunStartsAtOnce",
			policy: ir.RestartPolicyAlways,
			latest: &ir.DAGRunStatus{Status: ir.NotStarted},
			start:  true, reason: "first_run", at: now,
		},
		{
			name:   "WaitElapsedRestarts",
			policy: ir.RestartPolicyAlways,
			latest: finished(ir.Failed, 20*time.Second),
			start:  true, reason: "previous_run_ended", at: now.Add(-20 * time.Second),
		},
		{
			name:   "WaitElapsedExactlyRestarts",
			policy: ir.RestartPolicyAlways,
			latest: finished(ir.Succeeded, wait),
			start:  true, reason: "previous_run_ended", at: now.Add(-wait),
		},
		{
			name:   "WaitNotElapsedWaits",
			policy: ir.RestartPolicyAlways,
			latest: finished(ir.Failed, 5*time.Second),
			reason: "waiting",
		},
		{
			name:   "OutageLongerThanWaitRestartsAtOnce",
			policy: ir.RestartPolicyAlways,
			latest: finished(ir.Failed, 3*time.Hour),
			start:  true, reason: "previous_run_ended", at: now.Add(-3 * time.Hour),
		},
		{name: "RunningWaits", policy: ir.RestartPolicyAlways, latest: finished(ir.Running, 0), reason: "run_active"},
		{name: "QueuedWaits", policy: ir.RestartPolicyAlways, latest: finished(ir.Queued, 0), reason: "run_active"},
		{name: "WaitingForApprovalWaits", policy: ir.RestartPolicyAlways, latest: finished(ir.Waiting, 0), reason: "run_active"},
		{
			name:   "NotStartedRunWaits",
			policy: ir.RestartPolicyAlways,
			latest: &ir.DAGRunStatus{DAGRunID: "run-1", Status: ir.NotStarted},
			reason: "run_active",
		},
		{
			name:   "AlwaysRestartsAbortedRun",
			policy: ir.RestartPolicyAlways,
			latest: finished(ir.Aborted, 20*time.Second),
			start:  true, reason: "previous_run_ended", at: now.Add(-20 * time.Second),
		},
		{
			name:   "UnlessStoppedKeepsAbortedRunStopped",
			policy: ir.RestartPolicyUnlessStopped,
			latest: finished(ir.Aborted, time.Hour),
			reason: "stopped",
		},
		{
			name:   "UnlessStoppedRestartsFailedRun",
			policy: ir.RestartPolicyUnlessStopped,
			latest: finished(ir.Failed, 20*time.Second),
			start:  true, reason: "previous_run_ended", at: now.Add(-20 * time.Second),
		},
		{
			name:   "RejectedRunRestarts",
			policy: ir.RestartPolicyAlways,
			latest: finished(ir.Rejected, 20*time.Second),
			start:  true, reason: "previous_run_ended", at: now.Add(-20 * time.Second),
		},
		{
			name:   "PartiallySucceededRunRestarts",
			policy: ir.RestartPolicyAlways,
			latest: finished(ir.PartiallySucceeded, 20*time.Second),
			start:  true, reason: "previous_run_ended", at: now.Add(-20 * time.Second),
		},
		{
			name:   "UnknownEndTimeRestarts",
			policy: ir.RestartPolicyAlways,
			latest: &ir.DAGRunStatus{DAGRunID: "run-1", Status: ir.Failed},
			start:  true, reason: "previous_run_end_unknown", at: now,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := decideRestart(tt.policy, wait, tt.latest, now)
			assert.Equal(t, tt.start, got.start)
			assert.Equal(t, tt.reason, got.reason)
			if tt.start {
				assert.True(t, tt.at.Equal(got.at), "at = %v, want %v", got.at, tt.at)
			}
		})
	}
}

func TestGenerateRestartRunID(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

	id := GenerateRestartRunID("svc", "run-1", at)
	assert.Equal(t, id, GenerateRestartRunID("svc", "run-1", at), "same previous run, same ID")
	assert.NotEqual(t, id, GenerateRestartRunID("svc", "run-2", at), "another previous run, another ID")
	assert.NotEqual(t, id, GenerateRestartRunID("other", "run-1", at), "another DAG, another ID")
	assert.Contains(t, id, restartPrefix)
	assert.LessOrEqual(t, len(id), maxRunIDLen)

	first := GenerateRestartRunID("svc", "", at)
	assert.NotEqual(t, first, GenerateRestartRunID("svc", "", at.Add(time.Second)),
		"the first run is named after the time it starts")
}

// restartHarness drives a RestartScanner with in-memory fakes.
type restartHarness struct {
	t         *testing.T
	now       time.Time
	entries   []DAGEntry
	suspended map[string]bool
	running   bool
	queued    bool
	latest    ir.DAGRunStatus
	existing  map[string]bool
	failWith  error
	enqueued  []enqueuedRun
	scanner   *RestartScanner
}

type enqueuedRun struct {
	dag         string
	runID       string
	triggerType ir.TriggerType
}

func newRestartHarness(t *testing.T, dags ...*ir.DAG) *restartHarness {
	t.Helper()
	h := &restartHarness{
		t:         t,
		now:       time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC),
		suspended: map[string]bool{},
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
			return h.latest, nil
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
	h.scanner.scan(h.t.Context())
}

func (h *restartHarness) endRun(status ir.Status, ago time.Duration) {
	h.latest = ir.DAGRunStatus{
		DAGRunID:   "run-1",
		Status:     status,
		FinishedAt: h.now.Add(-ago).Format(time.RFC3339),
	}
}

func serviceDAG(name string, policy ir.RestartPolicy) *ir.DAG {
	return &ir.DAG{Name: name, RestartPolicy: policy, RestartWait: 10 * time.Second}
}

func TestRestartScanner(t *testing.T) {
	t.Parallel()

	t.Run("StartsTheFirstRunAtOnce", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.scan()
		require.Len(t, h.enqueued, 1)
		assert.Equal(t, "svc", h.enqueued[0].dag)
		assert.Equal(t, ir.TriggerTypeRestart, h.enqueued[0].triggerType)
	})

	t.Run("RestartsOnceTheWaitElapsed", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.endRun(ir.Failed, 4*time.Second)
		h.scan()
		assert.Empty(t, h.enqueued, "the wait has not elapsed")

		h.now = h.now.Add(6 * time.Second)
		h.scan()
		require.Len(t, h.enqueued, 1)
		assert.Equal(t, GenerateRestartRunID("svc", "run-1", h.now.Add(-10*time.Second)), h.enqueued[0].runID)
	})

	t.Run("RestartsAfterASuccessfulRun", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.endRun(ir.Succeeded, time.Minute)
		h.scan()
		assert.Len(t, h.enqueued, 1)
	})

	t.Run("NeverStartsASecondRun", func(t *testing.T) {
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
			})
		}
	})

	t.Run("SuspendedDAGIsNotStarted", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.suspended["svc"] = true
		h.scan()
		assert.Empty(t, h.enqueued)

		h.suspended["svc"] = false
		h.scan()
		assert.Len(t, h.enqueued, 1, "resuming restarts the DAG")
	})

	t.Run("UnlessStoppedStaysStopped", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyUnlessStopped))
		h.endRun(ir.Aborted, time.Hour)
		h.scan()
		assert.Empty(t, h.enqueued)
	})

	t.Run("IgnoresDAGsWithoutPolicy", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("plain", ir.RestartPolicyNo), serviceDAG("svc", ir.RestartPolicyAlways))
		h.entries = append(h.entries, DAGEntry{})
		h.scan()
		require.Len(t, h.enqueued, 1)
		assert.Equal(t, "svc", h.enqueued[0].dag)
	})

	t.Run("FailedEnqueueIsRetriedAtTheNextScan", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.failWith = errors.New("disk full")
		h.scan()
		h.scan()
		assert.Empty(t, h.enqueued)

		h.failWith = nil
		h.scan()
		assert.Len(t, h.enqueued, 1)
	})

	t.Run("QueuesDisabledStartsNothing", func(t *testing.T) {
		t.Parallel()
		h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
		h.scanner.cfg.Enqueue = nil
		h.scan()
		h.scan()
		assert.Empty(t, h.enqueued)
		assert.Len(t, h.scanner.warnedNoQueue, 1, "the warning is logged once per DAG")
	})
}

func TestRestartScannerThrottlesErrorLogs(t *testing.T) {
	t.Parallel()

	h := newRestartHarness(t, serviceDAG("svc", ir.RestartPolicyAlways))
	h.failWith = errors.New("disk full")

	h.scan()
	first := h.scanner.lastErrorLog["svc"]
	h.now = h.now.Add(time.Second)
	h.scan()
	assert.Equal(t, first, h.scanner.lastErrorLog["svc"], "no second log within the throttle window")

	h.now = h.now.Add(restartErrorLogInterval)
	h.scan()
	assert.True(t, h.scanner.lastErrorLog["svc"].After(first))
}

func TestRestartIsSchedulerManaged(t *testing.T) {
	t.Parallel()
	assert.True(t, isSchedulerManagedTriggerType(ir.TriggerTypeRestart))
}
