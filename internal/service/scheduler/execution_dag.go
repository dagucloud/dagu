// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// rehydrateExecutionDAG reloads a full DAG from source before scheduler-owned
// execution or persistence paths use it as an execution snapshot.
func rehydrateExecutionDAG(
	ctx context.Context,
	dag *ir.DAG,
	params any,
	baseConfig string,
	workspaceBaseConfigDir string,
) (*ir.DAG, error) {
	fresh, err := spec.ResolveRuntimeParams(ctx, dag, params, spec.ResolveRuntimeParamsOptions{
		BaseConfig:             baseConfig,
		WorkspaceBaseConfigDir: workspaceBaseConfigDir,
	})
	if err != nil {
		return nil, fmt.Errorf("rehydrate execution DAG: %w", err)
	}
	return fresh, nil
}
