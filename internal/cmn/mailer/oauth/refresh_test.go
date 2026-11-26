// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRefreshTokenFuncRejectsSMTPOnlyProvider(t *testing.T) {
	t.Parallel()

	_, err := NewRefreshTokenFunc("ops@example.com", &oauthconfig.Config{
		Provider: oauthconfig.ProviderMicrosoft, TenantID: "tenant", ClientID: "client", ClientSecret: "secret",
	})
	require.EqualError(t, err, "oauth.provider must be google_refresh or microsoft_refresh")
}

func TestMicrosoftMailScopes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"https://outlook.office.com/.default", "offline_access"},
		microsoftMailScopes(oauthconfig.Config{}))
	assert.Equal(t,
		[]string{"https://outlook.office.com/IMAP.AccessAsUser.All", "https://outlook.office.com/SMTP.Send", "offline_access"},
		microsoftMailScopes(oauthconfig.Config{Scopes: []string{
			"https://outlook.office.com/IMAP.AccessAsUser.All", "https://outlook.office.com/SMTP.Send",
		}}))
}
