// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

// DAGChangeType identifies the kind of DAG lifecycle event.
type DAGChangeType = persis.DAGChangeType

const (
	DAGChangeAdded   = persis.DAGChangeAdded
	DAGChangeUpdated = persis.DAGChangeUpdated
	DAGChangeDeleted = persis.DAGChangeDeleted
)

// DAGChangeEvent represents a DAG lifecycle event.
type DAGChangeEvent = persis.DAGChangeEvent

// DAGEntry pairs a DAG definition with its stable persistence identity.
type DAGEntry = persis.DAGEntry

const deletedWatermarkGrace = 2 * time.Minute

// PlannedRun represents a run that the TickPlanner has decided should be dispatched.
type PlannedRun struct {
	DAGEntry
	RunID         string
	ScheduledTime time.Time
	TriggerType   ir.TriggerType
	ScheduleType  ScheduleType
	Schedule      ir.Schedule
	Fingerprint   string
}

// DispatchFunc dispatches a catch-up or scheduled run for the given DAG.
type DispatchFunc func(ctx context.Context, entry DAGEntry, runID string, triggerType ir.TriggerType, scheduleTime time.Time) error

// RunIDFunc generates a unique run ID.
type RunIDFunc func(ctx context.Context) (string, error)

// IsRunningFunc checks if a DAG has any active run.
type IsRunningFunc func(ctx context.Context, dag *ir.DAG) (bool, error)

// GetLatestStatusFunc retrieves the latest status of a DAG.
type GetLatestStatusFunc func(ctx context.Context, dag *ir.DAG) (ir.DAGRunStatus, error)

// IsSuspendedFunc checks whether a DAG is currently suspended.
type IsSuspendedFunc func(ctx context.Context, dagName string) (bool, error)

// StopFunc stops a running DAG.
type StopFunc func(ctx context.Context, dag *ir.DAG) error

// RestartFunc restarts a DAG unconditionally.
type RestartFunc func(ctx context.Context, entry DAGEntry, scheduleTime time.Time) error

// EnqueueFunc enqueues a catchup run for the given DAG.
type EnqueueFunc func(ctx context.Context, entry DAGEntry, runID string, triggerType ir.TriggerType, scheduleTime time.Time) error

// IsQueuedFunc checks if a DAG has any pending queued items.
type IsQueuedFunc func(ctx context.Context, dag *ir.DAG) (bool, error)

// HasGlobalQueueFunc reports whether a DAG names a configured queue.
type HasGlobalQueueFunc func(dag *ir.DAG) bool

// RunExistsFunc checks whether a durable dag-run record already exists.
type RunExistsFunc func(ctx context.Context, dag *ir.DAG, runID string) (bool, error)

// TickPlannerConfig holds the dependencies for creating a TickPlanner.
type TickPlannerConfig struct {
	StateStore      schedulerstate.Store
	IsSuspended     IsSuspendedFunc
	GetLatestStatus GetLatestStatusFunc
	IsRunning       IsRunningFunc
	GenRunID        RunIDFunc
	Dispatch        DispatchFunc
	Stop            StopFunc
	Restart         RestartFunc
	Clock           Clock
	Location        *time.Location // timezone for cron schedule evaluation
	Events          <-chan DAGChangeEvent
	ProfileResolver DAGProfileResolver

	// QueuesEnabled indicates whether the queue subsystem is active.
	// When false, catchup buffers are not populated.
	QueuesEnabled bool
	// Enqueue enqueues a catchup run. Nil when queues are disabled.
	Enqueue EnqueueFunc
	// IsQueued checks if a DAG has any pending queued items.
	IsQueued IsQueuedFunc
	// HasGlobalQueue reports a DAG whose queue is configured. Its scheduled
	// runs wait for queue capacity instead of starting as the schedule fires.
	HasGlobalQueue HasGlobalQueueFunc
	// RunExists checks whether a durable dag-run record already exists.
	RunExists RunExistsFunc
}

