// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/stretchr/testify/require"
)

type fileNameProfileResolver struct{}

func (fileNameProfileResolver) ResolveProfile(_ context.Context, dagName string, _ string) (string, error) {
	if dagName == "settings-key" {
		return "prod", nil
	}
	return "", nil
}

type workspaceProfileResolver struct{}

func (workspaceProfileResolver) ResolveProfile(_ context.Context, dagName string, workspaceName string) (string, error) {
	if dagName == "settings-key" && workspaceName == "ops" {
		return "prod", nil
	}
	return "", nil
}

type countingProfileResolver struct {
	profile string
	calls   int
}

func (r *countingProfileResolver) ResolveProfile(context.Context, string, string) (string, error) {
	r.calls++
	return r.profile, nil
}

func TestTickPlanner_ProfileScopedSchedulesUseDAGFileName(t *testing.T) {
	t.Parallel()

	scheduledAt := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp := scheduler.NewTickPlanner(scheduler.TickPlannerConfig{
		ProfileResolver: fileNameProfileResolver{},
		GetLatestStatus: func(context.Context, *ir.DAG) (dagrun.DAGRunStatus, error) {
			return dagrun.DAGRunStatus{}, nil
		},
		IsRunning: func(context.Context, *ir.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(context.Context) (string, error) {
			return "run-1", nil
		},
		Clock: func() time.Time {
			return scheduledAt
		},
		Events: make(chan scheduler.DAGChangeEvent, 1),
	})

	schedule, err := ir.NewCronSchedule("0 * * * *")
	require.NoError(t, err)
	schedule.Profile = "prod"
	dag := &ir.DAG{
		Name:     "yaml-name",
		Location: "/tmp/settings-key.yaml",
		Schedule: []ir.Schedule{schedule},
	}
	require.NoError(t, tp.Init(context.Background(), []*ir.DAG{dag}))

	runs := tp.Plan(context.Background(), scheduledAt)
	require.Len(t, runs, 1)
	require.Equal(t, "prod", runs[0].Schedule.Profile)
}

func TestTickPlanner_ProfileScopedSchedulesUseWorkspaceDefaultProfile(t *testing.T) {
	t.Parallel()

	scheduledAt := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp := scheduler.NewTickPlanner(scheduler.TickPlannerConfig{
		ProfileResolver: workspaceProfileResolver{},
		GetLatestStatus: func(context.Context, *ir.DAG) (dagrun.DAGRunStatus, error) {
			return dagrun.DAGRunStatus{}, nil
		},
		IsRunning: func(context.Context, *ir.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(context.Context) (string, error) {
			return "run-1", nil
		},
		Clock: func() time.Time {
			return scheduledAt
		},
		Events: make(chan scheduler.DAGChangeEvent, 1),
	})

	schedule, err := ir.NewCronSchedule("0 * * * *")
	require.NoError(t, err)
	schedule.Profile = "prod"
	dag := &ir.DAG{
		Name:     "yaml-name",
		Location: "/tmp/settings-key.yaml",
		Labels:   ir.NewLabels([]string{"workspace=ops"}),
		Schedule: []ir.Schedule{schedule},
	}
	require.NoError(t, tp.Init(context.Background(), []*ir.DAG{dag}))

	runs := tp.Plan(context.Background(), scheduledAt)
	require.Len(t, runs, 1)
	require.Equal(t, "prod", runs[0].Schedule.Profile)
}

func TestTickPlanner_ProfileScopedSchedulesRejectInvalidWorkspaceLabel(t *testing.T) {
	t.Parallel()

	scheduledAt := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp := scheduler.NewTickPlanner(scheduler.TickPlannerConfig{
		ProfileResolver: workspaceProfileResolver{},
		GetLatestStatus: func(context.Context, *ir.DAG) (dagrun.DAGRunStatus, error) {
			return dagrun.DAGRunStatus{}, nil
		},
		IsRunning: func(context.Context, *ir.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(context.Context) (string, error) {
			return "run-1", nil
		},
		Clock: func() time.Time {
			return scheduledAt
		},
		Events: make(chan scheduler.DAGChangeEvent, 1),
	})

	schedule, err := ir.NewCronSchedule("0 * * * *")
	require.NoError(t, err)
	schedule.Profile = "prod"
	dag := &ir.DAG{
		Name:     "yaml-name",
		Location: "/tmp/settings-key.yaml",
		Labels:   ir.NewLabels([]string{"workspace="}),
		Schedule: []ir.Schedule{schedule},
	}
	require.NoError(t, tp.Init(context.Background(), []*ir.DAG{dag}))

	runs := tp.Plan(context.Background(), scheduledAt)
	require.Empty(t, runs)
}

func TestTickPlanner_UnprofiledSchedulesSkipProfileResolver(t *testing.T) {
	t.Parallel()

	scheduledAt := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	resolver := &countingProfileResolver{profile: "prod"}
	tp := scheduler.NewTickPlanner(scheduler.TickPlannerConfig{
		ProfileResolver: resolver,
		GetLatestStatus: func(context.Context, *ir.DAG) (dagrun.DAGRunStatus, error) {
			return dagrun.DAGRunStatus{}, nil
		},
		IsRunning: func(context.Context, *ir.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(context.Context) (string, error) {
			return "run-1", nil
		},
		Clock: func() time.Time {
			return scheduledAt
		},
		Events: make(chan scheduler.DAGChangeEvent, 1),
	})

	schedule, err := ir.NewCronSchedule("0 * * * *")
	require.NoError(t, err)
	dag := &ir.DAG{
		Name:     "unprofiled-dag",
		Location: "/tmp/unprofiled-dag.yaml",
		Schedule: []ir.Schedule{schedule},
	}
	require.NoError(t, tp.Init(context.Background(), []*ir.DAG{dag}))

	runs := tp.Plan(context.Background(), scheduledAt)
	require.Len(t, runs, 1)
	require.Equal(t, 0, resolver.calls)
}

func TestTickPlanner_InactiveProfileSchedulePersistsNoNextRunProjection(t *testing.T) {
	t.Parallel()

	scheduledAt := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	store := scheduler.NewWatermarkStore(testutil.NewMemoryBackend().Collection("watermark"))
	tp := scheduler.NewTickPlanner(scheduler.TickPlannerConfig{
		WatermarkStore:  store,
		ProfileResolver: &countingProfileResolver{},
		GetLatestStatus: func(context.Context, *ir.DAG) (dagrun.DAGRunStatus, error) {
			return dagrun.DAGRunStatus{}, nil
		},
		IsRunning: func(context.Context, *ir.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(context.Context) (string, error) {
			return "run-1", nil
		},
		Clock: func() time.Time {
			return scheduledAt
		},
		Events: make(chan scheduler.DAGChangeEvent, 1),
	})

	schedule, err := ir.NewCronSchedule("0 * * * *")
	require.NoError(t, err)
	schedule.Profile = "prod"
	dag := &ir.DAG{
		Name:     "inactive-profile-dag",
		Location: "/tmp/inactive-profile-dag.yaml",
		Schedule: []ir.Schedule{schedule},
	}
	require.NoError(t, tp.Init(context.Background(), []*ir.DAG{dag}))

	runs := tp.Plan(context.Background(), scheduledAt)
	require.Empty(t, runs)

	state, err := store.Load(context.Background())
	require.NoError(t, err)
	watermark, ok := state.DAGs[dag.Name]
	require.True(t, ok)
	require.Nil(t, watermark.NextRun)
}
