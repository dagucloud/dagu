// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"os"
	"regexp"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A page counts as refused when the site answered with a bot check, or the
// page shows one in a frame, or the site said the page is forbidden or asked
// for too often, with or without a body. The sentence names the check.
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
		"AWS WAF challenge": {
			page:    pageOutcome{Host: "api.example.com", Status: 202, BotCheck: browserhost.BotCheckAWSWAFChallenge},
			message: "api.example.com answered with an AWS WAF challenge (HTTP 202) in place of the page",
		},
		"AWS WAF CAPTCHA": {
			page:    pageOutcome{Host: "store.example.com", Status: 405, BotCheck: browserhost.BotCheckAWSWAFCaptcha},
			message: "store.example.com answered with an AWS WAF CAPTCHA (HTTP 405) in place of the page",
		},
		"reCAPTCHA frame": {
			page:    pageOutcome{Host: "portal.example.com", Status: 200, Title: "Sign in", BotCheck: browserhost.BotCheckRecaptcha},
			message: "portal.example.com shows a reCAPTCHA challenge (HTTP 200)",
		},
		"hCaptcha frame": {
			page:    pageOutcome{Host: "portal.example.com", Status: 200, BotCheck: browserhost.BotCheckHCaptcha},
			message: "portal.example.com shows an hCaptcha challenge (HTTP 200)",
		},
		"Turnstile frame": {
			page:    pageOutcome{Host: "portal.example.com", Status: 200, BotCheck: browserhost.BotCheckTurnstile},
			message: "portal.example.com shows a Cloudflare Turnstile challenge (HTTP 200)",
		},
		"unknown check": {
			page:    pageOutcome{Host: "shop.example.com", Status: 403, BotCheck: "datadome"},
			message: "shop.example.com answered with a datadome bot check (HTTP 403) in place of the page",
		},
	} {
		refused, ok := refusalOf(tc.page)
		assert.Equal(t, tc.message != "", ok, name)
		if ok {
			assert.Equal(t, tc.message, refused.String(), name)
		}
	}
}

// Every bot check browserhost tells apart has its own sentence, so none is
// described by the fallback for an unknown kind.
func TestBotCheckPhrasesCoverKinds(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("../../../browserhost/responses.go")
	require.NoError(t, err)
	kinds := regexp.MustCompile(`(?m)^\s*BotCheck\w+\s*=\s*"([^"]+)"`).FindAllStringSubmatch(string(source), -1)
	require.NotEmpty(t, kinds)
	for _, kind := range kinds {
		assert.Contains(t, botCheckPhrases, kind[1])
	}
}
