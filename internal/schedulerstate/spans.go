// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schedulerstate

import (
	"context"
	"time"
)

// Period is an ended stretch of time in which start-schedule slots could not
// run. The list that holds it tells why.
type Period struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Span is a Period with why slots could not run in it.
type Span struct {
	From, To time.Time
	Reason   SpanReason
}

// SpanReason is why slots could not run.
type SpanReason string

const (
	SpanSuspended SpanReason = "suspended" // the DAG was suspended
	SpanPaused    SpanReason = "paused"    // an operator paused the scheduler
	SpanDown      SpanReason = "down"      // no scheduler ran
)

// SpanStore records the ended periods in which slots could not run: the
// scheduler's outages, which hold for every DAG, and each DAG's suspensions,
// named by its file name. Each list keeps the 100 that ended last within 30
// days. Implementations must be safe for concurrent use across processes.
type SpanStore interface {
	// AddDown records a time no scheduler ran.
	AddDown(ctx context.Context, down Period) error
	// AddSuspended records a DAG's ended suspension, unless one that began
	// at the same time is recorded.
	AddSuspended(ctx context.Context, dag string, suspended Period) error
	// Ended returns the spans that hold for the DAG: the scheduler's
	// outages, the operator's pauses, and the DAG's suspensions, oldest
	// first.
	Ended(ctx context.Context, dag string) ([]Span, error)
}
