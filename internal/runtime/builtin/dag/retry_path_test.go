// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag_test

import (
	"context"
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
}

func (r *retryRecorder) ShouldRun(context.Context, executor.SubWorkflowRequest) bool {
	return true
}

func (r *retryRecorder) Run(_ context.Context, req executor.SubWorkflowRequest) (*ir.RunStatus, error) {
	r.mu.Lock()
	r.runs = append(r.runs, req)
	r.mu.Unlock()
	return &ir.RunStatus{Name: req.DAG.Name, DAGRunID: req.RunID, Params: req.Params, Status: ir.Succeeded}, nil
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
