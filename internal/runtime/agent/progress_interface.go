// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// ProgressReporter is the interface for progress display implementations
type ProgressReporter interface {
	// Start begins the progress display
	Start()

	// Stop stops the progress display
	Stop()

	// UpdateNode updates the progress for a specific node
	UpdateNode(node *ir.Node)

	// UpdateStatus updates the overall DAG status
	UpdateStatus(status *dagrun.DAGRunStatus)

	// SetDAGRunInfo sets the DAG run ID and parameters
	SetDAGRunInfo(dagRunID, params string)
}

// Ensure implementation satisfies the interface
var _ ProgressReporter = (*SimpleProgressDisplay)(nil)
