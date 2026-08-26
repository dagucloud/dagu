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

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/opencodehost"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/profile"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// Clock is a function that returns the current time.
// It can be replaced for testing purposes.
type Clock func() time.Time

type processRepository interface {
	queueProcessRepository
	zombieProcessRepository
	CountAliveByDAGName(ctx context.Context, groupName, dagName string) (int, error)
}

type queueProcessRepository interface {
	CountAlive(ctx context.Context, groupName string) (int, error)
	IsRunAlive(ctx context.Context, groupName string, dagRun ir.DAGRunRef) (bool, error)
}

type zombieProcessRepository interface {
	ListAllEntries(ctx context.Context) ([]proc.ProcEntry, error)
	RemoveIfStale(ctx context.Context, entry proc.ProcEntry) error
}

type Scheduler struct {
	entryReader         EntryReader
	quit                chan any
	running             atomic.Bool
	dagRunRepository    *persis.DAGRunRepository
	queueStore          queuedomain.QueueStore
	procRepository      processRepository
	config              *config.Config
	dirLock             dirlock.DirLock // File-based lock to prevent multiple scheduler instances
	dagExecutor         *DAGExecutor
	healthServer        *HealthServer // Health check server for monitoring
	serviceRegistry     serviceregistry.ServiceRegistry
	disableHealthServer bool            // Disable health server when running from start-all
	zombieDetector      *ZombieDetector // Zombie DAG run detector
	instanceID          string          // Unique instance identifier for service registry
	queueProcessor      *QueueProcessor // Processor for queued DAG runs
	queueWatcher        queuedomain.QueueWatcher
	retryScanner        *RetryScanner // DAG-level retry scanner
	planner             *TickPlanner  // Unified scheduling decision module
	stopOnce            sync.Once
	lock                sync.Mutex
	lifecycleMu         sync.Mutex
	startupCancel       context.CancelFunc
	lockHeld            atomic.Bool
	clock               Clock // Clock function for getting current time
	eventService        *eventstore.Service
	eventCollector      func(context.Context)
	notificationMonitor func(context.Context, *eventstore.DAGRunCursor)
	incidentMonitor     func(context.Context, *eventstore.DAGRunCursor)
}

type schedulerHooks struct {
	onLockWait func()
}

