// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockWatermarkStore struct {
	state   *SchedulerState
	loadErr error
	saveErr error
	mu      sync.Mutex
	saved   []*SchedulerState
}

func (m *mockWatermarkStore) Load(_ context.Context) (*SchedulerState, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	if m.state == nil {
		return &SchedulerState{Version: SchedulerStateVersion, DAGs: make(map[string]DAGWatermark)}, nil
	}
	return m.state, nil
}

func (m *mockWatermarkStore) Save(_ context.Context, state *SchedulerState) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saved = append(m.saved, state)
	return nil
}

func (m *mockWatermarkStore) lastSaved() *SchedulerState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.saved) == 0 {
		return nil
	}
	return m.saved[len(m.saved)-1]
}

func newMockWatermarkState(lastTick time.Time) *SchedulerState {
	return &SchedulerState{
		Version:  SchedulerStateVersion,
		LastTick: lastTick,
		DAGs:     make(map[string]DAGWatermark),
	}
}

func newHourlyCatchupDAG(t *testing.T, name string) *core.DAG {
	t.Helper()
	return &core.DAG{
		Name:          name,
		CatchupWindow: 6 * time.Hour,
		Schedule:      []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
}

type testProfileResolver struct {
	profile string
	err     error
}

func (r *testProfileResolver) ResolveProfile(context.Context, string, string) (string, error) {
	return r.profile, r.err
}

func mustParseProfileSchedule(t *testing.T, expr, profile string) core.Schedule {
	t.Helper()
	schedule := mustParseSchedule(t, expr)
	schedule.Profile = profile
	return schedule
}

func newTestTickPlanner(store WatermarkStore) (*TickPlanner, chan DAGChangeEvent) {
	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		QueuesEnabled:  true,
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "test-run-id", nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		RunExists: func(_ context.Context, _ *core.DAG, _ string) (bool, error) {
			return false, nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})
	return tp, eventCh
}

func TestTickPlanner_InitNoWatermarkStore(t *testing.T) {
	t.Parallel()
	tp := NewTickPlanner(TickPlannerConfig{})
	err := tp.Init(context.Background(), nil)
	require.NoError(t, err)
}

func TestTickPlanner_InitLoadError(t *testing.T) {
	t.Parallel()
	store := &mockWatermarkStore{loadErr: errors.New("disk error")}
	tp, _ := newTestTickPlanner(store)

	err := tp.Init(context.Background(), nil)
	require.NoError(t, err) // non-fatal
	// Falls back to empty state on load error
	tp.mu.RLock()
	require.NotNil(t, tp.watermarkState)
	require.Equal(t, SchedulerStateVersion, tp.watermarkState.Version)
	tp.mu.RUnlock()
}

func TestTickPlanner_InitWithMissedRuns(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}
	tp, _ := newTestTickPlanner(store)

	dag := newHourlyCatchupDAG(t, "test-dag")
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	buf, ok := tp.buffers["test-dag"]
	require.True(t, ok)
	// Should have 3 missed: 10:00, 11:00, 12:00
	require.Equal(t, 3, buf.Len())
}

func TestTickPlanner_Advance(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	tickTime := time.Date(2026, 2, 7, 13, 0, 0, 0, time.UTC)
	tp.Advance(tickTime)

	tp.mu.RLock()
	require.Equal(t, tickTime, tp.watermarkState.LastTick)
	tp.mu.RUnlock()
	require.True(t, tp.watermarkDirty.Load())
}

func TestTickPlanner_AdvanceBeforeInit(t *testing.T) {
	t.Parallel()

	tp := NewTickPlanner(TickPlannerConfig{})
	// Init must be called before Advance to set watermarkState.
	// This test verifies Init+Advance works with all-default config.
	require.NoError(t, tp.Init(context.Background(), nil))
	tp.Advance(time.Now())
}

func TestTickPlanner_FlushWritesSnapshot(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	tickTime := time.Date(2026, 2, 7, 13, 0, 0, 0, time.UTC)
	tp.Advance(tickTime)
	tp.Flush(context.Background())

	saved := store.lastSaved()
	require.NotNil(t, saved)
	require.Equal(t, tickTime, saved.LastTick)
	require.False(t, tp.watermarkDirty.Load())
}

func TestTickPlanner_FlushRemarksDirtyOnError(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{saveErr: errors.New("write error")}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	tp.Advance(time.Now())
	tp.Flush(context.Background())

	assert.True(t, tp.watermarkDirty.Load())
}

func TestTickPlanner_FlushSkipsWhenClean(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	tp.Flush(context.Background())
	assert.Nil(t, store.lastSaved())
}

func TestTickPlanner_PlanCatchupDispatches(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)),
	}
	tp, _ := newTestTickPlanner(store)

	dag := newHourlyCatchupDAG(t, "my-dag")
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	// Should have one catchup run (drains one per tick)
	assert.Len(t, runs, 1)
	assert.Equal(t, "my-dag", runs[0].DAG.Name)
	assert.Equal(t, core.TriggerTypeCatchUp, runs[0].TriggerType)
}

func TestTickPlanner_PlanCatchupSkipOverlap(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)),
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		QueuesEnabled:  true,
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-1", nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return true, nil // DAG is always running
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := newHourlyCatchupDAG(t, "skip-dag")
	dag.OverlapPolicy = core.OverlapPolicySkip
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	initialLen := tp.buffers["skip-dag"].Len()

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	// Should have skipped (popped without returning a run)
	assert.Len(t, runs, 0)
	// Buffer should be shorter by one (item was popped/discarded)
	if buf, ok := tp.buffers["skip-dag"]; ok {
		assert.Equal(t, initialLen-1, buf.Len())
	}
}

