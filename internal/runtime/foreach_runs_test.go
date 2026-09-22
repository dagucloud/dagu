// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate/memstore"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestForeachPersistsNestedItemRuns(t *testing.T) {
	r := setupRunner(t)
	dag, err := spec.LoadYAML(r.Context, []byte(fmt.Sprintf(`name: nested-items
type: graph
params: "prefix=hello"
steps:
  - id: outer
    foreach:
      items: [red, blue]
      max_concurrent: 2
      steps:
        - id: each
          foreach:
            items: [one, two]
            max_concurrent: 2
            steps:
              - id: render
                run: %q
                output:
                  value: {from: stdout}
            collect:
              value: ${steps.render.outputs.value}
          output:
            values: {from: stdout, decode: json, select: .outputs}
      collect:
        value: ${steps.each.outputs.values}
`, test.Output("${params.prefix}:${foreach.item}"))))
	require.NoError(t, err)
	dag.WorkingDir = t.TempDir()
	state := memstore.New()
	root := ir.NewDAGRunRef(dag.Name, r.cfg.DAGRunID)
	ctx := runtime.NewContext(r.Context, dag, r.cfg.DAGRunID, "", runtime.WithRunStateStore(state), runtime.WithRootDAGRun(root))
	plan, err := runtime.NewPlan(dag.Steps...)
	require.NoError(t, err)
	require.NoError(t, r.runner.Run(ctx, plan, nil))
	refs := plan.Nodes()[0].State().SubRuns
	require.Len(t, refs, 2)
	paths := map[string]bool{}
	ids := map[string]bool{}
	for index, ref := range refs {
		attempt, err := state.OpenChildAttempt(ctx, root, ref.DAGRunID)
		require.NoError(t, err)
		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, status.Status)
		require.Equal(t, root, status.Parent)
		require.Len(t, status.Nodes, 1)
		var metadata struct {
			Index int
			Key   string
			Item  string
		}
		require.NoError(t, json.Unmarshal([]byte(ref.Params), &metadata))
		require.Equal(t, index, metadata.Index)
		require.Contains(t, []string{"red", "blue"}, metadata.Item)
		require.Len(t, status.Nodes[0].SubRuns, 2)
		for _, nested := range status.Nodes[0].SubRuns {
			require.False(t, ids[nested.DAGRunID])
			ids[nested.DAGRunID] = true
			nestedAttempt, err := state.OpenChildAttempt(ctx, root, nested.DAGRunID)
			require.NoError(t, err)
			child, err := nestedAttempt.ReadStatus(ctx)
			require.NoError(t, err)
			require.Equal(t, ir.Succeeded, child.Status)
			require.Equal(t, ref.DAGRunID, child.Parent.ID)
			require.Len(t, child.Nodes, 1)
			require.Equal(t, ir.NodeSucceeded, child.Nodes[0].Status)
			require.False(t, paths[child.Nodes[0].Stdout])
			paths[child.Nodes[0].Stdout] = true
			data, err := os.ReadFile(child.Nodes[0].Stdout)
			require.NoError(t, err)
			require.Contains(t, []string{"hello:one", "hello:two"}, strings.TrimSpace(string(data)))
		}
	}
}

func TestForeachPersistsLiveAndCancelledItems(t *testing.T) {
	r := setupRunner(t)
	executorType, execs := registerStoppedStatusExecutor(t)
	body := ir.Step{ID: "wait", Name: "wait", ExecutorConfig: ir.ExecutorConfig{Type: executorType}}
	loop := ir.Step{ID: "each", Name: "each", ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach}, Foreach: &ir.ForeachConfig{As: "item", Items: []any{"one", "two"}, MaxConcurrent: 1, Steps: []ir.Step{body}}}
	dag := &ir.DAG{Name: "cancel-items", Type: ir.TypeGraph, WorkingDir: t.TempDir(), Steps: []ir.Step{loop}}
	state := memstore.New()
	root := ir.NewDAGRunRef(dag.Name, r.cfg.DAGRunID)
	ctx := runtime.NewContext(r.Context, dag, r.cfg.DAGRunID, "", runtime.WithRunStateStore(state), runtime.WithRootDAGRun(root))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	plan, err := runtime.NewPlan(loop)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- r.runner.Run(ctx, plan, nil) }()
	select {
	case execution := <-execs:
		select {
		case <-execution.ready:
		case <-time.After(5 * time.Second):
			t.Fatal("body did not start")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("body executor was not created")
	}
	refs := plan.Nodes()[0].State().SubRuns
	require.Len(t, refs, 2)
	for index, ref := range refs {
		attempt, err := state.OpenChildAttempt(ctx, root, ref.DAGRunID)
		require.NoError(t, err)
		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		if index == 0 {
			require.Equal(t, ir.Running, status.Status)
		} else {
			require.Equal(t, ir.Queued, status.Status)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not cancel")
	}
	for _, ref := range refs {
		attempt, err := state.OpenChildAttempt(context.WithoutCancel(ctx), root, ref.DAGRunID)
		require.NoError(t, err)
		status, err := attempt.ReadStatus(context.WithoutCancel(ctx))
		require.NoError(t, err)
		require.Equal(t, ir.Aborted, status.Status)
		require.NotEmpty(t, status.FinishedAt)
	}
}

func TestForeachPersistsFailedBody(t *testing.T) {
	r := setupRunner(t)
	body := failStep("fail")
	body.ID = "fail"
	loop := ir.Step{ID: "each", Name: "each", ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach}, Foreach: &ir.ForeachConfig{As: "item", Items: []any{nil}, MaxConcurrent: 1, Steps: []ir.Step{body}}}
	dag := &ir.DAG{Name: "failed-item", WorkingDir: t.TempDir(), Steps: []ir.Step{loop}}
	state := memstore.New()
	root := ir.NewDAGRunRef(dag.Name, r.cfg.DAGRunID)
	ctx := runtime.NewContext(r.Context, dag, r.cfg.DAGRunID, "", runtime.WithRunStateStore(state), runtime.WithRootDAGRun(root))
	plan, err := runtime.NewPlan(loop)
	require.NoError(t, err)
	require.Error(t, r.runner.Run(ctx, plan, nil))
	refs := plan.Nodes()[0].State().SubRuns
	require.Len(t, refs, 1)
	require.JSONEq(t, `{"index":0,"key":"0","item":null}`, refs[0].Params)
	attempt, err := state.OpenChildAttempt(ctx, root, refs[0].DAGRunID)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, ir.Failed, status.Status)
	require.Equal(t, ir.NodeFailed, status.Nodes[0].Status)
	require.NotEmpty(t, status.Nodes[0].Error)
	require.NotEmpty(t, status.Nodes[0].Stderr)
	require.NotEmpty(t, status.FinishedAt)
}

