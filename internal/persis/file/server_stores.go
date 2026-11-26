// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileaudit "github.com/dagucloud/dagu/v2/internal/persis/file/audit"
	filecalendar "github.com/dagucloud/dagu/v2/internal/persis/file/calendar"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	fileserviceregistry "github.com/dagucloud/dagu/v2/internal/persis/file/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/upgrade"
)

// ServerStores holds the stores that every project shares, kept in
// paths.data_dir whether or not projects are on. A store that writes when
// it opens, such as one that needs the encryption key, opens on each call.
type ServerStores struct {
	ServiceRegistry serviceregistry.ServiceRegistry
	// ProjectReleases records in this process's service registry entries
	// the projects whose stores it has released.
	ProjectReleases           serviceregistry.ProjectReleaseRecorder
	DispatchTaskStore         dispatch.DispatchTaskStore
	WorkerHeartbeatStore      dispatch.WorkerHeartbeatStore
	DAGRunLeaseStore          dispatch.DAGRunLeaseStore
	ActiveDistributedRunStore dispatch.ActiveDistributedRunStore
	SchedulerPauseStore       schedulerstate.PauseStore
	RestartBackoffStore       schedulerstate.RestartBackoffStore
	// Calendars is the one source of business calendars for every
	// project's DAGs.
	Calendars *spec.CalendarSource

	cfg     *config.Config
	backend *Backend
	pauses  *store.SchedulerPauseStore
	owners  *filedag.Owners

	encryptorMu sync.Mutex
	encryptor   *crypto.Encryptor
}

// NewServerStores opens the stores every project shares. It does not
// access the filesystem.
func NewServerStores(cfg *config.Config) *ServerStores {
	backend := newServerBackend(cfg.Paths)
	registry := fileserviceregistry.New(cfg.Paths.ServiceRegistryDir)
	leases := store.NewDAGRunLeaseStore(backend.Collection(persis.CollectionDAGRunLeases))
	activeRuns := store.NewActiveDistributedRunStore(backend.Collection(persis.CollectionActiveDistributedRuns))
	bundles := workspacebundle.NewStore(
		workspacebundle.StoreDir(cfg.Paths.DataDir),
		workspacebundle.DefaultLimits(),
	)
	pauses := store.NewSchedulerPauseStore(backend.Collection(persis.CollectionSchedulerProcess))
	return &ServerStores{
		ServiceRegistry: registry,
		ProjectReleases: registry,
		DispatchTaskStore: store.NewDispatchTaskStore(
			backend.Collection(persis.CollectionDispatchTasks),
			store.WithDispatchAdmissionLiveness(leases, activeRuns),
			store.WithDispatchTransitionLock(bundles.WithLock),
		),
		WorkerHeartbeatStore:      store.NewWorkerHeartbeatStore(backend.Collection(persis.CollectionWorkerHeartbeats)),
		DAGRunLeaseStore:          leases,
		ActiveDistributedRunStore: activeRuns,
		SchedulerPauseStore:       pauses,
		RestartBackoffStore:       store.NewRestartBackoffStore(backend.Collection(persis.CollectionSchedulerProcess)),
		Calendars:                 spec.NewCalendarSource(cfg.Paths.CalendarsDir),
		cfg:                       cfg,
		backend:                   backend,
		pauses:                    pauses,
		owners: filedag.NewOwners(filedag.OwnerDirs{
			DAGsDir:     cfg.Paths.DAGsDir,
			ProjectsDir: cfg.Paths.ProjectsDir,
			Library:     cfg.Paths.AltDAGsDir,
		}, projectregistry.New(cfg.Paths.DataDir), nil),
	}
}

// DAGOwners tells which project or library a DAG file belongs to, by the
// paths and the project registry in effect.
func (s *ServerStores) DAGOwners() *filedag.Owners {
	return s.owners
}

// Encryptor returns the encryptor of the server's key, which every project
// encrypts its secrets with. It creates the key in paths.data_dir when
// neither DAGU_ENCRYPTION_KEY nor that file holds one.
func (s *ServerStores) Encryptor() (*crypto.Encryptor, error) {
	s.encryptorMu.Lock()
	defer s.encryptorMu.Unlock()
	if s.encryptor != nil {
		return s.encryptor, nil
	}
	key, err := crypto.ResolveKey(s.cfg.Paths.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve encryption key: %w", err)
	}
	enc, err := crypto.NewEncryptor(key)
	if err != nil {
		return nil, fmt.Errorf("create encryptor: %w", err)
	}
	s.encryptor = enc
	return enc, nil
}

