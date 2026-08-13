// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/license"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	incidentservice "github.com/dagucloud/dagu/v2/internal/service/incident"
	notificationservice "github.com/dagucloud/dagu/v2/internal/service/notification"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// SchedulerConfig contains the wiring needed to construct the scheduler process role.
type SchedulerConfig struct {
	Context           context.Context
	Config            *config.Config
	DAGRepository     *persis.DAGRepository
	DAGRunRepository  *persis.DAGRunRepository
	QueueStore        queue.QueueStore
	ProcRepository    *proc.Repository
	ServiceRegistry   serviceregistry.ServiceRegistry
	DispatchTaskStore dispatch.DispatchTaskStore
	DAGRunLeaseStore  dispatch.DAGRunLeaseStore
	EventService      *eventstore.Service
	LicenseManager    *license.Manager
}

// NewScheduler creates the scheduler from the process repositories and services.
func NewScheduler(cfg SchedulerConfig) (*scheduler.Scheduler, error) {
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}

	coordinatorClient := NewCoordinatorClient(ctx, cfg.Config, cfg.ServiceRegistry)
	entryReader := scheduler.NewFileEntryReader(
		cfg.Config.Paths.DAGsDir,
		cfg.DAGRepository,
		cfg.Config.DAGDiscovery.Recursive,
	)
	watermarkStore := scheduler.NewWatermarkStore(
		file.NewCollection(filepath.Join(cfg.Config.Paths.DataDir, "scheduler"), file.WithIndentedJSON()),
	)

	schedulerRunManager := runtime.NewManager(
		cfg.DAGRunRepository,
		cfg.ProcRepository,
		cfg.Config,
		runtime.WithLatestStatusAllHistory(),
	)

	dagSettingsStore, err := file.NewDAGSettingsStore(cfg.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize DAG settings store: %w", err)
	}
	profileStore := file.NewProfileStore(ctx, cfg.Config)

	sched, err := scheduler.New(
		cfg.Config,
		entryReader,
		schedulerRunManager,
		cfg.DAGRepository,
		cfg.DAGRunRepository,
		cfg.QueueStore,
		cfg.ProcRepository,
		cfg.ServiceRegistry,
		coordinatorClient,
		watermarkStore,
		scheduler.WithDAGProfileResolver(scheduler.NewDAGProfileResolver(dagSettingsStore, profileStore)),
	)
	if err != nil {
		return nil, err
	}

	if cfg.EventService != nil {
		collector, eventErr := file.NewEventCollector(cfg.Config)
		if eventErr != nil {
			logger.Warn(ctx, "Failed to initialize event collector; continuing without collection", tag.Error(eventErr))
		} else {
			sched.SetEventCollector(collector)
		}
		if notificationMonitor := newNotificationMonitor(ctx, cfg.Config, cfg.DAGRepository, cfg.EventService); notificationMonitor != nil {
			sched.SetNotificationMonitor(notificationMonitor)
		}
		if incidentMonitor := newIncidentMonitor(ctx, cfg.Config, cfg.LicenseManager, cfg.EventService); incidentMonitor != nil {
			sched.SetIncidentMonitor(incidentMonitor)
		}
	}

	sched.SetDAGRunLeaseStore(cfg.DAGRunLeaseStore)
	sched.SetDispatchTaskStore(cfg.DispatchTaskStore)

	return sched, nil
}

// newNotificationMonitor wires optional DAG notification delivery. It returns nil
// when encrypted settings storage is unavailable so scheduler startup can continue.
func newNotificationMonitor(
	ctx context.Context,
	cfg *config.Config,
	dagRepository *persis.DAGRepository,
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
	store, err := file.NewNotificationStore(cfg, encryptor)
	if err != nil {
		logger.Warn(ctx, "Failed to create notification settings store", tag.Error(err))
		return nil
	}
	notificationService := newSchedulerNotificationService(cfg, store, dagRepository)
	stateFile := file.NotificationMonitorStateFile(cfg)
	return chatbridge.NewNotificationMonitor(
		eventService,
		stateFile,
		notificationService,
		slog.Default(),
		chatbridge.DefaultNotificationMonitorConfig(),
	)
}

func newSchedulerNotificationService(
	cfg *config.Config,
	store notificationmodel.Store,
	dagRepository *persis.DAGRepository,
	opts ...notificationservice.Option,
) *notificationservice.Service {
	opts = append([]notificationservice.Option{
		notificationservice.WithPublicURL(cfg.Server.PublicURL),
	}, opts...)
	return notificationservice.New(
		store,
		dagRepository,
		opts...,
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
	store, err := file.NewIncidentStore(cfg, encryptor)
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
	stateFile := file.IncidentMonitorStateFile(cfg)
	monitorConfig := chatbridge.DefaultNotificationMonitorConfig()
	monitorConfig.UrgentWindow = time.Second
	monitorConfig.SuccessWindow = time.Second
	monitorConfig.InterestedEventTypes = []eventstore.EventType{
		eventstore.TypeDAGRunFailed,
		eventstore.TypeDAGRunSucceeded,
		eventstore.TypeDAGRunPartiallySucceeded,
	}
	return chatbridge.NewNotificationMonitor(
		eventService,
		stateFile,
		incidentService,
		slog.Default(),
		monitorConfig,
	)
}
