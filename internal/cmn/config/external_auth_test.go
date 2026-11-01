// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func validExternalAuthConfig() *Config {
	cfg := validBaseConfig()
	cfg.Server.Auth = Auth{
		Mode: AuthModeNone,
		External: AuthExternal{
			Enabled: true,
			Token: ExternalIdentityToken{
				Header:         "X-Identity-Token",
				Issuer:         "https://gateway.example.org",
				Audience:       "dagu",
				MaxAge:         time.Minute,
				PublicKeysFile: "/etc/dagu/gateway-keys.pem",
			},
			AuthZEN: ExternalAuthZEN{
				BaseURL: "https://pdp.example.org",
				Timeout: time.Second,
			},
		},
	}
	return cfg
}

func TestConfigValidateExternalAuth(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validExternalAuthConfig().Validate())
	})

	t.Run("DisabledIgnoresIncompleteBlock", func(t *testing.T) {
		t.Parallel()
		cfg := validBaseConfig()
		cfg.Server.Auth.Mode = AuthModeNone
		cfg.Server.Auth.External.Token.Header = "X-Identity-Token"
		require.NoError(t, cfg.Validate())
	})

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "RejectsBuiltinMode",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Mode = AuthModeBuiltin
				cfg.Server.Auth.Builtin.Token = TokenConfig{Secret: "secret", TTL: time.Hour}
				cfg.Paths.UsersDir = "/tmp/users"
			},
			wantErr: "auth.external.enabled requires auth.mode to be none",
		},
		{
			name: "RejectsBasicMode",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Mode = AuthModeBasic
				cfg.Server.Auth.Basic = AuthBasic{Username: "u", Password: "p"}
			},
			wantErr: "auth.external.enabled requires auth.mode to be none",
		},
		{
			name:    "RejectsMissingHeader",
			mutate:  func(cfg *Config) { cfg.Server.Auth.External.Token.Header = "" },
			wantErr: "auth.external.token.header is required",
		},
		{
			name:    "RejectsReservedHeader",
			mutate:  func(cfg *Config) { cfg.Server.Auth.External.Token.Header = "Cookie" },
			wantErr: "auth.external.token.header must not use the reserved header",
		},
		{
			name:    "RejectsMissingIssuer",
			mutate:  func(cfg *Config) { cfg.Server.Auth.External.Token.Issuer = "" },
			wantErr: "auth.external.token.issuer is required",
		},
		{
			name:    "RejectsMissingAudience",
			mutate:  func(cfg *Config) { cfg.Server.Auth.External.Token.Audience = "" },
			wantErr: "auth.external.token.audience is required",
		},
		{
			name:    "RejectsZeroMaxAge",
			mutate:  func(cfg *Config) { cfg.Server.Auth.External.Token.MaxAge = 0 },
			wantErr: "auth.external.token.max_age must be a positive duration",
		},
		{
			name:    "RejectsMissingKeysFile",
			mutate:  func(cfg *Config) { cfg.Server.Auth.External.Token.PublicKeysFile = "" },
			wantErr: "auth.external.token.public_keys_file is required",
		},
		{
			name:    "RejectsMissingBaseURL",
			mutate:  func(cfg *Config) { cfg.Server.Auth.External.AuthZEN.BaseURL = "" },
			wantErr: "auth.external.authzen.base_url is required",
		},
		{
			name:    "RejectsBaseURLScheme",
			mutate:  func(cfg *Config) { cfg.Server.Auth.External.AuthZEN.BaseURL = "ftp://pdp.example.org" },
			wantErr: "auth.external.authzen.base_url must use http or https",
		},
		{
			name:    "RejectsBaseURLQuery",
			mutate:  func(cfg *Config) { cfg.Server.Auth.External.AuthZEN.BaseURL = "https://pdp.example.org?x=1" },
			wantErr: "without credentials, query or fragment",
		},
		{
			name:    "RejectsZeroTimeout",
			mutate:  func(cfg *Config) { cfg.Server.Auth.External.AuthZEN.Timeout = 0 },
			wantErr: "auth.external.authzen.timeout must be a positive duration",
		},
		{
			name:    "RejectsNegativeCacheTTL",
			mutate:  func(cfg *Config) { cfg.Server.Auth.External.AuthZEN.CacheTTL = -time.Second },
			wantErr: "auth.external.authzen.cache_ttl must not be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := validExternalAuthConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLoadExternalAuthDefaults(t *testing.T) {
	cfg := loadFromYAML(t, `
auth:
  mode: none
  external:
    enabled: false
`)

	external := cfg.Server.Auth.External
	require.False(t, external.Enabled)
	require.Equal(t, 60*time.Second, external.Token.MaxAge)
	require.Equal(t, 2*time.Second, external.AuthZEN.Timeout)
	require.Zero(t, external.AuthZEN.CacheTTL)
}

func TestLoadExternalAuthFromYAML(t *testing.T) {
	cfg := loadFromYAML(t, `
auth:
  mode: none
  external:
    enabled: true
    token:
      header: X-Identity-Token
      issuer: https://gateway.example.org
      audience: dagu
      max_age: 30s
      public_keys_file: /etc/dagu/gateway-keys.pem
    authzen:
      base_url: https://pdp.example.org
      timeout: 500ms
      cache_ttl: 5s
      token_file: /run/secrets/pdp-token
`)

	require.Equal(t, AuthExternal{
		Enabled: true,
		Token: ExternalIdentityToken{
			Header:         "X-Identity-Token",
			Issuer:         "https://gateway.example.org",
			Audience:       "dagu",
			MaxAge:         30 * time.Second,
			PublicKeysFile: "/etc/dagu/gateway-keys.pem",
		},
		AuthZEN: ExternalAuthZEN{
			BaseURL:   "https://pdp.example.org",
			Timeout:   500 * time.Millisecond,
			CacheTTL:  5 * time.Second,
			TokenFile: "/run/secrets/pdp-token",
		},
	}, cfg.Server.Auth.External)
}

func TestLoadExternalAuthRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "UnknownKey",
			spec: `
auth:
  mode: none
  external:
    token:
      max_agee: 30s
`,
			wantErr: "max_agee",
		},
		{
			name: "UnknownAuthZENKey",
			spec: `
auth:
  mode: none
  external:
    authzen:
      endpoint: https://pdp.example.org/access/v1/evaluation
`,
			wantErr: "endpoint",
		},
		{
			name: "MalformedDuration",
			spec: `
auth:
  mode: none
  external:
    authzen:
      cache_ttl: five
`,
			wantErr: "auth.external.authzen.cache_ttl must be a duration",
		},
		{
			name: "NonStringDuration",
			spec: `
auth:
  mode: none
  external:
    token:
      max_age: 60
`,
			wantErr: "invalid auth.external config",
		},
		{
			name: "BuiltinMode",
			spec: `
auth:
  mode: builtin
  builtin:
    token:
      secret: a-long-random-secret-for-tests
  external:
    enabled: true
    token:
      header: X-Identity-Token
      issuer: https://gateway.example.org
      audience: dagu
      public_keys_file: /etc/dagu/gateway-keys.pem
    authzen:
      base_url: https://pdp.example.org
`,
			wantErr: "auth.external.enabled requires auth.mode to be none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := loadWithErrorFromYAML(t, tt.spec)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
