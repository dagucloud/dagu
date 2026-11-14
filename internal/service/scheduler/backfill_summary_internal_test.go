// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	filebackfill "github.com/dagucloud/dagu/v2/internal/persis/file/backfill"
	notificationservice "github.com/dagucloud/dagu/v2/internal/service/notification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An ended backfill's summary gives its slot counts by state, the time from
// its submission to its end, and its failure groups.
func TestBackfillSummaryOf(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	slot := time.Date(2026, 10, 2, 2, 0, 0, 0, time.UTC)
	summary := backfillSummaryOf(backfill.Report{
		Record: &backfill.Record{
			ID: "bf123456", DAGName: "etl", Labels: ir.NewLabels([]string{"workspace=ops"}),
			Status: backfill.StatusFailed, CreatedAt: created, EndedAt: created.Add(100 * time.Minute),
			Slots: make([]backfill.RecordSlot, 5),
		},
		Counts: map[backfill.SlotState]int{
			backfill.SlotStateSucceeded: 2, backfill.SlotStateFailed: 1,
			backfill.SlotStateSkipped: 1, backfill.SlotStateNotRun: 1,
		},
		Failures: []backfill.Failure{{Step: "load", Error: "connection refused", Slots: 1, At: slot, RunID: "run-1"}},
	})

	assert.Equal(t, notificationservice.BackfillSummary{
		ID: "bf123456", DAGName: "etl", Labels: []string{"workspace=ops"},
		Slots: 5, SucceededSlots: 2, FailedSlots: 1, SkippedSlots: 1, NotRunSlots: 1,
		Duration: 100 * time.Minute,
		Failures: []notificationservice.BackfillFailure{{Step: "load", Error: "connection refused", Slots: 1, Slot: slot, RunID: "run-1"}},
	}, summary)
}

// blockingNotifier holds each summary until released, as a target that
// takes long to answer.
type blockingNotifier struct {
	release chan struct{}
	sent    chan notificationservice.BackfillSummary
}

func (n *blockingNotifier) SendBackfillSummary(_ context.Context, summary notificationservice.BackfillSummary) error {
	<-n.release
	n.sent <- summary
	return nil
}

// A target that takes long to answer does not hold the dispatch loop: the
// summary is sent while the loop goes on.
func TestNotifyBackfillEndedDoesNotWaitForTargets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := filebackfill.New(t.TempDir())
	record := &backfill.Record{
		ID: "bf123456", DAGName: "etl", Status: backfill.StatusSucceeded,
		CreatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		EndedAt:   time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC),
	}
	require.NoError(t, store.Create(ctx, record))
	notifier := &blockingNotifier{release: make(chan struct{}), sent: make(chan notificationservice.BackfillSummary, 1)}
	s := &Scheduler{
		backfills: &backfill.Dispatcher{
			Store:   store,
			ReadRun: func(context.Context, ir.DAGRunRef) (*ir.DAGRunStatus, error) { return nil, nil },
		},
		backfillNotifier: notifier,
	}

	returned := make(chan struct{})
	go func() {
		s.notifyBackfillEnded(ctx, record)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("the dispatch loop waited for the target")
	}

	close(notifier.release)
	select {
	case summary := <-notifier.sent:
		assert.Equal(t, "bf123456", summary.ID)
	case <-time.After(5 * time.Second):
		t.Fatal("the summary was not sent")
	}
}
