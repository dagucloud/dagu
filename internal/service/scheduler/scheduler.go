// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/fileeventstore"
	"github.com/dagucloud/dagu/internal/runtime"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

// Clock is a function that returns the current time.
// It can be replaced for testing purposes.
type Clock func() time.Time

type Scheduler struct {
	entryReader         EntryReader
	quit                chan any
	running             atomic.Bool
	dagRunStore         exec.DAGRunStore
	queueStore          exec.QueueStore
	procStore           exec.ProcStore
	config              *config.Config
	dirLock             dirlock.DirLock // File-based lock to prevent multiple scheduler instances
	dagExecutor         *DAGExecutor
	healthServer        *HealthServer // Health check server for monitoring
	serviceRegistry     exec.ServiceRegistry
	disableHealthServer bool            // Disable health server when running from start-all
	zombieDetector      *ZombieDetector // Zombie DAG run detector
	instanceID          string          // Unique instance identifier for service registry
	queueProcessor      *QueueProcessor // Processor for queued DAG runs
	queueWatcher        exec.QueueWatcher
	retryScanner        *RetryScanner // DAG-level retry scanner
	planner             *TickPlanner  // Unified scheduling decision module
	stopOnce            sync.Once
	lock                sync.Mutex
	lifecycleMu         sync.Mutex
	startupCancel       context.CancelFunc
	storeCloser         func(context.Context)
	lockHeld            atomic.Bool
	clock               Clock // Clock function for getting current time
	eventCollector      *fileeventstore.Collector
	githubDispatch      githubDispatchRunner
}

type schedulerHooks struct {
	onLockWait func()
}

type startupState struct {
	serviceRegistered      bool
	lockAcquired           bool
	healthServerStarted    bool
	queueWatcherStarted    bool
	queueProcessorStarted  bool
	entryReaderInitialized bool
	plannerStarted         bool
}

// New constructs a Scheduler from the provided stores, runtime manager,
// service registry, and dispatcher.
func New(
	cfg *config.Config,
	er EntryReader,
	drm runtime.Manager,
	dagRunStore exec.DAGRunStore,
	queueStore exec.QueueStore,
	procStore exec.ProcStore,
	reg exec.ServiceRegistry,
	coordinatorCli exec.Dispatcher,
	watermarkStore WatermarkStore,
) (*Scheduler, error) {
	return newScheduler(cfg, er, drm, dagRunStore, queueStore, procStore, reg, coordinatorCli, watermarkStore, schedulerHooks{})
}

