// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLoad(t *testing.T, opts ...ConfigLoaderOption) *Config {
	t.Helper()
	opts = append([]ConfigLoaderOption{WithAppHomeDir(t.TempDir())}, opts...)
	cfg, err := NewConfigLoader(viper.New(), opts...).Load()
	require.NoError(t, err)
	return cfg
}

func testLoadWithError(t *testing.T, opts ...ConfigLoaderOption) error {
	t.Helper()
	opts = append([]ConfigLoaderOption{WithAppHomeDir(t.TempDir())}, opts...)
	_, err := NewConfigLoader(viper.New(), opts...).Load()
	return err
}

func preserveTZEnv(t *testing.T) {
	t.Helper()

	oldTZ, hadTZ := os.LookupEnv("TZ")
	t.Cleanup(func() {
		if hadTZ {
			if err := os.Setenv("TZ", oldTZ); err != nil {
				t.Fatalf("restore TZ: %v", err)
			}
			return
		}
		if err := os.Unsetenv("TZ"); err != nil {
			t.Fatalf("unset TZ: %v", err)
		}
	})
}

func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, entry := range env {
		if after, ok := strings.CutPrefix(entry, prefix); ok {
			return after, true
		}
	}
	return "", false
}

func envKeyCount(env []string, key string) int {
	prefix := key + "="
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			count++
		}
	}
	return count
}

func resolvedTestPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := fileutil.ResolvePath(path)
	require.NoError(t, err)
	return resolved
}

func TestLoad_Env(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")
	err := os.WriteFile(configFile, []byte("# minimal config"), 0600)
	require.NoError(t, err)

	testPaths := filepath.Join(tempDir, "test")

	testEnvs := map[string]string{
		"DAGU_LOG_FORMAT":    "json",
		"DAGU_BASE_PATH":     "/test/base",
		"DAGU_API_BASE_URL":  "/test/api",
		"DAGU_TZ":            "Europe/Berlin",
		"DAGU_HOST":          "test.example.com",
		"DAGU_PORT":          "9876",
		"DAGU_DEBUG":         "true",
		"DAGU_HEADLESS":      "true",
		"DAGU_CHECK_UPDATES": "false",

		"DAGU_DEFAULT_SHELL":                 "/bin/zsh",
		"DAGU_SECRETS_VAULT_ADDRESS":         "https://vault.example.com",
		"DAGU_SECRETS_VAULT_TOKEN":           "vault-token",
		"DAGU_SECRETS_KUBERNETES_NAMESPACE":  "secret-ns",
		"DAGU_SECRETS_KUBERNETES_KUBECONFIG": filepath.Join(testPaths, "kubeconfig"),
		"DAGU_SECRETS_KUBERNETES_CONTEXT":    "secret-context",

		"DAGU_UI_MAX_DASHBOARD_PAGE_LIMIT": "250",
		"DAGU_UI_LOG_ENCODING_CHARSET":     "iso-8859-1",
		"DAGU_UI_NAVBAR_COLOR":             "#123456",
		"DAGU_UI_NAVBAR_TITLE":             "Test Dagu",

		"DAGU_AUTH_MODE":           "basic",
		"DAGU_AUTH_BASIC_USERNAME": "testuser",
		"DAGU_AUTH_BASIC_PASSWORD": "testpass",

		"DAGU_CERT_FILE": filepath.Join(testPaths, "cert.pem"),
		"DAGU_KEY_FILE":  filepath.Join(testPaths, "key.pem"),

		"DAGU_DAGS_DIR":             filepath.Join(testPaths, "dags"),
		"DAGU_EXECUTABLE":           filepath.Join(testPaths, "bin", "dagu"),
		"DAGU_LOG_DIR":              filepath.Join(testPaths, "logs"),
		"DAGU_DATA_DIR":             filepath.Join(testPaths, "data"),
		"DAGU_ARTIFACT_DIR":         filepath.Join(testPaths, "artifacts"),
		"DAGU_SUSPEND_FLAGS_DIR":    filepath.Join(testPaths, "suspend"),
		"DAGU_ADMIN_LOG_DIR":        filepath.Join(testPaths, "admin"),
		"DAGU_BASE_CONFIG":          filepath.Join(testPaths, "base.yaml"),
		"DAGU_DAG_RUNS_DIR":         filepath.Join(testPaths, "runs"),
		"DAGU_PROC_DIR":             filepath.Join(testPaths, "proc"),
		"DAGU_QUEUE_DIR":            filepath.Join(testPaths, "queue"),
		"DAGU_SERVICE_REGISTRY_DIR": filepath.Join(testPaths, "service-registry"),
		"DAGU_ALT_DAGS_DIR":         filepath.Join(testPaths, "alt-dags"),

		"DAGU_LATEST_STATUS_TODAY": "true",

		"DAGU_QUEUE_ENABLED": "false",

		"DAGU_COORDINATOR_HOST":        "0.0.0.0",
		"DAGU_COORDINATOR_ADVERTISE":   "dagu-coordinator",
		"DAGU_COORDINATOR_PORT":        "50099",
		"DAGU_COORDINATOR_HEALTH_PORT": "50101",

		"DAGU_WORKER_ID":              "test-worker-123",
		"DAGU_WORKER_MAX_ACTIVE_RUNS": "200",
		"DAGU_WORKER_HEALTH_PORT":     "50102",

		"DAGU_SCHEDULER_PORT":                      "9999",
		"DAGU_SCHEDULER_ZOMBIE_DETECTION_INTERVAL": "90s",

		"DAGU_AUTH_OIDC_CLIENT_ID":     "test-client-id",
		"DAGU_AUTH_OIDC_CLIENT_SECRET": "test-secret",
		"DAGU_AUTH_OIDC_ISSUER":        "https://auth.example.com",
		"DAGU_AUTH_OIDC_SCOPES":        "openid,profile,email",

		"DAGU_UI_DAGS_SORT_FIELD": "status",
		"DAGU_UI_DAGS_SORT_ORDER": "desc",

		"DAGU_TERMINAL_ENABLED": "true",
		"DAGU_ACCESS_LOG_MODE":  "none",

		"DAGU_AUDIT_ENABLED": "false",

		"DAGU_TUNNEL_ENABLED":                              "true",
		"DAGU_TUNNEL_TAILSCALE_AUTH_KEY":                   "tskey-test-123",
		"DAGU_TUNNEL_TAILSCALE_HOSTNAME":                   "test-dagu",
		"DAGU_TUNNEL_TAILSCALE_FUNNEL":                     "false",
		"DAGU_TUNNEL_TAILSCALE_HTTPS":                      "true",
		"DAGU_TUNNEL_ALLOW_TERMINAL":                       "true",
		"DAGU_TUNNEL_RATE_LIMITING_ENABLED":                "true",
		"DAGU_TUNNEL_RATE_LIMITING_LOGIN_ATTEMPTS":         "10",
		"DAGU_TUNNEL_RATE_LIMITING_WINDOW_SECONDS":         "600",
		"DAGU_TUNNEL_RATE_LIMITING_BLOCK_DURATION_SECONDS": "1800",
	}

	for key, val := range testEnvs {
		t.Setenv(key, val)
	}

	cfg := testLoad(t, WithConfigFile(configFile))

	berlinLoc, _ := time.LoadLocation("Europe/Berlin")
	_, berlinOffset := time.Now().In(berlinLoc).Zone()

	require.NotEmpty(t, cfg.Paths.ConfigFileUsed)
	cfg.Paths.ConfigFileUsed = ""

	expected := &Config{
		Core: Core{
			Debug:                  true,
			LogFormat:              "json",
			TZ:                     "Europe/Berlin",
			TzOffsetInSec:          berlinOffset,
			Location:               berlinLoc,
			DefaultShell:           "/bin/zsh",
			SkipExamples:           false,
			EnvPassthrough:         []string{},
			EnvPassthroughPrefixes: []string{},
			Peer:                   Peer{Insecure: true}, // Default is true
			BaseEnv:                cfg.Core.BaseEnv,     // Dynamic, copy from actual
		},
		Server: Server{
			Host:         "test.example.com",
			Port:         9876,
			BasePath:     "/test/base",
			APIBasePath:  "/test/api",
			Headless:     true,
			CheckUpdates: false,
			AccessLog:    AccessLogNone,
			Auth: Auth{
				Mode:  AuthModeBasic, // Explicit basic mode from env
				Basic: AuthBasic{Username: "testuser", Password: "testpass"},
				OIDC: AuthOIDC{
					ClientID:     "test-client-id",
					ClientSecret: "test-secret",
					Issuer:       "https://auth.example.com",
					Scopes:       []string{"openid", "profile", "email"},
					AutoSignup:   true, // Defaults to true
					ButtonLabel:  "Login with SSO",
					RoleMapping:  OIDCRoleMapping{DefaultRole: "viewer"},
				},
				Builtin: AuthBuiltin{
					Token: TokenConfig{TTL: 24 * time.Hour},
				},
			},
			TLS: &TLSConfig{
				CertFile: filepath.Join(testPaths, "cert.pem"),
				KeyFile:  filepath.Join(testPaths, "key.pem"),
			},
			Permissions:       map[Permission]bool{PermissionWriteDAGs: true, PermissionRunDAGs: true},
			LatestStatusToday: true,
			StrictValidation:  false,
			Metrics:           MetricsAccessPrivate,
			Terminal:          TerminalConfig{Enabled: true, MaxSessions: 5},
			Audit:             AuditConfig{Enabled: false, RetentionDays: 7},
			Session:           SessionConfig{MaxPerUser: 100},
			SSE: SSEConfig{
				MaxTopicsPerConnection: 20,
				MaxClients:             1000,
				HeartbeatInterval:      10 * time.Second,
				WriteBufferSize:        65536,
				SlowClientTimeout:      30 * time.Second,
			},
		},
		EventStore: EventStoreConfig{
			Enabled:       true,
			RetentionDays: 1,
		},
		Webhooks: WebhooksConfig{
			MaxPayloadSize: DefaultWebhookMaxPayloadSize,
		},
		Paths: PathsConfig{
			DAGsDir:            filepath.Join(testPaths, "dags"),
			DocsDir:            filepath.Join(testPaths, "dags", "docs"),
			AltDAGsDir:         filepath.Join(testPaths, "alt-dags"),
			Executable:         filepath.Join(testPaths, "bin", "dagu"),
			LogDir:             filepath.Join(testPaths, "logs"),
			DataDir:            filepath.Join(testPaths, "data"),
			ArtifactDir:        filepath.Join(testPaths, "artifacts"),
			SuspendFlagsDir:    filepath.Join(testPaths, "suspend"),
			AdminLogsDir:       filepath.Join(testPaths, "admin"),
			EventStoreDir:      cfg.Paths.EventStoreDir,
			BaseConfig:         filepath.Join(testPaths, "base.yaml"),
			DAGRunsDir:         filepath.Join(testPaths, "runs"),
			ProcDir:            filepath.Join(testPaths, "proc"),
			QueueDir:           filepath.Join(testPaths, "queue"),
			ServiceRegistryDir: filepath.Join(testPaths, "service-registry"),
			UsersDir:           filepath.Join(testPaths, "data", "users"),             // Derived from DataDir
			APIKeysDir:         filepath.Join(testPaths, "data", "apikeys"),           // Derived from DataDir
			WebhooksDir:        filepath.Join(testPaths, "data", "webhooks"),          // Derived from DataDir
			SessionsDir:        filepath.Join(testPaths, "data", "agent", "sessions"), // Derived from DataDir
			ContextsDir:        filepath.Join(testPaths, "data", "contexts"),          // Derived from DataDir
			RemoteNodesDir:     filepath.Join(testPaths, "data", "remote-nodes"),      // Derived from DataDir
			WorkspacesDir:      filepath.Join(testPaths, "data", "workspaces"),        // Derived from DataDir
		},
		Secrets: SecretsConfig{
			Vault: VaultSecretsConfig{
				Address: "https://vault.example.com",
				Token:   "vault-token",
			},
			Kubernetes: KubernetesSecretsConfig{
				Namespace:  "secret-ns",
				Kubeconfig: filepath.Join(testPaths, "kubeconfig"),
				Context:    "secret-context",
			},
		},
		UI: UI{
			LogEncodingCharset:    "iso-8859-1",
			NavbarColor:           "#123456",
			NavbarTitle:           "Test Dagu",
			MaxDashboardPageLimit: 250,
			DAGs: DAGsConfig{
				SortField: "status",
				SortOrder: "desc",
			},
		},
		Queues: Queues{Enabled: false},
		Coordinator: Coordinator{
			Enabled:    true,
			Host:       "0.0.0.0",
			Advertise:  "dagu-coordinator",
			Port:       50099,
			HealthPort: 50101,
		},
		Worker: Worker{
			ID:            "test-worker-123",
			MaxActiveRuns: 200,
			HealthPort:    50102,
			PostgresPool: PostgresPoolConfig{
				MaxOpenConns:    25,
				MaxIdleConns:    5,
				ConnMaxLifetime: 300,
				ConnMaxIdleTime: 60,
			},
		},
		Proc: Proc{
			HeartbeatInterval:     5 * time.Second,
			HeartbeatSyncInterval: 10 * time.Second,
			StaleThreshold:        90 * time.Second,
		},
		Scheduler: Scheduler{
			Port:                    9999,
			LockStaleThreshold:      30 * time.Second,
			LockRetryInterval:       5 * time.Second,
			ZombieDetectionInterval: 90 * time.Second,
			RetryFailureWindow:      24 * time.Hour,
			FailureThreshold:        3,
		},
		Monitoring: MonitoringConfig{
			Retention: 24 * time.Hour,
			Interval:  5 * time.Second,
		},
		Tunnel: TunnelConfig{
			Enabled:       true,
			AllowTerminal: true,
			Tailscale: TailscaleTunnelConfig{
				AuthKey:  "tskey-test-123",
				Hostname: "test-dagu",
				Funnel:   false,
				HTTPS:    true,
			},
			RateLimiting: TunnelRateLimitConfig{
				Enabled:              true,
				LoginAttempts:        10,
				WindowSeconds:        600,
				BlockDurationSeconds: 1800,
			},
		},
		Bots: BotsConfig{
			SafeMode: true,
			Telegram: TelegramBotConfig{
				InterestedEventTypes: DefaultBotInterestedEventTypes,
			},
			Slack: SlackBotConfig{
				InterestedEventTypes: DefaultBotInterestedEventTypes,
				RespondToAll:         true,
			},
			Discord: DiscordBotConfig{
				InterestedEventTypes: DefaultBotInterestedEventTypes,
				RespondToAll:         true,
			},
		},
		DefaultExecMode: ExecutionModeLocal,
		Warnings:        nil,
		Cache:           CacheModeNormal,
	}

	assert.Equal(t, expected, cfg)
}

