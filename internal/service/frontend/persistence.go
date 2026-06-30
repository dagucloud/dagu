// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"

	authmodel "github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/core/baseconfig"
	"github.com/dagucloud/dagu/internal/dagsettings"
	"github.com/dagucloud/dagu/internal/incident"
	"github.com/dagucloud/dagu/internal/notification"
	"github.com/dagucloud/dagu/internal/profile"
	"github.com/dagucloud/dagu/internal/remotenode"
	"github.com/dagucloud/dagu/internal/secret"
	"github.com/dagucloud/dagu/internal/service/audit"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/internal/upgrade"
	"github.com/dagucloud/dagu/internal/view"
	"github.com/dagucloud/dagu/internal/workspace"
)

// StoreFactories contains backend-specific persistence wiring for the frontend server.
type StoreFactories struct {
	WorkspaceBaseConfigStoreFactory  apiv1.WorkspaceBaseConfigStoreFactory
	BaseConfigStoreFactory           BaseConfigStoreFactory
	BuiltinAuthFactory               BuiltinAuthFactory
	RemoteNodeStoreFactory           RemoteNodeStoreFactory
	SecretStoreFactory               SecretStoreFactory
	ProfileStoreFactory              ProfileStoreFactory
	DAGSettingsStoreFactory          DAGSettingsStoreFactory
	NotificationStoreFactory         NotificationStoreFactory
	NotificationMonitorStateFileFunc MonitorStateFileFunc
	IncidentStoreFactory             IncidentStoreFactory
	IncidentMonitorStateFileFunc     MonitorStateFileFunc
	WorkspaceStoreFactory            WorkspaceStoreFactory
	UpgradeCheckStoreFactory         UpgradeCheckStoreFactory
	AuditStoreFactory                AuditStoreFactory
	EventStoreFactory                EventStoreFactory
	ViewStoreFactory                 ViewStoreFactory
}

type BaseConfigStoreFactory func(filePath string) (baseconfig.Store, error)

type SecretStoreFactory func(context.Context, *config.Config) secret.Store

type ProfileStoreFactory func(context.Context, *config.Config) profile.Store

type BuiltinAuthFactory func(context.Context, *config.Config) (*BuiltinAuthResult, bool, error)

type RemoteNodeStoreFactory func(*config.Config, *crypto.Encryptor) (remotenode.Store, error)

type DAGSettingsStoreFactory func(*config.Config) (dagsettings.Store, error)

type NotificationStoreFactory func(*config.Config, *crypto.Encryptor) (notification.Store, error)

type IncidentStoreFactory func(*config.Config, *crypto.Encryptor) (incident.Store, error)

type WorkspaceStoreFactory func(*config.Config) (workspace.Store, error)

type UpgradeCheckStoreFactory func(*config.Config) (upgrade.CacheStore, error)

type AuditStoreFactory func(*config.Config) (AuditStore, error)

type EventStoreFactory func(*config.Config) (eventstore.Store, error)

type ViewStoreFactory func(*config.Config) (view.Store, error)

type MonitorStateFileFunc func(*config.Config) string

// AuditStore is an audit store with an optional background cleaner.
type AuditStore interface {
	audit.Store
	Close() error
}

type BuiltinAuthResult struct {
	AuthService *authservice.Service
	UserStore   authmodel.UserStore
}
