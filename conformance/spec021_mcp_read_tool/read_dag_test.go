// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec021_mcp_read_tool_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/mcptest"
	"github.com/stretchr/testify/require"
)

func TestReadDAGTargets(t *testing.T) {
	fixture := newReadFixture(t)

	t.Run("dags target", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target": "dags",
			"query":  "name=" + fixture.dagName + "&page=1&perPage=10&sort=name&order=asc",
		})
		output := requireReadSuccess(t, result, "dags", "", "", "")
		data := requireData(t, output)
		item := requireItem(t, requireItems(t, data), "name", fixture.dagName)
		require.Equal(t, dagSpecURI(fixture.dagName), requireString(t, item, "uri"))
		requireBool(t, item, "suspended")
		_, hasPagination := data["pagination"].(map[string]any)
		require.True(t, hasPagination)
	})

	t.Run("dag target", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target": "dag",
			"name":   fixture.dagName,
		})
		output := requireReadSuccess(t, result, "dag", "", "", "")
		data := requireData(t, output)
		require.Equal(t, fixture.dagName, data["name"])
		require.Equal(t, dagSpecURI(fixture.dagName), data["specUri"])
		requireBool(t, data, "suspended")
		// The fixture DAG has a completed run, so the latest-run summary must
		// be present.
		latestRun, ok := data["latestRun"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, fixture.dagRunID, latestRun["dagRunId"])
		requireNumber(t, latestRun, "status")
		require.NotEmpty(t, requireString(t, latestRun, "statusLabel"))
	})

	t.Run("dag_spec target", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target": "dag_spec",
			"name":   fixture.dagName,
		})
		output := requireReadSuccess(t, result, "dag_spec", dagSpecURI(fixture.dagName), "dag_spec", "application/yaml")
		requireDAGSpecData(t, requireData(t, output), fixture.dagName)
	})

	t.Run("dag_search target", func(t *testing.T) {
		// The fixture spec contains "echo <dagName>", so searching for the
		// DAG name matches the definition content.
		result := callRead(t, fixture.session, map[string]any{
			"target": "dag_search",
			"search": fixture.dagName,
		})
		output := mcptest.StructuredMap(t, result)
		require.False(t, result.IsError)
		require.Equal(t, "dag_search", output["target"])
		data := requireData(t, output)
		requireBool(t, data, "hasMore")

		results, ok := data["results"].([]any)
		require.True(t, ok)
		item := requireItem(t, results, "name", fixture.dagName)
		require.Equal(t, dagSpecURI(fixture.dagName), item["uri"])
		matches, ok := item["matches"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, matches)
	})

	t.Run("dag_search requires search text", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{"target": "dag_search"})
		output := requireReadError(t, result, "invalid_tool_input")
		require.Equal(t, "search", output["field"])
	})
}

func TestReadDAGURIMode(t *testing.T) {
	fixture := newReadFixture(t)

	tests := []struct {
		name     string
		uri      string
		target   string
		linkName string
		mimeType string
	}{
		{
			name:     "dags collection",
			uri:      "dagu://dags?name=" + fixture.dagName + "&perPage=10",
			target:   "dags",
			linkName: "dags",
			mimeType: "application/json",
		},
		{
			name:     "dag spec",
			uri:      dagSpecURI(fixture.dagName),
			target:   "dag_spec",
			linkName: "dag_spec",
			mimeType: "application/yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callRead(t, fixture.session, map[string]any{"uri": tt.uri})
			output := requireReadSuccess(t, result, tt.target, tt.uri, tt.linkName, tt.mimeType)
			data := requireData(t, output)
			if tt.target == "dags" {
				item := requireItem(t, requireItems(t, data), "name", fixture.dagName)
				require.Equal(t, dagSpecURI(fixture.dagName), requireString(t, item, "uri"))
				return
			}
			requireDAGSpecData(t, data, fixture.dagName)
		})
	}
}

func TestReadDAGURIUsesCanonicalNameSegment(t *testing.T) {
	fixture := newDottedDAGReadFixture(t)

	result := callRead(t, fixture.session, map[string]any{
		"target": "dag_spec",
		"name":   fixture.dagName,
	})
	requireReadSuccess(t, result, "dag_spec", dagSpecURI(fixture.dagName), "dag_spec", "application/yaml")
	require.Equal(t, "dagu://dags/mcp.read-contract/spec", dagSpecURI(fixture.dagName))
}

func requireDAGSpecData(t *testing.T, data map[string]any, dagName string) {
	t.Helper()

	require.Equal(t, dagName, data["name"])
	require.Equal(t, "application/yaml", data["mimeType"])
	require.Contains(t, requireString(t, data, "spec"), "echo "+dagName)
	require.IsType(t, []any{}, data["errors"])
}
