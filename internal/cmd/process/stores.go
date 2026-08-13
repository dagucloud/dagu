// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	authmodel "github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
)

// StoreRole identifies the process roles that need control-plane stores.
type StoreRole uint8

const (
	StoreRoleEvents StoreRole = 1 << iota
	StoreRoleServer
	StoreRoleScheduler
)

func (r StoreRole) has(role StoreRole) bool {
	return r&role != 0
}

// Stores contains optional control-plane persistence shared by process roles.
type Stores struct {
	frontend.Stores
	EventCollector func(context.Context)
}

// Frontend returns the subset consumed by the frontend server.
func (s Stores) Frontend() frontend.Stores {
	return s.Stores
}

// NewFileStores creates the file-backed stores needed by the selected roles.
func NewFileStores(ctx context.Context, cfg *config.Config, roles StoreRole) (Stores, error) {
	stores := Stores{}

	if roles.has(StoreRoleEvents) && cfg.EventStore.Enabled {
		store, err := file.NewEventStore(cfg)
		if err != nil {
			if roles.has(StoreRoleServer) {
				return Stores{}, fmt.Errorf("failed to initialize event store: %w", err)
			}
			logger.Warn(ctx, "Failed to initialize event store; continuing without event persistence", tag.Error(err))
		} else if store != nil {
			stores.Event = eventstore.New(store)
		}
	}

	if roles.has(StoreRoleScheduler) && stores.Event != nil {
		collector, err := file.NewEventCollector(cfg)
		if err != nil {
			logger.Warn(ctx, "Failed to initialize event collector; continuing without collection", tag.Error(err))
		} else {
			stores.EventCollector = collector.Start
		}
	}

	if !roles.has(StoreRoleServer) && !roles.has(StoreRoleScheduler) {
		return stores, nil
	}

	dagSettingsStore, err := file.NewDAGSettingsStore(cfg)
	if err != nil {
		if roles.has(StoreRoleScheduler) {
			return Stores{}, fmt.Errorf("failed to initialize DAG settings store: %w", err)
		}
		logger.Warn(ctx, "Failed to create DAG settings store", tag.Error(err))
	} else {
		stores.DAGSettings = dagSettingsStore
	}
	stores.Profile = file.NewProfileStore(ctx, cfg)

	if roles.has(StoreRoleServer) {
		if err := initFileServerStores(ctx, cfg, &stores); err != nil {
			return Stores{}, err
		}
	}

	if roles.has(StoreRoleServer) || stores.Event != nil {
		initEncryptedStores(ctx, cfg, &stores, roles.has(StoreRoleServer))
	}
	return stores, nil
}

func initFileServerStores(ctx context.Context, cfg *config.Config, stores *Stores) error {
	stores.WorkspaceBaseConfig = func(workspaceName string) (dagsettings.BaseConfigStore, error) {
		return file.NewWorkspaceBaseConfigStore(cfg.Paths.DAGsDir, workspaceName)
	}
	if cfg.Paths.BaseConfig != "" {
		baseConfigStore, err := file.NewBaseConfigStore(cfg.Paths.BaseConfig)
		if err != nil {
			logger.Warn(ctx, "Failed to create base config store", tag.Error(err))
		} else {
			stores.BaseConfig = baseConfigStore
		}
	}

	if cfg.Server.Auth.Mode == config.AuthModeBuiltin {
		builtinAuth, err := newBuiltinAuth(ctx, cfg)
		if err != nil {
			return fmt.Errorf("failed to initialize builtin auth service: %w", err)
		}
		stores.AuthService = builtinAuth.service
		stores.UserStore = builtinAuth.userStore
		stores.AuthSetupRequired = builtinAuth.setupRequired
	}

	stores.Secret = file.NewSecretStore(ctx, cfg)

	wikiStore, err := file.NewWikiStore(cfg)
	if err != nil {
		return fmt.Errorf("failed to create Wiki store: %w", err)
	}
	stores.Wiki = wikiStore

	workspaceStore, err := file.NewWorkspaceStore(cfg)
	if err != nil {
		logger.Warn(ctx, "Failed to create workspace store", tag.Error(err))
	} else {
		stores.Workspace = workspaceStore
	}

	auditStore, err := file.NewAuditStore(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize audit service: failed to create audit store: %w", err)
	}
	stores.Audit = auditStore

	viewStore, err := store.NewViewStore(file.NewCollection(cfg.Paths.ViewsDir, file.WithIndentedJSON()))
	if err != nil {
		logger.Warn(ctx, "Failed to create view store", tag.Error(err))
	} else {
		stores.View = viewStore
	}

	if cfg.Server.CheckUpdates {
		upgradeStore, err := file.NewUpgradeCheckStore(cfg)
		if err != nil {
			logger.Warn(ctx, "Failed to create upgrade check store", tag.Error(err))
		} else {
			stores.Upgrade = upgradeStore
		}
	}

	return nil
}

