// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIEProxyConfigRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		config        ieProxyConfig
		routes        []routeCase
		warningsAbout []string
	}{
		{
			name:   "SingleProxy",
			config: ieProxyConfig{proxy: "proxy.corp.example:8080"},
			routes: []routeCase{
				{"http://www.example.com/", "PROXY proxy.corp.example:8080"},
				{"https://www.example.com/", "PROXY proxy.corp.example:8080"},
				{"http://localhost:8080/", "DIRECT"},
			},
		},
		{
			name:   "PerScheme",
			config: ieProxyConfig{proxy: "http=web.corp.example:80;https=secure.corp.example:8443;ftp=files.corp.example:21;socks=socks.corp.example:1080"},
			routes: []routeCase{
				{"http://www.example.com/", "PROXY web.corp.example:80"},
				{"https://www.example.com/", "PROXY secure.corp.example:8443"},
				{"ftp://files.example.com/", "SOCKS socks.corp.example:1080"},
			},
		},
		{
			name:   "SchemePrefixes",
			config: ieProxyConfig{proxy: "https=https://secure.corp.example socks=socks5://socks.corp.example"},
			routes: []routeCase{
				{"https://www.example.com/", "HTTPS secure.corp.example:443"},
				{"http://www.example.com/", "SOCKS5 socks.corp.example:1080"},
			},
		},
		{
			name:   "List",
			config: ieProxyConfig{proxy: "first.corp.example:8080 second.corp.example:8080"},
			routes: []routeCase{
				{"https://www.example.com/", "PROXY first.corp.example:8080; PROXY second.corp.example:8080"},
			},
		},
		{
			name: "Bypass",
			config: ieProxyConfig{
				proxy:       "proxy.corp.example:8080",
				proxyBypass: "<local>;*.corp.example; 10.*;exact.example;http://scheme.example",
			},
			routes: []routeCase{
				{"http://intranet/", "DIRECT"},
				{"https://wiki.corp.example/", "DIRECT"},
				{"http://10.20.30.40/", "DIRECT"},
				{"https://exact.example/", "DIRECT"},
				{"https://www.exact.example/", "PROXY proxy.corp.example:8080"},
				{"https://www.example.com/", "PROXY proxy.corp.example:8080"},
			},
			warningsAbout: []string{`"http://scheme.example"`},
		},
		{
			name:   "InvalidEntry",
			config: ieProxyConfig{proxy: "http=bad!host:80;https=secure.corp.example:8443"},
			routes: []routeCase{
				{"http://www.example.com/", "DIRECT"},
				{"https://www.example.com/", "PROXY secure.corp.example:8443"},
			},
			warningsAbout: []string{`"http=bad!host:80"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, warnings := tt.config.systemConfig()
			assertRoutes(t, cfg.static.pac(), tt.routes)
			require.Len(t, warnings, len(tt.warningsAbout), warnings)
			for i, about := range tt.warningsAbout {
				assert.Contains(t, warnings[i], about)
			}
		})
	}
}

func TestIEProxyConfigAutoConfig(t *testing.T) {
	t.Parallel()
	cfg, warnings := ieProxyConfig{
		autoDetect:    true,
		autoConfigURL: " http://pac.corp.example/proxy.pac ",
		proxy:         "proxy.corp.example:8080",
	}.systemConfig()
	assert.Empty(t, warnings)
	assert.Equal(t, "http://pac.corp.example/proxy.pac", cfg.autoConfigURL)
	assert.True(t, cfg.autoDetect)
	assertRoutes(t, cfg.static.pac(), []routeCase{
		{"https://www.example.com/", "PROXY proxy.corp.example:8080"},
	})
}
