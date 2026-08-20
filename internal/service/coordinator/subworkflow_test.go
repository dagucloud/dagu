// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubWorkflowRunnerFactoryDoesNotCleanInjectedDispatcher(t *testing.T) {
	t.Parallel()

	dispatcher := &borrowedDispatcher{}
	factory := coordinator.NewSubWorkflowRunnerFactory(coordinator.SubWorkflowRunnerConfig{
		Dispatcher:      dispatcher,
		DefaultExecMode: config.ExecutionModeDistributed,
	})
	runner, err := factory(context.Background())
	require.NoError(t, err)

	cleaner, ok := runner.(interface{ Cleanup(context.Context) error })
	require.True(t, ok)
	require.NoError(t, cleaner.Cleanup(context.Background()))
	assert.Zero(t, dispatcher.cleanupCalls)
}

type borrowedDispatcher struct {
	cleanupCalls int
}

func (*borrowedDispatcher) Dispatch(context.Context, dispatch.DispatchRequest) error {
	return nil
}

func (d *borrowedDispatcher) Cleanup(context.Context) error {
	d.cleanupCalls++
	return nil
}

func (*borrowedDispatcher) GetDAGRunStatus(
	context.Context,
	string,
	string,
	*ir.DAGRunRef,
) (*dispatch.DAGRunStatusResult, error) {
	return &dispatch.DAGRunStatusResult{}, nil
}

func (*borrowedDispatcher) RequestCancel(context.Context, string, string, *ir.DAGRunRef) error {
	return nil
}
