// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagwarning

import (
	"context"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/core"
)

// LoadDotEnv loads dotenv files, logs warnings added by that load, and returns new build errors.
func LoadDotEnv(ctx context.Context, dag *core.DAG) error {
	if dag == nil {
		return nil
	}

	warningStart := len(dag.BuildWarnings)
	errorStart := len(dag.BuildErrors)
	dag.LoadDotEnv(ctx)
	Log(ctx, dag.BuildWarnings[warningStart:])
	if len(dag.BuildErrors) > errorStart {
		return core.ErrorList(dag.BuildErrors[errorStart:])
	}
	return nil
}

// Log emits DAG build warnings through Dagu's logger.
func Log(ctx context.Context, warnings []string) {
	for _, warning := range warnings {
		logger.Warn(ctx, warning)
	}
}
