// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dispatch holds control-plane policy for deciding how a DAG run is
// executed. It is platform code: it combines language-level DAG fields with
// runtime configuration to choose between local and coordinator-dispatched
// execution.
package dispatch

import (
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
)

// ShouldDispatchToCoordinator decides whether a DAG should be dispatched
// to the coordinator for distributed execution.
func ShouldDispatchToCoordinator(dag *core.DAG, hasCoordinator bool, defaultMode config.ExecutionMode) bool {
	if dag.ForceLocal {
		return false
	}
	if !hasCoordinator {
		return false
	}
	if len(dag.WorkerSelector) > 0 {
		return true
	}
	if defaultMode == config.ExecutionModeDistributed {
		return true
	}
	return false
}
