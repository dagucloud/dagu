// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/procutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
)

// panicToError converts a panic value to an error, including stack trace.
func panicToError(r any) error {
	stack := string(debug.Stack())
	if err, ok := r.(error); ok {
		return fmt.Errorf("panic: %w\n%s", err, stack)
	}
	return fmt.Errorf("panic: %v\n%s", r, stack)
}

// ZombieDetector finds and cleans up zombie DAG runs
type ZombieDetector struct {
	dagRunStore      exec.DAGRunStore
	procStore        exec.ProcStore
	interval         time.Duration
	failureThreshold int
	staleCounters    map[string]int // attempt identity -> consecutive stale count
	runMutexesMu     sync.Mutex
	runMutexes       map[string]*sync.Mutex
	quit             chan struct{}
	closeOnce        sync.Once
}

// NewZombieDetector creates a new zombie detector
func NewZombieDetector(
	dagRunStore exec.DAGRunStore,
	procStore exec.ProcStore,
	interval time.Duration,
	failureThreshold int,
) *ZombieDetector {
	if interval <= 0 {
		interval = 45 * time.Second
	}
	if failureThreshold <= 0 {
		failureThreshold = 3
	}
	return &ZombieDetector{
		dagRunStore:      dagRunStore,
		procStore:        procStore,
		interval:         interval,
		failureThreshold: failureThreshold,
		staleCounters:    make(map[string]int),
		runMutexes:       make(map[string]*sync.Mutex),
		quit:             make(chan struct{}),
	}
}

// Start begins the zombie detection loop
func (z *ZombieDetector) Start(ctx context.Context) {
	ticker := time.NewTicker(z.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error(ctx, "Zombie detection check panicked", tag.Error(panicToError(r)))
					}
				}()
				z.detectAndCleanZombies(ctx)
			}()

		case <-z.quit:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (z *ZombieDetector) Stop(ctx context.Context) {
	logger.Info(ctx, "Stopping zombie detector")
	z.closeOnce.Do(func() {
		close(z.quit)
	})
}

// getRunMutex returns or creates a per-attempt mutex for serializing repair access.
func (z *ZombieDetector) getRunMutex(attemptKey string) *sync.Mutex {
	z.runMutexesMu.Lock()
	defer z.runMutexesMu.Unlock()

	if mu, ok := z.runMutexes[attemptKey]; ok {
		return mu
	}

	mu := &sync.Mutex{}
	z.runMutexes[attemptKey] = mu
	return mu
}

func (z *ZombieDetector) clearAttemptState(attemptKey string) {
	delete(z.staleCounters, attemptKey)
}

func (z *ZombieDetector) findAttempt(ctx context.Context, entry exec.ProcEntry) (exec.DAGRunAttempt, error) {
	if entry.IsRoot() {
		return z.dagRunStore.FindAttempt(ctx, entry.Meta.DAGRun())
	}
	return z.dagRunStore.FindSubAttempt(ctx, entry.Meta.Root(), entry.Meta.DAGRunID)
}

// detectAndCleanZombies finds stale proc entries and repairs only the matching persisted attempt.
func (z *ZombieDetector) detectAndCleanZombies(ctx context.Context) {
	entries, err := z.procStore.ListAllEntries(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list proc entries", tag.Error(err))
		return
	}

	logger.Debug(ctx, "Checking proc entries for zombie DAG runs", tag.Count(len(entries)))

	freshByRunScope := make(map[string]exec.ProcEntry)
	activeAttemptKeys := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		activeAttemptKeys[entry.AttemptKey()] = struct{}{}
		if !entry.Fresh {
			continue
		}
		scopeKey := entry.RunScopeKey()
		current, ok := freshByRunScope[scopeKey]
		if !ok || current.Meta.StartedAt < entry.Meta.StartedAt ||
			(current.Meta.StartedAt == entry.Meta.StartedAt && current.LastHeartbeatAt < entry.LastHeartbeatAt) {
			freshByRunScope[scopeKey] = entry
		}
	}

	for _, entry := range entries {
		// Check for quit signal
		select {
		case <-ctx.Done():
			return
		case <-z.quit:
			return
		default:
		}

		if err := z.checkAndCleanZombie(ctx, entry, freshByRunScope); err != nil {
			logger.Error(ctx, "Failed to check zombie status",
				tag.Name(entry.Meta.Name),
				tag.RunID(entry.Meta.DAGRunID),
				tag.AttemptID(entry.Meta.AttemptID),
				tag.Error(err))
		}
	}

	for id := range z.staleCounters {
		if _, ok := activeAttemptKeys[id]; !ok {
			delete(z.staleCounters, id)
		}
	}

	z.runMutexesMu.Lock()
	for id := range z.runMutexes {
		if _, ok := activeAttemptKeys[id]; !ok {
			delete(z.runMutexes, id)
		}
	}
	z.runMutexesMu.Unlock()
}

