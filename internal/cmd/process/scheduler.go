// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/license"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	incidentservice "github.com/dagucloud/dagu/v2/internal/service/incident"
	notificationservice "github.com/dagucloud/dagu/v2/internal/service/notification"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
)

// SchedulerConfig contains the wiring needed to construct the scheduler process role.
type SchedulerConfig struct {
	Context        context.Context
	Config         *config.Config
	Persistence    CorePersistence
	Stores         Stores
	LicenseManager *license.Manager
}

// NewScheduler creates the scheduler from the process repositories and services.
func NewScheduler(cfg SchedulerConfig) (*scheduler.Scheduler, error) {
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}

	coordinatorClient := NewCoordinatorClient(ctx, cfg.Config, cfg.Persistence.ServiceRegistry)
	entryReader := scheduler.NewFileEntryReader(
		cfg.Config.Paths.DAGsDir,
		cfg.Persistence.DAGRepository,
		cfg.Config.DAGDiscovery.Recursive,
	)
	schedulerRunManager := runtime.NewManager(
		cfg.Persistence.DAGRunRepository,
		cfg.Persistence.ProcRepository,
		cfg.Config,
		runtime.WithLatestStatusAllHistory(),
	)

	if cfg.Stores.DAGSettings == nil {
		return nil, errors.New("DAG settings store is not configured")
	}

	sched, err := scheduler.New(
		cfg.Config,
		entryReader,
		schedulerRunManager,
		cfg.Persistence.DAGRepository,
		cfg.Persistence.DAGRunRepository,
		cfg.Persistence.QueueStore,
		cfg.Persistence.ProcRepository,
		cfg.Persistence.ServiceRegistry,
		coordinatorClient,
		cfg.Persistence.SchedulerStateStore,
		scheduler.WithDAGProfileResolver(scheduler.NewDAGProfileResolver(cfg.Stores.DAGSettings, cfg.Stores.Profile)),
	)
	if err != nil {
		return nil, err
	}

	if cfg.Stores.Event != nil {
		if cfg.Stores.EventCollector != nil {
			sched.SetEventCollector(cfg.Stores.EventCollector)
		}
		if notificationMonitor := newNotificationMonitor(cfg.Config, cfg.Persistence.DAGRepository, cfg.Stores); notificationMonitor != nil {
			sched.SetNotificationMonitor(notificationMonitor)
		}
		if incidentMonitor := newIncidentMonitor(cfg.Config, cfg.LicenseManager, cfg.Stores); incidentMonitor != nil {
			sched.SetIncidentMonitor(incidentMonitor)
		}
	}

	sched.SetDAGRunLeaseStore(cfg.Persistence.DAGRunLeaseStore)
	sched.SetDispatchTaskStore(cfg.Persistence.DispatchTaskStore)

	return sched, nil
}

// newNotificationMonitor wires optional DAG notification delivery.
func newNotificationMonitor(
	cfg *config.Config,
	dagRepository *persis.DAGRepository,
	stores Stores,
) *chatbridge.NotificationMonitor {
	if stores.Notification == nil || stores.NotificationState == nil {
		return nil
	}
	var lease chatbridge.Lease
	if stores.NewNotificationLease != nil {
		lease = stores.NewNotificationLease()
	}
	notificationService := newSchedulerNotificationService(cfg, stores.Notification, dagRepository)
	return chatbridge.NewNotificationMonitor(
		stores.Event,
		stores.NotificationState,
		lease,
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

// newIncidentMonitor wires optional incident notifications.
func newIncidentMonitor(
	cfg *config.Config,
	licenseManager *license.Manager,
	stores Stores,
) *chatbridge.NotificationMonitor {
	if stores.Incident == nil || stores.IncidentState == nil {
		return nil
	}
	var lease chatbridge.Lease
	if stores.NewIncidentLease != nil {
		lease = stores.NewIncidentLease()
	}
	var checker license.Checker
	if licenseManager != nil {
		checker = licenseManager.Checker()
	}
	incidentService := incidentservice.New(
		stores.Incident,
		incidentservice.WithIncidentsEnabled(func() bool {
			return license.HasActiveLicense(checker)
		}),
		incidentservice.WithPublicURL(cfg.Server.PublicURL),
	)
	monitorConfig := chatbridge.DefaultNotificationMonitorConfig()
	monitorConfig.UrgentWindow = time.Second
	monitorConfig.SuccessWindow = time.Second
	monitorConfig.InterestedEventTypes = []eventstore.EventType{
		eventstore.TypeDAGRunFailed,
		eventstore.TypeDAGRunSucceeded,
		eventstore.TypeDAGRunPartiallySucceeded,
	}
	return chatbridge.NewNotificationMonitor(
		stores.Event,
		stores.IncidentState,
		lease,
		incidentService,
		slog.Default(),
		monitorConfig,
	)
}
