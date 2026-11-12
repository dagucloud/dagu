// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proc"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// writeQueuedRun writes a queued run of the fixture's DAG, a backfill run
// when backfillID is set.
func writeQueuedRun(t *testing.T, f *queueFixture, runID, backfillID string) ir.DAGRunRef {
	t.Helper()
	run, err := f.dagRunRepository.CreateAttempt(f.ctx, f.dag, time.Now(), runID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, run.Open(f.ctx))
	st := ir.InitialStatus(f.dag)
	st.Status, st.DAGRunID, st.AttemptID, st.BackfillID = ir.Queued, runID, run.ID(), backfillID
	require.NoError(t, run.Write(f.ctx, st))
	require.NoError(t, run.Close(f.ctx))
	return ir.NewDAGRunRef(f.dag.Name, runID)
}

// A queued backfill run does not keep a DAG busy for its live schedule; a
// queued live run does.
func TestLiveQueuedCheckerIgnoresBackfillRuns(t *testing.T) {
	f := newQueueFixture(t).withDAG("live-queued-dag", 1)
	check := liveQueuedChecker(f.queueStore, liveRunChecker(f.dagRunRepository))

	backfillRun := writeQueuedRun(t, f, "backfill-run", "bf123456")
	require.NoError(t, f.queueStore.Enqueue(f.ctx, f.dag.Name, queuedomain.QueuePriorityLow, backfillRun))
	busy, err := check(f.ctx, f.dag)
	require.NoError(t, err)
	assert.False(t, busy)

	liveRun := writeQueuedRun(t, f, "live-run", "")
	require.NoError(t, f.queueStore.Enqueue(f.ctx, f.dag.Name, queuedomain.QueuePriorityHigh, liveRun))
	busy, err = check(f.ctx, f.dag)
	require.NoError(t, err)
	assert.True(t, busy)
}

func TestLiveRunningCheckerIgnoresBackfillRuns(t *testing.T) {
	f := newQueueFixture(t).withDAG("live-running-dag", 1)
	backfillRun := writeQueuedRun(t, f, "backfill-run", "bf123456")
	liveRun := writeQueuedRun(t, f, "live-run", "")
	entry := func(ref ir.DAGRunRef) proc.ProcEntry {
		return proc.ProcEntry{Fresh: true, Meta: proc.ProcMeta{Name: ref.Name, DAGRunID: ref.ID}}
	}

	for _, tc := range []struct {
		name    string
		entries []proc.ProcEntry
		want    bool
	}{
		{"backfill run only", []proc.ProcEntry{entry(backfillRun)}, false},
		{"live run too", []proc.ProcEntry{entry(backfillRun), entry(liveRun)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			procRepository := &mockProcRepository{}
			procRepository.On("CountAliveByDAGName", mock.Anything, f.dag.ProcGroup(), f.dag.Name).Return(len(tc.entries), nil)
			procRepository.On("ListAllEntries", mock.Anything).Return(tc.entries, nil)

			busy, err := liveRunningChecker(procRepository, liveRunChecker(f.dagRunRepository))(f.ctx, f.dag)
			require.NoError(t, err)
			assert.Equal(t, tc.want, busy)
		})
	}
}