func TestTickPlanner_PlanLiveRun(t *testing.T) {
	t.Parallel()

	// Create a planner with no catchup, just a live schedule
	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "live-run-id", nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := &core.DAG{
		Name:     "live-dag",
		Schedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	// Tick at 12:00 — hourly schedule should fire
	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	assert.Len(t, runs, 1)
	assert.Equal(t, "live-dag", runs[0].DAG.Name)
	assert.Equal(t, core.TriggerTypeScheduler, runs[0].TriggerType)
}

func TestTickPlanner_PlanSuspendedDAGSkipped(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool {
			return true // Always suspended
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-id", nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := &core.DAG{
		Name:     "suspended-dag",
		Schedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0)
}

func TestTickPlanner_PlanSuspendedCatchupDropsBufferAndAdvancesWatermark(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}
	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		QueuesEnabled:  true,
		IsSuspended: func(_ context.Context, _ string) bool {
			return true
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-id", nil
		},
		Clock: func() time.Time {
			return now
		},
		Events: eventCh,
	})

	dag := newHourlyCatchupDAG(t, "suspended-catchup-dag")
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))
	_, ok := tp.buffers[dag.Name]
	require.True(t, ok, "catchup buffer should exist before planning while suspended")

	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0)
	_, ok = tp.buffers[dag.Name]
	assert.False(t, ok, "catchup buffer should be cleared while suspended")

	tp.mu.RLock()
	wm, ok := tp.watermarkState.DAGs[dag.Name]
	tp.mu.RUnlock()
	require.True(t, ok)
	assert.Equal(t, now, wm.LastScheduledTime)
}

func TestTickPlanner_HandleEvent_Added(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	newDAG := &core.DAG{
		Name:     "new-dag",
		Schedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}

	tp.entryMu.Lock()
	tp.handleEvent(context.Background(), DAGChangeEvent{
		Type:    DAGChangeAdded,
		DAG:     newDAG,
		DAGName: "new-dag",
	})
	tp.entryMu.Unlock()

	// Verify entry was added
	_, ok := tp.entries["new-dag"]
	assert.True(t, ok, "new-dag should be in entries")

	// Watermark should be set for new DAG
	tp.mu.RLock()
	_, hasWM := tp.watermarkState.DAGs["new-dag"]
	tp.mu.RUnlock()
	assert.True(t, hasWM, "watermark should be set for new DAG")
	assert.NotNil(t, store.lastSaved(), "new DAG watermark should be flushed immediately")
}

func TestTickPlanner_HandleEvent_Deleted(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	tp, _ := newTestTickPlanner(store)

	dag := &core.DAG{
		Name:     "del-dag",
		Schedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	// Verify it exists
	_, ok := tp.entries["del-dag"]
	require.True(t, ok)

	tp.entryMu.Lock()
	tp.handleEvent(context.Background(), DAGChangeEvent{
		Type:    DAGChangeDeleted,
		DAGName: "del-dag",
	})
	tp.entryMu.Unlock()

	// Verify entry was removed
	_, ok = tp.entries["del-dag"]
	assert.False(t, ok, "del-dag should be removed from entries")

	tp.mu.RLock()
	_, hasWM := tp.watermarkState.DAGs["del-dag"]
	tp.mu.RUnlock()
	assert.True(t, hasWM, "del-dag watermark should be retained during the rewrite grace window")
}

func TestTickPlanner_DeletedWatermarkExpiresAfterGraceWindow(t *testing.T) {
	t.Parallel()

	const graceWindow = 2 * time.Minute

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	clock := now
	eventCh := make(chan DAGChangeEvent, 256)
	store := &mockWatermarkStore{}
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		QueuesEnabled:  true,
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "test-run-id", nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		RunExists: func(_ context.Context, _ *core.DAG, _ string) (bool, error) {
			return false, nil
		},
		Clock: func() time.Time {
			return clock
		},
		Events: eventCh,
	})

	dag := &core.DAG{
		Name:     "deleted-after-grace",
		Schedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	tp.entryMu.Lock()
	tp.handleEvent(context.Background(), DAGChangeEvent{
		Type:    DAGChangeDeleted,
		DAGName: dag.Name,
	})
	tp.entryMu.Unlock()

	tp.mu.RLock()
	_, hasWM := tp.watermarkState.DAGs[dag.Name]
	tp.mu.RUnlock()
	require.True(t, hasWM, "watermark should remain available during the rewrite grace window")

	clock = now.Add(graceWindow + time.Second)
	tp.Plan(context.Background(), clock)
	tp.Flush(context.Background())

	tp.mu.RLock()
	_, hasWM = tp.watermarkState.DAGs[dag.Name]
	tp.mu.RUnlock()
	assert.False(t, hasWM, "watermark should be pruned after the grace window expires")

	saved := store.lastSaved()
	require.NotNil(t, saved)
	_, hasPersisted := saved.DAGs[dag.Name]
	assert.False(t, hasPersisted, "expired deleted watermark should not be persisted")
}

func TestTickPlanner_HandleEvent_Updated(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}
	tp, _ := newTestTickPlanner(store)

	dag := newHourlyCatchupDAG(t, "upd-dag")
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	// Should have a buffer from init
	_, hasBuf := tp.buffers["upd-dag"]
	require.True(t, hasBuf)

	// Send Updated event with different schedule
	updatedDAG := &core.DAG{
		Name:          "upd-dag",
		CatchupWindow: 6 * time.Hour,
		Schedule:      []core.Schedule{mustParseSchedule(t, "*/30 * * * *")}, // changed schedule
	}

	tp.entryMu.Lock()
	tp.handleEvent(context.Background(), DAGChangeEvent{
		Type:    DAGChangeUpdated,
		DAG:     updatedDAG,
		DAGName: "upd-dag",
	})
	tp.entryMu.Unlock()

	// Entry should be updated
	entry, ok := tp.entries["upd-dag"]
	require.True(t, ok)
	assert.Equal(t, "*/30 * * * *", entry.dag.Schedule[0].Expression)
}

