// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec021_mcp_read_tool_test

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/dagucloud/dagu/conformance/mcptest"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type readFixture struct {
	server   *mcptest.Server
	session  *mcpsdk.ClientSession
	dagName  string
	dagRunID string
}

func newReadFixture(t *testing.T) readFixture {
	t.Helper()

	server := mcptest.NewServer(t)
	dagName := "mcp_read_contract"
	dagRunID := server.CreateCompletedRun(t, dagName)
	session := server.Connect(t, "")
	return readFixture{
		server:   server,
		session:  session,
		dagName:  dagName,
		dagRunID: dagRunID,
	}
}

func newDottedDAGReadFixture(t *testing.T) readFixture {
	t.Helper()

	server := mcptest.NewServer(t)
	dagName := "mcp.read-contract"
	dagRunID := server.CreateCompletedRun(t, dagName)
	session := server.Connect(t, "")
	return readFixture{
		server:   server,
		session:  session,
		dagName:  dagName,
		dagRunID: dagRunID,
	}
}

func callRead(t *testing.T, session *mcpsdk.ClientSession, arguments map[string]any) *mcpsdk.CallToolResult {
	t.Helper()

	return callReadWithArguments(t, session, arguments)
}

func callReadWithArguments(t *testing.T, session *mcpsdk.ClientSession, arguments any) *mcpsdk.CallToolResult {
	t.Helper()

	ctx := mcptest.Context(t)
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "dagu_read",
		Arguments: arguments,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

func requireReadSuccess(t *testing.T, result *mcpsdk.CallToolResult, target, uri, linkName, mimeType string) map[string]any {
	t.Helper()

	require.False(t, result.IsError)
	requireResultContent(t, result, uri, linkName, mimeType)

	output := mcptest.StructuredMap(t, result)
	require.Equal(t, target, output["target"])
	if uri == "" {
		require.NotContains(t, output, "uri")
		requireOnlyKeys(t, output, "target", "data", "references")
	} else {
		requireURIEqual(t, uri, requireString(t, output, "uri"))
		requireOnlyKeys(t, output, "target", "uri", "data", "references")
	}
	requireReferences(t, output["references"])
	return output
}

func requireReadError(t *testing.T, result *mcpsdk.CallToolResult, code string) map[string]any {
	t.Helper()

	require.True(t, result.IsError)
	output := mcptest.StructuredMap(t, result)
	require.Equal(t, code, output["code"])
	require.NotEmpty(t, requireString(t, output, "message"))
	for key := range output {
		require.Contains(t, []string{"code", "message", "target", "field", "uri", "details"}, key)
	}
	if details, ok := output["details"]; ok {
		_, isObject := details.(map[string]any)
		require.True(t, isObject)
	}
	return output
}

func requireResultContent(t *testing.T, result *mcpsdk.CallToolResult, uri, linkName, mimeType string) {
	t.Helper()

	if uri == "" {
		require.Len(t, result.Content, 1)
	} else {
		require.Len(t, result.Content, 2)
	}

	text, ok := result.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok)
	require.Equal(t, "Dagu read completed.", text.Text)

	if uri == "" {
		return
	}

	link, ok := result.Content[1].(*mcpsdk.ResourceLink)
	require.True(t, ok)
	requireURIEqual(t, uri, link.URI)
	require.Equal(t, linkName, link.Name)
	require.Equal(t, mimeType, link.MIMEType)
}

func requireData(t *testing.T, output map[string]any) map[string]any {
	t.Helper()

	data, ok := output["data"].(map[string]any)
	require.True(t, ok)
	return data
}

func requireItems(t *testing.T, data map[string]any) []any {
	t.Helper()

	items, ok := data["items"].([]any)
	require.True(t, ok)
	return items
}

func requireItem(t *testing.T, items []any, key, value string) map[string]any {
	t.Helper()

	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		require.True(t, ok)
		if itemMap[key] == value {
			return itemMap
		}
	}
	require.Fail(t, fmt.Sprintf("item with %s=%q was not found", key, value))
	return nil
}

func requireString(t *testing.T, data map[string]any, key string) string {
	t.Helper()

	value, ok := data[key].(string)
	require.True(t, ok)
	return value
}

func requireNumber(t *testing.T, data map[string]any, key string) {
	t.Helper()

	_, ok := data[key].(float64)
	require.True(t, ok)
}

func requireBool(t *testing.T, data map[string]any, key string) {
	t.Helper()

	_, ok := data[key].(bool)
	require.True(t, ok)
}

func requireReferences(t *testing.T, raw any) {
	t.Helper()

	values, ok := raw.([]any)
	require.True(t, ok)

	got := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		require.True(t, ok)
		got = append(got, text)
	}

	require.ElementsMatch(t, []string{
		"dagu://reference/authoring",
		"dagu://reference/tools",
		"dagu://reference/notifications",
	}, got)
}

func requireOnlyKeys(t *testing.T, data map[string]any, keys ...string) {
	t.Helper()

	got := make([]string, 0, len(data))
	for key := range data {
		got = append(got, key)
	}
	require.ElementsMatch(t, keys, got)
}

func requireURIEqual(t *testing.T, expected, actual string) {
	t.Helper()

	expectedURI, err := url.Parse(expected)
	require.NoError(t, err)
	actualURI, err := url.Parse(actual)
	require.NoError(t, err)

	require.Equal(t, expectedURI.Scheme, actualURI.Scheme)
	require.Equal(t, expectedURI.Host, actualURI.Host)
	require.Equal(t, expectedURI.EscapedPath(), actualURI.EscapedPath())
	require.Equal(t, expectedURI.Fragment, actualURI.Fragment)

	expectedQuery, err := url.ParseQuery(expectedURI.RawQuery)
	require.NoError(t, err)
	actualQuery, err := url.ParseQuery(actualURI.RawQuery)
	require.NoError(t, err)
	require.Equal(t, expectedQuery, actualQuery)
}

func dagSpecURI(name string) string {
	return "dagu://dags/" + url.PathEscape(name) + "/spec"
}

func runURI(name, dagRunID string) string {
	return "dagu://runs/" + url.PathEscape(name) + "/" + url.PathEscape(dagRunID)
}

func runLogsURI(name, dagRunID, query string) string {
	uri := runURI(name, dagRunID) + "/logs"
	if query == "" {
		return uri
	}
	return uri + "?" + query
}
