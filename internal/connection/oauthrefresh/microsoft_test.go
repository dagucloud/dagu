// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauthrefresh

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMicrosoftTokenURL(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "https://login.microsoftonline.com/common/oauth2/v2.0/token", MicrosoftTokenURL(""))
	assert.Equal(t, "https://login.microsoftonline.com/contoso/oauth2/v2.0/token", MicrosoftTokenURL("contoso"))
}

func TestMicrosoftScopes(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		[]string{"https://outlook.office.com/IMAP.AccessAsUser.All", "https://outlook.office.com/SMTP.Send", "offline_access"},
		MicrosoftScopes([]string{
			" https://outlook.office.com/IMAP.AccessAsUser.All", "https://outlook.office.com/SMTP.Send ",
		}))
	assert.Equal(t, []string{"offline_access", "https://outlook.office.com/IMAP.AccessAsUser.All"},
		MicrosoftScopes([]string{"offline_access", "https://outlook.office.com/IMAP.AccessAsUser.All"}),
		"offline_access is not added twice")
}
