// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustOneOffSchedule(t *testing.T, at string) ir.Schedule {
	t.Helper()
	schedule, err := ir.NewOneOffSchedule(at)
	require.NoError(t, err)
	return schedule
}

func TestNextPlannedRun_PendingOneOffOverridesMetadata(t *testing.T) {
	t.Parallel()

	cronSchedule := mustParseSchedule(t, "0 * * * *")
	oneOffSchedule := mustOneOffSchedule(t, "2026-02-07T12:00:00Z")
	now := time.Date(2026, 2, 7, 12, 30, 0, 0, time.UTC)

	state := &schedulerstate.State{
		Version: schedulerstate.CurrentVersion,
		DAGs: map[string]schedulerstate.DAGWatermark{
			"mixed-dag": {
				OneOffs: map[string]schedulerstate.OneOffScheduleState{
					oneOffSchedule.Fingerprint(): {
						ScheduledTime: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
						Status:        schedulerstate.OneOffStatusPending,
					},
				},
			},
		},
	}

	dag := &ir.DAG{
		Name:     "mixed-dag",
		Schedule: []ir.Schedule{cronSchedule, oneOffSchedule},
	}

	assert.Equal(t, time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC), NextPlannedRun(dag, now, state))
}

func TestReconcileOneOffState_NewEntriesRespectAddSemantics(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	future := mustOneOffSchedule(t, "2026-02-07T12:10:00Z")
	past := mustOneOffSchedule(t, "2026-02-07T11:50:00Z")

	dag := &ir.DAG{
		Name:     "one-off-dag",
		Schedule: []ir.Schedule{future, past},
	}

	next, changed := reconcileOneOffState(schedulerstate.DAGWatermark{}, dag.Schedule, now)
	require.True(t, changed)
	require.Len(t, next.OneOffs, 2)
	assert.Equal(t, schedulerstate.OneOffStatusPending, next.OneOffs[future.Fingerprint()].Status)
	assert.Equal(t, schedulerstate.OneOffStatusConsumed, next.OneOffs[past.Fingerprint()].Status)
}

func TestReconcileOneOffState_ScheduledNowStaysPending(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	schedule := mustOneOffSchedule(t, "2026-02-07T12:00:00Z")

	dag := &ir.DAG{
		Name:     "one-off-dag",
		Schedule: []ir.Schedule{schedule},
	}

	next, changed := reconcileOneOffState(schedulerstate.DAGWatermark{}, dag.Schedule, now)
	require.True(t, changed)
	require.Len(t, next.OneOffs, 1)
	assert.Equal(t, schedulerstate.OneOffStatusPending, next.OneOffs[schedule.Fingerprint()].Status)
}

func TestTickPlanner_PlanPendingOneOffRun(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	tp, _ := newTestTickPlanner(store)

	schedule := mustOneOffSchedule(t, "2026-02-07T12:00:00Z")
	dag := &ir.DAG{Name: "one-off-dag", Schedule: []ir.Schedule{schedule}}
	store.state = &schedulerstate.State{
		Version: schedulerstate.CurrentVersion,
		DAGs: map[string]schedulerstate.DAGWatermark{
			dag.Name: {
				OneOffs: map[string]schedulerstate.OneOffScheduleState{
					schedule.Fingerprint(): {
						ScheduledTime: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
						Status:        schedulerstate.OneOffStatusPending,
					},
				},
			},
		},
	}

	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	runs := tp.Plan(context.Background(), time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC))
	require.Len(t, runs, 1)
	assert.Equal(t, GenerateOneOffRunID(dag.Name, schedule.Fingerprint(), time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)), runs[0].RunID)
	assert.Equal(t, schedule.Fingerprint(), runs[0].Fingerprint)
	assert.True(t, runs[0].Schedule.IsOneOff())
}

func TestTickPlanner_DispatchRun_ExistingOneOffAttemptConsumesState(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		Dispatch: func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error {
			t.Fatal("dispatch should not be called when the run already exists")
			return nil
		},
		RunExists: func(context.Context, *ir.DAG, string) (bool, error) {
			return true, nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
	})

	schedule := mustOneOffSchedule(t, "2026-02-07T12:00:00Z")
	dag := &ir.DAG{Name: "one-off-dag", Schedule: []ir.Schedule{schedule}}
	store.state = &schedulerstate.State{
		Version: schedulerstate.CurrentVersion,
		DAGs: map[string]schedulerstate.DAGWatermark{
			dag.Name: {
				OneOffs: map[string]schedulerstate.OneOffScheduleState{
					schedule.Fingerprint(): {
						ScheduledTime: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
						Status:        schedulerstate.OneOffStatusPending,
					},
				},
			},
		},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	run, ok := tp.createPlannedRun(context.Background(), DAGEntry{DAG: dag}, schedule, time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC), ir.TriggerTypeScheduler)
	require.True(t, ok)
	tp.DispatchRun(context.Background(), run)

	state := store.lastSaved()
	require.NotNil(t, state)
	assert.Equal(t, schedulerstate.OneOffStatusConsumed, state.DAGs[dag.Name].OneOffs[schedule.Fingerprint()].Status)
}

