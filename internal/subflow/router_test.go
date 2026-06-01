// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/subflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouterPrefersFirstMatchingRunner(t *testing.T) {
	t.Parallel()

	distributed := &stubRunner{
		shouldRun: true,
		result: &exec.RunStatus{
			Name:     "child",
			DAGRunID: "child-run",
			Status:   core.Succeeded,
		},
	}
	local := &stubRunner{
		shouldRun: true,
		result: &exec.RunStatus{
			Name:     "child",
			DAGRunID: "child-run",
			Status:   core.Failed,
		},
	}
	router := subflow.NewRouter(distributed, local)

	req := validSubWorkflowRequest()
	got, err := router.Run(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, core.Succeeded, got.Status)
	assert.Equal(t, 1, distributed.runCount)
	assert.Equal(t, 0, local.runCount)
}

func TestRouterFallsBackToLocalRunner(t *testing.T) {
	t.Parallel()

	distributed := &stubRunner{shouldRun: false}
	local := &stubRunner{
		shouldRun: true,
		result: &exec.RunStatus{
			Name:     "child",
			DAGRunID: "child-run",
			Status:   core.Succeeded,
		},
	}
	router := subflow.NewRouter(distributed, local)

	req := validSubWorkflowRequest()
	got, err := router.Run(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, core.Succeeded, got.Status)
	assert.Equal(t, 0, distributed.runCount)
	assert.Equal(t, 1, local.runCount)
}

func TestLocalCLIShouldRunValidLocalRequest(t *testing.T) {
	t.Parallel()

	runner := subflow.NewLocalCLI()

	assert.True(t, runner.ShouldRun(context.Background(), validSubWorkflowRequest()))
}

func validSubWorkflowRequest() executor.SubWorkflowRequest {
	return executor.SubWorkflowRequest{
		DAG: &core.DAG{
			Name:     "child",
			Location: "/tmp/child.yaml",
		},
		RootDAGRun:   exec.NewDAGRunRef("root", "root-run"),
		ParentDAGRun: exec.NewDAGRunRef("parent", "parent-run"),
		RunID:        "child-run",
	}
}

type stubRunner struct {
	shouldRun bool
	result    *exec.RunStatus
	runCount  int
}

func (r *stubRunner) ShouldRun(context.Context, executor.SubWorkflowRequest) bool {
	return r.shouldRun
}

func (r *stubRunner) Run(context.Context, executor.SubWorkflowRequest) (*exec.RunStatus, error) {
	r.runCount++
	return r.result, nil
}

func (r *stubRunner) Retry(context.Context, executor.SubWorkflowRetryRequest) (*exec.RunStatus, error) {
	return r.result, nil
}

func (r *stubRunner) Cancel(context.Context, executor.SubWorkflowCancelRequest) error {
	return nil
}
