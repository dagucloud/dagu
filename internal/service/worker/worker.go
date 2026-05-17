// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/builtin/sql"
	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/healthcheck"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

// Worker represents a worker instance that polls for tasks from the coordinator.
type Worker struct {
	id             string
	maxActiveRuns  int
	coordinatorCli coordinator.Client
	handler        TaskHandler
	labels         map[string]string
	cfg            *config.Config

	// For tracking poller states and heartbeats
	pollersMu    sync.Mutex
	runningTasks map[string]*runningTaskState // attemptKey -> running task
	pollerTasks  map[string]string            // pollerID -> attemptKey

	// For cancellation support (key is AttemptKey)
	cancelFuncs map[string]context.CancelFunc

	// For graceful shutdown
	stopOnce   sync.Once
	stopCancel context.CancelFunc // Cancels the worker's internal context
	stopDone   chan struct{}      // Signals when all goroutines have stopped

	// For global PostgreSQL connection pool (shared-nothing mode)
	poolManager  *sql.GlobalPoolManager
	healthServer *healthcheck.Server

	afterTaskAckHook func(context.Context, *coordinatorv1.Task) bool
}

type runningTaskState struct {
	task                 *coordinatorv1.RunningTask
	owner                exec.HostInfo
	lastOwnerHeartbeatAt time.Time
}

var errTaskClaimRejectedBeforeExecution = errors.New("task claim rejected before execution")

const (
	ownerRunHeartbeatCallTimeout = 10 * time.Second
	ownerHeartbeatTimeout        = exec.DefaultStaleLeaseThreshold
)

// SetHandler sets a custom task executor for testing or custom execution logic
func (w *Worker) SetHandler(executor TaskHandler) {
	w.handler = executor
}

// SetAfterTaskAckHook installs a hook that runs after a task claim has been
// acknowledged but before the worker registers or executes the task. Returning
// true abandons execution for that claimed task. This is intended for tests.
func (w *Worker) SetAfterTaskAckHook(hook func(context.Context, *coordinatorv1.Task) bool) {
	w.pollersMu.Lock()
	defer w.pollersMu.Unlock()
	w.afterTaskAckHook = hook
}

// NewWorker creates a new worker instance.
func NewWorker(workerID string, maxActiveRuns int, coordinatorClient coordinator.Client, labels map[string]string, cfg *config.Config) *Worker {
	// Generate default worker ID if not provided
	if workerID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown"
		}
		workerID = fmt.Sprintf("%s@%d", hostname, os.Getpid())
	}

	healthPort := 0
	if cfg != nil {
		healthPort = cfg.Worker.HealthPort
	}

	var bundleClient workspacebundle.Client
	if client, ok := coordinatorClient.(workspacebundle.Client); ok {
		bundleClient = client
	}

	return &Worker{
		id:             workerID,
		maxActiveRuns:  maxActiveRuns,
		coordinatorCli: coordinatorClient,
		handler:        NewTaskHandler(cfg, bundleClient),
		labels:         labels,
		cfg:            cfg,
		runningTasks:   make(map[string]*runningTaskState),
		pollerTasks:    make(map[string]string),
		cancelFuncs:    make(map[string]context.CancelFunc),
		healthServer:   healthcheck.NewServer("worker", healthPort),
	}
}

