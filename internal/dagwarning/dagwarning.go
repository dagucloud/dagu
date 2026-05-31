// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagwarning

import (
	"context"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/core"
)

// LoadDotEnv loads dotenv files and logs only warnings added by that load.
func LoadDotEnv(ctx context.Context, dag *core.DAG) {
	if dag == nil {
		return
	}

	start := len(dag.BuildWarnings)
	dag.LoadDotEnv(ctx)
	Log(ctx, dag.BuildWarnings[start:])
}

// Log emits DAG build warnings through Dagu's logger.
func Log(ctx context.Context, warnings []string) {
	for _, warning := range warnings {
		logger.Warn(ctx, warning)
	}
}
