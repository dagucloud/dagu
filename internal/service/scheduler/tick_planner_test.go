// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStateStore struct {
	state   *schedulerstate.State
	loadErr error
	saveErr error
	mu      sync.Mutex
	saved   []*schedulerstate.State
	onSave  func(*schedulerstate.State)
}

func (m *mockStateStore) Load(_ context.Context) (*schedulerstate.State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	if m.state == nil {
		return &schedulerstate.State{
			DAGs: make(map[string]schedulerstate.DAGWatermark),
		}, nil
	}
	return schedulerstate.Clone(m.state), nil
}

func (m *mockStateStore) Save(_ context.Context, state *schedulerstate.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saved = append(m.saved, schedulerstate.Clone(state))
	if m.onSave != nil {
		m.onSave(state)
	}
	return nil
}

func (m *mockStateStore) lastSaved() *schedulerstate.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.saved) == 0 {
		return nil
	}
	return schedulerstate.Clone(m.saved[len(m.saved)-1])
}

func newMockState(lastTick time.Time) *schedulerstate.State {
	return &schedulerstate.State{
		LastTick: lastTick,
		DAGs:     make(map[string]schedulerstate.DAGWatermark),
	}
}

func newHourlyCatchupDAG(t *testing.T, name string) *ir.DAG {
	t.Helper()
	return &ir.DAG{
		Name:          name,
		CatchupWindow: 6 * time.Hour,
		Schedule:      []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
}

func testDAGEntries(dags ...*ir.DAG) []DAGEntry {
	entries := make([]DAGEntry, 0, len(dags))
	for _, dag := range dags {
		entries = append(entries, DAGEntry{DefinitionID: dag.SuspendFlagName(), DAG: dag})
	}
	return entries
}

type testProfileResolver struct {
	profile       string
	err           error
	dagName       string
	workspaceName string
}

func (r *testProfileResolver) ResolveProfile(_ context.Context, dagName string, workspaceName string) (string, error) {
	r.dagName = dagName
	r.workspaceName = workspaceName
	return r.profile, r.err
}

func mustParseProfileSchedule(t *testing.T, expr, profile string) ir.Schedule {
	t.Helper()
	schedule := mustParseSchedule(t, expr)
	schedule.Profile = profile
	return schedule
}

func newTestTickPlanner(store schedulerstate.Store) (*TickPlanner, chan DAGChangeEvent) {
	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:    store,
		QueuesEnabled: true,
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "test-run-id", nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
			return false, nil
		},
		RunExists: func(_ context.Context, _ *ir.DAG, _ string) (bool, error) {
			return false, nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Location: time.UTC,
		Events:   eventCh,
	})
	return tp, eventCh
}

func TestResumePreservesPending(t *testing.T) {
	for _, policy := range []ir.OverlapPolicy{ir.OverlapPolicyAll, ir.OverlapPolicyLatest} {
		t.Run(string(policy), func(t *testing.T) {
			base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
			wake := base.Add(10 * time.Minute)
			store := &mockStateStore{state: newMockState(base.Add(-2 * time.Minute))}
			planner, _ := newTestTickPlanner(store)
			planner.cfg.Clock = func() time.Time { return base }
			planner.cfg.Location = time.UTC
			running := true
			planner.cfg.IsRunning = func(context.Context, *ir.DAG) (bool, error) { return running, nil }
			planner.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error { return nil }
			dag := &ir.DAG{Name: "pending", CatchupWindow: 3 * time.Minute, OverlapPolicy: policy,
				Schedule: []ir.Schedule{mustParseSchedule(t, "* * * * *")}}
			require.NoError(t, planner.Init(t.Context(), testDAGEntries(dag)))
			planner.resume(t.Context(), wake)
			planner.resume(t.Context(), wake)
			require.Empty(t, planner.Plan(t.Context(), wake), "active work must defer catchup")
			planner.Advance(wake)
			running = false
			want := []time.Time{base.Add(-time.Minute), base, wake.Add(-2 * time.Minute), wake.Add(-time.Minute), wake}
			if policy == ir.OverlapPolicyLatest {
				want = []time.Time{wake}
			}
			for i, scheduled := range want {
				tick := wake.Add(time.Duration(i+1) * time.Minute)
				runs := planner.Plan(t.Context(), tick)
				require.Len(t, runs, 1)
				require.Equal(t, ir.TriggerTypeCatchUp, runs[0].TriggerType)
				require.True(t, scheduled.Equal(runs[0].ScheduledTime), "slot %d: %s", i, runs[0].ScheduledTime)
				planner.DispatchRun(t.Context(), runs[0])
				planner.Advance(tick)
			}
			// All recovered slots were consumed once; the next run is a new live slot.
			next := wake.Add(time.Duration(len(want)+1) * time.Minute)
			runs := planner.Plan(t.Context(), next)
			require.Len(t, runs, 1)
			require.Equal(t, ir.TriggerTypeScheduler, runs[0].TriggerType)
			require.True(t, next.Equal(runs[0].ScheduledTime))
		})
	}
}

func TestCatchupBufferLimit(t *testing.T) {
	const gap = 10
	for _, path := range []string{"resume", "update"} {
		for _, policy := range []ir.OverlapPolicy{ir.OverlapPolicyAll, ir.OverlapPolicySkip, ir.OverlapPolicyLatest} {
			for _, pending := range []int{2, DefaultMaxBufferItems - gap, DefaultMaxBufferItems - 1, DefaultMaxBufferItems} {
				t.Run(fmt.Sprintf("%s/%s/%d", path, policy, pending), func(t *testing.T) {
					base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
					now := base
					store := &mockStateStore{state: newMockState(base.Add(-time.Duration(pending) * time.Minute))}
					planner, _ := newTestTickPlanner(store)
					planner.cfg.Clock = func() time.Time { return now }
					planner.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error {
						return errors.New("enqueue failed")
					}
					dag := &ir.DAG{Name: "buffer-limit", CatchupWindow: 48 * time.Hour, OverlapPolicy: policy,
						Schedule: []ir.Schedule{mustParseSchedule(t, "* * * * *")}}
					require.NoError(t, planner.Init(t.Context(), testDAGEntries(dag)))
					planner.Advance(base)
					now = base.Add(gap * time.Minute)
					for range 2 {
						switch path {
						case "resume":
							planner.resume(t.Context(), now)
						case "update":
							updated := *dag
							updated.Timeout = time.Minute
							planner.entryMu.Lock()
							planner.applyEvent(t.Context(), DAGChangeEvent{Type: DAGChangeUpdated, DAGEntry: DAGEntry{DAG: &updated}})
							planner.entryMu.Unlock()
						}
					}

					// Recovery retains the newest slots once, including when the buffer fills.
					count := min(pending+gap, DefaultMaxBufferItems)
					if policy == ir.OverlapPolicyLatest {
						count = 1
					}
					planner.Flush(t.Context())
					if count < pending+gap {
						// Persist discarded slots without consuming the retained backlog.
						want := now.Add(-time.Duration(count) * time.Minute)
						require.True(t, want.Equal(store.lastSaved().DAGs[dag.Name].LastScheduledTime), "discarded slots must advance the checkpoint to %s", want)
					}

					// A failed enqueue must preserve the first retained slot for retry.
					runs := planner.Plan(t.Context(), now)
					require.Len(t, runs, 1)
					planner.DispatchRun(t.Context(), runs[0])
					planner.Advance(now)
					planner.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error { return nil }

					for i := range count {
						tick := now.Add(time.Duration(i+1) * time.Minute)
						runs := planner.Plan(t.Context(), tick)
						require.Len(t, runs, 1)
						require.Equal(t, ir.TriggerTypeCatchUp, runs[0].TriggerType)
						want := now.Add(time.Duration(i-count+1) * time.Minute)
						require.True(t, want.Equal(runs[0].ScheduledTime), "slot %d: got %s, want %s", i, runs[0].ScheduledTime, want)
						planner.DispatchRun(t.Context(), runs[0])
						planner.Advance(tick)
					}
					runs = planner.Plan(t.Context(), now.Add(time.Duration(count+1)*time.Minute))
					require.Len(t, runs, 1)
					require.Equal(t, ir.TriggerTypeScheduler, runs[0].TriggerType)
				})
			}
		}
	}
}