func TestTickPlanner_DispatchRun_LegacyOneOffAttemptConsumesState(t *testing.T) {
	t.Parallel()

	scheduledTime := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	schedule := mustOneOffSchedule(t, "2026-02-07T12:00:00Z")
	dag := &ir.DAG{Name: "one.off-dag", Schedule: []ir.Schedule{schedule}}
	legacyRunID := generateLegacyOneOffRunID(dag.Name, schedule.Fingerprint(), scheduledTime)

	store := &mockWatermarkStore{}
	var checkedRunIDs []string
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		Dispatch: func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error {
			t.Fatal("dispatch should not be called when the legacy run already exists")
			return nil
		},
		RunExists: func(_ context.Context, _ *ir.DAG, runID string) (bool, error) {
			checkedRunIDs = append(checkedRunIDs, runID)
			return runID == legacyRunID, nil
		},
		Clock: func() time.Time {
			return scheduledTime
		},
	})

	store.state = &schedulerstate.State{
		Version: schedulerstate.CurrentVersion,
		DAGs: map[string]schedulerstate.DAGWatermark{
			dag.Name: {
				OneOffs: map[string]schedulerstate.OneOffScheduleState{
					schedule.Fingerprint(): {
						ScheduledTime: scheduledTime,
						Status:        schedulerstate.OneOffStatusPending,
					},
				},
			},
		},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	run, ok := tp.createPlannedRun(context.Background(), DAGEntry{DAG: dag}, schedule, scheduledTime, ir.TriggerTypeScheduler)
	require.True(t, ok)
	tp.DispatchRun(context.Background(), run)

	assert.Equal(t, []string{run.RunID, legacyRunID}, checkedRunIDs)
	state := store.lastSaved()
	require.NotNil(t, state)
	assert.Equal(t, schedulerstate.OneOffStatusConsumed, state.DAGs[dag.Name].OneOffs[schedule.Fingerprint()].Status)
}

func TestTickPlanner_DispatchRun_OneOffFailureLeavesPendingState(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		Dispatch: func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error {
			return assert.AnError
		},
		RunExists: func(context.Context, *ir.DAG, string) (bool, error) {
			return false, nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
	})

	schedule := mustOneOffSchedule(t, "2026-02-07T12:00:00Z")
	dag := &ir.DAG{Name: "one-off-dag", Schedule: []ir.Schedule{schedule}}
	store.state = &schedulerstate.State{
		Version: schedulerstate.CurrentVersion,
		DAGs: map[string]schedulerstate.DAGWatermark{
			dag.Name: {
				OneOffs: map[string]schedulerstate.OneOffScheduleState{
					schedule.Fingerprint(): {
						ScheduledTime: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
						Status:        schedulerstate.OneOffStatusPending,
					},
				},
			},
		},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	run, ok := tp.createPlannedRun(context.Background(), DAGEntry{DAG: dag}, schedule, time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC), ir.TriggerTypeScheduler)
	require.True(t, ok)
	tp.DispatchRun(context.Background(), run)

	tp.mu.RLock()
	defer tp.mu.RUnlock()
	assert.Equal(t, schedulerstate.OneOffStatusPending, tp.watermarkState.DAGs[dag.Name].OneOffs[schedule.Fingerprint()].Status)
}

func TestTickPlanner_PlanOneOffChoosesEarliestStartCandidate(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	tp, _ := newTestTickPlanner(store)

	earlier := mustOneOffSchedule(t, "2026-02-07T11:59:00Z")
	later := mustOneOffSchedule(t, "2026-02-07T12:00:00Z")
	cronSchedule := mustParseSchedule(t, "0 12 * * *")
	dag := &ir.DAG{Name: "one-off-dag", Schedule: []ir.Schedule{later, cronSchedule, earlier}}
	store.state = &schedulerstate.State{
		Version: schedulerstate.CurrentVersion,
		DAGs: map[string]schedulerstate.DAGWatermark{
			dag.Name: {
				OneOffs: map[string]schedulerstate.OneOffScheduleState{
					earlier.Fingerprint(): {
						ScheduledTime: time.Date(2026, 2, 7, 11, 59, 0, 0, time.UTC),
						Status:        schedulerstate.OneOffStatusPending,
					},
					later.Fingerprint(): {
						ScheduledTime: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
						Status:        schedulerstate.OneOffStatusPending,
					},
				},
			},
		},
	}

	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	runs := tp.Plan(context.Background(), time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC))
	require.Len(t, runs, 1)
	assert.True(t, runs[0].Schedule.IsOneOff())
	assert.Equal(t, time.Date(2026, 2, 7, 11, 59, 0, 0, time.UTC), runs[0].ScheduledTime)
	assert.Equal(t, earlier.Fingerprint(), runs[0].Fingerprint)
}

func TestTickPlanner_DispatchRun_OneOffRequiresRunExists(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	dispatched := false
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		Dispatch: func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error {
			dispatched = true
			return nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
	})

	schedule := mustOneOffSchedule(t, "2026-02-07T12:00:00Z")
	dag := &ir.DAG{Name: "one-off-dag", Schedule: []ir.Schedule{schedule}}
	store.state = &schedulerstate.State{
		Version: schedulerstate.CurrentVersion,
		DAGs: map[string]schedulerstate.DAGWatermark{
			dag.Name: {
				OneOffs: map[string]schedulerstate.OneOffScheduleState{
					schedule.Fingerprint(): {
						ScheduledTime: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
						Status:        schedulerstate.OneOffStatusPending,
					},
				},
			},
		},
	}

	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	run, ok := tp.createPlannedRun(context.Background(), DAGEntry{DAG: dag}, schedule, time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC), ir.TriggerTypeScheduler)
	require.True(t, ok)
	tp.DispatchRun(context.Background(), run)

	assert.False(t, dispatched)

	tp.mu.RLock()
	defer tp.mu.RUnlock()
	assert.Equal(t, schedulerstate.OneOffStatusPending, tp.watermarkState.DAGs[dag.Name].OneOffs[schedule.Fingerprint()].Status)
}
