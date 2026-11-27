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

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/audit"
	authmodel "github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/connection/connect"
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	fileaudit "github.com/dagucloud/dagu/v2/internal/persis/file/audit"
	filebaseconfig "github.com/dagucloud/dagu/v2/internal/persis/file/baseconfig"
	filecalendar "github.com/dagucloud/dagu/v2/internal/persis/file/calendar"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	filewiki "github.com/dagucloud/dagu/v2/internal/persis/file/wiki"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// NewStores creates the file-backed stores used by the frontend service from
// the stores every project shares and the stores of the project it serves.
func NewStores(
	ctx context.Context,
	cfg *config.Config,
	server *persisfile.ServerStores,
	project *persisfile.ProjectSettings,
) (frontend.Stores, error) {
	stores := frontend.Stores{}

	if cfg.EventStore.Enabled {
		store, err := fileeventstore.New(project.Paths.EventStoreDir)
		if err != nil {
			return frontend.Stores{}, fmt.Errorf("failed to initialize event store: %w", err)
		}
		stores.Event = eventstore.New(store)
	}

	dagSettingsStore, err := project.DAGSettingsStore()
	if err != nil {
		logger.Warn(ctx, "Failed to create DAG settings store", tag.Error(err))
	} else {
		stores.DAGSettings = dagSettingsStore
	}
	stores.Profile = project.ProfileStore(ctx)

	if err := initStores(ctx, cfg, server, project, &stores); err != nil {
		return frontend.Stores{}, err
	}
	initEncryptedStores(ctx, cfg, server, project, &stores)
	return stores, nil
}

// enableAIModels gives the AI endpoint service its models lists (Spec 093, "The
// models list"). The reader calls the providers through the same client as the
// gateway: the proxy of ai.upstream.proxy_url only, TLS verification on, and no
// redirect. The timeout is ai.models_timeout_sec.
func enableAIModels(ctx context.Context, cfg *config.Config, server *persisfile.ServerStores, service *aiendpoint.Service) {
	if service == nil {
		return
	}
	client, err := aigateway.NewUpstreamClient(aigateway.LimitsFromConfig(cfg.AI.Relay), cfg.AI.Upstream.ProxyURL)
	if err != nil {
		logger.Warn(ctx, "Failed to create the client of the AI models lists", tag.Error(err))
		return
	}
	reader, err := aiendpoint.NewModelsReader(client, time.Duration(cfg.AI.ModelsTimeoutSec)*time.Second)
	if err != nil {
		logger.Warn(ctx, "Failed to create the reader of the AI models lists", tag.Error(err))
		return
	}
	server.EnableAIEndpointModels(ctx, service, reader)
}

