// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/agentsnapshot"
	authmodel "github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core/baseconfig"
	"github.com/dagucloud/dagu/internal/incident"
	"github.com/dagucloud/dagu/internal/notification"
	"github.com/dagucloud/dagu/internal/remotenode"
	"github.com/dagucloud/dagu/internal/service/audit"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/internal/upgrade"
	"github.com/dagucloud/dagu/internal/workspace"
)

// StoreFactories contains backend-specific persistence wiring for the frontend server.
type StoreFactories struct {
	SnapshotStoreFactory             agentsnapshot.StoreFactory
	WorkspaceBaseConfigStoreFactory  apiv1.WorkspaceBaseConfigStoreFactory
	BaseConfigStoreFactory           BaseConfigStoreFactory
	AgentStoresFactory               AgentStoresFactory
	AgentSessionStoreFactory         AgentSessionStoreFactory
	DocStoreFactory                  DocStoreFactory
	BuiltinAuthFactory               BuiltinAuthFactory
	RemoteNodeStoreFactory           RemoteNodeStoreFactory
	NotificationStoreFactory         NotificationStoreFactory
	NotificationMonitorStateFileFunc MonitorStateFileFunc
	IncidentStoreFactory             IncidentStoreFactory
	IncidentMonitorStateFileFunc     MonitorStateFileFunc
	WorkspaceStoreFactory            WorkspaceStoreFactory
	UpgradeCheckStoreFactory         UpgradeCheckStoreFactory
	AuditStoreFactory                AuditStoreFactory
	EventStoreFactory                EventStoreFactory
}

type BaseConfigStoreFactory func(filePath string) (baseconfig.Store, error)

type AgentStoresFactory func(context.Context, *config.Config, AgentStoresOptions) agent.RuntimeStores

type AgentStoresOptions struct {
	MemoryCache      *fileutil.Cache[string]
	SeedReferences   bool
	SeedExampleSouls bool
}

type AgentSessionStoreFactory func(*config.Config) (agent.SessionStore, error)

type DocStoreFactory func(*config.Config) agent.DocStore

type BuiltinAuthFactory func(context.Context, *config.Config) (*BuiltinAuthResult, bool, error)

type RemoteNodeStoreFactory func(*config.Config, *crypto.Encryptor) (remotenode.Store, error)

type NotificationStoreFactory func(*config.Config, *crypto.Encryptor) (notification.Store, error)

type IncidentStoreFactory func(*config.Config, *crypto.Encryptor) (incident.Store, error)

type WorkspaceStoreFactory func(*config.Config) (workspace.Store, error)

type UpgradeCheckStoreFactory func(*config.Config) (upgrade.CacheStore, error)

type AuditStoreFactory func(*config.Config) (AuditStore, error)

type EventStoreFactory func(*config.Config) (eventstore.Store, error)

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
