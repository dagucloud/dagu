// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schedulerstate

import (
	"context"
	"time"
)

// Span is a stretch of time in which start-schedule slots could not run.
type Span struct {
	From time.Time `json:"from"`
	// To is zero while the span lasts.
	To     time.Time  `json:"to,omitzero"`
	Reason SpanReason `json:"reason"`
}

// SpanReason is why slots could not run.
type SpanReason string

const (
	SpanSuspended SpanReason = "suspended" // the DAG was suspended
	SpanPaused    SpanReason = "paused"    // an operator paused the scheduler
	SpanDown      SpanReason = "down"      // no scheduler ran
)

// Dismissal records who dismissed a DAG's missed slots, and when: it covers
// the slots of the spans that ended by Through.
type Dismissal struct {
	Through time.Time `json:"through"`
	By      string    `json:"by,omitempty"`
}

// SpanStore records the spans in which slots could not run, as they begin
// and end: the scheduler's own, which hold for every DAG and are named by
// the empty DAG, and each DAG's, named by its file name. For each name it
// keeps the span that lasts, and the 100 that ended last within 30 days. It
// also keeps the latest dismissal of each DAG's missed slots.
// Implementations must be safe for concurrent use across processes.
type SpanStore interface {
	// Mark records that slots stopped running for reason at at, when held,
	// or ran again. It begins no second span while one of reason lasts,
	// and ending none is not an error.
	Mark(ctx context.Context, dag string, reason SpanReason, held bool, at time.Time) error
	// Add records a span that has ended.
	Add(ctx context.Context, dag string, span Span) error
	// Ended returns the spans of the scheduler and of the DAG that have
	// ended, oldest first.
	Ended(ctx context.Context, dag string) ([]Span, error)
	// Dismiss records a dismissal of the DAG's missed slots in place of
	// the one before.
	Dismiss(ctx context.Context, dag string, dismissal Dismissal) error
	// Dismissed returns the DAG's latest dismissal, or the zero Dismissal.
	Dismissed(ctx context.Context, dag string) (Dismissal, error)
}
