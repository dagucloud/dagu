// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/queue"
)

// QueueView reads the queues a waiting backfill run sees.
type QueueView struct {
	Items QueueLister
	Procs RunCounter
	// Limit returns how many runs of a queue run at once.
	Limit func(queueName string) int
}

// QueueLister lists a queue's items in the order they start.
type QueueLister interface {
	List(ctx context.Context, name string) ([]queue.QueuedItemData, error)
}

// RunCounter counts a queue's runs in progress.
type RunCounter interface {
	CountAlive(ctx context.Context, groupName string) (int, error)
}

// WaitReason explains why an open backfill makes no progress: a run of one
// of its slots waits for a person, or its next run waits in the queue. It
// is empty when neither holds. Slot times are shown in loc.
func WaitReason(ctx context.Context, report Report, q QueueView, loc *time.Location) (string, error) {
	if !report.Record.Open() {
		return "", nil
	}
	nextRunID := ""
	for _, slot := range report.Slots {
		if slot.State == SlotStateWaiting {
			return fmt.Sprintf("Blocked: the run of slot %s is waiting for a person", slot.At.In(loc).Format(slotTimeLayout)), nil
		}
		if slot.State == SlotStateQueued && nextRunID == "" {
			nextRunID = slot.RunID
		}
	}
	if nextRunID == "" {
		return "", nil
	}
	items, err := q.Items.List(ctx, report.queue)
	if err != nil {
		return "", err
	}
	ahead := -1
	for i, item := range items {
		if ref, err := item.Data(); err == nil && ref.ID == nextRunID {
			ahead = i
			break
		}
	}
	if ahead < 0 {
		// The run left the queue since the report was read.
		return "", nil
	}
	running, err := q.Procs.CountAlive(ctx, report.queue)
	if err != nil {
		return "", err
	}
	atLimit := running >= max(q.Limit(report.queue), 1)
	switch {
	case atLimit && ahead > 0:
		return fmt.Sprintf("Waiting: queue at limit, %s ahead", countOf(ahead, "run")), nil
	case atLimit:
		return "Waiting: queue at limit", nil
	case ahead > 0:
		return fmt.Sprintf("Waiting: %s ahead in the queue", countOf(ahead, "run")), nil
	default:
		return "Waiting: next in the queue", nil
	}
}

// slotTimeLayout is how a reason names a slot.
const slotTimeLayout = "2006-01-02 15:04"

func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