func TestLoad_HealthPortDefaults(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")
	err := os.WriteFile(configFile, []byte("# minimal config"), 0600)
	require.NoError(t, err)

	cfg := testLoad(t, WithConfigFile(configFile))

	assert.Equal(t, 8091, cfg.Coordinator.HealthPort)
	assert.Equal(t, 8092, cfg.Worker.HealthPort)
}

func TestLoad_HealthPortsCanBeDisabledWithZero(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")
	err := os.WriteFile(configFile, []byte(`
coordinator:
  health_port: 0
worker:
  health_port: 0
`), 0600)
	require.NoError(t, err)

	cfg := testLoad(t, WithConfigFile(configFile))

	assert.Zero(t, cfg.Coordinator.HealthPort)
	assert.Zero(t, cfg.Worker.HealthPort)
}

func TestLoad_WithAppHomeDir(t *testing.T) {
	tempDir := t.TempDir()
	cfg := testLoad(t, WithAppHomeDir(tempDir))

	assert.Equal(t, filepath.Join(tempDir, "dags"), cfg.Paths.DAGsDir)
	assert.Equal(t, filepath.Join(tempDir, "data"), cfg.Paths.DataDir)
	assert.Equal(t, filepath.Join(tempDir, "logs"), cfg.Paths.LogDir)
	assert.Equal(t, filepath.Join(tempDir, "data", "artifacts"), cfg.Paths.ArtifactDir)

	baseEnv := cfg.Core.BaseEnv.AsSlice()
	require.Contains(t, baseEnv, fmt.Sprintf("DAGU_HOME=%s", tempDir))
}

func TestLoad_BaseEnvIncludesConfigDerivedOverrides(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")
	executablePath := filepath.Join(tempDir, "bin", "dagu")
	preserveTZEnv(t)
	require.NoError(t, os.Setenv("TZ", "America/Los_Angeles"))
	err := os.WriteFile(configFile, fmt.Appendf(nil, `
default_shell: /bin/sh
tz: UTC
paths:
  executable: %s
`, executablePath), 0o600)
	require.NoError(t, err)

	cfg := testLoad(t, WithConfigFile(configFile), WithAppHomeDir(tempDir))
	baseEnv := cfg.Core.BaseEnv.AsSlice()

	require.Contains(t, baseEnv, fmt.Sprintf("DAGU_HOME=%s", tempDir))
	require.Contains(t, baseEnv, fmt.Sprintf("DAGU_CONFIG=%s", configFile))
	require.Contains(t, baseEnv, fmt.Sprintf("DAGU_EXECUTABLE=%s", executablePath))
	require.Contains(t, baseEnv, "SHELL=/bin/sh")
	require.Contains(t, baseEnv, "TZ=UTC")
	require.NotContains(t, baseEnv, "TZ=America/Los_Angeles")
	require.Equal(t, 1, envKeyCount(baseEnv, "TZ"))
}

func TestLoad_BaseEnvPreservesInheritedTZWhenTimezoneConfigUnset(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")
	preserveTZEnv(t)
	require.NoError(t, os.Setenv("TZ", "America/Los_Angeles"))
	require.NoError(t, os.WriteFile(configFile, []byte("# minimal config"), 0o600))

	cfg := testLoad(t, WithConfigFile(configFile), WithAppHomeDir(tempDir))
	baseEnv := cfg.Core.BaseEnv.AsSlice()

	tzValue, ok := envValue(baseEnv, "TZ")
	require.True(t, ok)
	require.Equal(t, "America/Los_Angeles", tzValue)
	require.Equal(t, 1, envKeyCount(baseEnv, "TZ"))
	require.NotEmpty(t, cfg.Core.TZ)
}

func TestLoad_BaseEnvDoesNotInjectSyntheticTZWhenTimezoneConfigUnset(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")
	preserveTZEnv(t)
	require.NoError(t, os.Unsetenv("TZ"))
	require.NoError(t, os.WriteFile(configFile, []byte("# minimal config"), 0o600))

	cfg := testLoad(t, WithConfigFile(configFile), WithAppHomeDir(tempDir))
	baseEnv := cfg.Core.BaseEnv.AsSlice()

	_, ok := envValue(baseEnv, "TZ")
	require.False(t, ok)

	expectedTZ := "UTC"
	if cfg.Core.TzOffsetInSec != 0 {
		expectedTZ = fmt.Sprintf("UTC%+d", cfg.Core.TzOffsetInSec/3600)
	}
	require.Equal(t, expectedTZ, cfg.Core.TZ)
}

func TestLoad_BaseEnvIncludesConfiguredEnvPassthroughFromYAML(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")
	preserveTZEnv(t)
	t.Setenv("CONFIG_EXACT_ENV", "exact-value")
	t.Setenv("CONFIG_PREFIX_TOKEN", "prefix-value")
	t.Setenv("CONFIG_BLOCKED_ENV", "blocked-value")
	require.NoError(t, os.WriteFile(configFile, []byte(`
env_passthrough:
  - CONFIG_EXACT_ENV
  - CONFIG_EXACT_ENV
env_passthrough_prefixes:
  - CONFIG_PREFIX_
  - CONFIG_PREFIX_
`), 0o600))

	cfg := testLoad(t, WithConfigFile(configFile), WithAppHomeDir(tempDir))
	baseEnv := cfg.Core.BaseEnv.AsSlice()

	require.Equal(t, []string{"CONFIG_EXACT_ENV"}, cfg.Core.EnvPassthrough)
	require.Equal(t, []string{"CONFIG_PREFIX_"}, cfg.Core.EnvPassthroughPrefixes)
	require.Contains(t, baseEnv, "CONFIG_EXACT_ENV=exact-value")
	require.Contains(t, baseEnv, "CONFIG_PREFIX_TOKEN=prefix-value")
	require.NotContains(t, baseEnv, "CONFIG_BLOCKED_ENV=blocked-value")
}