// TickPlanner is the unified scheduling decision module.
// Given the current time, it determines which start-schedule runs should dispatch,
// tracks progress via watermarks, and reacts to DAG lifecycle changes.
//
// Thread safety:
//   - entries, buffers, and deletedGrace are protected by entryMu (accessed
//     from drainEvents goroutine and cronLoop's Plan).
//   - watermarkState is shared with the flusher goroutine and protected by mu.
//   - flushMu spans a flush's snapshot and save, so a later snapshot is never
//     overwritten by an earlier one. Lock order: entryMu, flushMu, mu.
//   - Plan() holds entryMu during I/O calls (IsSuspended, IsRunning,
//     GetLatestStatus, GenRunID). This is intentional: the lock prevents
//     event processing during planning, ensuring a consistent snapshot of
//     entries for the entire plan cycle.
//   - Plan() resolves DAG profiles before the main planning lock, then
//     revalidates the entry snapshot under entryMu before producing work.
//   - lastPlanResult is accessed only from cronLoop (Plan writes, Advance reads)
//     and requires no lock. See field comment for details.
type TickPlanner struct {
	cfg TickPlannerConfig

	// watermark state (protected by mu)
	mu             sync.RWMutex
	watermarkState *schedulerstate.State
	flushMu        sync.Mutex

	// per-DAG tracking (protected by entryMu)
	entryMu      sync.Mutex
	entries      map[string]*plannerEntry
	buffers      map[string]*ScheduleBuffer
	deletedGrace map[string]time.Time

	// lastPlanResult holds the runs from the most recent Plan() call.
	// It is written by Plan() and read by Advance(). Both are called
	// sequentially from the same goroutine (cronLoop in scheduler.go),
	// so no lock is needed. Do NOT call Plan() or Advance() from
	// different goroutines without external synchronization.
	lastPlanResult []PlannedRun

	// lifecycle
	lifecycleMu sync.Mutex
	started     atomic.Bool
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

type latestScheduledSlotState int

const (
	latestScheduledSlotUnknown latestScheduledSlotState = iota
	latestScheduledSlotCurrent
	latestScheduledSlotStale
)

// plannerEntry tracks a single DAG's scheduling metadata.
type plannerEntry struct {
	DAGEntry
}

type plannerEntrySnapshot struct {
	dagName string
	DAGEntry
}

type activeDAGSchedules struct {
	profile string
	start   []ir.Schedule
	stop    []ir.Schedule
	restart []ir.Schedule
}

// NewTickPlanner creates a new TickPlanner with the given configuration.
// Nil config fields are replaced with no-op defaults, except RunExists which
// fails closed when it is not configured.
func NewTickPlanner(cfg TickPlannerConfig) *TickPlanner {
	if cfg.StateStore == nil {
		cfg.StateStore = noopStateStore{}
	}
	if cfg.IsSuspended == nil {
		cfg.IsSuspended = func(context.Context, string) (bool, error) { return false, nil }
	}
	if cfg.IsRunning == nil {
		cfg.IsRunning = func(context.Context, *ir.DAG) (bool, error) { return false, nil }
	}
	if cfg.GetLatestStatus == nil {
		cfg.GetLatestStatus = func(context.Context, *ir.DAG) (ir.DAGRunStatus, error) {
			return ir.DAGRunStatus{}, nil
		}
	}
	if cfg.Stop == nil {
		cfg.Stop = func(context.Context, *ir.DAG) error { return nil }
	}
	if cfg.Restart == nil {
		cfg.Restart = func(context.Context, DAGEntry, time.Time) error { return nil }
	}
	if cfg.IsQueued == nil {
		cfg.IsQueued = func(context.Context, *ir.DAG) (bool, error) { return false, nil }
	}
	if cfg.RunExists == nil {
		cfg.RunExists = func(context.Context, *ir.DAG, string) (bool, error) {
			return false, fmt.Errorf("runExists not configured")
		}
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	if cfg.GenRunID == nil {
		cfg.GenRunID = func(context.Context) (string, error) {
			return "", fmt.Errorf("genRunID not configured")
		}
	}
	if cfg.Dispatch == nil {
		cfg.Dispatch = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error {
			return fmt.Errorf("dispatch not configured")
		}
	}
	return &TickPlanner{
		cfg:          cfg,
		entries:      make(map[string]*plannerEntry),
		buffers:      make(map[string]*ScheduleBuffer),
		deletedGrace: make(map[string]time.Time),
	}
}

func (tp *TickPlanner) activeDAGSchedules(ctx context.Context, entry DAGEntry) (activeDAGSchedules, bool) {
	dag := entry.DAG
	if dag == nil {
		return activeDAGSchedules{}, true
	}
	if !hasProfileScopedSchedules(dag.Schedule, dag.StopSchedule, dag.RestartSchedule) {
		return activeDAGSchedules{
			start:   dag.Schedule,
			stop:    dag.StopSchedule,
			restart: dag.RestartSchedule,
		}, true
	}
	profile, ok := tp.resolveDAGProfile(ctx, entry.DefinitionID, dag)
	if !ok {
		return activeDAGSchedules{}, false
	}
	return activeDAGSchedules{
		profile: profile,
		start:   filterSchedulesByProfile(dag.Schedule, profile),
		stop:    filterSchedulesByProfile(dag.StopSchedule, profile),
		restart: filterSchedulesByProfile(dag.RestartSchedule, profile),
	}, true
}

func hasProfileScopedSchedules(scheduleGroups ...[]ir.Schedule) bool {
	for _, schedules := range scheduleGroups {
		for _, schedule := range schedules {
			if schedule.Profile != "" {
				return true
			}
		}
	}
	return false
}

func (tp *TickPlanner) resolveDAGProfile(ctx context.Context, definitionID string, dag *ir.DAG) (string, bool) {
	if tp.cfg.ProfileResolver == nil {
		return "", true
	}
	if definitionID == "" {
		logger.Error(ctx, "Failed to resolve DAG profile: definition ID is empty",
			tag.DAG(dag.Name),
		)
		return "", false
	}
	workspaceName, err := dagWorkspaceName(dag)
	if err != nil {
		logger.Error(ctx, "Failed to resolve DAG profile",
			tag.DAG(dag.Name),
			tag.Error(err),
		)
		return "", false
	}
	profile, err := tp.cfg.ProfileResolver.ResolveProfile(ctx, definitionID, workspaceName)
	if err != nil {
		logger.Error(ctx, "Failed to resolve DAG profile",
			tag.DAG(dag.Name),
			tag.Error(err),
		)
		return "", false
	}
	return profile, true
}

func filterSchedulesByProfile(schedules []ir.Schedule, profile string) []ir.Schedule {
	filtered := make([]ir.Schedule, 0, len(schedules))
	for _, schedule := range schedules {
		if scheduleMatchesProfile(schedule, profile) {
			filtered = append(filtered, schedule)
		}
	}
	return filtered
}

func scheduleMatchesProfile(schedule ir.Schedule, profile string) bool {
	return schedule.Profile == "" || schedule.Profile == profile
}

// Init loads watermark state and computes catchup buffers for existing definitions.
func (tp *TickPlanner) Init(ctx context.Context, entries []DAGEntry) error {
	tp.entryMu.Lock()
	defer tp.entryMu.Unlock()

	validEntries := make([]DAGEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.DAG != nil {
			validEntries = append(validEntries, entry)
		}
	}
	entries = validEntries

	for _, entry := range entries {
		tp.entries[entry.DAG.Name] = &plannerEntry{DAGEntry: entry}
	}

	state, err := tp.cfg.StateStore.Load(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to load watermark state", tag.Error(err))
		state = &schedulerstate.State{DAGs: make(map[string]schedulerstate.DAGWatermark)}
	}
	if state.DAGs == nil {
		state.DAGs = make(map[string]schedulerstate.DAGWatermark)
	}

	// Prune stale DAG entries that no longer exist on disk.
	activeDags := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		activeDags[entry.DAG.Name] = struct{}{}
	}
	pruned := 0
	for name := range state.DAGs {
		if _, ok := activeDags[name]; !ok {
			delete(state.DAGs, name)
			pruned++
		}
	}
	observedAt := tp.cfg.Clock().In(tp.cfg.Location)
	activeByDAG := make(map[string]activeDAGSchedules, len(entries))
	for _, entry := range entries {
		dag := entry.DAG
		active, ok := tp.activeDAGSchedules(ctx, entry)
		if !ok {
			state.DAGs[dag.Name] = reconcileNextRunState(state.DAGs[dag.Name], nil, observedAt, true)
			continue
		}
		activeByDAG[dag.Name] = active
		current := state.DAGs[dag.Name]
		next, _ := reconcileOneOffState(current, active.start, observedAt)
		next, _ = reconcileStartScheduleState(next, active.start, dag.SkipIfSuccessful, observedAt)
		next = reconcileNextRunState(next, active.start, observedAt, false)
		state.DAGs[dag.Name] = next
	}

	if pruned > 0 {
		logger.Info(ctx, "Pruned stale watermark entries",
			slog.Int("pruned", pruned),
		)
	}

	tp.mu.Lock()
	tp.watermarkState = state
	tp.mu.Unlock()

	logger.Info(ctx, "Loaded scheduler watermark",
		slog.Time("lastTick", state.LastTick),
		slog.Int("dagCount", len(state.DAGs)),
	)

	tp.settlePlannedSlots(ctx, entries)
	// The first tick takes the current minute's slots.
	firstTick := tp.cfg.Clock().Truncate(time.Minute)
	for _, entry := range entries {
		if active, ok := activeByDAG[entry.DAG.Name]; ok {
			tp.recordMissedSlot(ctx, entry, active, state.LastTick, firstTick)
		}
	}
	tp.initBuffers(ctx, entries, activeByDAG)
	tp.Flush(ctx)
	return nil
}

// settlePlannedSlots settles each slot left planned, as when the scheduler
// stopped between planning and dispatching it: dispatched when the DAG's
// latest run is of that slot. Otherwise the slot stays planned.
func (tp *TickPlanner) settlePlannedSlots(ctx context.Context, entries []DAGEntry) {
	for _, entry := range entries {
		tp.mu.RLock()
		slot := tp.watermarkState.DAGs[entry.DAG.Name].LastSlot
		tp.mu.RUnlock()
		if slot == nil || slot.Outcome != schedulerstate.SlotPlanned {
			continue
		}
		status, err := tp.cfg.GetLatestStatus(ctx, entry.DAG)
		if err != nil {
			logger.Warn(ctx, "Failed to read the latest run of a planned slot",
				tag.DAG(entry.DAG.Name), tag.Error(err))
			continue
		}
		scheduledAt, err := stringutil.ParseTime(status.ScheduleTime)
		if err == nil && scheduledAt.Truncate(time.Minute).Equal(slot.At.Truncate(time.Minute)) {
			tp.recordSlot(entry.DAG.Name, slot.At, schedulerstate.SlotDispatched)
		}
	}
}

// recordMissedSlot records as missed the latest start slot of a DAG that is
// not caught up which came due after from and before to, while no tick ran.
// A suspended DAG records none.
func (tp *TickPlanner) recordMissedSlot(ctx context.Context, entry DAGEntry, active activeDAGSchedules, from, to time.Time) {
	if from.IsZero() || (tp.cfg.QueuesEnabled && entry.DAG.CatchupWindow > 0) {
		return
	}
	slot, ok := latestCronSlot(active.start, from.In(tp.cfg.Location), to.In(tp.cfg.Location))
	if !ok {
		return
	}
	suspended, err := isSuspendedDAG(ctx, tp.cfg.IsSuspended, nil, entry.DAG, entry.DefinitionID)
	if err != nil || suspended {
		return
	}
	tp.recordSlot(entry.DAG.Name, slot, schedulerstate.SlotMissed)
}

// missedSlotLookback bounds how far back the latest missed slot is looked
// for.
const missedSlotLookback = 32 * 24 * time.Hour

// latestCronSlot returns the latest fire time of the cron schedules after
// from and before to.
func latestCronSlot(schedules []ir.Schedule, from, to time.Time) (time.Time, bool) {
	if earliest := to.Add(-missedSlotLookback); from.Before(earliest) {
		from = earliest
	}
	var latest time.Time
	for _, schedule := range schedules {
		if !schedule.IsCron() || schedule.Parsed == nil {
			continue
		}
		for t := schedule.Parsed.Next(from); t.Before(to); {
			if t.After(latest) {
				latest = t
			}
			next := schedule.Parsed.Next(t)
			if !next.After(t) {
				break
			}
			t = next
		}
	}
	return latest, !latest.IsZero()
}

// recordSlot keeps what came of a start slot as the DAG's latest, unless a
// later slot is kept, the same slot was dispatched, or it is only planned
// again. It reports whether the state changed.
func (tp *TickPlanner) recordSlot(dagName string, at time.Time, outcome schedulerstate.SlotOutcome) bool {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	if tp.watermarkState == nil {
		return false
	}
	dagState := schedulerstate.CloneDAGWatermark(tp.watermarkState.DAGs[dagName])
	if last := dagState.LastSlot; last != nil {
		same := at.Equal(last.At) && (last.Outcome == outcome || last.Outcome == schedulerstate.SlotDispatched ||
			outcome == schedulerstate.SlotPlanned)
		if at.Before(last.At) || same {
			return false
		}
	}
	dagState.LastSlot = &schedulerstate.ScheduleSlot{At: at, Outcome: outcome}
	tp.watermarkState.DAGs[dagName] = dagState
	return true
}

// initBuffers creates per-DAG queues for DAGs with CatchupWindow > 0
// and enqueues catch-up items. Requires QueuesEnabled; when disabled,
// catchup buffers are not populated and a warning is logged per DAG.
func (tp *TickPlanner) initBuffers(ctx context.Context, entries []DAGEntry, activeByDAG map[string]activeDAGSchedules) {
	if !tp.cfg.QueuesEnabled {
		for _, entry := range entries {
			dag := entry.DAG
			if dag.CatchupWindow > 0 {
				logger.Warn(ctx, "DAG has catchup enabled but queues are disabled; catchup will not run",
					tag.DAG(dag.Name),
				)
			}
		}
		return
	}

	// Snapshot watermark state under the lock. Although initBuffers is only
	// called from Init (before Start), we snapshot defensively to avoid
	// reading the shared DAGs map outside the lock.
	tp.mu.RLock()
	lastTick := tp.watermarkState.LastTick
	dagWatermarks := make(map[string]schedulerstate.DAGWatermark, len(tp.watermarkState.DAGs))
	maps.Copy(dagWatermarks, tp.watermarkState.DAGs)
	tp.mu.RUnlock()

	now := tp.cfg.Clock()
	var totalMissed int

	for _, entry := range entries {
		dag := entry.DAG
		if dag.CatchupWindow <= 0 {
			continue
		}
		active, ok := activeByDAG[dag.Name]
		if !ok && activeByDAG == nil {
			active, ok = tp.activeDAGSchedules(ctx, entry)
		}
		if !ok || len(active.start) == 0 {
			continue
		}

		var lastScheduledTime time.Time
		if wm, ok := dagWatermarks[dag.Name]; ok {
			lastScheduledTime = wm.LastScheduledTime
		}

		replayFrom := ComputeReplayFrom(dag.CatchupWindow, lastTick, lastScheduledTime, now)
		missed := computeMissedScheduleIntervals(active.start, replayFrom.In(tp.cfg.Location), now)

		if len(missed) == 0 {
			continue
		}

		totalMissed += len(missed)

		logger.Info(ctx, "Catch-up planned",
			tag.DAG(dag.Name),
			slog.Int("missedCount", len(missed)),
			slog.Time("replayFrom", replayFrom),
			slog.Time("replayTo", now),
		)

		q := NewScheduleBuffer(dag.Name, dag.OverlapPolicy)
		tp.buffers[dag.Name] = q

		for _, interval := range missed {
			if !q.Send(QueueItem{
				DAGEntry:      entry,
				ScheduledTime: interval.ScheduledTime,
				TriggerType:   ir.TriggerTypeCatchUp,
				ScheduleType:  ScheduleTypeStart,
				Schedule:      interval.Schedule,
			}) {
				logger.Error(ctx, "Catch-up buffer full, dropping remaining items",
					tag.DAG(dag.Name),
					slog.Int("buffered", q.Len()),
					slog.Int("dropped", len(missed)-q.Len()),
				)
				break
			}
		}

		if dag.OverlapPolicy == ir.OverlapPolicyLatest && q.Len() > 1 {
			dropped := q.DropAllButLast()
			totalMissed -= len(dropped)
			tp.advanceDAGWatermark(dag.Name, dropped[len(dropped)-1].ScheduledTime)
		}
	}

	if totalMissed > 0 {
		logger.Info(ctx, "Catch-up initialization complete",
			slog.Int("dagCount", len(tp.buffers)),
			slog.Int("totalMissedRuns", totalMissed),
		)
	}
}

// Plan drains queued DAG events, then returns ordered runs to dispatch this tick.
// Includes live scheduled runs and catchup runs. Only returns runs that pass
// all guards (not running, not suspended, not finished, not skipped).
// The caller just dispatches.
func (tp *TickPlanner) Plan(ctx context.Context, now time.Time) []PlannedRun {
	tp.entryMu.Lock()
	flushState := tp.pruneExpiredDeletedWatermarks(now)
	snapshots := make([]plannerEntrySnapshot, 0, len(tp.entries))
	for dagName, entry := range tp.entries {
		snapshots = append(snapshots, plannerEntrySnapshot{
			dagName:  dagName,
			DAGEntry: entry.DAGEntry,
		})
	}
	tp.entryMu.Unlock()

	activeByDAG := make(map[string]activeDAGSchedules, len(snapshots))
	for _, snapshot := range snapshots {
		active, ok := tp.activeDAGSchedules(ctx, snapshot.DAGEntry)
		if ok {
			activeByDAG[snapshot.dagName] = active
			continue
		}
		flushState = tp.reconcileNextRun(snapshot.dagName, nil, now, true) || flushState
	}
	if flushState {
		tp.Flush(ctx)
	}

	tp.entryMu.Lock()
	defer tp.entryMu.Unlock()

	var candidates []PlannedRun

	for _, snapshot := range snapshots {
		dagName := snapshot.dagName
		entry, ok := tp.entries[dagName]
		if !ok || entry.DAG != snapshot.DAG {
			continue
		}
		// Suspension is keyed by the persisted definition identity.
		suspended, err := isSuspendedDAG(ctx, tp.cfg.IsSuspended, nil, entry.DAG, entry.DefinitionID)
		if err != nil {
			logger.Error(ctx, "Failed to check DAG suspension; skipping this cycle",
				tag.DAG(dagName), tag.Error(err))
			continue
		}
		if suspended {
			tp.reconcileNextRun(dagName, nil, now, true)
			tp.dropSuspendedCatchupState(dagName, entry.DAG, now)
			continue
		}
		active, ok := activeByDAG[dagName]
		if !ok {
			continue
		}
		tp.reconcileNextRun(dagName, active.start, now.In(tp.cfg.Location), false)

		// Check catchup buffer first (catchup has priority over live)
		catchupProduced := false
		catchupDeferred := false
		if buf, ok := tp.buffers[dagName]; ok {
			tp.dropInactiveCatchupItems(ctx, dagName, buf, active.profile)
			item, hasItem := buf.Peek()
			if !hasItem {
				delete(tp.buffers, dagName)
			} else {
				running, err := tp.cfg.IsRunning(ctx, item.DAG)
				if err != nil {
					logger.Error(ctx, "Failed to check if DAG is running, assuming not running",
						tag.DAG(dagName),
						tag.Error(err),
					)
					running = false
				}

				queued, qErr := tp.cfg.IsQueued(ctx, item.DAG)
				if qErr != nil {
					logger.Error(ctx, "Failed to check if DAG is queued; deferring catch-up item",
						tag.DAG(dagName),
						tag.Error(qErr),
					)
					catchupDeferred = true
				} else {
					busy := running || queued
					if !busy {
						// For "latest", collapse to most recent before popping.
						if buf.overlapPolicy == ir.OverlapPolicyLatest && buf.Len() > 1 {
							dropped := buf.DropAllButLast()
							tp.advanceDAGWatermark(dagName, dropped[len(dropped)-1].ScheduledTime)
							// Re-peek: front changed from oldest to latest
							item, _ = buf.Peek()
						}
						buf.Pop()
						run, ok := tp.createPlannedRun(ctx, item.DAGEntry, item.Schedule, item.ScheduledTime, item.TriggerType)
						if ok {
							candidates = append(candidates, run)
							catchupProduced = true
						}
					} else {
						switch buf.overlapPolicy {
						case ir.OverlapPolicySkip:
							popped, _ := buf.Pop()
							logger.Info(ctx, "Catch-up run skipped (overlap policy: skip)",
								tag.DAG(dagName),
							)
							tp.advanceDAGWatermark(dagName, popped.ScheduledTime)
							tp.recordSlot(dagName, popped.ScheduledTime, schedulerstate.SlotOverlap)
						case ir.OverlapPolicyAll:
							// leave in buffer, retry next tick
						case ir.OverlapPolicyLatest:
							// Collapse to latest, advance watermark past discarded items.
							dropped := buf.DropAllButLast()
							if len(dropped) > 0 {
								tp.advanceDAGWatermark(dagName, dropped[len(dropped)-1].ScheduledTime)
							}
							// Leave the single remaining (latest) item for retry next tick.
						default:
							popped, _ := buf.Pop()
							logger.Warn(ctx, "Unknown overlap policy, treating as skip",
								tag.DAG(dagName),
								slog.String("overlapPolicy", string(buf.overlapPolicy)),
							)
							tp.advanceDAGWatermark(dagName, popped.ScheduledTime)
						}
					}

					// Clean up empty buffers
					if buf.Len() == 0 {
						delete(tp.buffers, dagName)
					}
				}
			}
		}

		// If catchup produced a run or was deferred, skip live evaluation.
		if catchupProduced || catchupDeferred {
			continue
		}

		var (
			startCandidate    PlannedRun
			hasStartCandidate bool
		)

		// All schedule evaluations use the configured Location so that
		// cron evaluation matches the wall-clock time the user expects.
		evalTime := now.In(tp.cfg.Location)

		// Evaluate pending one-off schedules.
		for _, schedule := range active.start {
			if !schedule.IsOneOff() {
				continue
			}

			fingerprint := schedule.Fingerprint()
			if fingerprint == "" {
				continue
			}

			oneOffState, ok := tp.pendingOneOffState(entry.DAG.Name, fingerprint)
			if !ok || oneOffState.ScheduledTime.After(evalTime) {
				continue
			}

			if !tp.shouldRunOneOff(ctx, entry.DAG) {
				continue
			}

			run, ok := tp.createPlannedRun(ctx, entry.DAGEntry, schedule, oneOffState.ScheduledTime, ir.TriggerTypeScheduler)
			if ok && shouldPreferStartCandidate(run, startCandidate, hasStartCandidate) {
				startCandidate = run
				hasStartCandidate = true
			}
		}

		// Evaluate cron schedules for live start runs.
		for _, schedule := range active.start {
			if !schedule.IsCron() {
				continue
			}
			next, due := scheduleDueAt(schedule, evalTime)
			if !due {
				continue
			}
			if ok, skipped := tp.shouldRun(ctx, entry.DAG, next, schedule); !ok {
				tp.recordSlot(dagName, next, skipped)
				continue
			}
			run, ok := tp.createPlannedRun(ctx, entry.DAGEntry, schedule, next, ir.TriggerTypeScheduler)
			if !ok {
				tp.recordSlot(dagName, next, schedulerstate.SlotFailed)
				continue
			}
			if shouldPreferStartCandidate(run, startCandidate, hasStartCandidate) {
				startCandidate = run
				hasStartCandidate = true
			}
		}

		if hasStartCandidate {
			// Recorded before dispatch starts: a dispatch that settles first
			// is never overwritten.
			if startCandidate.Schedule.IsCron() {
				tp.recordSlot(dagName, startCandidate.ScheduledTime, schedulerstate.SlotPlanned)
			}
			candidates = append(candidates, startCandidate)
		}

		// Evaluate stop schedules.
		for _, schedule := range active.stop {
			next, due := scheduleDueAt(schedule, evalTime)
			if !due {
				continue
			}

			// Guard: DAG must be running before issuing a stop
			latestStatus, err := tp.cfg.GetLatestStatus(ctx, entry.DAG)
			if err != nil {
				logger.Error(ctx, "Failed to fetch DAG status for stop schedule",
					tag.DAG(dagName), tag.Error(err))
				continue
			}
			if latestStatus.Status != ir.Running {
				continue
			}

			candidates = append(candidates, PlannedRun{
				DAGEntry:      entry.DAGEntry,
				ScheduledTime: next,
				ScheduleType:  ScheduleTypeStop,
				Schedule:      schedule,
			})
		}

		// Evaluate restart schedules (no guard -- fires unconditionally).
		for _, schedule := range active.restart {
			next, due := scheduleDueAt(schedule, evalTime)
			if !due {
				continue
			}
			candidates = append(candidates, PlannedRun{
				DAGEntry:      entry.DAGEntry,
				ScheduledTime: next,
				ScheduleType:  ScheduleTypeRestart,
				Schedule:      schedule,
			})
		}
	}

	tp.lastPlanResult = candidates
	return candidates
}

func (tp *TickPlanner) dropInactiveCatchupItems(ctx context.Context, dagName string, buf *ScheduleBuffer, profile string) {
	for {
		item, ok := buf.Peek()
		if !ok {
			return
		}
		if item.Schedule.GetKind() == "" || scheduleMatchesProfile(item.Schedule, profile) {
			return
		}
		popped, _ := buf.Pop()
		logger.Info(ctx, "Catch-up run skipped because schedule profile is inactive",
			tag.DAG(dagName),
			slog.String("schedule_profile", popped.Schedule.Profile),
		)
		tp.advanceDAGWatermark(dagName, popped.ScheduledTime)
	}
}

// shouldRun checks all guards for a live scheduled run. When a guard holds
// the run back, it returns what came of the slot.
func (tp *TickPlanner) shouldRun(ctx context.Context, dag *ir.DAG, scheduledTime time.Time, schedule ir.Schedule) (bool, schedulerstate.SlotOutcome) {
	// Guard 1: isRunning (uses process-level check)
	running, err := tp.cfg.IsRunning(ctx, dag)
	if err != nil {
		logger.Error(ctx, "Failed to check if DAG is running",
			tag.DAG(dag.Name),
			tag.Error(err),
		)
		return false, schedulerstate.SlotFailed
	}
	if running {
		logger.Info(ctx, "Skipping job because the DAG is running",
			tag.DAG(dag.Name),
			tag.ScheduledTime(scheduledTime),
		)
		return false, schedulerstate.SlotRunning
	}

	// Guard 1b: isQueued, a queued run of any trigger keeps the DAG busy, as a running one does.
	// On error, conservatively skip (assume busy) to avoid duplicates.
	queued, qErr := tp.cfg.IsQueued(ctx, dag)
	if qErr != nil {
		logger.Error(ctx, "Failed to check if DAG is queued; assuming busy",
			tag.DAG(dag.Name),
			tag.Error(qErr),
		)
		return false, schedulerstate.SlotFailed
	}
	if queued {
		logger.Info(ctx, "Skipping job because a run of the DAG is queued",
			tag.DAG(dag.Name),
			tag.ScheduledTime(scheduledTime),
		)
		return false, schedulerstate.SlotQueued
	}

	latestStatus, err := tp.cfg.GetLatestStatus(ctx, dag)
	if err != nil {
		logger.Error(ctx, "Failed to fetch latest DAG status",
			tag.DAG(dag.Name),
			tag.Error(err),
		)
		return false, schedulerstate.SlotFailed
	}

	// Also check status-based running (belt and suspenders)
	if latestStatus.Status == ir.Running {
		return false, schedulerstate.SlotRunning
	}

	latestScheduleTime, slotState := latestScheduledSlot(latestStatus, schedule)
	switch slotState {
	case latestScheduledSlotCurrent:
		// Guard 2: alreadyFinished — exact scheduled slot already completed.
		if !latestScheduleTime.Before(scheduledTime) {
			return false, schedulerstate.SlotDone
		}

		// Guard 3: skipIfSuccessful — only the current schedule's own slots may suppress.
		if dag.SkipIfSuccessful && latestStatus.Status == ir.Succeeded && schedule.Parsed != nil {
			if tp.isPreEditSuccess(dag.Name, latestStatus) {
				return true, ""
			}
			prevExecTime := computePrevExecTime(scheduledTime, schedule)
			if !latestScheduleTime.Before(prevExecTime) && latestScheduleTime.Before(scheduledTime) {
				logger.Info(ctx, "Skipping job due to successful prior run",
					tag.DAG(dag.Name),
					slog.String("schedule-time", latestScheduleTime.Format(time.RFC3339)),
				)
				return false, schedulerstate.SlotSucceeded
			}
		}

		return true, ""
	case latestScheduledSlotStale:
		// The latest run belongs to a removed/edited slot. Do not let its runtime
		// timestamps suppress the current schedule.
		return true, ""
	case latestScheduledSlotUnknown:
		// Fall back to runtime-based suppression when the latest run does not carry
		// a trustworthy scheduled slot identity.
	}

	// Guard 2 fallback: legacy/manual runs without an authoritative schedule slot.
	latestStartedAt, ok := latestRunReferenceTime(latestStatus)
	if ok {
		if !latestStartedAt.Before(scheduledTime) {
			return false, schedulerstate.SlotDone
		}

		// Guard 3 fallback: preserve manual-run semantics when no slot identity exists.
		if dag.SkipIfSuccessful && latestStatus.Status == ir.Succeeded && schedule.Parsed != nil {
			if tp.isPreEditSuccess(dag.Name, latestStatus) {
				return true, ""
			}
			prevExecTime := computePrevExecTime(scheduledTime, schedule)
			if !latestStartedAt.Before(prevExecTime) && latestStartedAt.Before(scheduledTime) {
				logger.Info(ctx, "Skipping job due to successful prior run",
					tag.DAG(dag.Name),
					slog.String("start-time", latestStartedAt.Format(time.RFC3339)),
				)
				return false, schedulerstate.SlotSucceeded
			}
		}
	}

	return true, ""
}

func latestRunReferenceTime(status ir.DAGRunStatus) (time.Time, bool) {
	latestStartedAt, err := stringutil.ParseTime(status.StartedAt)
	if err != nil {
		return time.Time{}, false
	}
	if status.QueuedAt != "" {
		queuedAt, parseErr := stringutil.ParseTime(status.QueuedAt)
		if parseErr == nil && queuedAt.Before(latestStartedAt) {
			latestStartedAt = queuedAt
		}
	}
	return latestStartedAt.Truncate(time.Minute), true
}

func latestSuccessReferenceTime(status ir.DAGRunStatus) (time.Time, bool) {
	if finishedAt, err := stringutil.ParseTime(status.FinishedAt); err == nil && !finishedAt.IsZero() {
		return finishedAt, true
	}
	if startedAt, err := stringutil.ParseTime(status.StartedAt); err == nil && !startedAt.IsZero() {
		return startedAt, true
	}
	return time.Time{}, false
}

func latestScheduledSlot(status ir.DAGRunStatus, schedule ir.Schedule) (time.Time, latestScheduledSlotState) {
	if status.ScheduleTime == "" {
		return time.Time{}, latestScheduledSlotUnknown
	}

	scheduledAt, err := stringutil.ParseTime(status.ScheduleTime)
	if err != nil {
		return time.Time{}, latestScheduledSlotUnknown
	}

	scheduledAt = scheduledAt.Truncate(time.Minute)
	if !scheduleMatchesFireTime(schedule, scheduledAt) {
		return scheduledAt, latestScheduledSlotStale
	}

	return scheduledAt, latestScheduledSlotCurrent
}

func scheduleMatchesFireTime(schedule ir.Schedule, scheduledTime time.Time) bool {
	next, due := scheduleDueAt(schedule, scheduledTime)
	return due && next.Equal(scheduledTime)
}

func (tp *TickPlanner) isPreEditSuccess(dagName string, status ir.DAGRunStatus) bool {
	resetAt := tp.skipSuccessResetAt(dagName)
	if resetAt.IsZero() {
		return false
	}

	successAt, ok := latestSuccessReferenceTime(status)
	return ok && successAt.Before(resetAt)
}

func (tp *TickPlanner) skipSuccessResetAt(dagName string) time.Time {
	tp.mu.RLock()
	defer tp.mu.RUnlock()

	if tp.watermarkState == nil {
		return time.Time{}
	}
	return tp.watermarkState.DAGs[dagName].SkipSuccessResetAt
}

func (tp *TickPlanner) shouldRunOneOff(ctx context.Context, dag *ir.DAG) bool {
	running, err := tp.cfg.IsRunning(ctx, dag)
	if err != nil {
		logger.Error(ctx, "Failed to check if DAG is running",
			tag.DAG(dag.Name),
			tag.Error(err),
		)
		return false
	}
	if running {
		return false
	}

	queued, qErr := tp.cfg.IsQueued(ctx, dag)
	if qErr != nil {
		logger.Error(ctx, "Failed to check if DAG is queued; assuming busy",
			tag.DAG(dag.Name),
			tag.Error(qErr),
		)
		return false
	}
	if queued {
		return false
	}

	latestStatus, err := tp.cfg.GetLatestStatus(ctx, dag)
	if err != nil {
		logger.Error(ctx, "Failed to fetch latest DAG status",
			tag.DAG(dag.Name),
			tag.Error(err),
		)
		return false
	}

	return latestStatus.Status != ir.Running
}

func (tp *TickPlanner) pendingOneOffState(dagName, fingerprint string) (schedulerstate.OneOffScheduleState, bool) {
	tp.mu.RLock()
	defer tp.mu.RUnlock()

	dagState, ok := tp.watermarkState.DAGs[dagName]
	if !ok || dagState.OneOffs == nil {
		return schedulerstate.OneOffScheduleState{}, false
	}
	oneOff, ok := dagState.OneOffs[fingerprint]
	if !ok || oneOff.Status != schedulerstate.OneOffStatusPending {
		return schedulerstate.OneOffScheduleState{}, false
	}
	return oneOff, true
}

// computePrevExecTime calculates the previous schedule fire time before next.
// It walks forward from (next - 32 days) to find the last fire time before next.
// 32 days covers monthly schedules, the most common sparse cron interval.
// This correctly handles non-uniform cron schedules (e.g., "0 9,17 * * *").
func computePrevExecTime(next time.Time, schedule ir.Schedule) time.Time {
	if schedule.Parsed == nil {
		return next
	}
	// Walk forward from 32 days before next to find the last fire time before next.
	seed := next.Add(-32 * 24 * time.Hour)
	var prev time.Time
	t := schedule.Parsed.Next(seed)
	for t.Before(next) {
		prev = t
		t = schedule.Parsed.Next(t)
	}
	if prev.IsZero() {
		// Fallback: no previous fire time found within the 7-day window.
		// Use interval heuristic as last resort.
		nextNext := schedule.Parsed.Next(next.Add(time.Second))
		return next.Add(-(nextNext.Sub(next)))
	}
	return prev
}

// scheduleDueAt returns the next fire time if the schedule is due at the given
// time, or the zero value if the schedule should not fire.
func scheduleDueAt(schedule ir.Schedule, now time.Time) (time.Time, bool) {
	if schedule.Parsed == nil {
		return time.Time{}, false
	}
	next := schedule.Parsed.Next(now.Add(-time.Second))
	if next.After(now) {
		return time.Time{}, false
	}
	return next, true
}

// createPlannedRun generates a run ID and constructs a PlannedRun.
// For catchup runs, a deterministic ID is generated from the DAG name and
// scheduled time. For all other runs, a random UUID v7 is used.
func (tp *TickPlanner) createPlannedRun(ctx context.Context, entry DAGEntry, schedule ir.Schedule, scheduledTime time.Time, triggerType ir.TriggerType) (PlannedRun, bool) {
	dag := entry.DAG
	var runID string
	if triggerType == ir.TriggerTypeCatchUp {
		runID = GenerateCatchupRunID(dag.Name, scheduledTime)
	} else if schedule.IsOneOff() {
		runID = GenerateOneOffRunID(dag.Name, schedule.Fingerprint(), scheduledTime)
	} else {
		var err error
		runID, err = tp.cfg.GenRunID(ctx)
		if err != nil {
			logger.Error(ctx, "Failed to generate run ID",
				tag.DAG(dag.Name),
				tag.Error(err),
			)
			return PlannedRun{}, false
		}
	}

	return PlannedRun{
		DAGEntry:      entry,
		RunID:         runID,
		ScheduledTime: scheduledTime,
		TriggerType:   triggerType,
		ScheduleType:  ScheduleTypeStart,
		Schedule:      schedule,
		Fingerprint:   schedule.Fingerprint(),
	}, true
}

// Advance records that this tick was processed. Updates global and per-DAG watermarks.
func (tp *TickPlanner) Advance(now time.Time) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	tp.watermarkState.LastTick = now

	for _, run := range tp.lastPlanResult {
		if run.ScheduleType != ScheduleTypeStart {
			continue
		}
		if run.TriggerType == ir.TriggerTypeCatchUp {
			continue // watermark updated in DispatchRun on success
		}
		if !run.Schedule.IsCron() {
			continue
		}
		dagState := schedulerstate.CloneDAGWatermark(tp.watermarkState.DAGs[run.DAG.Name])
		dagState.LastScheduledTime = run.ScheduledTime
		tp.watermarkState.DAGs[run.DAG.Name] = dagState
	}

	tp.lastPlanResult = nil
}

