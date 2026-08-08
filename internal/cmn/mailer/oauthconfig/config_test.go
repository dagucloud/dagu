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