func TestLoad_BaseEnvIncludesConfiguredEnvPassthroughFromEnv(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")
	preserveTZEnv(t)
	t.Setenv("EXACT_FROM_ENV", "exact-value")
	t.Setenv("PREFIX_FROM_ENV_TOKEN", "prefix-value")
	t.Setenv("BLOCKED_FROM_ENV", "blocked-value")
	t.Setenv("DAGU_ENV_PASSTHROUGH", " EXACT_FROM_ENV , EXACT_FROM_ENV ,,")
	t.Setenv("DAGU_ENV_PASSTHROUGH_PREFIXES", " PREFIX_FROM_ENV_ , PREFIX_FROM_ENV_ ,,")
	require.NoError(t, os.WriteFile(configFile, []byte("# minimal config"), 0o600))

	cfg := testLoad(t, WithConfigFile(configFile), WithAppHomeDir(tempDir))
	baseEnv := cfg.Core.BaseEnv.AsSlice()

	require.Equal(t, []string{"EXACT_FROM_ENV"}, cfg.Core.EnvPassthrough)
	require.Equal(t, []string{"PREFIX_FROM_ENV_"}, cfg.Core.EnvPassthroughPrefixes)
	require.Contains(t, baseEnv, "EXACT_FROM_ENV=exact-value")
	require.Contains(t, baseEnv, "PREFIX_FROM_ENV_TOKEN=prefix-value")
	require.NotContains(t, baseEnv, "BLOCKED_FROM_ENV=blocked-value")
}

func TestLoad_YAML(t *testing.T) {
	cfg := loadFromYAML(t, `
host: "0.0.0.0"
port: 9090
permissions:
  write_dags: false
  run_dags: false
debug: true
base_path: "/dagu"
api_base_path: "/api/v1"
tz: "UTC"
log_format: "json"
headless: true
check_updates: false
latest_status_today: true
default_shell: "/bin/bash"
skip_examples: true
paths:
  dags_dir: "/var/dagu/dags"
  log_dir: "/var/dagu/logs"
  data_dir: "/var/dagu/data"
  suspend_flags_dir: "/var/dagu/suspend"
  admin_logs_dir: "/var/dagu/adminlogs"
  base_config: "/var/dagu/base.yaml"
  executable: "/usr/local/bin/dagu"
ui:
  navbar_title: "Test Dagu"
  navbar_color: "#ff5733"
  log_encoding_charset: "iso-8859-1"
  max_dashboard_page_limit: 50
  dags:
    sort_field: "name"
    sort_order: "asc"
auth:
  mode: "basic"
  basic:
    username: "admin"
    password: "secret"
  oidc:
    client_id: "test-client-id"
    client_secret: "test-client-secret"
    client_url: "http://localhost:8081"
    issuer: "https://accounts.example.com"
    scopes:
      - "openid"
      - "profile"
      - "email"
    whitelist:
      - "user@example.com"
remote_nodes:
  - name: "node1"
    description: "Primary processing node"
    api_base_url: "http://node1.example.com/api"
    auth_type: basic
    basic_auth_username: "nodeuser"
    basic_auth_password: "nodepass"
    skip_tls_verify: true
  - name: "node2"
    api_base_url: "http://node2.example.com/api"
    auth_type: token
    auth_token: "node-token-123"
tls:
  cert_file: "/path/to/cert.pem"
  key_file: "/path/to/key.pem"
  ca_file: "/path/to/ca.pem"
peer:
  cert_file: "/path/to/peer-cert.pem"
  key_file: "/path/to/peer-key.pem"
  client_ca_file: "/path/to/peer-ca.pem"
  skip_tls_verify: false
  insecure: false
queues:
  enabled: true
  config:
    - name: "critical"
      max_active_runs: 5
    - name: "normal"
      max_active_runs: 10
coordinator:
  host: "coordinator.example.com"
  port: 8081
worker:
  id: "worker-1"
  max_active_runs: 50
  labels:
    env: "production"
    region: "us-west-2"
scheduler:
  port: 7890
  lock_stale_threshold: 50s
  lock_retry_interval: 10s
  zombie_detection_interval: 60s
`)

	utcLoc, _ := time.LoadLocation("UTC")

	expected := &Config{
		Core: Core{
			Debug:                  true,
			LogFormat:              "json",
			TZ:                     "UTC",
			TzOffsetInSec:          0,
			Location:               utcLoc,
			DefaultShell:           "/bin/bash",
			SkipExamples:           true,
			EnvPassthrough:         []string{},
			EnvPassthroughPrefixes: []string{},
			Peer: Peer{
				CertFile:      "/path/to/peer-cert.pem",
				KeyFile:       "/path/to/peer-key.pem",
				ClientCaFile:  "/path/to/peer-ca.pem",
				SkipTLSVerify: false,
				Insecure:      false,
			},
			BaseEnv: cfg.Core.BaseEnv, // Dynamic, copy from actual
		},
		Server: Server{
			Host:              "0.0.0.0",
			Port:              9090,
			BasePath:          "/dagu",
			APIBasePath:       "/api/v1",
			Headless:          true,
			CheckUpdates:      false,
			AccessLog:         AccessLogAll,
			LatestStatusToday: true,
			Auth: Auth{
				Mode:  AuthModeBasic, // Explicit basic mode from YAML
				Basic: AuthBasic{Username: "admin", Password: "secret"},
				OIDC: AuthOIDC{
					ClientID:     "test-client-id",
					ClientSecret: "test-client-secret",
					ClientURL:    "http://localhost:8081",
					Issuer:       "https://accounts.example.com",
					Scopes:       []string{"openid", "profile", "email"},
					Whitelist:    []string{"user@example.com"},
					AutoSignup:   true, // Defaults to true
					ButtonLabel:  "Login with SSO",
					RoleMapping:  OIDCRoleMapping{DefaultRole: "viewer"},
				},
				Builtin: AuthBuiltin{
					Token: TokenConfig{TTL: 24 * time.Hour},
				},
			},
			TLS: &TLSConfig{
				CertFile: "/path/to/cert.pem",
				KeyFile:  "/path/to/key.pem",
				CAFile:   "/path/to/ca.pem",
			},
			RemoteNodes: []RemoteNode{
				{
					Name:              "node1",
					Description:       "Primary processing node",
					APIBaseURL:        "http://node1.example.com/api",
					AuthType:          "basic",
					BasicAuthUsername: "nodeuser",
					BasicAuthPassword: "nodepass",
					SkipTLSVerify:     true,
				},
				{
					Name:       "node2",
					APIBaseURL: "http://node2.example.com/api",
					AuthType:   "token",
					AuthToken:  "node-token-123",
				},
			},
			Permissions: map[Permission]bool{
				PermissionWriteDAGs: false,
				PermissionRunDAGs:   false,
			},
			Metrics:  MetricsAccessPrivate,
			Terminal: TerminalConfig{Enabled: false, MaxSessions: 5},
			Audit:    AuditConfig{Enabled: true, RetentionDays: 7},
			Session:  SessionConfig{MaxPerUser: 100},
			SSE: SSEConfig{
				MaxTopicsPerConnection: 20,
				MaxClients:             1000,
				HeartbeatInterval:      10 * time.Second,
				WriteBufferSize:        65536,
				SlowClientTimeout:      30 * time.Second,
			},
		},
		EventStore: EventStoreConfig{
			Enabled:       true,
			RetentionDays: 1,
		},
		Webhooks: WebhooksConfig{
			MaxPayloadSize: DefaultWebhookMaxPayloadSize,
		},
		Paths: PathsConfig{
			DAGsDir:            resolvedTestPath(t, "/var/dagu/dags"),
			DocsDir:            resolvedTestPath(t, "/var/dagu/dags/docs"),
			LogDir:             resolvedTestPath(t, "/var/dagu/logs"),
			DataDir:            resolvedTestPath(t, "/var/dagu/data"),
			ArtifactDir:        cfg.Paths.ArtifactDir,
			SuspendFlagsDir:    resolvedTestPath(t, "/var/dagu/suspend"),
			AdminLogsDir:       resolvedTestPath(t, "/var/dagu/adminlogs"),
			EventStoreDir:      cfg.Paths.EventStoreDir,
			BaseConfig:         resolvedTestPath(t, "/var/dagu/base.yaml"),
			Executable:         resolvedTestPath(t, "/usr/local/bin/dagu"),
			DAGRunsDir:         resolvedTestPath(t, "/var/dagu/data/dag-runs"),
			ProcDir:            resolvedTestPath(t, "/var/dagu/data/proc"),
			QueueDir:           resolvedTestPath(t, "/var/dagu/data/queue"),
			ServiceRegistryDir: resolvedTestPath(t, "/var/dagu/data/service-registry"),
			UsersDir:           resolvedTestPath(t, "/var/dagu/data/users"),
			APIKeysDir:         resolvedTestPath(t, "/var/dagu/data/apikeys"),
			WebhooksDir:        resolvedTestPath(t, "/var/dagu/data/webhooks"),
			SessionsDir:        resolvedTestPath(t, "/var/dagu/data/agent/sessions"),
			ContextsDir:        resolvedTestPath(t, "/var/dagu/data/contexts"),
			RemoteNodesDir:     resolvedTestPath(t, "/var/dagu/data/remote-nodes"),
			WorkspacesDir:      resolvedTestPath(t, "/var/dagu/data/workspaces"),
		},
		UI: UI{
			LogEncodingCharset:    "iso-8859-1",
			NavbarColor:           "#ff5733",
			NavbarTitle:           "Test Dagu",
			MaxDashboardPageLimit: 50,
			DAGs: DAGsConfig{
				SortField: "name",
				SortOrder: "asc",
			},
		},
		Queues: Queues{
			Enabled: true,
			Config: []QueueConfig{
				{Name: "critical", MaxActiveRuns: 5},
				{Name: "normal", MaxActiveRuns: 10},
			},
		},
		Coordinator: Coordinator{
			Enabled:    true,
			Host:       "coordinator.example.com",
			Port:       8081,
			HealthPort: 8091,
		},
		Worker: Worker{
			ID:            "worker-1",
			MaxActiveRuns: 50,
			HealthPort:    8092,
			Labels: map[string]string{
				"env":    "production",
				"region": "us-west-2",
			},
			PostgresPool: PostgresPoolConfig{
				MaxOpenConns:    25,
				MaxIdleConns:    5,
				ConnMaxLifetime: 300,
				ConnMaxIdleTime: 60,
			},
		},
		Proc: Proc{
			HeartbeatInterval:     5 * time.Second,
			HeartbeatSyncInterval: 10 * time.Second,
			StaleThreshold:        90 * time.Second,
		},
		Scheduler: Scheduler{
			Port:                    7890,
			LockStaleThreshold:      50 * time.Second,
			LockRetryInterval:       10 * time.Second,
			ZombieDetectionInterval: 60 * time.Second,
			RetryFailureWindow:      24 * time.Hour,
			FailureThreshold:        3,
		},
		Monitoring: MonitoringConfig{
			Retention: 24 * time.Hour,
			Interval:  5 * time.Second,
		},
		Bots: BotsConfig{
			SafeMode: true,
			Telegram: TelegramBotConfig{
				InterestedEventTypes: DefaultBotInterestedEventTypes,
			},
			Slack: SlackBotConfig{
				InterestedEventTypes: DefaultBotInterestedEventTypes,
				RespondToAll:         true,
			},
			Discord: DiscordBotConfig{
				InterestedEventTypes: DefaultBotInterestedEventTypes,
				RespondToAll:         true,
			},
		},
		DefaultExecMode: ExecutionModeLocal,
		Warnings:        nil,
		Cache:           CacheModeNormal,
	}

	assert.Equal(t, expected, cfg)
}

