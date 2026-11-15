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
	// SpanSuspended: the DAG was suspended.
	SpanSuspended SpanReason = "suspended"
	// SpanPaused: an operator paused the scheduler.
	SpanPaused SpanReason = "paused"
	// SpanDown: no scheduler ran.
	SpanDown SpanReason = "down"
)

// SpanStore records the spans in which slots could not run, as they begin
// and end: the scheduler's own, which hold for every DAG and are named by
// the empty DAG, and each DAG's, named by its file name. It keeps only the
// spans that ended in the last 30 days, at most 100 for each name.
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
}