func TestTickPlanner_HandleEvent_UpdatedFlushesWatermarkMutationsImmediately(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}
	tp, _ := newTestTickPlanner(store)

	dag := &core.DAG{
		Name:          "upd-latest-dag",
		CatchupWindow: 6 * time.Hour,
		Schedule:      []core.Schedule{mustParseSchedule(t, "0 * * * *")},
		OverlapPolicy: core.OverlapPolicyLatest,
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	tp.entryMu.Lock()
	tp.handleEvent(context.Background(), DAGChangeEvent{
		Type: DAGChangeUpdated,
		DAG: &core.DAG{
			Name:          "upd-latest-dag",
			CatchupWindow: 6 * time.Hour,
			Schedule:      []core.Schedule{mustParseSchedule(t, "*/30 * * * *")},
			OverlapPolicy: core.OverlapPolicyLatest,
		},
		DAGName: "upd-latest-dag",
	})
	tp.entryMu.Unlock()

	assert.NotNil(t, store.lastSaved(), "watermark mutations should be flushed immediately on update")
}

func TestTickPlanner_ConcurrentFlushAndAdvance(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			tp.Advance(time.Date(2026, 2, 7, 12, i, 0, 0, time.UTC))
		}(i)
		go func() {
			defer wg.Done()
			tp.Flush(context.Background())
		}()
	}
	wg.Wait()
}

func TestTickPlanner_PrunesStaleDAGEntries(t *testing.T) {
	t.Parallel()

	state := newMockWatermarkState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC))
	state.DAGs = map[string]DAGWatermark{
		"active-dag":  {LastScheduledTime: time.Date(2026, 2, 7, 8, 0, 0, 0, time.UTC)},
		"deleted-dag": {LastScheduledTime: time.Date(2026, 2, 7, 7, 0, 0, 0, time.UTC)},
		"gone-dag":    {LastScheduledTime: time.Date(2026, 2, 7, 6, 0, 0, 0, time.UTC)},
	}
	store := &mockWatermarkStore{state: state}
	tp, _ := newTestTickPlanner(store)

	dags := []*core.DAG{{Name: "active-dag"}}
	require.NoError(t, tp.Init(context.Background(), dags))

	tp.mu.RLock()
	_, hasActive := tp.watermarkState.DAGs["active-dag"]
	_, hasDeleted := tp.watermarkState.DAGs["deleted-dag"]
	_, hasGone := tp.watermarkState.DAGs["gone-dag"]
	tp.mu.RUnlock()

	assert.True(t, hasActive, "active-dag should remain")
	assert.False(t, hasDeleted, "deleted-dag should be pruned")
	assert.False(t, hasGone, "gone-dag should be pruned")
}

func TestTickPlanner_NilWatermarkStoreFullPath(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		Events: eventCh,
	})

	require.NoError(t, tp.Init(context.Background(), []*core.DAG{{Name: "any-dag"}}))

	tp.Advance(time.Now())
	tp.Plan(context.Background(), time.Now())
	tp.Flush(context.Background())

	// With noop defaults, watermarkState is always initialized
	tp.mu.RLock()
	assert.NotNil(t, tp.watermarkState)
	tp.mu.RUnlock()
}

func TestTickPlanner_AdvanceUpdatesPerDAGWatermarks(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	scheduledTime := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp.lastPlanResult = []PlannedRun{
		{
			DAG:           &core.DAG{Name: "test-dag"},
			RunID:         "run-1",
			ScheduledTime: scheduledTime,
			TriggerType:   core.TriggerTypeScheduler,
			Schedule:      mustParseSchedule(t, "0 * * * *"),
		},
	}

	tickTime := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp.Advance(tickTime)

	tp.mu.RLock()
	wm, ok := tp.watermarkState.DAGs["test-dag"]
	tp.mu.RUnlock()
	assert.True(t, ok, "per-DAG watermark should be set")
	assert.Equal(t, scheduledTime, wm.LastScheduledTime)
}

func TestTickPlanner_PlanBufferCleansEmpty(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)),
	}
	tp, _ := newTestTickPlanner(store)

	dag := newHourlyCatchupDAG(t, "drain-dag")
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	_, exists := tp.buffers["drain-dag"]
	require.True(t, exists, "buffer should exist after init")

	// Drain all items (1 per Plan call)
	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	for range 10 {
		tp.Plan(context.Background(), now)
	}

	_, exists = tp.buffers["drain-dag"]
	assert.False(t, exists, "empty buffer should be removed from map")
}

func TestTickPlanner_ShouldRunGuardRunning(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{Status: core.Running}, nil
		},
		Dispatch: func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-id", nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := &core.DAG{
		Name:     "running-dag",
		Schedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0, "should not plan run when DAG is already running")
}

func TestTickPlanner_PlanStopSchedule(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{Status: core.Running}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-id", nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Location: time.UTC,
		Events:   eventCh,
	})

	dag := &core.DAG{
		Name:         "stop-dag",
		StopSchedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	require.Len(t, runs, 1)
	assert.Equal(t, "stop-dag", runs[0].DAG.Name)
	assert.Equal(t, ScheduleTypeStop, runs[0].ScheduleType)
}

func TestTickPlanner_PlanStopSkipsNotRunning(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{Status: core.Succeeded}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-id", nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Location: time.UTC,
		Events:   eventCh,
	})

	dag := &core.DAG{
		Name:         "stop-dag-not-running",
		StopSchedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0, "stop should be skipped when DAG is not running")
}

func TestTickPlanner_PlanRestartSchedule(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-id", nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Location: time.UTC,
		Events:   eventCh,
	})

	dag := &core.DAG{
		Name:            "restart-dag",
		RestartSchedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	require.Len(t, runs, 1)
	assert.Equal(t, "restart-dag", runs[0].DAG.Name)
	assert.Equal(t, ScheduleTypeRestart, runs[0].ScheduleType)
}

