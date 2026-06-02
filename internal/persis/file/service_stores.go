// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/internal/agent"
	authmodel "github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/core/baseconfig"
	"github.com/dagucloud/dagu/internal/dagsettings"
	"github.com/dagucloud/dagu/internal/githubdispatch"
	"github.com/dagucloud/dagu/internal/incident"
	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/notification"
	fileaudit "github.com/dagucloud/dagu/internal/persis/file/audit"
	filebaseconfig "github.com/dagucloud/dagu/internal/persis/file/baseconfig"
	"github.com/dagucloud/dagu/internal/persis/file/doc"
	fileeventstore "github.com/dagucloud/dagu/internal/persis/file/eventstore"
	fileincident "github.com/dagucloud/dagu/internal/persis/file/incident"
	filenotification "github.com/dagucloud/dagu/internal/persis/file/notification"
	"github.com/dagucloud/dagu/internal/persis/file/tokensecret"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/remotenode"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/dagucloud/dagu/internal/upgrade"
	"github.com/dagucloud/dagu/internal/workspace"
)

type BaseConfigStoreOption = filebaseconfig.Option

// BaseConfigStore is a file-backed base DAG configuration store.
type BaseConfigStore interface {
	baseconfig.Store
	Initialize() error
}

func WithBaseConfigSkipDefault(skip bool) BaseConfigStoreOption {
	return filebaseconfig.WithSkipDefault(skip)
}

func NewBaseConfigStore(filePath string, opts ...BaseConfigStoreOption) (BaseConfigStore, error) {
	return filebaseconfig.New(filePath, opts...)
}

func NewWorkspaceBaseConfigStore(dagsDir, workspaceName string) (baseconfig.Store, error) {
	return NewBaseConfigStore(
		workspace.BaseConfigPath(dagsDir, workspaceName),
		WithBaseConfigSkipDefault(true),
	)
}

// AuditStore is a file-backed audit store with an optional background cleaner.
type AuditStore interface {
	audit.Store
	Close() error
}

func NewAuditStore(cfg *config.Config) (AuditStore, error) {
	if cfg == nil || !cfg.Server.Audit.Enabled {
		return nil, nil
	}
	return fileaudit.New(filepath.Join(cfg.Paths.AdminLogsDir, "audit"), cfg.Server.Audit.RetentionDays)
}

func NewDocStore(cfg *config.Config) agent.DocStore {
	return doc.New(cfg.Paths.DocsDir)
}

func NewEventStore(cfg *config.Config) (eventstore.Store, error) {
	if cfg == nil || !cfg.EventStore.Enabled {
		return nil, nil
	}
	return fileeventstore.New(cfg.Paths.EventStoreDir)
}

// EventCollector drains inbox events into committed event log files.
type EventCollector interface {
	Start(context.Context)
}

func NewEventCollector(cfg *config.Config) (EventCollector, error) {
	if cfg == nil || !cfg.EventStore.Enabled {
		return nil, nil
	}
	return fileeventstore.NewCollector(cfg.Paths.EventStoreDir, cfg.EventStore.RetentionDays)
}

func NewGitHubDispatchTracker(cfg *config.Config) githubdispatch.Tracker {
	dir := filepath.Join(cfg.Paths.DataDir, "github-dispatch")
	_ = os.MkdirAll(dir, 0o700)
	return store.NewGitHubDispatchStore(NewCollection(dir, WithIndentedJSON()))
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

func NewIncidentStore(cfg *config.Config, enc *crypto.Encryptor) (incident.Store, error) {
	return fileincident.New(
		filepath.Join(cfg.Paths.DataDir, "incidents"),
		fileincident.WithEncryptor(enc),
	)
}

func IncidentMonitorStateFile(cfg *config.Config) string {
	return filepath.Join(cfg.Paths.DataDir, "incidents", "monitor-state.json")
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

func NewNotificationStore(cfg *config.Config, enc *crypto.Encryptor) (notification.Store, error) {
	return filenotification.New(
		filepath.Join(cfg.Paths.DataDir, "notifications", "dags"),
		filenotification.WithEncryptor(enc),
	)
}

func NotificationMonitorStateFile(cfg *config.Config) string {
	return filepath.Join(cfg.Paths.DataDir, "notifications", "monitor-state.json")
}

func NewRemoteNodeStore(cfg *config.Config, enc *crypto.Encryptor) (remotenode.Store, error) {
	dir := cfg.Paths.RemoteNodesDir
	if dir == "" {
		return nil, fmt.Errorf("remote-node store: RemoteNodesDir cannot be empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("remote-node store: create directory %s: %w", dir, err)
	}
	return store.NewRemoteNodeStore(NewCollection(dir, WithIndentedJSON()), enc)
}

func NewTokenSecretProvider(cfg *config.Config) authmodel.TokenSecretProvider {
	return tokensecret.New(filepath.Join(cfg.Paths.DataDir, "auth"))
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

func NewWorkspaceStore(cfg *config.Config) (workspace.Store, error) {
	dir := cfg.Paths.WorkspacesDir
	if dir == "" {
		return nil, fmt.Errorf("workspace store: WorkspacesDir cannot be empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("workspace store: create directory %s: %w", dir, err)
	}
	return store.NewWorkspaceStore(NewCollection(dir, WithIndentedJSON()))
}
