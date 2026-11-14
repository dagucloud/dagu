// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

// failingProcs and failingLeases cannot be read.
type failingProcs struct{}

func (failingProcs) ListAllAlive(context.Context) (map[string][]ir.DAGRunRef, error) {
	return nil, errors.New("proc files unreadable")
}

type failingLeases struct{}

func (failingLeases) ListAll(context.Context) ([]dispatch.DAGRunLease, error) {
	return nil, errors.New("leases unreadable")
}

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
// in progress on the server and on distributed workers, each counted once,
// and how many may run at once. A queue whose next run waits for a worker
// comes first, then queues with room, then full ones, then queues with
// nothing queued.
func TestQueuesListWhatWaitsAndRuns(t *testing.T) {
	t.Parallel()
	s := newTestQueueStore(t)
	enqueue(t, s, "etl", "etl", "e1")
	enqueue(t, s, "etl", "etl", "e2")
	enqueue(t, s, "busy", "busy", "u2")
	enqueue(t, s, "batch", "report", "b1")
	now := time.Now().Add(2 * time.Minute)
	runs := &fakeRuns{statuses: map[string]*ir.DAGRunStatus{
		"e1": {Conditions: ir.NoMatchingWorkerConditions(now)},
		"b1": {},
	}}
	q := Queues{
		Store: s,
		Procs: fakeProcs{"busy": {ir.NewDAGRunRef("busy", "u1")}, "idle": {ir.NewDAGRunRef("idle", "i1")}},
		Leases: fakeLeases{
			{DAGRun: ir.NewDAGRunRef("report", "b0"), AttemptKey: "b0-a1", QueueName: "batch", LastHeartbeatAt: now.UnixMilli()},
			{DAGRun: ir.NewDAGRunRef("report", "b0"), AttemptKey: "b0-a2", QueueName: "batch", LastHeartbeatAt: now.UnixMilli()},
			{DAGRun: ir.NewDAGRunRef("etl", "e0"), QueueName: "etl", LastHeartbeatAt: now.Add(-time.Hour).UnixMilli()},
			{DAGRun: ir.NewDAGRunRef("busy", "u1"), QueueName: "busy", LastHeartbeatAt: now.UnixMilli()},
			{DAGRun: ir.NewDAGRunRef("other", "i1"), QueueName: "other", LastHeartbeatAt: now.UnixMilli()},
		},
		Runs: runs,
		Capacity: func(name string) int {
			if name == "batch" {
				return 2
			}
			return 1
		},
	}

	read, ok := q.read(context.Background(), now, nil)

	require.True(t, ok)
	assert.Equal(t, 5, read.total)
	queues := read.queues
	// When the next runs were queued depends on the clock; only whether a
	// queue says so is compared, and only what a report sends.
	for i := range queues {
		if queues[i].NextQueuedAt != "" {
			queues[i].NextQueuedAt = "set"
		}
		queues[i].next, queues[i].nextAt = "", time.Time{}
	}
	assert.Equal(t, []queueHealth{
		{Name: "etl", Queued: 2, MaxActive: 1, NextQueuedAt: "set", BlockedReason: ir.DAGRunReasonNoMatchingWorker},
		{Name: "batch", Queued: 1, Running: 1, MaxActive: 2, NextQueuedAt: "set"},
		{Name: "busy", Queued: 1, Running: 1, MaxActive: 1, NextQueuedAt: "set"},
		{Name: "idle", Running: 1, MaxActive: 1},
		{Name: "other", Running: 1, MaxActive: 1},
	}, queues, "a stale lease counts no run; a run counts once, on the server or across its attempts' leases, "+
		"and another DAG's run of the same ID counts")
	assert.ElementsMatch(t, []string{"e1", "b1"}, runs.opened, "only queues with room are asked why they wait")
}

// A queue configured to run none at once runs one, as the scheduler runs
// it.
func TestQueuesRunAtLeastOneAtOnce(t *testing.T) {
	t.Parallel()
	s := newTestQueueStore(t)
	enqueue(t, s, "batch", "report", "b1")

	read, ok := Queues{Store: s, Capacity: func(string) int { return 0 }}.read(context.Background(), time.Now(), nil)

	require.True(t, ok)
	require.Len(t, read.queues, 1)
	assert.Equal(t, 1, read.queues[0].MaxActive)
}

// A read whose runs in progress cannot be read fails, rather than send
// counts that are short.
func TestQueuesFailWhenTheRunsInProgressCannotBeRead(t *testing.T) {
	t.Parallel()
	s := newTestQueueStore(t)
	enqueue(t, s, "etl", "etl", "e1")

	for name, q := range map[string]Queues{
		"processes": {Store: s, Procs: failingProcs{}},
		"leases":    {Store: s, Leases: failingLeases{}},
	} {
		t.Run(name, func(t *testing.T) {
			_, ok := q.read(context.Background(), time.Now(), nil)
			assert.False(t, ok)
		})
	}
}