func (tp *TickPlanner) dropSuspendedCatchupState(dagName string, dag *ir.DAG, now time.Time) {
	if _, ok := tp.buffers[dagName]; ok {
		delete(tp.buffers, dagName)
		tp.advanceDAGWatermark(dagName, now)
		return
	}
	if dag != nil && dag.CatchupWindow > 0 {
		tp.advanceDAGWatermark(dagName, now)
	}
}

// advanceDAGWatermark updates the per-DAG watermark to the given time.
// Caller must NOT hold tp.mu.
func (tp *TickPlanner) advanceDAGWatermark(dagName string, scheduledTime time.Time) bool {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	dagState := schedulerstate.CloneDAGWatermark(tp.watermarkState.DAGs[dagName])
	if dagState.LastScheduledTime.Equal(scheduledTime) {
		return false
	}
	dagState.LastScheduledTime = scheduledTime
	tp.watermarkState.DAGs[dagName] = dagState
	return true
}

func (tp *TickPlanner) markOneOffConsumed(dagName, fingerprint string, scheduledTime time.Time) bool {
	if fingerprint == "" {
		return false
	}

	tp.mu.Lock()
	defer tp.mu.Unlock()

	dagState := schedulerstate.CloneDAGWatermark(tp.watermarkState.DAGs[dagName])
	if dagState.OneOffs == nil {
		dagState.OneOffs = make(map[string]schedulerstate.OneOffScheduleState)
	}

	state := dagState.OneOffs[fingerprint]
	if state.ScheduledTime.IsZero() {
		state.ScheduledTime = scheduledTime
	}
	if state.Status == schedulerstate.OneOffStatusConsumed {
		return false
	}

	state.Status = schedulerstate.OneOffStatusConsumed
	dagState.OneOffs[fingerprint] = state
	tp.watermarkState.DAGs[dagName] = dagState
	return true
}

