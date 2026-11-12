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

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/signalctx"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
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
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/publicwebhook"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/samples"
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
	queuedomain.RunProcesses
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
	dagRepository       *persis.DAGRepository
	dagRunRepository    *persis.DAGRunRepository
	queueStore          queuedomain.QueueStore
	procRepository      processRepository
	config              *config.Config
	lease               *lockLease // Renews dirLock while this instance is active
	lockLost            atomic.Pointer[error]
	fenceExit           func(error)
	fenceExitAfter      time.Duration
	fenceMu             sync.Mutex
	fenceWatchdog       *time.Timer
	startReturned       bool
	dirLock             dirlock.DirLock // File-based lock to prevent multiple scheduler instances
	dagExecutor         *DAGExecutor
	healthServer        *HealthServer // Health check server for monitoring
	serviceRegistry     serviceregistry.ServiceRegistry
	disableHealthServer bool            // Disable health server when running from start-all
	disableSignals      bool            // Leave OS signals to start-all
	zombieDetector      *ZombieDetector // Zombie DAG run detector
	instanceID          string          // Unique instance identifier for service registry
	queueProcessor      *QueueProcessor // Processor for queued DAG runs
	queueWatcher        queuedomain.QueueWatcher
	retryScanner        *RetryScanner // DAG-level retry scanner
	planner             *TickPlanner  // Unified scheduling decision module
	restartScanner      *RestartScanner
	pauseStore          schedulerstate.PauseStore
	backfills           *backfill.Dispatcher
	stopOnce            sync.Once
	lock                sync.Mutex
	lifecycleMu         sync.Mutex
	startupCancel       context.CancelFunc
	lockHeld            atomic.Bool
	clock               Clock    // Clock function for getting current time
	wakeHold            wakeHold // The hold on the ticks after the host wakes (wake_hold.go)
	eventCollector      func(context.Context)
	notificationMonitor *chatbridge.NotificationMonitor
	incidentMonitor     *chatbridge.NotificationMonitor
	publicWebhooks      *publicwebhook.Service
	folderWatcher       *folderWatcher // Starts runs for files arriving in watched folders
}

type schedulerHooks struct {
	onLockWait func()
}

