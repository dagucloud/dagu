// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator/subflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouterPrefersFirstMatchingRunner(t *testing.T) {
	t.Parallel()

	distributed := &stubRunner{
		shouldRun: true,
		result: &ir.RunStatus{
			Name:     "child",
			DAGRunID: "child-run",
			Status:   ir.Succeeded,
		},
	}
	local := &stubRunner{
		shouldRun: true,
		result: &ir.RunStatus{
			Name:     "child",
			DAGRunID: "child-run",
			Status:   ir.Failed,
		},
	}
	router := subflow.NewRouter(distributed, local)

	req := validSubWorkflowRequest()
	got, err := router.Run(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, ir.Succeeded, got.Status)
	assert.Equal(t, 1, distributed.runCount)
	assert.Equal(t, 0, local.runCount)
}

func TestRouterFallsBackToLocalRunner(t *testing.T) {
	t.Parallel()

	distributed := &stubRunner{shouldRun: false}
	local := &stubRunner{
		shouldRun: true,
		result: &ir.RunStatus{
			Name:     "child",
			DAGRunID: "child-run",
			Status:   ir.Succeeded,
		},
	}
	router := subflow.NewRouter(distributed, local)

	req := validSubWorkflowRequest()
	got, err := router.Run(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, ir.Succeeded, got.Status)
	assert.Equal(t, 0, distributed.runCount)
	assert.Equal(t, 1, local.runCount)
}

func TestRouterEnqueueUsesFirstEnqueuer(t *testing.T) {
	t.Parallel()

	enqueuer := &stubEnqueuer{stubRunner: &stubRunner{}}
	router := subflow.NewRouter(&stubRunner{}, enqueuer)

	got, err := router.Enqueue(context.Background(), executor.EnqueueRequest{
		DAG:   &ir.DAG{Name: "child"},
		RunID: "child-run",
	})
	require.NoError(t, err)
	assert.Equal(t, ir.Queued, got.Status)
	assert.Equal(t, 1, enqueuer.enqueueCount)
}

func TestRouterCancelRoutesToSelectedRunner(t *testing.T) {
	t.Parallel()

	distributed := newBlockingRunner(true)
	local := &stubRunner{shouldRun: true}
	router := subflow.NewRouter(distributed, local)

	req := validSubWorkflowRequest()
	done := make(chan error, 1)
	go func() {
		_, err := router.Run(context.Background(), req)
		done <- err
	}()

	<-distributed.started
	err := router.Cancel(context.Background(), executor.SubWorkflowCancelRequest{
		DAG:        req.DAG,
		RootDAGRun: req.RootDAGRun,
		RunID:      req.RunID,
	})
	require.NoError(t, err)

	close(distributed.release)
	require.NoError(t, <-done)

	assert.Equal(t, 1, distributed.cancelCount)
	assert.Equal(t, 0, local.cancelCount)
}

func TestRouterCancelFallsBackToAllRunnersWhenOwnerUnknown(t *testing.T) {
	t.Parallel()

	distributed := &stubRunner{shouldRun: true}
	local := &stubRunner{shouldRun: true}
	router := subflow.NewRouter(distributed, local)

	req := validSubWorkflowRequest()
	err := router.Cancel(context.Background(), executor.SubWorkflowCancelRequest{
		DAG:        req.DAG,
		RootDAGRun: req.RootDAGRun,
		RunID:      req.RunID,
	})
	require.NoError(t, err)

	assert.Equal(t, 1, distributed.cancelCount)
	assert.Equal(t, 1, local.cancelCount)
}

func TestLocalShouldRunValidInProcessRequestWithoutDAGLocation(t *testing.T) {
	t.Parallel()

	req := validSubWorkflowRequest()
	req.DAG.Location = ""
	req.DAG.YamlData = []byte("name: child\nsteps:\n  - name: ok\n    run: echo ok\n")
	runner := subflow.NewLocal(runtime.Manager{}, nil)

	assert.True(t, runner.ShouldRun(context.Background(), req))
}

func TestLocalRejectsWorkerSelector(t *testing.T) {
	t.Parallel()

	req := validSubWorkflowRequest()
	req.WorkerSelector = map[string]string{"gpu": "true"}
	runner := subflow.NewLocal(runtime.Manager{}, nil)

	assert.False(t, runner.ShouldRun(context.Background(), req))
}

func TestLocalForceLocalOverridesWorkerSelector(t *testing.T) {
	t.Parallel()

	req := validSubWorkflowRequest()
	req.DAG.ForceLocal = true
	req.WorkerSelector = map[string]string{"gpu": "true"}
	runner := subflow.NewLocal(runtime.Manager{}, nil)

	assert.True(t, runner.ShouldRun(context.Background(), req))
}

func validSubWorkflowRequest() executor.SubWorkflowRequest {
	return executor.SubWorkflowRequest{
		DAG: &ir.DAG{
			Name:     "child",
			Location: "/tmp/child.yaml",
		},
		RootDAGRun:   ir.NewDAGRunRef("root", "root-run"),
		ParentDAGRun: ir.NewDAGRunRef("parent", "parent-run"),
		RunID:        "child-run",
	}
}

type stubRunner struct {
	shouldRun   bool
	result      *ir.RunStatus
	runCount    int
	cancelCount int
}

type stubEnqueuer struct {
	*stubRunner
	enqueueCount int
}

func (r *stubEnqueuer) Enqueue(context.Context, executor.EnqueueRequest) (executor.EnqueueResult, error) {
	r.enqueueCount++
	return executor.EnqueueResult{Status: ir.Queued}, nil
}

func (r *stubRunner) ShouldRun(context.Context, executor.SubWorkflowRequest) bool {
	return r.shouldRun
}

func (r *stubRunner) Run(context.Context, executor.SubWorkflowRequest) (*ir.RunStatus, error) {
	r.runCount++
	return r.result, nil
}

func (r *stubRunner) Retry(context.Context, executor.SubWorkflowRetryRequest) (*ir.RunStatus, error) {
	return r.result, nil
}

func (r *stubRunner) Cancel(context.Context, executor.SubWorkflowCancelRequest) error {
	r.cancelCount++
	return nil
}

type blockingRunner struct {
	shouldRun   bool
	started     chan struct{}
	release     chan struct{}
	cancelCount int
}

func newBlockingRunner(shouldRun bool) *blockingRunner {
	return &blockingRunner{
		shouldRun: shouldRun,
		started:   make(chan struct{}),
		release:   make(chan struct{}),
	}
}

func (r *blockingRunner) ShouldRun(context.Context, executor.SubWorkflowRequest) bool {
	return r.shouldRun
}

func (r *blockingRunner) Run(context.Context, executor.SubWorkflowRequest) (*ir.RunStatus, error) {
	close(r.started)
	<-r.release
	return &ir.RunStatus{
		Name:     "child",
		DAGRunID: "child-run",
		Status:   ir.Succeeded,
	}, nil
}

func (r *blockingRunner) Retry(context.Context, executor.SubWorkflowRetryRequest) (*ir.RunStatus, error) {
	return nil, nil
}

func (r *blockingRunner) Cancel(context.Context, executor.SubWorkflowCancelRequest) error {
	r.cancelCount++
	return nil
}
