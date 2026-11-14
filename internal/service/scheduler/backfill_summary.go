// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"log/slog"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	notificationservice "github.com/dagucloud/dagu/v2/internal/service/notification"
)

// backfillNotifier sends the summary of an ended backfill.
type backfillNotifier interface {
	SendBackfillSummary(ctx context.Context, summary notificationservice.BackfillSummary) error
}

// notifyBackfillEnded sends one summary of a backfill that succeeded or
// failed. A cancelled backfill sends none, and its runs sent none.
func (s *Scheduler) notifyBackfillEnded(ctx context.Context, record *backfill.Record) {
	if s.backfillNotifier == nil || s.backfills == nil ||
		(record.Status != backfill.StatusSucceeded && record.Status != backfill.StatusFailed) {
		return
	}
	report, err := backfill.Describe(ctx, s.backfills.Store, s.backfills.ReadRun, record)
	if err != nil {
		logger.Warn(ctx, "Failed to describe an ended backfill", slog.String("backfill", record.ID), tag.Error(err))
		return
	}
	if err := s.backfillNotifier.SendBackfillSummary(ctx, backfillSummaryOf(report)); err != nil {
		logger.Warn(ctx, "Failed to send a backfill's summary", slog.String("backfill", record.ID), tag.Error(err))
	}
}

// backfillSummaryOf is the summary of an ended backfill's report.
func backfillSummaryOf(report backfill.Report) notificationservice.BackfillSummary {
	record := report.Record
	summary := notificationservice.BackfillSummary{
		ID:             record.ID,
		DAGName:        record.DAGName,
		Labels:         record.Labels.Strings(),
		Succeeded:      record.Status == backfill.StatusSucceeded,
		Slots:          len(record.Slots),
		SucceededSlots: report.Counts[backfill.SlotStateSucceeded],
		FailedSlots:    report.Counts[backfill.SlotStateFailed],
		SkippedSlots:   report.Counts[backfill.SlotStateSkipped],
		NotRunSlots:    report.Counts[backfill.SlotStateNotRun],
		Duration:       record.EndedAt.Sub(record.CreatedAt),
	}
	for _, failure := range report.Failures {
		summary.Failures = append(summary.Failures, notificationservice.BackfillFailure{
			Step:  failure.Step,
			Error: failure.Error,
			Slots: failure.Slots,
			Slot:  failure.At,
			RunID: failure.RunID,
		})
	}
	return summary
}
