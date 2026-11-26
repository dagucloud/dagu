// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/audit"
	authmodel "github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
)

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

		result, err := newBuiltinAuth(t.Context(), cfg, persisfile.NewServerStores(cfg))
		require.NoError(t, err)
		assert.False(t, result.setupRequired, "setup should not be required after auto-provisioning")

		count, err := result.service.CountUsers(t.Context())
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)

		user, err := result.userStore.GetByUsername(t.Context(), "testadmin")
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

		store, err := persiststore.NewUserStore(persisfile.NewCollection(cfg.Paths.UsersDir))
		require.NoError(t, err)
		existing := authmodel.NewUser("existinguser", "$2a$12$K8gHXqrFdFvMwJBG0VlJGuAGz3FwBmTm8xnNQblN2tCxrQgPLmwHa", authmodel.RoleAdmin)
		require.NoError(t, store.Create(t.Context(), existing))

		result, err := newBuiltinAuth(t.Context(), cfg, persisfile.NewServerStores(cfg))
		require.NoError(t, err)
		assert.False(t, result.setupRequired)

		count, err := result.service.CountUsers(t.Context())
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})

	t.Run("SkipsWhenNotConfigured", func(t *testing.T) {
		t.Parallel()
		cfg := storesTestConfig(t.TempDir(), config.InitialAdmin{})

		result, err := newBuiltinAuth(t.Context(), cfg, persisfile.NewServerStores(cfg))
		require.NoError(t, err)
		assert.True(t, result.setupRequired, "setup should be required when initial_admin is not configured")
	})

	t.Run("FailsOnInvalidPassword", func(t *testing.T) {
		t.Parallel()
		cfg := storesTestConfig(t.TempDir(), config.InitialAdmin{
			Username: "testadmin",
			Password: "short",
		})

		_, err := newBuiltinAuth(t.Context(), cfg, persisfile.NewServerStores(cfg))
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

		result, err := newBuiltinAuth(t.Context(), cfg, persisfile.NewServerStores(cfg))
		require.NoError(t, err)
		assert.False(t, result.setupRequired)

		result, err = newBuiltinAuth(t.Context(), cfg, persisfile.NewServerStores(cfg))
		require.NoError(t, err)
		assert.False(t, result.setupRequired)

		count, err := result.service.CountUsers(t.Context())
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

	result, err := newBuiltinAuth(t.Context(), cfg, persisfile.NewServerStores(cfg))
	require.NoError(t, err)

	user, err := result.service.Authenticate(t.Context(), "authadmin", "mypassword123")
	require.NoError(t, err)
	assert.Equal(t, "authadmin", user.Username)
	assert.Equal(t, authmodel.RoleAdmin, user.Role)

	_, err = result.service.Authenticate(t.Context(), "authadmin", "wrongpassword")
	require.Error(t, err)
}

func TestResolveTokenSecret(t *testing.T) {
	t.Parallel()

	t.Run("configured secret takes precedence", func(t *testing.T) {
		t.Parallel()
		cfg := storesTestConfig(t.TempDir(), config.InitialAdmin{})
		ctx := t.Context()
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
		ctx := t.Context()
		cfg.Server.Auth.Builtin.Token.Secret = ""

		first, err := resolveTokenSecret(ctx, cfg)
		require.NoError(t, err)
		second, err := resolveTokenSecret(ctx, cfg)
		require.NoError(t, err)
		assert.Equal(t, first.SigningKey(), second.SigningKey())
	})
}

func TestNewStoresFailsWhenEventStorageIsUnavailable(t *testing.T) {
	t.Parallel()

	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("not a directory"), 0o600))
	cfg := &config.Config{
		EventStore: config.EventStoreConfig{Enabled: true},
		Paths:      config.PathsConfig{EventStoreDir: filepath.Join(blocker, "events")},
	}

	_, err := newStores(t, cfg)
	require.ErrorContains(t, err, "failed to initialize event store")
}

func TestNewStoresProvidesWorkspaceBaseConfig(t *testing.T) {
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

	stores, err := newStores(t, cfg)
	require.NoError(t, err)
	require.NotNil(t, stores.WorkspaceBaseConfig)

	workspaceStore, err := stores.WorkspaceBaseConfig("operations")
	require.NoError(t, err)
	require.NoError(t, workspaceStore.UpdateSpec(t.Context(), []byte("max_active_runs: 2\n")))
	spec, err := workspaceStore.GetSpec(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "max_active_runs: 2\n", spec)
}

// A server whose secret store failed to start cannot mask the stored secret
// values, so its audit entries must not carry the masked marker.
func TestNewStoresMarksNoAuditEntryWithoutSecretStore(t *testing.T) {
	// An empty key falls through to the key file, which the blocker hides.
	t.Setenv("DAGU_ENCRYPTION_KEY", "")
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "auth"), []byte("not a directory"), 0o600))
	cfg := &config.Config{
		Paths: config.PathsConfig{
			DAGsDir:        filepath.Join(root, "dags"),
			WikiDir:        filepath.Join(root, "wiki"),
			DataDir:        dataDir,
			AdminLogsDir:   filepath.Join(root, "admin"),
			RemoteNodesDir: filepath.Join(root, "remote-nodes"),
			WorkspacesDir:  filepath.Join(root, "workspaces"),
			ViewsDir:       filepath.Join(root, "views"),
		},
		Server: config.Server{
			Auth:  config.Auth{Mode: config.AuthModeNone},
			Audit: config.AuditConfig{Enabled: true},
		},
	}
	stores, err := newStores(t, cfg)
	require.NoError(t, err)
	require.Nil(t, stores.Secret, "the secret store must fail to start")

	require.NoError(t, stores.Audit.Append(t.Context(), audit.NewEntry(audit.CategoryTerminal, "command", "u", "alice")))

	result, err := stores.Audit.Query(t.Context(), audit.QueryFilter{})
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	assert.False(t, result.Entries[0].Masked)
}

