// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"

	"github.com/dagucloud/dagu/internal/core/exec"
)

// Scheduler publishes run scheduling intent outside the History service.
type Scheduler interface {
	ScheduleRun(context.Context, ScheduleRequest) error
}

// ScheduleFunc adapts a function into a Scheduler.
type ScheduleFunc func(context.Context, ScheduleRequest) error

// ScheduleRun publishes the scheduling intent.
func (f ScheduleFunc) ScheduleRun(ctx context.Context, req ScheduleRequest) error {
	return f(ctx, req)
}

// ScheduleRequest is the dispatch intent emitted by History after a lifecycle transition.
type ScheduleRequest struct {
	QueueName string
	Priority  exec.QueuePriority
	DAGRun    exec.DAGRunRef
}
