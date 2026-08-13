// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authmodel "github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
)

func storesTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

func storesTestConfig(tmpDir string, ia config.InitialAdmin) *config.Config {
	return &config.Config{
		Paths: config.PathsConfig{
			UsersDir:    filepath.Join(tmpDir, "users"),
			APIKeysDir:  filepath.Join(tmpDir, "apikeys"),
			WebhooksDir: filepath.Join(tmpDir, "webhooks"),
			DataDir:     filepath.Join(tmpDir, "data"),
		},
		Server: config.Server{
			Auth: config.Auth{
				Mode: config.AuthModeBuiltin,
				Builtin: config.AuthBuiltin{
					Token: config.TokenConfig{
						Secret: "test-secret-for-jwt-signing",
						TTL:    24 * time.Hour,
					},
					InitialAdmin: ia,
				},
			},
		},
	}
}

func TestNewBuiltinAuthServiceAutoProvision(t *testing.T) {
	t.Parallel()

	t.Run("ProvisionsAdminWhenNoUsers", func(t *testing.T) {
		t.Parallel()
		cfg := storesTestConfig(t.TempDir(), config.InitialAdmin{
			Username: "testadmin",
			Password: "securepass123",
		})

		result, err := newBuiltinAuth(storesTestContext(t), cfg)
		require.NoError(t, err)
		assert.False(t, result.setupRequired, "setup should not be required after auto-provisioning")

		count, err := result.service.CountUsers(storesTestContext(t))
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)

		user, err := result.userStore.GetByUsername(storesTestContext(t), "testadmin")
		require.NoError(t, err)
		assert.Equal(t, "testadmin", user.Username)
		assert.Equal(t, authmodel.RoleAdmin, user.Role)
	})

	t.Run("SkipsWhenUsersExist", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		cfg := storesTestConfig(tmpDir, config.InitialAdmin{
			Username: "testadmin",
			Password: "securepass123",
		})

		store, err := persiststore.NewUserStore(file.NewCollection(cfg.Paths.UsersDir))
		require.NoError(t, err)
		existing := authmodel.NewUser("existinguser", "$2a$12$K8gHXqrFdFvMwJBG0VlJGuAGz3FwBmTm8xnNQblN2tCxrQgPLmwHa", authmodel.RoleAdmin)
		require.NoError(t, store.Create(storesTestContext(t), existing))

		result, err := newBuiltinAuth(storesTestContext(t), cfg)
		require.NoError(t, err)
		assert.False(t, result.setupRequired)

		count, err := result.service.CountUsers(storesTestContext(t))
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})

	t.Run("SkipsWhenNotConfigured", func(t *testing.T) {
		t.Parallel()
		cfg := storesTestConfig(t.TempDir(), config.InitialAdmin{})

		result, err := newBuiltinAuth(storesTestContext(t), cfg)
		require.NoError(t, err)
		assert.True(t, result.setupRequired, "setup should be required when initial_admin is not configured")
	})

	t.Run("FailsOnInvalidPassword", func(t *testing.T) {
		t.Parallel()
		cfg := storesTestConfig(t.TempDir(), config.InitialAdmin{
			Username: "testadmin",
			Password: "short",
		})

		_, err := newBuiltinAuth(storesTestContext(t), cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to auto-provision initial admin user")
	})

	t.Run("Idempotent", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		cfg := storesTestConfig(tmpDir, config.InitialAdmin{
			Username: "testadmin",
			Password: "securepass123",
		})

		result, err := newBuiltinAuth(storesTestContext(t), cfg)
		require.NoError(t, err)
		assert.False(t, result.setupRequired)

		result, err = newBuiltinAuth(storesTestContext(t), cfg)
		require.NoError(t, err)
		assert.False(t, result.setupRequired)

		count, err := result.service.CountUsers(storesTestContext(t))
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})
}

