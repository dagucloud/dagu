// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDiscoverySource_NeedsHeartbeat verifies the NeedsHeartbeat method for all
// DiscoverySource values. This is a pure logic test with no I/O, so it runs in parallel.
func TestDiscoverySource_NeedsHeartbeat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source DiscoverySource
		want   bool
	}{
		{
			name:   "SourceNone does not need heartbeat",
			source: SourceNone,
			want:   false,
		},
		{
			name:   "SourceEnvInline does not need heartbeat",
			source: SourceEnvInline,
			want:   false,
		},
		{
			name:   "SourceEnvKey needs heartbeat",
			source: SourceEnvKey,
			want:   true,
		},
		{
			name:   "SourceConfigKey needs heartbeat",
			source: SourceConfigKey,
			want:   true,
		},
		{
			name:   "SourceActivationFile needs heartbeat",
			source: SourceActivationFile,
			want:   true,
		},
		{
			name:   "SourceFileJWT does not need heartbeat",
			source: SourceFileJWT,
			want:   false,
		},
		{
			name:   "unknown source does not need heartbeat",
			source: DiscoverySource(99),
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.source.NeedsHeartbeat()
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDiscover_EnvInline covers DAGU_LICENSE environment variable discovery.
// Tests using t.Setenv cannot use t.Parallel in subtests.
func TestDiscover_EnvInline(t *testing.T) {
	t.Run("DAGU_LICENSE env returns SourceEnvInline with token", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "some-jwt-token")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		result, err := Discover("", "", nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceEnvInline, result.Source)
		assert.Equal(t, "some-jwt-token", result.Token)
		assert.Empty(t, result.LicenseKey)
		assert.Nil(t, result.Activation)
	})
}

// TestDiscover_EnvKey covers DAGU_LICENSE_KEY environment variable discovery.
func TestDiscover_EnvKey(t *testing.T) {
	t.Run("DAGU_LICENSE_KEY env returns SourceEnvKey with key", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "my-license-key-abc")
		t.Setenv("DAGU_LICENSE_FILE", "")

		result, err := Discover("", "", nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceEnvKey, result.Source)
		assert.Equal(t, "my-license-key-abc", result.LicenseKey)
		assert.Empty(t, result.Token)
		assert.Nil(t, result.Activation)
	})
}

// TestDiscover_ConfigKey covers the configKey parameter path.
func TestDiscover_ConfigKey(t *testing.T) {
	t.Run("configKey param returns SourceConfigKey with key", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		result, err := Discover("", "config-license-key-xyz", nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceConfigKey, result.Source)
		assert.Equal(t, "config-license-key-xyz", result.LicenseKey)
		assert.Empty(t, result.Token)
		assert.Nil(t, result.Activation)
	})
}

// TestDiscover_ActivationStore covers all activation store scenarios.
func TestDiscover_ActivationStore(t *testing.T) {
	t.Run("valid store data with non-empty token returns SourceActivationFile", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		ad := &ActivationData{
			Token:           "activation-jwt-token",
			HeartbeatSecret: "secret-abc",
			LicenseKey:      "lic-key-123",
			ServerID:        "server-001",
		}
		store := &mockActivationStore{data: ad}

		result, err := Discover("", "", store)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceActivationFile, result.Source)
		assert.Equal(t, "activation-jwt-token", result.Token)
		require.NotNil(t, result.Activation)
		assert.Equal(t, ad.HeartbeatSecret, result.Activation.HeartbeatSecret)
		assert.Equal(t, ad.LicenseKey, result.Activation.LicenseKey)
		assert.Equal(t, ad.ServerID, result.Activation.ServerID)
	})

	t.Run("activation data with empty token is skipped and falls through", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		ad := &ActivationData{
			Token:      "",
			LicenseKey: "some-key",
		}
		store := &mockActivationStore{data: ad}

		result, err := Discover("", "", store)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceNone, result.Source)
	})

	t.Run("nil data from store is skipped and falls through", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		store := &mockActivationStore{data: nil}

		result, err := Discover("", "", store)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceNone, result.Source)
	})

	t.Run("store Load error is propagated as error", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		loadErr := errors.New("disk read failure")
		store := &mockActivationStore{loadErr: loadErr}

		result, err := Discover("", "", store)

		assert.Error(t, err)
		assert.Equal(t, loadErr, err)
		assert.Nil(t, result)
	})

	t.Run("nil store is skipped and falls through", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		result, err := Discover("", "", nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceNone, result.Source)
	})
}

