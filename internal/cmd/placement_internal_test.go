// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// placementContext is the context of a run command on a host with the
// given coordinator.enabled and default execution mode. placed marks a run
// that a Dagu process launched after placing it.
func placementContext(t *testing.T, enabled bool, mode config.ExecutionMode, placed bool) *Context {
	t.Helper()
	return &Context{
		Context: context.Background(),
		Command: &cobra.Command{Use: "start"},
		Config: &config.Config{
			Coordinator:     config.Coordinator{Enabled: enabled},
			Core:            config.Core{Peer: config.Peer{Insecure: true}},
			DefaultExecMode: mode,
		},
		runPlaced: placed,
	}
}

// A user-invoked run goes to the coordinator exactly when the API and the
// scheduler would send it there. A run that a Dagu process launched after
// placing it runs on this host.
func TestPlaceRun(t *testing.T) {
	t.Parallel()

	selector := map[string]string{"gpu": "true"}
	tests := []struct {
		name     string
		dag      *ir.DAG
		enabled  bool
		mode     config.ExecutionMode
		placed   bool
		dispatch bool
	}{
		{name: "WorkerSelector", dag: &ir.DAG{WorkerSelector: selector}, enabled: true, dispatch: true},
		{name: "DistributedMode", dag: &ir.DAG{}, enabled: true, mode: config.ExecutionModeDistributed, dispatch: true},
		{name: "LocalMode", dag: &ir.DAG{}, enabled: true},
		{name: "SelectorLocal", dag: &ir.DAG{ForceLocal: true}, enabled: true, mode: config.ExecutionModeDistributed},
		{name: "CoordinatorDisabled", dag: &ir.DAG{WorkerSelector: selector}, mode: config.ExecutionModeDistributed},
		{name: "LaunchedPlaced", dag: &ir.DAG{WorkerSelector: selector}, enabled: true, placed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			placement, err := placeRun(placementContext(t, tt.enabled, tt.mode, tt.placed), tt.dag, nil, "")
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

	ctx := placementContext(t, true, config.ExecutionModeDistributed, false)
	_, err := placeRun(ctx, &ir.DAG{Type: ir.TypeBuild}, nil, "")
	require.ErrorIs(t, err, dispatch.ErrBuildRequiresLocal)
}

// A run on this host does not build a coordinator client, so a peer
// configuration that no client accepts fails only the runs it dispatches.
func TestPlaceRunLocalWithoutClient(t *testing.T) {
	t.Parallel()

	ctx := placementContext(t, true, config.ExecutionModeLocal, false)
	ctx.Config.Core.Peer.Insecure = false

	placement, err := placeRun(ctx, &ir.DAG{}, nil, "")
	require.NoError(t, err)
	assert.False(t, placement.dispatched())

	_, err = placeRun(ctx, &ir.DAG{WorkerSelector: map[string]string{"gpu": "true"}}, nil, "")
	require.ErrorContains(t, err, "invalid coordinator client configuration")
}

// A run of a project other than the default one cannot be dispatched, so the
// rules refuse it before the command acts, as before a restart stops a run.
// The same run on this host is not refused.
func TestPlaceRunOtherProject(t *testing.T) {
	t.Parallel()

	ctx := placementContext(t, true, config.ExecutionModeLocal, false)
	ctx.Config.ProjectsEnabled = true
	ctx.project = project.Project{Name: "weather"}

	_, err := placeRun(ctx, &ir.DAG{WorkerSelector: map[string]string{"gpu": "true"}}, nil, "")
	require.ErrorIs(t, err, project.ErrWorkerRun)

	placement, err := placeRun(ctx, &ir.DAG{}, nil, "")
	require.NoError(t, err)
	assert.False(t, placement.dispatched())
}

// The launch marker leaves the environment of the command it marks, so a step
// of the run that starts dagu does not inherit it and places its own run.
func TestClaimRunPlaced(t *testing.T) {
	t.Setenv(runenv.EnvKeyRunPlaced, "1")

	assert.True(t, claimRunPlaced())
	_, inherited := os.LookupEnv(runenv.EnvKeyRunPlaced)
	assert.False(t, inherited, "the marker is removed")
	assert.False(t, claimRunPlaced(), "a command without the marker places its run")
}
