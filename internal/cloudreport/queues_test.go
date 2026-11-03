// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	persistest "github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/testutil"
)

// fakeProcs lists the runs in progress on the server, by queue.
type fakeProcs map[string][]ir.DAGRunRef

func (p fakeProcs) ListAllAlive(context.Context) (map[string][]ir.DAGRunRef, error) { return p, nil }

// fakeLeases lists distributed runs' leases.
type fakeLeases []dispatch.DAGRunLease

func (l fakeLeases) ListAll(context.Context) ([]dispatch.DAGRunLease, error) { return l, nil }

// fakeRuns reads runs' statuses by run ID, and records which it opened.
type fakeRuns struct {
	statuses map[string]*ir.DAGRunStatus
	opened   []string
}

func (r *fakeRuns) FindAttempt(_ context.Context, ref ir.DAGRunRef) (dagrun.Attempt, error) {
	r.opened = append(r.opened, ref.ID)
	status, ok := r.statuses[ref.ID]
	if !ok {
		return nil, dagrun.ErrDAGRunIDNotFound
	}
	return &testutil.MockAttempt{Status: status}, nil
}

func newTestQueueStore(t *testing.T) *store.QueueStore {
	t.Helper()
	return store.NewQueueStore(persistest.NewMemoryBackend().Collection("queue"))
}

func enqueue(t *testing.T, s *store.QueueStore, queueName, dagName, runID string) {
	t.Helper()
	require.NoError(t, s.Enqueue(context.Background(), queueName, queue.QueuePriorityLow, ir.NewDAGRunRef(dagName, runID)))
}

// A read lists each queue that holds queued or running runs, with the runs
// in progress on the server and on distributed workers, and how many may
// run at once. A queue whose next run cannot start for want of a worker
// comes first, then the longest waits, then queues with nothing queued.
func TestQueuesListWhatWaitsAndRuns(t *testing.T) {
	t.Parallel()
	s := newTestQueueStore(t)
	enqueue(t, s, "etl", "etl", "e1")
	enqueue(t, s, "etl", "etl", "e2")
	enqueue(t, s, "batch", "report", "b1")
	enqueue(t, s, "busy", "busy", "u2")
	now := time.Now().Add(2 * time.Minute)
	runs := &fakeRuns{statuses: map[string]*ir.DAGRunStatus{
		"e1": {Conditions: ir.NoMatchingWorkerConditions(now)},
		"b1": {},
	}}
	q := Queues{
		Store: s,
		Procs: fakeProcs{"busy": {ir.NewDAGRunRef("busy", "u1")}, "idle": {ir.NewDAGRunRef("idle", "i1")}},
		Leases: fakeLeases{
			{DAGRun: ir.NewDAGRunRef("report", "b0"), QueueName: "batch", LastHeartbeatAt: now.UnixMilli()},
			{DAGRun: ir.NewDAGRunRef("etl", "e0"), QueueName: "etl", LastHeartbeatAt: now.Add(-time.Hour).UnixMilli()},
			{DAGRun: ir.NewDAGRunRef("busy", "u1"), QueueName: "busy", LastHeartbeatAt: now.UnixMilli()},
		},
		Runs: runs,
		Capacity: func(name string) int {
			if name == "batch" {
				return 2
			}
			return 1
		},
	}

	queues, total, ok := q.read(context.Background(), now)

	require.True(t, ok)
	assert.Equal(t, 4, total)
	// When the next runs were queued depends on the clock; only whether a
	// queue says so is compared.
	for i := range queues {
		if queues[i].NextQueuedAt != "" {
			queues[i].NextQueuedAt = "set"
		}
	}
	assert.Equal(t, []queueHealth{
		{Name: "etl", Queued: 2, MaxActive: 1, NextQueuedAt: "set", BlockedReason: ir.DAGRunReasonNoMatchingWorker},
		{Name: "batch", Queued: 1, Running: 1, MaxActive: 2, NextQueuedAt: "set"},
		{Name: "busy", Queued: 1, Running: 1, MaxActive: 1, NextQueuedAt: "set"},
		{Name: "idle", Running: 1, MaxActive: 1},
	}, queues, "a stale lease counts no run, and a run on the server counts once")
	assert.ElementsMatch(t, []string{"e1", "b1"}, runs.opened, "only queues with room are asked why they wait")
}

// A queue configured to run none at once runs one, as the scheduler runs
// it.
func TestQueuesRunAtLeastOneAtOnce(t *testing.T) {
	t.Parallel()
	s := newTestQueueStore(t)
	enqueue(t, s, "batch", "report", "b1")

	queues, _, ok := Queues{Store: s, Capacity: func(string) int { return 0 }}.read(context.Background(), time.Now())

	require.True(t, ok)
	require.Len(t, queues, 1)
	assert.Equal(t, 1, queues[0].MaxActive)
}

// A queue whose next run has not waited long is not asked why.
func TestQueuesAskWhyOnlyAfterAWait(t *testing.T) {
	t.Parallel()
	s := newTestQueueStore(t)
	enqueue(t, s, "etl", "etl", "e1")
	runs := &fakeRuns{}

	queues, _, ok := Queues{Store: s, Runs: runs}.read(context.Background(), time.Now().Add(reasonAfter/2))

	require.True(t, ok)
	require.Len(t, queues, 1)
	assert.Empty(t, runs.opened)
}

// A read lists at most maxQueues queues and counts them all, and leaves
// out a queue whose name Dagu Console would refuse.
func TestQueuesListAtMostTheLimit(t *testing.T) {
	t.Parallel()
	s := newTestQueueStore(t)
	for i := range maxQueues + 1 {
		name := fmt.Sprintf("q%03d", i)
		enqueue(t, s, name, name, "r"+name)
	}
	enqueue(t, s, strings.Repeat("x", maxQueueNameLen+1), "long", "r-long")

	queues, total, ok := Queues{Store: s}.read(context.Background(), time.Now())

	require.True(t, ok)
	assert.Len(t, queues, maxQueues)
	assert.Equal(t, maxQueues+1, total)
	assert.Equal(t, "q000", queues[0].Name, "the longest wait first")
}