func TestLoad_EdgeCases_WorkerLabels(t *testing.T) {
	t.Run("LabelsFromString", func(t *testing.T) {
		cfg := loadFromYAML(t, `
worker:
  labels: "gpu=true,memory=64G,region=us-east-1"
`)
		assert.Equal(t, map[string]string{
			"gpu":    "true",
			"memory": "64G",
			"region": "us-east-1",
		}, cfg.Worker.Labels)
	})

	t.Run("LabelsFromMap", func(t *testing.T) {
		cfg := loadFromYAML(t, `
worker:
  labels:
    gpu: "true"
    memory: "64G"
    region: "us-west-2"
`)
		assert.Equal(t, map[string]string{
			"gpu":    "true",
			"memory": "64G",
			"region": "us-west-2",
		}, cfg.Worker.Labels)
	})

	t.Run("LabelsFromEnvironment", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_WORKER_LABELS": "instance-type=m5.xlarge,cpu-arch=amd64",
		})
		assert.Equal(t, map[string]string{
			"instance-type": "m5.xlarge",
			"cpu-arch":      "amd64",
		}, cfg.Worker.Labels)
	})
}

func TestLoad_EdgeCases_DerivedPaths(t *testing.T) {
	cfg := loadFromYAML(t, `
paths:
  data_dir: "/custom/data"
`)
	dataDir := resolvedTestPath(t, "/custom/data")
	assert.Equal(t, dataDir, cfg.Paths.DataDir)
	assert.Equal(t, filepath.Join(dataDir, "dag-runs"), cfg.Paths.DAGRunsDir)
	assert.Equal(t, filepath.Join(dataDir, "proc"), cfg.Paths.ProcDir)
	assert.Equal(t, filepath.Join(dataDir, "queue"), cfg.Paths.QueueDir)
	assert.Equal(t, filepath.Join(dataDir, "service-registry"), cfg.Paths.ServiceRegistryDir)
	assert.Equal(t, filepath.Join(dataDir, "users"), cfg.Paths.UsersDir)
	assert.Equal(t, filepath.Join(dataDir, "agent", "sessions"), cfg.Paths.SessionsDir)
	assert.Equal(t, filepath.Join(dataDir, "contexts"), cfg.Paths.ContextsDir)
}

func TestLoad_EdgeCases_ContextsDirFromEnv(t *testing.T) {
	cfg := loadWithEnv(t, "# empty", map[string]string{
		"DAGU_CONTEXTS_DIR": "/tmp/custom-contexts",
	})

	assert.Equal(t, resolvedTestPath(t, "/tmp/custom-contexts"), cfg.Paths.ContextsDir)
}

func TestLoad_ArtifactDirFromConfig(t *testing.T) {
	cfg := loadFromYAML(t, `
paths:
  artifact_dir: "/custom/artifacts"
`)

	assert.Equal(t, resolvedTestPath(t, "/custom/artifacts"), cfg.Paths.ArtifactDir)
}

func TestLoad_ArtifactDirFromEnv(t *testing.T) {
	cfg := loadWithEnv(t, "# empty", map[string]string{
		"DAGU_ARTIFACT_DIR": "/env/artifacts",
	})

	assert.Equal(t, resolvedTestPath(t, "/env/artifacts"), cfg.Paths.ArtifactDir)
}