func TestTickPlanner_PlanSuspendedStopSkipped(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool {
			return true // Always suspended
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{Status: core.Running}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-id", nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Location: time.UTC,
		Events:   eventCh,
	})

	dag := &core.DAG{
		Name:         "suspended-stop-dag",
		StopSchedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0, "suspended DAG's stop schedule should be skipped")
}

func TestTickPlanner_AdvanceIgnoresStopRestartWatermarks(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	startTime := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	stopTime := time.Date(2026, 2, 7, 12, 30, 0, 0, time.UTC)
	restartTime := time.Date(2026, 2, 7, 13, 0, 0, 0, time.UTC)

	tp.lastPlanResult = []PlannedRun{
		{
			DAG:           &core.DAG{Name: "test-dag"},
			RunID:         "run-1",
			ScheduledTime: startTime,
			ScheduleType:  ScheduleTypeStart,
			Schedule:      mustParseSchedule(t, "0 * * * *"),
		},
		{
			DAG:           &core.DAG{Name: "test-dag"},
			ScheduledTime: stopTime,
			ScheduleType:  ScheduleTypeStop,
		},
		{
			DAG:           &core.DAG{Name: "test-dag"},
			ScheduledTime: restartTime,
			ScheduleType:  ScheduleTypeRestart,
		},
	}

	tp.Advance(startTime)

	tp.mu.RLock()
	wm, ok := tp.watermarkState.DAGs["test-dag"]
	tp.mu.RUnlock()

	require.True(t, ok, "watermark should exist for test-dag")
	assert.Equal(t, startTime, wm.LastScheduledTime,
		"watermark should reflect start time, not stop/restart")
}

func TestTickPlanner_PlanStopRestartWithNonUTCTimezone(t *testing.T) {
	t.Parallel()

	est := time.FixedZone("EST", -5*3600)

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{Status: core.Running}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-id", nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 20, 0, 0, 0, time.UTC)
		},
		Location: est,
		Events:   eventCh,
	})

	// 3pm EST = 20:00 UTC
	dag := &core.DAG{
		Name:         "tz-stop-dag",
		StopSchedule: []core.Schedule{mustParseSchedule(t, "0 15 * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	// Tick at 20:00 UTC = 15:00 EST — should match the stop schedule
	now := time.Date(2026, 2, 7, 20, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	require.Len(t, runs, 1, "stop schedule should fire in EST timezone")
	assert.Equal(t, ScheduleTypeStop, runs[0].ScheduleType)
}

func TestTickPlanner_IsRunningErrorAssumesNotRunning(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)),
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		QueuesEnabled:  true,
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, errors.New("proc store error")
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-1", nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := newHourlyCatchupDAG(t, "err-running-dag")
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	// IsRunning error should be logged, assumed not running, and catchup dispatched
	assert.Len(t, runs, 1, "should still dispatch catchup run when IsRunning returns error")
}

func TestTickPlanner_IsQueuedErrorDefersCatchupWithoutDroppingState(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)),
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		QueuesEnabled:  true,
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		IsQueued: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, errors.New("queue read failed")
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-1", nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := newHourlyCatchupDAG(t, "queue-error-dag")
	dag.OverlapPolicy = core.OverlapPolicySkip
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	initialBuf, ok := tp.buffers["queue-error-dag"]
	require.True(t, ok)
	initialLen := initialBuf.Len()
	initialItem, ok := initialBuf.Peek()
	require.True(t, ok)

	tp.mu.RLock()
	initialWatermark, hadInitialWatermark := tp.watermarkState.DAGs["queue-error-dag"]
	tp.mu.RUnlock()

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	assert.Len(t, runs, 0, "planner should defer the DAG instead of emitting live or catchup runs")

	buf, ok := tp.buffers["queue-error-dag"]
	require.True(t, ok, "catchup buffer must remain intact on queue read error")
	assert.Equal(t, initialLen, buf.Len())
	item, ok := buf.Peek()
	require.True(t, ok)
	assert.Equal(t, initialItem.ScheduledTime, item.ScheduledTime)

	tp.mu.RLock()
	deferWatermark, hasDeferredWatermark := tp.watermarkState.DAGs["queue-error-dag"]
	tp.mu.RUnlock()
	assert.Equal(t, hadInitialWatermark, hasDeferredWatermark)
	if hadInitialWatermark {
		assert.Equal(t, initialWatermark.LastScheduledTime, deferWatermark.LastScheduledTime)
	}
}

func TestTickPlanner_GetLatestStatusErrorSkipsStop(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, errors.New("status error")
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-id", nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Location: time.UTC,
		Events:   eventCh,
	})

	dag := &core.DAG{
		Name:         "status-err-dag",
		StopSchedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	assert.Len(t, runs, 0, "stop should be skipped when GetLatestStatus returns error")
}

