// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package controlplanestore selects and exposes shared control-plane
// persistence backends.
package controlplanestore

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	cmncrypto "github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/dagucloud/dagu/internal/workspace"
)

// Store exposes the shared control-plane stores owned by a backend.
type Store interface {
	DAGRuns() exec.DAGRunStore
	Queue() exec.QueueStore
	Services() exec.ServiceRegistry
	DispatchTasks() exec.DispatchTaskStore
	WorkerHeartbeats() exec.WorkerHeartbeatStore
	DAGRunLeases() exec.DAGRunLeaseStore
	ActiveDistributedRuns() exec.ActiveDistributedRunStore
	Audit() audit.Store
	Users() auth.UserStore
	APIKeys() auth.APIKeyStore
	Webhooks() auth.WebhookStore
	Workspaces() workspace.Store
	Sessions() agent.SessionStore
	Events() eventstore.Store
	Close() error
}

// Options contains runtime options that are not part of persistent config.
type Options struct {
	FileCache         *fileutil.Cache[*exec.DAGRunStatus]
	LatestStatusToday bool
	Location          *time.Location
	Role              Role
	WebhookEncryptor  *cmncrypto.Encryptor
}

// Option configures control-plane store construction.
type Option func(*Options)

// Role identifies the Dagu process role that owns a control-plane connection.
type Role string

const (
	// RoleServer is used by the frontend/API process.
	RoleServer Role = "server"
	// RoleScheduler is used by the scheduler process.
	RoleScheduler Role = "scheduler"
	// RoleCoordinator is used by the coordinator process.
	RoleCoordinator Role = "coordinator"
	// RoleAgent is used by DAG execution processes.
	RoleAgent Role = "agent"
)

// WithHistoryFileCache sets the optional file-store status cache.
func WithHistoryFileCache(cache *fileutil.Cache[*exec.DAGRunStatus]) Option {
	return func(o *Options) {
		o.FileCache = cache
	}
}

// WithLatestStatusToday configures whether latest lookups are restricted to today.
func WithLatestStatusToday(latestStatusToday bool) Option {
	return func(o *Options) {
		o.LatestStatusToday = latestStatusToday
	}
}

// WithLocation sets the timezone used for "today" calculations.
func WithLocation(location *time.Location) Option {
	return func(o *Options) {
		o.Location = location
	}
}

// WithRole selects the process-specific PostgreSQL control-plane settings.
func WithRole(role Role) Option {
	return func(o *Options) {
		o.Role = role
	}
}

// WithWebhookEncryptor sets the encryptor used for webhook HMAC secrets.
func WithWebhookEncryptor(encryptor *cmncrypto.Encryptor) Option {
	return func(o *Options) {
		o.WebhookEncryptor = encryptor
	}
}

// NewDAGRunStore creates the configured DAG-run store. This keeps current
// DAG-run call sites narrow while the broader control-plane store is adopted.
func NewDAGRunStore(ctx context.Context, cfg *config.Config, opts ...Option) (exec.DAGRunStore, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config must not be nil")
	}

	options := defaultOptions(cfg)
	for _, opt := range opts {
		opt(&options)
	}

	switch cfg.ControlPlaneStore.Backend {
	case "", config.ControlPlaneStoreBackendFile:
		return newFileDAGRunStore(cfg, options), nil
	case config.ControlPlaneStoreBackendPostgres:
		store, err := New(ctx, cfg, opts...)
		if err != nil {
			return nil, err
		}
		return store.DAGRuns(), nil
	default:
		return nil, fmt.Errorf("unsupported control-plane store backend %q", cfg.ControlPlaneStore.Backend)
	}
}

// New creates the configured shared control-plane store.
func New(ctx context.Context, cfg *config.Config, opts ...Option) (Store, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config must not be nil")
	}
	options := defaultOptions(cfg)
	for _, opt := range opts {
		opt(&options)
	}

	switch cfg.ControlPlaneStore.Backend {
	case "", config.ControlPlaneStoreBackendFile:
		return newFileStore(ctx, cfg, options)
	case config.ControlPlaneStoreBackendPostgres:
		pgCfg, err := postgresRoleConfig(cfg.ControlPlaneStore.Postgres, options.Role)
		if err != nil {
			return nil, err
		}
		if options.Role == RoleAgent && !pgCfg.DirectAccess {
			return nil, fmt.Errorf("control_plane_store.postgres.agent.direct_access must be true to open PostgreSQL control-plane store from an agent process; use a shared-nothing worker with coordinator or enable direct_access for local development")
		}
		if pgCfg.DSN == "" {
			return nil, fmt.Errorf("control_plane_store.postgres.%s.dsn is required when control_plane_store.backend is postgres", options.Role)
		}
		return postgres.New(ctx, postgres.Config{
			DSN:               pgCfg.DSN,
			LocalWorkDirBase:  cfg.Paths.DAGRunsDir,
			AutoMigrate:       pgCfg.AutoMigrate,
			LatestStatusToday: options.LatestStatusToday,
			Location:          options.Location,
			WebhookEncryptor:  options.WebhookEncryptor,
			Pool: postgres.PoolConfig{
				MaxOpenConns:    pgCfg.Pool.MaxOpenConns,
				MaxIdleConns:    pgCfg.Pool.MaxIdleConns,
				ConnMaxLifetime: pgCfg.Pool.ConnMaxLifetime,
				ConnMaxIdleTime: pgCfg.Pool.ConnMaxIdleTime,
			},
		})
	default:
		return nil, fmt.Errorf("unsupported control-plane store backend %q", cfg.ControlPlaneStore.Backend)
	}
}

func defaultOptions(cfg *config.Config) Options {
	return Options{
		Location: cfg.Core.Location,
		Role:     RoleServer,
	}
}

func postgresRoleConfig(cfg config.ControlPlaneStorePostgresConfig, role Role) (config.ControlPlaneStorePostgresRoleConfig, error) {
	switch role {
	case "", RoleServer:
		return cfg.Server, nil
	case RoleScheduler:
		return cfg.Scheduler, nil
	case RoleCoordinator:
		return cfg.Coordinator, nil
	case RoleAgent:
		return cfg.Agent, nil
	default:
		return config.ControlPlaneStorePostgresRoleConfig{}, fmt.Errorf("unsupported control-plane store role %q", role)
	}
}

func newFileDAGRunStore(cfg *config.Config, options Options) exec.DAGRunStore {
	fileOpts := []filedagrun.DAGRunStoreOption{
		filedagrun.WithArtifactDir(cfg.Paths.ArtifactDir),
		filedagrun.WithLatestStatusToday(options.LatestStatusToday),
		filedagrun.WithLocation(options.Location),
	}
	if options.FileCache != nil {
		fileOpts = append(fileOpts, filedagrun.WithHistoryFileCache(options.FileCache))
	}
	return filedagrun.New(cfg.Paths.DAGRunsDir, fileOpts...)
}
