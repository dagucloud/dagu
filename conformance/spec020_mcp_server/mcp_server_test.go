// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec020_mcp_server_test

import (
	"slices"
	"testing"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/conformance/mcptest"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestStreamableHTTPExposesExpectedTools(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")
	ctx := mcptest.Context(t)

	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)

	require.Equal(t, []string{"dagu_change", "dagu_execute", "dagu_read"}, names)
}

func TestStreamableHTTPReadsReferenceResource(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")
	ctx := mcptest.Context(t)

	resources, err := session.ListResources(ctx, nil)
	require.NoError(t, err)

	var found bool
	for _, resource := range resources.Resources {
		if resource.URI == "dagu://reference/tools" {
			found = true
			require.Equal(t, "text/markdown", resource.MIMEType)
		}
	}
	require.True(t, found)

	read, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: "dagu://reference/tools"})
	require.NoError(t, err)
	require.Len(t, read.Contents, 1)

	content := read.Contents[0]
	require.Equal(t, "dagu://reference/tools", content.URI)
	require.Equal(t, "text/markdown", content.MIMEType)
	require.Contains(t, content.Text, "# Dagu MCP tools")
	require.Contains(t, content.Text, "dagu_read")
	require.Contains(t, content.Text, "dagu_change")
	require.Contains(t, content.Text, "dagu_execute")
}

func TestAPIKeySurfaceControlsMCPConnection(t *testing.T) {
	server := mcptest.NewAuthServer(t)
	ctx := mcptest.Context(t)

	mcpKey := server.CreateAPIKey(t, "mcp-connect", api.CreateAPIKeyRequestAllowedSurfacesMcp)
	session := server.Connect(t, mcpKey)
	_, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	restKey := server.CreateAPIKey(t, "rest-connect", api.CreateAPIKeyRequestAllowedSurfacesRestApi)
	rejected, err := server.TryConnect(t, restKey)
	if rejected != nil {
		t.Cleanup(func() { _ = rejected.Close() })
	}
	require.Error(t, err)
}
