// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/assert"
)

// A page counts as refused when the site answered with a bot check, or said
// the page is forbidden or asked for too often, with or without a body.
func TestRefusalOf(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		page    pageOutcome
		message string
	}{
		"bot check": {
			page:    pageOutcome{Host: "shop.example.com", Status: 403, BotCheck: browserhost.BotCheckCloudflare},
			message: "shop.example.com answered with a Cloudflare bot check (HTTP 403) in place of the page",
		},
		"forbidden": {
			page:    pageOutcome{Host: "portal.example.com", Status: 403, Title: "Access denied"},
			message: "portal.example.com refused the page (HTTP 403 Forbidden)",
		},
		"too many requests": {
			page:    pageOutcome{Host: "api.example.com", Status: 429},
			message: "api.example.com refused the page (HTTP 429 Too Many Requests)",
		},
		"forbidden without a body": {
			page:    pageOutcome{Code: httpErrorCode, Host: "portal.example.com", Status: 403},
			message: "portal.example.com refused the page (HTTP 403 Forbidden)",
		},
		"not found":               {page: pageOutcome{Host: "shop.example.com", Status: 404}},
		"page":                    {page: pageOutcome{Host: "shop.example.com", Status: 200}},
		"unreachable":             {page: pageOutcome{Code: "ERR_CONNECTION_REFUSED", Host: "shop.example.com"}},
		"no host to name":         {page: pageOutcome{Status: 403}},
		"bot check of any status": {page: pageOutcome{Host: "shop.example.com", Status: 200, BotCheck: browserhost.BotCheckCloudflare}, message: "shop.example.com answered with a Cloudflare bot check (HTTP 200) in place of the page"},
	} {
		refused, ok := refusalOf(tc.page)
		assert.Equal(t, tc.message != "", ok, name)
		if ok {
			assert.Equal(t, tc.message, refused.String(), name)
		}
	}
}
