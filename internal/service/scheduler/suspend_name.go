// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import "github.com/dagucloud/dagu/v2/internal/ir"

// dagSuspendFlagName returns the filename stem used by the file-based suspend
// flag system. This intentionally follows DAG file naming, not dag.Name.
func dagSuspendFlagName(dag *ir.DAG) string {
	return dag.SuspendFlagName()
}
