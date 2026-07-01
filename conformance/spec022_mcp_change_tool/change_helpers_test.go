// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec022_mcp_change_tool_test

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/conformance/mcptest"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type changeFixture struct {
	server  *mcptest.Server
	session *mcpsdk.ClientSession
}

func newChangeFixture(t *testing.T) changeFixture {
	t.Helper()

	server := mcptest.NewServer(t)
	session := server.Connect(t, "")
	return changeFixture{
		server:  server,
		session: session,
	}
}

func fixtureSpec(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(data)
}

func callChange(t *testing.T, session *mcpsdk.ClientSession, arguments any) *mcpsdk.CallToolResult {
	t.Helper()

	ctx := mcptest.Context(t)
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "dagu_change",
		Arguments: arguments,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

func callRead(t *testing.T, session *mcpsdk.ClientSession, arguments map[string]any) *mcpsdk.CallToolResult {
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

func requireChangeSuccess(t *testing.T, result *mcpsdk.CallToolResult, text, dagURI string) map[string]any {
	t.Helper()

	require.False(t, result.IsError)
	require.Len(t, result.Content, 2)

	contentText, ok := result.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok)
	require.Equal(t, text, contentText.Text)

	link, ok := result.Content[1].(*mcpsdk.ResourceLink)
	require.True(t, ok)
	requireURIEqual(t, dagURI, link.URI)
	require.Equal(t, "dag_spec", link.Name)
	require.Equal(t, "application/yaml", link.MIMEType)

	output := mcptest.StructuredMap(t, result)
	require.Equal(t, "upsert_dag", requireString(t, output, "type"))
	require.Equal(t, dagURI, requireString(t, output, "dagUri"))
	requireBool(t, output, "valid")
	requireArray(t, output, "errors")
	requireBool(t, output, "applied")
	requireReferences(t, output["references"])
	return output
}

func requireChangeError(t *testing.T, result *mcpsdk.CallToolResult, code string) map[string]any {
	t.Helper()

	require.True(t, result.IsError)
	output := mcptest.StructuredMap(t, result)
	require.Equal(t, code, output["code"])
	require.NotEmpty(t, requireString(t, output, "message"))
	for key := range output {
		require.Contains(t, []string{"code", "message", "mode", "type", "dagName", "field", "dagUri", "details"}, key)
	}
	if details, ok := output["details"]; ok {
		_, isObject := details.(map[string]any)
		require.True(t, isObject)
	}
	return output
}

func requireReadDAGSpec(t *testing.T, session *mcpsdk.ClientSession, name string) string {
	t.Helper()

	result := callRead(t, session, map[string]any{
		"target": "dag_spec",
		"name":   name,
	})
	require.False(t, result.IsError)

	output := mcptest.StructuredMap(t, result)
	data, ok := output["data"].(map[string]any)
	require.True(t, ok)
	return requireString(t, data, "spec")
}

func requireDAGSpecNotFound(t *testing.T, session *mcpsdk.ClientSession, name string) {
	t.Helper()

	result := callRead(t, session, map[string]any{
		"target": "dag_spec",
		"name":   name,
	})
	require.True(t, result.IsError)
	output := mcptest.StructuredMap(t, result)
	require.Equal(t, "resource_not_found", output["code"])
}

func requireNoDAGRuns(t *testing.T, session *mcpsdk.ClientSession, name string) {
	t.Helper()

	result := callRead(t, session, map[string]any{
		"target": "runs",
		"query":  "name=" + url.QueryEscape(name),
	})
	require.False(t, result.IsError)

	output := mcptest.StructuredMap(t, result)
	data, ok := output["data"].(map[string]any)
	require.True(t, ok)
	require.Empty(t, requireArray(t, data, "items"))
}

func changeArguments(mode, changeType, name, spec string) map[string]any {
	return map[string]any{
		"mode": mode,
		"type": changeType,
		"name": name,
		"spec": spec,
	}
}

func changeDAGSpecURI(name string) string {
	return "dagu://dags/" + url.PathEscape(name) + "/spec"
}

func requireString(t *testing.T, data map[string]any, key string) string {
	t.Helper()

	value, ok := data[key].(string)
	require.True(t, ok)
	return value
}

func requireBool(t *testing.T, data map[string]any, key string) bool {
	t.Helper()

	value, ok := data[key].(bool)
	require.True(t, ok)
	return value
}

func requireArray(t *testing.T, data map[string]any, key string) []any {
	t.Helper()

	switch value := data[key].(type) {
	case []any:
		return value
	case []string:
		items := make([]any, 0, len(value))
		for _, item := range value {
			items = append(items, item)
		}
		return items
	default:
		require.Failf(t, "field is not an array", "%s has type %T", key, data[key])
		return nil
	}
}

func requireReferences(t *testing.T, raw any) {
	t.Helper()

	var got []string
	switch values := raw.(type) {
	case []any:
		got = make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			require.True(t, ok)
			got = append(got, text)
		}
	case []string:
		got = values
	default:
		require.Failf(t, "references is not an array", "references has type %T", raw)
	}

	require.ElementsMatch(t, []string{
		"dagu://reference/authoring",
		"dagu://reference/tools",
		"dagu://reference/notifications",
	}, got)
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
	require.Equal(t, expectedURI.RawQuery, actualURI.RawQuery)
}
