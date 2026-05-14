// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaults_RetryPolicy(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	dag := th.DAG(t, `
defaults:
  retry_policy:
    limit: 2
    interval_sec: 0
    exit_code: [1]

steps:
  - name: failing-step
    run: exit 1
`)
	agent := dag.Agent()
	_ = agent.Run(agent.Context)

	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, core.NodeFailed, dagRunStatus.Nodes[0].Status)
	assert.Equal(t, 2, dagRunStatus.Nodes[0].RetryCount, "step should inherit retry limit from defaults")
}

func TestDefaults_ContinueOn(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	dag := th.DAG(t, `
defaults:
  continue_on: failed

steps:
  - name: fail-step
    run: exit 1

  - name: success-step
    run: echo "runs after failure"
`)
	agent := dag.Agent()
	_ = agent.Run(agent.Context)

	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, dagRunStatus.Nodes, 2)
	// Both steps should have executed
	assert.Equal(t, core.NodeFailed, dagRunStatus.Nodes[0].Status)
	assert.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[1].Status)
}

func TestDefaults_StepOverridesDefault(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	dag := th.DAG(t, `
defaults:
  retry_policy:
    limit: 5
    interval_sec: 0
    exit_code: [1]

steps:
  - name: failing-step
    run: exit 1
    retry_policy:
      limit: 1
      interval_sec: 0
      exit_code: [1]
`)
	agent := dag.Agent()
	_ = agent.Run(agent.Context)

	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, dagRunStatus.Nodes, 1)
	// Step should use its own retry limit (1), not the default (5)
	assert.Equal(t, 1, dagRunStatus.Nodes[0].RetryCount, "step should use its own retry_policy, not default")
}

func TestDefaults_AdditiveEnv(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	dag := th.DAG(t, `
defaults:
  env:
    - DEFAULT_KEY: default_value

steps:
  - name: step-with-both
    run: echo "${DEFAULT_KEY}_${STEP_KEY}"
    env:
      - STEP_KEY: step_value
    output: RESULT
`)
	agent := dag.Agent()
	agent.RunSuccess(t)

	dag.AssertOutputs(t, map[string]any{
		"RESULT": "default_value_step_value",
	})
}
