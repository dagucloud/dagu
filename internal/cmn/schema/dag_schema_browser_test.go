// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDAGSchemaBrowser(t *testing.T) {
	t.Parallel()
	const source = `
llm:
  provider: anthropic
  model: claude-sonnet-5
steps:
  - id: hn
    action: browser.extract
    with:
      url: https://news.ycombinator.com
      instruction: The top stories
      schema:
        type: object
        properties:
          stories: {type: array}
  - id: invoice
    action: browser.run
    with:
      url: https://portal.example.com
      cache: true
      browser:
        headless: true
        viewport: {width: 1280, height: 800}
        allowed_domains: [portal.example.com]
        screenshots: each
        profile: vendor
      variables:
        user: alice
      do:
        - act: Sign in as %user%
          when: A login form is visible
        - act: {instruction: Open billing, cache: false}
          timeout: 30s
        - ask: {prompt: Enter the code, as: otp, timeout: 10m}
        - expect: The billing page lists an invoice
        - expect: {text: Invoice}
          when: {url: /billing}
        - act: Open the invoice
          when: {selector: "#invoice", within: 5s}
        - extract:
            instruction: The latest invoice
            schema: {type: object, properties: {invoice_number: {type: string}}}
        - wait: {selector: "#done"}
        - wait: {duration: 2s}
        - screenshot: billing
        - goto: https://portal.example.com/logout
`
	resolved := mustResolveDAGSchema(t)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, source)))
	for _, tc := range []struct{ name, from, to string }{
		{"extract without schema", "      schema:\n        type: object\n        properties:\n          stories: {type: array}\n", ""},
		{"run without operations", "      do:\n", "      steps:\n"},
		{"two operations in one item", "        - goto: https://portal.example.com/logout", "        - {goto: https://x.test, act: Click}"},
		{"wait with both forms", `{selector: "#done"}`, `{selector: "#done", duration: 1s}`},
		{"non-object extract schema", "{type: object, properties: {invoice_number: {type: string}}}", "{type: array}"},
		{"unknown screenshot policy", "screenshots: each", "screenshots: sometimes"},
		{"invalid variable name", "        user: alice", "        user-name: alice"},
		{"unknown browser option", "headless: true", "stealth: true"},
		{"condition with two checks", "expect: {text: Invoice}", "expect: {text: Invoice, url: /x}"},
		{"unknown condition check", "when: {url: /billing}", "when: {title: Billing}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := mustParseYAMLDocument(t, strings.Replace(source, tc.from, tc.to, 1))
			require.Error(t, resolved.Validate(doc))
		})
	}
}