// Flush writes the current scheduler state snapshot to durable storage.
// Safe for concurrent use; concurrent flushes save in snapshot order.
func (tp *TickPlanner) Flush(ctx context.Context) {
	tp.flushMu.Lock()
	defer tp.flushMu.Unlock()

	// Snapshot under read lock to avoid holding the lock during I/O.
	tp.mu.RLock()
	if tp.watermarkState == nil {
		tp.mu.RUnlock()
		return
	}
	snapshot := &schedulerstate.State{
		LastTick: tp.watermarkState.LastTick,
		DAGs:     make(map[string]schedulerstate.DAGWatermark, len(tp.watermarkState.DAGs)),
	}
	for dagName, dagState := range tp.watermarkState.DAGs {
		snapshot.DAGs[dagName] = schedulerstate.CloneDAGWatermark(dagState)
	}
	tp.mu.RUnlock()

	if err := tp.cfg.StateStore.Save(ctx, snapshot); err != nil {
		logger.Error(ctx, "Failed to flush watermark state", tag.Error(err))
	}
}

// Start launches the internal goroutines (event drainer + watermark flusher).
func (tp *TickPlanner) Start(ctx context.Context) {
	tp.lifecycleMu.Lock()
	defer tp.lifecycleMu.Unlock()

	if !tp.started.CompareAndSwap(false, true) {
		return
	}
	ctx, tp.cancel = context.WithCancel(ctx)
	tp.wg.Add(2)
	go func() {
		defer tp.wg.Done()
		tp.drainEvents(ctx)
	}()
	go func() {
		defer tp.wg.Done()
		tp.startFlusher(ctx)
	}()
}