func initEncryptedStores(ctx context.Context, cfg *config.Config, stores *Stores, server bool) {
	encKey, err := crypto.ResolveKey(cfg.Paths.DataDir)
	if err != nil {
		logger.Warn(ctx, "Failed to resolve encryption key for encrypted stores", tag.Error(err))
		logger.Warn(ctx, "Notification settings store is disabled because encrypted storage is not available")
		logger.Warn(ctx, "Incident settings store is disabled because encrypted storage is not available")
		return
	}
	encryptor, err := crypto.NewEncryptor(encKey)
	if err != nil {
		logger.Warn(ctx, "Failed to create encryptor for encrypted stores", tag.Error(err))
		logger.Warn(ctx, "Notification settings store is disabled because encrypted storage is not available")
		logger.Warn(ctx, "Incident settings store is disabled because encrypted storage is not available")
		return
	}

	if server {
		remoteNodeStore, err := file.NewRemoteNodeStore(cfg, encryptor)
		if err != nil {
			logger.Warn(ctx, "Failed to create remote node store", tag.Error(err))
		} else {
			stores.RemoteNode = remoteNodeStore
		}
	}

	notificationStore, err := file.NewNotificationStore(cfg, encryptor)
	if err != nil {
		logger.Warn(ctx, "Failed to create notification settings store", tag.Error(err))
	} else {
		stores.Notification = notificationStore
		stateFile := file.NotificationMonitorStateFile(cfg)
		stores.NotificationState = filemonitor.NewStateStore(stateFile)
		stores.NewNotificationLease = newFileMonitorLease(stateFile)
	}

	incidentStore, err := file.NewIncidentStore(cfg, encryptor)
	if err != nil {
		logger.Warn(ctx, "Failed to create incident settings store", tag.Error(err))
	} else {
		stores.Incident = incidentStore
		stateFile := file.IncidentMonitorStateFile(cfg)
		stores.IncidentState = filemonitor.NewStateStore(stateFile)
		stores.NewIncidentLease = newFileMonitorLease(stateFile)
	}
}

func newFileMonitorLease(stateFile string) func() chatbridge.Lease {
	lockDir := filepath.Clean(stateFile) + ".lock"
	return func() chatbridge.Lease {
		lease := filemonitor.NewLease(stateFile, &dirlock.LockOptions{
			StaleThreshold: chatbridge.DefaultNotificationLockStaleThreshold,
			RetryInterval:  chatbridge.DefaultNotificationLockRetryInterval,
			OnWait: func() {
				slog.Info("Notification lock is held by another process; DAG run notifications are on standby",
					slog.String("lock_dir", lockDir),
				)
			},
		})
		if lease == nil {
			return nil
		}
		return lease
	}
}

type builtinAuth struct {
	service       *authservice.Service
	userStore     authmodel.UserStore
	setupRequired bool
}