func newScheduler(
	cfg *config.Config,
	er EntryReader,
	drm runtime.Manager,
	dagRunStore exec.DAGRunStore,
	queueStore exec.QueueStore,
	procStore exec.ProcStore,
	reg exec.ServiceRegistry,
	coordinatorCli exec.Dispatcher,
	watermarkStore WatermarkStore,
	hooks schedulerHooks,
) (*Scheduler, error) {
	timeLoc := cfg.Core.Location
	if timeLoc == nil {
		timeLoc = time.Local
	}
	lockOpts := &dirlock.LockOptions{
		StaleThreshold: cfg.Scheduler.LockStaleThreshold,
		RetryInterval:  cfg.Scheduler.LockRetryInterval,
		OnWait:         hooks.onLockWait,
	}
	lockDir := filepath.Join(cfg.Paths.DataDir, "scheduler", "locks")
	dirLock := dirlock.New(lockDir, lockOpts)
	subCmdBuilder := runtime.NewSubCmdBuilder(cfg)
	dagStore := er.DAGStore()
	dagExecutor := NewDAGExecutor(
		coordinatorCli,
		subCmdBuilder,
		cfg.DefaultExecMode,
		cfg.Paths.BaseConfig,
		buildSnapshotBuilder(cfg.Paths, dagStore),
	)
	healthServer := NewHealthServer(cfg.Scheduler.Port)

	// Resolve IsSuspended once at construction time and wire the event channel.
	eventCh := make(chan DAGChangeEvent)
	var isSuspended IsSuspendedFunc
	if dagStore != nil {
		isSuspended = dagStore.IsSuspended
	}
	if impl, ok := er.(*entryReaderImpl); ok {
		impl.setEvents(eventCh)
	}
	processor := NewQueueProcessor(
		queueStore,
		dagRunStore,
		procStore,
		dagExecutor,
		cfg.Queues,
		WithIsSuspended(isSuspended),
	)
	defaultClock := Clock(time.Now)

	// Build catchup-enqueue callbacks when queues are enabled.
	queuesEnabled := cfg.Queues.Enabled
	var isQueued IsQueuedFunc
	var enqueueFunc EnqueueFunc
	if queuesEnabled {
		isQueued = func(ctx context.Context, dag *core.DAG) (bool, error) {
			items, err := queueStore.ListByDAGName(ctx, dag.ProcGroup(), dag.Name)
			if err != nil {
				return false, err
			}
			return len(items) > 0, nil
		}
		enqueueFunc = func(ctx context.Context, dag *core.DAG, runID string, triggerType core.TriggerType, scheduleTime time.Time) error {
			return EnqueueCatchupRun(ctx, dagRunStore, queueStore, cfg.Paths.LogDir, cfg.Paths.ArtifactDir, cfg.Paths.BaseConfig, dag, runID, triggerType, scheduleTime)
		}
	}

	planner := NewTickPlanner(TickPlannerConfig{
		WatermarkStore:  watermarkStore,
		IsSuspended:     isSuspended,
		GetLatestStatus: drm.GetLatestStatus,
		IsRunning: func(ctx context.Context, dag *core.DAG) (bool, error) {
			count, err := procStore.CountAliveByDAGName(ctx, dag.ProcGroup(), dag.Name)
			if err != nil {
				return false, err
			}
			return count > 0, nil
		},
		GenRunID: drm.GenDAGRunID,
		Dispatch: func(ctx context.Context, dag *core.DAG, runID string, triggerType core.TriggerType, scheduleTime time.Time) error {
			return dagExecutor.HandleJob(
				ctx, dag,
				coordinatorv1.Operation_OPERATION_START,
				runID, triggerType, scheduleTime,
			)
		},
		Stop: func(ctx context.Context, dag *core.DAG) error {
			return drm.Stop(ctx, dag, "")
		},
		Restart: func(ctx context.Context, dag *core.DAG, scheduleTime time.Time) error {
			return dagExecutor.Restart(ctx, dag, scheduleTime)
		},
		Clock:         defaultClock,
		Location:      timeLoc,
		Events:        eventCh,
		QueuesEnabled: queuesEnabled,
		Enqueue:       enqueueFunc,
		IsQueued:      isQueued,
		RunExists: func(ctx context.Context, dag *core.DAG, runID string) (bool, error) {
			_, err := dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, runID))
			switch {
			case err == nil:
				return true, nil
			case errors.Is(err, exec.ErrDAGRunIDNotFound):
				return false, nil
			case errors.Is(err, exec.ErrNoStatusData):
				return true, nil
			default:
				return false, err
			}
		},
	})

	retryScanner, err := NewRetryScanner(
		dagRunStore,
		queueStore,
		isSuspended,
		cfg.Scheduler.RetryFailureWindow,
		defaultClock,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize retry scanner: %w", err)
	}

	return &Scheduler{
		quit:            make(chan any),
		entryReader:     er,
		dagRunStore:     dagRunStore,
		queueStore:      queueStore,
		procStore:       procStore,
		config:          cfg,
		dirLock:         dirLock,
		dagExecutor:     dagExecutor,
		healthServer:    healthServer,
		serviceRegistry: reg,
		queueProcessor:  processor,
		retryScanner:    retryScanner,
		planner:         planner,
		clock:           defaultClock,
	}, nil
}

// SetClock sets a custom clock function for testing purposes.
// This must be called before Start().
func (s *Scheduler) SetClock(clock Clock) {
	s.clock = clock
	s.planner.cfg.Clock = clock
	if s.retryScanner != nil {
		s.retryScanner.clock = clock
	}
}

// SetEventCollector configures the scheduler-owned collector loop.
// This must be called before Start().
func (s *Scheduler) SetEventCollector(collector *fileeventstore.Collector) {
	if s == nil {
		return
	}
	s.eventCollector = collector
}