func TestCatchupTimezone(t *testing.T) {
	location, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	wake := time.Date(2026, 1, 2, 10, 0, 0, 0, location).UTC()
	checkpoint := wake.Add(-2 * time.Hour)
	for _, path := range []string{"init", "update", "resume"} {
		for _, expression := range []string{"0 9 * * *", "CRON_TZ=UTC 0 0 * * *"} {
			t.Run(path+"/"+expression, func(t *testing.T) {
				now := checkpoint
				if path == "init" {
					now = wake
				}
				planner, _ := newTestTickPlanner(&mockStateStore{state: newMockState(checkpoint)})
				planner.cfg.Clock = func() time.Time { return now }
				planner.cfg.Location = location
				dag := &ir.DAG{Name: "local-morning", CatchupWindow: 4 * time.Hour, OverlapPolicy: ir.OverlapPolicyLatest,
					Schedule: []ir.Schedule{mustParseSchedule(t, expression)}}
				require.NoError(t, planner.Init(t.Context(), testDAGEntries(dag)))
				now = wake
				switch path {
				case "update":
					planner.entryMu.Lock()
					planner.applyEvent(t.Context(), DAGChangeEvent{Type: DAGChangeUpdated, DAGEntry: DAGEntry{DAG: dag}})
					planner.entryMu.Unlock()
				case "resume":
					planner.resume(t.Context(), now)
				}
				// A UTC checkpoint at 08:00 JST must recover the missed 09:00 JST run.
				runs := planner.Plan(t.Context(), now)
				require.Len(t, runs, 1)
				require.Equal(t, ir.TriggerTypeCatchUp, runs[0].TriggerType)
				require.True(t, wake.Add(-time.Hour).Equal(runs[0].ScheduledTime))
			})
		}
	}
}

func TestTickPlanner_InitNoStateStore(t *testing.T) {
	t.Parallel()
	tp := NewTickPlanner(TickPlannerConfig{})
	err := tp.Init(context.Background(), nil)
	require.NoError(t, err)
}

func TestTickPlanner_InitLoadError(t *testing.T) {
	t.Parallel()
	store := &mockStateStore{loadErr: errors.New("disk error")}
	tp, _ := newTestTickPlanner(store)

	err := tp.Init(context.Background(), nil)
	require.NoError(t, err) // non-fatal
	// Falls back to empty state on load error
	tp.mu.RLock()
	require.NotNil(t, tp.watermarkState)
	require.NotNil(t, tp.watermarkState.DAGs)
	tp.mu.RUnlock()
}

func TestTickPlanner_InitSkipsEntriesWithoutDAGs(t *testing.T) {
	t.Parallel()

	tp, _ := newTestTickPlanner(&mockStateStore{})
	require.NoError(t, tp.Init(context.Background(), []DAGEntry{{DefinitionID: "invalid.yaml"}}))
	assert.Empty(t, tp.entries)
	assert.Empty(t, tp.buffers)
}

func TestTickPlanner_InitWithMissedRuns(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}
	tp, _ := newTestTickPlanner(store)

	dag := newHourlyCatchupDAG(t, "test-dag")
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	buf, ok := tp.buffers["test-dag"]
	require.True(t, ok)
	// Should have 3 missed: 10:00, 11:00, 12:00
	require.Equal(t, 3, buf.Len())
}

func TestTickPlanner_Advance(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	tickTime := time.Date(2026, 2, 7, 13, 0, 0, 0, time.UTC)
	tp.Advance(tickTime)

	tp.mu.RLock()
	require.Equal(t, tickTime, tp.watermarkState.LastTick)
	tp.mu.RUnlock()
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

	store := &mockStateStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	tickTime := time.Date(2026, 2, 7, 13, 0, 0, 0, time.UTC)
	tp.Advance(tickTime)
	tp.Flush(context.Background())

	saved := store.lastSaved()
	require.NotNil(t, saved)
	require.Equal(t, tickTime, saved.LastTick)
}

func TestTickPlanner_FlushHandlesSaveError(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{saveErr: errors.New("write error")}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	tp.Advance(time.Now())
	tp.Flush(context.Background())

	assert.Nil(t, store.lastSaved())
}

func TestTickPlanner_FlushWritesCurrentSnapshot(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	tp.Flush(context.Background())
	assert.NotNil(t, store.lastSaved())
}

// heldStateStore holds the save of the snapshot taken at holdTick until
// release closes, to keep a flush between its snapshot and its save.
type heldStateStore struct {
	mockStateStore
	holdTick time.Time
	held     chan struct{}
	release  chan struct{}
}

func (s *heldStateStore) Save(ctx context.Context, state *schedulerstate.State) error {
	if state.LastTick.Equal(s.holdTick) {
		close(s.held)
		<-s.release
	}
	return s.mockStateStore.Save(ctx, state)
}

// A flush that snapshots later must save later, or the older snapshot stays
// on disk until the next flush.
func TestTickPlanner_FlushSavesInSnapshotOrder(t *testing.T) {
	t.Parallel()

	first := time.Date(2026, 2, 7, 13, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	store := &heldStateStore{holdTick: first, held: make(chan struct{}), release: make(chan struct{})}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(t.Context(), nil))

	tp.Advance(first)
	firstDone := make(chan struct{})
	go func() {
		tp.Flush(t.Context())
		close(firstDone)
	}()
	<-store.held

	tp.Advance(second)
	secondDone := make(chan struct{})
	go func() {
		tp.Flush(t.Context())
		close(secondDone)
	}()
	// Give the later flush time to overtake the held one.
	select {
	case <-secondDone:
	case <-time.After(100 * time.Millisecond):
	}
	close(store.release)
	<-firstDone
	<-secondDone

	assert.Equal(t, second, store.lastSaved().LastTick)
}

// Events already queued, as one directory refresh sends them, are saved
// together, and a lone event is still saved without waiting for the
// periodic flush.
func TestTickPlanner_DrainSavesQueuedEventsOnce(t *testing.T) {
	t.Parallel()

	const queued = 200
	store := &mockStateStore{}
	tp, eventCh := newTestTickPlanner(store)
	require.NoError(t, tp.Init(t.Context(), nil))
	savesAfterInit := len(store.saved)

	addEvent := func(name string) DAGChangeEvent {
		return DAGChangeEvent{Type: DAGChangeAdded, DAGEntry: DAGEntry{DAG: &ir.DAG{
			Name:     name,
			Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
		}}}
	}
	for i := range queued {
		eventCh <- addEvent(fmt.Sprintf("dag-%d", i))
	}
	savedDAGs := func() int {
		saved := store.lastSaved()
		if saved == nil {
			return 0
		}
		return len(saved.DAGs)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tp.Start(ctx)
	require.Eventually(t, func() bool { return savedDAGs() == queued }, 2*time.Second, 5*time.Millisecond)
	store.mu.Lock()
	burstSaves := len(store.saved) - savesAfterInit
	store.mu.Unlock()
	assert.Equal(t, 1, burstSaves)

	eventCh <- addEvent("lone-dag")
	require.Eventually(t, func() bool { return savedDAGs() == queued+1 }, 2*time.Second, 5*time.Millisecond)
}

func TestTickPlanner_PlanCatchupDispatches(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)),
	}
	tp, _ := newTestTickPlanner(store)

	dag := newHourlyCatchupDAG(t, "my-dag")
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	// Should have one catchup run (drains one per tick)
	assert.Len(t, runs, 1)
	assert.Equal(t, "my-dag", runs[0].DAG.Name)
	assert.Equal(t, ir.TriggerTypeCatchUp, runs[0].TriggerType)
}

func TestTickPlanner_PlanCatchupSkipOverlap(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)),
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:    store,
		QueuesEnabled: true,
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-1", nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
			return true, nil // DAG is always running
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := newHourlyCatchupDAG(t, "skip-dag")
	dag.OverlapPolicy = ir.OverlapPolicySkip
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

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
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "live-run-id", nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
			return false, nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := &ir.DAG{
		Name:     "live-dag",
		Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	// Tick at 12:00 — hourly schedule should fire
	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	assert.Len(t, runs, 1)
	assert.Equal(t, "live-dag", runs[0].DAG.Name)
	assert.Equal(t, ir.TriggerTypeScheduler, runs[0].TriggerType)
}

