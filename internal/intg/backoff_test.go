// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryPolicy_WithExponentialBackoff(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	// Load DAG with retry backoff
	dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: failing-step
    %s
    retry_policy:
      limit: 3
      interval_sec: 1
      backoff: 2.0
      exit_code: [1]
`, portableDirectFailureStepYAML(t)))
	agent := dag.Agent()

	// Record start time
	startTime := time.Now()

	// Run with timeout
	ctx, cancel := context.WithTimeout(agent.Context, 30*time.Second)
	defer cancel()

	// Run the DAG (it will fail due to retry limit)
	_ = agent.Run(ctx)

	// Get the latest status
	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.NotNil(t, dagRunStatus)

	// Verify the step failed after retries
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, ir.NodeFailed, dagRunStatus.Nodes[0].Status)
	assert.Equal(t, "failing-step", dagRunStatus.Nodes[0].Step.Name)

	// Verify it retried exactly 3 times
	assert.Equal(t, 3, dagRunStatus.Nodes[0].RetryCount, "Step should have retried exactly 3 times")

	// Verify total time is approximately correct
	// Expected intervals: initial, then 1s, 2s, 4s = 7s minimum
	totalTime := time.Since(startTime)
	assert.GreaterOrEqual(t, totalTime, 6*time.Second, "Total time should be at least 6 seconds")
}

func TestRetryPolicy_WithBackoffBoolean(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	// Load DAG with boolean backoff
	dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: failing-step
    %s
    retry_policy:
      limit: 3
      interval_sec: 1
      backoff: true  # Should use default 2.0 multiplier
      exit_code: [1]
`, portableDirectFailureStepYAML(t)))
	agent := dag.Agent()

	// Record start time
	startTime := time.Now()

	// Run with timeout
	ctx, cancel := context.WithTimeout(agent.Context, 30*time.Second)
	defer cancel()

	// Run the DAG (it will fail due to retry limit)
	_ = agent.Run(ctx)

	// Get the latest status
	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.NotNil(t, dagRunStatus)

	// Verify the step failed after retries
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, ir.NodeFailed, dagRunStatus.Nodes[0].Status)
	assert.Equal(t, 3, dagRunStatus.Nodes[0].RetryCount)

	// Verify timing (backoff: true should use 2.0 multiplier)
	// Expected intervals: initial, then 1s, 2s, 4s = 7s minimum
	totalTime := time.Since(startTime)
	assert.GreaterOrEqual(t, totalTime, 6*time.Second, "Total time should be at least 6 seconds")
}

func TestRepeatPolicy_WithExponentialBackoff(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	// Load DAG with repeat backoff
	dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: repeat-step
    %s
    repeat_policy:
      repeat: while
      limit: 4
      interval_sec: 1
      backoff: 2.0
      exit_code: [0]  # Repeat while exit code is 0
`, portableDirectSuccessStepYAML(t)))
	agent := dag.Agent()

	// Record start time
	startTime := time.Now()

	// Run with timeout
	ctx, cancel := context.WithTimeout(agent.Context, 30*time.Second)
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err, "DAG should complete successfully")

	// Verify successful completion
	dag.AssertLatestStatus(t, ir.Succeeded)

	// Get the latest status
	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.NotNil(t, dagRunStatus)

	// Verify it repeated exactly 4 times
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, ir.NodeSucceeded, dagRunStatus.Nodes[0].Status)
	assert.Equal(t, 4, dagRunStatus.Nodes[0].DoneCount, "Step should have executed exactly 4 times")

	// Verify timing
	// Expected intervals: initial, then 1s, 2s, 4s = 7s minimum
	totalTime := time.Since(startTime)
	assert.GreaterOrEqual(t, totalTime, 6*time.Second, "Total time should be at least 6 seconds")
}
