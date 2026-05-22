// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/require"
)

func TestPreservedQueueTriggerType(t *testing.T) {
	t.Parallel()

	require.Equal(t, core.TriggerTypeWebhook, exec.PreservedQueueTriggerType(&exec.DAGRunStatus{
		Status:      core.Queued,
		TriggerType: core.TriggerTypeWebhook,
	}))
	require.Equal(t, core.TriggerTypeCatchUp, exec.PreservedQueueTriggerType(&exec.DAGRunStatus{
		Status:      core.Queued,
		TriggerType: core.TriggerTypeCatchUp,
	}))
	require.Equal(t, core.TriggerTypeUnknown, exec.PreservedQueueTriggerType(&exec.DAGRunStatus{
		Status:      core.Queued,
		TriggerType: core.TriggerTypeRetry,
	}))
	require.Equal(t, core.TriggerTypeUnknown, exec.PreservedQueueTriggerType(&exec.DAGRunStatus{
		Status:      core.Succeeded,
		TriggerType: core.TriggerTypeWebhook,
	}))
}