func TestTickPlanner_PlanEveryInterval(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "every-run-id", nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
			return false, nil
		},
		Clock: func() time.Time {
			return start
		},
		Events: eventCh,
	})

	schedule, err := ir.NewCronSchedule("@every 2m")
	require.NoError(t, err)
	dag := &ir.DAG{
		Name:     "every-dag",
		Schedule: []ir.Schedule{schedule},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	// The epoch-aligned two-minute grid lands on every other minute tick.
	for i, wantRun := range []bool{true, false, true} {
		tick := start.Add(time.Duration(i) * time.Minute)
		runs := tp.Plan(context.Background(), tick)
		if !wantRun {
			assert.Empty(t, runs, "tick %s", tick)
			continue
		}
		require.Len(t, runs, 1, "tick %s", tick)
		assert.True(t, tick.Equal(runs[0].ScheduledTime), "tick %s: got %s", tick, runs[0].ScheduledTime)
	}
}

func TestTickPlanner_PlanSuspendedDAGSkipped(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return true, nil // Always suspended
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-id", nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
			return false, nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := &ir.DAG{
		Name:     "suspended-dag",
		Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0)
}

func TestTickPlanner_PlanSuspendedCatchupDropsBufferAndAdvancesWatermark(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}
	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:    store,
		QueuesEnabled: true,
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return true, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
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
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))
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

func TestTickPlanner_ApplyEvent_Added(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	newDAG := &ir.DAG{
		Name:     "new-dag",
		Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}

	tp.entryMu.Lock()
	changed := tp.applyEvent(context.Background(), DAGChangeEvent{
		Type:     DAGChangeAdded,
		DAGEntry: DAGEntry{DAG: newDAG},
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
	assert.True(t, changed, "new DAG watermark should need saving")
}

func TestTickPlanner_ApplyEvent_Deleted(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{}
	tp, _ := newTestTickPlanner(store)

	dag := &ir.DAG{
		Name:     "del-dag",
		Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	// Verify it exists
	_, ok := tp.entries["del-dag"]
	require.True(t, ok)

	tp.entryMu.Lock()
	tp.applyEvent(context.Background(), DAGChangeEvent{
		Type:     DAGChangeDeleted,
		DAGEntry: DAGEntry{DAG: dag},
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
	store := &mockStateStore{}
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:    store,
		QueuesEnabled: true,
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "test-run-id", nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
			return false, nil
		},
		RunExists: func(_ context.Context, _ *ir.DAG, _ string) (bool, error) {
			return false, nil
		},
		Clock: func() time.Time {
			return clock
		},
		Events: eventCh,
	})

	dag := &ir.DAG{
		Name:     "deleted-after-grace",
		Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	tp.entryMu.Lock()
	tp.applyEvent(context.Background(), DAGChangeEvent{
		Type:     DAGChangeDeleted,
		DAGEntry: DAGEntry{DAG: dag},
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

func TestTickPlanner_ApplyEvent_Updated(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}
	tp, _ := newTestTickPlanner(store)

	dag := newHourlyCatchupDAG(t, "upd-dag")
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	// Should have a buffer from init
	_, hasBuf := tp.buffers["upd-dag"]
	require.True(t, hasBuf)

	// Send Updated event with different schedule
	updatedDAG := &ir.DAG{
		Name:          "upd-dag",
		CatchupWindow: 6 * time.Hour,
		Schedule:      []ir.Schedule{mustParseSchedule(t, "*/30 * * * *")}, // changed schedule
	}

	tp.entryMu.Lock()
	tp.applyEvent(context.Background(), DAGChangeEvent{
		Type:     DAGChangeUpdated,
		DAGEntry: DAGEntry{DAG: updatedDAG},
	})
	tp.entryMu.Unlock()

	// Entry should be updated
	entry, ok := tp.entries["upd-dag"]
	require.True(t, ok)
	assert.Equal(t, "*/30 * * * *", entry.DAG.Schedule[0].Expression)
}

func TestUpdatePreservesPending(t *testing.T) {
	for _, change := range []string{"timeout", "latest", "latest-gap", "disabled", "removed", "schedule"} {
		t.Run(change, func(t *testing.T) {
			base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
			now := base
			planner, _ := newTestTickPlanner(&mockStateStore{state: newMockState(base.Add(-2 * time.Minute))})
			planner.cfg.Clock = func() time.Time { return now }
			planner.cfg.Location = time.UTC
			planner.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error { return nil }
			dag := &ir.DAG{Name: "updated", CatchupWindow: time.Hour, OverlapPolicy: ir.OverlapPolicyAll,
				Schedule: []ir.Schedule{mustParseSchedule(t, "* * * * *")}}
			if change == "schedule" {
				dag.Schedule = []ir.Schedule{mustParseSchedule(t, "59 * * * *"), mustParseSchedule(t, "0 * * * *")}
			}
			require.NoError(t, planner.Init(t.Context(), testDAGEntries(dag)))
			// Pending work survives later global checkpoints and unrelated definition edits.
			now = base.Add(time.Minute)
			planner.Advance(now)
			updated := *dag
			updated.Timeout = 10 * time.Minute
			want := []time.Time{base.Add(-time.Minute), base}
			switch change {
			case "latest":
				updated.OverlapPolicy = ir.OverlapPolicyLatest
				want = []time.Time{base}
			case "latest-gap":
				updated.OverlapPolicy = ir.OverlapPolicyLatest
				updated.CatchupWindow = 48 * time.Hour
				now = base.Add(updated.CatchupWindow)
				want = []time.Time{now}
			case "disabled":
				updated.CatchupWindow = 0
				want = nil
			case "removed":
				updated.Schedule = nil
				want = nil
			case "schedule":
				updated.Schedule = []ir.Schedule{mustParseSchedule(t, "0 * * * *")}
				want = []time.Time{base}
			}
			planner.entryMu.Lock()
			planner.applyEvent(t.Context(), DAGChangeEvent{Type: DAGChangeUpdated,
				DAGEntry: DAGEntry{DAG: &updated, DefinitionID: "updated"}})
			planner.entryMu.Unlock()
			for _, scheduled := range want {
				now = now.Add(time.Minute)
				runs := planner.Plan(t.Context(), now)
				require.Len(t, runs, 1)
				require.Equal(t, ir.TriggerTypeCatchUp, runs[0].TriggerType)
				require.True(t, scheduled.Equal(runs[0].ScheduledTime))
				require.Same(t, &updated, runs[0].DAG)
				planner.DispatchRun(t.Context(), runs[0])
				planner.Advance(now)
			}
			now = now.Add(time.Minute)
			runs := planner.Plan(t.Context(), now)
			if change == "removed" || change == "schedule" {
				require.Empty(t, runs)
			} else {
				require.Len(t, runs, 1)
				require.Equal(t, ir.TriggerTypeScheduler, runs[0].TriggerType)
				require.True(t, now.Equal(runs[0].ScheduledTime))
			}
		})
	}
}

func TestTickPlanner_UpdatedEventSavesWatermarkMutations(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}
	tp, eventCh := newTestTickPlanner(store)

	dag := &ir.DAG{
		Name:          "upd-latest-dag",
		CatchupWindow: 6 * time.Hour,
		Schedule:      []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
		OverlapPolicy: ir.OverlapPolicyLatest,
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))
	initFingerprint := store.lastSaved().DAGs[dag.Name].StartScheduleFingerprint

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tp.Start(ctx)
	eventCh <- DAGChangeEvent{
		Type: DAGChangeUpdated,
		DAGEntry: DAGEntry{
			DAG: &ir.DAG{
				Name:          "upd-latest-dag",
				CatchupWindow: 6 * time.Hour,
				Schedule:      []ir.Schedule{mustParseSchedule(t, "*/30 * * * *")},
				OverlapPolicy: ir.OverlapPolicyLatest,
			},
		},
	}

	// The save comes from the event, well before the periodic flush.
	require.Eventually(t, func() bool {
		return store.lastSaved().DAGs[dag.Name].StartScheduleFingerprint != initFingerprint
	}, 2*time.Second, 5*time.Millisecond, "watermark mutations should be saved on update")
}

func TestTickPlanner_ConcurrentFlushAndAdvance(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{}
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

	state := newMockState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC))
	state.DAGs = map[string]schedulerstate.DAGWatermark{
		"active-dag":  {LastScheduledTime: time.Date(2026, 2, 7, 8, 0, 0, 0, time.UTC)},
		"deleted-dag": {LastScheduledTime: time.Date(2026, 2, 7, 7, 0, 0, 0, time.UTC)},
		"gone-dag":    {LastScheduledTime: time.Date(2026, 2, 7, 6, 0, 0, 0, time.UTC)},
	}
	store := &mockStateStore{state: state}
	tp, _ := newTestTickPlanner(store)

	dags := []*ir.DAG{{Name: "active-dag"}}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dags...)))

	tp.mu.RLock()
	_, hasActive := tp.watermarkState.DAGs["active-dag"]
	_, hasDeleted := tp.watermarkState.DAGs["deleted-dag"]
	_, hasGone := tp.watermarkState.DAGs["gone-dag"]
	tp.mu.RUnlock()

	assert.True(t, hasActive, "active-dag should remain")
	assert.False(t, hasDeleted, "deleted-dag should be pruned")
	assert.False(t, hasGone, "gone-dag should be pruned")
}