// checkAndCleanZombie checks if a single stale proc entry is a zombie candidate and cleans it up.
func (z *ZombieDetector) checkAndCleanZombie(ctx context.Context, entry exec.ProcEntry, freshByRunScope map[string]exec.ProcEntry) error {
	attemptKey := entry.AttemptKey()
	ctx = logger.WithValues(ctx,
		tag.DAG(entry.Meta.Name),
		tag.RunID(entry.Meta.DAGRunID),
		tag.AttemptID(entry.Meta.AttemptID),
		tag.Queue(entry.GroupName),
	)

	if entry.Fresh {
		z.clearAttemptState(attemptKey)
		return nil
	}

	if sibling, ok := freshByRunScope[entry.RunScopeKey()]; ok && sibling.Meta.AttemptID != entry.Meta.AttemptID {
		z.clearAttemptState(attemptKey)
		if err := z.procStore.RemoveIfStale(ctx, entry); err != nil {
			return fmt.Errorf("remove stale proc with fresh sibling: %w", err)
		}
		return nil
	}

	z.staleCounters[attemptKey]++
	count := z.staleCounters[attemptKey]

	if count < z.failureThreshold {
		logger.Warn(ctx, "Proc entry appears stale, waiting for threshold",
			slog.Int("stale_count", count),
			slog.Int("threshold", z.failureThreshold),
		)
		return nil
	}

	runMu := z.getRunMutex(attemptKey)
	runMu.Lock()
	defer runMu.Unlock()

	attempt, err := z.findAttempt(ctx, entry)
	if err != nil {
		return z.cleanupOrphanedStaleEntry(ctx, entry, attemptKey, err)
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("read status: %w", err)
	}
	if status.AttemptID != entry.Meta.AttemptID || status.Status != core.Running {
		z.clearAttemptState(attemptKey)
		if err := z.procStore.RemoveIfStale(ctx, entry); err != nil {
			return fmt.Errorf("remove mismatched stale proc: %w", err)
		}
		return nil
	}

	if status.WorkerID != "" && status.WorkerID != "local" {
		z.clearAttemptState(attemptKey)
		if err := z.procStore.RemoveIfStale(ctx, entry); err != nil {
			return fmt.Errorf("remove remote stale proc: %w", err)
		}
		return nil
	}

	if status.PID > 0 && status.PIDStartedAt > 0 {
		matched, actualStartedAt, ok := procutil.MatchesStartTime(int(status.PID), status.PIDStartedAt)
		if matched {
			logger.Warn(ctx, "Skipping zombie repair because local process identity is still alive despite stale proc heartbeat",
				tag.PID(int(status.PID)),
				slog.Int("stale_count", count),
			)
			return nil
		}
		if ok {
			logger.Debug(ctx, "Not skipping zombie repair because PID start time does not match persisted run status",
				tag.PID(int(status.PID)),
				slog.Int64("expected-pid-started-at", status.PIDStartedAt),
				slog.Int64("actual-pid-started-at", actualStartedAt),
			)
		}
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return fmt.Errorf("read dag: %w", err)
	}

	repairedStatus, repaired, err := runtime.RepairStaleLocalRun(ctx, attempt, dag)
	if err != nil {
		return fmt.Errorf("repair stale local run: %w", err)
	}
	if !repaired {
		logger.Info(ctx, "Run already terminal, removing stale proc entry",
			slog.String("status", repairedStatus.Status.String()),
		)
	} else {
		logger.Info(ctx, "Confirmed zombie DAG run, updated to failed status",
			slog.Int("stale_count", count),
		)
	}

	if err := z.procStore.RemoveIfStale(ctx, entry); err != nil {
		return fmt.Errorf("remove stale proc after repair: %w", err)
	}
	z.clearAttemptState(attemptKey)

	return nil
}

func (z *ZombieDetector) cleanupOrphanedStaleEntry(ctx context.Context, entry exec.ProcEntry, attemptKey string, findErr error) error {
	if !errors.Is(findErr, exec.ErrDAGRunIDNotFound) &&
		!errors.Is(findErr, exec.ErrNoStatusData) &&
		!errors.Is(findErr, exec.ErrCorruptedStatusFile) {
		return fmt.Errorf("find attempt: %w", findErr)
	}

	if errors.Is(findErr, exec.ErrCorruptedStatusFile) {
		logger.Warn(ctx, "Removing orphaned stale proc entry with corrupted persisted DAG run state", tag.Error(findErr))
	} else {
		logger.Info(ctx, "Removing orphaned stale proc entry with missing persisted DAG run state", tag.Error(findErr))
	}
	// A corrupted or missing status snapshot cannot be used for recovery, so the
	// stale proc entry must be dropped to stop reporting the run as active.
	z.clearAttemptState(attemptKey)
	if err := z.procStore.RemoveIfStale(ctx, entry); err != nil {
		return fmt.Errorf("remove orphaned stale proc: %w", err)
	}
	return nil
}
