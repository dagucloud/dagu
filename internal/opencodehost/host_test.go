// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package opencodehost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedEnvironment(t *testing.T) {
	env, err := managedEnvironment([]string{
		"PATH=/bin", "HOME=/home/dagu", "OPENAI_API_KEY=secret", "DATABASE_URL=secret",
		"DAGU_AUTH_BASIC_PASSWORD=secret", "OPENCODE_CONFIG_CONTENT={}", envPassword + "=secret",
	}, []string{"OPENAI_API_KEY"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"PATH=/bin", "HOME=/home/dagu", "OPENAI_API_KEY=secret", "OPENCODE_CONFIG_CONTENT={}",
	}, env)

	_, err = managedEnvironment(nil, []string{"OPENCODE_SERVER_PASSWORD"})
	require.Error(t, err)
}

func TestWithManagedConfigPreservesExistingKeysAndDisablesSharing(t *testing.T) {
	env, err := withManagedConfig([]string{`OPENCODE_CONFIG_CONTENT={"mcp":{"docs":{"type":"local"}},"share":"auto"}`})
	require.NoError(t, err)
	require.Len(t, env, 1)
	_, content, ok := strings.Cut(env[0], "=")
	require.True(t, ok)
	var settings map[string]any
	require.NoError(t, json.Unmarshal([]byte(content), &settings))
	assert.Equal(t, "disabled", settings["share"])
	assert.Contains(t, settings, "mcp")
}

func TestWithManagedConfigRejectsNull(t *testing.T) {
	_, err := withManagedConfig([]string{"OPENCODE_CONFIG_CONTENT=null"})
	require.EqualError(t, err, "OPENCODE_CONFIG_CONTENT must contain a JSON object")
}

func TestEnsureDoesNotReplaceRunningUnhealthyHost(t *testing.T) {
	host := New(context.Background(), config.OpenCodeConfig{})
	host.cmd = &exec.Cmd{}
	host.waitCh = make(chan error, 1)
	host.config = Config{URL: "http://127.0.0.1:1", Username: "opencode", Password: "secret", InstanceID: "instance"}
	host.healthCheck = func(Config) (string, error) { return "", errors.New("probe failed") }

	_, err := host.Ensure(context.Background())
	require.Error(t, err)
	assert.NotNil(t, host.cmd)
	assert.Equal(t, "instance", host.config.InstanceID)
}

func TestCloseIsIdempotent(t *testing.T) {
	host := New(context.Background(), config.OpenCodeConfig{})
	host.cmd = &exec.Cmd{}
	host.waitCh = make(chan error, 1)
	host.waitCh <- context.Canceled

	require.NoError(t, host.Close(t.Context()))
	require.NoError(t, host.Close(t.Context()))
}

func TestSessionAvailableUsesPersistedProviderState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/workspace", r.URL.Query().Get("directory"))
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "session-1", "directory": "/workspace"})
	}))
	t.Cleanup(server.Close)

	available, err := SessionAvailable(t.Context(), Config{
		URL: server.URL, Username: "opencode", Password: "secret", InstanceID: "new-host",
	}, "/workspace", "session-1")

	require.NoError(t, err)
	assert.True(t, available)
}

func TestModeTreatsSharingAsCLIOnly(t *testing.T) {
	mode, err := Mode(map[string]any{"provider": "opencode", "share": true})
	require.NoError(t, err)
	assert.False(t, mode.Managed)
	assert.False(t, mode.Required)
	assert.Contains(t, mode.Reason, "CLI integration")

	mode, err = Mode(map[string]any{"provider": "opencode", "managed": true, "share": true})
	require.Error(t, err)
	assert.True(t, mode.Required)
}

func TestRealOpenCodeCapabilities(t *testing.T) {
	executable := os.Getenv("DAGU_TEST_OPENCODE_EXECUTABLE")
	if executable == "" {
		t.Skip("DAGU_TEST_OPENCODE_EXECUTABLE is not set")
	}
	host := New(context.Background(), config.OpenCodeConfig{Executable: executable})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, host.Close(ctx))
	})

	hostConfig, err := host.Ensure(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, hostConfig.InstanceID)
	if expected := os.Getenv("DAGU_TEST_OPENCODE_VERSION"); expected != "" {
		require.Equal(t, expected, hostConfig.Version)
	}
}
