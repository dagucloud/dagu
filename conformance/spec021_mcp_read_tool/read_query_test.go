// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec021_mcp_read_tool_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadTargetQueryParameters(t *testing.T) {
	fixture := newReadFixture(t)

	tests := []struct {
		name     string
		target   string
		query    string
		validate func(t *testing.T, output map[string]any)
	}{
		{
			name:   "dags pagination sorting and labels",
			target: "dags",
			query:  "page=1&perPage=1000&sort=nextRun&order=desc&labels=alpha,beta",
			validate: func(t *testing.T, output map[string]any) {
				requireItems(t, requireData(t, output))
			},
		},
		{
			name:   "dags name filter",
			target: "dags",
			query:  "name=" + fixture.dagName,
			validate: func(t *testing.T, output map[string]any) {
				requireItem(t, requireItems(t, requireData(t, output)), "name", fixture.dagName)
			},
		},
		{
			name:   "runs name and dagRunId filters",
			target: "runs",
			query:  "name=" + fixture.dagName + "&dagRunId=" + fixture.dagRunID,
			validate: func(t *testing.T, output map[string]any) {
				item := requireItem(t, requireItems(t, requireData(t, output)), "dagRunId", fixture.dagRunID)
				requireRunListItem(t, item, fixture.dagName, fixture.dagRunID)
			},
		},
		{
			name:   "runs latest dagRunId",
			target: "runs",
			query:  "name=" + fixture.dagName + "&dagRunId=latest",
			validate: func(t *testing.T, output map[string]any) {
				requireItems(t, requireData(t, output))
			},
		},
		{
			name:   "runs repeatable status",
			target: "runs",
			query:  "status=4&status=4",
			validate: func(t *testing.T, output map[string]any) {
				requireItems(t, requireData(t, output))
			},
		},
		{
			name:   "runs dates limit cursor and labels",
			target: "runs",
			query:  "fromDate=0&toDate=4102444800&limit=500&cursor=opaque-cursor&labels=alpha,beta",
			validate: func(t *testing.T, output map[string]any) {
				requireItems(t, requireData(t, output))
			},
		},
		{
			name:   "run_logs tail",
			target: "run_logs",
			query:  "tail=1",
			validate: func(t *testing.T, output map[string]any) {
				requireRunLogsData(t, requireData(t, output))
			},
		},
		{
			name:   "run_logs head offset and limit",
			target: "run_logs",
			query:  "head=1&offset=1&limit=10000",
			validate: func(t *testing.T, output map[string]any) {
				requireRunLogsData(t, requireData(t, output))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arguments := map[string]any{
				"target": tt.target,
				"query":  tt.query,
			}
			if tt.target == "run_logs" {
				arguments["name"] = fixture.dagName
				arguments["dagRunId"] = fixture.dagRunID
			}

			result := callRead(t, fixture.session, arguments)
			var uri string
			var linkName string
			var mimeType string
			if tt.target == "run_logs" {
				uri = runLogsURI(fixture.dagName, fixture.dagRunID, tt.query)
				linkName = "dag_run_logs"
				mimeType = "application/json"
			}
			output := requireReadSuccess(t, result, tt.target, uri, linkName, mimeType)
			tt.validate(t, output)
		})
	}
}

func TestReadURIQueryParameters(t *testing.T) {
	fixture := newReadFixture(t)

	tests := []struct {
		name     string
		target   string
		uri      string
		linkName string
		mimeType string
		validate func(t *testing.T, output map[string]any)
	}{
		{
			name:     "dags query",
			target:   "dags",
			uri:      "dagu://dags?page=1&perPage=1000&name=" + fixture.dagName + "&sort=name&order=asc&labels=alpha,beta",
			linkName: "dags",
			mimeType: "application/json",
			validate: func(t *testing.T, output map[string]any) {
				requireItems(t, requireData(t, output))
			},
		},
		{
			name:     "runs query",
			target:   "runs",
			uri:      "dagu://runs?name=" + fixture.dagName + "&dagRunId=latest&status=4&status=4&fromDate=0&toDate=4102444800&limit=500&cursor=opaque-cursor&labels=alpha,beta",
			linkName: "dag_runs",
			mimeType: "application/json",
			validate: func(t *testing.T, output map[string]any) {
				requireItems(t, requireData(t, output))
			},
		},
		{
			name:     "run logs query",
			target:   "run_logs",
			uri:      runLogsURI(fixture.dagName, fixture.dagRunID, "head=1&offset=1&limit=10000"),
			linkName: "dag_run_logs",
			mimeType: "application/json",
			validate: func(t *testing.T, output map[string]any) {
				requireRunLogsData(t, requireData(t, output))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callRead(t, fixture.session, map[string]any{"uri": tt.uri})
			output := requireReadSuccess(t, result, tt.target, tt.uri, tt.linkName, tt.mimeType)
			tt.validate(t, output)
		})
	}
}

