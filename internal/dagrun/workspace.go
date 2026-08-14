// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import "github.com/dagucloud/dagu/v2/internal/ir"

// DAGRunWorkspaceRef identifies the execution workspace belonging to a DAG run.
type DAGRunWorkspaceRef struct {
	RootDAGRun ir.DAGRunRef
	DAGRun     ir.DAGRunRef
}