func TestTickPlanner_GenRunIDErrorSkipsStartRun(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "", errors.New("id gen error")
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := &core.DAG{
		Name:     "genid-err-dag",
		Schedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	assert.Len(t, runs, 0, "start run should be skipped when GenRunID returns error")
}

func TestTickPlanner_DispatchRunStopError(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		Stop: func(_ context.Context, _ *core.DAG) error {
			return errors.New("stop failed")
		},
		Events: eventCh,
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	// Should not panic; error is logged internally
	tp.DispatchRun(context.Background(), PlannedRun{
		DAG:           &core.DAG{Name: "stop-err-dag"},
		ScheduledTime: time.Now(),
		ScheduleType:  ScheduleTypeStop,
	})
}

func TestTickPlanner_DispatchRunRestartError(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		Restart: func(_ context.Context, _ *core.DAG, _ time.Time) error {
			return errors.New("restart failed")
		},
		Events: eventCh,
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	// Should not panic; error is logged internally
	tp.DispatchRun(context.Background(), PlannedRun{
		DAG:           &core.DAG{Name: "restart-err-dag"},
		ScheduledTime: time.Now(),
		ScheduleType:  ScheduleTypeRestart,
	})
}

func TestTickPlanner_StopRestartRunsHaveEmptyRunID(t *testing.T) {
	t.Parallel()

	// Use two DAGs: one for start (needs status != Running), one for stop+restart (needs Running)
	startDAG := &core.DAG{
		Name:     "start-dag",
		Schedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	stopRestartDAG := &core.DAG{
		Name:            "stop-restart-dag",
		StopSchedule:    []core.Schedule{mustParseSchedule(t, "0 * * * *")},
		RestartSchedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, dag *core.DAG) (exec.DAGRunStatus, error) {
			if dag.Name == "stop-restart-dag" {
				return exec.DAGRunStatus{Status: core.Running}, nil
			}
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "generated-run-id", nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Location: time.UTC,
		Events:   eventCh,
	})

	require.NoError(t, tp.Init(context.Background(), []*core.DAG{startDAG, stopRestartDAG}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	require.Len(t, runs, 3, "should produce start, stop, and restart runs")

	var startRun, stopRun, restartRun *PlannedRun
	for i := range runs {
		switch runs[i].ScheduleType {
		case ScheduleTypeStart:
			startRun = &runs[i]
		case ScheduleTypeStop:
			stopRun = &runs[i]
		case ScheduleTypeRestart:
			restartRun = &runs[i]
		}
	}

	require.NotNil(t, startRun, "start run should exist")
	require.NotNil(t, stopRun, "stop run should exist")
	require.NotNil(t, restartRun, "restart run should exist")

	assert.NotEmpty(t, startRun.RunID, "start run should have a RunID")
	assert.Empty(t, stopRun.RunID, "stop run should have empty RunID")
	assert.Empty(t, restartRun.RunID, "restart run should have empty RunID")
}

func TestTickPlanner_CatchupBlocksStopRestartSchedules(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		QueuesEnabled:  true,
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{Status: core.Running}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "catchup-run-id", nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Location: time.UTC,
		Events:   eventCh,
	})

	dag := &core.DAG{
		Name:            "catchup-blocks-dag",
		CatchupWindow:   6 * time.Hour,
		Schedule:        []core.Schedule{mustParseSchedule(t, "0 * * * *")},
		StopSchedule:    []core.Schedule{mustParseSchedule(t, "0 * * * *")},
		RestartSchedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	// Should only produce the catchup run, not stop/restart
	require.Len(t, runs, 1, "catchup should block stop/restart schedules")
	assert.Equal(t, core.TriggerTypeCatchUp, runs[0].TriggerType)
}

func TestTickPlanner_ConcurrentPlanAndEvents(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-id", nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	ctx, cancel := context.WithCancel(context.Background())
	tp.Start(ctx)

	var wg sync.WaitGroup

	// Pre-build DAGs outside goroutine to avoid t.Fatal from non-test goroutine
	dags := make([]*core.DAG, 50)
	for i := range 50 {
		dags[i] = &core.DAG{
			Name:     fmt.Sprintf("dag-%d", i),
			Schedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
		}
	}

	// Push events concurrently
	wg.Go(func() {
		for i := range 50 {
			eventCh <- DAGChangeEvent{
				Type:    DAGChangeAdded,
				DAG:     dags[i],
				DAGName: fmt.Sprintf("dag-%d", i),
			}
		}
	})

	// Call Plan concurrently
	wg.Go(func() {
		now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		for range 50 {
			tp.Plan(context.Background(), now)
		}
	})

	wg.Wait()

	// Wait for drain goroutine to process all events by verifying
	// all 50 DAGs are present in the planner's state.
	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	require.Eventually(t, func() bool {
		planned := tp.Plan(context.Background(), now)
		return len(planned) == 50
	}, 5*time.Second, 10*time.Millisecond)

	cancel()
	tp.Stop(context.Background())
}

func TestTickPlanner_ShouldRunSkipIfSuccessful(t *testing.T) {
	t.Parallel()

	// DAG with SkipIfSuccessful=true, schedule fires at 12:00 hourly
	// Latest status: succeeded, started at 11:30 (between prevExecTime=11:00 and scheduledTime=12:00)
	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool { return false },
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{
				Status:    core.Succeeded,
				StartedAt: "2026-02-07T11:30:00Z",
			}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    eventCh,
	})

	dag := &core.DAG{
		Name:             "skip-success-dag",
		SkipIfSuccessful: true,
		Schedule:         []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0, "should skip when SkipIfSuccessful and last run succeeded in interval")
}