// With more waiting queues than one read asks of, each read asks of those
// asked longest ago, so a queue past the first maxQueueReasons is asked in
// turn, and keeps its answer while others are asked, until its next run
// changes.
func TestQueuesAskOfWaitingQueuesInTurn(t *testing.T) {
	t.Parallel()
	s := newTestQueueStore(t)
	statuses := make(map[string]*ir.DAGRunStatus)
	for i := range maxQueueReasons + 2 {
		name := fmt.Sprintf("q%02d", i)
		enqueue(t, s, name, name, "r"+name)
		statuses["r"+name] = &ir.DAGRunStatus{}
	}
	last := fmt.Sprintf("q%02d", maxQueueReasons+1)
	statuses["r"+last] = &ir.DAGRunStatus{Conditions: ir.NoMatchingWorkerConditions(time.Now())}
	runs := &fakeRuns{statuses: statuses}
	q := Queues{Store: s, Runs: runs}
	start := time.Now().Add(reasonAfter)
	blocked := func(read queueRead) string {
		for _, h := range read.queues {
			if h.BlockedReason != "" {
				return h.Name
			}
		}
		return ""
	}

	first, ok := q.read(context.Background(), start, nil)
	require.True(t, ok)
	assert.Len(t, runs.opened, maxQueueReasons)
	assert.Empty(t, blocked(first), "the last queue waited least, or ties last by name, so it is not asked first")

	second, ok := q.read(context.Background(), start.Add(time.Minute), first.checks)
	require.True(t, ok)
	assert.Len(t, runs.opened, 2*maxQueueReasons)
	assert.Equal(t, last, blocked(second))
	assert.Equal(t, last, second.queues[0].Name, "a queue waiting for a worker comes first")

	third, ok := q.read(context.Background(), start.Add(2*time.Minute), second.checks)
	require.True(t, ok)
	assert.Equal(t, last, blocked(third), "it keeps its answer while others are asked")

	items, err := s.List(context.Background(), last)
	require.NoError(t, err)
	_, err = s.DeleteByItemIDs(context.Background(), last, []string{items[0].ID()})
	require.NoError(t, err)
	enqueue(t, s, last, last, "r"+last+"-next")
	statuses["r"+last+"-next"] = &ir.DAGRunStatus{}
	fourth, ok := q.read(context.Background(), start.Add(3*time.Minute), third.checks)
	require.True(t, ok)
	assert.Empty(t, blocked(fourth), "a new next run does not keep the answer about the last one")
	assert.Equal(t, "r"+last+"-next", runs.opened[len(runs.opened)-maxQueueReasons], "and is asked first")
}

// Waiting queues whose next runs were queued at the same moment, as a
// coarse clock makes them, are asked in the order of their names.
func TestQueuesAskOfTiedQueuesByName(t *testing.T) {
	t.Parallel()
	s := newTestQueueStore(t)
	for i := range maxQueueReasons + 1 {
		name := fmt.Sprintf("q%02d", maxQueueReasons-i)
		enqueue(t, s, name, name, "r"+name)
	}
	summaries, err := s.Summaries(context.Background())
	require.NoError(t, err)
	queued := time.Now()
	// Listed in reverse name order, so only explain puts them in name order.
	var queues []queueHealth
	for _, summary := range slices.Backward(summaries) {
		queues = append(queues, queueHealth{Name: summary.Name, Queued: 1, MaxActive: 1, next: summary.Next, nextAt: queued})
	}
	runs := &fakeRuns{statuses: map[string]*ir.DAGRunStatus{}}

	Queues{Store: s, Runs: runs}.explain(context.Background(), queues, queued.Add(reasonAfter), nil)

	require.Len(t, runs.opened, maxQueueReasons)
	assert.Equal(t, "rq00", runs.opened[0])
	assert.NotContains(t, runs.opened, fmt.Sprintf("rq%02d", maxQueueReasons))
}

// A queue whose next run has not waited long is not asked why.
func TestQueuesAskWhyOnlyAfterAWait(t *testing.T) {
	t.Parallel()
	s := newTestQueueStore(t)
	enqueue(t, s, "etl", "etl", "e1")
	runs := &fakeRuns{}

	read, ok := Queues{Store: s, Runs: runs}.read(context.Background(), time.Now().Add(reasonAfter/2), nil)

	require.True(t, ok)
	require.Len(t, read.queues, 1)
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

	read, ok := Queues{Store: s}.read(context.Background(), time.Now(), nil)

	require.True(t, ok)
	assert.Len(t, read.queues, maxQueues)
	assert.Equal(t, maxQueues+1, read.total)
	assert.Equal(t, "q000", read.queues[0].Name, "the longest wait first")
}