// Start begins the worker's operation, launching multiple polling goroutines.
func (w *Worker) Start(ctx context.Context) (err error) {
	logger.Info(ctx, "Starting worker",
		tag.WorkerID(w.id),
		tag.MaxConcurrency(w.maxActiveRuns))

	// Create an internal context that can be cancelled by Stop()
	// This context is cancelled when either the parent context is done OR Stop() is called
	internalCtx, cancel := context.WithCancel(ctx)
	w.stopCancel = cancel
	w.stopDone = make(chan struct{})

	// Initialize global PostgreSQL pool manager if in shared-nothing mode
	if w.isSharedNothingMode() {
		w.poolManager = sql.NewGlobalPoolManager(sql.GlobalPoolConfig{
			MaxOpenConns:    w.cfg.Worker.PostgresPool.MaxOpenConns,
			MaxIdleConns:    w.cfg.Worker.PostgresPool.MaxIdleConns,
			ConnMaxLifetime: time.Duration(w.cfg.Worker.PostgresPool.ConnMaxLifetime) * time.Second,
			ConnMaxIdleTime: time.Duration(w.cfg.Worker.PostgresPool.ConnMaxIdleTime) * time.Second,
		})
		internalCtx = sql.WithPoolManager(internalCtx, w.poolManager)
		logger.Info(ctx, "Global PostgreSQL pool manager initialized",
			tag.WorkerID(w.id),
			slog.Int("maxOpenConns", w.cfg.Worker.PostgresPool.MaxOpenConns),
			slog.Int("maxIdleConns", w.cfg.Worker.PostgresPool.MaxIdleConns))
	}

	defer func() {
		if err == nil {
			return
		}

		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()

		if w.healthServer != nil {
			_ = w.healthServer.Stop(cleanupCtx)
		}
		if w.poolManager != nil {
			_ = w.poolManager.Close()
			w.poolManager = nil
		}
	}()

	if w.healthServer != nil {
		if err := w.healthServer.Start(ctx); err != nil {
			return fmt.Errorf("failed to start worker health check server: %w", err)
		}
	}

	// Create a wait group to track all polling goroutines
	var wg sync.WaitGroup

	// Launch polling goroutines
	for i := 0; i < w.maxActiveRuns; i++ {
		wg.Add(1)
		go func(pollerIndex int) {
			defer wg.Done()
			// Create a wrapper task handler that tracks task state
			wrappedHandler := &trackingHandler{
				worker:      w,
				pollerIndex: pollerIndex,
				handler:     w.handler,
			}
			poller := NewPoller(w.id, w.coordinatorCli, wrappedHandler, pollerIndex, w.labels)
			poller.Run(internalCtx)
		}(i)
	}

	// Start heartbeat goroutine
	wg.Go(func() {
		w.sendHeartbeats(internalCtx)
	})
	wg.Go(func() {
		w.sendRunHeartbeats(internalCtx)
	})

	// Wait for all goroutines to complete, then signal done
	go func() {
		wg.Wait()
		close(w.stopDone)
	}()

	// Block until all goroutines complete
	<-w.stopDone

	return nil
}

// Stop gracefully shuts down the worker.
func (w *Worker) Stop(ctx context.Context) error {
	var err error
	w.stopOnce.Do(func() {
		logger.Info(ctx, "Worker stopping", tag.WorkerID(w.id))

		// Cancel the internal context to signal all goroutines to stop
		if w.stopCancel != nil {
			w.stopCancel()
		}

		// Wait for all goroutines to complete (with timeout from ctx)
		if w.stopDone != nil {
			select {
			case <-w.stopDone:
				// All goroutines have stopped
			case <-ctx.Done():
				logger.Warn(ctx, "Worker stop timed out waiting for goroutines",
					tag.WorkerID(w.id))
			}
		}

		// Close the global PostgreSQL pool manager if initialized
		if w.poolManager != nil {
			if poolErr := w.poolManager.Close(); poolErr != nil {
				logger.Error(ctx, "Failed to close PostgreSQL pool manager",
					tag.WorkerID(w.id),
					tag.Error(poolErr))
				if err == nil {
					err = fmt.Errorf("failed to close PostgreSQL pool manager: %w", poolErr)
				}
			} else {
				logger.Info(ctx, "PostgreSQL pool manager closed", tag.WorkerID(w.id))
			}
		}

		// Cleanup coordinator client connections
		if cleanupErr := w.coordinatorCli.Cleanup(ctx); cleanupErr != nil {
			if err == nil {
				err = fmt.Errorf("failed to cleanup coordinator client: %w", cleanupErr)
			}
		}

		if w.healthServer != nil {
			if stopErr := w.healthServer.Stop(ctx); stopErr != nil && err == nil {
				err = fmt.Errorf("failed to stop worker health check server: %w", stopErr)
			}
		}
	})

	return err
}

