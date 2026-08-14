// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package file creates file-backed dependencies for the frontend service.
package file

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
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	fileaudit "github.com/dagucloud/dagu/v2/internal/persis/file/audit"
	filebaseconfig "github.com/dagucloud/dagu/v2/internal/persis/file/baseconfig"
	fileincident "github.com/dagucloud/dagu/v2/internal/persis/file/incident"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	filenotification "github.com/dagucloud/dagu/v2/internal/persis/file/notification"
	filewiki "github.com/dagucloud/dagu/v2/internal/persis/file/wiki"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// NewStores creates the file-backed stores used by the frontend service.
func NewStores(ctx context.Context, cfg *config.Config) (frontend.Stores, error) {
	stores := frontend.Stores{}

	if cfg.EventStore.Enabled {
		store, err := persisfile.NewEventStore(cfg)
		if err != nil {
			return frontend.Stores{}, fmt.Errorf("failed to initialize event store: %w", err)
		} else if store != nil {
			stores.Event = eventstore.New(store)
		}
	}

	dagSettingsStore, err := persisfile.NewDAGSettingsStore(cfg)
	if err != nil {
		logger.Warn(ctx, "Failed to create DAG settings store", tag.Error(err))
	} else {
		stores.DAGSettings = dagSettingsStore
	}
	stores.Profile = persisfile.NewProfileStore(ctx, cfg)

	if err := initStores(ctx, cfg, &stores); err != nil {
		return frontend.Stores{}, err
	}
	initEncryptedStores(ctx, cfg, &stores)
	return stores, nil
}

func initStores(ctx context.Context, cfg *config.Config, stores *frontend.Stores) error {
	stores.WorkspaceBaseConfig = func(workspaceName string) (dagsettings.BaseConfigStore, error) {
		return filebaseconfig.New(
			workspace.BaseConfigPath(cfg.Paths.DAGsDir, workspaceName),
			filebaseconfig.WithSkipDefault(true),
		)
	}
	if cfg.Paths.BaseConfig != "" {
		baseConfigStore, err := filebaseconfig.New(cfg.Paths.BaseConfig)
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

	stores.Secret = persisfile.NewSecretStore(ctx, cfg)

	wikiStore, err := newWikiStore(cfg)
	if err != nil {
		return fmt.Errorf("failed to create Wiki store: %w", err)
	}
	stores.Wiki = wikiStore

	workspaceStore, err := newWorkspaceStore(cfg)
	if err != nil {
		logger.Warn(ctx, "Failed to create workspace store", tag.Error(err))
	} else {
		stores.Workspace = workspaceStore
	}

	if cfg.Server.Audit.Enabled {
		auditStore, err := fileaudit.New(filepath.Join(cfg.Paths.AdminLogsDir, "audit"), cfg.Server.Audit.RetentionDays)
		if err != nil {
			return fmt.Errorf("failed to initialize audit service: failed to create audit store: %w", err)
		}
		stores.Audit = auditStore
	}

	viewStore, err := store.NewViewStore(persisfile.NewCollection(cfg.Paths.ViewsDir, persisfile.WithIndentedJSON()))
	if err != nil {
		logger.Warn(ctx, "Failed to create view store", tag.Error(err))
	} else {
		stores.View = viewStore
	}

	if cfg.Server.CheckUpdates {
		upgradeStore, err := persisfile.NewUpgradeCheckStore(cfg)
		if err != nil {
			logger.Warn(ctx, "Failed to create upgrade check store", tag.Error(err))
		} else {
			stores.Upgrade = upgradeStore
		}
	}

	return nil
}

func newWikiStore(cfg *config.Config) (*filewiki.Store, error) {
	if cfg.Paths.WikiDir == "" {
		return nil, nil
	}
	opts := []filewiki.Option{filewiki.WithLegacyLayout(cfg.Paths.WikiDirLegacy)}
	if cfg.Paths.DataDir != "" {
		dataDir, err := selectWikiStoragePath(
			filepath.Join(cfg.Paths.DataDir, "wiki"),
			filepath.Join(cfg.Paths.DataDir, "docs"),
			cfg.Paths.WikiDirLegacy,
		)
		if err != nil {
			return nil, fmt.Errorf("wiki store: %w", err)
		}
		opts = append(opts, filewiki.WithDataDir(dataDir))
	}
	wikiStore, err := filewiki.New(cfg.Paths.WikiDir, opts...)
	if err != nil {
		return nil, fmt.Errorf("wiki store: %w", err)
	}
	return wikiStore, nil
}

func selectWikiStoragePath(canonical, legacy string, preferLegacy bool) (string, error) {
	canonicalExists, err := storePathExists(canonical)
	if err != nil {
		return "", err
	}
	legacyExists, err := storePathExists(legacy)
	if err != nil {
		return "", err
	}
	if canonicalExists && legacyExists {
		return "", fmt.Errorf("both %s and %s exist; reconcile them before starting Dagu", canonical, legacy)
	}
	if legacyExists || (!canonicalExists && preferLegacy) {
		return legacy, nil
	}
	return canonical, nil
}

func storePathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	if parent, parentErr := os.Stat(filepath.Dir(path)); parentErr == nil && !parent.IsDir() {
		return false, nil
	}
	return false, fmt.Errorf("inspect %s: %w", path, err)
}

