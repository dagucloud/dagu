// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schedulerstate

import (
	"context"
	"time"
)

// Period is an ended stretch of time in which start-schedule slots could not
// run. A stored list holds the periods of one reason and leaves Reason
// empty; the list tells why.
type Period struct {
	From   time.Time    `json:"from"`
	To     time.Time    `json:"to"`
	Reason PeriodReason `json:"-"`
}

// Dismissal is who dismissed a DAG's missed slots for every user, and when.
type Dismissal struct {
	At time.Time `json:"at"`
	By string    `json:"by,omitempty"`
}

// PeriodReason is why slots could not run.
type PeriodReason string

const (
	PeriodSuspended PeriodReason = "suspended" // the DAG was suspended
	PeriodPaused    PeriodReason = "paused"    // an operator paused the scheduler
	PeriodDown      PeriodReason = "down"      // no scheduler ran
)

// PeriodStore records the ended periods in which slots could not run: the
// scheduler's outages, which hold for every DAG, and each DAG's suspensions,
// named by its file name. Each list keeps the 100 that ended last within 30
// days. Implementations must be safe for concurrent use across processes.
type PeriodStore interface {
	// AddDown records a time no scheduler ran.
	AddDown(ctx context.Context, down Period) error
	// AddSuspended records a DAG's ended suspension, or extends the one
	// that began at the same time to its later end.
	AddSuspended(ctx context.Context, dag string, suspended Period) error
	// Dismiss records that the DAG's missed slots were dismissed, unless a
	// later dismissal is recorded.
	Dismiss(ctx context.Context, dag string, dismissal Dismissal) error
	// Ended returns the periods that hold for the DAG: the scheduler's
	// outages, the operator's pauses, and the DAG's suspensions, oldest
	// first; and the DAG's latest dismissal.
	Ended(ctx context.Context, dag string) ([]Period, Dismissal, error)
}
