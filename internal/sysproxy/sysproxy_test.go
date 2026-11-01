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

func TestDetectExplicit(t *testing.T) {
	t.Parallel()
	script, warnings, err := Detect(context.Background(), " https://proxy.example:8443 ")
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assertRoutes(t, script, []routeCase{
		{"https://www.example.com/", "HTTPS proxy.example:8443"},
		{"http://localhost:8080/", "DIRECT"},
	})

	_, _, err = Detect(context.Background(), "http=a:80;https=b:443")
	assert.Error(t, err)
}

func TestSystemConfigPrecedence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	static := staticConfig{fallback: []proxyServer{{kind: httpProxy, host: "static.example", port: "8080"}}}
	const staticRoute = "PROXY static.example:8080"
	discoveredPAC := `function FindProxyForURL(url, host) { return "PROXY discovered.example:3128"; }`
	discovered := func(t *testing.T) discoverFunc {
		scriptURL := servePAC(t, discoveredPAC)
		return func(context.Context) (string, error) { return scriptURL, nil }
	}

	t.Run("ConfiguredScriptFirst", func(t *testing.T) {
		t.Parallel()
		cfg := systemConfig{autoConfigURL: servePAC(t, testPAC), autoDetect: true, static: static}
		script, warnings := cfg.pac(ctx, discovered(t))
		assert.Empty(t, warnings)
		assert.Equal(t, testPAC, script)
	})

	t.Run("DiscoveredScriptBeforeStatic", func(t *testing.T) {
		t.Parallel()
		cfg := systemConfig{autoDetect: true, static: static}
		script, warnings := cfg.pac(ctx, discovered(t))
		assert.Empty(t, warnings)
		assert.Equal(t, discoveredPAC, script)
	})

	t.Run("DiscoveryFailsToStatic", func(t *testing.T) {
		t.Parallel()
		cfg := systemConfig{autoDetect: true, static: static}
		script, warnings := cfg.pac(ctx, func(context.Context) (string, error) {
			return "", errors.New("the network offers no proxy auto-config")
		})
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "proxy auto-discovery failed: the network offers no proxy auto-config")
		assertRoutes(t, script, []routeCase{{"https://www.example.com/", staticRoute}})
	})

	t.Run("DiscoveredScriptUnreadableToStatic", func(t *testing.T) {
		t.Parallel()
		missing := servePAC(t, testPAC) + ".missing"
		cfg := systemConfig{autoDetect: true, static: static}
		script, warnings := cfg.pac(ctx, func(context.Context) (string, error) { return missing, nil })
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "could not read "+missing)
		assertRoutes(t, script, []routeCase{{"https://www.example.com/", staticRoute}})
	})

	t.Run("Static", func(t *testing.T) {
		t.Parallel()
		script, warnings := systemConfig{static: static}.pac(ctx, nil)
		assert.Empty(t, warnings)
		assertRoutes(t, script, []routeCase{{"https://www.example.com/", staticRoute}})
	})
}
