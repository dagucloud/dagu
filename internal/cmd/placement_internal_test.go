// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// placementContext is the context of a run command with the command line
// args, on a host with the given coordinator.enabled and default execution
// mode.
func placementContext(t *testing.T, enabled bool, mode config.ExecutionMode, args ...string) *Context {
	t.Helper()
	command := &cobra.Command{Use: "start"}
	initFlags(command, noDispatchFlag)
	require.NoError(t, command.ParseFlags(args))
	return &Context{
		Context: context.Background(),
		Command: command,
		Config: &config.Config{
			Coordinator:     config.Coordinator{Enabled: enabled},
			Core:            config.Core{Peer: config.Peer{Insecure: true}},
			DefaultExecMode: mode,
		},
	}
}

// A user-invoked run goes to the coordinator exactly when the API and the
// scheduler would send it there. A run that a Dagu process launched with
// --no-dispatch was already placed, and runs on this host.
func TestPlaceRun(t *testing.T) {
	t.Parallel()

	selector := map[string]string{"gpu": "true"}
	tests := []struct {
		name     string
		dag      *ir.DAG
		enabled  bool
		mode     config.ExecutionMode
		args     []string
		dispatch bool
	}{
		{name: "WorkerSelector", dag: &ir.DAG{WorkerSelector: selector}, enabled: true, dispatch: true},
		{name: "DistributedMode", dag: &ir.DAG{}, enabled: true, mode: config.ExecutionModeDistributed, dispatch: true},
		{name: "LocalMode", dag: &ir.DAG{}, enabled: true},
		{name: "SelectorLocal", dag: &ir.DAG{ForceLocal: true}, enabled: true, mode: config.ExecutionModeDistributed},
		{name: "CoordinatorDisabled", dag: &ir.DAG{WorkerSelector: selector}, mode: config.ExecutionModeDistributed},
		{name: "LaunchedPlaced", dag: &ir.DAG{WorkerSelector: selector}, enabled: true, args: []string{"--no-dispatch"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			placement, err := placeRun(placementContext(t, tt.enabled, tt.mode, tt.args...), tt.dag, nil, "")
			require.NoError(t, err)
			assert.Equal(t, tt.dispatch, placement.dispatched())
			assert.Empty(t, placement.worker)
		})
	}
}

// A build workflow cannot be dispatched, so a run the rules send to the
// coordinator is refused rather than run on this host.
func TestPlaceRunBuild(t *testing.T) {
	t.Parallel()

	ctx := placementContext(t, true, config.ExecutionModeDistributed)
	_, err := placeRun(ctx, &ir.DAG{Type: ir.TypeBuild}, nil, "")
	require.ErrorIs(t, err, dispatch.ErrBuildRequiresLocal)
}

// A run on this host does not build a coordinator client, so a peer
// configuration that no client accepts fails only the runs it dispatches.
func TestPlaceRunLocalWithoutClient(t *testing.T) {
	t.Parallel()

	ctx := placementContext(t, true, config.ExecutionModeLocal)
	ctx.Config.Core.Peer.Insecure = false

	placement, err := placeRun(ctx, &ir.DAG{}, nil, "")
	require.NoError(t, err)
	assert.False(t, placement.dispatched())

	_, err = placeRun(ctx, &ir.DAG{WorkerSelector: map[string]string{"gpu": "true"}}, nil, "")
	require.ErrorContains(t, err, "invalid coordinator client configuration")
}
