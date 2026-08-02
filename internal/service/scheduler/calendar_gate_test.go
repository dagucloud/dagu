// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/buscal"
	"github.com/dagucloud/dagu/v2/internal/core"
)

func TestTickPlanner_DispatchRun_CalendarGate(t *testing.T) {
	t.Parallel()

	scheduledTime := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)

	newPlanner := func(t *testing.T, decide CalendarDecideFunc, dispatched *int) *TickPlanner {
		t.Helper()
		tp := NewTickPlanner(TickPlannerConfig{
			DecideCalendar: decide,
			Dispatch: func(context.Context, *core.DAG, string, core.TriggerType, time.Time) error {
				*dispatched++
				return nil
			},
			Events: make(chan DAGChangeEvent, 1),
		})
		require.NoError(t, tp.Init(context.Background(), nil))
		return tp
	}

	calendarDAG := func() *core.DAG {
		return &core.DAG{
			Name:     "calendar-dag",
			Calendar: &core.CalendarConfig{Name: "jp-banking"},
		}
	}
	plannedRun := func(dag *core.DAG, triggerType core.TriggerType) PlannedRun {
		return PlannedRun{
			DAG:           dag,
			ScheduleType:  ScheduleTypeStart,
			ScheduledTime: scheduledTime,
			TriggerType:   triggerType,
			RunID:         "run-1",
		}
	}

	t.Run("SkipBlocksDispatch", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Decision {
			return buscal.Decision{Kind: buscal.DecisionSkip, Reason: "holiday"}
		}, &dispatched)

		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeScheduler))
		assert.Zero(t, dispatched)
	})

	t.Run("NoSkipDispatches", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Decision {
			return buscal.Decision{}
		}, &dispatched)

		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeScheduler))
		assert.Equal(t, 1, dispatched)
	})

	t.Run("UnlicensedIgnoresCalendar", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Decision {
			return buscal.Decision{Kind: buscal.DecisionUnlicensed}
		}, &dispatched)

		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeScheduler))
		assert.Equal(t, 1, dispatched)
	})

	t.Run("EvaluationErrorBlocksDispatch", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Decision {
			return buscal.Decision{Kind: buscal.DecisionError, Err: errors.New("calendar not found")}
		}, &dispatched)

		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeScheduler))
		assert.Zero(t, dispatched)
	})

	t.Run("ManualTriggerBypassesCalendar", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Decision {
			return buscal.Decision{Kind: buscal.DecisionSkip, Reason: "holiday"}
		}, &dispatched)

		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeManual))
		assert.Equal(t, 1, dispatched)
	})

	t.Run("NoCalendarConfigDispatches", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Decision {
			t.Error("decide must not be called without a calendar config")
			return buscal.Decision{}
		}, &dispatched)

		dag := &core.DAG{Name: "plain-dag"}
		tp.DispatchRun(context.Background(), plannedRun(dag, core.TriggerTypeScheduler))
		assert.Equal(t, 1, dispatched)
	})

	t.Run("CatchupSkipAdvancesWatermark", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Decision {
			return buscal.Decision{Kind: buscal.DecisionSkip, Reason: "holiday"}
		}, &dispatched)

		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeCatchUp))
		assert.Zero(t, dispatched)

		tp.mu.RLock()
		defer tp.mu.RUnlock()
		assert.Equal(t, scheduledTime, tp.watermarkState.DAGs["calendar-dag"].LastScheduledTime)
	})

	t.Run("CatchupErrorReinsertsWithoutAdvancingWatermark", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Decision {
			return buscal.Decision{Kind: buscal.DecisionError, Err: errors.New("calendar unreadable")}
		}, &dispatched)

		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeCatchUp))
		assert.Zero(t, dispatched)

		buf := tp.buffers["calendar-dag"]
		if assert.NotNil(t, buf, "catchup item must be re-inserted for retry") {
			item, ok := buf.Peek()
			assert.True(t, ok)
			assert.Equal(t, scheduledTime, item.ScheduledTime)
		}

		tp.mu.RLock()
		defer tp.mu.RUnlock()
		assert.True(t, tp.watermarkState.DAGs["calendar-dag"].LastScheduledTime.IsZero(),
			"evaluation error must not consume the catchup slot")
	})

	t.Run("RetryTriggerGated", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Decision {
			return buscal.Decision{Kind: buscal.DecisionSkip, Reason: "holiday"}
		}, &dispatched)

		// Scheduler-managed retries follow the calendar, matching suspension
		// behavior.
		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeRetry))
		assert.Zero(t, dispatched)
	})

	t.Run("OneOffSkipConsumesState", func(t *testing.T) {
		t.Parallel()
		store := &mockWatermarkStore{}
		dispatched := 0
		tp := NewTickPlanner(TickPlannerConfig{
			WatermarkStore: store,
			DecideCalendar: func(*core.CalendarConfig, time.Time) buscal.Decision {
				return buscal.Decision{Kind: buscal.DecisionSkip, Reason: "holiday"}
			},
			Dispatch: func(context.Context, *core.DAG, string, core.TriggerType, time.Time) error {
				dispatched++
				return nil
			},
			Events: make(chan DAGChangeEvent, 1),
			Clock:  func() time.Time { return scheduledTime },
		})

		schedule := mustOneOffSchedule(t, "2026-01-01T01:00:00Z")
		dag := calendarDAG()
		dag.Schedule = []core.Schedule{schedule}
		store.state = &SchedulerState{
			Version: SchedulerStateVersion,
			DAGs: map[string]DAGWatermark{
				dag.Name: {
					OneOffs: map[string]OneOffScheduleState{
						schedule.Fingerprint(): {
							ScheduledTime: scheduledTime,
							Status:        OneOffStatusPending,
						},
					},
				},
			},
		}
		require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

		run, ok := tp.createPlannedRun(context.Background(), dag, schedule, scheduledTime, core.TriggerTypeScheduler)
		require.True(t, ok)
		tp.DispatchRun(context.Background(), run)

		assert.Zero(t, dispatched)
		state := store.lastSaved()
		require.NotNil(t, state)
		assert.Equal(t, OneOffStatusConsumed, state.DAGs[dag.Name].OneOffs[schedule.Fingerprint()].Status,
			"a calendar-skipped one-off must resolve instead of re-planning every tick")
	})

	t.Run("StopScheduleBypassesCalendar", func(t *testing.T) {
		t.Parallel()
		stopped := 0
		tp := NewTickPlanner(TickPlannerConfig{
			DecideCalendar: func(*core.CalendarConfig, time.Time) buscal.Decision {
				return buscal.Decision{Kind: buscal.DecisionSkip, Reason: "holiday"}
			},
			Stop:   func(context.Context, *core.DAG) error { stopped++; return nil },
			Events: make(chan DAGChangeEvent, 1),
		})
		require.NoError(t, tp.Init(context.Background(), nil))

		tp.DispatchRun(context.Background(), PlannedRun{
			DAG:           calendarDAG(),
			ScheduleType:  ScheduleTypeStop,
			ScheduledTime: scheduledTime,
			TriggerType:   core.TriggerTypeScheduler,
		})
		assert.Equal(t, 1, stopped)
	})

	t.Run("RestartScheduleGated", func(t *testing.T) {
		t.Parallel()
		restarted := 0
		kind := buscal.DecisionSkip
		tp := NewTickPlanner(TickPlannerConfig{
			DecideCalendar: func(*core.CalendarConfig, time.Time) buscal.Decision {
				return buscal.Decision{Kind: kind, Reason: "holiday"}
			},
			Restart: func(context.Context, *core.DAG, time.Time) error { restarted++; return nil },
			Events:  make(chan DAGChangeEvent, 1),
		})
		require.NoError(t, tp.Init(context.Background(), nil))

		restartRun := PlannedRun{
			DAG:           calendarDAG(),
			ScheduleType:  ScheduleTypeRestart,
			ScheduledTime: scheduledTime,
			TriggerType:   core.TriggerTypeScheduler,
		}

		// A restart launches a fresh run, so it follows the calendar.
		tp.DispatchRun(context.Background(), restartRun)
		assert.Zero(t, restarted)

		kind = buscal.DecisionAllow
		tp.DispatchRun(context.Background(), restartRun)
		assert.Equal(t, 1, restarted)
	})
}