func TestTickPlanner_NilStateStoreFullPath(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		Events: eventCh,
	})

	require.NoError(t, tp.Init(context.Background(), testDAGEntries(&ir.DAG{Name: "any-dag"})))

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

	store := &mockStateStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	scheduledTime := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp.lastPlanResult = []PlannedRun{
		{
			DAGEntry:      DAGEntry{DAG: &ir.DAG{Name: "test-dag"}},
			RunID:         "run-1",
			ScheduledTime: scheduledTime,
			TriggerType:   ir.TriggerTypeScheduler,
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

	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)),
	}
	tp, _ := newTestTickPlanner(store)

	dag := newHourlyCatchupDAG(t, "drain-dag")
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

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
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{Status: ir.Running}, nil
		},
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-id", nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
			return false, nil
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Events: eventCh,
	})

	dag := &ir.DAG{
		Name:     "running-dag",
		Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0, "should not plan run when DAG is already running")
}

// A slot skipped because its DAG is busy is logged with the DAG and the slot,
// so the missing run can be traced.
func TestPlanLogsBusySkip(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		running bool
		queued  bool
		want    string
	}{
		{"Running", true, false, "Skipping job because the DAG is running"},
		{"Queued", false, true, "Skipping job because a run of the DAG is queued"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logs := &syncBuffer{buf: new(bytes.Buffer)}
			ctx := logger.WithFixedLogger(t.Context(), logger.NewLogger(
				logger.WithFormat("text"), logger.WithWriter(logs),
			))
			tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(now.Add(-time.Minute))})
			tp.cfg.IsRunning = func(context.Context, *ir.DAG) (bool, error) { return tt.running, nil }
			tp.cfg.IsQueued = func(context.Context, *ir.DAG) (bool, error) { return tt.queued, nil }
			dag := &ir.DAG{Name: "busy-dag", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
			require.NoError(t, tp.Init(ctx, testDAGEntries(dag)))

			require.Empty(t, tp.Plan(ctx, now))
			out := logs.String()
			assert.Contains(t, out, tt.want)
			assert.Contains(t, out, "dag=busy-dag")
			assert.Contains(t, out, "scheduled-time=2026-02-07T12:00:00")
		})
	}
}

