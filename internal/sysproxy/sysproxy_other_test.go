// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !windows

package sysproxy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubEnv makes env the whole environment.
func stubEnv(t *testing.T, env map[string]string) {
	t.Helper()
	original := getenv
	getenv = func(name string) string { return env[name] }
	t.Cleanup(func() { getenv = original })
}

func TestDetectEnvironment(t *testing.T) {
	ctx := context.Background()

	t.Run("Proxies", func(t *testing.T) {
		stubEnv(t, map[string]string{
			"https_proxy":         "secure.example:3129",
			"NO_PROXY":            ".corp.example",
			"XDG_CURRENT_DESKTOP": "GNOME",
		})
		script, warnings, err := Detect(ctx, "")
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assertRoutes(t, script, []routeCase{
			{"https://www.example.com/", "PROXY secure.example:3129"},
			{"https://wiki.corp.example/", "DIRECT"},
			{"http://www.example.com/", "DIRECT"},
		})
	})

	t.Run("DesktopWithoutVariables", func(t *testing.T) {
		stubEnv(t, map[string]string{"XDG_CURRENT_DESKTOP": "GNOME"})
		script, warnings, err := Detect(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, directPAC, script)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "desktop's proxy settings are not read")
	})

	t.Run("NoDesktop", func(t *testing.T) {
		stubEnv(t, map[string]string{})
		script, warnings, err := Detect(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, directPAC, script)
		assert.Empty(t, warnings)
	})
}
