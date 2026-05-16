// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestServerExposesCompactToolSurface(t *testing.T) {
	ctx := context.Background()
	session := connectTestClient(t, ctx, NewServer(nil))

	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)

	require.Equal(t, []string{toolChange, toolExecute, toolRead}, names)
}

func TestHTTPHandlerServesStreamableMCP(t *testing.T) {
	ctx := context.Background()
	httpServer := httptest.NewServer(NewHTTPHandler(nil))
	t.Cleanup(httpServer.Close)

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "dagu-mcp-test", Version: "v0.0.0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, result.Tools, 3)
}

func TestServerExposesReferenceResourcesAndPrompts(t *testing.T) {
	ctx := context.Background()
	session := connectTestClient(t, ctx, NewServer(nil))

	resources, err := session.ListResources(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, resources.Resources)

	got, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: "dagu://reference/tools"})
	require.NoError(t, err)
	require.Len(t, got.Contents, 1)
	require.Contains(t, got.Contents[0].Text, "dagu_execute")
	require.Contains(t, got.Contents[0].Text, "retry")
	require.Contains(t, got.Contents[0].Text, "stop")

	prompts, err := session.ListPrompts(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(prompts.Prompts))
	for _, prompt := range prompts.Prompts {
		names = append(names, prompt.Name)
	}
	require.Contains(t, names, "dagu_create_dag")
	require.Contains(t, names, "dagu_edit_dag")
	require.Contains(t, names, "dagu_debug_failed_run")
}

func TestReadToolCanReadReferenceResource(t *testing.T) {
	ctx := context.Background()
	session := connectTestClient(t, ctx, NewServer(nil))

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      toolRead,
		Arguments: readInput{Target: "reference", Name: "notifications"},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotEmpty(t, result.Content)
	require.NotNil(t, result.StructuredContent)
}

func TestPromptMentionsPreviewBeforeApply(t *testing.T) {
	ctx := context.Background()
	session := connectTestClient(t, ctx, NewServer(nil))

	result, err := session.GetPrompt(ctx, &mcpsdk.GetPromptParams{
		Name:      "dagu_create_dag",
		Arguments: map[string]string{"goal": "print hello"},
	})
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)

	data, err := result.Messages[0].Content.MarshalJSON()
	require.NoError(t, err)
	text := string(data)
	require.True(t, strings.Contains(text, "mode=preview"))
	require.True(t, strings.Contains(text, "dagu_change"))
}

func connectTestClient(t *testing.T, ctx context.Context, server *mcpsdk.Server) *mcpsdk.ClientSession {
	t.Helper()

	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "dagu-mcp-test", Version: "v0.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession
}
