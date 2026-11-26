// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/noderuns"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A retry records no child run for a removed node that never had one. web03
// was disabled when the step first matched it and has left the inventory
// since, which now holds web01 and web02.
func TestRetryRemovedNodeWithoutRun(t *testing.T) {
	t.Parallel()

	r := setupRunner(t)
	r.Context = executor.WithSubWorkflowRunner(r.Context, &childRuns{})

	recordsDir := t.TempDir()
	_, err := noderuns.Write(recordsDir, []ir.NodeRun{
		{Node: "web01", Status: ir.NodeRunFailed, DAGName: "deploy-one", DAGRunID: "child-web01"},
		{Node: "web03", Status: ir.NodeRunDisabled},
	})
	require.NoError(t, err)

	step := newStep("rollout", withExecutorType(ir.ExecutorTypeDAG), withSubDAG("deploy-one"), withNodes("web"))
	dag := &ir.DAG{Name: "test_dag", Steps: []ir.Step{step}}
	plan, err := runtime.CreateRetryPlan(r.Context, dag,
		runtime.NewNode(step, runtime.NodeState{Status: ir.NodeFailed, RetryNodeRunsDir: recordsDir}))
	require.NoError(t, err)
	ph := planHelper{
		testHelper: r,
		Plan:       plan,
		workDir:    t.TempDir(),
		steps:      dag.Steps,
		localDAGs:  map[string]*ir.DAG{"deploy-one": deployOneDAG()},
		contextOpts: []runtime.ContextOption{
			runtime.WithRootDAGRun(runtime.NewDAGRunRef("test_dag", r.cfg.DAGRunID)),
			runtime.WithNodeSelector(webNodes{}),
		},
	}
	node := ph.assertRun(t, ir.Succeeded).nodeByName(t, "rollout")

	records, err := noderuns.Read(noderuns.Dir(node.GetStdout(), "rollout"))
	require.NoError(t, err)
	require.Len(t, records.Runs, 2)
	assert.Equal(t, ir.NodeRunRemoved, records.Runs[1].Status)
	assert.Empty(t, records.Runs[1].DAGRunID)
}