// Stop cancels internal goroutines, waits for them, and performs a final flush.
func (tp *TickPlanner) Stop(ctx context.Context) {
	tp.lifecycleMu.Lock()
	cancel := tp.cancel
	tp.cancel = nil
	tp.lifecycleMu.Unlock()

	if cancel != nil {
		cancel()
	}
	tp.wg.Wait()
	tp.Flush(ctx)
}

// startFlusher runs the periodic watermark flusher. Blocks until ctx is done.
func (tp *TickPlanner) startFlusher(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tp.Flush(ctx)
		}
	}
}

// drainEvents continuously processes DAG change events. Blocks until ctx is done.
func (tp *TickPlanner) drainEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-tp.cfg.Events:
			if !ok {
				return
			}
			tp.entryMu.Lock()
			tp.handleEvent(ctx, event)
			tp.entryMu.Unlock()
		}
	}
}

// handleEvent processes a single DAG change event.
func (tp *TickPlanner) handleEvent(ctx context.Context, event DAGChangeEvent) {
	flushNow := false

	switch event.Type {
	case DAGChangeAdded:
		if event.DAG == nil {
			return
		}
		dagName := event.DAG.Name
		active, activeOK := tp.activeDAGSchedules(ctx, event.DAGEntry)
		delete(tp.deletedGrace, dagName)
		tp.entries[dagName] = &plannerEntry{DAGEntry: event.DAGEntry}
		// Set watermark to now (new DAGs have no catchup)
		flushNow = tp.advanceDAGWatermark(dagName, tp.cfg.Clock())
		if activeOK {
			flushNow = tp.reconcileStartScheduleState(event.DAG, active) || flushNow
			flushNow = tp.reconcileOneOffSchedules(event.DAG, active) || flushNow
			flushNow = tp.reconcileNextRun(dagName, active.start, tp.cfg.Clock().In(tp.cfg.Location), false) || flushNow
		} else {
			flushNow = tp.reconcileNextRun(dagName, nil, tp.cfg.Clock().In(tp.cfg.Location), true) || flushNow
		}
		logger.Info(ctx, "Planner: DAG added", tag.DAG(dagName))

	case DAGChangeUpdated:
		if event.DAG == nil {
			return
		}
		dagName := event.DAG.Name
		active, activeOK := tp.activeDAGSchedules(ctx, event.DAGEntry)
		delete(tp.deletedGrace, dagName)
		tp.entries[dagName] = &plannerEntry{DAGEntry: event.DAGEntry}
		if activeOK && event.DAG.CatchupWindow > 0 {
			flushNow = tp.recomputeBuffer(ctx, event.DAGEntry, active)
		} else {
			delete(tp.buffers, dagName)
		}
		if activeOK {
			flushNow = tp.reconcileStartScheduleState(event.DAG, active) || flushNow
			flushNow = tp.reconcileOneOffSchedules(event.DAG, active) || flushNow
			flushNow = tp.reconcileNextRun(dagName, active.start, tp.cfg.Clock().In(tp.cfg.Location), false) || flushNow
		} else {
			flushNow = tp.reconcileNextRun(dagName, nil, tp.cfg.Clock().In(tp.cfg.Location), true) || flushNow
		}
		logger.Info(ctx, "Planner: DAG updated", tag.DAG(dagName))

	case DAGChangeDeleted:
		if event.DAG == nil {
			return
		}
		dagName := event.DAG.Name
		delete(tp.entries, dagName)
		delete(tp.buffers, dagName)
		if tp.hasDAGWatermark(dagName) {
			tp.deletedGrace[dagName] = tp.cfg.Clock().Add(deletedWatermarkGrace)
		}
		// Preserve watermark state briefly across delete+add rewrite cycles so
		// the re-added DAG can detect schedule edits before its next slot.
		logger.Info(ctx, "Planner: DAG deleted", tag.DAG(dagName))
	}

	if flushNow {
		tp.Flush(ctx)
	}
}

