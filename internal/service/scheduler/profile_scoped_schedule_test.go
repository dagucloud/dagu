// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/scheduler"
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

func TestTickPlanner_ProfileScopedSchedulesUseDAGFileName(t *testing.T) {
	t.Parallel()

	scheduledAt := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp := scheduler.NewTickPlanner(scheduler.TickPlannerConfig{
		ProfileResolver: fileNameProfileResolver{},
		GetLatestStatus: func(context.Context, *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(context.Context, *core.DAG) (bool, error) {
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

	schedule, err := core.NewCronSchedule("0 * * * *")
	require.NoError(t, err)
	schedule.Profile = "prod"
	dag := &core.DAG{
		Name:     "yaml-name",
		Location: "/tmp/settings-key.yaml",
		Schedule: []core.Schedule{schedule},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	runs := tp.Plan(context.Background(), scheduledAt)
	require.Len(t, runs, 1)
	require.Equal(t, "prod", runs[0].Schedule.Profile)
}

func TestTickPlanner_ProfileScopedSchedulesUseWorkspaceDefaultProfile(t *testing.T) {
	t.Parallel()

	scheduledAt := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp := scheduler.NewTickPlanner(scheduler.TickPlannerConfig{
		ProfileResolver: workspaceProfileResolver{},
		GetLatestStatus: func(context.Context, *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(context.Context, *core.DAG) (bool, error) {
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

	schedule, err := core.NewCronSchedule("0 * * * *")
	require.NoError(t, err)
	schedule.Profile = "prod"
	dag := &core.DAG{
		Name:     "yaml-name",
		Location: "/tmp/settings-key.yaml",
		Labels:   core.NewLabels([]string{"workspace=ops"}),
		Schedule: []core.Schedule{schedule},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	runs := tp.Plan(context.Background(), scheduledAt)
	require.Len(t, runs, 1)
	require.Equal(t, "prod", runs[0].Schedule.Profile)
}

func TestTickPlanner_ProfileScopedSchedulesRejectInvalidWorkspaceLabel(t *testing.T) {
	t.Parallel()

	scheduledAt := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp := scheduler.NewTickPlanner(scheduler.TickPlannerConfig{
		ProfileResolver: workspaceProfileResolver{},
		GetLatestStatus: func(context.Context, *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(context.Context, *core.DAG) (bool, error) {
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

	schedule, err := core.NewCronSchedule("0 * * * *")
	require.NoError(t, err)
	schedule.Profile = "prod"
	dag := &core.DAG{
		Name:     "yaml-name",
		Location: "/tmp/settings-key.yaml",
		Labels:   core.NewLabels([]string{"workspace="}),
		Schedule: []core.Schedule{schedule},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	runs := tp.Plan(context.Background(), scheduledAt)
	require.Empty(t, runs)
}
