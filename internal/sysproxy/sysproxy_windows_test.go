// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubWindowsSettings makes the Internet Options read as config and
// auto-discovery find scriptURL, or fail with discoverErr.
func stubWindowsSettings(t *testing.T, config ieProxyConfig, readErr error, scriptURL string, discoverErr error) {
	t.Helper()
	originalRead, originalDetect := readIEProxyConfig, detectAutoConfigURL
	readIEProxyConfig = func() (ieProxyConfig, error) { return config, readErr }
	detectAutoConfigURL = func() (string, error) { return scriptURL, discoverErr }
	t.Cleanup(func() { readIEProxyConfig, detectAutoConfigURL = originalRead, originalDetect })
}

func TestDetectWindows(t *testing.T) {
	ctx := context.Background()

	t.Run("ConfiguredScript", func(t *testing.T) {
		stubWindowsSettings(t, ieProxyConfig{autoConfigURL: servePAC(t, testPAC), proxy: "proxy.example:8080"}, nil, "", nil)
		script, warnings, err := Detect(ctx, "")
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assert.Equal(t, testPAC, script)
	})

	t.Run("AutoDetect", func(t *testing.T) {
		stubWindowsSettings(t, ieProxyConfig{autoDetect: true, proxy: "proxy.example:8080"}, nil, servePAC(t, testPAC), nil)
		script, warnings, err := Detect(ctx, "")
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assert.Equal(t, testPAC, script)
	})

	t.Run("AutoDetectFailsToStatic", func(t *testing.T) {
		stubWindowsSettings(t, ieProxyConfig{autoDetect: true, proxy: "proxy.example:8080", proxyBypass: "<local>"},
			nil, "", errors.New("the network offers no proxy auto-config"))
		script, warnings, err := Detect(ctx, "")
		require.NoError(t, err)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "proxy auto-discovery failed")
		assertRoutes(t, script, []routeCase{
			{"https://www.example.com/", "PROXY proxy.example:8080"},
			{"http://intranet/", "DIRECT"},
		})
	})

	t.Run("SettingsUnreadable", func(t *testing.T) {
		stubWindowsSettings(t, ieProxyConfig{}, errors.New("access denied"), "", nil)
		script, warnings, err := Detect(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, directPAC, script)
		assert.Equal(t, []string{"could not read the Windows proxy settings: access denied; requests go DIRECT"}, warnings)
	})
}