func (tp *TickPlanner) reconcileOneOffSchedules(dag *ir.DAG, active activeDAGSchedules) bool {
	if dag == nil {
		return false
	}

	tp.mu.Lock()
	defer tp.mu.Unlock()

	current := tp.watermarkState.DAGs[dag.Name]
	next, changed := reconcileOneOffState(current, active.start, tp.cfg.Clock())
	if !changed {
		return false
	}

	if isZeroDAGWatermark(next) {
		delete(tp.watermarkState.DAGs, dag.Name)
	} else {
		tp.watermarkState.DAGs[dag.Name] = next
	}
	return true
}

func (tp *TickPlanner) reconcileNextRun(dagName string, schedules []ir.Schedule, now time.Time, suspended bool) bool {
	if dagName == "" {
		return false
	}

	tp.mu.Lock()
	defer tp.mu.Unlock()

	current := tp.watermarkState.DAGs[dagName]
	next := reconcileNextRunState(current, schedules, now, suspended)
	if sameTimePtr(current.NextRun, next.NextRun) {
		if _, ok := tp.watermarkState.DAGs[dagName]; ok {
			return false
		}
	}
	tp.watermarkState.DAGs[dagName] = next
	return true
}

func (tp *TickPlanner) recomputeDAGProjection(ctx context.Context, dag *ir.DAG) bool {
	if dag == nil {
		return false
	}
	entry, exists := tp.entries[dag.Name]
	if !exists {
		return false
	}
	active, ok := tp.activeDAGSchedules(ctx, entry.DAGEntry)
	if !ok {
		return tp.reconcileNextRun(dag.Name, nil, tp.cfg.Clock().In(tp.cfg.Location), true)
	}
	return tp.reconcileNextRun(
		dag.Name,
		active.start,
		tp.cfg.Clock().In(tp.cfg.Location),
		false,
	)
}

