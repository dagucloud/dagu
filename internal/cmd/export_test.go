// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func RestoreDAGFromStatusForTest(ctx context.Context, dag *core.DAG, status *exec.DAGRunStatus) (*core.DAG, error) {
	return restoreDAGFromStatus(ctx, dag, status)
}
