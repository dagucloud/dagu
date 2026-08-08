// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

func resolveChildRunParams(
	ctx context.Context,
	childDAG *ir.DAG,
	runParams executor.RunParams,
) (executor.RunParams, error) {
	if len(runParams.WorkerSelector) > 0 {
		return runParams, nil
	}
	if len(childDAG.WorkerSelector) == 0 {
		return runParams, nil
	}

	resolved, err := spec.ResolveRuntimeParams(ctx, childDAG, runParams.Params, spec.ResolveRuntimeParamsOptions{
		BaseConfig: config.GetConfig(ctx).Paths.BaseConfig,
	})
	if err != nil {
		return executor.RunParams{}, fmt.Errorf("resolve sub-DAG routing parameters: %w", err)
	}
	runParams.WorkerSelector = resolved.WorkerSelector
	runParams.Params = strings.Join(spec.QuoteRuntimeParams(resolved.Params, resolved.ParamDefs), " ")
	return runParams, nil
}
