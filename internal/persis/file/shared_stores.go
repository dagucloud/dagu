// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/license"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/upgrade"
)

// NewSecretStore wires the encrypted file-backed secret store from config paths.
func NewSecretStore(ctx context.Context, cfg *config.Config) secret.Store {
	if cfg == nil || cfg.Paths.DataDir == "" {
		return nil
	}
	if encKey, encErr := crypto.ResolveKey(cfg.Paths.DataDir); encErr != nil {
		logger.Warn(ctx, "Failed to resolve encryption key for secret store", tag.Error(encErr))
	} else if enc, encErr := crypto.NewEncryptor(encKey); encErr != nil {
		logger.Warn(ctx, "Failed to create encryptor for secret store", tag.Error(encErr))
	} else if secretStore, storeErr := store.NewSecretStore(
		NewCollection(filepath.Join(cfg.Paths.DataDir, "secrets"), WithIndentedJSON()), enc,
	); storeErr != nil {
		logger.Warn(ctx, "Failed to create secret store", tag.Error(storeErr))
	} else {
		return secretStore
	}
	return nil
}

// NewProfileStore wires the file-backed runtime profile store from config paths.
func NewProfileStore(ctx context.Context, cfg *config.Config) profile.Store {
	if cfg == nil || cfg.Paths.DataDir == "" {
		return nil
	}
	profileStore, err := store.NewProfileStore(
		NewCollection(filepath.Join(cfg.Paths.DataDir, "profiles"), WithIndentedJSON()),
	)
	if err != nil {
		logger.Warn(ctx, "Failed to create profile store", tag.Error(err))
		return nil
	}
	return profileStore
}

func NewEventStore(cfg *config.Config) (eventstore.Store, error) {
	if cfg == nil || !cfg.EventStore.Enabled {
		return nil, nil
	}
	return fileeventstore.New(cfg.Paths.EventStoreDir)
}

func NewEventCollector(cfg *config.Config) (*fileeventstore.Collector, error) {
	if cfg == nil || !cfg.EventStore.Enabled {
		return nil, nil
	}
	return fileeventstore.NewCollector(cfg.Paths.EventStoreDir, cfg.EventStore.RetentionDays)
}

func NewDAGSettingsStore(cfg *config.Config) (dagsettings.Store, error) {
	if cfg == nil || cfg.Paths.DataDir == "" {
		return nil, fmt.Errorf("DAG settings store: DataDir cannot be empty")
	}
	dir := filepath.Join(cfg.Paths.DataDir, "dag-settings")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("DAG settings store: create directory %s: %w", dir, err)
	}
	return store.NewDAGSettingsStore(NewCollection(dir, WithIndentedJSON()))
}

func NewLicenseStore(cfg *config.Config) license.ActivationStore {
	dir := LicenseDir(cfg)
	// Pre-create at 0o700 so the directory ends up with the stricter perm.
	// Collection.Put falls back to MkdirAll(0o750) when the dir is missing,
	// which would otherwise relax the bit on fresh installs.
	_ = os.MkdirAll(dir, 0o700)
	return store.NewLicenseStore(NewCollection(dir, WithIndentedJSON()))
}

func LicenseDir(cfg *config.Config) string {
	return filepath.Join(cfg.Paths.DataDir, "license")
}

func NewUpgradeCheckStore(cfg *config.Config) (upgrade.CacheStore, error) {
	if cfg.Paths.DataDir == "" {
		return nil, fmt.Errorf("upgrade check store: data directory cannot be empty")
	}
	dir := filepath.Join(cfg.Paths.DataDir, "upgrade")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("upgrade check store: create directory %s: %w", dir, err)
	}
	return store.NewUpgradeCheckStore(NewCollection(dir, WithIndentedJSON())), nil
}
