// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
)

func TestNewIncidentStorePreservesFileLayout(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "incidents")
	backend := persisfile.NewBackend(config.PathsConfig{DataDir: dataDir})
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	s, err := persisfile.NewIncidentStore(
		backend.Collection(persis.CollectionIncidents),
		backend.Collection(persis.CollectionIncidentProviders),
		enc,
	)
	require.NoError(t, err)

	for _, subdir := range []string{"providers", "policies/workspaces", "policies/dags", "states"} {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(subdir)))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		if runtime.GOOS != "windows" {
			assert.Zero(t, info.Mode().Perm()&^os.FileMode(0o750))
		}
	}

	provider, err := incident.NormalizeProvider(&incident.Provider{
		Name:    "PagerDuty",
		Type:    incident.ProviderPagerDuty,
		Enabled: true,
		PagerDuty: &incident.PagerDutyProvider{
			RoutingKey: "pagerduty-routing-key",
		},
	}, "user-1")
	require.NoError(t, err)
	require.NoError(t, s.SaveProvider(context.Background(), provider))

	path := filepath.Join(dir, "providers", hashedFileName(provider.ID))
	data, err := os.ReadFile(path) //nolint:gosec // Test reads a file in its temporary directory.
	require.NoError(t, err)
	assert.False(t, bytes.Contains(data, []byte("pagerduty-routing-key")))
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	loaded, err := s.GetProvider(context.Background(), provider.ID)
	require.NoError(t, err)
	assert.Equal(t, provider.PagerDuty.RoutingKey, loaded.PagerDuty.RoutingKey)
}

// The store reads every record where released versions wrote it in one
// directory: DAG routes, route sets, channels, and notification settings.
func TestNewNotificationStoreReadsExistingFileLayout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "notifications")
	backend := persisfile.NewBackend(config.PathsConfig{DataDir: dataDir})
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	s, err := persisfile.NewNotificationStore(
		backend.Collection(persis.CollectionNotifications),
		backend.Collection(persis.CollectionNotificationChannels),
		enc,
	)
	require.NoError(t, err)

	for _, subdir := range []string{"dags", "channels", "routes/workspaces"} {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(subdir)))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		if runtime.GOOS != "windows" {
			assert.Zero(t, info.Mode().Perm()&^os.FileMode(0o750))
		}
	}

	stamps := `"createdAt": "2026-08-15T00:00:00Z", "updatedAt": "2026-08-15T00:00:00Z"`
	for rel, content := range map[string]string{
		"dags/" + hashedFileName("daily-report"): `{"id": "settings-1", "dagName": "daily-report", "enabled": true,
			"events": ["dag-run.failed"], "targets": [], ` + stamps + `}`,
		"routes/global.json":                `{"id": "routes-1", "scope": "global", "enabled": true, "routes": [], ` + stamps + `}`,
		"channels/" + hashedFileName("ops"): `{"id": "ops", "name": "Ops", "type": "slack", "enabled": true, ` + stamps + `}`,
		"workspace.json":                    `{"smtp": {"host": "smtp.example.com"}, ` + stamps + `}`,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(content), 0o600))
	}

	settings, err := s.GetByDAGName(ctx, "daily-report")
	require.NoError(t, err)
	assert.Equal(t, "settings-1", settings.ID)
	assert.Equal(t, "daily-report", settings.DAGName)
	assert.True(t, settings.Enabled)
	routeSet, err := s.GetRouteSet(ctx, notification.RouteScopeGlobal, "")
	require.NoError(t, err)
	assert.Equal(t, "routes-1", routeSet.ID)
	channel, err := s.GetChannel(ctx, "ops")
	require.NoError(t, err)
	assert.Equal(t, "Ops", channel.Name)
	smtp, err := s.GetWorkspaceSettings(ctx)
	require.NoError(t, err)
	require.NotNil(t, smtp.SMTP)
	assert.Equal(t, "smtp.example.com", smtp.SMTP.Host)
}

func TestCollectionStoresPreserveCorruptFileHandling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)

	dataDir := t.TempDir()
	backend := persisfile.NewBackend(config.PathsConfig{DataDir: dataDir})
	notificationDir := filepath.Join(dataDir, "notifications")
	notificationStore, err := persisfile.NewNotificationStore(
		backend.Collection(persis.CollectionNotifications),
		backend.Collection(persis.CollectionNotificationChannels),
		enc,
	)
	require.NoError(t, err)
	notificationPath := filepath.Join(notificationDir, "dags", hashedFileName("daily-report"))
	require.NoError(t, os.WriteFile(notificationPath, []byte("{"), 0o600))
	settings, err := notificationStore.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, settings)
	_, err = notificationStore.GetByDAGName(ctx, "daily-report")
	assert.Error(t, err)

	incidentDir := filepath.Join(dataDir, "incidents")
	incidentStore, err := persisfile.NewIncidentStore(
		backend.Collection(persis.CollectionIncidents),
		backend.Collection(persis.CollectionIncidentProviders),
		enc,
	)
	require.NoError(t, err)
	providerPath := filepath.Join(incidentDir, "providers", hashedFileName("provider-1"))
	require.NoError(t, os.WriteFile(providerPath, []byte("{"), 0o600))
	providers, err := incidentStore.ListProviders(ctx)
	require.NoError(t, err)
	assert.Empty(t, providers)
	statePath := filepath.Join(incidentDir, "states", hashedFileName("provider-1\x00dedup"))
	require.NoError(t, os.WriteFile(statePath, []byte("{"), 0o600))
	_, err = incidentStore.ListOpenStates(ctx)
	assert.Error(t, err)
}

func hashedFileName(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:]) + ".json"
}
