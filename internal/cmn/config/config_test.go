// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validBaseConfig returns a minimal valid Config for use in tests.
// Callers can mutate the returned config to set up specific test scenarios.
func validBaseConfig() *Config {
	return &Config{
		DefaultExecMode: ExecutionModeLocal,
		Server: Server{
			Port: 8080,
			Auth: Auth{Mode: AuthModeNone},
			Terminal: TerminalConfig{
				MaxSessions: 5,
			},
			SSE: SSEConfig{
				MaxTopicsPerConnection: 20,
				MaxClients:             1000,
				HeartbeatInterval:      10 * time.Second,
			},
		},
		Webhooks: WebhooksConfig{
			MaxPayloadSize: DefaultWebhookMaxPayloadSize,
		},
		UI: UI{MaxDashboardPageLimit: 100},
	}
}

func TestConfig_Validate(t *testing.T) {
	t.Parallel()
	t.Run("ValidConfig", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("InvalidWebhookMaxPayloadSize", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Webhooks.MaxPayloadSize = 0
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "webhooks.max_payload_size must be > 0")
	})

	t.Run("InvalidPort_Negative", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Port = -1
		cfg.Server.Auth = Auth{}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid port number")
	})

	t.Run("InvalidPort_TooLarge", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Port = 99999
		cfg.Server.Auth = Auth{}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid port number")
	})

	t.Run("InvalidPort_MaxValue", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Port = 65536
		cfg.Server.Auth = Auth{}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid port number")
	})

	t.Run("ValidPort_MinValue", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Port = 0
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("ValidPort_MaxValue", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Port = 65535
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("NegativeSchedulerFailureThreshold", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Scheduler.FailureThreshold = -1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scheduler.failure_threshold must be >= 0")
	})

	t.Run("InvalidCoordinatorHealthPort", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Coordinator.HealthPort = -1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid coordinator.health_port")
	})

	t.Run("InvalidCoordinatorHealthPortCollision", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Coordinator.Port = 50055
		cfg.Coordinator.HealthPort = 50055
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "coordinator.port and coordinator.health_port must be different")
	})

	t.Run("ValidCoordinatorHealthPortDisabled", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Coordinator.Port = 50055
		cfg.Coordinator.HealthPort = 0
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("InvalidWorkerHealthPort", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Worker.HealthPort = 65536
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid worker.health_port")
	})

	t.Run("IncompleteTLS_MissingCert", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.TLS = &TLSConfig{
			KeyFile: "/path/to/key.pem",
		}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "TLS configuration incomplete")
	})

	t.Run("IncompleteTLS_MissingKey", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.TLS = &TLSConfig{
			CertFile: "/path/to/cert.pem",
		}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "TLS configuration incomplete")
	})

	t.Run("CompleteTLS", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.TLS = &TLSConfig{
			CertFile: "/path/to/cert.pem",
			KeyFile:  "/path/to/key.pem",
		}
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("InvalidMaxDashboardPageLimit_Zero", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.UI.MaxDashboardPageLimit = 0
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid max dashboard page limit")
	})

	t.Run("InvalidSSEHeartbeatInterval_Zero", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.SSE.HeartbeatInterval = 0

		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sse.heartbeat_interval must be > 0")
	})

	t.Run("InvalidSSEMaxClients_Zero", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.SSE.MaxClients = 0

		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sse.max_clients must be > 0")
	})

	t.Run("InvalidSSEMaxTopicsPerConnection_Zero", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.SSE.MaxTopicsPerConnection = 0

		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sse.max_topics_per_connection must be > 0")
	})

	t.Run("InvalidTerminalMaxSessions_Zero", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Terminal.MaxSessions = 0

		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "terminal.max_sessions must be > 0")
	})

	t.Run("InvalidProcHeartbeatInterval", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Proc.HeartbeatInterval = 10 * time.Second
		cfg.Proc.StaleThreshold = 10 * time.Second

		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proc.heartbeat_interval")
	})

	t.Run("InvalidMaxDashboardPageLimit_Negative", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.UI.MaxDashboardPageLimit = -1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid max dashboard page limit")
	})

	t.Run("ValidMaxDashboardPageLimit_MinValue", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("BuiltinAuth_MissingUsersDir", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 1},
			},
		}
		cfg.Paths.UsersDir = ""
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "users_dir")
	})

	t.Run("BuiltinAuth_ValidConfig", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 1},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("BuiltinAuth_SkippedForOtherModes", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("InvalidAuthMode", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth.Mode = "invalid"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid auth mode")
	})

	t.Run("BuiltinAuth_InvalidTokenTTL", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 0},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "positive token TTL")
	})

	t.Run("BuiltinAuth_MaxTokenTTL", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 365 * 24 * time.Hour},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		require.NoError(t, cfg.Validate())
	})

	t.Run("BuiltinAuth_TokenTTLAboveMaximum", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 365*24*time.Hour + time.Second},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not exceed 8760h (365 days)")
	})

	t.Run("BuiltinAuth_InitialAdmin_BothSet", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token:        TokenConfig{Secret: "secret", TTL: 1},
				InitialAdmin: InitialAdmin{Username: "admin", Password: "strongpass123"},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("BuiltinAuth_InitialAdmin_UsernameOnly", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token:        TokenConfig{Secret: "secret", TTL: 1},
				InitialAdmin: InitialAdmin{Username: "admin"},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "both username and password")
	})

	t.Run("BuiltinAuth_InitialAdmin_PasswordOnly", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token:        TokenConfig{Secret: "secret", TTL: 1},
				InitialAdmin: InitialAdmin{Password: "strongpass123"},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "both username and password")
	})

	t.Run("BuiltinAuth_InitialAdmin_NeitherSet", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 1},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("BuiltinAuth_InitialAdmin_IgnoredForOtherModes", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeNone,
			Builtin: AuthBuiltin{
				InitialAdmin: InitialAdmin{Username: "admin"},
			},
		}
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.NoError(t, err)
	})

	// Tests for incomplete OIDC config - with auto-detection, missing required fields
	// means OIDC is simply not enabled (no error). This is intentional.
	t.Run("BuiltinAuth_OIDC_IncompleteConfig_NoError", func(t *testing.T) {
		t.Parallel()
		// Missing clientId - OIDC is not enabled, no error
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 1},
			},
			OIDC: AuthOIDC{
				ClientID:     "", // Missing
				ClientSecret: "secret",
				ClientURL:    "https://example.com",
				Issuer:       "https://issuer.com",
				RoleMapping:  OIDCRoleMapping{DefaultRole: "viewer"},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.NoError(t, err, "incomplete OIDC config should not error - OIDC is simply not enabled")
		assert.False(t, cfg.Server.Auth.OIDC.IsConfigured(), "OIDC should not be configured")
	})

	t.Run("BuiltinAuth_OIDC_InvalidDefaultRole", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 1},
			},
			OIDC: AuthOIDC{
				ClientID:     "client-id",
				ClientSecret: "secret",
				ClientURL:    "https://example.com",
				Issuer:       "https://issuer.com",
				RoleMapping:  OIDCRoleMapping{DefaultRole: "invalid-role"},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "defaultRole")
	})

	t.Run("BuiltinAuth_OIDC_ValidConfig", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 1},
			},
			OIDC: AuthOIDC{
				ClientID:     "client-id",
				ClientSecret: "secret",
				ClientURL:    "https://example.com",
				Issuer:       "https://issuer.com",
				Scopes:       []string{"openid", "profile", "email"},
				RoleMapping:  OIDCRoleMapping{DefaultRole: "viewer"},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("BuiltinAuth_OIDC_MissingEmailScope_AddsWarning", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 1},
			},
			OIDC: AuthOIDC{
				ClientID:     "client-id",
				ClientSecret: "secret",
				ClientURL:    "https://example.com",
				Issuer:       "https://issuer.com",
				Scopes:       []string{"openid", "profile"}, // No email scope
				RoleMapping:  OIDCRoleMapping{DefaultRole: "viewer"},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.NoError(t, err)
		assert.Len(t, cfg.Warnings, 1)
		assert.Contains(t, cfg.Warnings[0], "email")
	})

	t.Run("BuiltinAuth_OIDC_MissingEmailScope_WithWhitelist_ReturnsError", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 1},
			},
			OIDC: AuthOIDC{
				ClientID:     "client-id",
				ClientSecret: "secret",
				ClientURL:    "https://example.com",
				Issuer:       "https://issuer.com",
				Scopes:       []string{"openid", "profile"}, // No email scope
				Whitelist:    []string{"user@example.com"},  // But whitelist is set
				RoleMapping:  OIDCRoleMapping{DefaultRole: "viewer"},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.UI.MaxDashboardPageLimit = 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "email")
	})

	t.Run("BuiltinAuth_OIDC_AllValidRoles", func(t *testing.T) {
		t.Parallel()
		validRoles := []string{"admin", "manager", "developer", "operator", "viewer"}
		for _, role := range validRoles {
			cfg := validBaseConfig()
			cfg.Server.Auth = Auth{
				Mode: AuthModeBuiltin,
				Builtin: AuthBuiltin{
					Token: TokenConfig{Secret: "secret", TTL: 1},
				},
				OIDC: AuthOIDC{
					ClientID:     "client-id",
					ClientSecret: "secret",
					ClientURL:    "https://example.com",
					Issuer:       "https://issuer.com",
					Scopes:       []string{"openid", "email"},
					RoleMapping:  OIDCRoleMapping{DefaultRole: role},
				},
			}
			cfg.Paths.UsersDir = "/tmp/users"
			cfg.UI.MaxDashboardPageLimit = 1
			err := cfg.Validate()
			require.NoError(t, err, "role %s should be valid", role)
		}
	})

	// Tunnel validation tests
	t.Run("TunnelPublicRequiresAuth", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Tunnel = TunnelConfig{
			Enabled: true,
			Tailscale: TailscaleTunnelConfig{
				Funnel: true, // Public access
			},
		}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires authentication")
	})

	t.Run("TunnelPublicWithAuthOK", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 1},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		cfg.Tunnel = TunnelConfig{
			Enabled: true,
			Tailscale: TailscaleTunnelConfig{
				Funnel: true, // Public access
			},
		}
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("TunnelPrivateNoAuthOK", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Tunnel = TunnelConfig{
			Enabled: true,
			Tailscale: TailscaleTunnelConfig{
				Funnel: false, // Private (tailnet only)
			},
		}
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("TunnelDisabledNoValidation", func(t *testing.T) {
		t.Parallel()
		// When tunnel is disabled, validation should pass regardless of settings
		cfg := validBaseConfig()
		cfg.Tunnel = TunnelConfig{
			Enabled: false,
			Tailscale: TailscaleTunnelConfig{
				Funnel: true, // Would require auth if enabled
			},
		}
		err := cfg.Validate()
		require.NoError(t, err)
	})
}

func TestConfigValidateIPAccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		configure func(*Config)
		wantError string
	}{
		{
			name: "ValidAddressesAndNetworks",
			configure: func(cfg *Config) {
				cfg.Server.IPAccess = IPAccessConfig{
					AllowedIPs:     []string{"203.0.113.10", "10.0.0.0/8", "2001:db8::/32"},
					TrustedProxies: []string{"127.0.0.1", "::1"},
				}
			},
		},
		{
			name: "InvalidAllowedIP",
			configure: func(cfg *Config) {
				cfg.Server.IPAccess.AllowedIPs = []string{"not-an-ip"}
			},
			wantError: `ip_access.allowed_ips[0] "not-an-ip"`,
		},
		{
			name: "IPv4MappedNetworkBelowMappedPrefix",
			configure: func(cfg *Config) {
				cfg.Server.IPAccess.AllowedIPs = []string{"::ffff:192.0.2.0/95"}
			},
			wantError: `ip_access.allowed_ips[0] "::ffff:192.0.2.0/95"`,
		},
		{
			name: "InvalidTrustedProxy",
			configure: func(cfg *Config) {
				cfg.Server.IPAccess.TrustedProxies = []string{"10.0.0.0/99"}
			},
			wantError: `ip_access.trusted_proxies[0] "10.0.0.0/99"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := validBaseConfig()
			tt.configure(cfg)

			err := cfg.Validate()
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestConfig_ValidateOIDCWorkspaceMappings(t *testing.T) {
	t.Parallel()

	newConfig := func() *Config {
		cfg := validBaseConfig()
		cfg.Server.Auth = Auth{
			Mode: AuthModeBuiltin,
			Builtin: AuthBuiltin{
				Token: TokenConfig{Secret: "secret", TTL: 1},
			},
			OIDC: AuthOIDC{
				ClientID:     "client-id",
				ClientSecret: "secret",
				ClientURL:    "https://example.com",
				Issuer:       "https://issuer.example.com",
				Scopes:       []string{"openid", "email"},
				RoleMapping: OIDCRoleMapping{
					DefaultRole:            "viewer",
					DefaultWorkspaceAccess: OIDCDefaultWorkspaceAccessAll,
				},
			},
		}
		cfg.Paths.UsersDir = "/tmp/users"
		return cfg
	}

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		cfg := newConfig()
		cfg.Server.Auth.OIDC.RoleMapping.WorkspaceMappings = map[string][]OIDCWorkspaceGrant{
			"team-a": {
				{Workspace: "payments", Role: "manager"},
				{Workspace: "infra", Role: "developer"},
				{Workspace: "operations", Role: "operator"},
				{Workspace: "reports", Role: "viewer"},
			},
			"team-b": {{Workspace: "payments", Role: "viewer"}},
		}

		require.NoError(t, cfg.Validate())
	})

	t.Run("AllowsOmittedDefaultWithoutMappings", func(t *testing.T) {
		t.Parallel()
		cfg := newConfig()
		cfg.Server.Auth.OIDC.RoleMapping.DefaultWorkspaceAccess = ""

		require.NoError(t, cfg.Validate())
	})

	t.Run("RequiresExplicitDefaultWithMappings", func(t *testing.T) {
		t.Parallel()
		cfg := newConfig()
		cfg.Server.Auth.OIDC.RoleMapping.DefaultWorkspaceAccess = ""
		cfg.Server.Auth.OIDC.RoleMapping.WorkspaceMappings = map[string][]OIDCWorkspaceGrant{
			"team": {{Workspace: "payments", Role: "viewer"}},
		}

		err := cfg.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "defaultWorkspaceAccess must be explicitly set")
	})

	t.Run("InvalidMappingWithIncompleteOIDC", func(t *testing.T) {
		t.Parallel()
		cfg := newConfig()
		cfg.Server.Auth.OIDC.ClientID = ""
		cfg.Server.Auth.OIDC.RoleMapping.WorkspaceMappings = map[string][]OIDCWorkspaceGrant{
			"team": {{Workspace: "bad/name", Role: "viewer"}},
		}

		err := cfg.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "workspace")
	})

	tests := []struct {
		name    string
		mapping OIDCRoleMapping
		wantErr string
	}{
		{
			name: "InvalidDefaultWorkspaceAccess",
			mapping: OIDCRoleMapping{
				DefaultRole:            "viewer",
				DefaultWorkspaceAccess: "restricted",
			},
			wantErr: "defaultWorkspaceAccess",
		},
		{
			name: "BlankGroup",
			mapping: OIDCRoleMapping{
				DefaultRole:            "viewer",
				DefaultWorkspaceAccess: OIDCDefaultWorkspaceAccessAll,
				WorkspaceMappings: map[string][]OIDCWorkspaceGrant{
					"  ": {{Workspace: "payments", Role: "viewer"}},
				},
			},
			wantErr: "blank group",
		},
		{
			name: "EmptyGrantList",
			mapping: OIDCRoleMapping{
				DefaultRole:            "viewer",
				DefaultWorkspaceAccess: OIDCDefaultWorkspaceAccessAll,
				WorkspaceMappings: map[string][]OIDCWorkspaceGrant{
					"team": {},
				},
			},
			wantErr: "at least one grant",
		},
		{
			name: "InvalidWorkspace",
			mapping: OIDCRoleMapping{
				DefaultRole:            "viewer",
				DefaultWorkspaceAccess: OIDCDefaultWorkspaceAccessAll,
				WorkspaceMappings: map[string][]OIDCWorkspaceGrant{
					"team": {{Workspace: "bad/name", Role: "viewer"}},
				},
			},
			wantErr: "workspace",
		},
		{
			name: "ReservedWorkspace",
			mapping: OIDCRoleMapping{
				DefaultRole:            "viewer",
				DefaultWorkspaceAccess: OIDCDefaultWorkspaceAccessAll,
				WorkspaceMappings: map[string][]OIDCWorkspaceGrant{
					"team": {{Workspace: "global", Role: "viewer"}},
				},
			},
			wantErr: "reserved",
		},
		{
			name: "DuplicateWorkspaceWithinGroup",
			mapping: OIDCRoleMapping{
				DefaultRole:            "viewer",
				DefaultWorkspaceAccess: OIDCDefaultWorkspaceAccessAll,
				WorkspaceMappings: map[string][]OIDCWorkspaceGrant{
					"team": {
						{Workspace: "payments", Role: "viewer"},
						{Workspace: "payments", Role: "operator"},
					},
				},
			},
			wantErr: "duplicate workspace",
		},
		{
			name: "InvalidRole",
			mapping: OIDCRoleMapping{
				DefaultRole:            "viewer",
				DefaultWorkspaceAccess: OIDCDefaultWorkspaceAccessAll,
				WorkspaceMappings: map[string][]OIDCWorkspaceGrant{
					"team": {{Workspace: "payments", Role: "owner"}},
				},
			},
			wantErr: "invalid role",
		},
		{
			name: "AdminRole",
			mapping: OIDCRoleMapping{
				DefaultRole:            "viewer",
				DefaultWorkspaceAccess: OIDCDefaultWorkspaceAccessAll,
				WorkspaceMappings: map[string][]OIDCWorkspaceGrant{
					"team": {{Workspace: "payments", Role: "admin"}},
				},
			},
			wantErr: "must not be admin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := newConfig()
			cfg.Server.Auth.OIDC.RoleMapping = tt.mapping

			err := cfg.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestOIDCRoleMapping_WorkspaceAccessPolicyActive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mapping OIDCRoleMapping
		want    bool
	}{
		{
			name: "DefaultAll",
			mapping: OIDCRoleMapping{
				DefaultWorkspaceAccess: OIDCDefaultWorkspaceAccessAll,
			},
		},
		{
			name: "ExplicitNone",
			mapping: OIDCRoleMapping{
				DefaultWorkspaceAccess: OIDCDefaultWorkspaceAccessNone,
			},
			want: true,
		},
		{
			name: "Mappings",
			mapping: OIDCRoleMapping{
				DefaultWorkspaceAccess: OIDCDefaultWorkspaceAccessAll,
				WorkspaceMappings: map[string][]OIDCWorkspaceGrant{
					"team": {{Workspace: "payments", Role: "viewer"}},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.mapping.WorkspaceAccessPolicyActive())
		})
	}
}

func TestConfig_ValidateRemoteNodes(t *testing.T) {
	t.Parallel()

	t.Run("ValidBasicAuth", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.RemoteNodes = []RemoteNode{{
			Name:              "node1",
			APIBaseURL:        "http://example.com/api/v1",
			AuthType:          "basic",
			BasicAuthUsername: "user",
			BasicAuthPassword: "pass",
		}}
		require.NoError(t, cfg.Validate())
	})

	t.Run("ValidTokenAuth", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.RemoteNodes = []RemoteNode{{
			Name:       "node1",
			APIBaseURL: "http://example.com/api/v1",
			AuthType:   "token",
			AuthToken:  "tok-123",
		}}
		require.NoError(t, cfg.Validate())
	})

	t.Run("ValidNoAuth", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.RemoteNodes = []RemoteNode{{
			Name:       "node1",
			APIBaseURL: "http://example.com/api/v1",
			AuthType:   "none",
		}}
		require.NoError(t, cfg.Validate())
	})

	t.Run("EmptyAuthTypeDefaultsToNone", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.RemoteNodes = []RemoteNode{{
			Name:       "node1",
			APIBaseURL: "http://example.com/api/v1",
		}}
		require.NoError(t, cfg.Validate())
	})

	t.Run("MissingAPIBaseURL", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.RemoteNodes = []RemoteNode{{
			Name: "node1",
		}}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "api_base_url is required")
		assert.Contains(t, err.Error(), "node1")
	})

	t.Run("InvalidAuthType", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.RemoteNodes = []RemoteNode{{
			Name:       "node1",
			APIBaseURL: "http://example.com/api/v1",
			AuthType:   "invalid",
		}}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid auth_type")
	})

	t.Run("BasicAuthMissingUsername", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.RemoteNodes = []RemoteNode{{
			Name:              "node1",
			APIBaseURL:        "http://example.com/api/v1",
			AuthType:          "basic",
			BasicAuthPassword: "pass",
		}}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "basic auth requires")
	})

	t.Run("BasicAuthMissingPassword", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.RemoteNodes = []RemoteNode{{
			Name:              "node1",
			APIBaseURL:        "http://example.com/api/v1",
			AuthType:          "basic",
			BasicAuthUsername: "user",
		}}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "basic auth requires")
	})

	t.Run("TokenAuthMissingToken", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.RemoteNodes = []RemoteNode{{
			Name:       "node1",
			APIBaseURL: "http://example.com/api/v1",
			AuthType:   "token",
		}}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token auth requires auth_token")
	})

	t.Run("MultipleNodes_OneInvalid", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.RemoteNodes = []RemoteNode{
			{Name: "good", APIBaseURL: "http://example.com/api/v1"},
			{Name: "bad", AuthType: "token"}, // missing api_base_url
		}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad")
	})

	t.Run("EmptyNameSkipped", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.RemoteNodes = []RemoteNode{{
			Name: "", // empty name -> skipped
		}}
		require.NoError(t, cfg.Validate())
	})
}

func TestFindQueueConfig(t *testing.T) {
	t.Parallel()

	t.Run("QueuesDisabled", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Queues: Queues{
				Enabled: false,
				Config:  []QueueConfig{{Name: "q1", MaxActiveRuns: 3}},
			},
		}
		assert.Nil(t, cfg.FindQueueConfig("q1"))
	})

	t.Run("NilConfig", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Queues: Queues{Enabled: true, Config: nil},
		}
		assert.Nil(t, cfg.FindQueueConfig("q1"))
	})

	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Queues: Queues{
				Enabled: true,
				Config: []QueueConfig{
					{Name: "a", MaxActiveRuns: 2},
					{Name: "b", MaxActiveRuns: 4},
				},
			},
		}
		assert.Nil(t, cfg.FindQueueConfig("c"))
	})

	t.Run("Found", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Queues: Queues{
				Enabled: true,
				Config: []QueueConfig{
					{Name: "a", MaxActiveRuns: 2},
					{Name: "b", MaxActiveRuns: 4},
				},
			},
		}
		result := cfg.FindQueueConfig("a")
		require.NotNil(t, result)
		assert.Equal(t, "a", result.Name)
		assert.Equal(t, 2, result.MaxActiveRuns)
	})

}
