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
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
)

// NewDependencies creates the file-backed stores used by the scheduler service.
func NewDependencies(ctx context.Context, cfg *config.Config, backend persis.Backend) (scheduler.Dependencies, error) {
	var deps scheduler.Dependencies
	if cfg.EventStore.Enabled {
		store, err := fileeventstore.New(cfg.Paths.EventStoreDir)
		if err != nil {
			logger.Warn(ctx, "Failed to initialize event store; continuing without event persistence", tag.Error(err))
		} else {
			deps.EventService = eventstore.New(store)
		}
	}
	if deps.EventService != nil {
		collector, err := fileeventstore.NewCollector(
			cfg.Paths.EventStoreDir,
			cfg.EventStore.RetentionDays,
			fileeventstore.WithDedupeCacheBytes(cfg.Cache.Limits().EventStoreBytes),
		)
		if err != nil {
			logger.Warn(ctx, "Failed to initialize event collector; continuing without collection", tag.Error(err))
		} else {
			deps.EventCollector = collector.Start
		}
	}

	dagSettingsStore, err := persisfile.NewDAGSettingsStore(
		cfg,
		backend.Collection(persis.CollectionDAGSettings),
	)
	if err != nil {
		return scheduler.Dependencies{}, fmt.Errorf("failed to initialize DAG settings store: %w", err)
	}
	deps.DAGSettingsStore = dagSettingsStore
	deps.ProfileStore = persisfile.NewProfileStore(ctx, cfg, backend.Collection(persis.CollectionProfiles))
	if deps.EventService != nil {
		initMonitorStores(ctx, cfg, backend, &deps)
	}
	return deps, nil
}

func initMonitorStores(ctx context.Context, cfg *config.Config, backend persis.Backend, deps *scheduler.Dependencies) {
	key, err := crypto.ResolveKey(cfg.Paths.DataDir)
	if err != nil {
		logger.Warn(ctx, "Failed to resolve encryption key for encrypted stores", tag.Error(err))
		logger.Warn(ctx, "Notification settings store is disabled because encrypted storage is not available")
		logger.Warn(ctx, "Incident settings store is disabled because encrypted storage is not available")
		return
	}
	encryptor, err := crypto.NewEncryptor(key)
	if err != nil {
		logger.Warn(ctx, "Failed to create encryptor for encrypted stores", tag.Error(err))
		logger.Warn(ctx, "Notification settings store is disabled because encrypted storage is not available")
		logger.Warn(ctx, "Incident settings store is disabled because encrypted storage is not available")
		return
	}

	notificationStore, err := persisfile.NewNotificationStore(
		backend.Collection(persis.CollectionNotifications),
		encryptor,
	)
	if err != nil {
		logger.Warn(ctx, "Failed to create notification settings store", tag.Error(err))
	} else {
		deps.NotificationStore = notificationStore
		stateFile := filepath.Join(cfg.Paths.DataDir, "notifications", "monitor-state.json")
		deps.NotificationState = filemonitor.NewStateStore(stateFile)
		deps.NewNotificationLease = newMonitorLease(stateFile)
	}

	incidentStore, err := persisfile.NewIncidentStore(
		backend.Collection(persis.CollectionIncidents),
		encryptor,
	)
	if err != nil {
		logger.Warn(ctx, "Failed to create incident settings store", tag.Error(err))
	} else {
		deps.IncidentStore = incidentStore
		stateFile := filepath.Join(cfg.Paths.DataDir, "incidents", "monitor-state.json")
		deps.IncidentState = filemonitor.NewStateStore(stateFile)
		deps.NewIncidentLease = newMonitorLease(stateFile)
	}
}

func newMonitorLease(stateFile string) func() chatbridge.Lease {
	lockDir := filepath.Clean(stateFile) + ".lock"
	return func() chatbridge.Lease {
		return filemonitor.NewLease(stateFile, &dirlock.LockOptions{
			StaleThreshold: chatbridge.DefaultNotificationLockStaleThreshold,
			RetryInterval:  chatbridge.DefaultNotificationLockRetryInterval,
			OnWait: func() {
				slog.Info("Notification lock is held by another process; DAG run notifications are on standby",
					slog.String("lock_dir", lockDir))
			},
		})
	}
}
