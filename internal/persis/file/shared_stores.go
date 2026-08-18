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
	"github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/upgrade"
)

// NewSecretStore wires the encrypted file-backed secret store from config paths.
func NewSecretStore(ctx context.Context, cfg *config.Config, col persis.Collection) secret.Store {
	if cfg == nil || cfg.Paths.DataDir == "" {
		return nil
	}
	if encKey, encErr := crypto.ResolveKey(cfg.Paths.DataDir); encErr != nil {
		logger.Warn(ctx, "Failed to resolve encryption key for secret store", tag.Error(encErr))
	} else if enc, encErr := crypto.NewEncryptor(encKey); encErr != nil {
		logger.Warn(ctx, "Failed to create encryptor for secret store", tag.Error(encErr))
	} else if secretStore, storeErr := store.NewSecretStore(col, enc); storeErr != nil {
		logger.Warn(ctx, "Failed to create secret store", tag.Error(storeErr))
	} else {
		return secretStore
	}
	return nil
}

// NewProfileStore wires the file-backed runtime profile store from config paths.
func NewProfileStore(ctx context.Context, cfg *config.Config, col persis.Collection) profile.Store {
	if cfg == nil || cfg.Paths.DataDir == "" {
		return nil
	}
	profileStore, err := store.NewProfileStore(col)
	if err != nil {
		logger.Warn(ctx, "Failed to create profile store", tag.Error(err))
		return nil
	}
	return profileStore
}

func NewDAGSettingsStore(cfg *config.Config, col persis.Collection) (dagsettings.Store, error) {
	if cfg == nil || cfg.Paths.DataDir == "" {
		return nil, fmt.Errorf("DAG settings store: DataDir cannot be empty")
	}
	if err := createCollectionDirs(col, "DAG settings store", 0o750, ""); err != nil {
		return nil, err
	}
	return store.NewDAGSettingsStore(col)
}

// NewIncidentStore creates an incident store backed by col.
func NewIncidentStore(col persis.Collection, enc *crypto.Encryptor) (incident.Store, error) {
	if err := createCollectionDirs(
		col,
		"incident store",
		0o750,
		"",
		"providers",
		"policies/workspaces",
		"policies/dags",
		"states",
	); err != nil {
		return nil, err
	}
	return store.NewIncidentStore(col, enc)
}

// NewNotificationStore creates a notification store backed by col.
func NewNotificationStore(col persis.Collection, enc *crypto.Encryptor) (notification.Store, error) {
	if err := createCollectionDirs(
		col,
		"notification store",
		0o750,
		"",
		"dags",
		"channels",
		"routes/workspaces",
	); err != nil {
		return nil, err
	}
	return store.NewNotificationStore(col, enc)
}

func NewLicenseStore(ctx context.Context, col persis.Collection) license.ActivationStore {
	// License data requires an owner-only collection directory.
	if err := createCollectionDirs(col, "license store", 0o700, ""); err != nil {
		logger.Warn(ctx, "Failed to create license store directory", tag.Error(err))
	}
	return store.NewLicenseStore(col)
}

func LicenseDir(cfg *config.Config) string {
	return filepath.Join(cfg.Paths.DataDir, "license")
}

func NewUpgradeCheckStore(cfg *config.Config, col persis.Collection) (upgrade.CacheStore, error) {
	if cfg.Paths.DataDir == "" {
		return nil, fmt.Errorf("upgrade check store: data directory cannot be empty")
	}
	if err := createCollectionDirs(col, "upgrade check store", 0o750, ""); err != nil {
		return nil, err
	}
	return store.NewUpgradeCheckStore(col), nil
}

func createCollectionDirs(col persis.Collection, storeName string, perm os.FileMode, relativePaths ...string) error {
	fileCol, ok := col.(*Collection)
	if !ok {
		return nil
	}
	if fileCol.dir == "" {
		return fmt.Errorf("%s: directory cannot be empty", storeName)
	}
	for _, relativePath := range relativePaths {
		path := filepath.Join(fileCol.dir, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(path, perm); err != nil {
			return fmt.Errorf("%s: create directory %s: %w", storeName, path, err)
		}
	}
	return nil
}
