// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// liveRunChecker reports whether a run counts for a DAG's live schedule. A
// backfill run does not: a live slot is queued ahead of it instead of
// skipped. A run whose status cannot be found yet counts.
func liveRunChecker(repository *persis.DAGRunRepository) func(context.Context, ir.DAGRunRef) (bool, error) {
	return func(ctx context.Context, ref ir.DAGRunRef) (bool, error) {
		attempt, err := repository.FindAttempt(ctx, ref)
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, dagrun.ErrNoStatusData) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		status, err := attempt.ReadStatus(ctx)
		if err != nil {
			return false, err
		}
		return status.BackfillID == "", nil
	}
}

// liveQueuedChecker reports whether a DAG has a queued run that counts for
// its live schedule.
func liveQueuedChecker(queueStore queuedomain.QueueStore, live func(context.Context, ir.DAGRunRef) (bool, error)) IsQueuedFunc {
	return func(ctx context.Context, dag *ir.DAG) (bool, error) {
		items, err := queueStore.ListByDAGName(ctx, dag.ProcGroup(), dag.Name)
		if err != nil {
			return false, err
		}
		for _, item := range items {
			ref, err := item.Data()
			if err != nil {
				return true, nil
			}
			if counts, err := live(ctx, *ref); err != nil || counts {
				return true, err
			}
		}
		return false, nil
	}
}

// liveRunningChecker reports whether a DAG has a running run that counts
// for its live schedule.
func liveRunningChecker(procRepository processRepository, live func(context.Context, ir.DAGRunRef) (bool, error)) IsRunningFunc {
	return func(ctx context.Context, dag *ir.DAG) (bool, error) {
		count, err := procRepository.CountAliveByDAGName(ctx, dag.ProcGroup(), dag.Name)
		if err != nil || count == 0 {
			return false, err
		}
		entries, err := procRepository.ListAllEntries(ctx)
		if err != nil {
			return false, err
		}
		for _, entry := range entries {
			if !entry.Fresh || entry.GroupName != dag.ProcGroup() || entry.Meta.Name != dag.Name {
				continue
			}
			if counts, err := live(ctx, entry.Meta.DAGRun()); err != nil || counts {
				return true, err
			}
		}
		return false, nil
	}
}

// backfillDispatchInterval is how often the active scheduler advances open
// backfills, so the next slot starts soon after the one before it ends.
const backfillDispatchInterval = 10 * time.Second

// newBackfillDispatcher returns the dispatcher of the scheduler's backfills,
// or nil when there is no backfill store.
func (s *Scheduler) newBackfillDispatcher(store backfill.Store) *backfill.Dispatcher {
	if store == nil {
		return nil
	}
	return &backfill.Dispatcher{
		Store:   store,
		History: s.dagRunRepository,
		ReadRun: func(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error) {
			attempt, err := s.dagRunRepository.FindAttempt(ctx, ref)
			if err != nil {
				return nil, err
			}
			return attempt.ReadStatus(ctx)
		},
		Target:    s.backfillTarget,
		Enqueue:   s.enqueueBackfillRun,
		DAGExists: s.backfillDAGExists,
		Now:       s.clock,
	}
}

// backfillDAGExists reports whether the file a backfill's DAG was submitted
// from still defines that DAG. A delete, a rename of the file, or a rename
// of the DAG inside it ends that.
func (s *Scheduler) backfillDAGExists(ctx context.Context, record *backfill.Record) (bool, error) {
	path := record.DAGFile
	if path == "" {
		return true, nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.config.Paths.DAGsDir, path)
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	dag, err := spec.Load(ctx, path, spec.OnlyMetadata(), spec.WithoutEval())
	if err != nil {
		// A file that does not load may be mid-edit, which says nothing
		// about the DAG it defines.
		return true, nil
	}
	return dag.Name == record.DAGName, nil
}

// guardCatchupSlot runs the enqueue of a catchup slot unless a backfill
// holds the slot. It holds the lock under which the dispatcher queues the
// DAG's backfill runs, so catchup and a backfill never both run a slot.
func (s *Scheduler) guardCatchupSlot(ctx context.Context, entry DAGEntry, slot time.Time, enqueue func() error) (bool, error) {
	dag := entry.DAG
	profile, err := s.dagExecutor.defaultProfileName(ctx, entry.DefinitionID, dag)
	if err != nil {
		return false, fmt.Errorf("resolve the DAG's profile: %w", err)
	}
	unlock, err := s.backfills.Store.LockDAG(ctx, dag.Name)
	if err != nil {
		return false, err
	}
	defer unlock()
	sched := backfill.SlotSchedule{Schedules: dag.Schedule, Profile: profile, Location: s.config.Core.Location}
	history, err := backfill.LoadHistory(ctx, s.dagRunRepository, dag, sched, slot, slot)
	if err != nil {
		return false, err
	}
	if backfill.HeldByBackfill(slot, sched, history) {
		return true, nil
	}
	return false, enqueue()
}

