// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// DAGChangeType identifies the kind of DAG lifecycle event.
type DAGChangeType int

const (
	DAGChangeAdded DAGChangeType = iota
	DAGChangeUpdated
	DAGChangeDeleted
)

// DAGChangeEvent represents a DAG lifecycle event emitted by the EntryReader.
type DAGChangeEvent struct {
	Type    DAGChangeType
	DAG     *core.DAG // non-nil for Added/Updated
	DAGName string    // always set (needed for delete)
}

const deletedWatermarkGrace = 2 * time.Minute

// PlannedRun represents a run that the TickPlanner has decided should be dispatched.
type PlannedRun struct {
	DAG           *core.DAG
	RunID         string
	ScheduledTime time.Time
	TriggerType   core.TriggerType
	ScheduleType  ScheduleType
	Schedule      core.Schedule
	Fingerprint   string
}

// DispatchFunc dispatches a catch-up or scheduled run for the given DAG.
type DispatchFunc func(ctx context.Context, dag *core.DAG, runID string, triggerType core.TriggerType, scheduleTime time.Time) error

// RunIDFunc generates a unique run ID.
type RunIDFunc func(ctx context.Context) (string, error)

// IsRunningFunc checks if a DAG has any active run.
type IsRunningFunc func(ctx context.Context, dag *core.DAG) (bool, error)

// GetLatestStatusFunc retrieves the latest status of a DAG.
type GetLatestStatusFunc func(ctx context.Context, dag *core.DAG) (exec.DAGRunStatus, error)

// IsSuspendedFunc checks whether a DAG is currently suspended.
type IsSuspendedFunc func(ctx context.Context, dagName string) bool

// StopFunc stops a running DAG.
type StopFunc func(ctx context.Context, dag *core.DAG) error

// RestartFunc restarts a DAG unconditionally.
type RestartFunc func(ctx context.Context, dag *core.DAG, scheduleTime time.Time) error

// EnqueueFunc enqueues a catchup run for the given DAG.
type EnqueueFunc func(ctx context.Context, dag *core.DAG, runID string, triggerType core.TriggerType, scheduleTime time.Time) error

// IsQueuedFunc checks if a DAG has any pending queued items.
type IsQueuedFunc func(ctx context.Context, dag *core.DAG) (bool, error)

// RunExistsFunc checks whether a durable dag-run record already exists.
type RunExistsFunc func(ctx context.Context, dag *core.DAG, runID string) (bool, error)

