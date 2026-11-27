// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadSettings(t *testing.T, yamlContent string) map[string]Setting {
	t.Helper()

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(yamlContent), 0600))

	loader := NewConfigLoader(viper.New(), WithAppHomeDir(t.TempDir()), WithConfigFile(configFile))
	cfg, err := loader.Load()
	require.NoError(t, err)

	settings := make(map[string]Setting)
	for _, s := range loader.Settings(cfg) {
		_, dup := settings[s.Key]
		require.False(t, dup, "key %s is listed twice", s.Key)
		settings[s.Key] = s
	}
	return settings
}

func TestSettingsSource(t *testing.T) {
	t.Setenv("DAGU_PORT", "")
	t.Setenv("DAGU_HOST", "0.0.0.0")
	t.Setenv("DAGU_COORDINATOR_PORT", "")

	settings := loadSettings(t, `
coordinator:
  port: 50060
`)

	assert.Equal(t, Setting{Key: "port", Value: "8080", Source: SourceDefault}, settings["port"])
	assert.Equal(t, Setting{Key: "host", Value: "0.0.0.0", Source: "env:DAGU_HOST"}, settings["host"])
	assert.Equal(t, Setting{Key: "coordinator.port", Value: "50060", Source: SourceFile}, settings["coordinator.port"])
}

func TestSettingsEnvOverridesFile(t *testing.T) {
	t.Setenv("DAGU_PORT", "9000")

	settings := loadSettings(t, "port: 8090\n")

	assert.Equal(t, Setting{Key: "port", Value: "9000", Source: "env:DAGU_PORT"}, settings["port"])
}

// A key bound to several variables reports the one Viper reads: the first
// in the binding table that is set.
func TestSettingsEnvAlias(t *testing.T) {
	t.Run("Legacy", func(t *testing.T) {
		t.Setenv("DAGU_UI_NAVBAR_COLOR", "")
		t.Setenv("DAGU_NAVBAR_COLOR", "red")

		settings := loadSettings(t, "")
		assert.Equal(t, Setting{Key: "ui.navbar_color", Value: "red", Source: "env:DAGU_NAVBAR_COLOR"}, settings["ui.navbar_color"])
	})

	t.Run("CanonicalWins", func(t *testing.T) {
		t.Setenv("DAGU_UI_NAVBAR_COLOR", "blue")
		t.Setenv("DAGU_NAVBAR_COLOR", "red")

		settings := loadSettings(t, "")
		assert.Equal(t, Setting{Key: "ui.navbar_color", Value: "blue", Source: "env:DAGU_UI_NAVBAR_COLOR"}, settings["ui.navbar_color"])
	})
}

// Viper's automatic DAGU_<KEY> lookup runs before the bound variables.
func TestSettingsAutomaticEnv(t *testing.T) {
	t.Setenv("DAGU_SERVER_METRICS", "private")
	t.Setenv("DAGU_METRICS", "public")

	settings := loadSettings(t, "")
	assert.Equal(t, Setting{Key: "metrics", Value: "public", Source: "env:DAGU_METRICS"}, settings["metrics"])
}

// Keys for projects stay out of the report while projects are off, so the
// report reads as it does without them.
func TestSettingsProjectKeys(t *testing.T) {
	t.Run("Off", func(t *testing.T) {
		settings := loadSettings(t, "")
		assert.NotContains(t, settings, "paths.projects_dir")
	})

	t.Run("On", func(t *testing.T) {
		settings := loadSettings(t, "experimental_projects: true\npaths:\n  projects_dir: /srv/projects\n")
		assert.Equal(t, Setting{Key: "paths.projects_dir", Value: "/srv/projects", Source: SourceFile}, settings["paths.projects_dir"])
	})
}