func (tp *TickPlanner) reconcileStartScheduleState(dag *ir.DAG, active activeDAGSchedules) bool {
	if dag == nil {
		return false
	}

	tp.mu.Lock()
	defer tp.mu.Unlock()

	current := tp.watermarkState.DAGs[dag.Name]
	next, changed := reconcileStartScheduleState(current, active.start, dag.SkipIfSuccessful, tp.cfg.Clock())
	if !changed {
		return false
	}

	if isZeroDAGWatermark(next) {
		delete(tp.watermarkState.DAGs, dag.Name)
	} else {
		tp.watermarkState.DAGs[dag.Name] = next
	}
	return true
}

func (tp *TickPlanner) hasDAGWatermark(dagName string) bool {
	tp.mu.RLock()
	defer tp.mu.RUnlock()

	if tp.watermarkState == nil {
		return false
	}
	_, ok := tp.watermarkState.DAGs[dagName]
	return ok
}

func (tp *TickPlanner) pruneExpiredDeletedWatermarks(now time.Time) bool {
	if len(tp.deletedGrace) == 0 {
		return false
	}

	tp.mu.Lock()
	defer tp.mu.Unlock()

	if tp.watermarkState == nil || tp.watermarkState.DAGs == nil {
		return false
	}

	changed := false
	for dagName, expiresAt := range tp.deletedGrace {
		if now.Before(expiresAt) {
			continue
		}
		delete(tp.deletedGrace, dagName)
		if _, active := tp.entries[dagName]; active {
			continue
		}
		if _, ok := tp.watermarkState.DAGs[dagName]; !ok {
			continue
		}
		delete(tp.watermarkState.DAGs, dagName)
		changed = true
	}
	return changed
}

// reinsertCatchupItem puts a failed catchup run back at the front of the
// DAG's schedule buffer so it retries on the next tick. If the buffer was
// already cleaned up, a new one is created.
func (tp *TickPlanner) reinsertCatchupItem(ctx context.Context, run PlannedRun) {
	tp.entryMu.Lock()
	defer tp.entryMu.Unlock()

	buf, ok := tp.buffers[run.DAG.Name]
	if !ok {
		buf = NewScheduleBuffer(run.DAG.Name, run.DAG.OverlapPolicy)
		tp.buffers[run.DAG.Name] = buf
	}
	if !buf.Prepend(QueueItem{
		DAGEntry:      run.DAGEntry,
		ScheduledTime: run.ScheduledTime,
		TriggerType:   run.TriggerType,
		ScheduleType:  run.ScheduleType,
		Schedule:      run.Schedule,
	}) {
		logger.Error(ctx, "Failed to re-insert catchup item; buffer full",
			tag.DAG(run.DAG.Name),
		)
	}
}

// resume retains pending work and adds slots missed while the scheduler was paused.
// A DAG that is not caught up records its latest skipped slot as missed.
func (tp *TickPlanner) resume(ctx context.Context, now time.Time) {
	tp.entryMu.Lock()
	defer tp.entryMu.Unlock()
	tp.mu.RLock()
	lastTick := tp.watermarkState.LastTick
	tp.mu.RUnlock()
	for name, entry := range tp.entries {
		dag := entry.DAG
		active, ok := tp.activeDAGSchedules(ctx, entry.DAGEntry)
		if !ok {
			continue
		}
		if !tp.cfg.QueuesEnabled || dag.CatchupWindow <= 0 {
			tp.recordMissedSlot(ctx, entry.DAGEntry, active, lastTick, now)
			continue
		}
		tp.mu.RLock()
		from := ComputeReplayFrom(dag.CatchupWindow, tp.watermarkState.LastTick,
			tp.watermarkState.DAGs[name].LastScheduledTime, now)
		tp.mu.RUnlock()
		buffer := tp.buffers[name]
		if buffer != nil && buffer.Len() > 0 {
			// Pending buffers already cover their interval, including gaps between cron slots.
			last := buffer.items[len(buffer.items)-1].ScheduledTime
			if last.After(from) {
				from = last
			}
		}
		missed := computeMissedScheduleIntervals(active.start, from.In(tp.cfg.Location), now)
		if len(missed) == 0 {
			continue
		}
		if buffer == nil {
			buffer = NewScheduleBuffer(name, dag.OverlapPolicy)
			tp.buffers[name] = buffer
		}
		for _, interval := range missed {
			buffer.items = append(buffer.items, QueueItem{DAGEntry: entry.DAGEntry, ScheduledTime: interval.ScheduledTime,
				TriggerType: ir.TriggerTypeCatchUp, ScheduleType: ScheduleTypeStart, Schedule: interval.Schedule})
		}
		tp.trimBuffer(buffer)
	}
}

// trimBuffer retains the newest slots after pending and recovered work are merged.
func (tp *TickPlanner) trimBuffer(buffer *ScheduleBuffer) bool {
	slices.SortFunc(buffer.items, func(a, b QueueItem) int {
		return a.ScheduledTime.Compare(b.ScheduledTime)
	})
	if buffer.overlapPolicy == ir.OverlapPolicyLatest && buffer.Len() > 1 {
		dropped := buffer.DropAllButLast()
		return tp.advanceDAGWatermark(buffer.dagName, dropped[len(dropped)-1].ScheduledTime)
	}
	if buffer.maxItems > 0 && buffer.Len() > buffer.maxItems {
		dropped := buffer.Len() - buffer.maxItems
		lastDropped := buffer.items[dropped-1].ScheduledTime
		buffer.items = slices.Delete(buffer.items, 0, dropped)
		return tp.advanceDAGWatermark(buffer.dagName, lastDropped)
	}
	return false
}

// recomputeBuffer refreshes pending runs and adds missed slots from the watermark.
func (tp *TickPlanner) recomputeBuffer(ctx context.Context, entry DAGEntry, active activeDAGSchedules) bool {
	dag := entry.DAG
	if !tp.cfg.QueuesEnabled || len(active.start) == 0 {
		delete(tp.buffers, dag.Name)
		return false
	}

	// Snapshot needed values under the lock to avoid reading the shared map
	// after releasing it (Advance and handleEvent can modify DAGs concurrently).
	tp.mu.RLock()
	lastTick := tp.watermarkState.LastTick
	var lastScheduledTime time.Time
	if wm, ok := tp.watermarkState.DAGs[dag.Name]; ok {
		lastScheduledTime = wm.LastScheduledTime
	}
	tp.mu.RUnlock()

	now := tp.cfg.Clock().In(tp.cfg.Location)

	replayFrom := ComputeReplayFrom(dag.CatchupWindow, lastTick, lastScheduledTime, now)
	missed := computeMissedScheduleIntervals(active.start, replayFrom.In(tp.cfg.Location), now)

	q := NewScheduleBuffer(dag.Name, dag.OverlapPolicy)
	seen := make(map[time.Time]bool)
	if pending := tp.buffers[dag.Name]; pending != nil {
		// Keep compatible pending slots and execute the updated definition.
		schedules := make(map[string]ir.Schedule, len(active.start))
		for _, schedule := range active.start {
			schedules[schedule.Fingerprint()] = schedule
		}
		for _, item := range pending.items {
			if schedule, ok := schedules[item.Schedule.Fingerprint()]; ok {
				item.DAGEntry = entry
				item.Schedule = schedule
				q.items = append(q.items, item)
				seen[item.ScheduledTime.UTC()] = true
			}
		}
	}
	for _, interval := range missed {
		if seen[interval.ScheduledTime.UTC()] {
			continue
		}
		q.items = append(q.items, QueueItem{
			DAGEntry:      entry,
			ScheduledTime: interval.ScheduledTime,
			TriggerType:   ir.TriggerTypeCatchUp,
			ScheduleType:  ScheduleTypeStart,
			Schedule:      interval.Schedule,
		})
	}
	if q.Len() == 0 {
		delete(tp.buffers, dag.Name)
		return false
	}
	watermarkAdvanced := tp.trimBuffer(q)

	tp.buffers[dag.Name] = q

	logger.Info(ctx, "Recomputed catch-up buffer",
		tag.DAG(dag.Name),
		slog.Int("missedCount", len(missed)),
	)
	return watermarkAdvanced
}

