// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

func isSchedulerManagedTriggerType(triggerType ir.TriggerType) bool {
	switch triggerType {
	case ir.TriggerTypeScheduler, ir.TriggerTypeCatchUp, ir.TriggerTypeRetry, ir.TriggerTypeRestart:
		return true
	case ir.TriggerTypeUnknown, ir.TriggerTypeManual, ir.TriggerTypeWebhook, ir.TriggerTypeSubDAG:
		return false
	}
	return false
}

func suspendFlagName(status *ir.DAGRunStatus, dag *ir.DAG, definitionID string) string {
	if statusDefinitionID := status.DAGDefinitionID(); statusDefinitionID != "" {
		return statusDefinitionID
	}
	if definitionID != "" {
		return definitionID
	}
	if dag != nil {
		if name := dag.SuspendFlagName(); name != "" {
			return name
		}
	}
	if status != nil {
		return status.Name
	}
	return ""
}

func isSuspendedDAG(
	ctx context.Context,
	isSuspended IsSuspendedFunc,
	status *ir.DAGRunStatus,
	dag *ir.DAG,
	definitionID string,
) (bool, error) {
	if isSuspended == nil {
		return false, nil
	}
	name := suspendFlagName(status, dag, definitionID)
	if name == "" {
		return false, nil
	}
	return isSuspended(ctx, name)
}

// newSuspensionChecker composes the cluster-wide scheduler pause with the
// per-DAG suspend flag. A pause reports every DAG as suspended, so every guard
// that already honors per-DAG suspension honors the pause without a separate
// code path.
//
// Read failures propagate. Every caller treats an error as "do not dispatch",
// which is the safe direction for an operator-driven pause.
func newSuspensionChecker(pauseStore schedulerstate.PauseStore, isSuspended IsSuspendedFunc) IsSuspendedFunc {
	if pauseStore == nil {
		return isSuspended
	}
	return func(ctx context.Context, dagName string) (bool, error) {
		paused, err := pauseStore.IsPaused(ctx)
		if err != nil || paused {
			return paused, err
		}
		return isSuspended(ctx, dagName)
	}
}
