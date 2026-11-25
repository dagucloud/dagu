// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/dag"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/require"
)

func TestParallelRetryPathReusesSiblings(t *testing.T) {
	child := &ir.DAG{
		Name:     "child",
		YamlData: []byte("name: child\nsteps:\n  - name: target\n    run: echo child\n"),
		Steps:    []ir.Step{{Name: "target"}},
	}
	parent := &ir.DAG{Name: "root", LocalDAGs: map[string]*ir.DAG{child.Name: child}}
	runner := &retryRecorder{}
	baseCtx := executor.WithSubWorkflowRunner(context.Background(), runner)
	rootRef := ir.NewDAGRunRef(parent.Name, "root-run")
	path := dagrun.RetryPath{
		Step: "target",
		Hops: []dagrun.RetryHop{{
			Step:  "parallel-child",
			RunID: "child-selected",
		}},
	}
	ctx := runtime.NewContext(
		baseCtx,
		parent,
		rootRef.ID,
		"",
		runtime.WithRootDAGRun(rootRef),
		runtime.WithRetryPath(path),
		runtime.WithBypassPreconditions(true),
	)
	step := ir.Step{
		Name:           "parallel-child",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeParallel},
		SubDAG:         &ir.SubDAG{Name: child.Name},
		Parallel:       &ir.ParallelConfig{MaxConcurrent: 2},
	}

	impl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)
	parallel := impl.(executor.ParallelExecutor)
	parallel.SetParamsList([]executor.RunParams{
		{RunID: "child-succeeded", DAGName: child.Name, Params: "ITEM=one"},
		{RunID: "child-selected", DAGName: child.Name, Params: "ITEM=two"},
	})
	require.NoError(t, impl.Run(ctx))

	runRequests, retryRequests := runner.requests()
	require.Len(t, runRequests, 1)
	require.Equal(t, "child-succeeded", runRequests[0].RunID)
	require.True(t, runRequests[0].Reuse)
	require.Len(t, retryRequests, 1)
	require.Equal(t, "child-selected", retryRequests[0].RunID)
	require.Equal(t, "target", retryRequests[0].StepName)
	require.Equal(t, "target", retryRequests[0].RetryPath.Step)
	require.True(t, retryRequests[0].BypassPreconditions)
}

// A targeted retry of one node of a step with nodes reruns that node even when
// a node before it failed, since fail-fast stops only nodes that never
// started.
func TestNodesRetryPathRunsTargetAfterFailedSibling(t *testing.T) {
	child := &ir.DAG{
		Name:         "deploy-one",
		NodeRequired: true,
		YamlData:     []byte("name: deploy-one\nnode: required\nsteps:\n  - name: target\n    action: noop\n"),
		Steps:        []ir.Step{{Name: "target"}},
	}
	parent := &ir.DAG{Name: "root", LocalDAGs: map[string]*ir.DAG{child.Name: child}}
	runner := &retryRecorder{failed: map[string]bool{"run-web01": true}}
	rootRef := ir.NewDAGRunRef(parent.Name, "root-run")
	ctx := runtime.NewContext(
		executor.WithSubWorkflowRunner(context.Background(), runner),
		parent,
		rootRef.ID,
		"",
		runtime.WithRootDAGRun(rootRef),
		runtime.WithRetryPath(dagrun.RetryPath{
			Step: "target",
			Hops: []dagrun.RetryHop{{Step: "deploy", RunID: "run-web03"}},
		}),
	)
	step := ir.Step{
		Name:           "deploy",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeNodes},
		SubDAG:         &ir.SubDAG{Name: child.Name},
		Nodes:          &ir.NodeTargets{Selector: "web", MaxConcurrent: 1},
	}

	impl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)
	var matched []executor.NodeRun
	for _, node := range []string{"web01", "web02", "web03"} {
		matched = append(matched, executor.NodeRun{Node: node, Run: executor.RunParams{RunID: "run-" + node, DAGName: child.Name, Node: node}})
	}
	impl.(executor.NodesExecutor).SetNodes(matched)
	require.Error(t, impl.Run(ctx), "the reused failure of web01 still fails the step")

	runRequests, retryRequests := runner.requests()
	require.Len(t, runRequests, 2)
	require.Len(t, retryRequests, 1)
	require.Equal(t, "run-web03", retryRequests[0].RunID)
	require.Equal(t, "web03", retryRequests[0].Node)
}

