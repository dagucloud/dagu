// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http/httpproxy"
)

func TestReadEnvProxyConfig(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"HTTP_PROXY":  "upper.example:3128",
		"http_proxy":  "lower.example:3128",
		"HTTPS_PROXY": "secure.example:3129",
		"ALL_PROXY":   "socks5://socks.example",
		"no_proxy":    "corp.example",
	}
	cfg := readEnvProxyConfig(func(name string) string { return env[name] })
	assert.Equal(t, envProxyConfig{
		httpProxy:  "lower.example:3128",
		httpsProxy: "secure.example:3129",
		allProxy:   "socks5://socks.example",
		noProxy:    "corp.example",
	}, cfg)
	assert.True(t, cfg.hasProxy())
	assert.False(t, readEnvProxyConfig(func(string) string { return "" }).hasProxy())
}

func TestEnvProxyConfigRoutes(t *testing.T) {
	t.Parallel()
	script, warnings := pacFromEnv(envProxyConfig{
		httpProxy:  "http://web.example:3128",
		httpsProxy: "secure.example:3129",
		allProxy:   "socks5h://socks.example",
	})
	assert.Empty(t, warnings)
	assertRoutes(t, script, []routeCase{
		{"http://www.example.com/", "PROXY web.example:3128"},
		{"https://www.example.com/", "PROXY secure.example:3129"},
		{"ftp://files.example.com/", "SOCKS5 socks.example:1080"},
		{"http://localhost:8080/", "DIRECT"},
	})

	script, warnings = pacFromEnv(envProxyConfig{allProxy: "socks4://socks.example:1081"})
	assert.Empty(t, warnings)
	assertRoutes(t, script, []routeCase{{"https://www.example.com/", "SOCKS socks.example:1081"}})
}

func TestEnvProxyConfigWarnings(t *testing.T) {
	t.Parallel()
	script, warnings := pacFromEnv(envProxyConfig{
		httpProxy:  "http://user:secret@web.example:3128",
		httpsProxy: "ftp://secure.example",
	})
	require.Len(t, warnings, 2)
	assert.Contains(t, warnings[0], "HTTP_PROXY includes a user name or password")
	assert.Contains(t, warnings[1], "ignored HTTPS_PROXY")
	for _, warning := range warnings {
		assert.NotContains(t, warning, "secret")
	}
	assertRoutes(t, script, []routeCase{
		{"http://www.example.com/", "PROXY web.example:3128"},
		{"https://www.example.com/", "DIRECT"},
	})
}

func TestEnvNoProxy(t *testing.T) {
	t.Parallel()
	const proxied = "PROXY proxy.example:3128"
	tests := []struct {
		noProxy       string
		routes        []routeCase
		warningsAbout []string
	}{
		{
			noProxy: "corp.example",
			routes: []routeCase{
				{"https://corp.example/", "DIRECT"},
				{"https://wiki.corp.example/", "DIRECT"},
				{"https://notcorp.example/", proxied},
			},
		},
		{
			noProxy: ".corp.example, *.dev.example",
			routes: []routeCase{
				{"https://corp.example/", proxied},
				{"https://wiki.corp.example/", "DIRECT"},
				{"https://dev.example/", proxied},
				{"https://app.dev.example/", "DIRECT"},
			},
		},
		{
			noProxy: "10.0.0.0/8,192.168.1.5,::1,Intranet.Example",
			routes: []routeCase{
				{"http://10.20.30.40/", "DIRECT"},
				{"http://11.0.0.1/", proxied},
				{"http://192.168.1.5/", "DIRECT"},
				{"http://192.168.1.6/", proxied},
				{"https://intranet.example/", "DIRECT"},
			},
		},
		{
			noProxy: "10.0.0.0/8",
			routes: []routeCase{
				// The range matches addresses in URLs, not names resolving into it.
				{"http://intranet.example/", proxied},
			},
		},
		{
			noProxy: "*",
			routes: []routeCase{
				{"https://www.example.com/", "DIRECT"},
			},
		},
		{
			noProxy: "corp.example:8443, fd00::/8, bad!host",
			routes: []routeCase{
				{"https://corp.example/", "DIRECT"},
				{"https://www.example.com/", proxied},
			},
			warningsAbout: []string{`"corp.example:8443"`, `"fd00::/8"`, `"bad!host"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.noProxy, func(t *testing.T) {
			t.Parallel()
			env := envProxyConfig{httpProxy: "proxy.example:3128", httpsProxy: "proxy.example:3128", noProxy: tt.noProxy}
			script, warnings := pacFromEnv(env)
			assertRoutes(t, script, tt.routes)
			require.Len(t, warnings, len(tt.warningsAbout), warnings)
			for i, about := range tt.warningsAbout {
				assert.Contains(t, warnings[i], about)
			}
		})
	}
}

// TestEnvMatchesGoHTTPClient checks the generated script against how Go's
// HTTP client reads the same variables.
func TestEnvMatchesGoHTTPClient(t *testing.T) {
	t.Parallel()
	goConfig := httpproxy.Config{
		HTTPProxy:  "web.example:3128",
		HTTPSProxy: "secure.example:3129",
		NoProxy:    "corp.example,.dev.example,*.lab.example,10.0.0.0/8,192.168.1.5",
	}
	script, warnings := pacFromEnv(envProxyConfig{
		httpProxy:  goConfig.HTTPProxy,
		httpsProxy: goConfig.HTTPSProxy,
		noProxy:    goConfig.NoProxy,
	})
	require.Empty(t, warnings)
	proxyFor := goConfig.ProxyFunc()
	for _, rawURL := range []string{
		"http://www.example.com/", "https://www.example.com/", "https://corp.example/", "https://a.corp.example/",
		"https://dev.example/", "https://a.dev.example/", "https://lab.example/", "https://a.lab.example/",
		"http://10.1.2.3/", "http://11.1.2.3/", "http://192.168.1.5/", "http://localhost/", "http://127.0.0.1/",
	} {
		u, err := url.Parse(rawURL)
		require.NoError(t, err)
		proxyURL, err := proxyFor(u)
		require.NoError(t, err)
		want := "DIRECT"
		if proxyURL != nil {
			want = "PROXY " + proxyURL.Host
		}
		assert.Equal(t, want, evalPAC(t, script, rawURL), rawURL)
	}
}

func pacFromEnv(env envProxyConfig) (string, []string) {
	cfg, warnings := env.staticConfig()
	return cfg.pac(), warnings
}