func newBuiltinAuth(ctx context.Context, cfg *config.Config) (builtinAuth, error) {
	tokenSecret, err := resolveTokenSecret(ctx, cfg)
	if err != nil {
		return builtinAuth{}, fmt.Errorf("failed to resolve token secret: %w", err)
	}

	userStore, err := store.NewUserStore(file.NewCollection(cfg.Paths.UsersDir, file.WithIndentedJSON()))
	if err != nil {
		return builtinAuth{}, fmt.Errorf("failed to create user store: %w", err)
	}

	apiKeyStore, err := store.NewAPIKeyStore(file.NewCollection(cfg.Paths.APIKeysDir, file.WithIndentedJSON()))
	if err != nil {
		return builtinAuth{}, fmt.Errorf("failed to create API key store: %w", err)
	}

	var webhookEncryptor *crypto.Encryptor
	encKey, encErr := crypto.ResolveKey(cfg.Paths.DataDir)
	if encErr != nil {
		logger.Warn(ctx, "Failed to resolve encryption key for webhook store", tag.Error(encErr))
	} else {
		webhookEncryptor, encErr = crypto.NewEncryptor(encKey)
		if encErr != nil {
			logger.Warn(ctx, "Failed to create encryptor for webhook store", tag.Error(encErr))
		}
	}
	webhookStore, err := store.NewWebhookStore(file.NewCollection(cfg.Paths.WebhooksDir, file.WithIndentedJSON()), webhookEncryptor)
	if err != nil {
		return builtinAuth{}, fmt.Errorf("failed to create webhook store: %w", err)
	}

	authSvc := authservice.New(userStore, authservice.Config{
		TokenSecret: tokenSecret,
		TokenTTL:    cfg.Server.Auth.Builtin.Token.TTL,
	},
		authservice.WithAPIKeyStore(apiKeyStore),
		authservice.WithWebhookStore(webhookStore),
	)

	count, err := authSvc.CountUsers(ctx)
	if err != nil {
		return builtinAuth{}, fmt.Errorf("failed to count users: %w", err)
	}
	setupRequired := count == 0

	if setupRequired && cfg.Server.Auth.Builtin.InitialAdmin.IsConfigured() {
		ia := cfg.Server.Auth.Builtin.InitialAdmin

		lock := dirlock.New(cfg.Paths.UsersDir, &dirlock.LockOptions{
			StaleThreshold: 30 * time.Second,
			RetryInterval:  50 * time.Millisecond,
		})
		if err := lock.Lock(ctx); err != nil {
			return builtinAuth{}, fmt.Errorf("failed to acquire lock for initial admin provisioning: %w", err)
		}
		defer func() { _ = lock.Unlock() }()

		count, err = authSvc.CountUsers(ctx)
		if err != nil {
			return builtinAuth{}, fmt.Errorf("failed to re-check user count: %w", err)
		}

		if count == 0 {
			if _, err := authSvc.CreateUser(ctx, authservice.CreateUserInput{
				Username: ia.Username,
				Password: ia.Password,
				Role:     authmodel.RoleAdmin,
			}); err != nil {
				return builtinAuth{}, fmt.Errorf("failed to auto-provision initial admin user: %w", err)
			}
			logger.Info(ctx, "Auto-provisioned initial admin user")
		}
		setupRequired = false
	}

	logger.Info(ctx, "Builtin auth initialized", slog.Bool("setupRequired", setupRequired))
	return builtinAuth{service: authSvc, userStore: userStore, setupRequired: setupRequired}, nil
}

func resolveTokenSecret(ctx context.Context, cfg *config.Config) (authmodel.TokenSecret, error) {
	authDir := filepath.Join(cfg.Paths.DataDir, "auth")

	if cfg.Server.Auth.Builtin.Token.Secret != "" {
		secret, err := authmodel.NewTokenSecretFromString(cfg.Server.Auth.Builtin.Token.Secret)
		if err != nil {
			logger.Warn(ctx, "Invalid token secret from config, falling back to file-based secret", tag.Error(err))
		} else {
			secretPath := filepath.Join(authDir, "token_secret")
			if data, readErr := os.ReadFile(secretPath); readErr == nil { //nolint:gosec // path is constructed from trusted config dir + constant filename
				fileSecret := strings.TrimSpace(string(data))
				if fileSecret != "" && fileSecret != cfg.Server.Auth.Builtin.Token.Secret {
					logger.Warn(ctx, "Token secret in config differs from file-based secret - config value takes priority; "+
						"removing it from config will switch to the file-based secret and invalidate existing sessions",
						slog.String("file", secretPath))
				}
			}
			return secret, nil
		}
	}

	return file.ResolveTokenSecret(authDir)
}
