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

	"github.com/dagucloud/dagu/internal/agent"
	authmodel "github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/auth/tokensecret"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/baseconfig"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
	"github.com/dagucloud/dagu/internal/service/frontend"
)

// NewFrontendStoreFactories returns the file-backed persistence wiring for the frontend server.
func NewFrontendStoreFactories() frontend.StoreFactories {
	return frontend.StoreFactories{
		SnapshotStoreFactory:             file.NewSnapshotStores,
		WorkspaceBaseConfigStoreFactory:  file.NewWorkspaceBaseConfigStore,
		BaseConfigStoreFactory:           newBaseConfigStore,
		AgentStoresFactory:               newFrontendAgentStores,
		AgentSessionStoreFactory:         file.NewAgentSessionStore,
		DocStoreFactory:                  file.NewDocStore,
		BuiltinAuthFactory:               newBuiltinAuthService,
		RemoteNodeStoreFactory:           file.NewRemoteNodeStore,
		NotificationStoreFactory:         file.NewNotificationStore,
		NotificationMonitorStateFileFunc: file.NotificationMonitorStateFile,
		IncidentStoreFactory:             file.NewIncidentStore,
		IncidentMonitorStateFileFunc:     file.IncidentMonitorStateFile,
		WorkspaceStoreFactory:            file.NewWorkspaceStore,
		UpgradeCheckStoreFactory:         file.NewUpgradeCheckStore,
		AuditStoreFactory:                newAuditStore,
		EventStoreFactory:                file.NewEventStore,
	}
}

func newBaseConfigStore(filePath string) (baseconfig.Store, error) {
	return file.NewBaseConfigStore(filePath)
}

func newAuditStore(cfg *config.Config) (frontend.AuditStore, error) {
	return file.NewAuditStore(cfg)
}

func newFrontendAgentStores(ctx context.Context, cfg *config.Config, opts frontend.AgentStoresOptions) agent.RuntimeStores {
	fileOpts := make([]file.AgentStoresOption, 0, 3)
	if opts.MemoryCache != nil {
		fileOpts = append(fileOpts, file.WithAgentMemoryCache(opts.MemoryCache))
	}
	if opts.SeedReferences {
		fileOpts = append(fileOpts, file.WithAgentSeedReferences())
	}
	if opts.SeedExampleSouls {
		fileOpts = append(fileOpts, file.WithAgentSeedExampleSouls())
	}
	return file.NewAgentStores(ctx, cfg, fileOpts...)
}

// newBuiltinAuthService creates the file-backed auth store and authentication service.
// It uses the token secret provider chain to resolve the JWT signing secret,
// auto-generating and persisting one if not configured.
func newBuiltinAuthService(ctx context.Context, cfg *config.Config) (*frontend.BuiltinAuthResult, bool, error) {
	tokenSecret, err := buildTokenSecretProvider(ctx, cfg).Resolve(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to resolve token secret: %w", err)
	}

	userStore, err := store.NewUserStore(file.NewCollection(cfg.Paths.UsersDir))
	if err != nil {
		return nil, false, fmt.Errorf("failed to create user store: %w", err)
	}

	apiKeyStore, err := store.NewAPIKeyStore(file.NewCollection(cfg.Paths.APIKeysDir))
	if err != nil {
		return nil, false, fmt.Errorf("failed to create API key store: %w", err)
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
	webhookStore, err := store.NewWebhookStore(file.NewCollection(cfg.Paths.WebhooksDir), webhookEncryptor)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create webhook store: %w", err)
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
		return nil, false, fmt.Errorf("failed to count users: %w", err)
	}
	setupRequired := count == 0

	if setupRequired && cfg.Server.Auth.Builtin.InitialAdmin.IsConfigured() {
		ia := cfg.Server.Auth.Builtin.InitialAdmin

		lock := dirlock.New(cfg.Paths.UsersDir, &dirlock.LockOptions{
			StaleThreshold: 30 * time.Second,
			RetryInterval:  50 * time.Millisecond,
		})
		if err := lock.Lock(ctx); err != nil {
			return nil, false, fmt.Errorf("failed to acquire lock for initial admin provisioning: %w", err)
		}
		defer func() { _ = lock.Unlock() }()

		count, err = authSvc.CountUsers(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("failed to re-check user count: %w", err)
		}

		if count == 0 {
			if _, err := authSvc.CreateUser(ctx, authservice.CreateUserInput{
				Username: ia.Username,
				Password: ia.Password,
				Role:     authmodel.RoleAdmin,
			}); err != nil {
				return nil, false, fmt.Errorf("failed to auto-provision initial admin user: %w", err)
			}
			logger.Info(ctx, "Auto-provisioned initial admin user")
		}
		setupRequired = false
	}

	logger.Info(ctx, "Builtin auth initialized",
		slog.Bool("setupRequired", setupRequired),
	)

	return &frontend.BuiltinAuthResult{
		AuthService: authSvc,
		UserStore:   userStore,
	}, setupRequired, nil
}

// buildTokenSecretProvider constructs the token secret provider chain.
// Priority: 1. Static from config/env, 2. File-based auto-generated secret.
func buildTokenSecretProvider(ctx context.Context, cfg *config.Config) authmodel.TokenSecretProvider {
	var providers []authmodel.TokenSecretProvider

	authDir := filepath.Join(cfg.Paths.DataDir, "auth")

	if cfg.Server.Auth.Builtin.Token.Secret != "" {
		staticProvider, err := tokensecret.NewStatic(cfg.Server.Auth.Builtin.Token.Secret)
		if err != nil {
			logger.Warn(ctx, "Invalid token secret from config, falling back to file-based secret",
				tag.Error(err))
		} else {
			providers = append(providers, staticProvider)

			secretPath := filepath.Join(authDir, "token_secret")
			if data, readErr := os.ReadFile(secretPath); readErr == nil { //nolint:gosec // path is constructed from trusted config dir + constant filename
				fileSecret := strings.TrimSpace(string(data))
				if fileSecret != "" && fileSecret != cfg.Server.Auth.Builtin.Token.Secret {
					logger.Warn(ctx, "Token secret in config differs from file-based secret - config value takes priority; "+
						"removing it from config will switch to the file-based secret and invalidate existing sessions",
						slog.String("file", secretPath))
				}
			}
		}
	}

	providers = append(providers, file.NewTokenSecretProvider(cfg))

	return tokensecret.NewChain(providers...)
}
