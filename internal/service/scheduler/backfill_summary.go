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

// backfillSummaryQueueSize bounds the summaries waiting to be sent.
// Backfills end rarely, so a full queue means the targets are not
// answering, and a summary that does not fit is dropped.
const backfillSummaryQueueSize = 32

// notifyBackfillEnded queues one summary of a backfill that succeeded or
// failed. A cancelled backfill sends none, and its runs sent none. The
// dispatch loop calls it, so it never waits: a target can take long to
// answer.
func (s *Scheduler) notifyBackfillEnded(ctx context.Context, record *backfill.Record) {
	if s.backfillSummaries == nil ||
		(record.Status != backfill.StatusSucceeded && record.Status != backfill.StatusFailed) {
		return
	}
	select {
	case s.backfillSummaries <- record:
	default:
		logger.Warn(ctx, "Dropped a backfill's summary: earlier summaries are still being sent", slog.String("backfill", record.ID))
	}
}

// sendBackfillSummaries sends the queued summaries one at a time until the
// scheduler stops, which also cancels the summary being sent.
func (s *Scheduler) sendBackfillSummaries(ctx context.Context) {
	if s.backfillSummaries == nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-s.quit:
			cancel()
		case <-ctx.Done():
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case record := <-s.backfillSummaries:
			s.sendBackfillSummary(ctx, record)
		}
	}
}

// sendBackfillSummary describes an ended backfill and sends its summary.
func (s *Scheduler) sendBackfillSummary(ctx context.Context, record *backfill.Record) {
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