func TestForeachPersistsMappedSteps(t *testing.T) {
	r := setupRunner(t)
	dag, err := spec.LoadYAML(r.Context, []byte(`name: mapped-items
type: graph
steps:
  each:
    foreach:
      items: [red]
      steps:
        - id: render
          run: echo hello
`))
	require.NoError(t, err)
	dag.WorkingDir = t.TempDir()
	state := memstore.New()
	root := ir.NewDAGRunRef(dag.Name, r.cfg.DAGRunID)
	ctx := runtime.NewContext(r.Context, dag, r.cfg.DAGRunID, "", runtime.WithRunStateStore(state), runtime.WithRootDAGRun(root))
	plan, err := runtime.NewPlan(dag.Steps...)
	require.NoError(t, err)
	require.NoError(t, r.runner.Run(ctx, plan, nil))
	require.Len(t, plan.Nodes()[0].State().SubRuns, 1)
	attempt, err := state.OpenChildAttempt(ctx, root, plan.Nodes()[0].State().SubRuns[0].DAGRunID)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status)
}

func TestForeachCollectFailureHasRunLog(t *testing.T) {
	r := setupRunner(t)
	body := successStep("render")
	loop := ir.Step{ID: "each", Name: "each", ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach}, Foreach: &ir.ForeachConfig{As: "item", Items: []any{"one"}, MaxConcurrent: 1, Steps: []ir.Step{body}, Collect: map[string]string{"value": "${steps.missing.outputs.value}"}}}
	dag := &ir.DAG{Name: "collect-failure", WorkingDir: t.TempDir(), Steps: []ir.Step{loop}}
	state := memstore.New()
	root := ir.NewDAGRunRef(dag.Name, r.cfg.DAGRunID)
	ctx := runtime.NewContext(r.Context, dag, r.cfg.DAGRunID, "", runtime.WithRunStateStore(state), runtime.WithRootDAGRun(root))
	plan, err := runtime.NewPlan(loop)
	require.NoError(t, err)
	require.Error(t, r.runner.Run(ctx, plan, nil))
	refs := plan.Nodes()[0].State().SubRuns
	require.Len(t, refs, 1)
	attempt, err := state.OpenChildAttempt(ctx, root, refs[0].DAGRunID)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, ir.Failed, status.Status)
	require.Equal(t, ir.NodeSucceeded, status.Nodes[0].Status)
	require.NotEmpty(t, status.Log)
	log, err := os.ReadFile(status.Log)
	require.NoError(t, err)
	require.Contains(t, string(log), "Foreach item failed")
	require.Contains(t, string(log), "missing")
}

func TestForeachRepeatPreservesDistinctChildren(t *testing.T) {
	r := setupRunner(t)
	loop := ir.Step{ID: "each", Name: "each", ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach}, Foreach: &ir.ForeachConfig{As: "item", Items: []any{"one"}, MaxConcurrent: 1, Steps: []ir.Step{successStep("render")}}, RepeatPolicy: ir.RepeatPolicy{RepeatMode: ir.RepeatModeWhile, Limit: 2}}
	dag := &ir.DAG{Name: "repeat-items", WorkingDir: t.TempDir(), Steps: []ir.Step{loop}}
	state := memstore.New()
	root := ir.NewDAGRunRef(dag.Name, r.cfg.DAGRunID)
	ctx := runtime.NewContext(r.Context, dag, r.cfg.DAGRunID, "", runtime.WithRunStateStore(state), runtime.WithRootDAGRun(root))
	plan, err := runtime.NewPlan(loop)
	require.NoError(t, err)
	require.NoError(t, r.runner.Run(ctx, plan, nil))
	node := plan.Nodes()[0].State()
	require.Len(t, node.SubRuns, 1)
	require.Len(t, node.SubRunsRepeated, 1)
	require.NotEqual(t, node.SubRuns[0].DAGRunID, node.SubRunsRepeated[0].DAGRunID)
	for _, ref := range append(node.SubRuns, node.SubRunsRepeated...) {
		attempt, err := state.OpenChildAttempt(ctx, root, ref.DAGRunID)
		require.NoError(t, err)
		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, status.Status)
	}
}
