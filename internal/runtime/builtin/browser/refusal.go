// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"fmt"
	"net/http"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
)

// httpErrorCode is the error Chrome shows for an HTTP error answer without
// a body.
const httpErrorCode = "ERR_HTTP_RESPONSE_CODE_FAILURE"

// challengeFrame is a check known by the address of the frame that shows
// it: one at Host, or a subdomain of it, under Path.
type challengeFrame struct {
	Kind string `json:"kind"`
	Host string `json:"host"`
	Path string `json:"path"`
}

// challengeFrames lists the frames known to show a check.
var challengeFrames = []challengeFrame{
	{Kind: browserhost.BotCheckRecaptcha, Host: "www.google.com", Path: "/recaptcha/"},
	{Kind: browserhost.BotCheckRecaptcha, Host: "recaptcha.net", Path: "/recaptcha/"},
	{Kind: browserhost.BotCheckHCaptcha, Host: "hcaptcha.com", Path: "/"},
	{Kind: browserhost.BotCheckTurnstile, Host: "challenges.cloudflare.com", Path: "/"},
}

// botCheckPhrases says, by kind, how a page shows a bot check, with the
// page's HTTP status.
var botCheckPhrases = map[string]string{
	browserhost.BotCheckCloudflare:      "answered with a Cloudflare bot check (HTTP %d) in place of the page",
	browserhost.BotCheckAWSWAFChallenge: "answered with an AWS WAF challenge (HTTP %d) in place of the page",
	browserhost.BotCheckAWSWAFCaptcha:   "answered with an AWS WAF CAPTCHA (HTTP %d) in place of the page",
	browserhost.BotCheckRecaptcha:       "shows a reCAPTCHA challenge (HTTP %d)",
	browserhost.BotCheckHCaptcha:        "shows an hCaptcha challenge (HTTP %d)",
	browserhost.BotCheckTurnstile:       "shows a Cloudflare Turnstile challenge (HTTP %d)",
}

// refusal is a site answering with a refusal or a bot check in place of
// the page the browser asked for, or a page showing a bot check.
type refusal struct {
	Host   string `json:"host"`
	Status int    `json:"status"`
	// BotCheck names the check the site answered with or the page shows,
	// or is empty.
	BotCheck string `json:"botCheck,omitempty"`
}

// refusalOf returns the refusal page shows, if it shows one: a bot check,
// or an answer that the page is forbidden or asked for too often.
func refusalOf(page pageOutcome) (refusal, bool) {
	refused := page.BotCheck != "" ||
		(page.Status == http.StatusForbidden || page.Status == http.StatusTooManyRequests) && (page.Code == "" || page.Code == httpErrorCode)
	if !refused || page.Host == "" {
		return refusal{}, false
	}
	return refusal{Host: page.Host, Status: page.Status, BotCheck: page.BotCheck}, true
}

func (r refusal) String() string {
	if r.BotCheck != "" {
		phrase, ok := botCheckPhrases[r.BotCheck]
		if !ok {
			phrase = fmt.Sprintf("answered with a %s bot check (HTTP %%d) in place of the page", r.BotCheck)
		}
		return r.Host + " " + fmt.Sprintf(phrase, r.Status)
	}
	return fmt.Sprintf("%s refused the page (HTTP %d %s)", r.Host, r.Status, http.StatusText(r.Status))
}
