// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauthconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateStructure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "Microsoft",
			config: Config{
				Provider: ProviderMicrosoft, TenantID: "tenant", ClientID: "client", ClientSecret: "secret",
			},
		},
		{
			name: "GoogleServiceAccount",
			config: Config{
				Provider: ProviderGoogleServiceAccount, ServiceAccountJSON: "{}",
			},
		},
		{
			name: "GoogleRefresh",
			config: Config{
				Provider: ProviderGoogleRefresh, ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, ValidateStructure(&tt.config))
		})
	}

	assert.Error(t, ValidateStructure(&Config{Provider: ProviderMicrosoft}))
	assert.Error(t, ValidateStructure(&Config{
		Provider: ProviderMicrosoft, TenantID: "tenant", ClientID: "client", ClientSecret: "secret", RefreshToken: "mixed",
	}))
	assert.Error(t, ValidateStructure(&Config{Provider: "other"}))
}

func TestValidateMailAccount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name:   "GoogleRefresh",
			config: Config{Provider: ProviderGoogleRefresh, ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh"},
		},
		{
			name:   "MicrosoftRefreshWithoutSecretOrTenant",
			config: Config{Provider: ProviderMicrosoftRefresh, ClientID: "client", RefreshToken: "refresh"},
		},
		{
			name: "MicrosoftRefreshWithSecretAndTenant",
			config: Config{
				Provider: ProviderMicrosoftRefresh, TenantID: "tenant", ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh",
			},
		},
		{
			name:    "MissingRefreshToken",
			config:  Config{Provider: ProviderMicrosoftRefresh, ClientID: "client"},
			wantErr: "oauth.refresh_token is required",
		},
		{
			name:    "GoogleRefreshWithTenant",
			config:  Config{Provider: ProviderGoogleRefresh, TenantID: "tenant", ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh"},
			wantErr: `oauth.tenant_id is not valid for provider "google_refresh"`,
		},
		{
			name: "MicrosoftRefreshWithScopes",
			config: Config{
				Provider: ProviderMicrosoftRefresh, ClientID: "client", RefreshToken: "refresh",
				Scopes: []string{"https://outlook.office.com/IMAP.AccessAsUser.All"},
			},
		},
		{
			// Google cannot change the scope of a refreshed token.
			name: "GoogleRefreshWithScopes",
			config: Config{
				Provider: ProviderGoogleRefresh, ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh",
				Scopes: []string{"https://mail.google.com/"},
			},
			wantErr: `oauth.scopes is not valid for provider "google_refresh"`,
		},
		{
			name:    "EmptyScope",
			config:  Config{Provider: ProviderMicrosoftRefresh, ClientID: "client", RefreshToken: "refresh", Scopes: []string{" "}},
			wantErr: "oauth.scopes must not contain an empty scope",
		},
		{
			// Client-credential providers suit SMTP notifications, not mailboxes.
			name:    "SMTPOnlyProvider",
			config:  Config{Provider: ProviderMicrosoft, TenantID: "tenant", ClientID: "client", ClientSecret: "secret"},
			wantErr: "oauth.provider must be google_refresh or microsoft_refresh",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateMailAccount(&tt.config)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestValidateStructureRejectsScopes(t *testing.T) {
	t.Parallel()

	err := ValidateStructure(&Config{
		Provider: ProviderGoogleRefresh, ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh",
		Scopes: []string{"https://mail.google.com/"},
	})
	require.EqualError(t, err, `scopes is not valid for SMTP OAuth provider "google_refresh"`)
}

func TestValidateStructureRejectsMicrosoftRefresh(t *testing.T) {
	t.Parallel()

	err := ValidateStructure(&Config{Provider: ProviderMicrosoftRefresh, ClientID: "client", RefreshToken: "refresh"})
	require.EqualError(t, err, `unsupported SMTP OAuth provider "microsoft_refresh"`)
}