func TestTickPlanner_PlanStopSchedule(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{Status: ir.Running}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
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

	dag := &ir.DAG{
		Name:         "stop-dag",
		StopSchedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

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
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{Status: ir.Succeeded}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
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

	dag := &ir.DAG{
		Name:         "stop-dag-not-running",
		StopSchedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0, "stop should be skipped when DAG is not running")
}

func TestTickPlanner_PlanRestartSchedule(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
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

	dag := &ir.DAG{
		Name:            "restart-dag",
		RestartSchedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

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
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return true, nil // Always suspended
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{Status: ir.Running}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
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

	dag := &ir.DAG{
		Name:         "suspended-stop-dag",
		StopSchedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0, "suspended DAG's stop schedule should be skipped")
}

func TestTickPlanner_AdvanceIgnoresStopRestartWatermarks(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{}
	tp, _ := newTestTickPlanner(store)
	require.NoError(t, tp.Init(context.Background(), nil))

	startTime := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	stopTime := time.Date(2026, 2, 7, 12, 30, 0, 0, time.UTC)
	restartTime := time.Date(2026, 2, 7, 13, 0, 0, 0, time.UTC)

	tp.lastPlanResult = []PlannedRun{
		{
			DAGEntry:      DAGEntry{DAG: &ir.DAG{Name: "test-dag"}},
			RunID:         "run-1",
			ScheduledTime: startTime,
			ScheduleType:  ScheduleTypeStart,
			Schedule:      mustParseSchedule(t, "0 * * * *"),
		},
		{
			DAGEntry:      DAGEntry{DAG: &ir.DAG{Name: "test-dag"}},
			ScheduledTime: stopTime,
			ScheduleType:  ScheduleTypeStop,
		},
		{
			DAGEntry:      DAGEntry{DAG: &ir.DAG{Name: "test-dag"}},
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
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{Status: ir.Running}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
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
	dag := &ir.DAG{
		Name:         "tz-stop-dag",
		StopSchedule: []ir.Schedule{mustParseSchedule(t, "0 15 * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	// Tick at 20:00 UTC = 15:00 EST — should match the stop schedule
	now := time.Date(2026, 2, 7, 20, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	require.Len(t, runs, 1, "stop schedule should fire in EST timezone")
	assert.Equal(t, ScheduleTypeStop, runs[0].ScheduleType)
}

func TestTickPlanner_IsRunningErrorAssumesNotRunning(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)),
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:    store,
		QueuesEnabled: true,
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
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
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	// IsRunning error should be logged, assumed not running, and catchup dispatched
	assert.Len(t, runs, 1, "should still dispatch catchup run when IsRunning returns error")
}

func TestTickPlanner_IsQueuedErrorDefersCatchupWithoutDroppingState(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)),
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:    store,
		QueuesEnabled: true,
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
			return false, nil
		},
		IsQueued: func(_ context.Context, _ *ir.DAG) (bool, error) {
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
	dag.OverlapPolicy = ir.OverlapPolicySkip
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

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
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, errors.New("status error")
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
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

	dag := &ir.DAG{
		Name:         "status-err-dag",
		StopSchedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	assert.Len(t, runs, 0, "stop should be skipped when GetLatestStatus returns error")
}

func TestTickPlanner_GenRunIDErrorSkipsStartRun(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
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

	dag := &ir.DAG{
		Name:     "genid-err-dag",
		Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	assert.Len(t, runs, 0, "start run should be skipped when GenRunID returns error")
}

func TestTickPlanner_DispatchRunStopError(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		Stop: func(_ context.Context, _ *ir.DAG) error {
			return errors.New("stop failed")
		},
		Events: eventCh,
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	// Should not panic; error is logged internally
	tp.DispatchRun(context.Background(), PlannedRun{
		DAGEntry:      DAGEntry{DAG: &ir.DAG{Name: "stop-err-dag"}},
		ScheduledTime: time.Now(),
		ScheduleType:  ScheduleTypeStop,
	})
}

func TestTickPlanner_DispatchRunRestartError(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		Restart: func(_ context.Context, _ DAGEntry, _ time.Time) error {
			return errors.New("restart failed")
		},
		Events: eventCh,
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	// Should not panic; error is logged internally
	tp.DispatchRun(context.Background(), PlannedRun{
		DAGEntry:      DAGEntry{DAG: &ir.DAG{Name: "restart-err-dag"}},
		ScheduledTime: time.Now(),
		ScheduleType:  ScheduleTypeRestart,
	})
}

func TestTickPlanner_StopRestartRunsHaveEmptyRunID(t *testing.T) {
	t.Parallel()

	// Use two DAGs: one for start (needs status != Running), one for stop+restart (needs Running)
	startDAG := &ir.DAG{
		Name:     "start-dag",
		Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	stopRestartDAG := &ir.DAG{
		Name:            "stop-restart-dag",
		StopSchedule:    []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
		RestartSchedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, dag *ir.DAG) (ir.DAGRunStatus, error) {
			if dag.Name == "stop-restart-dag" {
				return ir.DAGRunStatus{Status: ir.Running}, nil
			}
			return ir.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
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

	require.NoError(t, tp.Init(context.Background(), testDAGEntries(startDAG, stopRestartDAG)))

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

	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:    store,
		QueuesEnabled: true,
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{Status: ir.Running}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
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

	dag := &ir.DAG{
		Name:            "catchup-blocks-dag",
		CatchupWindow:   6 * time.Hour,
		Schedule:        []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
		StopSchedule:    []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
		RestartSchedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	// Should only produce the catchup run, not stop/restart
	require.Len(t, runs, 1, "catchup should block stop/restart schedules")
	assert.Equal(t, ir.TriggerTypeCatchUp, runs[0].TriggerType)
}

func TestTickPlanner_ConcurrentPlanAndEvents(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{}
	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore: store,
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
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
	dags := make([]*ir.DAG, 50)
	for i := range 50 {
		dags[i] = &ir.DAG{
			Name:     fmt.Sprintf("dag-%d", i),
			Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
		}
	}

	// Push events concurrently
	wg.Go(func() {
		for i := range 50 {
			eventCh <- DAGChangeEvent{
				Type:     DAGChangeAdded,
				DAGEntry: DAGEntry{DAG: dags[i]},
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
		IsSuspended: func(_ context.Context, _ string) (bool, error) { return false, nil },
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{
				Status:    ir.Succeeded,
				StartedAt: "2026-02-07T11:30:00Z",
			}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    eventCh,
	})

	dag := &ir.DAG{
		Name:             "skip-success-dag",
		SkipIfSuccessful: true,
		Schedule:         []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0, "should skip when SkipIfSuccessful and last run succeeded in interval")
}

func TestTickPlanner_ShouldRunSkipIfSuccessfulIgnoresStaleEditedScheduleSlot(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) (bool, error) { return false, nil },
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{
				Status:       ir.Succeeded,
				StartedAt:    "2026-02-07T12:34:00Z",
				ScheduleTime: "2026-02-07T12:34:00Z",
				TriggerType:  ir.TriggerTypeScheduler,
			}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 43, 0, 0, time.UTC) },
		Events:    eventCh,
	})

	dag := &ir.DAG{
		Name:             "skip-success-edited-schedule-dag",
		SkipIfSuccessful: true,
		Schedule:         []ir.Schedule{mustParseSchedule(t, "43 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 43, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 1, "should not skip when the last success belongs to a removed schedule slot")
}

func TestTickPlanner_ShouldRunSkipIfSuccessfulFallsBackToManualRunStartTime(t *testing.T) {
	t.Parallel()

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) (bool, error) { return false, nil },
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{
				Status:      ir.Succeeded,
				StartedAt:   "2026-02-07T12:34:00Z",
				TriggerType: ir.TriggerTypeManual,
			}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 43, 0, 0, time.UTC) },
		Events:    eventCh,
	})

	dag := &ir.DAG{
		Name:             "skip-success-manual-fallback-dag",
		SkipIfSuccessful: true,
		Schedule:         []ir.Schedule{mustParseSchedule(t, "43 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 43, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0, "should still skip when a manual run already succeeded in the current interval")
}

func TestLatestScheduledSlotMarksRemovedScheduleSlotStale(t *testing.T) {
	t.Parallel()

	scheduledAt, state := latestScheduledSlot(ir.DAGRunStatus{
		ScheduleTime: "2026-02-07T12:34:00Z",
	}, mustParseSchedule(t, "43 * * * *"))

	assert.Equal(t, latestScheduledSlotStale, state)
	assert.Equal(t, time.Date(2026, 2, 7, 12, 34, 0, 0, time.UTC), scheduledAt)
}

func TestTickPlanner_ProfileScopedStartSchedules(t *testing.T) {
	t.Parallel()

	resolver := &testProfileResolver{profile: "prod"}
	tp := NewTickPlanner(TickPlannerConfig{
		ProfileResolver: resolver,
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    make(chan DAGChangeEvent, 1),
	})
	dag := &ir.DAG{
		Name:     "profile-scoped-start-dag",
		Location: "/tmp/profile-scoped-start-dag.yaml",
		Labels:   ir.NewLabels([]string{"workspace=ops"}),
		Schedule: []ir.Schedule{
			mustParseProfileSchedule(t, "0 * * * *", "prod"),
			mustParseProfileSchedule(t, "0 * * * *", "dev"),
		},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	runs := tp.Plan(context.Background(), time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC))
	require.Len(t, runs, 1)
	assert.Equal(t, "prod", runs[0].Schedule.Profile)
	assert.Equal(t, "profile-scoped-start-dag", resolver.dagName)
	assert.Equal(t, "ops", resolver.workspaceName)
}

func TestTickPlanner_ProfileScopedStartSchedulesWithoutDefaultProfile(t *testing.T) {
	t.Parallel()

	tp := NewTickPlanner(TickPlannerConfig{
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    make(chan DAGChangeEvent, 1),
	})
	dag := &ir.DAG{
		Name: "profile-scoped-empty-default-dag",
		Schedule: []ir.Schedule{
			mustParseProfileSchedule(t, "0 * * * *", "prod"),
			mustParseSchedule(t, "0 * * * *"),
		},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	runs := tp.Plan(context.Background(), time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC))
	require.Len(t, runs, 1)
	assert.Empty(t, runs[0].Schedule.Profile)
}

func TestTickPlanner_ProfileScopedSchedulesResolveErrorFailsClosed(t *testing.T) {
	t.Parallel()

	tp := NewTickPlanner(TickPlannerConfig{
		ProfileResolver: &testProfileResolver{err: errors.New("profile store unavailable")},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    make(chan DAGChangeEvent, 1),
	})
	dag := &ir.DAG{
		Name:     "profile-resolve-error-dag",
		Location: "/tmp/profile-resolve-error-dag.yaml",
		Schedule: []ir.Schedule{mustParseProfileSchedule(t, "0 * * * *", "prod")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	runs := tp.Plan(context.Background(), time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC))
	assert.Empty(t, runs)
}

func TestTickPlanner_ProfileScopedStopRestartSchedules(t *testing.T) {
	t.Parallel()

	tp := NewTickPlanner(TickPlannerConfig{
		ProfileResolver: &testProfileResolver{profile: "dev"},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{Status: ir.Running}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    make(chan DAGChangeEvent, 1),
	})
	dag := &ir.DAG{
		Name:     "profile-scoped-control-dag",
		Location: "/tmp/profile-scoped-control-dag.yaml",
		StopSchedule: []ir.Schedule{
			mustParseProfileSchedule(t, "0 * * * *", "prod"),
			mustParseProfileSchedule(t, "0 * * * *", "dev"),
		},
		RestartSchedule: []ir.Schedule{
			mustParseProfileSchedule(t, "0 * * * *", "prod"),
			mustParseProfileSchedule(t, "0 * * * *", "dev"),
		},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

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

	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:      store,
		QueuesEnabled:   true,
		ProfileResolver: &testProfileResolver{profile: "dev"},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    make(chan DAGChangeEvent, 1),
	})
	dag := &ir.DAG{
		Name:          "profile-scoped-catchup-dag",
		Location:      "/tmp/profile-scoped-catchup-dag.yaml",
		CatchupWindow: 6 * time.Hour,
		Schedule: []ir.Schedule{
			mustParseProfileSchedule(t, "0 * * * *", "prod"),
			mustParseProfileSchedule(t, "30 * * * *", "dev"),
		},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

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
	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}
	resolver := &testProfileResolver{profile: "dev"}
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:      store,
		QueuesEnabled:   true,
		ProfileResolver: resolver,
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return now },
		Location:  time.UTC,
		Events:    make(chan DAGChangeEvent, 1),
	})
	dag := &ir.DAG{
		Name:          "profile-change-catchup-dag",
		Location:      "/tmp/profile-change-catchup-dag.yaml",
		CatchupWindow: 6 * time.Hour,
		Schedule: []ir.Schedule{
			mustParseProfileSchedule(t, "30 * * * *", "dev"),
		},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

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
		IsSuspended: func(_ context.Context, _ string) (bool, error) { return false, nil },
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{
				Status:    ir.Succeeded,
				StartedAt: "2026-02-07T12:00:00Z",
			}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    eventCh,
	})

	dag := &ir.DAG{
		Name:     "already-finished-dag",
		Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)
	assert.Len(t, runs, 0, "should skip when last run started at/after scheduled time")
}

func TestTickPlanner_ShouldRunFailedPreviousRunNotSkipped(t *testing.T) {
	t.Parallel()

	// SkipIfSuccessful=true but last run failed — should NOT skip
	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) (bool, error) { return false, nil },
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{
				Status:    ir.Failed,
				StartedAt: "2026-02-07T11:30:00Z",
			}, nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) { return false, nil },
		GenRunID:  func(_ context.Context) (string, error) { return "run-1", nil },
		Clock:     func() time.Time { return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC) },
		Events:    eventCh,
	})

	dag := &ir.DAG{
		Name:             "failed-run-dag",
		SkipIfSuccessful: true,
		Schedule:         []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

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
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, scheduleTime time.Time) error {
			dispatched = true
			gotScheduleTime = scheduleTime
			return nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	scheduledTime := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp.DispatchRun(context.Background(), PlannedRun{
		DAGEntry:      DAGEntry{DAG: &ir.DAG{Name: "start-dag"}},
		RunID:         "run-1",
		ScheduledTime: scheduledTime,
		ScheduleType:  ScheduleTypeStart,
		TriggerType:   ir.TriggerTypeScheduler,
	})
	assert.True(t, dispatched, "Dispatch callback should be invoked for ScheduleTypeStart")
	assert.Equal(t, scheduledTime, gotScheduleTime, "Dispatch callback should receive the scheduled time")
}