// queuesRun reports a scheduled run the queue admits rather than one the
// scheduler starts as its schedule fires, so the queue's capacity paces every
// run of the DAGs that share it.
func (tp *TickPlanner) queuesRun(dag *ir.DAG) bool {
	return tp.cfg.QueuesEnabled &&
		tp.cfg.Enqueue != nil &&
		tp.cfg.HasGlobalQueue != nil &&
		tp.cfg.HasGlobalQueue(dag)
}

// DispatchRun dispatches a PlannedRun using the configured dispatch functions.
func (tp *TickPlanner) DispatchRun(ctx context.Context, run PlannedRun) {
	logger.Info(ctx, "Dispatching planned run",
		tag.DAG(run.DAG.Name),
		slog.String("scheduleType", run.ScheduleType.String()),
		slog.String("scheduledTime", run.ScheduledTime.Format(time.RFC3339)),
	)

	if run.ScheduleType == ScheduleTypeStart &&
		isSchedulerManagedTriggerType(run.TriggerType) {
		suspended, err := isSuspendedDAG(ctx, tp.cfg.IsSuspended, nil, run.DAG, run.DefinitionID)
		if err != nil {
			logger.Error(ctx, "Failed to check DAG suspension; skipping dispatch",
				tag.DAG(run.DAG.Name), tag.Error(err))
			tp.recordStartSlot(run, schedulerstate.SlotFailed)
			if run.TriggerType == ir.TriggerTypeCatchUp {
				tp.reinsertCatchupItem(ctx, run)
			}
			return
		}
		if suspended {
			logger.Info(ctx, "Skipping suspended scheduler-managed run dispatch",
				tag.DAG(run.DAG.Name),
				slog.String("trigger_type", run.TriggerType.String()),
			)
			if run.TriggerType == ir.TriggerTypeCatchUp {
				tp.advanceDAGWatermark(run.DAG.Name, run.ScheduledTime)
			}
			return
		}
	}

	if run.ScheduleType == ScheduleTypeStart && run.Schedule.IsOneOff() {
		exists, err := tp.cfg.RunExists(ctx, run.DAG, run.RunID)
		if err != nil {
			logger.Error(ctx, "Failed to check for existing one-off dag-run",
				tag.DAG(run.DAG.Name),
				tag.RunID(run.RunID),
				tag.Error(err),
			)
			return
		}
		if !exists {
			legacyRunID := generateLegacyOneOffRunID(run.DAG.Name, run.Fingerprint, run.ScheduledTime)
			exists, err = tp.cfg.RunExists(ctx, run.DAG, legacyRunID)
			if err != nil {
				logger.Error(ctx, "Failed to check for existing one-off dag-run",
					tag.DAG(run.DAG.Name),
					tag.RunID(legacyRunID),
					tag.Error(err),
				)
				return
			}
		}
		if exists {
			if tp.markOneOffConsumed(run.DAG.Name, run.Fingerprint, run.ScheduledTime) {
				tp.recomputeDAGProjection(ctx, run.DAG)
				tp.Flush(ctx)
			}
			return
		}
	}

	var err error
	switch run.ScheduleType {
	case ScheduleTypeStart:
		if run.TriggerType == ir.TriggerTypeCatchUp {
			if tp.cfg.Enqueue == nil {
				logger.Error(ctx, "Catchup dispatch requires queues to be enabled; skipping",
					tag.DAG(run.DAG.Name),
				)
				return
			}
			legacyRunID := generateLegacyCatchupRunID(run.DAG.Name, run.ScheduledTime)
			exists, existsErr := tp.cfg.RunExists(ctx, run.DAG, legacyRunID)
			if existsErr != nil {
				logger.Error(ctx, "Failed to check for existing catchup dag-run",
					tag.DAG(run.DAG.Name),
					tag.RunID(legacyRunID),
					tag.Error(existsErr),
				)
				tp.reinsertCatchupItem(ctx, run)
				return
			}
			if exists {
				tp.advanceDAGWatermark(run.DAG.Name, run.ScheduledTime)
				tp.recordStartSlot(run, schedulerstate.SlotDone)
				return
			}
			err = tp.cfg.Enqueue(ctx, run.DAGEntry, run.RunID, run.TriggerType, run.ScheduledTime)
		} else if tp.queuesRun(run.DAG) {
			err = tp.cfg.Enqueue(ctx, run.DAGEntry, run.RunID, run.TriggerType, run.ScheduledTime)
		} else {
			err = tp.cfg.Dispatch(ctx, run.DAGEntry, run.RunID, run.TriggerType, run.ScheduledTime)
		}
	case ScheduleTypeStop:
		err = tp.cfg.Stop(ctx, run.DAG)
	case ScheduleTypeRestart:
		err = tp.cfg.Restart(ctx, run.DAGEntry, run.ScheduledTime)
	}

	if err != nil {
		if run.ScheduleType == ScheduleTypeStart && run.Schedule.IsOneOff() {
			exists, existsErr := tp.cfg.RunExists(ctx, run.DAG, run.RunID)
			if existsErr != nil {
				logger.Error(ctx, "Failed to re-check one-off dag-run after dispatch error",
					tag.DAG(run.DAG.Name),
					tag.RunID(run.RunID),
					tag.Error(existsErr),
				)
				return
			}
			if !exists {
				legacyRunID := generateLegacyOneOffRunID(run.DAG.Name, run.Fingerprint, run.ScheduledTime)
				exists, existsErr = tp.cfg.RunExists(ctx, run.DAG, legacyRunID)
				if existsErr != nil {
					logger.Error(ctx, "Failed to re-check one-off dag-run after dispatch error",
						tag.DAG(run.DAG.Name),
						tag.RunID(legacyRunID),
						tag.Error(existsErr),
					)
					return
				}
			}
			if exists {
				if tp.markOneOffConsumed(run.DAG.Name, run.Fingerprint, run.ScheduledTime) {
					tp.recomputeDAGProjection(ctx, run.DAG)
					tp.Flush(ctx)
				}
				return
			}
		}

		logger.Error(ctx, "Failed to dispatch run",
			tag.DAG(run.DAG.Name),
			slog.String("scheduleType", run.ScheduleType.String()),
			tag.Error(err),
		)
		tp.recordStartSlot(run, schedulerstate.SlotFailed)

		// For catchup runs: the item was already popped from the buffer by
		// Plan(). Re-insert it at the front so it retries on the next tick
		// instead of being lost until scheduler restart.
		if run.TriggerType == ir.TriggerTypeCatchUp && run.ScheduleType == ScheduleTypeStart {
			tp.reinsertCatchupItem(ctx, run)
		}
		return
	}

	// On successful catchup dispatch, advance the per-DAG watermark.
	if run.TriggerType == ir.TriggerTypeCatchUp && run.ScheduleType == ScheduleTypeStart {
		tp.advanceDAGWatermark(run.DAG.Name, run.ScheduledTime)
	}
	tp.recordStartSlot(run, schedulerstate.SlotDispatched)
	if run.ScheduleType == ScheduleTypeStart && run.Schedule.IsOneOff() {
		if tp.markOneOffConsumed(run.DAG.Name, run.Fingerprint, run.ScheduledTime) {
			tp.recomputeDAGProjection(ctx, run.DAG)
			tp.Flush(ctx)
		}
	}
}

// recordStartSlot records what came of the slot of a cron start run.
func (tp *TickPlanner) recordStartSlot(run PlannedRun, outcome schedulerstate.SlotOutcome) {
	if run.ScheduleType == ScheduleTypeStart && run.Schedule.IsCron() {
		tp.recordSlot(run.DAG.Name, run.ScheduledTime, outcome)
	}
}

func shouldPreferStartCandidate(candidate, current PlannedRun, hasCurrent bool) bool {
	if !hasCurrent {
		return true
	}
	if candidate.ScheduledTime.Before(current.ScheduledTime) {
		return true
	}
	if candidate.ScheduledTime.Equal(current.ScheduledTime) && candidate.Schedule.IsOneOff() && !current.Schedule.IsOneOff() {
		return true
	}
	return false
}
