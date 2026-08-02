// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core"
)

// createProgressReporter creates the progress reporter
func createProgressReporter(dag *core.DAG, dagRunID string, params []string) ProgressReporter {
	if dag != nil && dag.Type == core.TypeController {
		display := NewControllerProgressDisplay(dag)
		display.SetDAGRunInfo(dagRunID, strings.Join(params, " "))
		return display
	}
	display := NewSimpleProgressDisplay(dag)
	display.SetDAGRunInfo(dagRunID, strings.Join(params, " "))
	return display
}
