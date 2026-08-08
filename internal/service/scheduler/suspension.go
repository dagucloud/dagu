// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

func isSchedulerManagedTriggerType(triggerType ir.TriggerType) bool {
	switch triggerType {
	case ir.TriggerTypeScheduler, ir.TriggerTypeCatchUp, ir.TriggerTypeRetry:
		return true
	case ir.TriggerTypeUnknown, ir.TriggerTypeManual, ir.TriggerTypeWebhook, ir.TriggerTypeSubDAG:
		return false
	}
	return false
}

func suspendFlagName(status *dagrun.DAGRunStatus, dag *ir.DAG) string {
	if status != nil && status.SuspendFlagName != "" {
		return status.SuspendFlagName
	}
	if dag != nil {
		if name := dagSuspendFlagName(dag); name != "" {
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
	status *dagrun.DAGRunStatus,
	dag *ir.DAG,
) bool {
	if isSuspended == nil {
		return false
	}
	name := suspendFlagName(status, dag)
	if name == "" {
		return false
	}
	return isSuspended(ctx, name)
}