// LicenseStore opens the store of license activations, creating its
// directory.
func (s *ServerStores) LicenseStore(ctx context.Context) license.ActivationStore {
	return NewLicenseStore(ctx, s.backend.Collection(persis.CollectionLicense))
}

// UserStore opens the users of builtin authentication.
func (s *ServerStores) UserStore() (*store.UserStore, error) {
	return store.NewUserStore(s.backend.Collection(persis.CollectionUsers))
}

// APIKeyStore opens the API keys.
func (s *ServerStores) APIKeyStore() (*store.APIKeyStore, error) {
	return store.NewAPIKeyStore(s.backend.Collection(persis.CollectionAPIKeys))
}

// WebhookStore opens the DAG webhooks, whose tokens and sender secrets enc
// encrypts. A nil enc leaves them unreadable.
func (s *ServerStores) WebhookStore(enc *crypto.Encryptor) (*store.WebhookStore, error) {
	return store.NewWebhookStore(
		s.backend.Collection(persis.CollectionWebhooks),
		s.backend.Collection(persis.CollectionWebhookDAGClaims),
		enc,
	)
}

// PublicWebhookStore opens the relay's status and each public webhook URL's
// deliveries.
func (s *ServerStores) PublicWebhookStore() *store.PublicWebhookStore {
	return store.NewPublicWebhookStore(s.backend.Collection(persis.CollectionPublicWebhooks))
}

// WorkspaceStore opens the workspaces.
func (s *ServerStores) WorkspaceStore() (*store.WorkspaceStore, error) {
	return store.NewWorkspaceStore(s.backend.Collection(persis.CollectionWorkspaces))
}

// RemoteNodeStore opens the remote nodes, whose credentials enc encrypts.
func (s *ServerStores) RemoteNodeStore(enc *crypto.Encryptor) (*store.RemoteNodeStore, error) {
	return store.NewRemoteNodeStore(s.backend.Collection(persis.CollectionRemoteNodes), enc)
}

// WorkerNodeStore opens the worker nodes, which it reports with their
// heartbeats.
func (s *ServerStores) WorkerNodeStore() (*store.WorkerNodeStore, error) {
	return store.NewWorkerNodeStore(s.backend.Collection(persis.CollectionWorkerNodes), s.WorkerHeartbeatStore)
}

// UpgradeCheckStore opens the cache of upgrade checks, creating its
// directory.
func (s *ServerStores) UpgradeCheckStore() (upgrade.CacheStore, error) {
	return NewUpgradeCheckStore(s.cfg, s.backend.Collection(persis.CollectionUpgradeCheck))
}

// AuditStore opens the audit log, which drops entries older than
// retentionDays, unless it is zero.
func (s *ServerStores) AuditStore(retentionDays int, opts ...fileaudit.Option) (*fileaudit.Store, error) {
	return fileaudit.New(fileaudit.Dir(s.cfg.Paths.AdminLogsDir), retentionDays, opts...)
}

// CalendarStore opens the business calendars for editing, creating their
// directory.
func (s *ServerStores) CalendarStore(opts ...filecalendar.Option) (*filecalendar.Store, error) {
	return filecalendar.New(s.cfg.Paths.CalendarsDir, opts...)
}

// ConnectionStore opens the store of OAuth apps, connections, their token
// state, and their tool catalogs, encrypted with the server's key. It
// creates the store's directories.
func (s *ServerStores) ConnectionStore() (connection.Store, error) {
	enc, err := s.Encryptor()
	if err != nil {
		return nil, err
	}
	return NewConnectionStore(
		s.backend.Collection(persis.CollectionOAuthApps),
		s.backend.Collection(persis.CollectionConnections),
		s.backend.Collection(persis.CollectionConnectionTokens),
		s.backend.Collection(persis.CollectionConnectionCatalogs),
		s.backend.Lock(persis.CollectionConnections),
		enc,
	)
}