// SetDAGRunLeaseStore configures the shared distributed lease store used for
// queue capacity accounting.
func (s *Scheduler) SetDAGRunLeaseStore(store exec.DAGRunLeaseStore) {
	if s == nil || s.queueProcessor == nil {
		return
	}
	s.queueProcessor.dagRunLeaseStore = store
}

// SetDispatchTaskStore configures the shared distributed dispatch reservation
// store used for queue admission and restart-safe deduplication.
func (s *Scheduler) SetDispatchTaskStore(store exec.DispatchTaskStore) {
	if s == nil || s.queueProcessor == nil {
		return
	}
	s.queueProcessor.dispatchTaskStore = store
}

// SetStoreCloser configures cleanup for the scheduler-owned persistence store.
// If unset, Stop closes dagRunStore directly.
func (s *Scheduler) SetStoreCloser(closer func(context.Context)) {
	if s == nil {
		return
	}
	s.storeCloser = closer
}

// SetRestartFunc overrides the planner's restart function for testing purposes.
// This must be called before Start().
func (s *Scheduler) SetRestartFunc(fn RestartFunc) {
	s.planner.cfg.Restart = fn
}

// SetStopFunc overrides the planner's stop function for testing purposes.
// This must be called before Start().
func (s *Scheduler) SetStopFunc(fn StopFunc) {
	s.planner.cfg.Stop = fn
}

// SetGetLatestStatusFunc overrides the planner's GetLatestStatus function for testing purposes.
// This must be called before Start().
func (s *Scheduler) SetGetLatestStatusFunc(fn GetLatestStatusFunc) {
	s.planner.cfg.GetLatestStatus = fn
}

// SetDispatchFunc overrides the planner's Dispatch function for testing purposes.
// This must be called before Start().
func (s *Scheduler) SetDispatchFunc(fn DispatchFunc) {
	s.planner.cfg.Dispatch = fn
}

// DisableHealthServer disables the health check server (used when running from start-all)
func (s *Scheduler) DisableHealthServer() {
	s.disableHealthServer = true
}

func (s *Scheduler) registerStartupCancel(cancel context.CancelFunc) bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	select {
	case <-s.quit:
		return false
	default:
	}

	s.startupCancel = cancel
	return true
}

func (s *Scheduler) clearStartupCancel() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	s.startupCancel = nil
}

func (s *Scheduler) stopping() bool {
	select {
	case <-s.quit:
		return true
	default:
		return false
	}
}

func (s *Scheduler) releaseDirLock(ctx context.Context, msg string) {
	if !s.lockHeld.CompareAndSwap(true, false) {
		return
	}
	if err := s.dirLock.Unlock(); err != nil {
		logger.Error(ctx, msg, tag.Error(err))
	}
}

func (s *Scheduler) beginStartup(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	startupCtx, cancel := context.WithCancel(ctx)
	if !s.registerStartupCancel(cancel) {
		cancel()
		return nil, nil, false
	}
	return startupCtx, cancel, true
}

func (s *Scheduler) cleanupFailedStartup(state startupState) {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()

	if state.queueWatcherStarted {
		s.stopQueueWatcher(cleanupCtx)
	}
	if state.queueProcessorStarted {
		s.queueProcessor.Stop()
	}
	if state.entryReaderInitialized {
		s.entryReader.Stop()
	}
	if state.plannerStarted {
		s.planner.Stop(cleanupCtx)
	}
	if state.healthServerStarted {
		s.stopHealthServer(cleanupCtx, "Failed to stop health check server during startup cleanup")
	}
	s.closeDAGExecutor(cleanupCtx)
	if state.serviceRegistered {
		s.unregisterService(cleanupCtx)
	}
	if state.lockAcquired {
		s.releaseDirLock(cleanupCtx, "Failed to release scheduler lock during startup cleanup")
	}
	s.closeDAGRunStore(cleanupCtx)
}

func (s *Scheduler) updateServiceStatus(ctx context.Context, status exec.ServiceStatus, failureMsg, successMsg string) {
	if s.serviceRegistry == nil {
		return
	}
	if err := s.serviceRegistry.UpdateStatus(ctx, exec.ServiceNameScheduler, status); err != nil {
		logger.Error(ctx, failureMsg, tag.Error(err))
		return
	}
	if successMsg != "" {
		logger.Info(ctx, successMsg)
	}
}