// TestTickPlanner_CalendarGate_RealStore exercises the production composition
// (buscal.Store → buscal.Service → planner gate) against a real calendar file
// instead of a fake decide func.
func TestTickPlanner_CalendarGate_RealStore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "jp-banking.yaml"),
		[]byte("holidays: [2026-01-01]"),
		0o600,
	))
	service := buscal.NewService(buscal.NewStore(dir), func() bool { return true })

	dispatched := 0
	tp := NewTickPlanner(TickPlannerConfig{
		DecideCalendar: service.Decide,
		Dispatch: func(context.Context, *core.DAG, string, core.TriggerType, time.Time) error {
			dispatched++
			return nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	dag := &core.DAG{
		Name:     "real-cal-dag",
		Calendar: &core.CalendarConfig{Name: "jp-banking"},
	}
	run := func(scheduled time.Time, runID string) PlannedRun {
		return PlannedRun{
			DAG:           dag,
			ScheduleType:  ScheduleTypeStart,
			ScheduledTime: scheduled,
			TriggerType:   core.TriggerTypeScheduler,
			RunID:         runID,
		}
	}

	// The calendar file declares no timezone, so dates evaluate in time.Local.
	tp.DispatchRun(context.Background(), run(time.Date(2026, 1, 1, 1, 0, 0, 0, time.Local), "r1"))
	assert.Zero(t, dispatched, "holiday must skip")

	tp.DispatchRun(context.Background(), run(time.Date(2026, 1, 5, 1, 0, 0, 0, time.Local), "r2"))
	assert.Equal(t, 1, dispatched, "business day must dispatch")
}
