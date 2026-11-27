// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

func TestCollectConnections(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
steps:
  - id: unread
    action: mcp.call
    with: {connection: work-google, server: gmail, tool: search_threads}
  - id: events
    action: mcp.call
    with: {connection: work-google, server: calendar, tool: list_events}
  - id: again
    action: mcp.call
    with: {connection: work-google, server: gmail, tool: get_thread}
  - id: each
    foreach:
      items: [a, b]
      steps:
        - id: item
          action: mcp.call
          with: {connection: ops-mail, tool: search}
  - id: plain
    action: mcp.call
    with: {url: "https://mcp.example.com/mcp", tool: t}
handler_on:
  failure:
    action: mcp.call
    with: {connection: alerts, server: chat, tool: send}
`))
	require.NoError(t, err)
	assert.Equal(t, []ir.ConnectionRef{
		{Name: "work-google", Servers: []string{"gmail", "calendar"}},
		{Name: "ops-mail"},
		{Name: "alerts", Servers: []string{"chat"}},
	}, dag.Connections)
}

func TestConnectionNameValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		with    string
		wantErr string
	}{
		{name: "Param", with: "connection: ${params.conn}", wantErr: "steps.call.with.connection"},
		{name: "Variable", with: "connection: $CONN", wantErr: "must be a literal name, not a reference"},
		{name: "Uppercase", with: "connection: Work-Google", wantErr: "must be up to 63 lowercase letters"},
		{name: "LeadingHyphen", with: `connection: "-ops"`, wantErr: "must be up to 63 lowercase letters"},
		{name: "TooLong", with: "connection: " + strings.Repeat("a", 64), wantErr: "must be up to 63 lowercase letters"},
		// The executor's config schema rejects a value that is not a string.
		{name: "NotAString", with: "connection: {name: x}", wantErr: `/properties/connection: type: map[name:x] has type "object", want "string"`},
		{name: "ServerReference", with: "connection: work-google\n      server: ${SERVER}", wantErr: "steps.call.with.server"},
		{name: "ServerWithoutConnection", with: "url: https://mcp.example.com/mcp\n      server: gmail", wantErr: "with.server requires with.connection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(context.Background(), []byte(
				"steps:\n  - id: call\n    action: mcp.call\n    with:\n      tool: t\n      "+tt.with+"\n"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// A connection secret declares its connection, once beside the steps that
// name it, and its key must be a literal name.
func TestConnectionSecrets(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
secrets:
  - name: GOOGLE_TOKEN
    provider: connection
    key: work-google
  - name: API_TOKEN
    provider: connection
    key: work-api
  - name: OTHER
    provider: env
    key: work-env
steps:
  - id: unread
    action: mcp.call
    with: {connection: work-google, server: gmail, tool: search_threads}
`))
	require.NoError(t, err)
	assert.Equal(t, []ir.ConnectionRef{
		{Name: "work-google", Servers: []string{"gmail"}},
		{Name: "work-api"},
	}, dag.Connections)

	_, err = spec.LoadYAML(context.Background(), []byte(`
secrets:
  - name: TOKEN
    provider: connection
    key: ${CONN}
steps:
  - run: "true"
`))
	require.ErrorContains(t, err, "secrets[0].key")
	require.ErrorContains(t, err, "must be a literal name, not a reference")
}
