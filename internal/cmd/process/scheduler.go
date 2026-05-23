// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/persis/fileeventstore"
	"github.com/dagucloud/dagu/internal/persis/filegithubdispatch"
	"github.com/dagucloud/dagu/internal/persis/fileincident"
	"github.com/dagucloud/dagu/internal/persis/filenotification"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/service/chatbridge"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	incidentservice "github.com/dagucloud/dagu/internal/service/incident"
	notificationservice "github.com/dagucloud/dagu/internal/service/notification"
	"github.com/dagucloud/dagu/internal/service/scheduler"
)

// SchedulerConfig contains the wiring needed to construct the scheduler process role.
type SchedulerConfig struct {
	Context           context.Context
	Config            *config.Config
	QueueStore        exec.QueueStore
	ProcStore         exec.ProcStore
	ServiceRegistry   exec.ServiceRegistry
	DispatchTaskStore exec.DispatchTaskStore
	DAGRunLeaseStore  exec.DAGRunLeaseStore
	EventService      *eventstore.Service
	LicenseManager    *license.Manager
}

// NewScheduler creates the scheduler and its process-local stores, monitors, and workers.
func NewScheduler(cfg SchedulerConfig) (*scheduler.Scheduler, error) {
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}

	limits := cfg.Config.Cache.Limits()
	dagCache := fileutil.NewCache[*core.DAG]("dag_definition", limits.DAG.Limit, limits.DAG.TTL)
	dagCache.StartEviction(ctx)

	dagStore, err := NewDAGStore(cfg.Config, DAGStoreConfig{Cache: dagCache})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize DAG client: %w", err)
	}

	coordinatorClient := NewCoordinatorClient(ctx, cfg.Config, cfg.ServiceRegistry)
	entryReader := scheduler.NewEntryReader(cfg.Config.Paths.DAGsDir, dagStore)
	wmBackend, wmErr := file.New(cfg.Config.Paths.DataDir)
	if wmErr != nil {
		return nil, fmt.Errorf("failed to open file backend for watermark: %w", wmErr)
	}
	watermarkStore := store.NewWatermarkStore(wmBackend.Collection("scheduler"))

	statusCache := fileutil.NewCache[*exec.DAGRunStatus]("scheduler_dag_run_status", limits.DAGRun.Limit, limits.DAGRun.TTL)
	statusCache.StartEviction(ctx)
	schedulerRunStore := filedagrun.New(
		cfg.Config.Paths.DAGRunsDir,
		filedagrun.WithArtifactDir(cfg.Config.Paths.ArtifactDir),
		filedagrun.WithLatestStatusToday(false),
		filedagrun.WithLocation(cfg.Config.Core.Location),
		filedagrun.WithHistoryFileCache(statusCache),
	)
	schedulerRunManager := runtime.NewManager(schedulerRunStore, cfg.ProcStore, cfg.Config)

	sched, err := scheduler.New(
		cfg.Config,
		entryReader,
		schedulerRunManager,
		schedulerRunStore,
		cfg.QueueStore,
		cfg.ProcStore,
		cfg.ServiceRegistry,
		coordinatorClient,
		watermarkStore,
	)
	if err != nil {
		return nil, err
	}

	if cfg.EventService != nil {
		collector, eventErr := fileeventstore.NewCollector(cfg.Config.Paths.EventStoreDir, cfg.Config.EventStore.RetentionDays)
		if eventErr != nil {
			logger.Warn(ctx, "Failed to initialize event collector; continuing without collection", tag.Error(eventErr))
		} else {
			sched.SetEventCollector(collector)
		}
		if notificationMonitor := newNotificationMonitor(ctx, cfg.Config, dagStore, cfg.EventService); notificationMonitor != nil {
			sched.SetNotificationMonitor(notificationMonitor)
		}
		if incidentMonitor := newIncidentMonitor(ctx, cfg.Config, cfg.LicenseManager, cfg.EventService); incidentMonitor != nil {
			sched.SetIncidentMonitor(incidentMonitor)
		}
	}

	sched.SetDAGRunLeaseStore(cfg.DAGRunLeaseStore)
	sched.SetDispatchTaskStore(cfg.DispatchTaskStore)
	if cfg.LicenseManager != nil {
		githubTracker := filegithubdispatch.New(filepath.Join(cfg.Config.Paths.DataDir, "github-dispatch"))
		sched.SetGitHubDispatchWorker(scheduler.NewGitHubDispatchWorker(
			cfg.Config,
			dagStore,
			schedulerRunStore,
			cfg.QueueStore,
			&schedulerRunManager,
			cfg.LicenseManager,
			license.NewCloudClient(cfg.Config.License.CloudURL),
			githubTracker,
			logger.FromContext(ctx),
		))
	}

	return sched, nil
}

