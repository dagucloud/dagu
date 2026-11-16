// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schedulerstate

import (
	"context"
	"time"
)

// Span is a stretch of time in which start-schedule slots could not run.
type Span struct {
	From   time.Time  `json:"from"`
	To     time.Time  `json:"to"`
	Reason SpanReason `json:"reason"`
}

// SpanReason is why slots could not run.
type SpanReason string

const (
	SpanSuspended SpanReason = "suspended" // the DAG was suspended
	SpanPaused    SpanReason = "paused"    // an operator paused the scheduler
	SpanDown      SpanReason = "down"      // no scheduler ran
)

// SpanStore records the spans in which slots could not run once they end:
// the scheduler's outages, named by the empty DAG, which hold for every DAG,
// and each DAG's suspensions, named by its file name. For each name it keeps
// the 100 that ended last within 30 days. Implementations must be safe for
// concurrent use across processes.
type SpanStore interface {
	// Add records a span that has ended, unless one of the name that began
	// at the same time is recorded.
	Add(ctx context.Context, dag string, span Span) error
	// Ended returns the spans that hold for the DAG and have ended: the
	// scheduler's outages, the operator's pauses, and the DAG's own, oldest
	// first.
	Ended(ctx context.Context, dag string) ([]Span, error)
	// Move moves the spans of a DAG renamed from oldDAG to newDAG.
	Move(ctx context.Context, oldDAG, newDAG string) error
	// Remove removes the spans of a deleted DAG.
	Remove(ctx context.Context, dag string) error
}
