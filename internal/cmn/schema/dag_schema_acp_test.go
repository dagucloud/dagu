// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDAGSchemaACP keeps schema protocol validation aligned with the YAML parser.
func TestDAGSchemaACP(t *testing.T) {
	const source = `harnesses:
  adapter:
    binary: adapter
    protocol: acp
steps:
  - action: harness.run
    with:
      provider: adapter
      prompt: hello
`
	resolved := mustResolveDAGSchema(t)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, source)))
	for _, replacement := range []string{"protocol: http", "protocol: true", "protocol: acp\n    prompt_mode: arg", "protocol: acp\n    prompt_flag: -p", "protocol: acp\n    prompt_position: after_flags"} {
		require.Error(t, resolved.Validate(mustParseYAMLDocument(t, strings.Replace(source, "protocol: acp", replacement, 1))))
	}
}

// TestDAGSchemaACPRemote validates mutually exclusive local and remote settings.
func TestDAGSchemaACPRemote(t *testing.T) {
	const source = `harnesses:
  adapter:
    protocol: acp
    transport: websocket
    url: wss://agent.example/acp
    cwd: /remote/workspace
    headers:
      Authorization: Bearer ${ACP_TOKEN}
steps:
  - action: harness.run
    with:
      provider: adapter
      prompt: hello
`
	resolved := mustResolveDAGSchema(t)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, source)))
	httpSource := strings.ReplaceAll(strings.ReplaceAll(source, "websocket", "http"), "wss://", "https://")
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, httpSource)))
	for _, option := range []string{"ca_file: /private/ca.pem", "bearer_subprotocol: true"} {
		withOption := strings.Replace(source, "transport: websocket", "transport: websocket\n    "+option, 1)
		require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, withOption)))
	}
	for _, tc := range []struct{ from, to string }{
		{"protocol: acp", "protocol: cli"},
		{"transport: websocket", "transport: grpc"},
		{"    cwd: /remote/workspace\n", ""},
		{"    url: wss://agent.example/acp\n", ""},
		{"transport: websocket", "transport: websocket\n    binary: adapter"},
		{"transport: websocket", "transport: websocket\n    prefix_args: []"},
		{"transport: websocket", "transport: websocket\n    flag_style: gnu_long"},
		{"transport: websocket", "transport: stdio\n    binary: adapter"},
		{"transport: websocket", "transport: websocket\n    ca_file: false"},
		{"transport: websocket", "transport: websocket\n    bearer_subprotocol: enabled"},
		{"transport: websocket", "transport: http\n    bearer_subprotocol: true"},
	} {
		require.Error(t, resolved.Validate(mustParseYAMLDocument(t, strings.Replace(source, tc.from, tc.to, 1))))
	}
}
