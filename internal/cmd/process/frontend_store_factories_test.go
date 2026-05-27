// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authmodel "github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/persis/file"
	persiststore "github.com/dagucloud/dagu/internal/persis/store"
)

func frontendStoreFactoryTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

func frontendStoreFactoryTestConfig(tmpDir string, ia config.InitialAdmin) *config.Config {
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
		cfg := frontendStoreFactoryTestConfig(t.TempDir(), config.InitialAdmin{
			Username: "testadmin",
			Password: "securepass123",
		})

		result, setupRequired, err := newBuiltinAuthService(frontendStoreFactoryTestContext(t), cfg)
		require.NoError(t, err)
		assert.False(t, setupRequired, "setup should not be required after auto-provisioning")

		count, err := result.AuthService.CountUsers(frontendStoreFactoryTestContext(t))
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)

		user, err := result.UserStore.GetByUsername(frontendStoreFactoryTestContext(t), "testadmin")
		require.NoError(t, err)
		assert.Equal(t, "testadmin", user.Username)
		assert.Equal(t, authmodel.RoleAdmin, user.Role)
	})

	t.Run("SkipsWhenUsersExist", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		cfg := frontendStoreFactoryTestConfig(tmpDir, config.InitialAdmin{
			Username: "testadmin",
			Password: "securepass123",
		})

		store, err := persiststore.NewUserStore(file.NewCollection(cfg.Paths.UsersDir))
		require.NoError(t, err)
		existing := authmodel.NewUser("existinguser", "$2a$12$K8gHXqrFdFvMwJBG0VlJGuAGz3FwBmTm8xnNQblN2tCxrQgPLmwHa", authmodel.RoleAdmin)
		require.NoError(t, store.Create(frontendStoreFactoryTestContext(t), existing))

		result, setupRequired, err := newBuiltinAuthService(frontendStoreFactoryTestContext(t), cfg)
		require.NoError(t, err)
		assert.False(t, setupRequired)

		count, err := result.AuthService.CountUsers(frontendStoreFactoryTestContext(t))
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})

	t.Run("SkipsWhenNotConfigured", func(t *testing.T) {
		t.Parallel()
		cfg := frontendStoreFactoryTestConfig(t.TempDir(), config.InitialAdmin{})

		_, setupRequired, err := newBuiltinAuthService(frontendStoreFactoryTestContext(t), cfg)
		require.NoError(t, err)
		assert.True(t, setupRequired, "setup should be required when initial_admin is not configured")
	})

	t.Run("FailsOnInvalidPassword", func(t *testing.T) {
		t.Parallel()
		cfg := frontendStoreFactoryTestConfig(t.TempDir(), config.InitialAdmin{
			Username: "testadmin",
			Password: "short",
		})

		_, _, err := newBuiltinAuthService(frontendStoreFactoryTestContext(t), cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to auto-provision initial admin user")
	})

	t.Run("Idempotent", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		cfg := frontendStoreFactoryTestConfig(tmpDir, config.InitialAdmin{
			Username: "testadmin",
			Password: "securepass123",
		})

		_, setupRequired, err := newBuiltinAuthService(frontendStoreFactoryTestContext(t), cfg)
		require.NoError(t, err)
		assert.False(t, setupRequired)

		result, setupRequired, err := newBuiltinAuthService(frontendStoreFactoryTestContext(t), cfg)
		require.NoError(t, err)
		assert.False(t, setupRequired)

		count, err := result.AuthService.CountUsers(frontendStoreFactoryTestContext(t))
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})
}

func TestNewBuiltinAuthServiceUserCanAuthenticate(t *testing.T) {
	t.Parallel()
	cfg := frontendStoreFactoryTestConfig(t.TempDir(), config.InitialAdmin{
		Username: "authadmin",
		Password: "mypassword123",
	})

	result, _, err := newBuiltinAuthService(frontendStoreFactoryTestContext(t), cfg)
	require.NoError(t, err)

	user, err := result.AuthService.Authenticate(frontendStoreFactoryTestContext(t), "authadmin", "mypassword123")
	require.NoError(t, err)
	assert.Equal(t, "authadmin", user.Username)
	assert.Equal(t, authmodel.RoleAdmin, user.Role)

	_, err = result.AuthService.Authenticate(frontendStoreFactoryTestContext(t), "authadmin", "wrongpassword")
	require.Error(t, err)
}