// retriedNodesStep returns a step with nodes over deploy-one, the context of
// its run, and the runner that records the child runs it asks for.
func retriedNodesStep(t *testing.T, runner *retryRecorder, matched []executor.NodeRun) (executor.Executor, context.Context) {
	t.Helper()

	child := &ir.DAG{
		Name:         "deploy-one",
		NodeRequired: true,
		YamlData:     []byte("name: deploy-one\nnode: required\nsteps:\n  - name: target\n    action: noop\n"),
		Steps:        []ir.Step{{Name: "target"}},
	}
	parent := &ir.DAG{Name: "root", LocalDAGs: map[string]*ir.DAG{child.Name: child}}
	rootRef := ir.NewDAGRunRef(parent.Name, "root-run")
	ctx := runtime.NewContext(
		executor.WithSubWorkflowRunner(context.Background(), runner),
		parent,
		rootRef.ID,
		"",
		runtime.WithRootDAGRun(rootRef),
	)
	step := ir.Step{
		Name:           "deploy",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeNodes},
		SubDAG:         &ir.SubDAG{Name: child.Name},
		Nodes:          &ir.NodeTargets{Selector: "web", MaxConcurrent: 1},
	}
	impl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)
	for i := range matched {
		matched[i].Run.DAGName = child.Name
	}
	impl.(executor.NodesExecutor).SetNodes(matched)
	return impl, ctx
}

