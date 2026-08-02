// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
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
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Outcome {
			return buscal.Outcome{Skip: true, Reason: "holiday"}
		}, &dispatched)

		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeScheduler))
		assert.Zero(t, dispatched)
	})

	t.Run("NoSkipDispatches", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Outcome {
			return buscal.Outcome{}
		}, &dispatched)

		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeScheduler))
		assert.Equal(t, 1, dispatched)
	})

	t.Run("UnlicensedIgnoresCalendar", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Outcome {
			return buscal.Outcome{Unlicensed: true}
		}, &dispatched)

		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeScheduler))
		assert.Equal(t, 1, dispatched)
	})

	t.Run("EvaluationErrorBlocksDispatch", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Outcome {
			return buscal.Outcome{Skip: true, Err: errors.New("calendar not found")}
		}, &dispatched)

		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeScheduler))
		assert.Zero(t, dispatched)
	})

	t.Run("ManualTriggerBypassesCalendar", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Outcome {
			return buscal.Outcome{Skip: true, Reason: "holiday"}
		}, &dispatched)

		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeManual))
		assert.Equal(t, 1, dispatched)
	})

	t.Run("NoCalendarConfigDispatches", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Outcome {
			t.Error("decide must not be called without a calendar config")
			return buscal.Outcome{}
		}, &dispatched)

		dag := &core.DAG{Name: "plain-dag"}
		tp.DispatchRun(context.Background(), plannedRun(dag, core.TriggerTypeScheduler))
		assert.Equal(t, 1, dispatched)
	})

	t.Run("CatchupSkipAdvancesWatermark", func(t *testing.T) {
		t.Parallel()
		dispatched := 0
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Outcome {
			return buscal.Outcome{Skip: true, Reason: "holiday"}
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
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Outcome {
			return buscal.Outcome{Skip: true, Err: errors.New("calendar unreadable")}
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
		tp := newPlanner(t, func(*core.CalendarConfig, time.Time) buscal.Outcome {
			return buscal.Outcome{Skip: true, Reason: "holiday"}
		}, &dispatched)

		// Scheduler-managed retries follow the calendar, matching suspension
		// behavior.
		tp.DispatchRun(context.Background(), plannedRun(calendarDAG(), core.TriggerTypeRetry))
		assert.Zero(t, dispatched)
	})

	t.Run("StopScheduleBypassesCalendar", func(t *testing.T) {
		t.Parallel()
		stopped := 0
		tp := NewTickPlanner(TickPlannerConfig{
			DecideCalendar: func(*core.CalendarConfig, time.Time) buscal.Outcome {
				return buscal.Outcome{Skip: true, Reason: "holiday"}
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
}