func TestLoad_EdgeCases_Errors(t *testing.T) {
	t.Run("InvalidTimezone", func(t *testing.T) {
		err := loadWithErrorFromYAML(t, `tz: "Invalid/Timezone"`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load timezone")
	})

	t.Run("IncompleteTLS", func(t *testing.T) {
		err := loadWithErrorFromYAML(t, `
tls:
  cert_file: "/path/to/cert.pem"
  key_file: ""
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "TLS configuration incomplete")
	})

	t.Run("InvalidPort_Negative", func(t *testing.T) {
		err := loadWithErrorFromYAML(t, `port: -1`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid port number")
	})

	t.Run("InvalidPort_TooLarge", func(t *testing.T) {
		err := loadWithErrorFromYAML(t, `port: 99999`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid port number")
	})

	t.Run("InvalidMaxDashboardPageLimit", func(t *testing.T) {
		err := loadWithErrorFromYAML(t, `
ui:
  max_dashboard_page_limit: 0
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid max dashboard page limit")
	})

	t.Run("InvalidSchedulerDurations", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: none
scheduler:
  lock_stale_threshold: "invalid"
  lock_retry_interval: "bad-duration"
  zombie_detection_interval: "not-a-duration"
`)
		assert.Equal(t, 30*time.Second, cfg.Scheduler.LockStaleThreshold)
		assert.Equal(t, 5*time.Second, cfg.Scheduler.LockRetryInterval)
		assert.Equal(t, time.Duration(0), cfg.Scheduler.ZombieDetectionInterval)

		require.Len(t, cfg.Warnings, 3)
		assert.Contains(t, cfg.Warnings[0], "Invalid scheduler.lock_stale_threshold")
		assert.Contains(t, cfg.Warnings[1], "Invalid scheduler.lock_retry_interval")
		assert.Contains(t, cfg.Warnings[2], "Invalid scheduler.zombie_detection_interval")
	})

	t.Run("ProcConfigLegacySchedulerAliases", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: none
proc:
  heartbeat_interval: 7s
  heartbeat_sync_interval: 11s
  stale_threshold: 95s
scheduler:
  heartbeat_interval: 20s
  heartbeat_sync_interval: 25s
  stale_threshold: 120s
`)

		assert.Equal(t, 7*time.Second, cfg.Proc.HeartbeatInterval)
		assert.Equal(t, 11*time.Second, cfg.Proc.HeartbeatSyncInterval)
		assert.Equal(t, 95*time.Second, cfg.Proc.StaleThreshold)
		require.Len(t, cfg.Warnings, 3)
		assert.Contains(t, cfg.Warnings[0], "scheduler.heartbeat_interval is deprecated and ignored")
		assert.Contains(t, cfg.Warnings[1], "scheduler.heartbeat_sync_interval is deprecated and ignored")
		assert.Contains(t, cfg.Warnings[2], "scheduler.stale_threshold is deprecated and ignored")
	})

	t.Run("ProcConfigLegacySchedulerFallback", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: none
scheduler:
  heartbeat_interval: 8s
  heartbeat_sync_interval: 12s
  stale_threshold: 100s
`)

		assert.Equal(t, 8*time.Second, cfg.Proc.HeartbeatInterval)
		assert.Equal(t, 12*time.Second, cfg.Proc.HeartbeatSyncInterval)
		assert.Equal(t, 100*time.Second, cfg.Proc.StaleThreshold)
		require.Len(t, cfg.Warnings, 3)
		assert.Contains(t, cfg.Warnings[0], "scheduler.heartbeat_interval is deprecated")
		assert.Contains(t, cfg.Warnings[1], "scheduler.heartbeat_sync_interval is deprecated")
		assert.Contains(t, cfg.Warnings[2], "scheduler.stale_threshold is deprecated")
	})

	t.Run("ProcConfigLoadsForServiceScopedCommands", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			service Service
		}{
			{name: "Server", service: ServiceServer},
			{name: "Scheduler", service: ServiceScheduler},
			{name: "Worker", service: ServiceWorker},
			{name: "Coordinator", service: ServiceCoordinator},
			{name: "Agent", service: ServiceAgent},
		} {
			t.Run(tc.name, func(t *testing.T) {
				configFile := filepath.Join(t.TempDir(), "config.yaml")
				err := os.WriteFile(configFile, []byte(`
auth:
  mode: none
proc:
  heartbeat_interval: 6s
  heartbeat_sync_interval: 9s
  stale_threshold: 75s
`), 0600)
				require.NoError(t, err)

				cfg := testLoad(t, WithConfigFile(configFile), WithService(tc.service))

				assert.Equal(t, 6*time.Second, cfg.Proc.HeartbeatInterval)
				assert.Equal(t, 9*time.Second, cfg.Proc.HeartbeatSyncInterval)
				assert.Equal(t, 75*time.Second, cfg.Proc.StaleThreshold)
			})
		}
	})

	t.Run("BuiltinAuthWithBasicAuthError", func(t *testing.T) {
		t.Parallel()
		configFile := filepath.Join(t.TempDir(), "config.yaml")
		err := os.WriteFile(configFile, []byte(`
auth:
  mode: builtin
  builtin:
    admin:
      username: admin
    token:
      secret: test-secret
  basic:
    username: basicuser
    password: basicpass
paths:
  users_dir: /tmp/users
`), 0600)
		require.NoError(t, err)
		loadErr := testLoadWithError(t, WithConfigFile(configFile))
		require.Error(t, loadErr)
		require.Contains(t, loadErr.Error(), "auth.basic credentials are set but auth.mode is")
	})
}

func TestLoad_LegacyEnv(t *testing.T) {
	tempDir := t.TempDir()

	cfg := loadWithEnv(t, "# empty", map[string]string{
		"DAGU__ADMIN_PORT":         "1234",
		"DAGU__ADMIN_HOST":         "0.0.0.0",
		"DAGU__ADMIN_NAVBAR_TITLE": "LegacyTitle",
		"DAGU__ADMIN_NAVBAR_COLOR": "#abc123",
		"DAGU__DATA":               filepath.Join(tempDir, "legacy", "data"),
		"DAGU__SUSPEND_FLAGS_DIR":  filepath.Join(tempDir, "legacy", "suspend"),
		"DAGU__ADMIN_LOGS_DIR":     filepath.Join(tempDir, "legacy", "adminlogs"),
	})

	assert.Equal(t, 1234, cfg.Server.Port)
	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, "LegacyTitle", cfg.UI.NavbarTitle)
	assert.Equal(t, "#abc123", cfg.UI.NavbarColor)
	assert.Equal(t, filepath.Join(tempDir, "legacy", "data"), cfg.Paths.DataDir)
	assert.Equal(t, filepath.Join(tempDir, "legacy", "suspend"), cfg.Paths.SuspendFlagsDir)
	assert.Equal(t, filepath.Join(tempDir, "legacy", "adminlogs"), cfg.Paths.AdminLogsDir)
}

func TestLoad_LoadLegacyFields(t *testing.T) {
	t.Parallel()
	loader := &ConfigLoader{}

	t.Run("AllFieldsSet", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		testPaths := filepath.Join(tempDir, "test")

		def := Definition{
			APIBaseURL:            "/api/v1",
			DAGs:                  filepath.Join(testPaths, "legacy", "dags"),
			DAGsDir:               filepath.Join(testPaths, "new", "dags"), // Takes precedence over DAGs
			Executable:            filepath.Join(testPaths, "bin", "dagu"),
			LogDir:                filepath.Join(testPaths, "logs"),
			DataDir:               filepath.Join(testPaths, "data"),
			SuspendFlagsDir:       filepath.Join(testPaths, "suspend"),
			AdminLogsDir:          filepath.Join(testPaths, "adminlogs"),
			BaseConfig:            filepath.Join(testPaths, "base.yaml"),
			LogEncodingCharset:    "iso-8859-1",
			NavbarColor:           "#123456",
			NavbarTitle:           "Title",
			MaxDashboardPageLimit: 100,
		}

		cfg := Config{}
		err := loader.LoadLegacyFields(&cfg, def)
		require.NoError(t, err)

		// Auth
		assert.Equal(t, "/api/v1", cfg.Server.APIBasePath)

		// Paths - DAGsDir should take precedence over DAGs
		assert.Equal(t, filepath.Join(testPaths, "new", "dags"), cfg.Paths.DAGsDir)
		assert.Equal(t, filepath.Join(testPaths, "bin", "dagu"), cfg.Paths.Executable)
		assert.Equal(t, filepath.Join(testPaths, "logs"), cfg.Paths.LogDir)
		assert.Equal(t, filepath.Join(testPaths, "data"), cfg.Paths.DataDir)
		assert.Equal(t, filepath.Join(testPaths, "suspend"), cfg.Paths.SuspendFlagsDir)
		assert.Equal(t, filepath.Join(testPaths, "adminlogs"), cfg.Paths.AdminLogsDir)
		assert.Equal(t, filepath.Join(testPaths, "base.yaml"), cfg.Paths.BaseConfig)

		// UI
		assert.Equal(t, "iso-8859-1", cfg.UI.LogEncodingCharset)
		assert.Equal(t, "#123456", cfg.UI.NavbarColor)
		assert.Equal(t, "Title", cfg.UI.NavbarTitle)
		assert.Equal(t, 100, cfg.UI.MaxDashboardPageLimit)
	})

	t.Run("DAGsPrecedence", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()

		def := Definition{
			DAGs:    filepath.Join(tempDir, "legacy", "dags"),
			DAGsDir: filepath.Join(tempDir, "new", "dags"),
		}
		cfg := Config{}
		err := loader.LoadLegacyFields(&cfg, def)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(tempDir, "new", "dags"), cfg.Paths.DAGsDir)

		def2 := Definition{
			DAGs: filepath.Join(tempDir, "legacy", "dags"),
		}
		cfg2 := Config{}
		err = loader.LoadLegacyFields(&cfg2, def2)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(tempDir, "legacy", "dags"), cfg2.Paths.DAGsDir)
	})
}

func loadWithEnv(t *testing.T, yamlContent string, env map[string]string) *Config {
	t.Helper()

	for k, v := range env {
		t.Setenv(k, v)
	}

	return loadFromYAML(t, yamlContent)
}

func loadFromYAML(t *testing.T, yamlContent string) *Config {
	t.Helper()

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(configFile, []byte(yamlContent), 0600)
	require.NoError(t, err)

	cfg := testLoad(t, WithConfigFile(configFile))
	cfg.Paths.ConfigFileUsed = ""
	return cfg
}

func loadWithErrorFromYAML(t *testing.T, yamlContent string) error {
	t.Helper()

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(configFile, []byte(yamlContent), 0600)
	require.NoError(t, err)

	return testLoadWithError(t, WithConfigFile(configFile))
}

func TestLoad_ConfigFileUsed(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")
	err := os.WriteFile(configFile, []byte("host: 127.0.0.1"), 0600)
	require.NoError(t, err)

	cfg := testLoad(t, WithConfigFile(configFile))

	assert.Equal(t, configFile, cfg.Paths.ConfigFileUsed)
}

func TestLoad_ConfigFileUsed_RelativePath(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")
	err := os.WriteFile(configFile, []byte("host: 127.0.0.1"), 0600)
	require.NoError(t, err)

	origDir, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		_ = os.Chdir(origDir)
	}()

	err = os.Chdir(tempDir)
	require.NoError(t, err)

	cfg := testLoad(t, WithConfigFile("config.yaml"))

	assert.True(t, filepath.IsAbs(cfg.Paths.ConfigFileUsed),
		"ConfigFileUsed should be absolute, got: %s", cfg.Paths.ConfigFileUsed)

	expectedPath, err := filepath.EvalSymlinks(configFile)
	require.NoError(t, err)
	actualPath, err := filepath.EvalSymlinks(cfg.Paths.ConfigFileUsed)
	require.NoError(t, err)
	assert.Equal(t, expectedPath, actualPath)
}

func TestBindEnv_AsPath(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		wantAbs  bool
	}{
		{
			name:     "relative path gets resolved to absolute",
			envValue: "relative/path",
			wantAbs:  true,
		},
		{
			name:     "absolute path stays absolute",
			envValue: filepath.Join(os.TempDir(), "absolute/path"),
			wantAbs:  true,
		},
		{
			name:     "empty value stays empty",
			envValue: "",
			wantAbs:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const envKey = "DAGU_DAGS_DIR"
			t.Setenv(envKey, tt.envValue)

			loader := NewConfigLoader(viper.New())
			loader.bindEnvironmentVariables()

			result := os.Getenv(envKey)
			if tt.envValue == "" {
				assert.Empty(t, result)
			} else if tt.wantAbs {
				assert.True(t, filepath.IsAbs(result), "expected absolute path, got: %s", result)
			}
		})
	}
}

func TestLoad_BotInterestedEventTypesEnvOverridesConfig(t *testing.T) {
	t.Run("telegram env overrides config", func(t *testing.T) {
		cfg := loadWithEnv(t, `
bots:
  telegram:
    interested_event_types:
      - dag.run.failed
      - dag.run.succeeded
`, map[string]string{
			"DAGU_BOTS_TELEGRAM_INTERESTED_EVENT_TYPES": "dag.run.running,dag.run.queued",
		})

		assert.Equal(t, []string{"dag.run.running", "dag.run.queued"}, cfg.Bots.Telegram.InterestedEventTypes)
	})

	t.Run("slack env can clear configured list", func(t *testing.T) {
		cfg := loadWithEnv(t, `
bots:
  slack:
    interested_event_types:
      - dag.run.failed
      - dag.run.succeeded
`, map[string]string{
			"DAGU_BOTS_SLACK_INTERESTED_EVENT_TYPES": "",
		})

		assert.Empty(t, cfg.Bots.Slack.InterestedEventTypes)
	})

	t.Run("discord env overrides config", func(t *testing.T) {
		cfg := loadWithEnv(t, `
bots:
  discord:
    interested_event_types:
      - dag.run.failed
      - dag.run.succeeded
`, map[string]string{
			"DAGU_BOTS_DISCORD_INTERESTED_EVENT_TYPES": "dag.run.running,dag.run.queued",
		})

		assert.Equal(t, []string{"dag.run.running", "dag.run.queued"}, cfg.Bots.Discord.InterestedEventTypes)
	})
}

func TestLoad_DiscordBotEnvOnlyConfig(t *testing.T) {
	cfg := loadWithEnv(t, "# empty", map[string]string{
		"DAGU_BOTS_PROVIDER":                    "discord",
		"DAGU_BOTS_DISCORD_TOKEN":               "discord-token",
		"DAGU_BOTS_DISCORD_ALLOWED_CHANNEL_IDS": "chan-1,chan-2",
		"DAGU_BOTS_DISCORD_RESPOND_TO_ALL":      "false",
	})

	assert.Equal(t, BotProviderDiscord, cfg.Bots.Provider)
	assert.Equal(t, "discord-token", cfg.Bots.Discord.Token)
	assert.Equal(t, []string{"chan-1", "chan-2"}, cfg.Bots.Discord.AllowedChannelIDs)
	assert.False(t, cfg.Bots.Discord.RespondToAll)
	assert.Equal(t, DefaultBotInterestedEventTypes, cfg.Bots.Discord.InterestedEventTypes)
}

func TestLoad_Monitoring(t *testing.T) {
	t.Run("FromYAML", func(t *testing.T) {
		cfg := loadFromYAML(t, `
monitoring:
  retention: "24h"
  interval: "10s"
`)
		assert.Equal(t, 24*time.Hour, cfg.Monitoring.Retention)
		assert.Equal(t, 10*time.Second, cfg.Monitoring.Interval)
	})

	t.Run("FromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_MONITORING_RETENTION": "30m",
			"DAGU_MONITORING_INTERVAL":  "15s",
		})
		assert.Equal(t, 30*time.Minute, cfg.Monitoring.Retention)
		assert.Equal(t, 15*time.Second, cfg.Monitoring.Interval)
	})

	t.Run("Default", func(t *testing.T) {
		cfg := loadFromYAML(t, "")
		assert.Equal(t, 24*time.Hour, cfg.Monitoring.Retention)
		assert.Equal(t, 5*time.Second, cfg.Monitoring.Interval)
	})
}

func TestLoad_SecretsConfig(t *testing.T) {
	t.Run("FromYAML", func(t *testing.T) {
		cfg := loadFromYAML(t, `
secrets:
  vault:
    address: "https://vault.example.com"
    token: "yaml-token"
  kubernetes:
    namespace: "secret-ns"
    kubeconfig: "relative/kubeconfig"
    context: "prod"
`)

		assert.Equal(t, "https://vault.example.com", cfg.Secrets.Vault.Address)
		assert.Equal(t, "yaml-token", cfg.Secrets.Vault.Token)
		assert.Equal(t, "secret-ns", cfg.Secrets.Kubernetes.Namespace)
		assert.Equal(t, resolvedTestPath(t, "relative/kubeconfig"), cfg.Secrets.Kubernetes.Kubeconfig)
		assert.Equal(t, "prod", cfg.Secrets.Kubernetes.Context)
	})

	t.Run("FromEnv", func(t *testing.T) {
		kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_SECRETS_VAULT_ADDRESS":         "https://vault.example.com",
			"DAGU_SECRETS_VAULT_TOKEN":           "env-token",
			"DAGU_SECRETS_KUBERNETES_NAMESPACE":  "env-ns",
			"DAGU_SECRETS_KUBERNETES_KUBECONFIG": kubeconfig,
			"DAGU_SECRETS_KUBERNETES_CONTEXT":    "env-context",
		})

		assert.Equal(t, "https://vault.example.com", cfg.Secrets.Vault.Address)
		assert.Equal(t, "env-token", cfg.Secrets.Vault.Token)
		assert.Equal(t, "env-ns", cfg.Secrets.Kubernetes.Namespace)
		assert.Equal(t, kubeconfig, cfg.Secrets.Kubernetes.Kubeconfig)
		assert.Equal(t, "env-context", cfg.Secrets.Kubernetes.Context)
	})

	t.Run("OldEnvNamesIgnored", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"VAULT_ADDR":       "https://legacy.example.com",
			"VAULT_TOKEN":      "legacy-token",
			"DAGU_VAULT_ADDR":  "https://legacy-dagu.example.com",
			"DAGU_VAULT_TOKEN": "legacy-dagu-token",
		})

		assert.Empty(t, cfg.Secrets.Vault.Address)
		assert.Empty(t, cfg.Secrets.Vault.Token)
	})

	t.Run("LoadsForServiceScopedCommands", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			service Service
		}{
			{name: "Server", service: ServiceServer},
			{name: "Scheduler", service: ServiceScheduler},
			{name: "Worker", service: ServiceWorker},
			{name: "Coordinator", service: ServiceCoordinator},
			{name: "Agent", service: ServiceAgent},
		} {
			t.Run(tc.name, func(t *testing.T) {
				configFile := filepath.Join(t.TempDir(), "config.yaml")
				err := os.WriteFile(configFile, []byte(`
secrets:
  vault:
    address: "https://vault.example.com"
    token: "scoped-token"
  kubernetes:
    namespace: "scoped-ns"
    kubeconfig: "relative/scoped-kubeconfig"
    context: "scoped-context"
`), 0600)
				require.NoError(t, err)

				cfg := testLoad(t, WithConfigFile(configFile), WithService(tc.service))

				assert.Equal(t, "https://vault.example.com", cfg.Secrets.Vault.Address)
				assert.Equal(t, "scoped-token", cfg.Secrets.Vault.Token)
				assert.Equal(t, "scoped-ns", cfg.Secrets.Kubernetes.Namespace)
				assert.Equal(t, resolvedTestPath(t, "relative/scoped-kubeconfig"), cfg.Secrets.Kubernetes.Kubeconfig)
				assert.Equal(t, "scoped-context", cfg.Secrets.Kubernetes.Context)
			})
		}
	})
}

func TestLoad_ProcConfig(t *testing.T) {
	t.Run("FromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_AUTH_MODE":                    "none",
			"DAGU_PROC_HEARTBEAT_INTERVAL":      "6s",
			"DAGU_PROC_HEARTBEAT_SYNC_INTERVAL": "9s",
			"DAGU_PROC_STALE_THRESHOLD":         "75s",
		})
		assert.Equal(t, 6*time.Second, cfg.Proc.HeartbeatInterval)
		assert.Equal(t, 9*time.Second, cfg.Proc.HeartbeatSyncInterval)
		assert.Equal(t, 75*time.Second, cfg.Proc.StaleThreshold)
	})

	t.Run("LegacySchedulerEnvFallback", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_AUTH_MODE":                         "none",
			"DAGU_SCHEDULER_HEARTBEAT_INTERVAL":      "8s",
			"DAGU_SCHEDULER_HEARTBEAT_SYNC_INTERVAL": "12s",
			"DAGU_SCHEDULER_STALE_THRESHOLD":         "100s",
		})
		assert.Equal(t, 8*time.Second, cfg.Proc.HeartbeatInterval)
		assert.Equal(t, 12*time.Second, cfg.Proc.HeartbeatSyncInterval)
		assert.Equal(t, 100*time.Second, cfg.Proc.StaleThreshold)
		require.Len(t, cfg.Warnings, 3)
		assert.Contains(t, cfg.Warnings[0], "scheduler.heartbeat_interval is deprecated")
		assert.Contains(t, cfg.Warnings[1], "scheduler.heartbeat_sync_interval is deprecated")
		assert.Contains(t, cfg.Warnings[2], "scheduler.stale_threshold is deprecated")
	})
}

func TestLoad_AuthMode(t *testing.T) {
	t.Run("AuthModeNone", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: "none"
`)
		assert.Equal(t, AuthModeNone, cfg.Server.Auth.Mode)
	})

	t.Run("AuthModeBuiltin", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: "builtin"
  builtin:
    admin:
      username: "admin"
      password: "secretpass123"
    token:
      secret: "my-jwt-secret-key"
      ttl: "12h"
`)
		assert.Equal(t, AuthModeBuiltin, cfg.Server.Auth.Mode)
		assert.Equal(t, "my-jwt-secret-key", cfg.Server.Auth.Builtin.Token.Secret)
		assert.Equal(t, 12*time.Hour, cfg.Server.Auth.Builtin.Token.TTL)
	})

	t.Run("AuthModeFromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_AUTH_MODE":         "builtin",
			"DAGU_AUTH_TOKEN_SECRET": "test-secret",
			"DAGU_PATHS_USERS_DIR":   t.TempDir(),
		})
		require.Equal(t, AuthModeBuiltin, cfg.Server.Auth.Mode)
	})

	t.Run("AuthModeDefaultBuiltin", func(t *testing.T) {
		cfg := loadFromYAML(t, "# empty")
		require.Equal(t, AuthModeBuiltin, cfg.Server.Auth.Mode)
	})

	t.Run("AuthModeInvalid", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: "invalid_mode"
`)
		require.Equal(t, AuthModeBuiltin, cfg.Server.Auth.Mode)
		require.Len(t, cfg.Warnings, 1)
		assert.Contains(t, cfg.Warnings[0], "Invalid auth.mode value")
		assert.Contains(t, cfg.Warnings[0], "invalid_mode")
	})
}