// A scheduled run of a DAG that names a configured queue waits for capacity
// instead of starting as its schedule fires.
func TestTickPlanner_DispatchRunStartQueued(t *testing.T) {
	t.Parallel()

	var dispatched, enqueued bool
	tp := NewTickPlanner(TickPlannerConfig{
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			dispatched = true
			return nil
		},
		Enqueue: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			enqueued = true
			return nil
		},
		QueuesEnabled:  true,
		HasGlobalQueue: func(dag *ir.DAG) bool { return dag.Queue == "paced" },
		Events:         make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	tp.DispatchRun(context.Background(), PlannedRun{
		DAGEntry:      DAGEntry{DAG: &ir.DAG{Name: "start-dag", Queue: "paced"}},
		RunID:         "run-1",
		ScheduledTime: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		ScheduleType:  ScheduleTypeStart,
		TriggerType:   ir.TriggerTypeScheduler,
	})
	assert.True(t, enqueued, "a run the queue paces should be enqueued")
	assert.False(t, dispatched, "a run the queue paces should not start directly")

	enqueued = false
	tp.DispatchRun(context.Background(), PlannedRun{
		DAGEntry:      DAGEntry{DAG: &ir.DAG{Name: "own-queue-dag"}},
		RunID:         "run-2",
		ScheduledTime: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		ScheduleType:  ScheduleTypeStart,
		TriggerType:   ir.TriggerTypeScheduler,
	})
	assert.True(t, dispatched, "a run without a configured queue should start directly")
	assert.False(t, enqueued, "a run without a configured queue should not be enqueued")
}

func TestTickPlanner_DispatchRunSuspendedStartSkipped(t *testing.T) {
	t.Parallel()

	dispatched := false
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, _ string) (bool, error) { return true, nil },
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			dispatched = true
			return nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	tp.DispatchRun(context.Background(), PlannedRun{
		DAGEntry:      DAGEntry{DAG: &ir.DAG{Name: "start-dag"}},
		RunID:         "run-1",
		ScheduledTime: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		ScheduleType:  ScheduleTypeStart,
		TriggerType:   ir.TriggerTypeScheduler,
	})

	assert.False(t, dispatched, "suspended scheduler-managed run should not dispatch")
}

func TestTickPlanner_DispatchRunSuspensionReadErrorSkipped(t *testing.T) {
	t.Parallel()

	checkedName := ""
	dispatched := false
	tp := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(_ context.Context, name string) (bool, error) {
			checkedName = name
			return false, errors.New("read suspend flag")
		},
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			dispatched = true
			return nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	tp.DispatchRun(context.Background(), PlannedRun{
		DAGEntry:      DAGEntry{DAG: &ir.DAG{Name: "start-dag"}},
		RunID:         "run-1",
		ScheduledTime: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		ScheduleType:  ScheduleTypeStart,
		TriggerType:   ir.TriggerTypeScheduler,
	})

	assert.Equal(t, "start-dag", checkedName)
	assert.False(t, dispatched, "scheduler-managed run should not dispatch when suspension state is unavailable")
}

func TestTickPlanner_DispatchRunCatchupSuspensionReadErrorRequeues(t *testing.T) {
	t.Parallel()

	dag := newHourlyCatchupDAG(t, "catchup-dag")
	scheduledTime := time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)
	tp := NewTickPlanner(TickPlannerConfig{
		QueuesEnabled: true,
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, errors.New("read suspend flag")
		},
		Enqueue: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			t.Fatal("catch-up run must not enqueue when suspension state is unavailable")
			return nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	tp.DispatchRun(context.Background(), PlannedRun{
		DAGEntry:      DAGEntry{DAG: dag},
		RunID:         "run-1",
		ScheduledTime: scheduledTime,
		ScheduleType:  ScheduleTypeStart,
		TriggerType:   ir.TriggerTypeCatchUp,
	})

	buf, ok := tp.buffers[dag.Name]
	require.True(t, ok)
	require.Equal(t, 1, buf.Len())
	item, ok := buf.Peek()
	require.True(t, ok)
	assert.Equal(t, scheduledTime, item.ScheduledTime)
}

func TestTickPlanner_DispatchRunSuspendedCatchupAdvancesWatermark(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{}
	enqueued := false
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:    store,
		QueuesEnabled: true,
		IsSuspended:   func(_ context.Context, _ string) (bool, error) { return true, nil },
		Enqueue: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			enqueued = true
			return nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	scheduledTime := time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)
	dag := newHourlyCatchupDAG(t, "suspended-catchup-dag")
	tp.DispatchRun(context.Background(), PlannedRun{
		DAGEntry:      DAGEntry{DAG: dag},
		RunID:         "run-1",
		ScheduledTime: scheduledTime,
		ScheduleType:  ScheduleTypeStart,
		TriggerType:   ir.TriggerTypeCatchUp,
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

	store := &mockStateStore{}
	scheduledTime := time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)
	dag := newHourlyCatchupDAG(t, "legacy.catchup-dag")
	legacyRunID := generateLegacyCatchupRunID(dag.Name, scheduledTime)
	var checkedRunIDs []string

	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:    store,
		QueuesEnabled: true,
		Enqueue: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			t.Fatal("enqueue should not be called when the legacy run already exists")
			return nil
		},
		RunExists: func(_ context.Context, _ *ir.DAG, runID string) (bool, error) {
			checkedRunIDs = append(checkedRunIDs, runID)
			return runID == legacyRunID, nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	run, ok := tp.createPlannedRun(context.Background(), DAGEntry{DAG: dag}, ir.Schedule{}, scheduledTime, ir.TriggerTypeCatchUp)
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
		Restart: func(_ context.Context, _ DAGEntry, scheduleTime time.Time) error {
			gotScheduleTime = scheduleTime
			return nil
		},
		Events: make(chan DAGChangeEvent, 1),
	})
	require.NoError(t, tp.Init(context.Background(), nil))

	scheduledTime := time.Date(2026, 2, 7, 13, 0, 0, 0, time.UTC)
	tp.DispatchRun(context.Background(), PlannedRun{
		DAGEntry:      DAGEntry{DAG: &ir.DAG{Name: "restart-dag"}},
		ScheduledTime: scheduledTime,
		ScheduleType:  ScheduleTypeRestart,
	})

	assert.Equal(t, scheduledTime, gotScheduleTime)
}

func TestTickPlanner_StartStop(t *testing.T) {
	t.Parallel()

	tp, _ := newTestTickPlanner(&mockStateStore{})
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
	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 6, 0, 0, 0, time.UTC)),
	}
	tp, _ := newTestTickPlanner(store)

	dag := &ir.DAG{
		Name:          "latest-init-dag",
		CatchupWindow: 12 * time.Hour,
		Schedule:      []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
		OverlapPolicy: ir.OverlapPolicyLatest,
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

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

	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:    store,
		QueuesEnabled: true,
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-latest", nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
			return false, nil // DAG is not running
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Location: time.UTC,
		Events:   eventCh,
	})

	dag := &ir.DAG{
		Name:          "latest-nr-dag",
		CatchupWindow: 6 * time.Hour,
		Schedule:      []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
		OverlapPolicy: ir.OverlapPolicyLatest,
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	runs := tp.Plan(context.Background(), now)

	// Should dispatch exactly 1 run — the latest (12:00), not the oldest (10:00)
	require.Len(t, runs, 1)
	assert.Equal(t, time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC), runs[0].ScheduledTime,
		"should dispatch the latest interval, not the oldest")
	assert.Equal(t, ir.TriggerTypeCatchUp, runs[0].TriggerType)

	// Buffer should be empty after dispatch
	_, bufExists := tp.buffers["latest-nr-dag"]
	assert.False(t, bufExists, "buffer should be cleaned up after draining")
}

