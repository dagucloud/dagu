// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controlplanestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func TestNewFileControlPlaneStoreReturnsAggregate(t *testing.T) {
	ctx := context.Background()
	cfg := testFileConfig(t)

	store, err := New(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, store)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	assert.NotNil(t, store.DAGRuns())
	assert.NotNil(t, store.Queue())
	assert.NotNil(t, store.Services())
	assert.NotNil(t, store.DispatchTasks())
	assert.NotNil(t, store.WorkerHeartbeats())
	assert.NotNil(t, store.DAGRunLeases())
	assert.NotNil(t, store.ActiveDistributedRuns())
	assert.NotNil(t, store.Audit())
	assert.NotNil(t, store.Users())
	assert.NotNil(t, store.APIKeys())
	assert.NotNil(t, store.Webhooks())
	assert.NotNil(t, store.Workspaces())
	assert.NotNil(t, store.Sessions())
	assert.NotNil(t, store.Events())

	dag := &core.DAG{Name: "example"}
	attempt, err := store.DAGRuns().CreateAttempt(ctx, dag, time.Now().UTC(), "run-1", exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, attempt.ID())
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Close(ctx))
}

func TestNewDAGRunStoreFileBackendDoesNotOpenWebhookStore(t *testing.T) {
	ctx := context.Background()
	cfg := testFileConfig(t)
	require.NoError(t, os.MkdirAll(cfg.Paths.WebhooksDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(cfg.Paths.WebhooksDir, "webhook-1.json"),
		[]byte(`{"id":"webhook-1","dagName":"example","tokenHash":"hash","hmacSecretEnc":"ciphertext-placeholder"}`),
		0o600,
	))

	store, err := NewDAGRunStore(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, store)
}

func testFileConfig(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	return &config.Config{
		Paths: config.PathsConfig{
			DataDir:            dataDir,
			DAGRunsDir:         filepath.Join(base, "dag-runs"),
			DAGsDir:            filepath.Join(base, "dags"),
			ArtifactDir:        filepath.Join(base, "artifacts"),
			QueueDir:           filepath.Join(base, "queue"),
			ServiceRegistryDir: filepath.Join(base, "service-registry"),
			AdminLogsDir:       filepath.Join(base, "admin-logs"),
			EventStoreDir:      filepath.Join(base, "events"),
			UsersDir:           filepath.Join(dataDir, "users"),
			APIKeysDir:         filepath.Join(dataDir, "api-keys"),
			WebhooksDir:        filepath.Join(dataDir, "webhooks"),
			SessionsDir:        filepath.Join(dataDir, "agent", "sessions"),
			WorkspacesDir:      filepath.Join(dataDir, "workspaces"),
		},
		ControlPlaneStore: config.ControlPlaneStoreConfig{
			Backend: config.ControlPlaneStoreBackendFile,
		},
	}
}