// WaitReady blocks until the worker appears in coordinator registration.
func (w *Worker) WaitReady(ctx context.Context) error {
	if w == nil {
		return fmt.Errorf("worker is not initialized")
	}
	if w.coordinatorCli == nil {
		return fmt.Errorf("worker coordinator client is not configured")
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		workers, err := w.coordinatorCli.GetWorkers(ctx)
		if err == nil {
			for _, item := range workers {
				if item != nil && item.WorkerId == w.id {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for worker %q registration: %w", w.id, ctx.Err())
		case <-ticker.C:
		}
	}
}

// trackingHandler wraps a TaskHandler to track running task state
type trackingHandler struct {
	worker      *Worker
	pollerIndex int
	handler     TaskHandler
}

func (w *Worker) shouldAbandonTaskAfterAck(ctx context.Context, task *coordinatorv1.Task) bool {
	w.pollersMu.Lock()
	hook := w.afterTaskAckHook
	w.pollersMu.Unlock()

	return hook != nil && hook(ctx, task)
}

// Handle tracks task state and delegates to the inner executor
func (t *trackingHandler) Handle(ctx context.Context, task *coordinatorv1.Task) error {
	if t.worker.shouldAbandonTaskAfterAck(ctx, task) {
		logger.Warn(ctx, "Abandoning claimed task before execution",
			tag.WorkerID(t.worker.id),
			tag.RunID(task.DagRunId))
		if err := ctx.Err(); err != nil {
			return err
		}
		return context.Canceled
	}

	pollerID := fmt.Sprintf("poller-%d", t.pollerIndex)
	attemptKey := task.AttemptKey
	if attemptKey == "" {
		attemptKey = fmt.Sprintf("%s:%s", task.DagRunId, pollerID)
	}
	owner, err := taskOwner(task)
	if err != nil {
		return err
	}

	// Create a cancellable context for this task
	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	runningTask := &coordinatorv1.RunningTask{
		DagRunId:         task.DagRunId,
		DagName:          task.Target,
		StartedAt:        time.Now().Unix(),
		RootDagRunName:   task.RootDagRunName,
		RootDagRunId:     task.RootDagRunId,
		ParentDagRunName: task.ParentDagRunName,
		ParentDagRunId:   task.ParentDagRunId,
		AttemptKey:       attemptKey,
	}

	if task.AttemptKey != "" && owner.Host != "" {
		cancelled, heartbeatErr := t.worker.validateClaimedTask(taskCtx, owner, runningTask)
		if heartbeatErr != nil {
			logger.Warn(taskCtx, "Failed to validate claimed task with owner coordinator before execution",
				tag.WorkerID(t.worker.id),
				tag.RunID(task.DagRunId),
				tag.AttemptKey(attemptKey),
				tag.Error(heartbeatErr),
			)
		} else if cancelled {
			logger.Warn(taskCtx, "Owner coordinator rejected claimed task before execution",
				tag.WorkerID(t.worker.id),
				tag.RunID(task.DagRunId),
				tag.AttemptKey(attemptKey),
			)
			return errTaskClaimRejectedBeforeExecution
		}
	}

	// Register the task only after preflight validation has accepted it or
	// failed softly. This keeps rejected claims from consuming worker capacity.
	t.worker.pollersMu.Lock()
	t.worker.runningTasks[attemptKey] = &runningTaskState{
		task:                 runningTask,
		owner:                owner,
		lastOwnerHeartbeatAt: time.Now().UTC(),
	}
	t.worker.pollerTasks[pollerID] = attemptKey
	t.worker.cancelFuncs[attemptKey] = cancel
	t.worker.pollersMu.Unlock()
	defer func() {
		t.worker.pollersMu.Lock()
		delete(t.worker.runningTasks, attemptKey)
		delete(t.worker.pollerTasks, pollerID)
		delete(t.worker.cancelFuncs, attemptKey)
		t.worker.pollersMu.Unlock()
	}()

	// Execute the task with cancellable context
	return t.handler.Handle(taskCtx, task)
}

func (w *Worker) validateClaimedTask(ctx context.Context, owner exec.HostInfo, task *coordinatorv1.RunningTask) (bool, error) {
	if task == nil || task.AttemptKey == "" || owner.Host == "" {
		return false, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, ownerRunHeartbeatCallTimeout)
	resp, err := w.coordinatorCli.RunHeartbeatTo(callCtx, owner, &coordinatorv1.RunHeartbeatRequest{
		WorkerId:           w.id,
		OwnerCoordinatorId: owner.ID,
		RunningTasks:       []*coordinatorv1.RunningTask{task},
	})
	cancel()
	if err != nil {
		return false, err
	}
	if resp == nil || len(resp.CancelledRuns) == 0 {
		return false, nil
	}

	for _, cancelled := range resp.CancelledRuns {
		if cancelled != nil && cancelled.AttemptKey == task.AttemptKey {
			return true, nil
		}
	}
	return false, nil
}

// sendHeartbeats sends periodic heartbeats to the coordinator
func (w *Worker) sendHeartbeats(ctx context.Context) {
	basePolicy := backoff.NewExponentialBackoffPolicy(1 * time.Second)
	basePolicy.BackoffFactor = 1.5
	basePolicy.MaxInterval = 15 * time.Second
	basePolicy.MaxRetries = 0 // unlimited retries
	retryPolicy := backoff.WithJitter(basePolicy, backoff.Jitter)
	retrier := backoff.NewRetrier(retryPolicy)

	const healthyInterval = 1 * time.Second

	waitWithContext := func(ctx context.Context, d time.Duration) bool {
		if d <= 0 {
			return ctx.Err() == nil
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}

	nextDelay := time.Duration(0)

	for {
		if !waitWithContext(ctx, nextDelay) {
			return
		}

		if err := w.sendHeartbeat(ctx); err != nil {
			nextInterval, nextErr := retrier.Next(err)
			if nextErr != nil {
				logger.Error(ctx, "Failed to compute heartbeat backoff interval",
					tag.WorkerID(w.id),
					tag.Error(err))
				nextInterval = healthyInterval
			} else {
				logger.Warn(ctx, "Heartbeat send failed; will retry with backoff",
					tag.WorkerID(w.id),
					tag.Error(err),
					tag.Interval(nextInterval))
			}

			nextDelay = nextInterval
			continue
		}

		// Successful heartbeat; reset backoff and schedule healthy interval
		retrier.Reset()
		nextDelay = healthyInterval
	}
}

func (w *Worker) sendRunHeartbeats(ctx context.Context) {
	const interval = 1 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sendOwnerRunHeartbeats(ctx)
		}
	}
}

func (w *Worker) sendOwnerRunHeartbeats(ctx context.Context) {
	type ownerGroup struct {
		owner exec.HostInfo
		tasks []*coordinatorv1.RunningTask
	}

	w.pollersMu.Lock()
	groups := make(map[string]*ownerGroup)
	lastSeen := make(map[string]time.Time, len(w.runningTasks))
	for attemptKey, state := range w.runningTasks {
		if state == nil || state.task == nil || state.owner.Host == "" {
			continue
		}
		key := fmt.Sprintf("%s|%s|%d", state.owner.ID, state.owner.Host, state.owner.Port)
		group := groups[key]
		if group == nil {
			group = &ownerGroup{owner: state.owner}
			groups[key] = group
		}
		group.tasks = append(group.tasks, state.task)
		lastSeen[attemptKey] = state.lastOwnerHeartbeatAt
	}
	w.pollersMu.Unlock()

	for _, group := range groups {
		callCtx, cancel := context.WithTimeout(ctx, ownerRunHeartbeatCallTimeout)
		resp, err := w.coordinatorCli.RunHeartbeatTo(callCtx, group.owner, &coordinatorv1.RunHeartbeatRequest{
			WorkerId:           w.id,
			OwnerCoordinatorId: group.owner.ID,
			RunningTasks:       group.tasks,
		})
		cancel()

		if err != nil {
			w.cancelTasksForOwnerTimeout(ctx, group.owner, group.tasks, lastSeen)
			continue
		}

		w.markOwnerHeartbeatSuccess(group.tasks, time.Now().UTC())
		if resp != nil && len(resp.CancelledRuns) > 0 {
			w.processCancellations(ctx, resp.CancelledRuns)
		}
	}
}

func (w *Worker) markOwnerHeartbeatSuccess(tasks []*coordinatorv1.RunningTask, observedAt time.Time) {
	w.pollersMu.Lock()
	defer w.pollersMu.Unlock()

	for _, task := range tasks {
		if task == nil {
			continue
		}
		if state, ok := w.runningTasks[task.AttemptKey]; ok && state != nil {
			state.lastOwnerHeartbeatAt = observedAt
		}
	}
}

func (w *Worker) cancelTasksForOwnerTimeout(ctx context.Context, owner exec.HostInfo, tasks []*coordinatorv1.RunningTask, lastSeen map[string]time.Time) {
	now := time.Now().UTC()
	var timedOut []*coordinatorv1.CancelledRun
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if now.Sub(lastSeen[task.AttemptKey]) <= ownerHeartbeatTimeout {
			continue
		}
		logger.Warn(ctx, "Owner coordinator unreachable; cancelling distributed run",
			tag.WorkerID(w.id),
			tag.AttemptKey(task.AttemptKey),
			slog.String("owner-id", owner.ID),
			tag.Host(owner.Host),
			tag.Port(owner.Port),
		)
		timedOut = append(timedOut, &coordinatorv1.CancelledRun{AttemptKey: task.AttemptKey})
	}
	if len(timedOut) > 0 {
		w.processCancellations(ctx, timedOut)
	}
}

