// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

func TestPreservedQueueTriggerType(t *testing.T) {
	t.Parallel()

	require.Equal(t, ir.TriggerTypeWebhook, exec.PreservedQueueTriggerType(&dagrun.DAGRunStatus{
		Status:      ir.Queued,
		TriggerType: ir.TriggerTypeWebhook,
	}))
	require.Equal(t, ir.TriggerTypeCatchUp, exec.PreservedQueueTriggerType(&dagrun.DAGRunStatus{
		Status:      ir.Queued,
		TriggerType: ir.TriggerTypeCatchUp,
	}))
	require.Equal(t, ir.TriggerTypeUnknown, exec.PreservedQueueTriggerType(&dagrun.DAGRunStatus{
		Status:      ir.Queued,
		TriggerType: ir.TriggerTypeRetry,
	}))
	require.Equal(t, ir.TriggerTypeUnknown, exec.PreservedQueueTriggerType(&dagrun.DAGRunStatus{
		Status:      ir.Succeeded,
		TriggerType: ir.TriggerTypeWebhook,
	}))
}