func newWorkspaceStore(cfg *config.Config) (*store.WorkspaceStore, error) {
	dir := cfg.Paths.WorkspacesDir
	if dir == "" {
		return nil, fmt.Errorf("workspace store: WorkspacesDir cannot be empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("workspace store: create directory %s: %w", dir, err)
	}
	return store.NewWorkspaceStore(persisfile.NewCollection(dir, persisfile.WithIndentedJSON()))
}

func initEncryptedStores(ctx context.Context, cfg *config.Config, stores *frontend.Stores) {
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

	remoteNodeStore, err := newRemoteNodeStore(cfg, encryptor)
	if err != nil {
		logger.Warn(ctx, "Failed to create remote node store", tag.Error(err))
	} else {
		stores.RemoteNode = remoteNodeStore
	}

	notificationStore, err := filenotification.New(
		filepath.Join(cfg.Paths.DataDir, "notifications", "dags"),
		filenotification.WithEncryptor(encryptor),
	)
	if err != nil {
		logger.Warn(ctx, "Failed to create notification settings store", tag.Error(err))
	} else {
		stores.Notification = notificationStore
		stateFile := filepath.Join(cfg.Paths.DataDir, "notifications", "monitor-state.json")
		stores.NotificationState = filemonitor.NewStateStore(stateFile)
		stores.NewNotificationLease = newMonitorLease(stateFile)
	}

	incidentStore, err := fileincident.New(
		filepath.Join(cfg.Paths.DataDir, "incidents"),
		fileincident.WithEncryptor(encryptor),
	)
	if err != nil {
		logger.Warn(ctx, "Failed to create incident settings store", tag.Error(err))
	} else {
		stores.Incident = incidentStore
		stateFile := filepath.Join(cfg.Paths.DataDir, "incidents", "monitor-state.json")
		stores.IncidentState = filemonitor.NewStateStore(stateFile)
		stores.NewIncidentLease = newMonitorLease(stateFile)
	}
}

func newRemoteNodeStore(cfg *config.Config, encryptor *crypto.Encryptor) (*store.RemoteNodeStore, error) {
	dir := cfg.Paths.RemoteNodesDir
	if dir == "" {
		return nil, fmt.Errorf("remote-node store: RemoteNodesDir cannot be empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("remote-node store: create directory %s: %w", dir, err)
	}
	return store.NewRemoteNodeStore(persisfile.NewCollection(dir, persisfile.WithIndentedJSON()), encryptor)
}

func newMonitorLease(stateFile string) func() chatbridge.Lease {
	lockDir := filepath.Clean(stateFile) + ".lock"
	return func() chatbridge.Lease {
		return filemonitor.NewLease(stateFile, &dirlock.LockOptions{
			StaleThreshold: chatbridge.DefaultNotificationLockStaleThreshold,
			RetryInterval:  chatbridge.DefaultNotificationLockRetryInterval,
			OnWait: func() {
				slog.Info("Notification lock is held by another process; DAG run notifications are on standby",
					slog.String("lock_dir", lockDir),
				)
			},
		})
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

	userStore, err := store.NewUserStore(persisfile.NewCollection(cfg.Paths.UsersDir, persisfile.WithIndentedJSON()))
	if err != nil {
		return builtinAuth{}, fmt.Errorf("failed to create user store: %w", err)
	}

	apiKeyStore, err := store.NewAPIKeyStore(persisfile.NewCollection(cfg.Paths.APIKeysDir, persisfile.WithIndentedJSON()))
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
	webhookStore, err := store.NewWebhookStore(persisfile.NewCollection(cfg.Paths.WebhooksDir, persisfile.WithIndentedJSON()), webhookEncryptor)
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

	return resolvePersistedTokenSecret(authDir)
}
