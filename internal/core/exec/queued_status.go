// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import "github.com/dagucloud/dagu/internal/core"

// IsQueuedCatchup reports whether the queued status belongs to a catchup run.
func IsQueuedCatchup(status *DAGRunStatus) bool {
	return status != nil &&
		status.Status == core.Queued &&
		status.TriggerType == core.TriggerTypeCatchUp
}

// PreservedQueueTriggerType returns the trigger type that must be preserved
// when consuming a queued item. Queued retry records still execute as retries;
// initial queued runs keep the trigger that originally enqueued them.
func PreservedQueueTriggerType(status *DAGRunStatus) core.TriggerType {
	if status == nil || status.Status != core.Queued || status.TriggerType == core.TriggerTypeRetry {
		return core.TriggerTypeUnknown
	}
	return status.TriggerType
}