// backfillTarget loads a backfill's DAG from the definition pinned at submit.
func (s *Scheduler) backfillTarget(ctx context.Context, record *backfill.Record) (backfill.Target, error) {
	opts := []spec.LoadOption{
		spec.WithoutEval(),
		spec.WithName(record.DAGName),
		spec.WithDAGsDir(s.config.Paths.DAGsDir),
		spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(s.config.Paths.DAGsDir)),
		spec.WithCalendarsDir(s.config.Paths.CalendarsDir),
	}
	if s.config.Paths.BaseConfig != "" {
		opts = append(opts, spec.WithBaseConfig(s.config.Paths.BaseConfig))
	}
	dag, err := spec.LoadYAML(ctx, record.Definition, opts...)
	if err != nil {
		return backfill.Target{}, fmt.Errorf("load the pinned definition: %w", err)
	}
	return backfill.Target{
		DAG: dag,
		Schedule: backfill.SlotSchedule{
			Schedules: dag.Schedule,
			Profile:   record.Options.Profile,
			Location:  s.config.Core.Location,
		},
	}, nil
}

// enqueueBackfillRun queues the run of one slot with the backfill's params,
// profile, and identity.
func (s *Scheduler) enqueueBackfillRun(ctx context.Context, record *backfill.Record, target backfill.Target, slot backfill.RecordSlot, runID string) error {
	var params any
	if record.Options.Params != "" {
		params = record.Options.Params
	} else {
		loc := target.Schedule.Location
		if loc == nil {
			loc = time.Local
		}
		for _, schedule := range target.Schedule.Schedules {
			if schedule.IsCron() && scheduleMatchesProfile(schedule, target.Schedule.Profile) &&
				scheduleMatchesFireTime(schedule, slot.At.In(loc)) {
				params = string(schedule.Params)
				break
			}
		}
	}
	dag, err := rehydrateExecutionDAG(ctx, target.DAG, params, s.config.Paths.BaseConfig, workspace.BaseConfigDir(s.config.Paths.DAGsDir))
	if err != nil {
		return err
	}
	_, err = intake.EnqueueRun(ctx, intake.QueueRequest{
		DAGRunRepository: s.dagRunRepository,
		QueueStore:       s.queueStore,
		DAG:              dag,
		DAGRunID:         runID,
		LogBaseDir:       s.config.Paths.LogDir,
		ArtifactBaseDir:  s.config.Paths.ArtifactDir,
		TriggerType:      ir.TriggerTypeBackfill,
		TriggerActor:     record.Actor,
		ScheduleTime:     stringutil.FormatTime(slot.At),
		ScheduleWindow:   slot.Window,
		ProfileName:      record.Options.Profile,
		DefinitionID:     s.definitionID(record.DAGName),
		BackfillID:       record.ID,
	})
	if err != nil {
		return err
	}
	logger.Info(ctx, "Backfill run enqueued", tag.DAG(record.DAGName), tag.RunID(runID))
	return nil
}

// definitionID returns the definition ID of a DAG the scheduler knows by
// name, or empty.
func (s *Scheduler) definitionID(dagName string) string {
	if s.entryReader == nil {
		return ""
	}
	for _, entry := range s.entryReader.Entries() {
		if entry.DAG != nil && entry.DAG.Name == dagName {
			return entry.DefinitionID
		}
	}
	return ""
}

// startBackfillDispatcher advances open backfills until the scheduler
// stops. It dispatches nothing while the lease is lost or the scheduler is
// paused.
func (s *Scheduler) startBackfillDispatcher(ctx context.Context) {
	if s.backfills == nil {
		return
	}
	ticker := time.NewTicker(backfillDispatchInterval)
	defer ticker.Stop()
	for {
		s.advanceBackfills(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.quit:
			return
		case <-ticker.C:
		}
	}
}

func (s *Scheduler) advanceBackfills(ctx context.Context) {
	if s.leaseExpired(ctx) {
		return
	}
	paused, err := newPauseChecker(s.pauseStore)(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to read the scheduler pause", tag.Error(err))
		return
	}
	if paused {
		return
	}
	if err := s.backfills.Tick(ctx); err != nil {
		logger.Error(ctx, "Failed to advance backfills", tag.Error(err))
	}
}
