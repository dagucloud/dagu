// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controlplanestore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/fileapikey"
	"github.com/dagucloud/dagu/internal/persis/fileaudit"
	"github.com/dagucloud/dagu/internal/persis/filedistributed"
	"github.com/dagucloud/dagu/internal/persis/fileeventstore"
	"github.com/dagucloud/dagu/internal/persis/filequeue"
	"github.com/dagucloud/dagu/internal/persis/fileserviceregistry"
	"github.com/dagucloud/dagu/internal/persis/filesession"
	"github.com/dagucloud/dagu/internal/persis/fileuser"
	"github.com/dagucloud/dagu/internal/persis/filewebhook"
	"github.com/dagucloud/dagu/internal/persis/fileworkspace"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/dagucloud/dagu/internal/workspace"
)

type fileStore struct {
	dagRuns               exec.DAGRunStore
	queue                 exec.QueueStore
	services              exec.ServiceRegistry
	dispatchTasks         exec.DispatchTaskStore
	workerHeartbeats      exec.WorkerHeartbeatStore
	dagRunLeases          exec.DAGRunLeaseStore
	activeDistributedRuns exec.ActiveDistributedRunStore
	audit                 audit.Store
	auditCloser           interface{ Close() error }
	users                 auth.UserStore
	apiKeys               auth.APIKeyStore
	webhooks              auth.WebhookStore
	workspaces            workspace.Store
	sessions              agent.SessionStore
	events                eventstore.Store
}

func newFileStore(ctx context.Context, cfg *config.Config, opts Options) (Store, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	limits := cfg.Cache.Limits()
	userCache := fileutil.NewCache[*auth.User]("user", limits.User.Limit, limits.User.TTL)
	userCache.StartEviction(ctx)
	apiKeyCache := fileutil.NewCache[*auth.APIKey]("api_key", limits.APIKey.Limit, limits.APIKey.TTL)
	apiKeyCache.StartEviction(ctx)
	webhookCache := fileutil.NewCache[*auth.Webhook]("webhook", limits.Webhook.Limit, limits.Webhook.TTL)
	webhookCache.StartEviction(ctx)

	userStore, err := fileuser.New(cfg.Paths.UsersDir, fileuser.WithFileCache(userCache))
	if err != nil {
		return nil, fmt.Errorf("create file user store: %w", err)
	}
	apiKeyStore, err := fileapikey.New(cfg.Paths.APIKeysDir, fileapikey.WithFileCache(apiKeyCache))
	if err != nil {
		return nil, fmt.Errorf("create file API key store: %w", err)
	}
	webhookOpts := []filewebhook.Option{filewebhook.WithFileCache(webhookCache)}
	if opts.WebhookEncryptor != nil {
		webhookOpts = append(webhookOpts, filewebhook.WithEncryptor(opts.WebhookEncryptor))
	}
	webhookStore, err := filewebhook.New(cfg.Paths.WebhooksDir, webhookOpts...)
	if err != nil {
		return nil, fmt.Errorf("create file webhook store: %w", err)
	}
	auditStore, err := fileaudit.New(filepath.Join(cfg.Paths.AdminLogsDir, "audit"), cfg.Server.Audit.RetentionDays)
	if err != nil {
		return nil, fmt.Errorf("create file audit store: %w", err)
	}
	workspaceStore, err := fileworkspace.New(cfg.Paths.WorkspacesDir)
	if err != nil {
		return nil, fmt.Errorf("create file workspace store: %w", err)
	}
	sessionStore, err := filesession.New(cfg.Paths.SessionsDir, filesession.WithMaxPerUser(cfg.Server.Session.MaxPerUser))
	if err != nil {
		return nil, fmt.Errorf("create file session store: %w", err)
	}
	eventStore, err := fileeventstore.New(cfg.Paths.EventStoreDir)
	if err != nil {
		return nil, fmt.Errorf("create file event store: %w", err)
	}

	distributedDir := filepath.Join(cfg.Paths.DataDir, "distributed")
	return &fileStore{
		dagRuns:               newFileDAGRunStore(cfg, opts),
		queue:                 filequeue.New(cfg.Paths.QueueDir),
		services:              fileserviceregistry.New(cfg.Paths.ServiceRegistryDir),
		dispatchTasks:         filedistributed.NewDispatchTaskStore(distributedDir),
		workerHeartbeats:      filedistributed.NewWorkerHeartbeatStore(distributedDir),
		dagRunLeases:          filedistributed.NewDAGRunLeaseStore(distributedDir),
		activeDistributedRuns: filedistributed.NewActiveDistributedRunStore(distributedDir),
		audit:                 auditStore,
		auditCloser:           auditStore,
		users:                 userStore,
		apiKeys:               apiKeyStore,
		webhooks:              webhookStore,
		workspaces:            workspaceStore,
		sessions:              sessionStore,
		events:                eventStore,
	}, nil
}

func (s *fileStore) DAGRuns() exec.DAGRunStore {
	return s.dagRuns
}

func (s *fileStore) Queue() exec.QueueStore {
	return s.queue
}

func (s *fileStore) Services() exec.ServiceRegistry {
	return s.services
}

func (s *fileStore) DispatchTasks() exec.DispatchTaskStore {
	return s.dispatchTasks
}

func (s *fileStore) WorkerHeartbeats() exec.WorkerHeartbeatStore {
	return s.workerHeartbeats
}

func (s *fileStore) DAGRunLeases() exec.DAGRunLeaseStore {
	return s.dagRunLeases
}

func (s *fileStore) ActiveDistributedRuns() exec.ActiveDistributedRunStore {
	return s.activeDistributedRuns
}

func (s *fileStore) Audit() audit.Store {
	return s.audit
}

func (s *fileStore) Users() auth.UserStore {
	return s.users
}

func (s *fileStore) APIKeys() auth.APIKeyStore {
	return s.apiKeys
}

func (s *fileStore) Webhooks() auth.WebhookStore {
	return s.webhooks
}

func (s *fileStore) Workspaces() workspace.Store {
	return s.workspaces
}

func (s *fileStore) Sessions() agent.SessionStore {
	return s.sessions
}

func (s *fileStore) Events() eventstore.Store {
	return s.events
}

func (s *fileStore) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	if s.services != nil {
		s.services.Unregister(context.Background())
	}
	if s.auditCloser != nil {
		if err := s.auditCloser.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