func (s *Scheduler) stopHealthServer(ctx context.Context, failureMsg string) {
	if s.healthServer == nil || s.disableHealthServer {
		return
	}
	if err := s.healthServer.Stop(ctx); err != nil {
		logger.Error(ctx, failureMsg, tag.Error(err))
	}
}

func (s *Scheduler) closeDAGExecutor(ctx context.Context) {
	if s.dagExecutor != nil {
		s.dagExecutor.Close(ctx)
	}
}

func (s *Scheduler) setQueueWatcher(w exec.QueueWatcher) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.queueWatcher = w
}

func (s *Scheduler) stopQueueWatcher(ctx context.Context) {
	s.lock.Lock()
	queueWatcher := s.queueWatcher
	s.queueWatcher = nil
	s.lock.Unlock()

	if queueWatcher != nil {
		queueWatcher.Stop(ctx)
	}
}

func (s *Scheduler) unregisterService(ctx context.Context) {
	if s.serviceRegistry != nil {
		s.serviceRegistry.Unregister(ctx)
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	ctx, cancel, ok := s.beginStartup(ctx)
	if !ok {
		return nil
	}
	state := startupState{}
	cleanupOnFailure := true
	defer func() {
		if !cleanupOnFailure {
			return
		}
		s.cleanupFailedStartup(state)
	}()
	defer func() {
		s.clearStartupCancel()
		cancel()
	}()

	if s.instanceID == "" {
		hostname, _ := os.Hostname()
		s.instanceID = fmt.Sprintf("%s-%d-%d", hostname, os.Getpid(), time.Now().Unix())
	}

	if s.serviceRegistry != nil {
		hostname, _ := os.Hostname()
		hostInfo := exec.HostInfo{
			ID:        s.instanceID,
			Host:      hostname,
			Port:      s.config.Scheduler.Port, // Health check port (0 if disabled)
			Status:    exec.ServiceStatusInactive,
			StartedAt: time.Now(),
		}
		if err := s.serviceRegistry.Register(ctx, exec.ServiceNameScheduler, hostInfo); err != nil {
			logger.Error(ctx, "Failed to register with service registry", tag.Error(err))
			// Continue anyway - service registry is not critical
		} else {
			state.serviceRegistered = true
			logger.Info(ctx, "Registered with service registry as inactive",
				tag.ServiceID(s.instanceID),
				tag.Host(hostname),
				tag.Port(s.config.Scheduler.Port),
			)
		}
	}

	// Every scheduler process should expose /health, even while it is waiting
	// on the HA lock. Active/inactive role is tracked separately via the
	// service registry status.
	if !s.disableHealthServer {
		if err := s.healthServer.Start(ctx); err != nil {
			return fmt.Errorf("failed to start health check server: %w", err)
		}
		state.healthServerStarted = true
	}

	logger.Info(ctx, "Waiting to acquire scheduler lock")
	if err := s.dirLock.Lock(ctx); err != nil {
		if errors.Is(err, context.Canceled) && s.stopping() {
			return nil
		}
		return fmt.Errorf("failed to acquire scheduler lock: %w", err)
	}
	s.lockHeld.Store(true)
	state.lockAcquired = true

	logger.Info(ctx, "Acquired scheduler lock")

	if ctx.Err() != nil && s.stopping() {
		return nil
	}

	s.updateServiceStatus(ctx, exec.ServiceStatusActive, "Failed to update status to active", "Updated scheduler status to active")

	sig := make(chan os.Signal, 1)

	queueWatcher := s.queueStore.QueueWatcher(ctx)
	notifyCh, err := queueWatcher.Start(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to start queue watcher", tag.Error(err))
		return err
	}
	s.setQueueWatcher(queueWatcher)
	state.queueWatcherStarted = true
	s.queueProcessor.Start(ctx, notifyCh)
	state.queueProcessorStarted = true

	signal.Notify(sig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(sig)

	var wg sync.WaitGroup

	wg.Go(func() {
		s.startHeartbeat(ctx)
	})

	if err := s.entryReader.Init(ctx); err != nil {
		logger.Error(ctx, "Failed to initialize entry reader", tag.Error(err))
		return err
	}
	state.entryReaderInitialized = true

	// planner.Init is best-effort: if watermark loading fails, Init falls back
	// to an empty state internally, so catch-up windows replay from scratch.
	if err := s.planner.Init(ctx, s.entryReader.DAGs()); err != nil {
		logger.Error(ctx, "Failed to initialize tick planner", tag.Error(err))
	}

	s.planner.Start(ctx)
	state.plannerStarted = true

	// Start background loops only after the last startup failure point.
	// Heartbeat starts earlier to keep the scheduler lock alive during init.
	wg.Go(func() {
		s.startZombieDetector(ctx)
	})

	wg.Go(func() {
		s.startRetryScanner(ctx)
	})

	wg.Go(func() {
		s.startEventCollector(ctx)
	})

	wg.Go(func() {
		s.startGitHubDispatch(ctx)
	})

	wg.Go(func() {
		s.entryReader.Start(ctx)
	})

	logger.Info(ctx, "Scheduler started")
	cleanupOnFailure = false

	wg.Add(1)
	go func(ctx context.Context) {
		defer wg.Done()
		s.cronLoop(ctx, sig)
	}(ctx)
	wg.Wait()

	return nil
}

func (s *Scheduler) startZombieDetector(ctx context.Context) {
	if s.config.Scheduler.ZombieDetectionInterval <= 0 {
		return
	}

	// Create zombie detector while holding lock. Check s.quit first to
	// avoid starting after Stop() has already run. Both this check and
	// Stop()'s close(s.quit) + zombieDetector.Stop() happen under s.lock,
	// so there is no race.
	s.lock.Lock()
	select {
	case <-s.quit:
		s.lock.Unlock()
		return
	case <-ctx.Done():
		s.lock.Unlock()
		return
	default:
	}
	s.zombieDetector = NewZombieDetector(
		s.dagRunStore,
		s.procStore,
		s.config.Scheduler.ZombieDetectionInterval,
		s.config.Scheduler.FailureThreshold,
	)
	zd := s.zombieDetector
	s.lock.Unlock()

	logger.Info(ctx, "Started zombie detector",
		tag.Interval(s.config.Scheduler.ZombieDetectionInterval),
	)

	// Start blocks, so call it after releasing the lock
	zd.Start(ctx)
}

func (s *Scheduler) startEventCollector(ctx context.Context) {
	if s.eventCollector == nil {
		return
	}
	s.eventCollector.Start(ctx)
}

func (s *Scheduler) startHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(time.Second * 7)
	defer ticker.Stop()

	for {
		select {
		case <-s.quit:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.dirLock.Heartbeat(ctx); err != nil {
				logger.Error(ctx, "Heartbeat failed, scheduler self-fencing", tag.Error(err))
				// Self-fencing protocol: clear lockHeld BEFORE calling Stop().
				// This makes releaseDirLock skip Unlock, so a scheduler that just
				// lost ownership cannot remove a replacement lock acquired by another process.
				s.lockHeld.Store(false)
				s.Stop(ctx)
				return
			}
		}
	}
}

func (s *Scheduler) startRetryScanner(ctx context.Context) {
	if s.retryScanner == nil || s.config.Scheduler.RetryFailureWindow <= 0 {
		logger.Info(ctx, "Retry scanner disabled",
			tag.Interval(s.config.Scheduler.RetryFailureWindow),
		)
		return
	}

	logger.Info(ctx, "Started retry scanner",
		tag.Interval(retryScanInterval),
		slog.Duration("retry_failure_window", s.config.Scheduler.RetryFailureWindow),
	)
	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		select {
		case <-s.quit:
			cancel()
		case <-ctx.Done():
			cancel()
		}
	}()

	s.retryScanner.Start(scanCtx)
}

