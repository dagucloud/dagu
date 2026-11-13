// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// FollowRetry queues again the slot of a backfill run that a retry is about
// to make active, so the backfill follows the run until the retry ends. A
// retry calls it first and fails when it fails. A run that is not the
// current run of a slot of its backfill is left alone.
func FollowRetry(ctx context.Context, store Store, status *ir.DAGRunStatus, now time.Time) error {
	if status.BackfillID == "" {
		return nil
	}
	record, err := store.Get(ctx, status.BackfillID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	progress, err := store.Progress(ctx, record.ID)
	if err != nil {
		return err
	}
	for _, event := range currentProgress(record, progress) {
		if event.RunID != status.DAGRunID || (event.Kind != ProgressEnqueued && !event.ended()) {
			continue
		}
		return store.Append(ctx, record.ID, Progress{
			Slot:       event.Slot,
			Kind:       ProgressEnqueued,
			RunID:      event.RunID,
			Generation: record.Generation,
			At:         now,
		})
	}
	return nil
}
