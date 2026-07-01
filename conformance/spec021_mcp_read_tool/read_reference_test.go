// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec021_mcp_read_tool_test

import (
	"testing"

	"github.com/dagucloud/dagu/conformance/mcptest"
	"github.com/stretchr/testify/require"
)

func TestReadReferenceTargets(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")

	tests := []struct {
		name      string
		arguments map[string]any
		uri       string
		contains  string
	}{
		{
			name:      "default reference",
			arguments: map[string]any{"target": "reference"},
			uri:       "dagu://reference/authoring",
			contains:  "# Dagu DAG authoring",
		},
		{
			name: "named reference",
			arguments: map[string]any{
				"target": "reference",
				"name":   "notifications",
			},
			uri:      "dagu://reference/notifications",
			contains: "# Dagu MCP notifications",
		},
		{
			name: "trimmed values",
			arguments: map[string]any{
				"target": " reference ",
				"name":   " tools ",
			},
			uri:      "dagu://reference/tools",
			contains: "# Dagu MCP tools",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callRead(t, session, tt.arguments)
			output := requireReadSuccess(t, result, "reference", tt.uri, "dagu_reference", "text/markdown")
			data := requireData(t, output)
			require.Equal(t, "text/markdown", data["mimeType"])
			require.Contains(t, requireString(t, data, "text"), tt.contains)
		})
	}
}

func TestReadReferenceCollectionTarget(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")

	result := callRead(t, session, map[string]any{"target": "references"})
	output := requireReadSuccess(t, result, "references", "", "", "")
	requireReferenceItems(t, requireData(t, output))
}

func TestReadReferenceURIMode(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")

	tests := []struct {
		name     string
		uri      string
		target   string
		linkName string
		mimeType string
	}{
		{
			name:     "reference collection",
			uri:      "dagu://reference",
			target:   "references",
			linkName: "dagu_references",
			mimeType: "application/json",
		},
		{
			name:     "reference topic",
			uri:      "dagu://reference/tools",
			target:   "reference",
			linkName: "dagu_reference",
			mimeType: "text/markdown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callRead(t, session, map[string]any{"uri": tt.uri})
			output := requireReadSuccess(t, result, tt.target, tt.uri, tt.linkName, tt.mimeType)
			data := requireData(t, output)
			if tt.target == "references" {
				requireReferenceItems(t, data)
				return
			}
			require.Equal(t, "text/markdown", data["mimeType"])
			require.Contains(t, requireString(t, data, "text"), "# Dagu MCP tools")
		})
	}
}

func requireReferenceItems(t *testing.T, data map[string]any) {
	t.Helper()

	items := requireItems(t, data)
	requireItem(t, items, "name", "authoring")
	requireItem(t, items, "name", "tools")
	requireItem(t, items, "name", "notifications")
	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		require.True(t, ok)
		require.NotEmpty(t, requireString(t, itemMap, "name"))
		require.NotEmpty(t, requireString(t, itemMap, "uri"))
		require.NotEmpty(t, requireString(t, itemMap, "mimeType"))
	}
}
