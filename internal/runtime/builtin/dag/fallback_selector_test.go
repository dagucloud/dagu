// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	dagbuiltin "github.com/dagucloud/dagu/internal/runtime/builtin/dag"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureSubWorkflowRunner records the dispatch requests it receives so tests
// can assert the worker selector that would drive routing.
type captureSubWorkflowRunner struct {
	mu       sync.Mutex
	requests []executor.SubWorkflowRequest
	result   *exec.RunStatus
}

func (r *captureSubWorkflowRunner) ShouldRun(context.Context, executor.SubWorkflowRequest) bool {
	return true
}

func (r *captureSubWorkflowRunner) Run(_ context.Context, req executor.SubWorkflowRequest) (*exec.RunStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
	return r.result, nil
}

func (r *captureSubWorkflowRunner) Retry(_ context.Context, req executor.SubWorkflowRetryRequest) (*exec.RunStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req.SubWorkflowRequest)
	return r.result, nil
}

func (r *captureSubWorkflowRunner) Cancel(context.Context, executor.SubWorkflowCancelRequest) error {
	return nil
}

func (r *captureSubWorkflowRunner) selectors() []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]string, 0, len(r.requests))
	for _, req := range r.requests {
		out = append(out, req.WorkerSelector)
	}
	return out
}

const paramDrivenSelectorChildYAML = "name: child\n" +
	"params:\n  - FACILITY: serverA\n" +
	"worker_selector:\n  host: ${FACILITY}\n" +
	"steps:\n  - name: step\n    command: echo child\n"

func newFallbackContext(t *testing.T, parent *core.DAG, runner executor.SubWorkflowRunner) context.Context {
	t.Helper()
	th := test.Setup(t)
	parentRun := exec.NewDAGRunRef(parent.Name, "parent-run")
	ctx := runtime.NewContext(
		th.Context,
		parent,
		parentRun.ID,
		filepath.Join(th.Config.Paths.LogDir, "parent.log"),
		runtime.WithRootDAGRun(parentRun),
		runtime.WithDAGRunStore(th.DAGRunStore),
		runtime.WithQueueStore(th.QueueStore),
		runtime.WithDAGRunLogDir(th.Config.Paths.LogDir),
		runtime.WithDAGRunArtifactDir(th.Config.Paths.ArtifactDir),
	)
	return executor.WithSubWorkflowRunner(ctx, runner)
}

func paramDrivenChildParent() *core.DAG {
	return &core.DAG{
		Name: "parent",
		LocalDAGs: map[string]*core.DAG{
			"child": {
				Name:           "child",
				YamlData:       []byte(paramDrivenSelectorChildYAML),
				WorkerSelector: map[string]string{"host": "serverA"},
				Steps: []core.Step{
					{Name: "step", ExecutorConfig: core.ExecutorConfig{Type: "noop"}},
				},
			},
		},
	}
}

func TestDAGExecutorFallbackReflectsParamOverride(t *testing.T) {
	t.Parallel()

	runner := &captureSubWorkflowRunner{result: &exec.RunStatus{Name: "child", DAGRunID: "child-run", Status: core.Succeeded}}
	ctx := newFallbackContext(t, paramDrivenChildParent(), runner)

	step := core.Step{
		Name:           "run-child",
		ExecutorConfig: core.ExecutorConfig{Type: core.ExecutorTypeDAG},
		SubDAG:         &core.SubDAG{Name: "child"},
	}
	execImpl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)

	dagExec, ok := execImpl.(executor.DAGExecutor)
	require.True(t, ok)
	dagExec.SetParams(executor.RunParams{RunID: "child-run", DAGName: "child", Params: "FACILITY=serverB"})

	require.NoError(t, execImpl.Run(ctx))

	require.Equal(t, []map[string]string{{"host": "serverB"}}, runner.selectors())
}

func TestParallelExecutorFallbackReflectsParamOverride(t *testing.T) {
	t.Parallel()

	runner := &captureSubWorkflowRunner{result: &exec.RunStatus{Name: "child", DAGRunID: "child-run", Status: core.Succeeded}}
	ctx := newFallbackContext(t, paramDrivenChildParent(), runner)

	step := core.Step{
		Name:           "run-child",
		ExecutorConfig: core.ExecutorConfig{Type: core.ExecutorTypeParallel},
		SubDAG:         &core.SubDAG{Name: "child"},
		Parallel:       &core.ParallelConfig{},
	}
	execImpl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)

	parallelExec, ok := execImpl.(executor.ParallelExecutor)
	require.True(t, ok)
	parallelExec.SetParamsList([]executor.RunParams{{RunID: "child-run", DAGName: "child", Params: "FACILITY=serverB"}})

	require.NoError(t, execImpl.Run(ctx))

	require.Equal(t, []map[string]string{{"host": "serverB"}}, runner.selectors())
}

func TestDAGExecutorApprovalGuardOnFallback(t *testing.T) {
	t.Parallel()

	parent := &core.DAG{
		Name: "parent",
		LocalDAGs: map[string]*core.DAG{
			"child": {
				Name:           "child",
				YamlData:       []byte(paramDrivenSelectorChildYAML),
				WorkerSelector: map[string]string{"host": "serverA"},
				Steps: []core.Step{
					{Name: "gate", Approval: &core.ApprovalConfig{Prompt: "approve?"}},
				},
			},
		},
	}
	runner := &captureSubWorkflowRunner{result: &exec.RunStatus{Name: "child", DAGRunID: "child-run", Status: core.Succeeded}}
	ctx := newFallbackContext(t, parent, runner)

	step := core.Step{
		Name:           "run-child",
		ExecutorConfig: core.ExecutorConfig{Type: core.ExecutorTypeDAG},
		SubDAG:         &core.SubDAG{Name: "child"},
	}
	execImpl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)

	dagExec := execImpl.(executor.DAGExecutor)
	dagExec.SetParams(executor.RunParams{RunID: "child-run", DAGName: "child", Params: "FACILITY=serverB"})

	err = execImpl.Run(ctx)
	require.ErrorIs(t, err, dagbuiltin.ErrApprovalStepsWithWorker)
	assert.Empty(t, runner.selectors())
}
