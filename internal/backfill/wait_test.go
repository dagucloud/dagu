// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queuedRun is a queue item holding one run.
type queuedRun string

func (r queuedRun) ID() string { return string(r) }

func (r queuedRun) Data() (*ir.DAGRunRef, error) {
	ref := ir.NewDAGRunRef("etl", string(r))
	return &ref, nil
}

// fixedQueue is a queue whose items and running runs are fixed.
type fixedQueue struct {
	items   []queue.QueuedItemData
	running int
}

func (q fixedQueue) List(context.Context, string) ([]queue.QueuedItemData, error) {
	return q.items, nil
}

func (q fixedQueue) CountAlive(context.Context, string) (int, error) {
	return q.running, nil
}

func TestWaitReason(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})
	f.tick()
	require.Len(t, f.queued, 1)
	runID := f.queued[0]

	reason := func(q fixedQueue) string {
		t.Helper()
		report, err := backfill.Describe(f.ctx, f.store, f.dispatcher.ReadRun, f.record(record.ID))
		require.NoError(t, err)
		wait, err := backfill.WaitReason(f.ctx, report, backfill.QueueView{
			Items: q,
			Procs: q,
			Limit: func(string) int { return 1 },
		}, time.UTC)
		require.NoError(t, err)
		return wait.Reason
	}

	assert.Equal(t, "Waiting: queue at limit, 2 runs ahead",
		reason(fixedQueue{items: []queue.QueuedItemData{queuedRun("live-1"), queuedRun("live-2"), queuedRun(runID)}, running: 1}))
	assert.Equal(t, "Waiting: 1 run ahead in the queue",
		reason(fixedQueue{items: []queue.QueuedItemData{queuedRun("live-1"), queuedRun(runID)}}))
	assert.Equal(t, "Waiting: next in the queue", reason(fixedQueue{items: []queue.QueuedItemData{queuedRun(runID)}}))
	assert.Empty(t, reason(fixedQueue{}), "a run that left the queue gives no reason")

	f.finish(runID, ir.Waiting)
	assert.Equal(t, "Blocked: the run of slot 2026-10-01 02:00 is waiting for a person", reason(fixedQueue{}))
	// A blocked backfill names the run that waits, so the page links to it.
	report, err := backfill.Describe(f.ctx, f.store, f.dispatcher.ReadRun, f.record(record.ID))
	require.NoError(t, err)
	wait, err := backfill.WaitReason(f.ctx, report, backfill.QueueView{Items: fixedQueue{}, Procs: fixedQueue{}, Limit: func(string) int { return 1 }}, time.UTC)
	require.NoError(t, err)
	assert.Equal(t, runID, wait.RunID)

	f.finish(runID, ir.Succeeded)
	assert.Empty(t, reason(fixedQueue{}), "nothing waits")
}
