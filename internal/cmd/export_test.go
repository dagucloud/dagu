// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

func RestoreDAGFromStatusForTest(ctx context.Context, dag *ir.DAG, status *ir.DAGRunStatus) (*ir.DAG, error) {
	return restoreDAGFromStatus(ctx, dag, status, nil)
}

func WorkerStopContextForTest(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return workerStopContext(parent, timeout)
}

var WorkerFlagsForTest = workerFlags
