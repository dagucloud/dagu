// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagwarning

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
)

// Log emits DAG warnings through Dagu's logger.
func Log(ctx context.Context, warnings []string) {
	for _, warning := range warnings {
		logger.Warn(ctx, warning)
	}
}