func TestTickPlanner_ShouldRunSkipIfSuccessfulIgnoresStaleEditedScheduleSlot(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool { return false },
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{
				Status:       core.Succeeded,
				StartedAt:    "2026-02-07T12:34:00Z",
				ScheduleTime: "2026-02-07T12:34:00Z",
				TriggerType:  core.TriggerTypeScheduler,
			}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 43, 0, 0, time.UTC) },
		Events:    eventCh,
	})

	dag := &core.DAG{
		Name:             "skip-success-edited-schedule-dag",
		SkipIfSuccessful: true,
		Schedule:         []core.Schedule{mustParseSchedule(t, "43 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 43, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 1, "should not skip when the last success belongs to a removed schedule slot")
}

func TestTickPlanner_ShouldRunSkipIfSuccessfulFallsBackToManualRunStartTime(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool { return false },
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{
				Status:      core.Succeeded,
				StartedAt:   "2026-02-07T12:34:00Z",
				TriggerType: core.TriggerTypeManual,
			}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 43, 0, 0, time.UTC) },
		Events:    eventCh,
	})

	dag := &core.DAG{
		Name:             "skip-success-manual-fallback-dag",
		SkipIfSuccessful: true,
		Schedule:         []core.Schedule{mustParseSchedule(t, "43 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 43, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0, "should still skip when a manual run already succeeded in the current interval")
}

func TestLatestScheduledSlotMarksRemovedScheduleSlotStale(t *testing.T) {
	t.Parallel()

	scheduledAt, state := latestScheduledSlot(exec.DAGRunStatus{
		ScheduleTime: "2026-02-07T12:34:00Z",
	}, mustParseSchedule(t, "43 * * * *"))

	assert.Equal(t, latestScheduledSlotStale, state)
	assert.Equal(t, time.Date(2026, 2, 7, 12, 34, 0, 0, time.UTC), scheduledAt)
}

func TestTickPlanner_ProfileScopedStartSchedules(t *testing.T) {
	t.Parallel()

	tp := NewTickPlanner(TickPlannerConfig{
		ProfileResolver: &testProfileResolver{profile: "prod"},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    make(chan DAGChangeEvent, 1),
	})
	dag := &core.DAG{
		Name:     "profile-scoped-start-dag",
		Location: "/tmp/profile-scoped-start-dag.yaml",
		Schedule: []core.Schedule{
			mustParseProfileSchedule(t, "0 * * * *", "prod"),
			mustParseProfileSchedule(t, "0 * * * *", "dev"),
		},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	runs := tp.Plan(context.Background(), time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC))
	require.Len(t, runs, 1)
	assert.Equal(t, "prod", runs[0].Schedule.Profile)
}

func TestTickPlanner_ProfileScopedStartSchedulesWithoutDefaultProfile(t *testing.T) {
	t.Parallel()

	tp := NewTickPlanner(TickPlannerConfig{
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    make(chan DAGChangeEvent, 1),
	})
	dag := &core.DAG{
		Name: "profile-scoped-empty-default-dag",
		Schedule: []core.Schedule{
			mustParseProfileSchedule(t, "0 * * * *", "prod"),
			mustParseSchedule(t, "0 * * * *"),
		},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	runs := tp.Plan(context.Background(), time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC))
	require.Len(t, runs, 1)
	assert.Empty(t, runs[0].Schedule.Profile)
}

func TestTickPlanner_ProfileScopedSchedulesResolveErrorFailsClosed(t *testing.T) {
	t.Parallel()

	tp := NewTickPlanner(TickPlannerConfig{
		ProfileResolver: &testProfileResolver{err: errors.New("profile store unavailable")},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    make(chan DAGChangeEvent, 1),
	})
	dag := &core.DAG{
		Name:     "profile-resolve-error-dag",
		Location: "/tmp/profile-resolve-error-dag.yaml",
		Schedule: []core.Schedule{mustParseProfileSchedule(t, "0 * * * *", "prod")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	runs := tp.Plan(context.Background(), time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC))
	assert.Empty(t, runs)
}

func TestTickPlanner_ProfileScopedStopRestartSchedules(t *testing.T) {
	t.Parallel()

	tp := NewTickPlanner(TickPlannerConfig{
		ProfileResolver: &testProfileResolver{profile: "dev"},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{Status: core.Running}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    make(chan DAGChangeEvent, 1),
	})
	dag := &core.DAG{
		Name:     "profile-scoped-control-dag",
		Location: "/tmp/profile-scoped-control-dag.yaml",
		StopSchedule: []core.Schedule{
			mustParseProfileSchedule(t, "0 * * * *", "prod"),
			mustParseProfileSchedule(t, "0 * * * *", "dev"),
		},
		RestartSchedule: []core.Schedule{
			mustParseProfileSchedule(t, "0 * * * *", "prod"),
			mustParseProfileSchedule(t, "0 * * * *", "dev"),
		},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	runs := tp.Plan(context.Background(), time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC))
	require.Len(t, runs, 2)
	var stopCount, restartCount int
	for _, run := range runs {
		switch run.ScheduleType {
		case ScheduleTypeStart:
			t.Fatalf("unexpected start schedule run: %+v", run)
		case ScheduleTypeStop:
			stopCount++
		case ScheduleTypeRestart:
			restartCount++
		}
	}
	assert.Equal(t, 1, stopCount)
	assert.Equal(t, 1, restartCount)
}

func TestTickPlanner_ProfileScopedCatchupSchedules(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore:  store,
		QueuesEnabled:   true,
		ProfileResolver: &testProfileResolver{profile: "dev"},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    make(chan DAGChangeEvent, 1),
	})
	dag := &core.DAG{
		Name:          "profile-scoped-catchup-dag",
		Location:      "/tmp/profile-scoped-catchup-dag.yaml",
		CatchupWindow: 6 * time.Hour,
		Schedule: []core.Schedule{
			mustParseProfileSchedule(t, "0 * * * *", "prod"),
			mustParseProfileSchedule(t, "30 * * * *", "dev"),
		},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	buf, ok := tp.buffers[dag.Name]
	require.True(t, ok)
	require.Equal(t, 3, buf.Len())
	for buf.Len() > 0 {
		item, ok := buf.Pop()
		require.True(t, ok)
		assert.Equal(t, "dev", item.Schedule.Profile)
	}
}

func TestTickPlanner_ProfileChangeDropsInactiveCatchupSchedules(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}
	resolver := &testProfileResolver{profile: "dev"}
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore:  store,
		QueuesEnabled:   true,
		ProfileResolver: resolver,
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return now },
		Events:    make(chan DAGChangeEvent, 1),
	})
	dag := &core.DAG{
		Name:          "profile-change-catchup-dag",
		Location:      "/tmp/profile-change-catchup-dag.yaml",
		CatchupWindow: 6 * time.Hour,
		Schedule: []core.Schedule{
			mustParseProfileSchedule(t, "30 * * * *", "dev"),
		},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	buf, ok := tp.buffers[dag.Name]
	require.True(t, ok)
	require.Equal(t, 3, buf.Len())

	resolver.profile = "prod"
	runs := tp.Plan(context.Background(), now)
	assert.Empty(t, runs)
	_, ok = tp.buffers[dag.Name]
	assert.False(t, ok)

	tp.mu.RLock()
	wm, ok := tp.watermarkState.DAGs[dag.Name]
	tp.mu.RUnlock()
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 2, 7, 11, 30, 0, 0, time.UTC), wm.LastScheduledTime)
}