func TestTickPlanner_PlanLatestRunning(t *testing.T) {
	t.Parallel()

	store := &mockStateStore{
		state: newMockState(time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)),
	}

	eventCh := make(chan DAGChangeEvent, 256)
	tp := NewTickPlanner(TickPlannerConfig{
		StateStore:    store,
		QueuesEnabled: true,
		IsSuspended: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		GetLatestStatus: func(_ context.Context, _ *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		},
		Dispatch: func(_ context.Context, _ DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
			return nil
		},
		GenRunID: func(_ context.Context) (string, error) {
			return "run-latest", nil
		},
		IsRunning: func(_ context.Context, _ *ir.DAG) (bool, error) {
			return true, nil // DAG is running
		},
		Clock: func() time.Time {
			return time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		},
		Location: time.UTC,
		Events:   eventCh,
	})

	dag := &ir.DAG{
		Name:          "latest-run-dag",
		CatchupWindow: 6 * time.Hour,
		Schedule:      []ir.Schedule{mustParseSchedule(t, "0 * * * *")},
		OverlapPolicy: ir.OverlapPolicyLatest,
	}
	require.NoError(t, tp.Init(context.Background(), testDAGEntries(dag)))

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

func TestInheritedScheduling(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		for _, workspaceBase := range []bool{false, true} {
			for _, policy := range []ir.OverlapPolicy{ir.OverlapPolicySkip, ir.OverlapPolicyAll, ir.OverlapPolicyLatest} {
				t.Run(fmt.Sprintf("Recursive=%t/Workspace=%t/%s", recursive, workspaceBase, policy), func(t *testing.T) {
					root := t.TempDir()
					dir := filepath.Join(root, "dags")
					fileDir := dir
					if recursive {
						fileDir = filepath.Join(dir, "nested")
					}
					require.NoError(t, os.MkdirAll(fileDir, 0750))
					path := filepath.Join(fileDir, "scheduled.yaml")
					require.NoError(t, os.WriteFile(path, []byte("labels: [workspace=ops]\nsteps:\n  - run: echo tick\n"), 0600))
					basePath := filepath.Join(root, "base.yaml")
					base := fmt.Sprintf("schedule:\n  start: '* * * * *'\n  stop: '0 0 * * *'\n  restart: '0 1 * * *'\ncatchup_window: 1h\noverlap_policy: %s\nqueue: pool\n", policy)
					require.NoError(t, os.WriteFile(basePath, []byte(base), 0600))
					opts := []filedag.Option{filedag.WithSkipExamples(true), filedag.WithBaseConfig(basePath), filedag.WithRecursiveDiscovery(recursive)}
					loadOpts := []spec.LoadOption{spec.WithBaseConfig(basePath), spec.WithoutEval()}
					if workspaceBase {
						workspaceDir := filepath.Join(root, "workspaces")
						require.NoError(t, os.MkdirAll(filepath.Join(workspaceDir, "ops"), 0750))
						require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "ops", "base.yaml"), []byte(base), 0600))
						require.NoError(t, os.WriteFile(basePath, []byte("queue: global\n"), 0600))
						opts = append(opts, filedag.WithWorkspaceBaseConfigDir(workspaceDir))
						loadOpts = append(loadOpts, spec.WithWorkspaceBaseConfigDir(workspaceDir))
					}
					repo := testutil.NewFileDAGRepository(dir, opts...)
					reader := filedag.NewFileEntryReader(dir, repo, recursive, basePath, "")
					require.NoError(t, reader.Init(t.Context()))
					t.Cleanup(reader.Stop)
					entries := reader.Entries()
					require.Len(t, entries, 1)
					full, err := spec.Load(t.Context(), path, loadOpts...)
					require.NoError(t, err)
					metadata := entries[0].DAG
					require.Equal(t, full.ProcGroup(), metadata.ProcGroup())
					require.Equal(t, "pool", metadata.ProcGroup())
					require.Equal(t, full.Schedule, metadata.Schedule)
					require.Equal(t, full.StopSchedule, metadata.StopSchedule)
					require.Equal(t, full.RestartSchedule, metadata.RestartSchedule)

					for _, guard := range []string{"running", "queued"} {
						t.Run(guard, func(t *testing.T) {
							now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
							busy := true
							enqueued := make(map[string]int)
							newPlanner := func(queues bool) *TickPlanner {
								planner, _ := newTestTickPlanner(&mockStateStore{state: newMockState(now.Add(-3 * time.Minute))})
								planner.cfg.QueuesEnabled = queues
								planner.cfg.IsRunning = func(_ context.Context, dag *ir.DAG) (bool, error) {
									require.Equal(t, full.ProcGroup(), dag.ProcGroup())
									return busy && guard == "running", nil
								}
								planner.cfg.IsQueued = func(_ context.Context, dag *ir.DAG) (bool, error) {
									require.Equal(t, full.ProcGroup(), dag.ProcGroup())
									return busy && guard == "queued", nil
								}
								planner.cfg.Enqueue = func(_ context.Context, entry DAGEntry, runID string, _ ir.TriggerType, _ time.Time) error {
									require.Equal(t, full.ProcGroup(), entry.DAG.ProcGroup())
									enqueued[runID]++
									return nil
								}

								require.NoError(t, planner.Init(t.Context(), entries))
								return planner
							}
							planner := newPlanner(true)
							require.Empty(t, planner.Plan(t.Context(), now))
							busy = false
							runs := planner.Plan(t.Context(), now.Add(time.Minute))
							require.Len(t, runs, 1)
							require.Equal(t, ir.TriggerTypeCatchUp, runs[0].TriggerType)
							var want time.Time
							switch policy {
							case ir.OverlapPolicyAll:
								want = now.Add(-2 * time.Minute)
							case ir.OverlapPolicySkip:
								want = now.Add(-time.Minute)
							case ir.OverlapPolicyLatest:
								want = now
							}
							require.True(t, want.Equal(runs[0].ScheduledTime))
							planner.DispatchRun(t.Context(), runs[0])
							require.Equal(t, 1, enqueued[runs[0].RunID])
							disabled := newPlanner(false)
							runs = disabled.Plan(t.Context(), now)
							require.Len(t, runs, 1)
							require.Equal(t, ir.TriggerTypeScheduler, runs[0].TriggerType)
						})
					}
				})
			}
		}
	}
}

func TestBaseRefreshUpdatesPlanner(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		t.Run(fmt.Sprint(recursive), func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "dags")
			require.NoError(t, os.MkdirAll(dir, 0750))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "scheduled.yaml"), []byte("name: scheduled\nsteps:\n  - run: echo tick\n"), 0600))
			base := filepath.Join(root, "base.yaml")
			require.NoError(t, os.WriteFile(base, []byte("queue: old\nschedule: '0 * * * *'\n"), 0600))
			repo := testutil.NewFileDAGRepository(dir, filedag.WithBaseConfig(base), filedag.WithRecursiveDiscovery(recursive))
			reader := filedag.NewFileEntryReader(dir, repo, recursive, base, "")
			require.NoError(t, reader.Init(t.Context()))
			t.Cleanup(reader.Stop)
			planner, _ := newTestTickPlanner(nil)
			require.NoError(t, planner.Init(t.Context(), reader.Entries()))
			require.NoError(t, os.WriteFile(base, []byte("queue: updated\nschedule: '*/5 * * * *'\n"), 0600))
			go reader.Start(t.Context())
			select {
			case event := <-reader.Events():
				require.Equal(t, DAGChangeUpdated, event.Type)
				planner.applyEvent(t.Context(), event)
			case <-time.After(3 * time.Second):
				t.Fatal("base update did not notify the planner")
			}
			dag := planner.entries["scheduled"].DAG
			require.Equal(t, "updated", dag.ProcGroup())
			require.Equal(t, "*/5 * * * *", dag.Schedule[0].Expression)
		})
	}
}

// lastSlot is the DAG's latest start slot as the planner keeps it.
func lastSlot(tp *TickPlanner, dagName string) *schedulerstate.ScheduleSlot {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	return tp.watermarkState.DAGs[dagName].LastSlot
}