// sendHeartbeat sends a single heartbeat to the coordinator
func (w *Worker) sendHeartbeat(ctx context.Context) error {
	w.pollersMu.Lock()

	// Calculate stats
	busyCount := len(w.runningTasks)
	runningTasks := make([]*coordinatorv1.RunningTask, 0, busyCount)
	for _, task := range w.runningTasks {
		runningTasks = append(runningTasks, task.task)
	}

	w.pollersMu.Unlock()

	// Safely convert to int32, capping at max int32 if needed
	totalPollers := int32(min(w.maxActiveRuns, math.MaxInt32)) //nolint:gosec
	busyCount32 := int32(min(busyCount, math.MaxInt32))        //nolint:gosec

	req := &coordinatorv1.HeartbeatRequest{
		WorkerId: w.id,
		Labels:   w.labels,
		Stats: &coordinatorv1.WorkerStats{
			TotalPollers: totalPollers,
			BusyPollers:  busyCount32,
			RunningTasks: runningTasks,
		},
	}

	resp, err := w.coordinatorCli.Heartbeat(ctx, req)
	if err != nil {
		return err
	}

	// Process cancellation directives from the coordinator
	if resp != nil && len(resp.CancelledRuns) > 0 {
		w.processCancellations(ctx, resp.CancelledRuns)
	}

	return nil
}

// processCancellations cancels tasks that the coordinator has marked for cancellation
func (w *Worker) processCancellations(ctx context.Context, cancelledRuns []*coordinatorv1.CancelledRun) {
	w.pollersMu.Lock()
	defer w.pollersMu.Unlock()

	for _, run := range cancelledRuns {
		if cancelFunc, exists := w.cancelFuncs[run.AttemptKey]; exists {
			logger.Info(ctx, "Cancelling task per coordinator directive",
				tag.WorkerID(w.id),
				tag.AttemptKey(run.AttemptKey))
			cancelFunc()
		}
	}
}

// isSharedNothingMode returns true if the worker is running in shared-nothing mode.
// Shared-nothing mode is detected when static coordinator addresses are configured.
// In shared-nothing mode, global PostgreSQL pool management is automatically enabled.
func (w *Worker) isSharedNothingMode() bool {
	return w.cfg != nil && len(w.cfg.Worker.Coordinators) > 0
}