func TestTickPlanner_ShouldRunAlreadyFinished(t *testing.T) {
	t.Parallel()

	// Latest status has StartedAt >= scheduledTime (12:00)
	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool { return false },
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{
				Status:    core.Succeeded,
				StartedAt: "2026-02-07T12:00:00Z",
			}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    eventCh,
	})

	dag := &core.DAG{
		Name:     "already-finished-dag",
		Schedule: []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0, "should skip when last run started at/after scheduled time")
}

func TestTickPlanner_ShouldRunFailedPreviousRunNotSkipped(t *testing.T) {
	t.Parallel()

	// SkipIfSuccessful=true but last run failed — should NOT skip
	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool { return false },
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{
				Status:    core.Failed,
				StartedAt: "2026-02-07T11:30:00Z",
			}, nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    eventCh,
	})

	dag := &core.DAG{
		Name:             "failed-run-dag",
		SkipIfSuccessful: true,
		Schedule:         []core.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 1, "should NOT skip when SkipIfSuccessful but last run failed")
}

func TestTickPlanner_DispatchRunStart(t *testing.T) {
	t.Parallel()

	var (
		dispatched      bool
		gotScheduleTime time.Time
	)
	tp := NewTickPlanner(TickPlannerConfig{
		Dispatch: func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, scheduleTime time.Time) error {
			dispatched = true
			gotScheduleTime = scheduleTime
			return nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	scheduledTime := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp.DispatchRun(context.Background(), PlannedRun{
		DAG:           &core.DAG{Name: "start-dag"},
		RunID:         "run-1",
		ScheduledTime: scheduledTime,
		ScheduleType:  ScheduleTypeStart,
		TriggerType:   core.TriggerTypeScheduler,
	})
	assert.True(t, dispatched, "Dispatch callback should be invoked for ScheduleTypeStart")
	assert.Equal(t, scheduledTime, gotScheduleTime, "Dispatch callback should receive the scheduled time")
}

func TestTickPlanner_DispatchRunSuspendedStartSkipped(t *testing.T) {
	t.Parallel()

	dispatched := false
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) bool { return true },
		Dispatch: func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
			dispatched = true
			return nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	tp.DispatchRun(context.Background(), PlannedRun{
		DAG:           &core.DAG{Name: "start-dag"},
		RunID:         "run-1",
		ScheduledTime: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		ScheduleType:  ScheduleTypeStart,
		TriggerType:   core.TriggerTypeScheduler,
	})

	assert.False(t, dispatched, "suspended scheduler-managed run should not dispatch")
}

func TestTickPlanner_DispatchRunSuspendedCatchupAdvancesWatermark(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	enqueued := false
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		QueuesEnabled:  true,
		IsSuspended:    func(_ context.Context, _ string) bool { return true },
		Enqueue: func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
			enqueued = true
			return nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	scheduledTime := time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)
	dag := newHourlyCatchupDAG(t, "suspended-catchup-dag")
	tp.DispatchRun(context.Background(), PlannedRun{
		DAG:           dag,
		RunID:         "run-1",
		ScheduledTime: scheduledTime,
		ScheduleType:  ScheduleTypeStart,
		TriggerType:   core.TriggerTypeCatchUp,
	})

	assert.False(t, enqueued, "suspended catchup run should not enqueue")
	tp.mu.RLock()
	wm, ok := tp.watermarkState.DAGs[dag.Name]
	tp.mu.RUnlock()
	require.True(t, ok)
	assert.Equal(t, scheduledTime, wm.LastScheduledTime)
}

func TestTickPlanner_DispatchRunLegacyCatchupAttemptAdvancesWatermark(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{}
	scheduledTime := time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)
	dag := newHourlyCatchupDAG(t, "legacy.catchup-dag")
	legacyRunID := generateLegacyCatchupRunID(dag.Name, scheduledTime)
	var checkedRunIDs []string

	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		QueuesEnabled:  true,
		Enqueue: func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
			t.Fatal("enqueue should not be called when the legacy run already exists")
			return nil
		},
		RunExists: func(_ context.Context, _ *core.DAG, runID string) (bool, error) {
			checkedRunIDs = append(checkedRunIDs, runID)
			return runID == legacyRunID, nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	run, ok := tp.createPlannedRun(context.Background(), dag, core.Schedule{}, scheduledTime, core.TriggerTypeCatchUp)
	require.True(t, ok)
	tp.DispatchRun(context.Background(), run)

	assert.Equal(t, []string{legacyRunID}, checkedRunIDs)
	tp.mu.RLock()
	wm, ok := tp.watermarkState.DAGs[dag.Name]
	tp.mu.RUnlock()
	require.True(t, ok)
	assert.Equal(t, scheduledTime, wm.LastScheduledTime)
}