// A deprecated key that Load applies in place of a listed setting is
// reported under that setting, so the report shows the value Dagu uses.
func TestSettingsLegacyKeys(t *testing.T) {
	settings := loadSettings(t, `
dags: /srv/dags
executable: /srv/bin/dagu
log_dir: /srv/logs
artifact_dir: /srv/artifacts
data_dir: /srv/data
suspend_flags_dir: /srv/data/suspend
admin_logs_dir: /srv/admin
event_store_dir: /srv/events
base_config: /srv/base.yaml
api_base_url: /legacy/api
navbar_color: red
navbar_title: Legacy
log_encoding_charset: iso-8859-1
max_dashboard_page_limit: 50
scheduler:
  heartbeat_interval: 10s
  stale_threshold: 60s
paths:
  docs_dir: /srv/docs
`)

	want := map[string]string{
		"paths.dags_dir":              "/srv/dags",
		"paths.executable":            "/srv/bin/dagu",
		"paths.log_dir":               "/srv/logs",
		"paths.artifact_dir":          "/srv/artifacts",
		"paths.data_dir":              "/srv/data",
		"paths.suspend_flags_dir":     "/srv/data/suspend",
		"paths.admin_logs_dir":        "/srv/admin",
		"paths.event_store_dir":       "/srv/events",
		"paths.base_config":           "/srv/base.yaml",
		"paths.wiki_dir":              "/srv/docs",
		"api_base_path":               "/legacy/api",
		"ui.navbar_color":             "red",
		"ui.navbar_title":             "Legacy",
		"ui.log_encoding_charset":     "iso-8859-1",
		"ui.max_dashboard_page_limit": "50",
		"proc.heartbeat_interval":     "10s",
		"proc.stale_threshold":        "60s",
	}
	for key, value := range want {
		assert.Equal(t, Setting{Key: key, Value: value, Source: SourceFile}, settings[key])
	}
}

// When a deprecated key and its replacement are both set, the report shows
// the one Load applies: dags_dir over dags, a flat path key over its paths.*
// key, and a canonical key over a deprecated alias that only fills it in.
func TestSettingsLegacyKeyPrecedence(t *testing.T) {
	settings := loadSettings(t, `
dags: /srv/old-dags
dags_dir: /srv/dags
data_dir: /srv/legacy-data
paths:
  data_dir: /srv/data
  wiki_dir: /srv/wiki
  docs_dir: /srv/docs
proc:
  heartbeat_interval: 5s
scheduler:
  heartbeat_interval: 10s
`)

	assert.Equal(t, Setting{Key: "paths.dags_dir", Value: "/srv/dags", Source: SourceFile}, settings["paths.dags_dir"])
	assert.Equal(t, Setting{Key: "paths.data_dir", Value: "/srv/legacy-data", Source: SourceFile}, settings["paths.data_dir"])
	assert.Equal(t, Setting{Key: "paths.wiki_dir", Value: "/srv/wiki", Source: SourceFile}, settings["paths.wiki_dir"])
	assert.Equal(t, Setting{Key: "proc.heartbeat_interval", Value: "5s", Source: SourceFile}, settings["proc.heartbeat_interval"])
}

// A deprecated DAGU__ variable overrides the config file, and the report
// names it as the source.
func TestSettingsLegacyEnv(t *testing.T) {
	t.Run("Applied", func(t *testing.T) {
		t.Setenv("DAGU__DATA", "/srv/env-data")
		t.Setenv("DAGU__ADMIN_PORT", "9090")

		settings := loadSettings(t, "data_dir: /srv/data\nport: 8090\n")

		assert.Equal(t, Setting{Key: "paths.data_dir", Value: "/srv/env-data", Source: "env:DAGU__DATA"}, settings["paths.data_dir"])
		assert.Equal(t, Setting{Key: "port", Value: "9090", Source: "env:DAGU__ADMIN_PORT"}, settings["port"])
	})

	// Load ignores a port that is not a number, so the file value stays.
	t.Run("Ignored", func(t *testing.T) {
		t.Setenv("DAGU__ADMIN_PORT", "invalid")

		settings := loadSettings(t, "port: 8090\n")

		assert.Equal(t, Setting{Key: "port", Value: "8090", Source: SourceFile}, settings["port"])
	})
}