func TestNewWikiStoreCreatesWikiDirectory(t *testing.T) {
	t.Parallel()

	wikiDir := filepath.Join(t.TempDir(), "nested", "wiki")
	wikiStore, err := newWikiStore(&config.Config{Paths: config.PathsConfig{WikiDir: wikiDir}})

	require.NoError(t, err)
	assert.NotNil(t, wikiStore)
	info, statErr := os.Stat(wikiDir)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())
}

func TestNewWikiStoreReturnsDirectoryCreationError(t *testing.T) {
	t.Parallel()

	blockingFile := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blockingFile, []byte("content"), 0o600))
	wikiStore, err := newWikiStore(&config.Config{
		Paths: config.PathsConfig{WikiDir: filepath.Join(blockingFile, "wiki")},
	})

	require.Error(t, err)
	assert.Nil(t, wikiStore)
}

func TestNewWikiStoreDataLayoutCompatibility(t *testing.T) {
	t.Run("fresh store uses Wiki data directory", func(t *testing.T) {
		dataDir := t.TempDir()
		wikiStore, err := newWikiStore(&config.Config{Paths: config.PathsConfig{
			WikiDir: filepath.Join(t.TempDir(), "wiki"),
			DataDir: dataDir,
		}})
		require.NoError(t, err)
		require.NoError(t, wikiStore.Create(t.Context(), "runbook", "first"))
		require.NoError(t, wikiStore.Update(t.Context(), "runbook", "second"))

		_, err = os.Stat(filepath.Join(dataDir, "wiki", "revisions.json"))
		require.NoError(t, err)
		_, err = os.Stat(filepath.Join(dataDir, "docs"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("existing legacy data directory stays in place", func(t *testing.T) {
		dataDir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dataDir, "docs"), 0o750))
		wikiStore, err := newWikiStore(&config.Config{Paths: config.PathsConfig{
			WikiDir: filepath.Join(t.TempDir(), "wiki"),
			DataDir: dataDir,
		}})
		require.NoError(t, err)
		require.NoError(t, wikiStore.Create(t.Context(), "runbook", "first"))
		require.NoError(t, wikiStore.Update(t.Context(), "runbook", "second"))

		_, err = os.Stat(filepath.Join(dataDir, "docs", "revisions.json"))
		require.NoError(t, err)
		_, err = os.Stat(filepath.Join(dataDir, "wiki"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("ambiguous data directories are rejected", func(t *testing.T) {
		dataDir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dataDir, "wiki"), 0o750))
		require.NoError(t, os.Mkdir(filepath.Join(dataDir, "docs"), 0o750))

		wikiStore, err := newWikiStore(&config.Config{Paths: config.PathsConfig{
			WikiDir: filepath.Join(t.TempDir(), "wiki"),
			DataDir: dataDir,
		}})
		require.Error(t, err)
		assert.Nil(t, wikiStore)
		assert.Contains(t, err.Error(), "both")
	})
}

// TestStoresBumpDAGChangeMarker verifies that editing the global or a workspace
// base configuration signals DAG changes to schedulers on other hosts.
func TestStoresBumpDAGChangeMarker(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		update func(t *testing.T, stores frontend.Stores)
	}{
		{
			name: "GlobalBaseConfig",
			update: func(t *testing.T, stores frontend.Stores) {
				require.NotNil(t, stores.BaseConfig)
				require.NoError(t, stores.BaseConfig.UpdateSpec(t.Context(), []byte("env:\n  - A=1\n")))
			},
		},
		{
			name: "WorkspaceBaseConfig",
			update: func(t *testing.T, stores frontend.Stores) {
				store, err := stores.WorkspaceBaseConfig("team")
				require.NoError(t, err)
				require.NoError(t, store.UpdateSpec(t.Context(), []byte("env:\n  - A=1\n")))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmpDir := t.TempDir()
			cfg := storesTestConfig(tmpDir, config.InitialAdmin{})
			cfg.Paths.DAGsDir = filepath.Join(tmpDir, "dags")
			cfg.Paths.BaseConfig = filepath.Join(tmpDir, "base.yaml")
			require.NoError(t, os.MkdirAll(filepath.Join(cfg.Paths.DAGsDir, "workspaces", "team"), 0o750))

			stores, err := newStores(t, cfg)
			require.NoError(t, err)

			markerPath := defaultSettings(t, cfg, persisfile.NewServerStores(cfg)).DAGChangeMarkerPath()
			require.NotEmpty(t, markerPath)
			require.NoFileExists(t, markerPath)
			tc.update(t, stores)
			assert.FileExists(t, markerPath)
		})
	}
}

// newStores opens the stores of a server that serves the default project.
func newStores(t *testing.T, cfg *config.Config) (frontend.Stores, error) {
	t.Helper()
	server := persisfile.NewServerStores(cfg)
	return NewStores(t.Context(), cfg, server, defaultSettings(t, cfg, server))
}

func defaultSettings(t *testing.T, cfg *config.Config, server *persisfile.ServerStores) *persisfile.ProjectSettings {
	t.Helper()
	settings, err := persisfile.NewProjectSettings(cfg, server, project.DefaultName)
	require.NoError(t, err)
	return settings
}
