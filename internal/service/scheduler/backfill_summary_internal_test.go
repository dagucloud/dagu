// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	notificationservice "github.com/dagucloud/dagu/v2/internal/service/notification"
	"github.com/stretchr/testify/assert"
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