// Dependencies contains the stores and services used by Scheduler.
type Dependencies struct {
	EntryReader          EntryReader
	DAGRunManager        runtime.Manager
	DAGRepository        *persis.DAGRepository
	DAGRunRepository     *persis.DAGRunRepository
	QueueStore           queuedomain.QueueStore
	ProcRepository       *persis.ProcRepository
	ServiceRegistry      serviceregistry.ServiceRegistry
	CoordinatorClient    dispatch.Dispatcher
	SchedulerStateStore  schedulerstate.Store
	DAGRunLeaseStore     dispatch.DAGRunLeaseStore
	DispatchTaskStore    dispatch.DispatchTaskStore
	WorkerHeartbeatStore dispatch.WorkerHeartbeatStore
	WorkerStaleAfter     time.Duration
	DAGSettingsStore     dagsettings.Store
	ProfileStore         profile.Store
	EventService         *eventstore.Service
	EventCollector       func(context.Context)
	NotificationStore    notification.Store
	NotificationState    chatbridge.StateStore
	NewNotificationLease func() chatbridge.Lease
	IncidentStore        incident.Store
	IncidentState        chatbridge.StateStore
	NewIncidentLease     func() chatbridge.Lease
	LicenseManager       *license.Manager
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

// New constructs a Scheduler from its configuration and dependencies.
func New(cfg *config.Config, deps Dependencies) (*Scheduler, error) {
	entryReader := deps.EntryReader
	if entryReader == nil && deps.DAGRepository != nil {
		entryReader = NewFileEntryReader(cfg.Paths.DAGsDir, deps.DAGRepository, cfg.DAGDiscovery.Recursive)
	}
	var profileResolver DAGProfileResolver
	if deps.DAGSettingsStore != nil {
		profileResolver = NewDAGProfileResolver(deps.DAGSettingsStore, deps.ProfileStore)
	}
	scheduler, err := newScheduler(
		cfg,
		entryReader,
		deps.DAGRunManager,
		deps.DAGRepository,
		deps.DAGRunRepository,
		deps.QueueStore,
		deps.ProcRepository,
		deps.ServiceRegistry,
		deps.CoordinatorClient,
		deps.SchedulerStateStore,
		schedulerHooks{},
		profileResolver,
	)
	if err != nil {
		return nil, err
	}
	scheduler.eventCollector = deps.EventCollector
	scheduler.eventService = deps.EventService
	if deps.EventService != nil {
		scheduler.notificationMonitor = newNotificationMonitor(cfg, deps)
		scheduler.incidentMonitor = newIncidentMonitor(cfg, deps)
	}
	scheduler.queueProcessor.dagRunLeaseStore = deps.DAGRunLeaseStore
	scheduler.queueProcessor.dispatchTaskStore = deps.DispatchTaskStore
	scheduler.queueProcessor.dispatchAdmissionStore = dispatchAdmissionStoreFromTaskStore(deps.DispatchTaskStore)
	scheduler.queueProcessor.workerHeartbeatStore = deps.WorkerHeartbeatStore
	if deps.WorkerStaleAfter != 0 {
		scheduler.queueProcessor.workerStaleAfter = deps.WorkerStaleAfter
	}
	return scheduler, nil
}

func newScheduler(
	cfg *config.Config,
	er EntryReader,
	drm runtime.Manager,
	dagRepository *persis.DAGRepository,
	dagRunRepository *persis.DAGRunRepository,
	queueStore queuedomain.QueueStore,
	procRepository processRepository,
	reg serviceregistry.ServiceRegistry,
	coordinatorCli dispatch.Dispatcher,
	stateStore schedulerstate.Store,
	hooks schedulerHooks,
	profileResolver DAGProfileResolver,
) (*Scheduler, error) {
	if dagRepository == nil {
		return nil, fmt.Errorf("DAG repository is required")
	}
	if dagRunRepository == nil {
		return nil, fmt.Errorf("DAG-run repository is required")
	}
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
	subCmdBuilder := launcher.NewSubCmdBuilder(cfg)
	workspaceBaseConfigDir := workspace.BaseConfigDir(cfg.Paths.DAGsDir)
	dagExecutor := NewDAGExecutor(
		coordinatorCli,
		subCmdBuilder,
		cfg.DefaultExecMode,
		cfg.Paths.BaseConfig,
		WithDAGExecutorProfileResolver(profileResolver),
		WithDAGExecutorWorkspaceBaseConfigDir(workspaceBaseConfigDir),
	)
	healthServer := NewHealthServer(cfg.Scheduler.Port)

	// Resolve IsSuspended once at construction time.
	eventCh := er.Events()
	isSuspended := dagRepository.IsSuspended
	processor := NewQueueProcessor(
		queueStore,
		dagRunRepository,
		procRepository,
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
		isQueued = func(ctx context.Context, dag *ir.DAG) (bool, error) {
			items, err := queueStore.ListByDAGName(ctx, dag.ProcGroup(), dag.Name)
			if err != nil {
				return false, err
			}
			return len(items) > 0, nil
		}
		enqueueFunc = func(ctx context.Context, entry DAGEntry, runID string, triggerType ir.TriggerType, scheduleTime time.Time) error {
			dag := entry.DAG
			profileName, err := dagExecutor.defaultProfileName(ctx, entry.DefinitionID, dag)
			if err != nil {
				return fmt.Errorf("failed to resolve DAG profile: %w", err)
			}
			return EnqueueCatchupRun(
				ctx,
				dagRunRepository,
				queueStore,
				cfg.Paths.LogDir,
				cfg.Paths.ArtifactDir,
				cfg.Paths.BaseConfig,
				workspaceBaseConfigDir,
				entry.DefinitionID,
				dag,
				runID,
				triggerType,
				scheduleTime,
				profileName,
			)
		}
	}

	planner := NewTickPlanner(TickPlannerConfig{
		StateStore:      stateStore,
		IsSuspended:     isSuspended,
		GetLatestStatus: drm.GetLatestStatus,
		IsRunning: func(ctx context.Context, dag *ir.DAG) (bool, error) {
			count, err := procRepository.CountAliveByDAGName(ctx, dag.ProcGroup(), dag.Name)
			if err != nil {
				return false, err
			}
			return count > 0, nil
		},
		GenRunID: func(context.Context) (string, error) {
			return ir.NewDAGRunID()
		},
		Dispatch: func(ctx context.Context, entry DAGEntry, runID string, triggerType ir.TriggerType, scheduleTime time.Time) error {
			return dagExecutor.HandleJob(
				ctx, entry,
				dispatch.DispatchOperationStart,
				runID, triggerType, scheduleTime,
			)
		},
		Stop: func(ctx context.Context, dag *ir.DAG) error {
			return drm.Stop(ctx, dag, "")
		},
		Restart: func(ctx context.Context, entry DAGEntry, scheduleTime time.Time) error {
			return dagExecutor.Restart(ctx, entry, scheduleTime)
		},
		Clock:           defaultClock,
		Location:        timeLoc,
		Events:          eventCh,
		ProfileResolver: profileResolver,
		QueuesEnabled:   queuesEnabled,
		Enqueue:         enqueueFunc,
		IsQueued:        isQueued,
		RunExists: func(ctx context.Context, dag *ir.DAG, runID string) (bool, error) {
			_, err := dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, runID))
			switch {
			case err == nil:
				return true, nil
			case errors.Is(err, dagrun.ErrDAGRunIDNotFound):
				return false, nil
			case errors.Is(err, dagrun.ErrNoStatusData):
				return true, nil
			default:
				return false, err
			}
		},
	})

	retryScanner, err := NewRetryScanner(
		dagRunRepository,
		queueStore,
		isSuspended,
		cfg.Scheduler.RetryFailureWindow,
		defaultClock,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize retry scanner: %w", err)
	}

	return &Scheduler{
		quit:             make(chan any),
		entryReader:      er,
		dagRunRepository: dagRunRepository,
		queueStore:       queueStore,
		procRepository:   procRepository,
		config:           cfg,
		dirLock:          dirLock,
		dagExecutor:      dagExecutor,
		healthServer:     healthServer,
		serviceRegistry:  reg,
		queueProcessor:   processor,
		retryScanner:     retryScanner,
		planner:          planner,
		clock:            defaultClock,
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

// SetOpenCodeHost enables the process-owned managed OpenCode service.
func (s *Scheduler) SetOpenCodeHost(host *opencodehost.Host) {
	s.dagExecutor.openCodeHost = host
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
}

func (s *Scheduler) updateServiceStatus(ctx context.Context, status serviceregistry.ServiceStatus, failureMsg, successMsg string) {
	if s.serviceRegistry == nil {
		return
	}
	if err := s.serviceRegistry.UpdateStatus(ctx, serviceregistry.ServiceNameScheduler, status); err != nil {
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

func (s *Scheduler) setQueueWatcher(w queuedomain.QueueWatcher) {
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
		hostInfo := serviceregistry.HostInfo{
			ID:        s.instanceID,
			Host:      hostname,
			Port:      s.config.Scheduler.Port, // Health check port (0 if disabled)
			Status:    serviceregistry.ServiceStatusInactive,
			StartedAt: time.Now(),
		}
		if err := s.serviceRegistry.Register(ctx, serviceregistry.ServiceNameScheduler, hostInfo); err != nil {
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

	s.updateServiceStatus(ctx, serviceregistry.ServiceStatusActive, "Failed to update status to active", "Updated scheduler status to active")
	monitorCursor := s.captureMonitorCursor(ctx)

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
	if err := s.planner.Init(ctx, s.entryReader.Entries()); err != nil {
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
		s.startNotificationMonitor(ctx, monitorCursor)
	})

	wg.Go(func() {
		s.startIncidentMonitor(ctx, monitorCursor)
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
		s.dagRunRepository,
		s.procRepository,
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
	s.eventCollector(ctx)
}

func (s *Scheduler) startNotificationMonitor(ctx context.Context, cursor *eventstore.DAGRunCursor) {
	if s.notificationMonitor == nil {
		return
	}
	s.notificationMonitor(ctx, cursor)
}

func (s *Scheduler) startIncidentMonitor(ctx context.Context, cursor *eventstore.DAGRunCursor) {
	if s.incidentMonitor == nil {
		return
	}
	s.incidentMonitor(ctx, cursor)
}

func (s *Scheduler) captureMonitorCursor(ctx context.Context) *eventstore.DAGRunCursor {
	if s.eventService == nil || (s.notificationMonitor == nil && s.incidentMonitor == nil) {
		return nil
	}
	cursor, err := s.eventService.DAGRunHeadCursor(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to capture notification cursor", tag.Error(err))
		return nil
	}
	return &cursor
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
		s.runTickSafely(ctx, tickTime)
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

func (s *Scheduler) runTickSafely(ctx context.Context, tickTime time.Time) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error(ctx, "Scheduler tick panicked",
				slog.Time("tick_time", tickTime),
				tag.Error(panicToError(r)),
			)
		}
	}()
	s.runTick(ctx, tickTime)
}

func (s *Scheduler) runTick(ctx context.Context, tickTime time.Time) {
	for _, run := range s.planner.Plan(ctx, tickTime) {
		s.dispatchRun(ctx, run)
	}
	s.planner.Advance(tickTime)
	s.planner.Flush(ctx)
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
	})
}

func (s *Scheduler) stopCron(ctx context.Context) {
	s.updateServiceStatus(ctx, serviceregistry.ServiceStatusInactive, "Failed to update status to inactive", "")
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
	if run.TriggerType == ir.TriggerTypeCatchUp {
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