func TestTickPlanner_DispatchRunRestartForwardsScheduledTime(t *testing.T) {
	t.Parallel()

	var gotScheduleTime time.Time
	tp := NewTickPlanner(TickPlannerConfig{
		Restart: func(_ context.Context, _ *core.DAG, scheduleTime time.Time) error {
			gotScheduleTime = scheduleTime
			return nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	scheduledTime := time.Date(2026, 2, 7, 13, 0, 0, 0, time.UTC)
	tp.DispatchRun(context.Background(), PlannedRun{
		DAG:           &core.DAG{Name: "restart-dag"},
		ScheduledTime: scheduledTime,
		ScheduleType:  ScheduleTypeRestart,
	})

	assert.Equal(t, scheduledTime, gotScheduleTime)
}

func TestTickPlanner_StartStop(t *testing.T) {
	t.Parallel()

	tp, _ := newTestTickPlanner(&mockWatermarkStore{})
	require.NoError(t, tp.Init(context.Background(), nil))

	ctx := context.Background()
	tp.Start(ctx)

	// Stop should not hang; Start uses wg.Add(2) synchronously so Stop
	// can be called immediately without waiting for the goroutines to begin.
	done := make(chan struct{})
	go func() {
		tp.Stop(ctx)
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not complete in time")
	}
}

func TestTickPlanner_InitBuffersLatestCollapse(t *testing.T) {
	t.Parallel()

	// Watermark at 06:00, now at 12:00, hourly cron → 6 missed intervals.
	// With "latest" policy, buffer should collapse to 1 item (12:00).
	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 6, 0, 0, 0, time.UTC)),
	}
	tp, _ := newTestTickPlanner(store)

	dag := &core.DAG{
		Name:          "latest-init-dag",
		CatchupWindow: 12 * time.Hour,
		Schedule:      []core.Schedule{mustParseSchedule(t, "0 * * * *")},
		OverlapPolicy: core.OverlapPolicyLatest,
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	buf, ok := tp.buffers["latest-init-dag"]
	require.True(t, ok, "buffer should exist")
	assert.Equal(t, 1, buf.Len(), "latest policy should collapse buffer to 1 item")

	item, ok := buf.Peek()
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC), item.ScheduledTime,
		"remaining item should be the latest (12:00)")

	// Watermark should be advanced past the discarded items
	tp.mu.RLock()
	wm, hasWM := tp.watermarkState.DAGs["latest-init-dag"]
	tp.mu.RUnlock()
	require.True(t, hasWM)
	assert.Equal(t, time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC), wm.LastScheduledTime,
		"watermark should be at the last discarded item (11:00)")
}

func TestTickPlanner_PlanLatestNotRunning(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		QueuesEnabled:  true,
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-latest", nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return false, nil // DAG is not running
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := &core.DAG{
		Name:          "latest-nr-dag",
		CatchupWindow: 6 * time.Hour,
		Schedule:      []core.Schedule{mustParseSchedule(t, "0 * * * *")},
		OverlapPolicy: core.OverlapPolicyLatest,
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	// Should dispatch exactly 1 run — the latest (12:00), not the oldest (10:00)
	require.Len(t, runs, 1)
	assert.Equal(t, time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC), runs[0].ScheduledTime,
		"should dispatch the latest interval, not the oldest")
	assert.Equal(t, core.TriggerTypeCatchUp, runs[0].TriggerType)

	// Buffer should be empty after dispatch
	_, bufExists := tp.buffers["latest-nr-dag"]
	assert.False(t, bufExists, "buffer should be cleaned up after draining")
}

func TestTickPlanner_PlanLatestRunning(t *testing.T) {
	t.Parallel()

	store := &mockWatermarkStore{
		state: newMockWatermarkState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		WatermarkStore: store,
		QueuesEnabled:  true,
		IsSuspended: func(_ context.Context, _ string) bool {
			return false
		},
		GetLatestStatus: func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-latest", nil
		},
		IsRunning: func(_ context.Context, _ *core.DAG) (bool, error) {
			return true, nil // DAG is running
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := &core.DAG{
		Name:          "latest-run-dag",
		CatchupWindow: 6 * time.Hour,
		Schedule:      []core.Schedule{mustParseSchedule(t, "0 * * * *")},
		OverlapPolicy: core.OverlapPolicyLatest,
	}
	require.NoError(t, tp.Init(context.Background(), []*core.DAG{dag}))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	// No dispatch when DAG is running
	assert.Len(t, runs, 0, "should not dispatch when DAG is running")

	// Buffer should be collapsed to 1 item (the latest)
	buf, ok := tp.buffers["latest-run-dag"]
	require.True(t, ok, "buffer should still exist")
	assert.Equal(t, 1, buf.Len(), "buffer should be collapsed to 1 item")

	item, ok := buf.Peek()
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC), item.ScheduledTime,
		"remaining item should be the latest (12:00)")

	// Watermark should be advanced past discarded items
	tp.mu.RLock()
	wm, hasWM := tp.watermarkState.DAGs["latest-run-dag"]
	tp.mu.RUnlock()
	require.True(t, hasWM)
	assert.Equal(t, time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC), wm.LastScheduledTime,
		"watermark should be at the last discarded item (11:00)")
}

func TestComputePrevExecTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		schedule string
		next     time.Time
		want     time.Time
	}{
		{
			name:     "HourlySchedule",
			schedule: "0 * * * *",
			next:     time.Date(2020, 1, 1, 2, 0, 0, 0, time.UTC),
			want:     time.Date(2020, 1, 1, 1, 0, 0, 0, time.UTC),
		},
		{
			name:     "EveryFiveMinutes",
			schedule: "*/5 * * * *",
			next:     time.Date(2020, 1, 1, 1, 5, 0, 0, time.UTC),
			want:     time.Date(2020, 1, 1, 1, 0, 0, 0, time.UTC),
		},
		{
			name:     "DailySchedule",
			schedule: "0 0 * * *",
			next:     time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC),
			want:     time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "NonUniform_9_17",
			schedule: "0 9,17 * * *",
			next:     time.Date(2020, 1, 1, 17, 0, 0, 0, time.UTC),
			want:     time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC),
		},
		{
			name:     "NonUniform_9_17_AtMorning",
			schedule: "0 9,17 * * *",
			next:     time.Date(2020, 1, 2, 9, 0, 0, 0, time.UTC),
			want:     time.Date(2020, 1, 1, 17, 0, 0, 0, time.UTC),
		},
		{
			name:     "WeeklySchedule",
			schedule: "0 9 * * 1",
			next:     time.Date(2020, 1, 13, 9, 0, 0, 0, time.UTC), // Monday
			want:     time.Date(2020, 1, 6, 9, 0, 0, 0, time.UTC),  // Previous Monday
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched := mustParseSchedule(t, tt.schedule)
			got := computePrevExecTime(tt.next, sched)
			assert.Equal(t, tt.want, got)
		})
	}
}
