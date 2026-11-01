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
	case ir.TriggerTypeScheduler, ir.TriggerTypeCatchUp, ir.TriggerTypeRetry:
		return true
	case ir.TriggerTypeUnknown, ir.TriggerTypeManual, ir.TriggerTypeWebhook, ir.TriggerTypeSubDAG, ir.TriggerTypeFile:
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
// A wake hold is not such a pause: the scheduler placed it on itself to keep
// its own ticks back after the host woke (wake_hold.go), and no tick runs
// while it lasts, so the guards have nothing to suppress. Treating it as a
// pause would dequeue the runs already queued and discard catch-up state.
//
// Read failures propagate. Every caller treats an error as "do not dispatch",
// which is the safe direction for an operator-driven pause.
func newSuspensionChecker(pauseStore schedulerstate.PauseStore, isSuspended IsSuspendedFunc) IsSuspendedFunc {
	if pauseStore == nil {
		return isSuspended
	}
	return func(ctx context.Context, dagName string) (bool, error) {
		pause, err := pauseStore.Get(ctx)
		if err != nil {
			return false, err
		}
		if pause.Paused && !pause.WakeHold() {
			return true, nil
		}
		return isSuspended(ctx, dagName)
	}
}
