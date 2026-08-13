// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	frontendfile "github.com/dagucloud/dagu/v2/internal/service/frontend/file"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	schedulerfile "github.com/dagucloud/dagu/v2/internal/service/scheduler/file"
)

func newFileStores(ctx context.Context, cfg *config.Config, commandName string) (frontend.Stores, scheduler.Dependencies, error) {
	switch commandName {
	case "server", "start-all":
		if commandName == "start-all" {
			if _, err := persisfile.NewDAGSettingsStore(cfg); err != nil {
				return frontend.Stores{}, scheduler.Dependencies{}, fmt.Errorf("failed to initialize DAG settings store: %w", err)
			}
		}
		stores, err := frontendfile.NewStores(ctx, cfg)
		if err != nil {
			return frontend.Stores{}, scheduler.Dependencies{}, err
		}
		var collector func(context.Context)
		if commandName == "start-all" && stores.Event != nil {
			eventCollector, err := persisfile.NewEventCollector(cfg)
			if err != nil {
				logger.Warn(ctx, "Failed to initialize event collector; continuing without collection", tag.Error(err))
			} else {
				collector = eventCollector.Start
			}
		}
		return stores, scheduler.Dependencies{
			DAGSettingsStore:     stores.DAGSettings,
			ProfileStore:         stores.Profile,
			EventService:         stores.Event,
			EventCollector:       collector,
			NotificationStore:    stores.Notification,
			NotificationState:    stores.NotificationState,
			NewNotificationLease: stores.NewNotificationLease,
			IncidentStore:        stores.Incident,
			IncidentState:        stores.IncidentState,
			NewIncidentLease:     stores.NewIncidentLease,
		}, nil
	case "scheduler":
		stores, err := schedulerfile.NewDependencies(ctx, cfg)
		if err != nil {
			return frontend.Stores{}, scheduler.Dependencies{}, err
		}
		return frontend.Stores{
			DAGSettings:          stores.DAGSettingsStore,
			Profile:              stores.ProfileStore,
			Event:                stores.EventService,
			Notification:         stores.NotificationStore,
			NotificationState:    stores.NotificationState,
			NewNotificationLease: stores.NewNotificationLease,
			Incident:             stores.IncidentStore,
			IncidentState:        stores.IncidentState,
			NewIncidentLease:     stores.NewIncidentLease,
		}, stores, nil
	case "worker":
		return frontend.Stores{}, scheduler.Dependencies{}, nil
	default:
		return newEventStores(ctx, cfg), scheduler.Dependencies{}, nil
	}
}

func newEventStores(ctx context.Context, cfg *config.Config) frontend.Stores {
	if !cfg.EventStore.Enabled {
		return frontend.Stores{}
	}
	store, err := persisfile.NewEventStore(cfg)
	if err != nil {
		logger.Warn(ctx, "Failed to initialize event store; continuing without event persistence", tag.Error(err))
		return frontend.Stores{}
	}
	if store == nil {
		return frontend.Stores{}
	}
	return frontend.Stores{Event: eventstore.New(store)}
}