func TestNewBuiltinAuthServiceUserCanAuthenticate(t *testing.T) {
	t.Parallel()
	cfg := storesTestConfig(t.TempDir(), config.InitialAdmin{
		Username: "authadmin",
		Password: "mypassword123",
	})

	result, err := newBuiltinAuth(storesTestContext(t), cfg)
	require.NoError(t, err)

	user, err := result.service.Authenticate(storesTestContext(t), "authadmin", "mypassword123")
	require.NoError(t, err)
	assert.Equal(t, "authadmin", user.Username)
	assert.Equal(t, authmodel.RoleAdmin, user.Role)

	_, err = result.service.Authenticate(storesTestContext(t), "authadmin", "wrongpassword")
	require.Error(t, err)
}

func TestResolveTokenSecret(t *testing.T) {
	t.Parallel()

	t.Run("configured secret takes precedence", func(t *testing.T) {
		t.Parallel()
		cfg := storesTestConfig(t.TempDir(), config.InitialAdmin{})
		ctx := storesTestContext(t)
		authDir := filepath.Join(cfg.Paths.DataDir, "auth")
		require.NoError(t, os.MkdirAll(authDir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(authDir, "token_secret"), []byte("file-secret"), 0o600))

		secret, err := resolveTokenSecret(ctx, cfg)
		require.NoError(t, err)
		assert.Equal(t, []byte(cfg.Server.Auth.Builtin.Token.Secret), secret.SigningKey())
	})

	t.Run("persistent secret is used when configuration is empty", func(t *testing.T) {
		t.Parallel()
		cfg := storesTestConfig(t.TempDir(), config.InitialAdmin{})
		ctx := storesTestContext(t)
		cfg.Server.Auth.Builtin.Token.Secret = ""

		first, err := resolveTokenSecret(ctx, cfg)
		require.NoError(t, err)
		second, err := resolveTokenSecret(ctx, cfg)
		require.NoError(t, err)
		assert.Equal(t, first.SigningKey(), second.SigningKey())
	})
}

func TestNewFileStoresAppliesRoleFailurePolicy(t *testing.T) {
	t.Parallel()

	t.Run("event-only continues when event storage is unavailable", func(t *testing.T) {
		t.Parallel()
		blocker := filepath.Join(t.TempDir(), "blocker")
		require.NoError(t, os.WriteFile(blocker, []byte("not a directory"), 0o600))
		cfg := &config.Config{
			EventStore: config.EventStoreConfig{Enabled: true},
			Paths:      config.PathsConfig{EventStoreDir: filepath.Join(blocker, "events")},
		}

		_, err := NewFileStores(context.Background(), cfg, StoreRoleEvents)
		require.NoError(t, err)

		_, err = NewFileStores(context.Background(), cfg, StoreRoleEvents|StoreRoleServer)
		require.ErrorContains(t, err, "failed to initialize event store")
	})

	t.Run("scheduler requires DAG settings storage", func(t *testing.T) {
		t.Parallel()
		cfg := &config.Config{}

		_, err := NewFileStores(context.Background(), cfg, StoreRoleEvents)
		require.NoError(t, err)

		_, err = NewFileStores(context.Background(), cfg, StoreRoleScheduler)
		require.ErrorContains(t, err, "failed to initialize DAG settings store")
	})
}

func TestNewFileStoresProvidesWorkspaceBaseConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := &config.Config{
		Paths: config.PathsConfig{
			DAGsDir:        filepath.Join(root, "dags"),
			WikiDir:        filepath.Join(root, "wiki"),
			DataDir:        filepath.Join(root, "data"),
			BaseConfig:     filepath.Join(root, "base.yaml"),
			RemoteNodesDir: filepath.Join(root, "remote-nodes"),
			WorkspacesDir:  filepath.Join(root, "workspaces"),
			ViewsDir:       filepath.Join(root, "views"),
		},
		Server: config.Server{Auth: config.Auth{Mode: config.AuthModeNone}},
	}

	stores, err := NewFileStores(context.Background(), cfg, StoreRoleServer)
	require.NoError(t, err)
	require.NotNil(t, stores.WorkspaceBaseConfig)

	workspaceStore, err := stores.WorkspaceBaseConfig("operations")
	require.NoError(t, err)
	require.NoError(t, workspaceStore.UpdateSpec(context.Background(), []byte("max_active_runs: 2\n")))
	spec, err := workspaceStore.GetSpec(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "max_active_runs: 2\n", spec)
}