// admin.yaml still accepts the camelCase signal handling key; the report
// shows it under the snake_case setting.
func TestSettingsLegacyAdminSignalHandling(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "admin.yaml"), []byte("signalHandling:\n  enablePropagation: true\n"), 0600))

	loader := NewConfigLoader(viper.New(), WithAppHomeDir(homeDir))
	cfg, err := loader.Load()
	require.NoError(t, err)

	assert.Contains(t, loader.Settings(cfg), Setting{Key: "signal_handling.enable_propagation", Value: "true", Source: SourceFile})
}

func TestSettingsListsAPIBasePath(t *testing.T) {
	t.Setenv("DAGU_API_BASE_PATH", "")

	settings := loadSettings(t, "api_base_path: /custom/api\n")
	assert.Equal(t, Setting{Key: "api_base_path", Value: "/custom/api", Source: SourceFile}, settings["api_base_path"])
}

// With no auth.mode set, Load runs the server in builtin mode; the report
// says so rather than showing an empty value.
func TestSettingsAuthMode(t *testing.T) {
	t.Setenv("DAGU_AUTH_MODE", "")

	t.Run("Default", func(t *testing.T) {
		settings := loadSettings(t, "")
		assert.Equal(t, Setting{Key: "auth.mode", Value: "builtin", Source: SourceDefault}, settings["auth.mode"])
	})

	t.Run("File", func(t *testing.T) {
		settings := loadSettings(t, "auth:\n  mode: none\n")
		assert.Equal(t, Setting{Key: "auth.mode", Value: "none", Source: SourceFile}, settings["auth.mode"])
	})
}

func TestSettingsMasksSecrets(t *testing.T) {
	t.Setenv("DAGU_AUTH_TOKEN_SECRET", "")
	t.Setenv("DAGU_AUTH_BASIC_PASSWORD", "")
	t.Setenv("DAGU_LICENSE_KEY", "env-license")

	settings := loadSettings(t, `
auth:
  builtin:
    token:
      secret: file-secret
`)

	assert.Equal(t, Setting{Key: "auth.builtin.token.secret", Value: MaskedValue, Source: SourceFile}, settings["auth.builtin.token.secret"])
	assert.Equal(t, Setting{Key: "license.key", Value: MaskedValue, Source: "env:DAGU_LICENSE_KEY"}, settings["license.key"])
	assert.Equal(t, Setting{Key: "auth.basic.password", Value: "", Source: SourceDefault}, settings["auth.basic.password"])
}

func TestSettingsFormatsLists(t *testing.T) {
	t.Setenv("DAGU_CORS_ALLOWED_ORIGINS", "")

	settings := loadSettings(t, `
cors_allowed_origins:
  - https://a.example
  - https://b.example
`)

	assert.Equal(t, "https://a.example,https://b.example", settings["cors_allowed_origins"].Value)
}

