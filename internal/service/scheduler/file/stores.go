// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package file creates file-backed dependencies for the scheduler service.
package file

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
)

// NewDependencies creates the file-backed stores used by the scheduler
// service from the stores every project shares and the stores of the
// project it schedules.
func NewDependencies(
	ctx context.Context,
	cfg *config.Config,
	server *persisfile.ServerStores,
	project *persisfile.ProjectSettings,
) (scheduler.Dependencies, error) {
	var deps scheduler.Dependencies
	if cfg.EventStore.Enabled {
		store, err := fileeventstore.New(project.Paths.EventStoreDir)
		if err != nil {
			logger.Warn(ctx, "Failed to initialize event store; continuing without event persistence", tag.Error(err))
		} else {
			deps.EventService = eventstore.New(store)
		}
	}
	if deps.EventService != nil {
		collector, err := fileeventstore.NewCollector(
			project.Paths.EventStoreDir,
			cfg.EventStore.RetentionDays,
			fileeventstore.WithDedupeCacheBytes(cfg.Cache.Limits().EventStoreBytes),
		)
		if err != nil {
			logger.Warn(ctx, "Failed to initialize event collector; continuing without collection", tag.Error(err))
		} else {
			deps.EventCollector = collector.Start
		}
	}

	dagSettingsStore, err := project.DAGSettingsStore()
	if err != nil {
		return scheduler.Dependencies{}, fmt.Errorf("failed to initialize DAG settings store: %w", err)
	}
	deps.DAGSettingsStore = dagSettingsStore
	deps.ProfileStore = project.ProfileStore(ctx)
	if deps.EventService != nil {
		initMonitorStores(ctx, server, project, &deps)
	}
	return deps, nil
}

func initMonitorStores(
	ctx context.Context,
	server *persisfile.ServerStores,
	project *persisfile.ProjectSettings,
	deps *scheduler.Dependencies,
) {
	if _, err := server.Encryptor(); err != nil {
		logger.Warn(ctx, "Failed to prepare encryption for encrypted stores", tag.Error(err))
		logger.Warn(ctx, "Notification settings store is disabled because encrypted storage is not available")
		logger.Warn(ctx, "Incident settings store is disabled because encrypted storage is not available")
		return
	}

	notificationStore, err := project.NotificationStore()
	if err != nil {
		logger.Warn(ctx, "Failed to create notification settings store", tag.Error(err))
	} else {
		deps.NotificationStore = notificationStore
		stateFile := project.NotificationStateFile()
		deps.NotificationState = filemonitor.NewStateStore(stateFile)
		deps.NewNotificationLease = newMonitorLease(stateFile)
	}

	incidentStore, err := project.IncidentStore()
	if err != nil {
		logger.Warn(ctx, "Failed to create incident settings store", tag.Error(err))
	} else {
		deps.IncidentStore = incidentStore
		stateFile := project.IncidentStateFile()
		deps.IncidentState = filemonitor.NewStateStore(stateFile)
		deps.NewIncidentLease = newMonitorLease(stateFile)
	}
}

func newMonitorLease(stateFile string) func() chatbridge.Lease {
	lockDir := filepath.Clean(stateFile) + ".lock"
	return func() chatbridge.Lease {
		return filemonitor.NewLease(stateFile, &dirlock.LockOptions{
			StaleThreshold: chatbridge.DefaultNotificationLockStaleThreshold,
			RetryInterval:  chatbridge.DefaultNotificationLockStandbyRetryInterval,
			OnWait: func() {
				slog.Info("Notification lock is held by another process; DAG run notifications are on standby",
					slog.String("lock_dir", lockDir))
			},
		})
	}
}

// NewPublicWebhookStores creates the stores public webhook URLs need: the
// project's webhook store, which decrypts their tokens and sender secrets,
// and the store of the relay's status and each URL's deliveries.
func NewPublicWebhookStores(
	server *persisfile.ServerStores,
	project *persisfile.ProjectSettings,
) (*store.WebhookStore, *store.PublicWebhookStore, error) {
	encryptor, err := server.Encryptor()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to prepare encryption: %w", err)
	}
	webhooks, err := project.WebhookStore(encryptor)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create webhook store: %w", err)
	}
	return webhooks, server.PublicWebhookStore(), nil
}