func TestLoad_AuthBuiltin(t *testing.T) {
	t.Run("FromYAML", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: "builtin"
  builtin:
    token:
      secret: "jwt-signing-secret"
      ttl: "24h"
`)
		assert.Equal(t, AuthModeBuiltin, cfg.Server.Auth.Mode)
		assert.Equal(t, "jwt-signing-secret", cfg.Server.Auth.Builtin.Token.Secret)
		assert.Equal(t, 24*time.Hour, cfg.Server.Auth.Builtin.Token.TTL)
	})

	t.Run("FromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_AUTH_MODE":         "builtin",
			"DAGU_AUTH_TOKEN_SECRET": "env-jwt-secret",
			"DAGU_AUTH_TOKEN_TTL":    "48h",
		})
		assert.Equal(t, AuthModeBuiltin, cfg.Server.Auth.Mode)
		assert.Equal(t, "env-jwt-secret", cfg.Server.Auth.Builtin.Token.Secret)
		assert.Equal(t, 48*time.Hour, cfg.Server.Auth.Builtin.Token.TTL)
	})

	t.Run("DefaultTTL", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: "builtin"
  builtin:
    token:
      secret: "secret"
`)
		assert.Equal(t, 24*time.Hour, cfg.Server.Auth.Builtin.Token.TTL)
	})
}

func TestLoad_AuthBuiltinInitialAdmin(t *testing.T) {
	t.Run("FromYAML", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: "builtin"
  builtin:
    token:
      secret: "jwt-signing-secret"
      ttl: "24h"
    initial_admin:
      username: "myadmin"
      password: "strongpass123"
`)
		assert.Equal(t, "myadmin", cfg.Server.Auth.Builtin.InitialAdmin.Username)
		assert.Equal(t, "strongpass123", cfg.Server.Auth.Builtin.InitialAdmin.Password)
		assert.True(t, cfg.Server.Auth.Builtin.InitialAdmin.IsConfigured())
	})

	t.Run("FromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_AUTH_MODE":                           "builtin",
			"DAGU_AUTH_TOKEN_SECRET":                   "env-jwt-secret",
			"DAGU_AUTH_BUILTIN_INITIAL_ADMIN_USERNAME": "envadmin",
			"DAGU_AUTH_BUILTIN_INITIAL_ADMIN_PASSWORD": "envpass12345",
		})
		assert.Equal(t, "envadmin", cfg.Server.Auth.Builtin.InitialAdmin.Username)
		assert.Equal(t, "envpass12345", cfg.Server.Auth.Builtin.InitialAdmin.Password)
	})

	t.Run("NotSet", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: "builtin"
  builtin:
    token:
      secret: "jwt-signing-secret"
      ttl: "24h"
`)
		assert.Equal(t, "", cfg.Server.Auth.Builtin.InitialAdmin.Username)
		assert.Equal(t, "", cfg.Server.Auth.Builtin.InitialAdmin.Password)
		assert.False(t, cfg.Server.Auth.Builtin.InitialAdmin.IsConfigured())
	})

	t.Run("WeakPasswordWarning", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: "builtin"
  builtin:
    token:
      secret: "a-strong-random-secret-value"
      ttl: "24h"
    initial_admin:
      username: "admin"
      password: "changeme"
`)
		assert.True(t, cfg.Server.Auth.Builtin.InitialAdmin.IsConfigured())
		found := false
		for _, w := range cfg.Warnings {
			if strings.Contains(w, "Initial admin password") {
				found = true
				break
			}
		}
		assert.True(t, found, "expected weak password warning, got warnings: %v", cfg.Warnings)
	})
}

func TestLoad_MetricsAccess(t *testing.T) {
	t.Run("MetricsAccessPublic", func(t *testing.T) {
		cfg := loadFromYAML(t, `
metrics: "public"
`)
		assert.Equal(t, MetricsAccessPublic, cfg.Server.Metrics)
	})

	t.Run("MetricsAccessPrivate", func(t *testing.T) {
		cfg := loadFromYAML(t, `
metrics: "private"
`)
		assert.Equal(t, MetricsAccessPrivate, cfg.Server.Metrics)
	})

	t.Run("MetricsAccessDefault", func(t *testing.T) {
		cfg := loadFromYAML(t, "# empty")
		assert.Equal(t, MetricsAccessPrivate, cfg.Server.Metrics)
	})

	t.Run("MetricsAccessFromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_SERVER_METRICS": "public",
		})
		assert.Equal(t, MetricsAccessPublic, cfg.Server.Metrics)
	})

	t.Run("MetricsAccessInvalid", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: none
metrics: "invalid_value"
`)
		assert.Equal(t, MetricsAccessPrivate, cfg.Server.Metrics)
		require.Len(t, cfg.Warnings, 1)
		assert.Contains(t, cfg.Warnings[0], "Invalid server.metrics value")
		assert.Contains(t, cfg.Warnings[0], "invalid_value")
	})
}

