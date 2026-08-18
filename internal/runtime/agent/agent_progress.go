// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// createProgressReporter creates the progress reporter
func createProgressReporter(dag *ir.DAG, dagRunID string, params []string) ProgressReporter {
	if dag != nil && dag.Type == ir.TypeAgent {
		display := NewAgentDAGProgressDisplay(dag)
		display.SetDAGRunInfo(dagRunID, strings.Join(params, " "))
		return display
	}
	display := NewSimpleProgressDisplay(dag)
	display.SetDAGRunInfo(dagRunID, strings.Join(params, " "))
	return display
}