// Config.Sources reports where each peer.* key and coordinator.host came
// from, by the precedence Load applies: a flag, then the environment, then a
// config file, then the default.
func TestLoadSources(t *testing.T) {
	t.Setenv("DAGU_COORDINATOR_HOST", "")
	t.Setenv("DAGU_PEER_INSECURE", "")
	t.Setenv("DAGU_PEER_CERT_FILE", "")

	t.Run("Default", func(t *testing.T) {
		cfg := testLoad(t)
		for _, key := range []string{"coordinator.host", "peer.insecure", "peer.cert_file", "peer.token"} {
			assert.Equal(t, ValueSource{Kind: SourceKindDefault}, cfg.Sources[key], key)
		}
	})

	t.Run("File", func(t *testing.T) {
		home := t.TempDir()
		configFile := writeHomeConfig(t, home, "config.yaml", "coordinator:\n  host: 0.0.0.0\npeer:\n  cert_file: /etc/dagu/client.crt\n")
		cfg := testLoad(t, WithAppHomeDir(home))
		want := ValueSource{Kind: SourceKindFile, Name: configFile}
		assert.Equal(t, want, cfg.Sources["coordinator.host"])
		assert.Equal(t, want, cfg.Sources["peer.cert_file"])
		assert.Equal(t, ValueSource{Kind: SourceKindDefault}, cfg.Sources["peer.insecure"])
	})

	// The legacy admin.yaml is merged over config.yaml, so a key that both
	// set comes from admin.yaml.
	t.Run("AdminFile", func(t *testing.T) {
		home := t.TempDir()
		configFile := writeHomeConfig(t, home, "config.yaml", "coordinator:\n  host: 0.0.0.0\npeer:\n  cert_file: /etc/dagu/client.crt\n")
		adminFile := writeHomeConfig(t, home, "admin.yaml", "coordinator:\n  host: 10.0.0.1\n")
		cfg := testLoad(t, WithAppHomeDir(home))
		assert.Equal(t, "10.0.0.1", cfg.Coordinator.Host)
		assert.Equal(t, ValueSource{Kind: SourceKindFile, Name: adminFile}, cfg.Sources["coordinator.host"])
		assert.Equal(t, ValueSource{Kind: SourceKindFile, Name: configFile}, cfg.Sources["peer.cert_file"])
	})

	t.Run("Env", func(t *testing.T) {
		home := t.TempDir()
		writeHomeConfig(t, home, "config.yaml", "coordinator:\n  host: 0.0.0.0\n")
		t.Setenv("DAGU_COORDINATOR_HOST", "10.0.0.2")
		cfg := testLoad(t, WithAppHomeDir(home))
		assert.Equal(t, ValueSource{Kind: SourceKindEnv, Name: "DAGU_COORDINATOR_HOST"}, cfg.Sources["coordinator.host"])
	})

	t.Run("Flag", func(t *testing.T) {
		t.Setenv("DAGU_COORDINATOR_HOST", "10.0.0.2")
		v := viper.New()
		// Set stands in for a bound flag: both take precedence over the
		// environment.
		v.Set("coordinator.host", "10.0.0.3")
		cfg, err := NewConfigLoader(v,
			WithAppHomeDir(t.TempDir()),
			WithFlags(map[string]string{"coordinator.host": "--coordinator.host"}),
		).Load()
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.3", cfg.Coordinator.Host)
		assert.Equal(t, ValueSource{Kind: SourceKindFlag, Name: "--coordinator.host"}, cfg.Sources["coordinator.host"])
	})
}

// An unset peer.insecure selects h2c as true does, and Config.Sources tells
// the two apart.
func TestLoadPeerInsecure(t *testing.T) {
	t.Setenv("DAGU_PEER_INSECURE", "")

	for _, tc := range []struct {
		name     string
		yaml     string
		insecure bool
		kind     SourceKind
	}{
		{"Unset", "", true, SourceKindDefault},
		{"NoValue", "peer:\n  insecure:\n", true, SourceKindDefault},
		{"True", "peer:\n  insecure: true\n", true, SourceKindFile},
		{"False", "peer:\n  insecure: false\n  skip_tls_verify: true\n", false, SourceKindFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadFromYAML(t, tc.yaml)
			assert.Equal(t, tc.insecure, cfg.Core.Peer.Insecure)
			assert.Equal(t, tc.kind, cfg.Sources["peer.insecure"].Kind)
		})
	}
}

// writeHomeConfig writes a config file named name into home, the app home
// directory Load searches, and returns its resolved path.
func writeHomeConfig(t *testing.T, home, name, content string) string {
	t.Helper()
	file := filepath.Join(home, name)
	require.NoError(t, os.WriteFile(file, []byte(content), 0600))
	return resolvedTestPath(t, file)
}
