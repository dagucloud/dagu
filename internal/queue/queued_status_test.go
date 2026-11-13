// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/stretchr/testify/require"
)

func TestPreservedQueueTriggerType(t *testing.T) {
	t.Parallel()

	require.Equal(t, ir.TriggerTypeWebhook, queue.PreservedQueueTriggerType(&ir.DAGRunStatus{
		Status:      ir.Queued,
		TriggerType: ir.TriggerTypeWebhook,
	}))
	require.Equal(t, ir.TriggerTypeCatchUp, queue.PreservedQueueTriggerType(&ir.DAGRunStatus{
		Status:      ir.Queued,
		TriggerType: ir.TriggerTypeCatchUp,
	}))
	require.Equal(t, ir.TriggerTypeUnknown, queue.PreservedQueueTriggerType(&ir.DAGRunStatus{
		Status:      ir.Queued,
		TriggerType: ir.TriggerTypeRetry,
	}))
	require.Equal(t, ir.TriggerTypeUnknown, queue.PreservedQueueTriggerType(&ir.DAGRunStatus{
		Status:      ir.Succeeded,
		TriggerType: ir.TriggerTypeWebhook,
	}))
}

func TestExecutionTriggerType(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status *ir.DAGRunStatus
		want   ir.TriggerType
	}{
		{"queued run keeps its trigger", &ir.DAGRunStatus{Status: ir.Queued, TriggerType: ir.TriggerTypeWebhook}, ir.TriggerTypeWebhook},
		{"queued backfill run", &ir.DAGRunStatus{Status: ir.Queued, TriggerType: ir.TriggerTypeBackfill, BackfillID: "bf123456"}, ir.TriggerTypeBackfill},
		{"queued retry", &ir.DAGRunStatus{Status: ir.Queued, TriggerType: ir.TriggerTypeRetry}, ir.TriggerTypeRetry},
		{"retry of a finished run", &ir.DAGRunStatus{Status: ir.Failed, TriggerType: ir.TriggerTypeScheduler}, ir.TriggerTypeRetry},
		{"queued retry of a backfill run", &ir.DAGRunStatus{Status: ir.Queued, TriggerType: ir.TriggerTypeRetry, BackfillID: "bf123456"}, ir.TriggerTypeBackfill},
		{"retry of a finished backfill run", &ir.DAGRunStatus{Status: ir.Failed, TriggerType: ir.TriggerTypeBackfill, BackfillID: "bf123456"}, ir.TriggerTypeBackfill},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, queue.ExecutionTriggerType(tc.status))
		})
	}
}

func TestPriorityOf(t *testing.T) {
	t.Parallel()

	require.Equal(t, queue.QueuePriorityHigh, queue.PriorityOf(&ir.DAGRunStatus{TriggerType: ir.TriggerTypeScheduler}))
	require.Equal(t, queue.QueuePriorityHigh, queue.PriorityOf(&ir.DAGRunStatus{TriggerType: ir.TriggerTypeRetry}))
	require.Equal(t, queue.QueuePriorityLow, queue.PriorityOf(&ir.DAGRunStatus{TriggerType: ir.TriggerTypeRetry, BackfillID: "bf123456"}))
}