// cronLoop runs the main scheduler loop to invoke jobs at scheduled times.
func (s *Scheduler) cronLoop(ctx context.Context, sig chan os.Signal) {
	tickTime := s.clock().Truncate(time.Minute)
	timer := time.NewTimer(0)
	defer timer.Stop()

	s.running.Store(true)
	defer s.running.Store(false)

	for s.waitForTick(ctx, sig, timer) {
		s.runTick(ctx, tickTime)
		tickTime = s.NextTick(tickTime)
		timer.Reset(tickTime.Sub(s.clock()))
	}
}

func (s *Scheduler) waitForTick(ctx context.Context, sig chan os.Signal, timer *time.Timer) bool {
	select {
	case <-ctx.Done():
		return false
	case <-sig:
		s.Stop(ctx)
		return false
	case <-s.quit:
		return false
	case <-timer.C:
		_ = timer.Stop()
		return true
	}
}

func (s *Scheduler) runTick(ctx context.Context, tickTime time.Time) {
	for _, run := range s.planner.Plan(ctx, tickTime) {
		s.dispatchRun(ctx, run)
	}
	s.planner.Advance(tickTime)
}

// NextTick returns the next tick time for the scheduler.
func (*Scheduler) NextTick(now time.Time) time.Time {
	return now.Truncate(time.Minute).Add(time.Minute)
}