// Dependencies contains the stores and services used by Scheduler.
type Dependencies struct {
	EntryReader         EntryReader
	DAGRunManager       runtime.Manager
	DAGRepository       *persis.DAGRepository
	DAGRunRepository    *persis.DAGRunRepository
	QueueStore          queuedomain.QueueStore
	ProcRepository      *persis.ProcRepository
	ServiceRegistry     serviceregistry.ServiceRegistry
	CoordinatorClient   dispatch.Dispatcher
	SchedulerStateStore schedulerstate.Store
	SchedulerPauseStore schedulerstate.PauseStore
	RestartBackoffStore schedulerstate.RestartBackoffStore
	// BackfillStore holds the backfills the scheduler runs. Nil runs none.
	BackfillStore        backfill.Store
	DAGRunLeaseStore     dispatch.DAGRunLeaseStore
	DispatchTaskStore    dispatch.DispatchTaskStore
	WorkerHeartbeatStore dispatch.WorkerHeartbeatStore
	WorkerStaleAfter     time.Duration
	// LeaseStaleAfter overrides how long a distributed run lease counts toward
	// queue concurrency after its last heartbeat. Zero keeps the default.
	LeaseStaleAfter      time.Duration
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
	// Webhooks, PublicWebhookStore, and CloudCredentials enable public
	// webhook URLs; the feature is off unless all are set.
	Webhooks           publicwebhook.Webhooks
	PublicWebhookStore publicwebhook.Store
	CloudCredentials   func() (license.CloudCredentials, bool)
	// WatchStore remembers the files watched folders held across restarts;
	// nil remembers them for the process only.
	WatchStore schedulerstate.WatchStore
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
	var profileResolver DAGProfileResolver
	if deps.DAGSettingsStore != nil {
		profileResolver = NewDAGProfileResolver(deps.DAGSettingsStore, deps.ProfileStore)
	}
	scheduler, err := newScheduler(
		cfg,
		deps.EntryReader,
		deps.DAGRunManager,
		deps.DAGRepository,
		deps.DAGRunRepository,
		deps.QueueStore,
		deps.ProcRepository,
		deps.ServiceRegistry,
		deps.CoordinatorClient,
		deps.SchedulerStateStore,
		deps.SchedulerPauseStore,
		schedulerHooks{},
		profileResolver,
	)
	if err != nil {
		return nil, err
	}
	scheduler.eventCollector = deps.EventCollector
	scheduler.restartScanner.cfg.BackoffStore = deps.RestartBackoffStore
	scheduler.backfills = scheduler.newBackfillDispatcher(deps.BackfillStore)
	if scheduler.backfills != nil {
		scheduler.planner.cfg.BackfillGuard = scheduler.guardCatchupSlot
	}
	if deps.EventService != nil {
		scheduler.notificationMonitor = newNotificationMonitor(cfg, deps)
		scheduler.incidentMonitor = newIncidentMonitor(cfg, deps)
		scheduler.restartScanner.events = deps.EventService
	}
	scheduler.publicWebhooks = newPublicWebhooks(cfg, deps, scheduler)
	scheduler.folderWatcher.cfg.Store = deps.WatchStore
	scheduler.queueProcessor.dagRunLeaseStore = deps.DAGRunLeaseStore
	scheduler.queueProcessor.dispatchTaskStore = deps.DispatchTaskStore
	scheduler.queueProcessor.dispatchAdmissionStore = dispatchAdmissionStoreFromTaskStore(deps.DispatchTaskStore)
	scheduler.queueProcessor.workerHeartbeatStore = deps.WorkerHeartbeatStore
	if deps.WorkerStaleAfter != 0 {
		scheduler.queueProcessor.workerStaleAfter = deps.WorkerStaleAfter
	}
	if deps.LeaseStaleAfter != 0 {
		scheduler.queueProcessor.leaseStaleThreshold = deps.LeaseStaleAfter
	}
	if deps.DAGRunLeaseStore != nil {
		scheduler.restartScanner.cfg.HasDistributedRun = hasFreshLease(deps.DAGRunLeaseStore, scheduler.queueProcessor.leaseStaleThresholdOrDefault())
	}
	if deps.DispatchTaskStore != nil {
		scheduler.restartScanner.cfg.IsDispatchPending = hasPendingDispatch(deps.DispatchTaskStore, scheduler.queueProcessor.leaseStaleThresholdOrDefault())
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
	pauseStore schedulerstate.PauseStore,
	hooks schedulerHooks,
	profileResolver DAGProfileResolver,
) (*Scheduler, error) {
	if dagRepository == nil {
		return nil, fmt.Errorf("DAG repository is required")
	}
	if dagRunRepository == nil {
		return nil, fmt.Errorf("DAG-run repository is required")
	}
	if er == nil {
		return nil, fmt.Errorf("DAG entry reader is required")
	}
	timeLoc := cfg.Core.Location
	if timeLoc == nil {
		timeLoc = time.Local
	}
	var tookOverStaleLock atomic.Bool
	lockOpts := &dirlock.LockOptions{
		StaleThreshold: cfg.Scheduler.LockStaleThreshold,
		RetryInterval:  cfg.Scheduler.LockRetryInterval,
		OnWait:         hooks.onLockWait,
		// Shared storage such as NFS caches directory attributes for longer
		// than the stale threshold, and hosts can disagree on the time.
		ObserveHeartbeat: true,
		OnRetryError: func(err error) {
			logger.Warn(context.Background(), "Failed to check scheduler lock, will retry", tag.Error(err))
		},
		OnStaleTakeover: func() {
			tookOverStaleLock.Store(true)
			logger.Warn(context.Background(), "Took over the scheduler lock from a failed instance")
		},
	}
	lockDir := filepath.Join(persisfile.SchedulerStateDir(cfg.Paths), "locks")
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
	isSuspended := newSuspensionChecker(pauseStore, dagRepository.IsSuspended)
	processor := NewQueueProcessor(
		queueStore,
		dagRunRepository,
		procRepository,
		dagExecutor,
		cfg.Queues,
		WithIsSuspended(isSuspended),
		WithIsPaused(newPauseChecker(pauseStore)),
	)
	defaultClock := Clock(time.Now)

	// Build catchup-enqueue callbacks when queues are enabled.
	queuesEnabled := cfg.Queues.Enabled
	var isQueued IsQueuedFunc
	var enqueueFunc EnqueueFunc
	liveRun := liveRunChecker(dagRunRepository)
	if queuesEnabled {
		isQueued = liveQueuedChecker(queueStore, liveRun)
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
				ir.ScheduleWindowAt(dag.Schedule, profileName, timeLoc, scheduleTime),
				profileName,
			)
		}
	}

	isRunning := liveRunningChecker(procRepository, liveRun)
	runExists := func(ctx context.Context, dag *ir.DAG, runID string) (bool, error) {
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
	}

	planner := NewTickPlanner(TickPlannerConfig{
		StateStore:      stateStore,
		IsSuspended:     isSuspended,
		GetLatestStatus: drm.GetLatestStatus,
		IsRunning:       isRunning,
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
		DataDir:         cfg.Paths.DataDir,
		Events:          eventCh,
		ProfileResolver: profileResolver,
		QueuesEnabled:   queuesEnabled,
		Enqueue:         enqueueFunc,
		HasGlobalQueue: func(dag *ir.DAG) bool {
			return cfg.FindQueueConfig(dag.ProcGroup()) != nil
		},
		IsQueued:          isQueued,
		RunExists:         runExists,
		RecordSkippedSlot: dagRunRepository.RecordSkippedSlot,
		RecoveryWindow: func() time.Duration {
			if tookOverStaleLock.Load() {
				return cfg.Scheduler.FailoverRecoveryWindow
			}
			return 0
		},
	})

	// The restart decision follows from the latest run, whatever its day. A
	// lookup limited to today finds no run for a DAG that ran yesterday, takes
	// it for a first run, and then finds the deterministic first-run ID taken:
	// the DAG would never restart.
	allHistory := drm.AllHistory()
	restartScanner := NewRestartScanner(RestartScannerConfig{
		Entries:         er.Entries,
		IsSuspended:     isSuspended,
		IsRunning:       isRunning,
		IsQueued:        isQueued,
		GetLatestStatus: allHistory.ReadLatestStatus,
		RecentRuns: func(ctx context.Context, dag *ir.DAG) ([]ir.DAGRunStatus, error) {
			return dagRunRepository.RecentStatuses(ctx, dag.Name, restartRecentRuns)
		},
		IsStopRequested: func(ctx context.Context, dag *ir.DAG, runID string) (bool, error) {
			return IsStopRequested(ctx, dagRunRepository, dag, runID)
		},
		FailStuckRun: func(ctx context.Context, dag *ir.DAG, status *ir.DAGRunStatus, at time.Time) error {
			_, _, err := dagRunRepository.CompareAndSwapLatestAttemptStatus(ctx,
				ir.NewDAGRunRef(dag.Name, status.DAGRunID), status.AttemptID, status.Status,
				func(latest *ir.DAGRunStatus) error {
					latest.Status = ir.Failed
					latest.FinishedAt = stringutil.FormatTime(at)
					latest.Error = stuckRestartRunError
					latest.WorkerID = ""
					latest.PID = 0
					latest.PIDStartedAt = 0
					latest.LeaseAt = 0
					return nil
				}, persis.DAGRunCompareAndSwapOptions{},
			)
			return err
		},
		Enqueue: enqueueFunc,
		History: NewServiceHistory(ServiceHistoryConfig{
			Dir: dagRunRepository.RunHistoryDir,
			RecentRuns: func(ctx context.Context, dagName string, limit int) ([]ir.DAGRunStatus, error) {
				return dagRunRepository.RecentStatuses(ctx, dagName, limit)
			},
			Open:      samples.OpenRecorder,
			NewestEnd: samples.NewestEnd,
			Clock:     defaultClock,
		}),
		Clock: defaultClock,
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
	retryScanner.processes = procRepository
	retryScanner.isPaused = newPauseChecker(pauseStore)

	s := &Scheduler{
		quit:             make(chan any),
		fenceExit:        exitOnLostLock,
		fenceExitAfter:   defaultFenceExitAfter,
		entryReader:      er,
		dagRepository:    dagRepository,
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
		restartScanner:   restartScanner,
		planner:          planner,
		clock:            defaultClock,
		wakeHold:         newWakeHold(cfg.Scheduler.WakeHold, pauseStore),
		pauseStore:       pauseStore,
	}
	s.folderWatcher = newFolderWatcher(folderWatcherConfig{
		Entries:     er.Entries,
		IsSuspended: isSuspended,
		Enqueue:     s.enqueueFileRun,
		Clock:       func() time.Time { return s.clock() },
	})
	return s, nil
}

// SetClock sets a custom clock function for testing purposes.
// This must be called before Start().
func (s *Scheduler) SetClock(clock Clock) {
	s.clock = clock
	s.planner.cfg.Clock = clock
	if s.retryScanner != nil {
		s.retryScanner.clock = clock
	}
	if s.restartScanner != nil {
		s.restartScanner.cfg.Clock = clock
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

// DisableSignalHandling leaves OS signals to the hosting process, which stops
// the scheduler through its context and Stop instead.
func (s *Scheduler) DisableSignalHandling() {
	s.disableSignals = true
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

func (s *Scheduler) healthRole() string {
	if s.lockHeld.Load() {
		return healthRoleActive
	}
	return healthRoleStandby
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

func (s *Scheduler) Start(ctx context.Context) (err error) {
	defer func() {
		s.disarmFenceWatchdog()
		if err == nil {
			err = s.lockLostError()
		}
	}()

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
		s.healthServer.SetRoleFunc(s.healthRole)
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

	// Catch signals before reporting the lock as held. A signal during the rest
	// of startup waits here for the cron loop, which stops cleanly and releases
	// the lock; without this it would kill the process and leave the lock to go
	// stale.
	sig := make(chan os.Signal, 1)
	if !s.disableSignals {
		signal.Notify(sig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
		defer signal.Stop(sig)
	}

	s.lockHeld.Store(true)
	state.lockAcquired = true

	logger.Info(ctx, "Acquired scheduler lock")

	if ctx.Err() != nil && s.stopping() {
		return nil
	}

	s.lease = newLockLease(s.dirLock, s.config.Scheduler.LockStaleThreshold)

	var wg sync.WaitGroup
	wg.Go(func() {
		s.startHeartbeat(ctx)
	})

	if err := s.dagRepository.MigrateSuspensionState(ctx); err != nil {
		if errors.Is(err, context.Canceled) && s.stopping() {
			return nil
		}
		return fmt.Errorf("migrate suspension state: %w", err)
	}
	if err := ctx.Err(); err != nil {
		if s.stopping() {
			return nil
		}
		return err
	}

	s.updateServiceStatus(ctx, serviceregistry.ServiceStatusActive, "Failed to update status to active", "Updated scheduler status to active")
	if err := s.BootstrapMonitors(ctx); err != nil {
		return err
	}

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
		s.startRestartScanner(ctx)
	})

	wg.Go(func() {
		s.startBackfillDispatcher(ctx)
	})

	wg.Go(func() {
		s.startEventCollector(ctx)
	})

	wg.Go(func() {
		s.startNotificationMonitor(ctx)
	})

	wg.Go(func() {
		s.startIncidentMonitor(ctx)
	})

	wg.Go(func() {
		s.startPublicWebhooks(ctx)
	})

	wg.Go(func() {
		s.folderWatcher.Start(ctx)
	})

	wg.Go(func() {
		s.entryReader.Start(ctx)
	})

	// A hold a previous process left behind would pause this one for nobody.
	s.wakeHold.clearStale(ctx)

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

// BootstrapMonitors persists the shared event boundary before producers start.
func (s *Scheduler) BootstrapMonitors(ctx context.Context) error {
	if s.notificationMonitor != nil {
		if err := s.notificationMonitor.Bootstrap(ctx); err != nil {
			return fmt.Errorf("bootstrap notification monitor: %w", err)
		}
	}

	if s.incidentMonitor != nil {
		if err := s.incidentMonitor.Bootstrap(ctx); err != nil {
			return fmt.Errorf("bootstrap incident monitor: %w", err)
		}
	}
	return nil
}

func (s *Scheduler) startNotificationMonitor(ctx context.Context) {
	if s.notificationMonitor == nil {
		return
	}
	s.notificationMonitor.Run(ctx)
}

func (s *Scheduler) startIncidentMonitor(ctx context.Context) {
	if s.incidentMonitor == nil {
		return
	}
	s.incidentMonitor.Run(ctx)
}

func (s *Scheduler) startPublicWebhooks(ctx context.Context) {
	if s.publicWebhooks == nil {
		return
	}
	s.publicWebhooks.Run(ctx)
}

func (s *Scheduler) startHeartbeat(ctx context.Context) {
	if err := s.lease.run(ctx, s.quit); err != nil {
		s.selfFence(ctx, err)
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

// hasFreshLease reports whether a DAG has a run whose distributed lease is
// fresh, which is how a run on a worker shows it is alive.
func hasFreshLease(leases dispatch.DAGRunLeaseStore, staleAfter time.Duration) IsRunningFunc {
	return func(ctx context.Context, dag *ir.DAG) (bool, error) {
		all, err := leases.ListAll(ctx)
		if err != nil {
			return false, err
		}
		now := time.Now().UTC()
		for _, lease := range all {
			if lease.DAGRun.Name == dag.Name && lease.IsFresh(now, staleAfter) {
				return true, nil
			}
		}
		return false, nil
	}
}

// hasPendingDispatch reports whether the attempt of a run has a pending or
// claimed dispatch task, which is how a run waiting in the coordinator shows it
// will still start.
func hasPendingDispatch(tasks dispatch.DispatchTaskStore, claimTimeout time.Duration) func(context.Context, *ir.DAGRunStatus) (bool, error) {
	return func(ctx context.Context, status *ir.DAGRunStatus) (bool, error) {
		attemptKey := dispatch.AttemptKeyForStatus(status, status.AttemptID)
		if attemptKey == "" {
			return false, nil
		}
		return tasks.HasOutstandingAttempt(ctx, attemptKey, claimTimeout)
	}
}

func (s *Scheduler) startRestartScanner(ctx context.Context) {
	if s.restartScanner == nil {
		return
	}

	logger.Info(ctx, "Started restart scanner", tag.Interval(restartScanInterval))
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

	s.restartScanner.Start(scanCtx)
}

// cronLoop runs the main scheduler loop to invoke jobs at scheduled times.
func (s *Scheduler) cronLoop(ctx context.Context, sig chan os.Signal) {
	tickTime := s.clock().Truncate(time.Minute)
	timer := time.NewTimer(0)
	defer timer.Stop()

	s.running.Store(true)
	defer s.running.Store(false)

	for s.waitForTick(ctx, sig, timer) {
		now := s.clock()
		if now.Before(tickTime) {
			// A clock adjustment must not dispatch a future scheduled slot.
			timer.Reset(tickTime.Sub(now))
			continue
		}
		switch outcome, wait, since := s.wakeHold.check(ctx, tickTime, now); outcome {
		case tickLater:
			timer.Reset(wait)
			continue
		case tickHeld:
			tickTime = s.runHeldTicks(ctx, since)
		case tickNow:
			tickTime = s.runTickSafely(ctx, tickTime)
		}
		tickTime = s.NextTick(tickTime)
		timer.Reset(tickTime.Sub(s.clock()))
	}
}

func (s *Scheduler) waitForTick(ctx context.Context, sig chan os.Signal, timer *time.Timer) bool {
	select {
	case <-ctx.Done():
		signal.Stop(sig)
		var received os.Signal
		_ = errors.As(context.Cause(ctx), &received)
		<-launcher.PropagateSignal(ctx, received)
		return false
	case received := <-sig:
		signalctx.AbsorbRepeatedTerminate(ctx)
		signal.Stop(sig)
		runsDone := launcher.PropagateSignal(ctx, received)
		s.Stop(ctx)
		<-runsDone
		return false
	case <-s.quit:
		return false
	case <-timer.C:
		_ = timer.Stop()
		return true
	}
}

func (s *Scheduler) runTickSafely(ctx context.Context, tickTime time.Time) (processed time.Time) {
	processed = tickTime
	if s.leaseExpired(ctx) {
		return processed
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Error(ctx, "Scheduler tick panicked",
				slog.Time("tick_time", tickTime),
				tag.Error(panicToError(r)),
			)
		}
	}()
	if current := s.clock().Truncate(time.Minute); current.After(tickTime) {
		// Recover missed slots before the next tick advances the checkpoint.
		s.planner.resume(ctx, current)
		processed = current
	}
	s.runTick(ctx, processed)
	return processed
}

func (s *Scheduler) runTick(ctx context.Context, tickTime time.Time) {
	for _, run := range s.planner.Plan(ctx, tickTime) {
		s.dispatchRun(ctx, run)
	}
	s.planner.Advance(tickTime)
	s.planner.Flush(ctx)
}

// runHeldTicks runs the ticks a wake hold kept back, from the minute the
// host woke in through the minute under way, each run dispatched before the
// next tick so that tick sees it. What the host slept through is recovered
// first, as a late tick recovers it. It returns the last tick run.
func (s *Scheduler) runHeldTicks(ctx context.Context, since time.Time) (processed time.Time) {
	processed = since.Truncate(time.Minute)
	if s.leaseExpired(ctx) {
		return processed
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Error(ctx, "Scheduler tick panicked",
				slog.Time("tick_time", processed),
				tag.Error(panicToError(r)),
			)
		}
	}()
	s.planner.resume(ctx, processed)
	last := s.clock().Truncate(time.Minute)
	for tick := processed; !tick.After(last); tick = tick.Add(time.Minute) {
		processed = tick
		for _, run := range s.planner.Plan(ctx, tick) {
			s.dispatchPlannedRun(ctx, run)
		}
		s.planner.Advance(tick)
		s.planner.Flush(ctx)
	}
	return processed
}

// leaseExpired reports whether the scheduler must not dispatch, because another
// instance may already be active. It catches a process that was paused long
// enough to lose its lease and has not yet noticed.
func (s *Scheduler) leaseExpired(ctx context.Context) bool {
	if s.lease == nil || !s.lease.expired() {
		return false
	}
	logger.Warn(ctx, "Skipping scheduler tick: scheduler lock lease expired")
	return true
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
// enqueueFileRun queues the run of a file that arrived in a watched folder.
func (s *Scheduler) enqueueFileRun(ctx context.Context, entry DAGEntry, runID, params string) error {
	profileName, err := s.dagExecutor.defaultProfileName(ctx, entry.DefinitionID, entry.DAG)
	if err != nil {
		return fmt.Errorf("failed to resolve DAG profile: %w", err)
	}
	return enqueueTriggeredRun(
		ctx,
		s.dagRunRepository,
		s.queueStore,
		s.config.Paths.LogDir,
		s.config.Paths.ArtifactDir,
		s.config.Paths.BaseConfig,
		workspace.BaseConfigDir(s.config.Paths.DAGsDir),
		entry.DefinitionID,
		entry.DAG,
		runID,
		params,
		profileName,
		s.clock(),
		ir.TriggerTypeFile,
		errInvalidFileRun,
	)
}

// errInvalidFileRun says a watched DAG, or the file's params, cannot start a
// run.
var errInvalidFileRun = errors.New("the file cannot start a run")

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