func initStores(
	ctx context.Context,
	cfg *config.Config,
	server *persisfile.ServerStores,
	project *persisfile.ProjectSettings,
	stores *frontend.Stores,
) error {
	// Base config edits change every DAG definition, so they signal the
	// scheduler on other hosts the same way DAG edits do.
	changeMarker := filedag.NewChangeMarker(project.DAGChangeMarkerPath())
	stores.WorkspaceBaseConfig = func(workspaceName string) (dagsettings.BaseConfigStore, error) {
		return filebaseconfig.New(
			workspace.BaseConfigPath(project.Paths.DAGsDir, workspaceName),
			filebaseconfig.WithSkipDefault(true),
			filebaseconfig.WithOnChange(changeMarker.Bump),
		)
	}
	// Calendar edits change the business days of the DAGs that use them.
	if cfg.Paths.CalendarsDir != "" {
		calendarStore, err := server.CalendarStore(filecalendar.WithOnChange(changeMarker.Bump))
		if err != nil {
			return fmt.Errorf("failed to create calendar store: %w", err)
		}
		stores.Calendar = calendarStore
	}
	if cfg.Paths.BaseConfig != "" {
		baseConfigStore, err := filebaseconfig.New(cfg.Paths.BaseConfig, filebaseconfig.WithOnChange(changeMarker.Bump))
		if err != nil {
			logger.Warn(ctx, "Failed to create base config store", tag.Error(err))
		} else {
			stores.BaseConfig = baseConfigStore
		}
	}

	if cfg.ProjectsEnabled {
		stores.Projects = projectregistry.New(cfg.Paths.DataDir)
	}

	if cfg.Server.Auth.Mode == config.AuthModeBuiltin {
		builtinAuth, err := newBuiltinAuth(ctx, cfg, server, project)
		if err != nil {
			return fmt.Errorf("failed to initialize builtin auth service: %w", err)
		}
		stores.AuthService = builtinAuth.service
		stores.UserStore = builtinAuth.userStore
		stores.AuthSetupRequired = builtinAuth.setupRequired
		stores.PublicWebhooks = server.PublicWebhookStore()
	}

	stores.Secret = project.SecretStore(ctx)
	stores.AIEndpoint = server.AIEndpointService(ctx, stores.Secret)
	enableAIModels(ctx, cfg, server, stores.AIEndpoint)
	stores.AIMCPServer = server.AIMCPServerService(ctx, stores.Secret)

	wikiStore, err := newWikiStore(cfg)
	if err != nil {
		return fmt.Errorf("failed to create Wiki store: %w", err)
	}
	stores.Wiki = wikiStore
	if cfg.ProjectsEnabled && wikiStore != nil {
		// The API addresses the default project's wiki.
		stores.Wiki = newProjectWikis(cfg, stores.Projects, wikiStore).defaults
	}

	workspaceStore, err := newWorkspaceStore(cfg, server)
	if err != nil {
		logger.Warn(ctx, "Failed to create workspace store", tag.Error(err))
	} else {
		stores.Workspace = workspaceStore
	}

	if cfg.Server.Audit.Enabled {
		// Without a secret store, no entry is marked masked, so none is sent.
		auditStore, err := server.AuditStore(cfg.Server.Audit.RetentionDays,
			fileaudit.WithMasker(audit.NewMasker(stores.Secret)))
		if err != nil {
			return fmt.Errorf("failed to initialize audit service: failed to create audit store: %w", err)
		}
		stores.Audit = auditStore
	}

	stores.View = project.ViewStore()

	hostKeyStore, err := project.HostKeyStore()
	if err != nil {
		return fmt.Errorf("failed to create host key store: %w", err)
	}
	stores.HostKeys = hostKeyStore
	inventoryStore, err := project.CachedInventoryStore()
	if err != nil {
		return fmt.Errorf("failed to create inventory store: %w", err)
	}
	stores.Inventory = inventoryStore
	workerNodeStore, err := server.WorkerNodeStore()
	if err != nil {
		return fmt.Errorf("failed to create worker node store: %w", err)
	}
	stores.WorkerNodes = workerNodeStore

	dagPinStore, err := project.DAGPinStore()
	if err != nil {
		return fmt.Errorf("failed to create DAG pin store: %w", err)
	}
	stores.DAGPins = dagPinStore

	if cfg.Server.CheckUpdates {
		upgradeStore, err := server.UpgradeCheckStore()
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

func newWorkspaceStore(cfg *config.Config, server *persisfile.ServerStores) (*store.WorkspaceStore, error) {
	dir := cfg.Paths.WorkspacesDir
	if dir == "" {
		return nil, fmt.Errorf("workspace store: WorkspacesDir cannot be empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("workspace store: create directory %s: %w", dir, err)
	}
	return server.WorkspaceStore()
}

func initEncryptedStores(
	ctx context.Context,
	cfg *config.Config,
	server *persisfile.ServerStores,
	project *persisfile.ProjectSettings,
	stores *frontend.Stores,
) {
	encryptor, err := server.Encryptor()
	if err != nil {
		logger.Warn(ctx, "Failed to prepare encryption for encrypted stores", tag.Error(err))
		logger.Warn(ctx, "Notification settings store is disabled because encrypted storage is not available")
		logger.Warn(ctx, "Incident settings store is disabled because encrypted storage is not available")
		return
	}

	connectEngine, err := newConnectEngine(cfg, server, encryptor)
	if err != nil {
		logger.Warn(ctx, "Failed to create connect engine", tag.Error(err))
	} else {
		stores.Connect = connectEngine
	}

	remoteNodeStore, err := newRemoteNodeStore(cfg, server, encryptor)
	if err != nil {
		logger.Warn(ctx, "Failed to create remote node store", tag.Error(err))
	} else {
		stores.RemoteNode = remoteNodeStore
	}

	notificationStore, err := project.NotificationStore()
	if err != nil {
		logger.Warn(ctx, "Failed to create notification settings store", tag.Error(err))
	} else {
		stores.Notification = notificationStore
		stateFile := project.NotificationStateFile()
		stores.NotificationState = filemonitor.NewStateStore(stateFile)
		stores.NewNotificationLease = newMonitorLease(stateFile)
	}

	incidentStore, err := project.IncidentStore()
	if err != nil {
		logger.Warn(ctx, "Failed to create incident settings store", tag.Error(err))
	} else {
		stores.Incident = incidentStore
		stateFile := project.IncidentStateFile()
		stores.IncidentState = filemonitor.NewStateStore(stateFile)
		stores.NewIncidentLease = newMonitorLease(stateFile)
	}
}

// newConnectEngine runs connect sessions over the server's connection
// store. Providers register as they are added.
func newConnectEngine(
	cfg *config.Config,
	server *persisfile.ServerStores,
	encryptor *crypto.Encryptor,
) (*connect.Engine, error) {
	connections, err := server.ConnectionStore()
	if err != nil {
		return nil, err
	}
	return connect.New(connect.Config{
		Store:           connections,
		Encryptor:       encryptor,
		AnyUserConfirms: cfg.Server.Auth.Mode == config.AuthModeNone,
	})
}

func newRemoteNodeStore(
	cfg *config.Config,
	server *persisfile.ServerStores,
	encryptor *crypto.Encryptor,
) (*store.RemoteNodeStore, error) {
	dir := cfg.Paths.RemoteNodesDir
	if dir == "" {
		return nil, fmt.Errorf("remote-node store: RemoteNodesDir cannot be empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("remote-node store: create directory %s: %w", dir, err)
	}
	return server.RemoteNodeStore(encryptor)
}

func newMonitorLease(stateFile string) func() chatbridge.Lease {
	lockDir := filepath.Clean(stateFile) + ".lock"
	return func() chatbridge.Lease {
		return filemonitor.NewLease(stateFile, &dirlock.LockOptions{
			StaleThreshold: chatbridge.DefaultNotificationLockStaleThreshold,
			RetryInterval:  chatbridge.DefaultNotificationLockStandbyRetryInterval,
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

func newBuiltinAuth(
	ctx context.Context,
	cfg *config.Config,
	server *persisfile.ServerStores,
	project *persisfile.ProjectSettings,
) (builtinAuth, error) {
	tokenSecret, err := resolveTokenSecret(ctx, cfg)
	if err != nil {
		return builtinAuth{}, fmt.Errorf("failed to resolve token secret: %w", err)
	}

	userStore, err := server.UserStore()
	if err != nil {
		return builtinAuth{}, fmt.Errorf("failed to create user store: %w", err)
	}

	apiKeyStore, err := server.APIKeyStore()
	if err != nil {
		return builtinAuth{}, fmt.Errorf("failed to create API key store: %w", err)
	}

	webhookEncryptor, encErr := server.Encryptor()
	if encErr != nil {
		logger.Warn(ctx, "Failed to prepare encryption for webhook store", tag.Error(encErr))
	}
	webhookStore, err := project.WebhookStore(webhookEncryptor)
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
