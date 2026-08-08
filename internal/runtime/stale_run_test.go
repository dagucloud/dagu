// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepairStaleLocalRunDoesNotMutateReadStatusSnapshot(t *testing.T) {
	t.Parallel()

	sharedStatus := &dagrun.DAGRunStatus{
		Name:       "test",
		DAGRunID:   "run-1",
		AttemptID:  "attempt-1",
		Status:     ir.Running,
		StartedAt:  time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		FinishedAt: dagrun.FormatTime(time.Time{}),
		Nodes: []*dagrun.Node{
			{
				Step:   ir.Step{Name: "step-1"},
				Status: ir.NodeRunning,
			},
		},
	}

	attempt := &testutil.MockDAGRunAttempt{Status: sharedStatus}
	attempt.On("Open", mock.Anything).Return(nil).Once()

	var written dagrun.DAGRunStatus
	attempt.
		On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			written = args.Get(1).(dagrun.DAGRunStatus)
		}).
		Return(nil).
		Once()
	attempt.On("Close", mock.Anything).Return(nil).Once()

	repaired, repairedNow, err := runtime.RepairStaleLocalRun(context.Background(), attempt, &ir.DAG{})
	require.NoError(t, err)
	require.True(t, repairedNow)
	require.NotNil(t, repaired)
	require.NotSame(t, sharedStatus, repaired)
	require.NotSame(t, sharedStatus.Nodes[0], repaired.Nodes[0])

	require.Equal(t, ir.Running, sharedStatus.Status)
	require.Equal(t, ir.NodeRunning, sharedStatus.Nodes[0].Status)
	require.Empty(t, sharedStatus.Nodes[0].Error)

	require.Equal(t, ir.Failed, repaired.Status)
	require.Equal(t, ir.NodeFailed, repaired.Nodes[0].Status)
	require.Equal(t, staleLocalRunErrorText(), repaired.Nodes[0].Error)
	require.Equal(t, ir.Failed, written.Status)
	require.Equal(t, ir.NodeFailed, written.Nodes[0].Status)
	require.Equal(t, staleLocalRunErrorText(), written.Nodes[0].Error)

	attempt.AssertExpectations(t)
}

func staleLocalRunErrorText() string {
	return "process terminated unexpectedly - stale local process detected"
}