// TickPlannerConfig holds the dependencies for creating a TickPlanner.
type TickPlannerConfig struct {
	WatermarkStore  WatermarkStore
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

	// QueuesEnabled indicates whether the queue subsystem is active.
	// When false, catchup buffers are not populated.
	QueuesEnabled bool
	// Enqueue enqueues a catchup run. Nil when queues are disabled.
	Enqueue EnqueueFunc
	// IsQueued checks if a DAG has any pending queued items.
	IsQueued IsQueuedFunc
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
//   - Plan() holds entryMu during I/O calls (IsSuspended, IsRunning,
//     GetLatestStatus, GenRunID). This is intentional: the lock prevents
//     event processing during planning, ensuring a consistent snapshot of
//     entries for the entire plan cycle.
//   - lastPlanResult is accessed only from cronLoop (Plan writes, Advance reads)
//     and requires no lock. See field comment for details.
type TickPlanner struct {
	cfg TickPlannerConfig

	// watermark state (protected by mu)
	mu             sync.RWMutex
	watermarkState *SchedulerState
	watermarkDirty atomic.Bool

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
	dag *core.DAG
}

// NewTickPlanner creates a new TickPlanner with the given configuration.
// Nil config fields are replaced with no-op defaults, except RunExists which
// fails closed when it is not configured.
func NewTickPlanner(cfg TickPlannerConfig) *TickPlanner {
	if cfg.WatermarkStore == nil {
		cfg.WatermarkStore = noopWatermarkStore{}
	}
	if cfg.IsSuspended == nil {
		cfg.IsSuspended = func(context.Context, string) bool { return false }
	}
	if cfg.IsRunning == nil {
		cfg.IsRunning = func(context.Context, *core.DAG) (bool, error) { return false, nil }
	}
	if cfg.GetLatestStatus == nil {
		cfg.GetLatestStatus = func(context.Context, *core.DAG) (exec.DAGRunStatus, error) {
			return exec.DAGRunStatus{}, nil
		}
	}
	if cfg.Stop == nil {
		cfg.Stop = func(context.Context, *core.DAG) error { return nil }
	}
	if cfg.Restart == nil {
		cfg.Restart = func(context.Context, *core.DAG, time.Time) error { return nil }
	}
	if cfg.IsQueued == nil {
		cfg.IsQueued = func(context.Context, *core.DAG) (bool, error) { return false, nil }
	}
	if cfg.RunExists == nil {
		cfg.RunExists = func(context.Context, *core.DAG, string) (bool, error) {
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
		cfg.Dispatch = func(context.Context, *core.DAG, string, core.TriggerType, time.Time) error {
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

// Init loads watermark state and computes catchup buffers for existing DAGs.
func (tp *TickPlanner) Init(ctx context.Context, dags []*core.DAG) error {
	tp.entryMu.Lock()
	defer tp.entryMu.Unlock()

	// Populate entries from existing DAGs
	for _, dag := range dags {
		tp.entries[dag.Name] = &plannerEntry{dag: dag}
	}

	state, err := tp.cfg.WatermarkStore.Load(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to load watermark state", tag.Error(err))
		state = &SchedulerState{Version: SchedulerStateVersion, DAGs: make(map[string]DAGWatermark)}
	}
	if state.Version == 0 {
		state.Version = SchedulerStateVersion
	}
	if state.DAGs == nil {
		state.DAGs = make(map[string]DAGWatermark)
	}

	// Prune stale DAG entries that no longer exist on disk.
	activeDags := make(map[string]struct{}, len(dags))
	for _, dag := range dags {
		activeDags[dag.Name] = struct{}{}
	}
	pruned := 0
	for name := range state.DAGs {
		if _, ok := activeDags[name]; !ok {
			delete(state.DAGs, name)
			pruned++
		}
	}
	stateChanged := pruned > 0

	observedAt := tp.cfg.Clock().In(tp.cfg.Location)
	for _, dag := range dags {
		current := state.DAGs[dag.Name]
		next, changed := reconcileOneOffState(current, dag, observedAt)
		next, startChanged := reconcileStartScheduleState(next, dag, observedAt)
		changed = changed || startChanged
		if !changed {
			continue
		}
		stateChanged = true
		if isZeroDAGWatermark(next) {
			delete(state.DAGs, dag.Name)
			continue
		}
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
	if stateChanged {
		tp.watermarkDirty.Store(true)
	}

	logger.Info(ctx, "Loaded scheduler watermark",
		slog.Time("lastTick", state.LastTick),
		slog.Int("dagCount", len(state.DAGs)),
	)

	tp.initBuffers(ctx, dags)
	if stateChanged {
		tp.Flush(ctx)
	}
	return nil
}

// initBuffers creates per-DAG queues for DAGs with CatchupWindow > 0
// and enqueues catch-up items. Requires QueuesEnabled; when disabled,
// catchup buffers are not populated and a warning is logged per DAG.
func (tp *TickPlanner) initBuffers(ctx context.Context, dags []*core.DAG) {
	if !tp.cfg.QueuesEnabled {
		for _, dag := range dags {
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
	dagWatermarks := make(map[string]DAGWatermark, len(tp.watermarkState.DAGs))
	maps.Copy(dagWatermarks, tp.watermarkState.DAGs)
	tp.mu.RUnlock()

	now := tp.cfg.Clock()
	var totalMissed int

	for _, dag := range dags {
		if dag.CatchupWindow <= 0 {
			continue
		}

		var lastScheduledTime time.Time
		if wm, ok := dagWatermarks[dag.Name]; ok {
			lastScheduledTime = wm.LastScheduledTime
		}

		replayFrom := ComputeReplayFrom(dag.CatchupWindow, lastTick, lastScheduledTime, now)
		missed := ComputeMissedIntervals(dag.Schedule, replayFrom, now)

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

		for _, t := range missed {
			if !q.Send(QueueItem{
				DAG:           dag,
				ScheduledTime: t,
				TriggerType:   core.TriggerTypeCatchUp,
				ScheduleType:  ScheduleTypeStart,
			}) {
				logger.Error(ctx, "Catch-up buffer full, dropping remaining items",
					tag.DAG(dag.Name),
					slog.Int("buffered", q.Len()),
					slog.Int("dropped", len(missed)-q.Len()),
				)
				break
			}
		}

		if dag.OverlapPolicy == core.OverlapPolicyLatest && q.Len() > 1 {
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
	defer tp.entryMu.Unlock()

	tp.pruneExpiredDeletedWatermarks(now)

	var candidates []PlannedRun

	for dagName, entry := range tp.entries {
		// Check suspension.
		// IsSuspended is keyed by filename stem (not dag.Name), matching the
		// file-based suspension flag system in filedag/store.go.
		if isSuspendedDAG(ctx, tp.cfg.IsSuspended, nil, entry.dag) {
			tp.dropSuspendedCatchupState(dagName, entry.dag, now)
			continue
		}

		// Check catchup buffer first (catchup has priority over live)
		catchupProduced := false
		catchupDeferred := false
		if buf, ok := tp.buffers[dagName]; ok {
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
						if buf.overlapPolicy == core.OverlapPolicyLatest && buf.Len() > 1 {
							dropped := buf.DropAllButLast()
							tp.advanceDAGWatermark(dagName, dropped[len(dropped)-1].ScheduledTime)
							// Re-peek: front changed from oldest to latest
							item, _ = buf.Peek()
						}
						buf.Pop()
						run, ok := tp.createPlannedRun(ctx, item.DAG, core.Schedule{}, item.ScheduledTime, item.TriggerType)
						if ok {
							candidates = append(candidates, run)
							catchupProduced = true
						}
					} else {
						switch buf.overlapPolicy {
						case core.OverlapPolicySkip:
							popped, _ := buf.Pop()
							logger.Info(ctx, "Catch-up run skipped (overlap policy: skip)",
								tag.DAG(dagName),
							)
							tp.advanceDAGWatermark(dagName, popped.ScheduledTime)
						case core.OverlapPolicyAll:
							// leave in buffer, retry next tick
						case core.OverlapPolicyLatest:
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
		for _, schedule := range entry.dag.Schedule {
			if !schedule.IsOneOff() {
				continue
			}

			fingerprint := schedule.Fingerprint()
			if fingerprint == "" {
				continue
			}

			oneOffState, ok := tp.pendingOneOffState(entry.dag.Name, fingerprint)
			if !ok || oneOffState.ScheduledTime.After(evalTime) {
				continue
			}

			if !tp.shouldRunOneOff(ctx, entry.dag) {
				continue
			}

			run, ok := tp.createPlannedRun(ctx, entry.dag, schedule, oneOffState.ScheduledTime, core.TriggerTypeScheduler)
			if ok && shouldPreferStartCandidate(run, startCandidate, hasStartCandidate) {
				startCandidate = run
				hasStartCandidate = true
			}
		}

		// Evaluate cron schedules for live start runs.
		for _, schedule := range entry.dag.Schedule {
			if !schedule.IsCron() {
				continue
			}
			next, due := scheduleDueAt(schedule, evalTime)
			if !due {
				continue
			}
			if !tp.shouldRun(ctx, entry.dag, next, schedule) {
				continue
			}
			run, ok := tp.createPlannedRun(ctx, entry.dag, schedule, next, core.TriggerTypeScheduler)
			if ok && shouldPreferStartCandidate(run, startCandidate, hasStartCandidate) {
				startCandidate = run
				hasStartCandidate = true
			}
		}

		if hasStartCandidate {
			candidates = append(candidates, startCandidate)
		}

		// Evaluate stop schedules.
		for _, schedule := range entry.dag.StopSchedule {
			next, due := scheduleDueAt(schedule, evalTime)
			if !due {
				continue
			}

			// Guard: DAG must be running before issuing a stop
			latestStatus, err := tp.cfg.GetLatestStatus(ctx, entry.dag)
			if err != nil {
				logger.Error(ctx, "Failed to fetch DAG status for stop schedule",
					tag.DAG(dagName), tag.Error(err))
				continue
			}
			if latestStatus.Status != core.Running {
				continue
			}

			candidates = append(candidates, PlannedRun{
				DAG:           entry.dag,
				ScheduledTime: next,
				ScheduleType:  ScheduleTypeStop,
			})
		}

		// Evaluate restart schedules (no guard -- fires unconditionally).
		for _, schedule := range entry.dag.RestartSchedule {
			next, due := scheduleDueAt(schedule, evalTime)
			if !due {
				continue
			}
			candidates = append(candidates, PlannedRun{
				DAG:           entry.dag,
				ScheduledTime: next,
				ScheduleType:  ScheduleTypeRestart,
			})
		}
	}

	tp.lastPlanResult = candidates
	return candidates
}

// shouldRun checks all guards for a live scheduled run.
func (tp *TickPlanner) shouldRun(ctx context.Context, dag *core.DAG, scheduledTime time.Time, schedule core.Schedule) bool {
	// Guard 1: isRunning (uses process-level check)
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

	// Guard 1b: isQueued — prevent live run while a catchup run is queued.
	// On error, conservatively skip (assume busy) to avoid duplicates.
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

	// Also check status-based running (belt and suspenders)
	if latestStatus.Status == core.Running {
		return false
	}

	latestScheduleTime, slotState := latestScheduledSlot(latestStatus, schedule)
	switch slotState {
	case latestScheduledSlotCurrent:
		// Guard 2: alreadyFinished — exact scheduled slot already completed.
		if !latestScheduleTime.Before(scheduledTime) {
			return false
		}

		// Guard 3: skipIfSuccessful — only the current schedule's own slots may suppress.
		if dag.SkipIfSuccessful && latestStatus.Status == core.Succeeded && schedule.Parsed != nil {
			if tp.isPreEditSuccess(dag.Name, latestStatus) {
				return true
			}
			prevExecTime := computePrevExecTime(scheduledTime, schedule)
			if !latestScheduleTime.Before(prevExecTime) && latestScheduleTime.Before(scheduledTime) {
				logger.Info(ctx, "Skipping job due to successful prior run",
					tag.DAG(dag.Name),
					slog.String("schedule-time", latestScheduleTime.Format(time.RFC3339)),
				)
				return false
			}
		}

		return true
	case latestScheduledSlotStale:
		// The latest run belongs to a removed/edited slot. Do not let its runtime
		// timestamps suppress the current schedule.
		return true
	case latestScheduledSlotUnknown:
		// Fall back to runtime-based suppression when the latest run does not carry
		// a trustworthy scheduled slot identity.
	}

	// Guard 2 fallback: legacy/manual runs without an authoritative schedule slot.
	latestStartedAt, ok := latestRunReferenceTime(latestStatus)
	if ok {
		if !latestStartedAt.Before(scheduledTime) {
			return false
		}

		// Guard 3 fallback: preserve manual-run semantics when no slot identity exists.
		if dag.SkipIfSuccessful && latestStatus.Status == core.Succeeded && schedule.Parsed != nil {
			if tp.isPreEditSuccess(dag.Name, latestStatus) {
				return true
			}
			prevExecTime := computePrevExecTime(scheduledTime, schedule)
			if !latestStartedAt.Before(prevExecTime) && latestStartedAt.Before(scheduledTime) {
				logger.Info(ctx, "Skipping job due to successful prior run",
					tag.DAG(dag.Name),
					slog.String("start-time", latestStartedAt.Format(time.RFC3339)),
				)
				return false
			}
		}
	}

	return true
}

func latestRunReferenceTime(status exec.DAGRunStatus) (time.Time, bool) {
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

func latestSuccessReferenceTime(status exec.DAGRunStatus) (time.Time, bool) {
	if finishedAt, err := stringutil.ParseTime(status.FinishedAt); err == nil && !finishedAt.IsZero() {
		return finishedAt, true
	}
	if startedAt, err := stringutil.ParseTime(status.StartedAt); err == nil && !startedAt.IsZero() {
		return startedAt, true
	}
	return time.Time{}, false
}

func latestScheduledSlot(status exec.DAGRunStatus, schedule core.Schedule) (time.Time, latestScheduledSlotState) {
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

func scheduleMatchesFireTime(schedule core.Schedule, scheduledTime time.Time) bool {
	next, due := scheduleDueAt(schedule, scheduledTime)
	return due && next.Equal(scheduledTime)
}

func (tp *TickPlanner) isPreEditSuccess(dagName string, status exec.DAGRunStatus) bool {
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

func (tp *TickPlanner) shouldRunOneOff(ctx context.Context, dag *core.DAG) bool {
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

	return latestStatus.Status != core.Running
}

func (tp *TickPlanner) pendingOneOffState(dagName, fingerprint string) (OneOffScheduleState, bool) {
	tp.mu.RLock()
	defer tp.mu.RUnlock()

	dagState, ok := tp.watermarkState.DAGs[dagName]
	if !ok || dagState.OneOffs == nil {
		return OneOffScheduleState{}, false
	}
	oneOff, ok := dagState.OneOffs[fingerprint]
	if !ok || oneOff.Status != OneOffStatusPending {
		return OneOffScheduleState{}, false
	}
	return oneOff, true
}

// computePrevExecTime calculates the previous schedule fire time before next.
// It walks forward from (next - 32 days) to find the last fire time before next.
// 32 days covers monthly schedules, the most common sparse cron interval.
// This correctly handles non-uniform cron schedules (e.g., "0 9,17 * * *").
func computePrevExecTime(next time.Time, schedule core.Schedule) time.Time {
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
func scheduleDueAt(schedule core.Schedule, now time.Time) (time.Time, bool) {
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
func (tp *TickPlanner) createPlannedRun(ctx context.Context, dag *core.DAG, schedule core.Schedule, scheduledTime time.Time, triggerType core.TriggerType) (PlannedRun, bool) {
	var runID string
	if triggerType == core.TriggerTypeCatchUp {
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
		DAG:           dag,
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
		if run.TriggerType == core.TriggerTypeCatchUp {
			continue // watermark updated in DispatchRun on success
		}
		if !run.Schedule.IsCron() {
			continue
		}
		dagState := cloneDAGWatermark(tp.watermarkState.DAGs[run.DAG.Name])
		dagState.LastScheduledTime = run.ScheduledTime
		tp.watermarkState.DAGs[run.DAG.Name] = dagState
	}

	tp.watermarkDirty.Store(true)
	tp.lastPlanResult = nil
}

func (tp *TickPlanner) dropSuspendedCatchupState(dagName string, dag *core.DAG, now time.Time) {
	if _, ok := tp.buffers[dagName]; ok {
		delete(tp.buffers, dagName)
		tp.advanceDAGWatermark(dagName, now)
		return
	}
	if dag != nil && dag.CatchupWindow > 0 {
		tp.advanceDAGWatermark(dagName, now)
	}
}

// advanceDAGWatermark updates the per-DAG watermark to the given time
// and marks the state as dirty. Caller must NOT hold tp.mu.
func (tp *TickPlanner) advanceDAGWatermark(dagName string, scheduledTime time.Time) bool {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	dagState := cloneDAGWatermark(tp.watermarkState.DAGs[dagName])
	if dagState.LastScheduledTime.Equal(scheduledTime) {
		return false
	}
	dagState.LastScheduledTime = scheduledTime
	tp.watermarkState.DAGs[dagName] = dagState
	tp.watermarkDirty.Store(true)
	return true
}

func (tp *TickPlanner) markOneOffConsumed(dagName, fingerprint string, scheduledTime time.Time) bool {
	if fingerprint == "" {
		return false
	}

	tp.mu.Lock()
	defer tp.mu.Unlock()

	dagState := cloneDAGWatermark(tp.watermarkState.DAGs[dagName])
	if dagState.OneOffs == nil {
		dagState.OneOffs = make(map[string]OneOffScheduleState)
	}

	state := dagState.OneOffs[fingerprint]
	if state.ScheduledTime.IsZero() {
		state.ScheduledTime = scheduledTime
	}
	if state.Status == OneOffStatusConsumed {
		return false
	}

	state.Status = OneOffStatusConsumed
	dagState.OneOffs[fingerprint] = state
	tp.watermarkState.DAGs[dagName] = dagState
	tp.watermarkDirty.Store(true)
	return true
}

// Flush writes the watermark state to disk if dirty.
// Safe for concurrent use.
func (tp *TickPlanner) Flush(ctx context.Context) {
	if !tp.watermarkDirty.CompareAndSwap(true, false) {
		return
	}

	// Snapshot under read lock to avoid holding the lock during I/O.
	tp.mu.RLock()
	snapshot := &SchedulerState{
		Version:  tp.watermarkState.Version,
		LastTick: tp.watermarkState.LastTick,
		DAGs:     make(map[string]DAGWatermark, len(tp.watermarkState.DAGs)),
	}
	for dagName, dagState := range tp.watermarkState.DAGs {
		snapshot.DAGs[dagName] = cloneDAGWatermark(dagState)
	}
	tp.mu.RUnlock()

	if err := tp.cfg.WatermarkStore.Save(ctx, snapshot); err != nil {
		logger.Error(ctx, "Failed to flush watermark state", tag.Error(err))
		tp.watermarkDirty.Store(true)
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
		delete(tp.deletedGrace, event.DAGName)
		tp.entries[event.DAGName] = &plannerEntry{dag: event.DAG}
		// Set watermark to now (new DAGs have no catchup)
		flushNow = tp.advanceDAGWatermark(event.DAGName, tp.cfg.Clock())
		flushNow = tp.reconcileStartScheduleState(event.DAG) || flushNow
		flushNow = tp.reconcileOneOffSchedules(event.DAG) || flushNow
		logger.Info(ctx, "Planner: DAG added", tag.DAG(event.DAGName))

	case DAGChangeUpdated:
		if event.DAG == nil {
			return
		}
		delete(tp.deletedGrace, event.DAGName)
		tp.entries[event.DAGName] = &plannerEntry{dag: event.DAG}
		// Remove existing buffer and recompute if catchupWindow > 0
		delete(tp.buffers, event.DAGName)
		if event.DAG.CatchupWindow > 0 {
			flushNow = tp.recomputeBuffer(ctx, event.DAG)
		}
		flushNow = tp.reconcileStartScheduleState(event.DAG) || flushNow
		flushNow = tp.reconcileOneOffSchedules(event.DAG) || flushNow
		logger.Info(ctx, "Planner: DAG updated", tag.DAG(event.DAGName))

	case DAGChangeDeleted:
		delete(tp.entries, event.DAGName)
		delete(tp.buffers, event.DAGName)
		if tp.hasDAGWatermark(event.DAGName) {
			tp.deletedGrace[event.DAGName] = tp.cfg.Clock().Add(deletedWatermarkGrace)
		}
		// Preserve watermark state briefly across delete+add rewrite cycles so
		// the re-added DAG can detect schedule edits before its next slot.
		logger.Info(ctx, "Planner: DAG deleted", tag.DAG(event.DAGName))
	}

	if flushNow {
		tp.Flush(ctx)
	}
}

func (tp *TickPlanner) reconcileOneOffSchedules(dag *core.DAG) bool {
	if dag == nil {
		return false
	}

	tp.mu.Lock()
	defer tp.mu.Unlock()

	current := tp.watermarkState.DAGs[dag.Name]
	next, changed := reconcileOneOffState(current, dag, tp.cfg.Clock())
	if !changed {
		return false
	}

	if isZeroDAGWatermark(next) {
		delete(tp.watermarkState.DAGs, dag.Name)
	} else {
		tp.watermarkState.DAGs[dag.Name] = next
	}
	tp.watermarkDirty.Store(true)
	return true
}

func (tp *TickPlanner) reconcileStartScheduleState(dag *core.DAG) bool {
	if dag == nil {
		return false
	}

	tp.mu.Lock()
	defer tp.mu.Unlock()

	current := tp.watermarkState.DAGs[dag.Name]
	next, changed := reconcileStartScheduleState(current, dag, tp.cfg.Clock())
	if !changed {
		return false
	}

	if isZeroDAGWatermark(next) {
		delete(tp.watermarkState.DAGs, dag.Name)
	} else {
		tp.watermarkState.DAGs[dag.Name] = next
	}
	tp.watermarkDirty.Store(true)
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

func (tp *TickPlanner) pruneExpiredDeletedWatermarks(now time.Time) {
	if len(tp.deletedGrace) == 0 {
		return
	}

	tp.mu.Lock()
	defer tp.mu.Unlock()

	if tp.watermarkState == nil || tp.watermarkState.DAGs == nil {
		return
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

	if changed {
		tp.watermarkDirty.Store(true)
	}
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
		DAG:           run.DAG,
		ScheduledTime: run.ScheduledTime,
		TriggerType:   run.TriggerType,
		ScheduleType:  run.ScheduleType,
	}) {
		logger.Error(ctx, "Failed to re-insert catchup item; buffer full",
			tag.DAG(run.DAG.Name),
		)
	}
}

// recomputeBuffer creates a new catch-up buffer for a DAG using the existing watermark.
func (tp *TickPlanner) recomputeBuffer(ctx context.Context, dag *core.DAG) bool {
	if !tp.cfg.QueuesEnabled {
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
	missed := ComputeMissedIntervals(dag.Schedule, replayFrom, now)

	if len(missed) == 0 {
		return false
	}

	watermarkAdvanced := false
	q := NewScheduleBuffer(dag.Name, dag.OverlapPolicy)
	for _, t := range missed {
		if !q.Send(QueueItem{
			DAG:           dag,
			ScheduledTime: t,
			TriggerType:   core.TriggerTypeCatchUp,
			ScheduleType:  ScheduleTypeStart,
		}) {
			break
		}
	}

	if dag.OverlapPolicy == core.OverlapPolicyLatest && q.Len() > 1 {
		dropped := q.DropAllButLast()
		watermarkAdvanced = tp.advanceDAGWatermark(dag.Name, dropped[len(dropped)-1].ScheduledTime)
	}

	tp.buffers[dag.Name] = q

	logger.Info(ctx, "Recomputed catch-up buffer",
		tag.DAG(dag.Name),
		slog.Int("missedCount", len(missed)),
	)
	return watermarkAdvanced
}

// DispatchRun dispatches a PlannedRun using the configured dispatch functions.
func (tp *TickPlanner) DispatchRun(ctx context.Context, run PlannedRun) {
	logger.Info(ctx, "Dispatching planned run",
		tag.DAG(run.DAG.Name),
		slog.String("scheduleType", run.ScheduleType.String()),
		slog.String("scheduledTime", run.ScheduledTime.Format(time.RFC3339)),
	)

	if run.ScheduleType == ScheduleTypeStart &&
		isSchedulerManagedTriggerType(run.TriggerType) &&
		isSuspendedDAG(ctx, tp.cfg.IsSuspended, nil, run.DAG) {
		logger.Info(ctx, "Skipping suspended scheduler-managed run dispatch",
			tag.DAG(run.DAG.Name),
			slog.String("trigger_type", run.TriggerType.String()),
		)
		if run.TriggerType == core.TriggerTypeCatchUp {
			tp.advanceDAGWatermark(run.DAG.Name, run.ScheduledTime)
		}
		return
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
		} else if exists {
			if tp.markOneOffConsumed(run.DAG.Name, run.Fingerprint, run.ScheduledTime) {
				tp.Flush(ctx)
			}
			return
		}
	}

	var err error
	switch run.ScheduleType {
	case ScheduleTypeStart:
		if run.TriggerType == core.TriggerTypeCatchUp {
			if tp.cfg.Enqueue == nil {
				logger.Error(ctx, "Catchup dispatch requires queues to be enabled; skipping",
					tag.DAG(run.DAG.Name),
				)
				return
			}
			err = tp.cfg.Enqueue(ctx, run.DAG, run.RunID, run.TriggerType, run.ScheduledTime)
		} else {
			err = tp.cfg.Dispatch(ctx, run.DAG, run.RunID, run.TriggerType, run.ScheduledTime)
		}
	case ScheduleTypeStop:
		err = tp.cfg.Stop(ctx, run.DAG)
	case ScheduleTypeRestart:
		err = tp.cfg.Restart(ctx, run.DAG, run.ScheduledTime)
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
			} else if exists {
				if tp.markOneOffConsumed(run.DAG.Name, run.Fingerprint, run.ScheduledTime) {
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

		// For catchup runs: the item was already popped from the buffer by
		// Plan(). Re-insert it at the front so it retries on the next tick
		// instead of being lost until scheduler restart.
		if run.TriggerType == core.TriggerTypeCatchUp && run.ScheduleType == ScheduleTypeStart {
			tp.reinsertCatchupItem(ctx, run)
		}
		return
	}

	// On successful catchup dispatch, advance the per-DAG watermark.
	if run.TriggerType == core.TriggerTypeCatchUp && run.ScheduleType == ScheduleTypeStart {
		tp.advanceDAGWatermark(run.DAG.Name, run.ScheduledTime)
	}
	if run.ScheduleType == ScheduleTypeStart && run.Schedule.IsOneOff() {
		if tp.markOneOffConsumed(run.DAG.Name, run.Fingerprint, run.ScheduledTime) {
			tp.Flush(ctx)
		}
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
