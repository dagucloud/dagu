// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scutilPACEnabled has a configured script alongside static proxies, and a
// scoped copy of the settings per network interface.
const scutilPACEnabled = `<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254/16
  }
  ExcludeSimpleHostnames : 1
  FTPPassive : 1
  HTTPEnable : 1
  HTTPPort : 8080
  HTTPProxy : proxy.corp.example
  HTTPSEnable : 1
  HTTPSPort : 8080
  HTTPSProxy : proxy.corp.example
  ProxyAutoConfigEnable : 1
  ProxyAutoConfigURLString : http://pac.corp.example/proxy.pac
  ProxyAutoDiscoveryEnable : 0
  SOCKSEnable : 0
  __SCOPED__ : <dictionary> {
    en0 : <dictionary> {
      ExceptionsList : <array> {
        0 : *.local
      }
      FTPPassive : 1
    }
  }
}
`

const scutilStatic = `<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254/16
    2 : build.corp.example
    3 : fe80::/10
  }
  ExcludeSimpleHostnames : 1
  FTPPassive : 1
  HTTPEnable : 1
  HTTPPort : 8080
  HTTPProxy : proxy.corp.example
  HTTPSEnable : 1
  HTTPSPort : 8443
  HTTPSProxy : secure.corp.example
  ProxyAutoConfigEnable : 0
  ProxyAutoDiscoveryEnable : 0
  SOCKSEnable : 0
}
`

const scutilSOCKSOnly = `<dictionary> {
  FTPPassive : 1
  SOCKSEnable : 1
  SOCKSPort : 1080
  SOCKSProxy : socks.corp.example
}
`

// scutilNothingEnabled is what a Mac without a proxy reports.
const scutilNothingEnabled = `<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254/16
  }
  FTPPassive : 1
}
`

const scutilAutoDiscovery = `<dictionary> {
  FTPPassive : 1
  ProxyAutoDiscoveryEnable : 1
}
`

func TestParseScutil(t *testing.T) {
	t.Parallel()
	settings, err := parseScutil([]byte(scutilPACEnabled))
	require.NoError(t, err)
	assert.Equal(t, []any{"*.local", "169.254/16"}, settings["ExceptionsList"])
	assert.Equal(t, "http://pac.corp.example/proxy.pac", settings["ProxyAutoConfigURLString"])
	assert.Equal(t, "8080", settings["HTTPPort"])
	assert.Equal(t, map[string]any{
		"en0": map[string]any{
			"ExceptionsList": []any{"*.local"},
			"FTPPassive":     "1",
		},
	}, settings["__SCOPED__"])

	for _, out := range []string{"", "no proxy settings", "<dictionary> {\n  HTTPEnable : 1\n", "<dictionary> {\n  HTTPEnable\n}\n"} {
		_, err := parseScutil([]byte(out))
		assert.Error(t, err, out)
	}
}

func TestMacSystemConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		out           string
		autoConfigURL string
		autoDetect    bool
		routes        []routeCase
		warningsAbout []string
	}{
		{
			name:          "PACEnabled",
			out:           scutilPACEnabled,
			autoConfigURL: "http://pac.corp.example/proxy.pac",
			routes: []routeCase{
				{"https://www.example.com/", "PROXY proxy.corp.example:8080"},
			},
		},
		{
			name: "StaticWithExceptions",
			out:  scutilStatic,
			routes: []routeCase{
				{"http://www.example.com/", "PROXY proxy.corp.example:8080"},
				{"https://www.example.com/", "PROXY secure.corp.example:8443"},
				{"ftp://files.example.com/", "DIRECT"},
				{"http://printer.local/", "DIRECT"},
				{"http://169.254.1.1/", "DIRECT"},
				{"http://intranet/", "DIRECT"},
				{"https://build.corp.example/", "DIRECT"},
				{"https://other.corp.example/", "PROXY secure.corp.example:8443"},
				{"http://localhost:3000/", "DIRECT"},
			},
			warningsAbout: []string{`"fe80::/10"`},
		},
		{
			name: "SOCKSOnly",
			out:  scutilSOCKSOnly,
			routes: []routeCase{
				{"https://www.example.com/", "SOCKS5 socks.corp.example:1080"},
				{"http://www.example.com/", "SOCKS5 socks.corp.example:1080"},
				{"http://intranet/", "SOCKS5 socks.corp.example:1080"},
			},
		},
		{
			name: "NothingEnabled",
			out:  scutilNothingEnabled,
			routes: []routeCase{
				{"https://www.example.com/", "DIRECT"},
			},
		},
		{
			name:       "AutoDiscoveryOnly",
			out:        scutilAutoDiscovery,
			autoDetect: true,
			routes: []routeCase{
				{"https://www.example.com/", "DIRECT"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings, err := parseScutil([]byte(tt.out))
			require.NoError(t, err)
			cfg, warnings := macSystemConfig(settings)
			assert.Equal(t, tt.autoConfigURL, cfg.autoConfigURL)
			assert.Equal(t, tt.autoDetect, cfg.autoDetect)
			assertRoutes(t, cfg.static.pac(), tt.routes)
			require.Len(t, warnings, len(tt.warningsAbout))
			for i, about := range tt.warningsAbout {
				assert.Contains(t, warnings[i], about)
			}
		})
	}
}

func TestMacSystemConfigWarnsOfInvalidProxy(t *testing.T) {
	t.Parallel()
	settings, err := parseScutil([]byte("<dictionary> {\n  HTTPEnable : 1\n  HTTPProxy : \n  HTTPSEnable : 1\n  HTTPSProxy : secure.example\n  HTTPSPort : 3129\n}\n"))
	require.NoError(t, err)
	cfg, warnings := macSystemConfig(settings)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "macOS HTTP proxy")
	assertRoutes(t, cfg.static.pac(), []routeCase{
		{"http://www.example.com/", "DIRECT"},
		{"https://www.example.com/", "PROXY secure.example:3129"},
	})
}
