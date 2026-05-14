// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	exec1 "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParams_DistributedSubDAGInlineDefsSuccess(t *testing.T) {
	f := newTestFixture(t, `
steps:
  - name: invoke-child
    action: dag.run
    with:
      dag: worker-inline-child
      params: "region=us-west-2 count=5 debug=true"

---
name: worker-inline-child
worker_selector:
  type: test-worker
params:
  - name: region
    type: string
    enum: [us-east-1, us-west-2]
    required: true
  - name: count
    type: integer
    minimum: 1
    maximum: 10
    required: true
  - name: debug
    type: boolean
    required: true
steps:
  - name: shell-values
    run: echo "region=$region count=$count debug=$debug"
    output: SHELL_VALUES
  - name: params-json
    run: printenv DAGU_PARAMS_JSON
    output: PARAMS_JSON
`, withLabels(map[string]string{"type": "test-worker"}))

	agent := f.dagWrapper.Agent()
	agent.RunSuccess(t)
	f.dagWrapper.AssertLatestStatus(t, core.Succeeded)

	rootStatus, err := f.latestStatus()
	require.NoError(t, err)
	require.Len(t, rootStatus.Nodes, 1)
	require.Len(t, rootStatus.Nodes[0].SubRuns, 1)

	rootRef := exec1.NewDAGRunRef(rootStatus.Name, rootStatus.DAGRunID)
	subRunID := rootStatus.Nodes[0].SubRuns[0].DAGRunID
	subStatus := readDistributedSubAttemptStatus(t, f, rootRef, subRunID)

	require.Equal(t, core.Succeeded, subStatus.Status)
	assert.Equal(t, []string{"region=us-west-2", "count=5", "debug=true"}, subStatus.ParamsList)
	require.Len(t, subStatus.Nodes, 2)
	assert.Equal(t, "region=us-west-2 count=5 debug=true", nodeOutputValue(t, subStatus.Nodes[0], "SHELL_VALUES"))
	assert.JSONEq(t, `{"region":"us-west-2","count":"5","debug":"true"}`, nodeOutputValue(t, subStatus.Nodes[1], "PARAMS_JSON"))
}

func TestParams_DistributedSubDAGInlineDefsFailure(t *testing.T) {
	f := newTestFixture(t, `
steps:
  - name: invoke-child
    action: dag.run
    with:
      dag: worker-inline-child
      params: "region=us-west-2 count=abc"

---
name: worker-inline-child
worker_selector:
  type: test-worker
params:
  - name: region
    type: string
    enum: [us-east-1, us-west-2]
    required: true
  - name: count
    type: integer
    minimum: 1
    maximum: 10
    required: true
steps:
  - name: shell-values
    run: echo "region=$region count=$count"
    output: SHELL_VALUES
`, withLabels(map[string]string{"type": "test-worker"}))

	agent := f.dagWrapper.Agent()
	err := agent.Run(agent.Context)
	require.Error(t, err)

	f.dagWrapper.AssertLatestStatus(t, core.Failed)

	rootStatus, statusErr := f.latestStatus()
	require.NoError(t, statusErr)
	require.Equal(t, core.Failed, rootStatus.Status)
	require.Len(t, rootStatus.Nodes, 1)
	require.Equal(t, core.NodeFailed, rootStatus.Nodes[0].Status)
	require.Len(t, rootStatus.Nodes[0].SubRuns, 1)

	rootRef := exec1.NewDAGRunRef(rootStatus.Name, rootStatus.DAGRunID)
	subRunID := rootStatus.Nodes[0].SubRuns[0].DAGRunID
	subStatus := readDistributedSubAttemptStatus(t, f, rootRef, subRunID)
	require.NotEqual(t, core.Succeeded, subStatus.Status)
	require.True(t, statusErrorsContain(subStatus.Errors(), "count"), "expected child status errors to mention count")
}

func readDistributedSubAttemptStatus(t *testing.T, f *testFixture, rootRef exec1.DAGRunRef, subRunID string) *exec1.DAGRunStatus {
	t.Helper()

	attempt, err := f.coord.DAGRunStore.FindSubAttempt(f.coord.Context, rootRef, subRunID)
	require.NoError(t, err)

	status, err := attempt.ReadStatus(f.coord.Context)
	require.NoError(t, err)

	return status
}

func nodeOutputValue(t *testing.T, node *exec1.Node, key string) string {
	t.Helper()

	require.NotNil(t, node.OutputVariables, "node %s should have output variables", node.Step.Name)
	value, ok := node.OutputVariables.Load(key)
	require.True(t, ok, "node %s should expose output %s", node.Step.Name, key)

	raw := fmt.Sprint(value)
	require.NotEmpty(t, raw, "node %s output %s should not be empty", node.Step.Name, key)
	if after, ok0 := strings.CutPrefix(raw, key+"="); ok0 {
		return after
	}
	return raw
}

func statusErrorsContain(errs []error, needle string) bool {
	for _, err := range errs {
		if strings.Contains(err.Error(), needle) {
			return true
		}
	}
	return false
}
