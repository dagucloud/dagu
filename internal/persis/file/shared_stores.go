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
	dir := filepath.Join(cfg.Paths.DataDir, "dag-settings")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("DAG settings store: create directory %s: %w", dir, err)
	}
	return store.NewDAGSettingsStore(col)
}

// NewIncidentStore creates a file-backed incident store rooted at dir.
func NewIncidentStore(dir string, col persis.Collection, enc *crypto.Encryptor) (incident.Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("incident store: directory cannot be empty")
	}
	for _, path := range []string{
		dir,
		filepath.Join(dir, "providers"),
		filepath.Join(dir, "policies", "workspaces"),
		filepath.Join(dir, "policies", "dags"),
		filepath.Join(dir, "states"),
	} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			return nil, fmt.Errorf("incident store: create directory %s: %w", path, err)
		}
	}
	return store.NewIncidentStore(col, enc)
}

// NewNotificationStore creates a file-backed notification store rooted at dir.
func NewNotificationStore(dir string, col persis.Collection, enc *crypto.Encryptor) (notification.Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("notification store: directory cannot be empty")
	}
	for _, path := range []string{
		dir,
		filepath.Join(dir, "dags"),
		filepath.Join(dir, "channels"),
		filepath.Join(dir, "routes", "workspaces"),
	} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			return nil, fmt.Errorf("notification store: create directory %s: %w", path, err)
		}
	}
	return store.NewNotificationStore(col, enc)
}

func NewLicenseStore(cfg *config.Config, col persis.Collection) license.ActivationStore {
	dir := LicenseDir(cfg)
	// Pre-create at 0o700 so the directory ends up with the stricter perm.
	// Collection.Put falls back to MkdirAll(0o750) when the dir is missing,
	// which would otherwise relax the bit on fresh installs.
	_ = os.MkdirAll(dir, 0o700)
	return store.NewLicenseStore(col)
}

func LicenseDir(cfg *config.Config) string {
	return filepath.Join(cfg.Paths.DataDir, "license")
}

func NewUpgradeCheckStore(cfg *config.Config, col persis.Collection) (upgrade.CacheStore, error) {
	if cfg.Paths.DataDir == "" {
		return nil, fmt.Errorf("upgrade check store: data directory cannot be empty")
	}
	dir := filepath.Join(cfg.Paths.DataDir, "upgrade")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("upgrade check store: create directory %s: %w", dir, err)
	}
	return store.NewUpgradeCheckStore(col), nil
}
