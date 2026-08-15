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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/incident"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
)

func TestNewIncidentStorePreservesFileLayout(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "incidents")
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	s, err := persisfile.NewIncidentStore(dir, persisfile.NewCollection(dir, persisfile.WithIndentedJSON()), enc)
	require.NoError(t, err)

	for _, subdir := range []string{"providers", "policies/workspaces", "policies/dags", "states"} {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(subdir)))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		assert.Equal(t, os.FileMode(0o750), info.Mode().Perm())
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
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	loaded, err := s.GetProvider(context.Background(), provider.ID)
	require.NoError(t, err)
	assert.Equal(t, provider.PagerDuty.RoutingKey, loaded.PagerDuty.RoutingKey)
}

func TestNewNotificationStoreReadsExistingFileLayout(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "notifications")
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	s, err := persisfile.NewNotificationStore(dir, persisfile.NewCollection(dir, persisfile.WithIndentedJSON()), enc)
	require.NoError(t, err)

	for _, subdir := range []string{"dags", "channels", "routes/workspaces"} {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(subdir)))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		assert.Equal(t, os.FileMode(0o750), info.Mode().Perm())
	}

	path := filepath.Join(dir, "dags", hashedFileName("daily-report"))
	existing := []byte(`{
  "id": "settings-1",
  "dagName": "daily-report",
  "enabled": true,
  "events": ["dag-run.failed"],
  "targets": [],
  "createdAt": "2026-08-15T00:00:00Z",
  "updatedAt": "2026-08-15T00:00:00Z"
}`)
	require.NoError(t, os.WriteFile(path, existing, 0o600))

	settings, err := s.GetByDAGName(context.Background(), "daily-report")
	require.NoError(t, err)
	assert.Equal(t, "settings-1", settings.ID)
	assert.Equal(t, "daily-report", settings.DAGName)
	assert.True(t, settings.Enabled)
}

func TestCollectionStoresPreserveCorruptFileHandling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)

	notificationDir := filepath.Join(t.TempDir(), "notifications")
	notificationStore, err := persisfile.NewNotificationStore(
		notificationDir,
		persisfile.NewCollection(notificationDir, persisfile.WithIndentedJSON()),
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

	incidentDir := filepath.Join(t.TempDir(), "incidents")
	incidentStore, err := persisfile.NewIncidentStore(
		incidentDir,
		persisfile.NewCollection(incidentDir, persisfile.WithIndentedJSON()),
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