func TestLoad_AccessLogMode(t *testing.T) {
	t.Run("AccessLogAll", func(t *testing.T) {
		cfg := loadFromYAML(t, `
access_log_mode: "all"
`)
		assert.Equal(t, AccessLogAll, cfg.Server.AccessLog)
	})

	t.Run("AccessLogNonPublic", func(t *testing.T) {
		cfg := loadFromYAML(t, `
access_log_mode: "non-public"
`)
		assert.Equal(t, AccessLogNonPublic, cfg.Server.AccessLog)
	})

	t.Run("AccessLogNone", func(t *testing.T) {
		cfg := loadFromYAML(t, `
access_log_mode: "none"
`)
		assert.Equal(t, AccessLogNone, cfg.Server.AccessLog)
	})

	t.Run("AccessLogDefault", func(t *testing.T) {
		cfg := loadFromYAML(t, "# empty")
		assert.Equal(t, AccessLogAll, cfg.Server.AccessLog)
	})

	t.Run("AccessLogFromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_ACCESS_LOG_MODE": "non-public",
		})
		assert.Equal(t, AccessLogNonPublic, cfg.Server.AccessLog)
	})

	t.Run("AccessLogInvalid", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: none
access_log_mode: "invalid"
`)
		assert.Equal(t, AccessLogAll, cfg.Server.AccessLog)
		require.Len(t, cfg.Warnings, 1)
		assert.Contains(t, cfg.Warnings[0], "Invalid access_log_mode value")
		assert.Contains(t, cfg.Warnings[0], "invalid")
	})
}

func TestLoad_CacheConfig(t *testing.T) {
	t.Run("DefaultCacheMode", func(t *testing.T) {
		cfg := loadFromYAML(t, ``)
		assert.Equal(t, CacheModeNormal, cfg.Cache)
	})

	t.Run("CacheModeLow", func(t *testing.T) {
		cfg := loadFromYAML(t, `
cache: low
`)
		assert.Equal(t, CacheModeLow, cfg.Cache)
	})

	t.Run("CacheModeNormal", func(t *testing.T) {
		cfg := loadFromYAML(t, `
cache: normal
`)
		assert.Equal(t, CacheModeNormal, cfg.Cache)
	})

	t.Run("CacheModeHigh", func(t *testing.T) {
		cfg := loadFromYAML(t, `
cache: high
`)
		assert.Equal(t, CacheModeHigh, cfg.Cache)
	})

	t.Run("CacheModeInvalid", func(t *testing.T) {
		cfg := loadFromYAML(t, `
auth:
  mode: none
cache: invalid
`)
		assert.Equal(t, CacheModeNormal, cfg.Cache)
		require.Len(t, cfg.Warnings, 1)
		assert.Contains(t, cfg.Warnings[0], "Invalid cache mode")
	})

	t.Run("CacheModeFromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, ``, map[string]string{
			"DAGU_CACHE": "low",
		})
		assert.Equal(t, CacheModeLow, cfg.Cache)
	})
}

func TestLoad_WebhooksConfig(t *testing.T) {
	t.Run("DefaultMaxPayloadSize", func(t *testing.T) {
		cfg := loadFromYAML(t, ``)
		assert.Equal(t, DefaultWebhookMaxPayloadSize, cfg.Webhooks.MaxPayloadSize)
	})

	t.Run("MaxPayloadSizeFromYAML", func(t *testing.T) {
		cfg := loadFromYAML(t, `
webhooks:
  max_payload_size: 2097152
`)
		assert.Equal(t, 2097152, cfg.Webhooks.MaxPayloadSize)
	})

	t.Run("MaxPayloadSizeFromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, ``, map[string]string{
			"DAGU_WEBHOOKS_MAX_PAYLOAD_SIZE": "3145728",
		})
		assert.Equal(t, 3145728, cfg.Webhooks.MaxPayloadSize)
	})

	t.Run("EnvOverridesYAML", func(t *testing.T) {
		cfg := loadWithEnv(t, `
webhooks:
  max_payload_size: 2097152
`, map[string]string{
			"DAGU_WEBHOOKS_MAX_PAYLOAD_SIZE": "3145728",
		})
		assert.Equal(t, 3145728, cfg.Webhooks.MaxPayloadSize)
	})

	t.Run("RejectsNonPositiveMaxPayloadSize", func(t *testing.T) {
		err := loadWithErrorFromYAML(t, `
webhooks:
  max_payload_size: 0
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "webhooks.max_payload_size must be > 0")
	})
}

func TestParseCoordinatorAddresses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		input            any
		expectedAddrs    []string
		expectedWarnings int // Number of expected warnings
	}{
		// Valid string input tests
		{"empty string", "", nil, 0},
		{"single address string", "host:8080", []string{"host:8080"}, 0},
		{"multiple addresses string", "host1:8080,host2:9090", []string{"host1:8080", "host2:9090"}, 0},
		{"whitespace trimmed string", " host1:8080 , host2:9090 ", []string{"host1:8080", "host2:9090"}, 0},
		{"empty parts filtered", "host1:8080,,host2:9090", []string{"host1:8080", "host2:9090"}, 0},
		{"trailing comma", "host:8080,", []string{"host:8080"}, 0},
		{"leading comma", ",host:8080", []string{"host:8080"}, 0},
		{"IPv6 address", "[::1]:8080", []string{"[::1]:8080"}, 0},

		// Valid []interface{} input tests (YAML)
		{"empty interface slice", []any{}, nil, 0},
		{"interface slice with addresses", []any{"host1:8080", "host2:9090"}, []string{"host1:8080", "host2:9090"}, 0},
		{"interface slice filters non-strings", []any{"host:8080", 123, "host2:9090"}, []string{"host:8080", "host2:9090"}, 0},
		{"interface slice filters empty strings", []any{"host:8080", "", "host2:9090"}, []string{"host:8080", "host2:9090"}, 0},
		{"interface slice trims whitespace", []any{" host1:8080 ", " host2:9090 "}, []string{"host1:8080", "host2:9090"}, 0},

		// Valid []string input tests
		{"empty string slice", []string{}, nil, 0},
		{"string slice with addresses", []string{"host1:8080", "host2:9090"}, []string{"host1:8080", "host2:9090"}, 0},
		{"string slice trims whitespace", []string{" host1:8080 ", " host2:9090 "}, []string{"host1:8080", "host2:9090"}, 0},
		{"string slice filters empty", []string{"host:8080", "", "host2:9090"}, []string{"host:8080", "host2:9090"}, 0},

		// Edge cases
		{"nil input", nil, nil, 0},
		{"unsupported type int", 12345, nil, 0},
		{"unsupported type map", map[string]string{"key": "value"}, nil, 0},

		// Invalid addresses - should generate warnings
		{"missing port", "host", nil, 1},
		{"invalid port", "host:abc", nil, 1},
		{"port out of range", "host:70000", nil, 1},
		{"port zero", "host:0", nil, 1},
		{"empty host", ":8080", nil, 1},
		{"mixed valid and invalid", "host1:8080,invalid,host2:9090", []string{"host1:8080", "host2:9090"}, 1},
		{"all invalid", []string{"invalid1", "invalid2"}, nil, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, warnings := parseCoordinatorAddresses(tt.input)
			assert.Equal(t, tt.expectedAddrs, result)
			assert.Len(t, warnings, tt.expectedWarnings)
		})
	}
}

func TestLoad_Terminal(t *testing.T) {
	t.Run("TerminalDefault", func(t *testing.T) {
		cfg := loadFromYAML(t, "# empty")
		assert.False(t, cfg.Server.Terminal.Enabled)
		assert.Equal(t, 5, cfg.Server.Terminal.MaxSessions)
	})

	t.Run("TerminalEnabled", func(t *testing.T) {
		cfg := loadFromYAML(t, `
terminal:
  enabled: true
  max_sessions: 7
`)
		assert.True(t, cfg.Server.Terminal.Enabled)
		assert.Equal(t, 7, cfg.Server.Terminal.MaxSessions)
	})

	t.Run("TerminalDisabled", func(t *testing.T) {
		cfg := loadFromYAML(t, `
terminal:
  enabled: false
`)
		assert.False(t, cfg.Server.Terminal.Enabled)
	})

	t.Run("TerminalEnabledFromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_TERMINAL_ENABLED":      "true",
			"DAGU_TERMINAL_MAX_SESSIONS": "8",
		})
		assert.True(t, cfg.Server.Terminal.Enabled)
		assert.Equal(t, 8, cfg.Server.Terminal.MaxSessions)
	})

	t.Run("TerminalDisabledFromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_TERMINAL_ENABLED": "false",
		})
		assert.False(t, cfg.Server.Terminal.Enabled)
	})

	t.Run("TerminalEnvOverridesYAML", func(t *testing.T) {
		cfg := loadWithEnv(t, `
terminal:
  enabled: false
  max_sessions: 3
`, map[string]string{
			"DAGU_TERMINAL_ENABLED":      "true",
			"DAGU_TERMINAL_MAX_SESSIONS": "9",
		})
		assert.True(t, cfg.Server.Terminal.Enabled)
		assert.Equal(t, 9, cfg.Server.Terminal.MaxSessions)
	})
}

