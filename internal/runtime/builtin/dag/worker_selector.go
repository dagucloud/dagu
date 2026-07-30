// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

func effectiveWorkerSelector(
	ctx context.Context,
	childDAG *core.DAG,
	runParams executor.RunParams,
) map[string]string {
	if len(runParams.WorkerSelector) > 0 {
		return runParams.WorkerSelector
	}
	if childDAG == nil || len(childDAG.WorkerSelector) == 0 {
		return nil
	}

	resolved, err := spec.ResolveRuntimeParams(ctx, childDAG, runParams.Params, spec.ResolveRuntimeParamsOptions{
		BaseConfig: config.GetConfig(ctx).Paths.BaseConfig,
	})
	if err != nil {
		// Child execution owns runtime parameter validation.
		return childDAG.WorkerSelector
	}
	return resolved.WorkerSelector
}