// Each due cron slot keeps what came of it: why a guard held it back, or
// that it was planned to run.
func TestTickPlanner_SlotOutcomes(t *testing.T) {
	t.Parallel()

	slot := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	errCheck := errors.New("check failed")
	for _, tc := range []struct {
		name string
		set  func(*TickPlanner, *ir.DAG)
		want schedulerstate.SlotOutcome
	}{
		{"planned", func(*TickPlanner, *ir.DAG) {}, schedulerstate.SlotPlanned},
		{"running", func(tp *TickPlanner, _ *ir.DAG) {
			tp.cfg.IsRunning = func(context.Context, *ir.DAG) (bool, error) { return true, nil }
		}, schedulerstate.SlotRunning},
		{"queued", func(tp *TickPlanner, _ *ir.DAG) {
			tp.cfg.IsQueued = func(context.Context, *ir.DAG) (bool, error) { return true, nil }
		}, schedulerstate.SlotQueued},
		{"check error", func(tp *TickPlanner, _ *ir.DAG) {
			tp.cfg.IsRunning = func(context.Context, *ir.DAG) (bool, error) { return false, errCheck }
		}, schedulerstate.SlotFailed},
		{"run ID error", func(tp *TickPlanner, _ *ir.DAG) {
			tp.cfg.GenRunID = func(context.Context) (string, error) { return "", errCheck }
		}, schedulerstate.SlotFailed},
		{"done", func(tp *TickPlanner, _ *ir.DAG) {
			tp.cfg.GetLatestStatus = func(context.Context, *ir.DAG) (ir.DAGRunStatus, error) {
				return ir.DAGRunStatus{Status: ir.Succeeded, ScheduleTime: "2026-02-07T12:00:00Z"}, nil
			}
		}, schedulerstate.SlotDone},
		{"succeeded", func(tp *TickPlanner, dag *ir.DAG) {
			dag.SkipIfSuccessful = true
			tp.cfg.GetLatestStatus = func(context.Context, *ir.DAG) (ir.DAGRunStatus, error) {
				return ir.DAGRunStatus{Status: ir.Succeeded, StartedAt: "2026-02-07T11:30:00Z"}, nil
			}
		}, schedulerstate.SlotSucceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tp, _ := newTestTickPlanner(&mockStateStore{})
			dag := &ir.DAG{Name: "hourly", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
			tc.set(tp, dag)
			require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

			tp.Plan(t.Context(), slot)

			assert.Equal(t, &schedulerstate.ScheduleSlot{At: slot, Outcome: tc.want}, lastSlot(tp, "hourly"))
		})
	}
}

// A planned slot settles as dispatched or failed by its dispatch, even when
// the dispatch finishes before the tick advances; planning it again changes
// nothing.
func TestTickPlanner_DispatchSettlesSlot(t *testing.T) {
	t.Parallel()

	slot := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		err  error
		want schedulerstate.SlotOutcome
	}{
		{"dispatched", nil, schedulerstate.SlotDispatched},
		{"failed", errors.New("spawn failed"), schedulerstate.SlotFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tp, _ := newTestTickPlanner(&mockStateStore{})
			tp.cfg.Dispatch = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error { return tc.err }
			dag := &ir.DAG{Name: "hourly", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
			require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

			runs := tp.Plan(t.Context(), slot)
			require.Len(t, runs, 1)
			tp.DispatchRun(t.Context(), runs[0])
			tp.Advance(slot)
			assert.False(t, tp.recordSlot("hourly", slot, schedulerstate.SlotPlanned), "a settled slot is not planned again")

			assert.Equal(t, &schedulerstate.ScheduleSlot{At: slot, Outcome: tc.want}, lastSlot(tp, "hourly"))
		})
	}
}

// The latest slot never moves back to an earlier one.
func TestTickPlanner_SlotNeverMovesBack(t *testing.T) {
	t.Parallel()

	tp, _ := newTestTickPlanner(&mockStateStore{})
	require.NoError(t, tp.Init(t.Context(), nil))
	later := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)

	require.True(t, tp.recordSlot("dag", later, schedulerstate.SlotMissed))
	assert.False(t, tp.recordSlot("dag", later.Add(-time.Hour), schedulerstate.SlotDispatched))
	assert.Equal(t, &schedulerstate.ScheduleSlot{At: later, Outcome: schedulerstate.SlotMissed}, lastSlot(tp, "dag"))
}

// A slot that was dispatched stays so when it is looked at again, as by a
// scheduler restarted within the slot's minute.
func TestTickPlanner_DispatchedSlotStays(t *testing.T) {
	t.Parallel()

	slot := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp, _ := newTestTickPlanner(&mockStateStore{})
	require.NoError(t, tp.Init(t.Context(), nil))

	require.True(t, tp.recordSlot("dag", slot, schedulerstate.SlotDispatched))
	assert.False(t, tp.recordSlot("dag", slot, schedulerstate.SlotRunning))
	assert.Equal(t, schedulerstate.SlotDispatched, lastSlot(tp, "dag").Outcome)
}

// A catch-up slot records its dispatch, and one its overlap policy drops
// records the overlap.
func TestTickPlanner_CatchupSlots(t *testing.T) {
	t.Parallel()

	tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(time.Date(2026, 2, 7, 10, 0, 0, 0, time.UTC))})
	tp.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error { return nil }
	dag := newHourlyCatchupDAG(t, "catchup")
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)

	runs := tp.Plan(t.Context(), now)
	require.Len(t, runs, 1)
	tp.DispatchRun(t.Context(), runs[0])
	assert.Equal(t, &schedulerstate.ScheduleSlot{At: runs[0].ScheduledTime, Outcome: schedulerstate.SlotDispatched},
		lastSlot(tp, "catchup"))

	dag.OverlapPolicy = ir.OverlapPolicySkip
	tp.buffers["catchup"].overlapPolicy = ir.OverlapPolicySkip
	tp.cfg.IsRunning = func(context.Context, *ir.DAG) (bool, error) { return true, nil }
	tp.Plan(t.Context(), now.Add(time.Minute))
	assert.Equal(t, schedulerstate.SlotOverlap, lastSlot(tp, "catchup").Outcome)
}

// A scheduler that starts after its last tick records, for each DAG it does
// not catch up, the latest slot that came due meanwhile as missed; the
// current minute's slot is left to the first tick. A DAG caught up or
// suspended records none.
func TestTickPlanner_InitRecordsMissedSlot(t *testing.T) {
	t.Parallel()

	tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(time.Date(2026, 2, 7, 9, 30, 0, 0, time.UTC))})
	tp.cfg.IsSuspended = func(_ context.Context, name string) (bool, error) { return name == "suspended", nil }
	hourly := func(name string) *ir.DAG {
		return &ir.DAG{Name: name, Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
	}
	suspended := hourly("suspended")
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(hourly("hourly"), newHourlyCatchupDAG(t, "catchup"), suspended)))

	assert.Equal(t, &schedulerstate.ScheduleSlot{At: time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC), Outcome: schedulerstate.SlotMissed},
		lastSlot(tp, "hourly"))
	assert.Nil(t, lastSlot(tp, "catchup"))
	assert.Nil(t, lastSlot(tp, "suspended"))
}

// A slot left planned when the scheduler stopped is dispatched once the
// DAG's latest run is of that slot; otherwise it stays planned.
func TestTickPlanner_InitSettlesPlannedSlot(t *testing.T) {
	t.Parallel()

	slot := time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name         string
		scheduleTime string
		want         schedulerstate.SlotOutcome
	}{
		{"ran", "2026-02-07T11:00:00Z", schedulerstate.SlotDispatched},
		{"never ran", "2026-02-07T10:00:00Z", schedulerstate.SlotPlanned},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := newMockState(time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC))
			state.DAGs["hourly"] = schedulerstate.DAGWatermark{
				LastSlot: &schedulerstate.ScheduleSlot{At: slot, Outcome: schedulerstate.SlotPlanned},
			}
			tp, _ := newTestTickPlanner(&mockStateStore{state: state})
			tp.cfg.GetLatestStatus = func(context.Context, *ir.DAG) (ir.DAGRunStatus, error) {
				return ir.DAGRunStatus{Status: ir.Succeeded, ScheduleTime: tc.scheduleTime}, nil
			}
			dag := &ir.DAG{Name: "hourly", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}

			require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

			assert.Equal(t, &schedulerstate.ScheduleSlot{At: slot, Outcome: tc.want}, lastSlot(tp, "hourly"))
		})
	}
}

// A tick that wakes late records the latest slot it slept through as
// missed, even without queues; the slot of the tick it wakes at is its own.
func TestTickPlanner_ResumeRecordsMissedSlot(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tp, _ := newTestTickPlanner(&mockStateStore{})
	tp.cfg.QueuesEnabled = false
	dag := &ir.DAG{Name: "quarterly", Schedule: []ir.Schedule{mustParseSchedule(t, "*/15 * * * *")}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	tp.Advance(start)

	tp.resume(t.Context(), start.Add(30*time.Minute))

	assert.Equal(t, &schedulerstate.ScheduleSlot{At: start.Add(15 * time.Minute), Outcome: schedulerstate.SlotMissed},
		lastSlot(tp, "quarterly"))
}