func TestLoad_CheckUpdates(t *testing.T) {
	t.Run("CheckUpdatesDefault", func(t *testing.T) {
		cfg := loadFromYAML(t, "# empty")
		assert.True(t, cfg.Server.CheckUpdates)
	})

	t.Run("CheckUpdatesDisabled", func(t *testing.T) {
		cfg := loadFromYAML(t, `
check_updates: false
`)
		assert.False(t, cfg.Server.CheckUpdates)
	})

	t.Run("CheckUpdatesEnabled", func(t *testing.T) {
		cfg := loadFromYAML(t, `
check_updates: true
`)
		assert.True(t, cfg.Server.CheckUpdates)
	})

	t.Run("CheckUpdatesDisabledFromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_CHECK_UPDATES": "false",
		})
		assert.False(t, cfg.Server.CheckUpdates)
	})

	t.Run("CheckUpdatesEnvOverridesYAML", func(t *testing.T) {
		cfg := loadWithEnv(t, `
check_updates: true
`, map[string]string{
			"DAGU_CHECK_UPDATES": "false",
		})
		assert.False(t, cfg.Server.CheckUpdates)
	})
}

func TestLoad_Audit(t *testing.T) {
	t.Run("AuditDefault", func(t *testing.T) {
		cfg := loadFromYAML(t, "# empty")
		assert.True(t, cfg.Server.Audit.Enabled)
	})

	t.Run("AuditEnabled", func(t *testing.T) {
		cfg := loadFromYAML(t, `
audit:
  enabled: true
`)
		assert.True(t, cfg.Server.Audit.Enabled)
	})

	t.Run("AuditDisabled", func(t *testing.T) {
		cfg := loadFromYAML(t, `
audit:
  enabled: false
`)
		assert.False(t, cfg.Server.Audit.Enabled)
	})

	t.Run("AuditEnabledFromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_AUDIT_ENABLED": "true",
		})
		assert.True(t, cfg.Server.Audit.Enabled)
	})

	t.Run("AuditDisabledFromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_AUDIT_ENABLED": "false",
		})
		assert.False(t, cfg.Server.Audit.Enabled)
	})

	t.Run("AuditEnvOverridesYAML", func(t *testing.T) {
		cfg := loadWithEnv(t, `
audit:
  enabled: true
`, map[string]string{
			"DAGU_AUDIT_ENABLED": "false",
		})
		assert.False(t, cfg.Server.Audit.Enabled)
	})
}

func TestLoad_Coordinator(t *testing.T) {
	t.Run("CoordinatorDefault", func(t *testing.T) {
		cfg := loadFromYAML(t, "# empty")
		assert.True(t, cfg.Coordinator.Enabled)
	})

	t.Run("CoordinatorDisabled", func(t *testing.T) {
		cfg := loadFromYAML(t, `
coordinator:
  enabled: false
`)
		assert.False(t, cfg.Coordinator.Enabled)
	})

	t.Run("CoordinatorDisabledFromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_COORDINATOR_ENABLED": "false",
		})
		assert.False(t, cfg.Coordinator.Enabled)
	})

	t.Run("CoordinatorEnvOverridesYAML", func(t *testing.T) {
		cfg := loadWithEnv(t, `
coordinator:
  enabled: true
`, map[string]string{
			"DAGU_COORDINATOR_ENABLED": "false",
		})
		assert.False(t, cfg.Coordinator.Enabled)
	})
}

func TestLoad_TunnelConfig(t *testing.T) {
	t.Run("TunnelDefault", func(t *testing.T) {
		cfg := loadFromYAML(t, "# empty")
		assert.False(t, cfg.Tunnel.Enabled)
		assert.Empty(t, cfg.Tunnel.Tailscale.AuthKey)
		assert.Empty(t, cfg.Tunnel.Tailscale.Hostname)
		assert.False(t, cfg.Tunnel.Tailscale.Funnel)
		assert.False(t, cfg.Tunnel.Tailscale.HTTPS)
	})

	t.Run("TunnelFromYAML", func(t *testing.T) {
		cfg := loadFromYAML(t, `
tunnel:
  enabled: true
  tailscale:
    auth_key: "tskey-yaml-test"
    hostname: "yaml-dagu"
    funnel: false
    https: true
    state_dir: "/var/dagu/tailscale"
  allow_terminal: true
  allowed_ips:
    - "192.168.1.0/24"
    - "10.0.0.0/8"
  rate_limiting:
    enabled: true
    login_attempts: 5
    window_seconds: 300
    block_duration_seconds: 900
`)
		assert.True(t, cfg.Tunnel.Enabled)
		assert.Equal(t, "tskey-yaml-test", cfg.Tunnel.Tailscale.AuthKey)
		assert.Equal(t, "yaml-dagu", cfg.Tunnel.Tailscale.Hostname)
		assert.False(t, cfg.Tunnel.Tailscale.Funnel)
		assert.True(t, cfg.Tunnel.Tailscale.HTTPS)
		assert.Equal(t, "/var/dagu/tailscale", cfg.Tunnel.Tailscale.StateDir)
		assert.True(t, cfg.Tunnel.AllowTerminal)
		assert.Equal(t, []string{"192.168.1.0/24", "10.0.0.0/8"}, cfg.Tunnel.AllowedIPs)
		assert.True(t, cfg.Tunnel.RateLimiting.Enabled)
		assert.Equal(t, 5, cfg.Tunnel.RateLimiting.LoginAttempts)
		assert.Equal(t, 300, cfg.Tunnel.RateLimiting.WindowSeconds)
		assert.Equal(t, 900, cfg.Tunnel.RateLimiting.BlockDurationSeconds)
	})

	t.Run("TunnelFromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_TUNNEL_ENABLED":                              "true",
			"DAGU_TUNNEL_TAILSCALE_AUTH_KEY":                   "tskey-env-test",
			"DAGU_TUNNEL_TAILSCALE_HOSTNAME":                   "env-dagu",
			"DAGU_TUNNEL_TAILSCALE_FUNNEL":                     "false",
			"DAGU_TUNNEL_TAILSCALE_HTTPS":                      "true",
			"DAGU_TUNNEL_ALLOW_TERMINAL":                       "true",
			"DAGU_TUNNEL_RATE_LIMITING_ENABLED":                "true",
			"DAGU_TUNNEL_RATE_LIMITING_LOGIN_ATTEMPTS":         "10",
			"DAGU_TUNNEL_RATE_LIMITING_WINDOW_SECONDS":         "600",
			"DAGU_TUNNEL_RATE_LIMITING_BLOCK_DURATION_SECONDS": "1800",
		})
		assert.True(t, cfg.Tunnel.Enabled)
		assert.Equal(t, "tskey-env-test", cfg.Tunnel.Tailscale.AuthKey)
		assert.Equal(t, "env-dagu", cfg.Tunnel.Tailscale.Hostname)
		assert.False(t, cfg.Tunnel.Tailscale.Funnel)
		assert.True(t, cfg.Tunnel.Tailscale.HTTPS)
		assert.True(t, cfg.Tunnel.AllowTerminal)
		assert.True(t, cfg.Tunnel.RateLimiting.Enabled)
		assert.Equal(t, 10, cfg.Tunnel.RateLimiting.LoginAttempts)
		assert.Equal(t, 600, cfg.Tunnel.RateLimiting.WindowSeconds)
		assert.Equal(t, 1800, cfg.Tunnel.RateLimiting.BlockDurationSeconds)
	})

	t.Run("TunnelEnvOverridesYAML", func(t *testing.T) {
		cfg := loadWithEnv(t, `
tunnel:
  enabled: true
  tailscale:
    auth_key: "yaml-key"
    hostname: "yaml-host"
    https: false
`, map[string]string{
			"DAGU_TUNNEL_TAILSCALE_AUTH_KEY": "env-key",
			"DAGU_TUNNEL_TAILSCALE_HTTPS":    "true",
		})
		assert.True(t, cfg.Tunnel.Enabled)
		assert.Equal(t, "env-key", cfg.Tunnel.Tailscale.AuthKey)
		assert.Equal(t, "yaml-host", cfg.Tunnel.Tailscale.Hostname)
		assert.True(t, cfg.Tunnel.Tailscale.HTTPS)
	})

	t.Run("TunnelDefaultHostname", func(t *testing.T) {
		cfg := loadFromYAML(t, `
tunnel:
  enabled: true
`)
		assert.True(t, cfg.Tunnel.Enabled)
		assert.Equal(t, AppSlug, cfg.Tunnel.Tailscale.Hostname)
	})

	t.Run("TunnelDefaultRateLimiting", func(t *testing.T) {
		cfg := loadFromYAML(t, `
tunnel:
  enabled: true
`)
		assert.Equal(t, 5, cfg.Tunnel.RateLimiting.LoginAttempts)
		assert.Equal(t, 300, cfg.Tunnel.RateLimiting.WindowSeconds)
		assert.Equal(t, 900, cfg.Tunnel.RateLimiting.BlockDurationSeconds)
	})

	t.Run("TunnelDisabledNoDefaults", func(t *testing.T) {
		cfg := loadFromYAML(t, `
tunnel:
  enabled: false
`)
		assert.False(t, cfg.Tunnel.Enabled)
		assert.Empty(t, cfg.Tunnel.Tailscale.Hostname)
	})
}

func TestLoad_DefaultExecutionMode(t *testing.T) {
	t.Run("DefaultIsLocal", func(t *testing.T) {
		cfg := loadFromYAML(t, "# empty")
		assert.Equal(t, ExecutionModeLocal, cfg.DefaultExecMode)
	})

	t.Run("SetToDistributed", func(t *testing.T) {
		cfg := loadFromYAML(t, `
default_execution_mode: distributed
`)
		assert.Equal(t, ExecutionModeDistributed, cfg.DefaultExecMode)
	})

	t.Run("SetToLocal", func(t *testing.T) {
		cfg := loadFromYAML(t, `
default_execution_mode: local
`)
		assert.Equal(t, ExecutionModeLocal, cfg.DefaultExecMode)
	})

	t.Run("FromEnv", func(t *testing.T) {
		cfg := loadWithEnv(t, "# empty", map[string]string{
			"DAGU_DEFAULT_EXECUTION_MODE": "distributed",
		})
		assert.Equal(t, ExecutionModeDistributed, cfg.DefaultExecMode)
	})

	t.Run("InvalidValue", func(t *testing.T) {
		err := loadWithErrorFromYAML(t, `
default_execution_mode: invalid
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid default_execution_mode")
	})
}
