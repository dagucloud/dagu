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

// blockingNotifier holds each summary until released or cancelled, as a
// target that takes long to answer, and reports how each send ended.
type blockingNotifier struct {
	started chan struct{}
	release chan struct{}
	sent    chan error
}

func newBlockingNotifier() *blockingNotifier {
	return &blockingNotifier{started: make(chan struct{}, 1), release: make(chan struct{}), sent: make(chan error, 1)}
}

func (n *blockingNotifier) SendBackfillSummary(ctx context.Context, _ notificationservice.BackfillSummary) error {
	n.started <- struct{}{}
	select {
	case <-n.release:
		n.sent <- nil
	case <-ctx.Done():
		n.sent <- ctx.Err()
	}
	return nil
}

// newSummaryScheduler is a scheduler that sends the summaries of ended
// backfills to notifier, with its summary worker running until the test
// ends or the scheduler stops.
func newSummaryScheduler(t *testing.T, notifier backfillNotifier) (*Scheduler, *backfill.Record, chan struct{}) {
	t.Helper()
	ctx := context.Background()
	store := filebackfill.New(t.TempDir())
	record := &backfill.Record{
		ID: "bf123456", DAGName: "etl", Status: backfill.StatusSucceeded,
		CreatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		EndedAt:   time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC),
	}
	require.NoError(t, store.Create(ctx, record))
	s := &Scheduler{
		quit: make(chan any),
		backfills: &backfill.Dispatcher{
			Store:   store,
			ReadRun: func(context.Context, ir.DAGRunRef) (*ir.DAGRunStatus, error) { return nil, nil },
		},
		backfillNotifier:  notifier,
		backfillSummaries: make(chan *backfill.Record, backfillSummaryQueueSize),
	}
	stopped := make(chan struct{})
	go func() {
		s.sendBackfillSummaries(ctx)
		close(stopped)
	}()
	t.Cleanup(func() {
		select {
		case <-s.quit:
		default:
			close(s.quit)
		}
		<-stopped
	})
	return s, record, stopped
}

// A target that takes long to answer does not hold the dispatch loop: the
// summary is sent while the loop goes on.
func TestNotifyBackfillEndedDoesNotWaitForTargets(t *testing.T) {
	t.Parallel()

	notifier := newBlockingNotifier()
	s, record, _ := newSummaryScheduler(t, notifier)

	returned := make(chan struct{})
	go func() {
		s.notifyBackfillEnded(context.Background(), record)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("the dispatch loop waited for the target")
	}

	close(notifier.release)
	select {
	case err := <-notifier.sent:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the summary was not sent")
	}
}

// Stopping the scheduler cancels the summary being sent and ends the worker.
func TestStopCancelsBackfillSummary(t *testing.T) {
	t.Parallel()

	notifier := newBlockingNotifier()
	s, record, stopped := newSummaryScheduler(t, notifier)
	s.notifyBackfillEnded(context.Background(), record)
	select {
	case <-notifier.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the summary was not sent")
	}
	close(s.quit)

	select {
	case err := <-notifier.sent:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the summary was not cancelled")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the summary worker did not stop")
	}
}
