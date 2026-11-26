// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauthrefresh

import (
	"net/url"
	"slices"
	"strings"
)

// offlineAccessScope keeps a refreshed Microsoft token refreshable.
const offlineAccessScope = "offline_access"

// MicrosoftScopes returns the scopes for a Microsoft refresh request: the
// given scopes, trimmed, with offline_access added when missing.
func MicrosoftScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes)+1)
	for _, scope := range scopes {
		out = append(out, strings.TrimSpace(scope))
	}
	if !slices.Contains(out, offlineAccessScope) {
		out = append(out, offlineAccessScope)
	}
	return out
}

// MicrosoftTokenURL returns the Microsoft identity platform token endpoint
// for tenant, or the common endpoint when tenant is empty.
func MicrosoftTokenURL(tenant string) string {
	if tenant == "" {
		tenant = "common"
	}
	return "https://login.microsoftonline.com/" + url.PathEscape(tenant) + "/oauth2/v2.0/token"
}