// A retried step with nodes keeps the result of a node whose child run
// succeeded and does not run a removed node. A node that fails again stops
// the nodes that never started, but not one whose result the step keeps.
func TestNodesRetryKeepsSucceededNodes(t *testing.T) {
	runner := &retryRecorder{failed: map[string]bool{"run-web02": true}}
	impl, ctx := retriedNodesStep(t, runner, []executor.NodeRun{
		{Node: "web01", Removed: true, Run: executor.RunParams{RunID: "run-web01"}},
		{Node: "web02", Run: executor.RunParams{RunID: "run-web02"}},
		{Node: "web03", Reuse: true, Run: executor.RunParams{RunID: "run-web03"}},
		{Node: "web04", Run: executor.RunParams{RunID: "run-web04"}},
	})
	var out bytes.Buffer
	impl.SetStdout(&out)
	require.Error(t, impl.Run(ctx), "web02 failed again")

	runRequests, retryRequests := runner.requests()
	require.Empty(t, retryRequests)
	reused := map[string]bool{}
	for _, req := range runRequests {
		reused[req.RunID] = req.Reuse
	}
	require.Equal(t, map[string]bool{"run-web02": false, "run-web03": true}, reused)

	var aggregate struct {
		Summary struct{ Total, Succeeded, Failed int } `json:"summary"`
		Results []struct{ Node, Status string }        `json:"results"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &aggregate))
	var statuses []string
	for _, result := range aggregate.Results {
		statuses = append(statuses, result.Node+"="+result.Status)
	}
	require.Equal(t, []string{"web01=removed", "web02=failed", "web03=succeeded", "web04=not_started"}, statuses)
	require.Equal(t, 4, aggregate.Summary.Total)
	require.Equal(t, 1, aggregate.Summary.Succeeded)
	require.Equal(t, 1, aggregate.Summary.Failed)
}

// A removed node does not fail the step.
func TestNodesRetryRemovedNodeSucceeds(t *testing.T) {
	runner := &retryRecorder{}
	impl, ctx := retriedNodesStep(t, runner, []executor.NodeRun{
		{Node: "web01", Run: executor.RunParams{RunID: "run-web01"}},
		{Node: "web02", Removed: true, Run: executor.RunParams{RunID: "run-web02"}},
	})
	require.NoError(t, impl.Run(ctx))
	status, err := impl.(executor.NodeStatusDeterminer).DetermineNodeStatus()
	require.NoError(t, err)
	require.Equal(t, ir.NodeSucceeded, status)
	require.Equal(t, []ir.NodeStatusDetail{
		{Label: "web01", Status: ir.NodeSucceeded},
		{Label: "web02", Status: ir.NodeSkipped},
	}, impl.(executor.StatusDetailsProvider).GetStatusDetails())
}

// A path that ends at a parallel child run continues that run as a plain
// attempt and reuses its siblings, so waiting siblings keep the step waiting.
func TestParallelRetryPathResumesLeaf(t *testing.T) {
	child := &ir.DAG{
		Name:     "child",
		YamlData: []byte("name: child\nsteps:\n  - name: gate\n    run: echo child\n"),
		Steps:    []ir.Step{{Name: "gate"}},
	}
	parent := &ir.DAG{Name: "root", LocalDAGs: map[string]*ir.DAG{child.Name: child}}
	runner := &retryRecorder{}
	rootRef := ir.NewDAGRunRef(parent.Name, "root-run")
	ctx := runtime.NewContext(
		executor.WithSubWorkflowRunner(context.Background(), runner),
		parent,
		rootRef.ID,
		"",
		runtime.WithRootDAGRun(rootRef),
		runtime.WithRetryPath(dagrun.RetryPath{
			Hops: []dagrun.RetryHop{{Step: "parallel-child", RunID: "child-approved"}},
			Leaf: dagrun.RetryLeafResume,
		}),
	)
	step := ir.Step{
		Name:           "parallel-child",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeParallel},
		SubDAG:         &ir.SubDAG{Name: child.Name},
		Parallel:       &ir.ParallelConfig{MaxConcurrent: 2},
	}

	impl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)
	impl.(executor.ParallelExecutor).SetParamsList([]executor.RunParams{
		{RunID: "child-waiting", DAGName: child.Name, Params: "ITEM=one"},
		{RunID: "child-approved", DAGName: child.Name, Params: "ITEM=two"},
	})
	require.NoError(t, impl.Run(ctx))

	runRequests, retryRequests := runner.requests()
	require.Empty(t, retryRequests)
	require.Len(t, runRequests, 2)
	reused := make(map[string]bool, len(runRequests))
	for _, req := range runRequests {
		reused[req.RunID] = req.Reuse
		require.Empty(t, req.RetryPath.Hops)
	}
	require.Equal(t, map[string]bool{"child-waiting": true, "child-approved": false}, reused)
}

// A path that ends at a called child run either continues that run or takes
// its persisted result, and never retries a step in it.
func TestSubDAGRetryPathLeaf(t *testing.T) {
	for leaf, wantReuse := range map[dagrun.RetryLeaf]bool{
		dagrun.RetryLeafResume: false,
		dagrun.RetryLeafReuse:  true,
	} {
		t.Run(string(leaf), func(t *testing.T) {
			child := &ir.DAG{
				Name:     "child",
				YamlData: []byte("name: child\nsteps:\n  - name: gate\n    run: echo child\n"),
				Steps:    []ir.Step{{Name: "gate"}},
			}
			parent := &ir.DAG{Name: "root", LocalDAGs: map[string]*ir.DAG{child.Name: child}}
			runner := &retryRecorder{}
			rootRef := ir.NewDAGRunRef(parent.Name, "root-run")
			ctx := runtime.NewContext(
				executor.WithSubWorkflowRunner(context.Background(), runner),
				parent,
				rootRef.ID,
				"",
				runtime.WithRootDAGRun(rootRef),
				runtime.WithRetryPath(dagrun.RetryPath{
					Hops: []dagrun.RetryHop{{Step: "run-child", RunID: "child-run"}},
					Leaf: leaf,
				}),
			)
			step := ir.Step{
				Name:           "run-child",
				ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeDAG},
				SubDAG:         &ir.SubDAG{Name: child.Name},
			}

			impl, err := executor.NewExecutor(ctx, step)
			require.NoError(t, err)
			impl.(executor.DAGExecutor).SetParams(executor.RunParams{RunID: "child-run", DAGName: child.Name})
			require.NoError(t, impl.Run(ctx))

			runRequests, retryRequests := runner.requests()
			require.Empty(t, retryRequests)
			require.Len(t, runRequests, 1)
			require.Equal(t, "child-run", runRequests[0].RunID)
			require.Equal(t, wantReuse, runRequests[0].Reuse)
		})
	}
}

// TestSubDAGRetryPathRejectsUnknownTarget asserts that a retry aimed at a child
// DAG run the step does not produce fails instead of starting a fresh child run.
func TestSubDAGRetryPathRejectsUnknownTarget(t *testing.T) {
	child := &ir.DAG{
		Name:     "child",
		YamlData: []byte("name: child\nsteps:\n  - name: target\n    run: echo child\n"),
		Steps:    []ir.Step{{Name: "target"}},
	}
	parent := &ir.DAG{Name: "root", LocalDAGs: map[string]*ir.DAG{child.Name: child}}
	runner := &retryRecorder{}
	baseCtx := executor.WithSubWorkflowRunner(context.Background(), runner)
	rootRef := ir.NewDAGRunRef(parent.Name, "root-run")
	ctx := runtime.NewContext(
		baseCtx,
		parent,
		rootRef.ID,
		"",
		runtime.WithRootDAGRun(rootRef),
		runtime.WithRetryPath(dagrun.RetryPath{
			Step: "target",
			Hops: []dagrun.RetryHop{{Step: "run-child", RunID: "child-stale"}},
		}),
	)
	step := ir.Step{
		Name:           "run-child",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeDAG},
		SubDAG:         &ir.SubDAG{Name: child.Name},
	}

	impl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)
	impl.(executor.DAGExecutor).SetParams(executor.RunParams{
		RunID:   "child-rebuilt",
		DAGName: child.Name,
	})

	err = impl.Run(ctx)
	require.ErrorContains(t, err, "target child DAG run child-stale is not present in step run-child")

	runRequests, retryRequests := runner.requests()
	require.Empty(t, runRequests)
	require.Empty(t, retryRequests)
}

type retryRecorder struct {
	mu      sync.Mutex
	runs    []executor.SubWorkflowRequest
	retries []executor.SubWorkflowRetryRequest
	// failed names the runs whose reused results failed.
	failed map[string]bool
}

func (r *retryRecorder) ShouldRun(context.Context, executor.SubWorkflowRequest) bool {
	return true
}

func (r *retryRecorder) Run(_ context.Context, req executor.SubWorkflowRequest) (*ir.RunStatus, error) {
	r.mu.Lock()
	r.runs = append(r.runs, req)
	r.mu.Unlock()
	status := ir.Succeeded
	if r.failed[req.RunID] {
		status = ir.Failed
	}
	return &ir.RunStatus{Name: req.DAG.Name, DAGRunID: req.RunID, Params: req.Params, Status: status}, nil
}

func (r *retryRecorder) Retry(_ context.Context, req executor.SubWorkflowRetryRequest) (*ir.RunStatus, error) {
	r.mu.Lock()
	r.retries = append(r.retries, req)
	r.mu.Unlock()
	return &ir.RunStatus{Name: req.DAG.Name, DAGRunID: req.RunID, Params: req.Params, Status: ir.Succeeded}, nil
}

func (*retryRecorder) Cancel(context.Context, executor.SubWorkflowCancelRequest) error {
	return nil
}

func (r *retryRecorder) requests() ([]executor.SubWorkflowRequest, []executor.SubWorkflowRetryRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]executor.SubWorkflowRequest(nil), r.runs...), append([]executor.SubWorkflowRetryRequest(nil), r.retries...)
}