func TestReadEmptyQueryStringIsAbsent(t *testing.T) {
	fixture := newReadFixture(t)

	t.Run("target collection", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target": "dags",
			"query":  "   ",
		})
		output := requireReadSuccess(t, result, "dags", "", "", "")
		requireItems(t, requireData(t, output))
	})

	t.Run("target logs", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target":   "run_logs",
			"name":     fixture.dagName,
			"dagRunId": fixture.dagRunID,
			"query":    "",
		})
		output := requireReadSuccess(t, result, "run_logs", runLogsURI(fixture.dagName, fixture.dagRunID, ""), "dag_run_logs", "application/json")
		requireRunLogsData(t, requireData(t, output))
	})
}

func TestReadTargetQueryValidationErrors(t *testing.T) {
	fixture := newReadFixture(t)

	tests := []struct {
		name   string
		target string
		query  string
	}{
		{name: "dags page below range", target: "dags", query: "page=0"},
		{name: "dags page not integer", target: "dags", query: "page=x"},
		{name: "dags perPage below range", target: "dags", query: "perPage=0"},
		{name: "dags perPage above range", target: "dags", query: "perPage=1001"},
		{name: "dags labels include empty label", target: "dags", query: "labels=alpha,,beta"},
		{name: "dags sort unsupported", target: "dags", query: "sort=createdAt"},
		{name: "dags order unsupported", target: "dags", query: "order=sideways"},
		{name: "dags repeated non-repeatable parameter", target: "dags", query: "name=a&name=b"},
		{name: "runs dagRunId empty", target: "runs", query: "dagRunId="},
		{name: "runs status below enum", target: "runs", query: "status=-1"},
		{name: "runs status above enum", target: "runs", query: "status=9"},
		{name: "runs status not integer", target: "runs", query: "status=x"},
		{name: "runs fromDate not timestamp", target: "runs", query: "fromDate=x"},
		{name: "runs toDate not timestamp", target: "runs", query: "toDate=x"},
		{name: "runs limit below range", target: "runs", query: "limit=0"},
		{name: "runs limit above range", target: "runs", query: "limit=501"},
		{name: "runs cursor empty", target: "runs", query: "cursor="},
		{name: "runs labels include empty label", target: "runs", query: "labels=alpha,,beta"},
		{name: "runs repeated non-repeatable parameter", target: "runs", query: "name=a&name=b"},
		{name: "run_logs tail below range", target: "run_logs", query: "tail=0"},
		{name: "run_logs tail not integer", target: "run_logs", query: "tail=x"},
		{name: "run_logs head below range", target: "run_logs", query: "head=0"},
		{name: "run_logs offset below range", target: "run_logs", query: "offset=0"},
		{name: "run_logs limit below range", target: "run_logs", query: "limit=0"},
		{name: "run_logs limit above range", target: "run_logs", query: "limit=10001"},
		{name: "run_logs repeated non-repeatable parameter", target: "run_logs", query: "tail=1&tail=2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arguments := map[string]any{
				"target": tt.target,
				"query":  tt.query,
			}
			if tt.target == "run_logs" {
				arguments["name"] = fixture.dagName
				arguments["dagRunId"] = fixture.dagRunID
			}

			result := callRead(t, fixture.session, arguments)
			output := requireReadError(t, result, "invalid_tool_input")
			require.Equal(t, tt.target, output["target"])
			require.Equal(t, "query", output["field"])
		})
	}
}

func TestReadURIQueryValidationErrors(t *testing.T) {
	fixture := newReadFixture(t)

	tests := []struct {
		name string
		uri  string
	}{
		{name: "dags page below range", uri: "dagu://dags?page=0"},
		{name: "dags perPage above range", uri: "dagu://dags?perPage=1001"},
		{name: "dags labels include empty label", uri: "dagu://dags?labels=alpha,,beta"},
		{name: "dags sort unsupported", uri: "dagu://dags?sort=createdAt"},
		{name: "dags repeated non-repeatable parameter", uri: "dagu://dags?name=a&name=b"},
		{name: "runs status above enum", uri: "dagu://runs?status=9"},
		{name: "runs status not integer", uri: "dagu://runs?status=x"},
		{name: "runs limit above range", uri: "dagu://runs?limit=501"},
		{name: "runs cursor empty", uri: "dagu://runs?cursor="},
		{name: "runs labels include empty label", uri: "dagu://runs?labels=alpha,,beta"},
		{name: "runs repeated non-repeatable parameter", uri: "dagu://runs?name=a&name=b"},
		{name: "runs malformed percent encoding", uri: "dagu://runs?name=%zz"},
		{name: "run_logs tail below range", uri: runLogsURI(fixture.dagName, fixture.dagRunID, "tail=0")},
		{name: "run_logs limit above range", uri: runLogsURI(fixture.dagName, fixture.dagRunID, "limit=10001")},
		{name: "run_logs repeated non-repeatable parameter", uri: runLogsURI(fixture.dagName, fixture.dagRunID, "tail=1&tail=2")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callRead(t, fixture.session, map[string]any{"uri": tt.uri})
			output := requireReadError(t, result, "invalid_resource_uri")
			require.Equal(t, tt.uri, output["uri"])
		})
	}
}
