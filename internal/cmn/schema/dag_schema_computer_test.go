// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDAGSchemaComputer(t *testing.T) {
	t.Parallel()
	const source = `
llm:
  provider: anthropic
  model: claude-opus-5
worker_selector:
  desktop: finance
steps:
  - id: total
    action: computer.extract
    with:
      instruction: The invoice total
      mode: generic
      schema:
        type: object
        properties:
          total: {type: number}
  - id: post
    action: computer.run
    with:
      mode: native
      ai: on_miss
      max_actions: 40
      on_confirmation: allow
      screenshots: each
      variables:
        password: secret
      do:
        - launch: notepad.exe
        - launch: {command: open, args: [-a, TextEdit]}
        - act: Log in with %password%
          when: A login form is visible
        - act: {instruction: Open billing, cache: false, max_actions: 10}
          timeout: 2m
        - act: {instruction: Save it, ai: never}
        - ask: {prompt: Enter the code, as: otp, timeout: 10m}
        - expect: The invoice is posted
        - expect: {statement: A document number is shown, within: 30s}
        - expect: {text: Posted, within: 10s}
        - expect: {window: "経費精算*"}
        - wait: 1s
          when: {element: {role: button, name: 保存, in: {role: group, name: 支払情報}}}
        - wait: 1s
          when: {element: {role: text_field, near: {label: 金額, side: right}, nth: 0}}
        - extract:
            instruction: The document number
            ai: every_run
            schema: {type: object, properties: {doc: {type: string}}}
        - wait: 2s
        - screenshot: posted
`
	resolved := mustResolveDAGSchema(t)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, source)))
	for _, tc := range []struct{ name, from, to string }{
		{"extract without schema", "      schema:\n        type: object\n        properties:\n          total: {type: number}\n", ""},
		{"run without operations", "      do:\n", "      steps:\n"},
		{"two operations in one item", "        - screenshot: posted", "        - {screenshot: posted, wait: 1s}"},
		{"browser operation", "        - wait: 2s", "        - goto: https://example.com"},
		{"unknown mode", "mode: native", "mode: fast"},
		{"unknown ai", "ai: on_miss", "ai: sometimes"},
		{"unknown ai on act", "ai: never", "ai: always"},
		{"unknown confirmation policy", "on_confirmation: allow", "on_confirmation: ask"},
		{"zero max actions", "max_actions: 40", "max_actions: 0"},
		{"launch without command", "{command: open, args: [-a, TextEdit]}", "{args: [-a, TextEdit]}"},
		{"two check keys", "{text: Posted, within: 10s}", "{text: Posted, window: App}"},
		{"statement and text", "{statement: A document number is shown, within: 30s}", "{statement: Shown, text: Posted}"},
		{"only within", "{text: Posted, within: 10s}", "{within: 10s}"},
		{"element without role", "{role: button, name: 保存, in: {role: group, name: 支払情報}}", "{name: 保存}"},
		{"container without role", "in: {role: group, name: 支払情報}", "in: {name: 支払情報}"},
		{"element role alone", "{role: text_field, near: {label: 金額, side: right}, nth: 0}", "{role: text_field}"},
		{"unknown selector key", "{role: button, name: 保存, in: {role: group, name: 支払情報}}", "{role: button, title: 保存}"},
		{"bad side", "side: right", "side: beside"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := mustParseYAMLDocument(t, strings.Replace(source, tc.from, tc.to, 1))
			require.Error(t, resolved.Validate(doc))
		})
	}
}
