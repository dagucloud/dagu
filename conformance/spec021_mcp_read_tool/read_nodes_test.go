// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec021_mcp_read_tool_test

import (
	"testing"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/conformance/mcptest"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// newNodesSession imports two inventories as the administrator and connects
// with a viewer API key, the role that reads inventories over the API.
func newNodesSession(t *testing.T) *mcpsdk.ClientSession {
	t.Helper()

	server := mcptest.NewAuthServer(t)
	server.ImportInventories(t, []map[string]any{
		{
			"name": "Tokyo production",
			"defaults": map[string]any{
				"user":   "deploy",
				"key":    map[string]any{"secret": "ssh/tokyo"},
				"labels": []string{"site=tokyo"},
			},
			"nodes": []map[string]any{
				{"name": "tokyo-web01", "host": "10.0.1.11", "labels": []string{"web"}},
				{"name": "tokyo-web02", "host": "10.0.1.12", "labels": []string{"web", "canary"}},
				{"name": "tokyo-db01", "host": "db01.internal", "password": map[string]any{"secret": "ssh/tokyo-db"}, "labels": []string{"db"}},
			},
		},
		{
			"name":  "Osaka staging",
			"nodes": []map[string]any{{"name": "osaka-web01", "host": "10.1.0.11", "key": "~/.ssh/id_ed25519"}},
		},
	})
	return server.Connect(t, server.CreateAPIKey(t, "viewer", api.CreateAPIKeyRequestAllowedSurfacesMcp))
}

func TestReadNodeTargets(t *testing.T) {
	session := newNodesSession(t)

	t.Run("nodes lists every node in name order", func(t *testing.T) {
		data := requireData(t, requireReadSuccess(t, callRead(t, session, map[string]any{"target": "nodes"}), "nodes", "", "", ""))
		require.Equal(t, []string{"osaka-web01", "tokyo-db01", "tokyo-web01", "tokyo-web02"}, itemNames(t, data))
	})

	t.Run("labels and inventory select nodes", func(t *testing.T) {
		result := callRead(t, session, map[string]any{"target": "nodes", "query": "labels=web,!canary&inventory=TOKYO+PRODUCTION"})
		data := requireData(t, requireReadSuccess(t, result, "nodes", "", "", ""))
		require.Equal(t, []string{"tokyo-web01"}, itemNames(t, data))
	})

	t.Run("nodes pages", func(t *testing.T) {
		result := callRead(t, session, map[string]any{"target": "nodes", "query": "perPage=1&page=2"})
		data := requireData(t, requireReadSuccess(t, result, "nodes", "", "", ""))
		require.Equal(t, []string{"tokyo-db01"}, itemNames(t, data))
		pagination, ok := data["pagination"].(map[string]any)
		require.True(t, ok)
		require.EqualValues(t, 2, pagination["currentPage"])
		require.EqualValues(t, 4, pagination["totalPages"])
	})

	// Query values are trimmed, as their validation trims them.
	t.Run("nodes trims query values", func(t *testing.T) {
		result := callRead(t, session, map[string]any{"target": "nodes", "query": "perPage=%201%20&page=+2&name=+web0"})
		data := requireData(t, requireReadSuccess(t, result, "nodes", "", "", ""))
		require.Equal(t, []string{"tokyo-web01"}, itemNames(t, data))
	})

	// Credentials are refs: a key path or a secret name, never a value.
	t.Run("node reads one node by name in any case", func(t *testing.T) {
		data := requireData(t, requireReadSuccess(t, callRead(t, session, map[string]any{"target": "node", "name": "TOKYO-DB01"}), "node", "", "", ""))
		require.Equal(t, "tokyo-db01", data["name"])
		require.Equal(t, "Tokyo production", data["inventory"].(map[string]any)["name"])
		require.Equal(t, []any{"db", "site=tokyo"}, data["labels"])
		auth, ok := data["auth"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "ssh/tokyo-db", auth["passwordSecret"])
		for key := range auth {
			require.Contains(t, []string{"keyPath", "keySecret", "passwordSecret", "passphraseSecret"}, key)
		}
	})

	t.Run("inventories lists every inventory", func(t *testing.T) {
		data := requireData(t, requireReadSuccess(t, callRead(t, session, map[string]any{"target": "inventories"}), "inventories", "", "", ""))
		items := requireItems(t, data)
		require.Equal(t, []string{"Osaka staging", "Tokyo production"}, itemNames(t, data))
		require.EqualValues(t, 1, items[0].(map[string]any)["nodeCount"])
		require.EqualValues(t, 3, items[1].(map[string]any)["nodeCount"])
		requireNumber(t, items[1].(map[string]any), "version")
	})
}

func TestReadNodeErrors(t *testing.T) {
	session := newNodesSession(t)

	for _, tc := range []struct {
		name      string
		arguments map[string]any
		code      string
		field     string
		message   string
	}{
		{
			name:      "unknown inventory",
			arguments: map[string]any{"target": "nodes", "query": "inventory=nowhere"},
			code:      "invalid_tool_input",
			field:     "query",
			message:   `no inventory has the ID or name "nowhere"`,
		},
		{
			name:      "invalid selector",
			arguments: map[string]any{"target": "nodes", "query": "labels=,"},
			code:      "invalid_tool_input",
			field:     "query",
		},
		{
			name:      "unsupported query parameter",
			arguments: map[string]any{"target": "nodes", "query": "sort=name"},
			code:      "invalid_tool_input",
			field:     "query",
		},
		{
			name:      "unknown node",
			arguments: map[string]any{"target": "node", "name": "web99"},
			code:      "resource_not_found",
			message:   `node "web99" not found`,
		},
		{
			name:      "node without name",
			arguments: map[string]any{"target": "node"},
			code:      "invalid_tool_input",
			field:     "name",
		},
		{
			name:      "nodes with name",
			arguments: map[string]any{"target": "nodes", "name": "tokyo-web01"},
			code:      "invalid_tool_input",
			field:     "name",
		},
		{
			name:      "inventories with query",
			arguments: map[string]any{"target": "inventories", "query": "page=1"},
			code:      "invalid_tool_input",
			field:     "query",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := requireReadError(t, callRead(t, session, tc.arguments), tc.code)
			require.Equal(t, tc.arguments["target"], output["target"])
			if tc.field != "" {
				require.Equal(t, tc.field, output["field"])
			}
			if tc.message != "" {
				require.Equal(t, tc.message, output["message"])
			}
		})
	}
}

// itemNames returns the name of each item of a collection, in order.
func itemNames(t *testing.T, data map[string]any) []string {
	t.Helper()

	var names []string
	for _, item := range requireItems(t, data) {
		entry, ok := item.(map[string]any)
		require.True(t, ok)
		names = append(names, requireString(t, entry, "name"))
	}
	return names
}