// newNotificationMonitor wires optional DAG notification delivery. It returns nil
// when encrypted settings storage is unavailable so scheduler startup can continue.
func newNotificationMonitor(
	ctx context.Context,
	cfg *config.Config,
	dagStore exec.DAGStore,
	eventService *eventstore.Service,
) *chatbridge.NotificationMonitor {
	encKey, encErr := crypto.ResolveKey(cfg.Paths.DataDir)
	if encErr != nil {
		logger.Warn(ctx, "Notification settings store is disabled because encrypted storage is not available", tag.Error(encErr))
		return nil
	}
	encryptor, encErr := crypto.NewEncryptor(encKey)
	if encErr != nil {
		logger.Warn(ctx, "Failed to create encryptor for notification settings store", tag.Error(encErr))
		return nil
	}
	store, err := filenotification.New(
		filepath.Join(cfg.Paths.DataDir, "notifications", "dags"),
		filenotification.WithEncryptor(encryptor),
	)
	if err != nil {
		logger.Warn(ctx, "Failed to create notification settings store", tag.Error(err))
		return nil
	}
	notificationService := notificationservice.New(
		store,
		dagStore,
	)
	stateFile := filepath.Join(cfg.Paths.DataDir, "notifications", "monitor-state.json")
	return chatbridge.NewNotificationMonitor(
		eventService,
		stateFile,
		notificationService,
		slog.Default(),
		chatbridge.DefaultNotificationMonitorConfig(),
	)
}

// newIncidentMonitor wires optional incident notifications. It returns nil when
// encrypted settings storage is unavailable so scheduler startup can continue.
func newIncidentMonitor(
	ctx context.Context,
	cfg *config.Config,
	licenseManager *license.Manager,
	eventService *eventstore.Service,
) *chatbridge.NotificationMonitor {
	encKey, encErr := crypto.ResolveKey(cfg.Paths.DataDir)
	if encErr != nil {
		logger.Warn(ctx, "Incident settings store is disabled because encrypted storage is not available", tag.Error(encErr))
		return nil
	}
	encryptor, encErr := crypto.NewEncryptor(encKey)
	if encErr != nil {
		logger.Warn(ctx, "Failed to create encryptor for incident settings store", tag.Error(encErr))
		return nil
	}
	store, err := fileincident.New(
		filepath.Join(cfg.Paths.DataDir, "incidents"),
		fileincident.WithEncryptor(encryptor),
	)
	if err != nil {
		logger.Warn(ctx, "Failed to create incident settings store", tag.Error(err))
		return nil
	}
	var checker license.Checker
	if licenseManager != nil {
		checker = licenseManager.Checker()
	}
	incidentService := incidentservice.New(
		store,
		incidentservice.WithIncidentsEnabled(func() bool {
			return license.HasActiveLicense(checker)
		}),
		incidentservice.WithPublicURL(cfg.Server.PublicURL),
	)
	stateFile := filepath.Join(cfg.Paths.DataDir, "incidents", "monitor-state.json")
	monitorConfig := chatbridge.DefaultNotificationMonitorConfig()
	monitorConfig.UrgentWindow = time.Second
	monitorConfig.SuccessWindow = time.Second
	monitorConfig.InterestedEventTypes = []eventstore.EventType{
		eventstore.TypeDAGRunFailed,
		eventstore.TypeDAGRunSucceeded,
	}
	return chatbridge.NewNotificationMonitor(
		eventService,
		stateFile,
		incidentService,
		slog.Default(),
		monitorConfig,
	)
}
