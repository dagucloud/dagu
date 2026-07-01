// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec021_mcp_read_tool_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadRunTargets(t *testing.T) {
	fixture := newReadFixture(t)

	t.Run("runs target", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target": "runs",
			"query":  "name=" + fixture.dagName + "&limit=20&status=4",
		})
		output := requireReadSuccess(t, result, "runs", "", "", "")
		item := requireItem(t, requireItems(t, requireData(t, output)), "dagRunId", fixture.dagRunID)
		requireRunListItem(t, item, fixture.dagName, fixture.dagRunID)
	})

	t.Run("run target", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target":   "run",
			"name":     fixture.dagName,
			"dagRunId": fixture.dagRunID,
		})
		output := requireReadSuccess(t, result, "run", runURI(fixture.dagName, fixture.dagRunID), "dag_run", "application/json")
		requireRunData(t, requireData(t, output), fixture.dagName, fixture.dagRunID)
	})

	t.Run("run_logs target", func(t *testing.T) {
		query := "tail=100"
		result := callRead(t, fixture.session, map[string]any{
			"target":   "run_logs",
			"name":     fixture.dagName,
			"dagRunId": fixture.dagRunID,
			"query":    query,
		})
		output := requireReadSuccess(t, result, "run_logs", runLogsURI(fixture.dagName, fixture.dagRunID, query), "dag_run_logs", "application/json")
		requireRunLogsData(t, requireData(t, output))
	})
}

func TestReadRunURIMode(t *testing.T) {
	fixture := newReadFixture(t)

	tests := []struct {
		name     string
		uri      string
		target   string
		linkName string
		mimeType string
	}{
		{
			name:     "runs collection",
			uri:      "dagu://runs?name=" + fixture.dagName + "&limit=20&status=4",
			target:   "runs",
			linkName: "dag_runs",
			mimeType: "application/json",
		},
		{
			name:     "run detail",
			uri:      runURI(fixture.dagName, fixture.dagRunID),
			target:   "run",
			linkName: "dag_run",
			mimeType: "application/json",
		},
		{
			name:     "run logs",
			uri:      runLogsURI(fixture.dagName, fixture.dagRunID, "head=1&offset=1&limit=100"),
			target:   "run_logs",
			linkName: "dag_run_logs",
			mimeType: "application/json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callRead(t, fixture.session, map[string]any{"uri": tt.uri})
			output := requireReadSuccess(t, result, tt.target, tt.uri, tt.linkName, tt.mimeType)
			data := requireData(t, output)
			switch tt.target {
			case "runs":
				item := requireItem(t, requireItems(t, data), "dagRunId", fixture.dagRunID)
				requireRunListItem(t, item, fixture.dagName, fixture.dagRunID)
			case "run":
				requireRunData(t, data, fixture.dagName, fixture.dagRunID)
			case "run_logs":
				requireRunLogsData(t, data)
			}
		})
	}
}

func TestReadRunURIUsesCanonicalNameSegment(t *testing.T) {
	fixture := newDottedDAGReadFixture(t)

	result := callRead(t, fixture.session, map[string]any{
		"target":   "run",
		"name":     fixture.dagName,
		"dagRunId": fixture.dagRunID,
	})
	requireReadSuccess(t, result, "run", runURI(fixture.dagName, fixture.dagRunID), "dag_run", "application/json")
	require.Equal(t, "dagu://runs/mcp.read-contract/"+fixture.dagRunID, runURI(fixture.dagName, fixture.dagRunID))
}

func requireRunListItem(t *testing.T, item map[string]any, dagName, dagRunID string) {
	t.Helper()

	require.Equal(t, dagName, item["name"])
	require.Equal(t, dagRunID, item["dagRunId"])
	require.Equal(t, runURI(dagName, dagRunID), item["uri"])
	requireNumber(t, item, "status")
	require.NotEmpty(t, requireString(t, item, "statusLabel"))
}

func requireRunData(t *testing.T, data map[string]any, dagName, dagRunID string) {
	t.Helper()

	require.Equal(t, dagName, data["name"])
	require.Equal(t, dagRunID, data["dagRunId"])
	require.Equal(t, runURI(dagName, dagRunID), data["uri"])
	requireNumber(t, data, "status")
	require.NotEmpty(t, requireString(t, data, "statusLabel"))
}

func requireRunLogsData(t *testing.T, data map[string]any) {
	t.Helper()

	schedulerLog, ok := data["schedulerLog"].(map[string]any)
	require.True(t, ok)
	requireString(t, schedulerLog, "content")
	requireNumber(t, schedulerLog, "lineCount")
	requireNumber(t, schedulerLog, "totalLines")
	requireBool(t, schedulerLog, "hasMore")

	stepLogs, ok := data["stepLogs"].([]any)
	require.True(t, ok)
	for _, stepLog := range stepLogs {
		step, ok := stepLog.(map[string]any)
		require.True(t, ok)
		requireString(t, step, "stepName")
		requireNumber(t, step, "status")
		requireString(t, step, "statusLabel")
		requireBool(t, step, "hasStdout")
		requireBool(t, step, "hasStderr")
	}
}