// TestDiscover_FileJWT covers all file-based JWT discovery paths.
func TestDiscover_FileJWT(t *testing.T) {
	t.Run("DAGU_LICENSE_FILE env points to valid file returns SourceFileJWT", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")

		dir := t.TempDir()
		jwtPath := filepath.Join(dir, "my-license.jwt")
		require.NoError(t, os.WriteFile(jwtPath, []byte("file-jwt-token-from-env"), 0600))

		t.Setenv("DAGU_LICENSE_FILE", jwtPath)

		result, err := Discover("", "", nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceFileJWT, result.Source)
		assert.Equal(t, "file-jwt-token-from-env", result.Token)
		assert.Empty(t, result.LicenseKey)
		assert.Nil(t, result.Activation)
	})

	t.Run("default path licenseDir/license.jwt returns SourceFileJWT", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		dir := t.TempDir()
		jwtPath := filepath.Join(dir, "license.jwt")
		require.NoError(t, os.WriteFile(jwtPath, []byte("default-path-jwt-token"), 0600))

		result, err := Discover(dir, "", nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceFileJWT, result.Source)
		assert.Equal(t, "default-path-jwt-token", result.Token)
	})

	t.Run("empty file content is skipped and falls through", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		dir := t.TempDir()
		jwtPath := filepath.Join(dir, "license.jwt")
		require.NoError(t, os.WriteFile(jwtPath, []byte(""), 0600))

		result, err := Discover(dir, "", nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceNone, result.Source)
	})

	t.Run("missing file is skipped and falls through", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		dir := t.TempDir()
		// license.jwt is intentionally absent in this temp directory.

		result, err := Discover(dir, "", nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceNone, result.Source)
	})

	t.Run("explicit missing file returns an error", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", filepath.Join(t.TempDir(), "missing.jwt"))

		result, err := Discover(t.TempDir(), "", nil)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to read license file")
	})

	t.Run("explicit empty file returns an error", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")

		jwtPath := filepath.Join(t.TempDir(), "empty.jwt")
		require.NoError(t, os.WriteFile(jwtPath, nil, 0600))
		t.Setenv("DAGU_LICENSE_FILE", jwtPath)

		result, err := Discover(t.TempDir(), "", nil)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "is empty")
	})
}

// TestDiscover_None verifies SourceNone is returned when no source is configured.
func TestDiscover_None(t *testing.T) {
	t.Run("no sources configured returns SourceNone", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		result, err := Discover("", "", nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceNone, result.Source)
		assert.Empty(t, result.Token)
		assert.Empty(t, result.LicenseKey)
		assert.Nil(t, result.Activation)
	})
}

// TestDiscover_Precedence verifies the documented priority order when multiple
// sources are configured simultaneously.
func TestDiscover_Precedence(t *testing.T) {
	t.Run("all sources set — env inline wins over all others", func(t *testing.T) {
		dir := t.TempDir()
		jwtPath := filepath.Join(dir, "license.jwt")
		require.NoError(t, os.WriteFile(jwtPath, []byte("file-jwt"), 0600))

		t.Setenv("DAGU_LICENSE", "inline-jwt-token")
		t.Setenv("DAGU_LICENSE_KEY", "env-license-key")
		t.Setenv("DAGU_LICENSE_FILE", jwtPath)

		ad := &ActivationData{Token: "activation-token"}
		store := &mockActivationStore{data: ad}

		result, err := Discover(dir, "config-key", store)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceEnvInline, result.Source)
		assert.Equal(t, "inline-jwt-token", result.Token)
	})

	t.Run("env key wins over config key when DAGU_LICENSE not set", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "env-key-wins")
		t.Setenv("DAGU_LICENSE_FILE", "")

		result, err := Discover("", "config-key-loses", nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceEnvKey, result.Source)
		assert.Equal(t, "env-key-wins", result.LicenseKey)
	})

	t.Run("config key wins over activation store when env vars not set", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		ad := &ActivationData{Token: "activation-token"}
		store := &mockActivationStore{data: ad}

		result, err := Discover("", "config-key-wins", store)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceConfigKey, result.Source)
		assert.Equal(t, "config-key-wins", result.LicenseKey)
	})

	t.Run("activation store wins over file JWT when env vars and config not set", func(t *testing.T) {
		dir := t.TempDir()
		jwtPath := filepath.Join(dir, "license.jwt")
		require.NoError(t, os.WriteFile(jwtPath, []byte("file-jwt-loses"), 0600))

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		ad := &ActivationData{Token: "activation-wins"}
		store := &mockActivationStore{data: ad}

		result, err := Discover(dir, "", store)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, SourceActivationFile, result.Source)
		assert.Equal(t, "activation-wins", result.Token)
	})
}
