// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/license"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	incidentservice "github.com/dagucloud/dagu/v2/internal/service/incident"
	notificationservice "github.com/dagucloud/dagu/v2/internal/service/notification"
)

func newNotificationMonitor(cfg *config.Config, deps Dependencies) func(context.Context, *eventstore.DAGRunCursor) {
	if deps.NotificationStore == nil || deps.NotificationState == nil {
		return nil
	}
	var lease chatbridge.Lease
	if deps.NewNotificationLease != nil {
		lease = deps.NewNotificationLease()
	}
	service := newNotificationService(cfg, deps.NotificationStore, deps.DAGRepository)
	return func(ctx context.Context, cursor *eventstore.DAGRunCursor) {
		monitorConfig := chatbridge.DefaultNotificationMonitorConfig()
		monitorConfig.BootstrapCursor = cursor
		chatbridge.NewNotificationMonitor(
			deps.EventService,
			deps.NotificationState,
			lease,
			service,
			slog.Default(),
			monitorConfig,
		).Run(ctx)
	}
}

func newNotificationService(
	cfg *config.Config,
	store notificationmodel.Store,
	dagRepository *persis.DAGRepository,
	opts ...notificationservice.Option,
) *notificationservice.Service {
	opts = append([]notificationservice.Option{
		notificationservice.WithPublicURL(cfg.Server.PublicURL),
	}, opts...)
	return notificationservice.New(store, dagRepository, opts...)
}

func newIncidentMonitor(cfg *config.Config, deps Dependencies) func(context.Context, *eventstore.DAGRunCursor) {
	if deps.IncidentStore == nil || deps.IncidentState == nil {
		return nil
	}
	var lease chatbridge.Lease
	if deps.NewIncidentLease != nil {
		lease = deps.NewIncidentLease()
	}
	var checker license.Checker
	if deps.LicenseManager != nil {
		checker = deps.LicenseManager.Checker()
	}
	service := incidentservice.New(
		deps.IncidentStore,
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
	return func(ctx context.Context, cursor *eventstore.DAGRunCursor) {
		monitorConfig.BootstrapCursor = cursor
		chatbridge.NewNotificationMonitor(
			deps.EventService,
			deps.IncidentState,
			lease,
			service,
			slog.Default(),
			monitorConfig,
		).Run(ctx)
	}
}