// IsRunning returns whether the scheduler is currently running.
func (s *Scheduler) IsRunning() bool {
	return s.running.Load()
}

// Stop stops the scheduler.
func (s *Scheduler) Stop(ctx context.Context) {
	s.stopOnce.Do(func() {
		var wg sync.WaitGroup
		wg.Add(3)

		var startupCancel context.CancelFunc
		var zd *ZombieDetector
		s.lifecycleMu.Lock()
		startupCancel = s.startupCancel
		s.lock.Lock()
		close(s.quit)
		zd = s.zombieDetector
		s.lock.Unlock()
		s.lifecycleMu.Unlock()

		if startupCancel != nil {
			startupCancel()
		}

		go func() {
			defer wg.Done()
			s.stopQueueWatcher(ctx)
		}()

		go func() {
			defer wg.Done()
			s.queueProcessor.Stop()
		}()

		go func(ctx context.Context) {
			defer wg.Done()
			s.stopCron(ctx)
		}(ctx)

		// Stop the producer (entryReader) synchronously BEFORE the consumer (planner).
		// This ensures er.quit is closed before drainEvents exits, so any in-flight
		// sendEvent unblocks via the select on er.quit.
		s.entryReader.Stop()

		if zd != nil {
			zd.Stop(ctx)
		}

		s.planner.Stop(ctx)

		s.releaseDirLock(ctx, "Failed to release scheduler lock in Stop")

		wg.Wait()

		s.closeDAGRunStore(ctx)
	})
}

func (s *Scheduler) closeDAGRunStore(ctx context.Context) {
	if s.storeCloser != nil {
		s.storeCloser(ctx)
		return
	}
	if err := exec.CloseDAGRunStore(ctx, s.dagRunStore); err != nil {
		logger.Warn(ctx, "Failed to close scheduler DAG-run store", tag.Error(err))
	}
}

func (s *Scheduler) stopCron(ctx context.Context) {
	s.updateServiceStatus(ctx, exec.ServiceStatusInactive, "Failed to update status to inactive", "")
	s.stopHealthServer(ctx, "Failed to stop health check server")
	s.closeDAGExecutor(ctx)
	s.unregisterService(ctx)

	logger.Info(ctx, "Scheduler stopped")
}

// dispatchRun dispatches a planned run.
// Catchup runs are dispatched synchronously (enqueue is fast file I/O and
// we need the result to decide whether to advance the watermark).
// Non-catchup runs are dispatched in a goroutine (process spawn can be slow).
func (s *Scheduler) dispatchRun(ctx context.Context, run PlannedRun) {
	if run.TriggerType == core.TriggerTypeCatchUp {
		s.dispatchPlannedRun(ctx, run)
		return
	}
	go s.dispatchPlannedRun(ctx, run)
}

func (s *Scheduler) dispatchPlannedRun(ctx context.Context, run PlannedRun) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error(ctx, "Run dispatch panicked",
				tag.DAG(run.DAG.Name),
				tag.Error(panicToError(r)),
			)
		}
	}()
	s.planner.DispatchRun(ctx, run)
}
