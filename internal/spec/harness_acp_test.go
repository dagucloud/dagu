// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"maps"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"

	"github.com/stretchr/testify/require"
)

// TestACPDefinition checks protocol selection and incompatible CLI prompt options.
func TestACPDefinition(t *testing.T) {
	def, err := parseHarnessDefinition("agent", map[string]any{"binary": "adapter", "protocol": "acp", "prefix_args": []any{"--acp"}})
	require.NoError(t, err)
	require.Equal(t, ir.HarnessProtocolACP, def.Protocol)
	require.Equal(t, []string{"--acp"}, def.PrefixArgs)
	for _, raw := range []map[string]any{
		{"binary": "adapter", "protocol": "http"},
		{"binary": "adapter", "protocol": true},
		{"binary": "adapter", "protocol": "acp", "prompt_mode": "stdin"},
		{"binary": "adapter", "protocol": "acp", "prompt_flag": "-p"},
		{"binary": "adapter", "protocol": "acp", "prompt_position": "after_flags"},
	} {
		_, err := parseHarnessDefinition("agent", raw)
		require.Error(t, err)
	}
}

// TestACPRemoteVariables accepts $VAR references that resolve only at run time.
func TestACPRemoteVariables(t *testing.T) {
	raw := map[string]any{"protocol": "acp", "transport": "websocket", "url": "$ACP_URL", "cwd": "$AGENT_ROOT/work"}
	def, err := parseHarnessDefinition("remote", raw)
	require.NoError(t, err)
	require.Equal(t, "$ACP_URL", def.URL)
	require.Equal(t, "$AGENT_ROOT/work", def.Cwd)
}

// TestACPRemoteDefinition checks remote endpoints without requiring a local binary.
func TestACPRemoteDefinition(t *testing.T) {
	raw := map[string]any{"protocol": "acp", "transport": "websocket", "url": "wss://agent.example/acp", "cwd": "/remote/workspace", "headers": map[string]any{"Authorization": "Bearer ${ACP_TOKEN}"}}
	def, err := parseHarnessDefinition("remote", raw)
	require.NoError(t, err)
	require.Empty(t, def.Binary)
	require.Equal(t, ir.HarnessTransportWebSocket, def.Transport)
	require.Equal(t, "Bearer ${ACP_TOKEN}", def.Headers["Authorization"])
	for key, value := range map[string]any{"ca_file": "/private/ca.pem", "bearer_subprotocol": true} {
		withConnectionOption := maps.Clone(raw)
		withConnectionOption[key] = value
		_, err := parseHarnessDefinition("remote", withConnectionOption)
		require.NoError(t, err)
	}
	httpConfig := maps.Clone(raw)
	httpConfig["transport"], httpConfig["url"] = "http", "https://agent.example/acp"
	_, err = parseHarnessDefinition("remote", httpConfig)
	require.NoError(t, err)
	for _, tc := range []struct {
		key   string
		value any
	}{
		{"transport", "grpc"}, {"transport", true}, {"protocol", "cli"},
		{"url", "https://agent.example/acp"}, {"url", "ws:///missing-host"}, {"url", "wss://user:password@agent.example/acp"},
		{"url", ""}, {"cwd", ""}, {"cwd", "relative/path"},
		{"binary", "adapter"}, {"prefix_args", []any{}}, {"flag_style", "gnu_long"},
		{"headers", map[string]any{"Authorization": true}},
		{"ca_file", false}, {"ca_file", ""}, {"bearer_subprotocol", "true"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			invalid := make(map[string]any, len(raw))
			maps.Copy(invalid, raw)
			invalid[tc.key] = tc.value
			_, err := parseHarnessDefinition("remote", invalid)
			require.Error(t, err)
		})
	}
	for _, cwd := range []string{`C:\agent\workspace`, `\\agent\workspace`, "${REMOTE_CWD}"} {
		raw["cwd"] = cwd
		_, err := parseHarnessDefinition("remote", raw)
		require.NoError(t, err)
	}
}
