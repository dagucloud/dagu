// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"cmp"
	"fmt"
	"net/http"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
)

// httpErrorCode is the error Chrome shows for an HTTP error answer without
// a body.
const httpErrorCode = "ERR_HTTP_RESPONSE_CODE_FAILURE"

// botCheckNames names the bot checks a refusal reports.
var botCheckNames = map[string]string{browserhost.BotCheckCloudflare: "Cloudflare"}

// refusal is a site answering with a refusal or a bot check in place of
// the page the browser asked for.
type refusal struct {
	Host   string `json:"host"`
	Status int    `json:"status"`
	// BotCheck names the check the site answered with, or is empty.
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
		return fmt.Sprintf("%s answered with a %s bot check (HTTP %d) in place of the page",
			r.Host, cmp.Or(botCheckNames[r.BotCheck], r.BotCheck), r.Status)
	}
	return fmt.Sprintf("%s refused the page (HTTP %d %s)", r.Host, r.Status, http.StatusText(r.Status))
}
