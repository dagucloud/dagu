// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const testConnectTimeout = 5 * time.Second

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

func TestServerAdvertisesSupportedCapabilities(t *testing.T) {
	ctx := context.Background()
	session := connectTestClient(t, ctx, NewServer(nil))

	result := session.InitializeResult()
	require.NotNil(t, result)
	require.Equal(t, &mcpsdk.PromptCapabilities{}, result.Capabilities.Prompts)
	require.Equal(t, &mcpsdk.ResourceCapabilities{Subscribe: true}, result.Capabilities.Resources)
	require.Equal(t, &mcpsdk.ToolCapabilities{}, result.Capabilities.Tools)
	apps, ok := result.Capabilities.Extensions[mcpAppsExtensionURI].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{mcpAppMIMEType}, apps["mimeTypes"])
}

func TestServerExposesMCPAppRunInspector(t *testing.T) {
	ctx := context.Background()
	session := connectTestClient(t, ctx, NewServer(nil))

	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, name := range []string{toolRead, toolExecute} {
		tool := findTool(t, tools.Tools, name)
		require.Equal(t, runInspectorURI, tool.Meta[runInspectorMetaKey])
		ui, ok := tool.Meta["ui"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, runInspectorURI, ui["resourceUri"])
		require.Equal(t, []any{"model", "app"}, ui["visibility"])
	}

	resources, err := session.ListResources(ctx, nil)
	require.NoError(t, err)
	resource := findResource(t, resources.Resources, runInspectorURI)
	require.Equal(t, mcpAppMIMEType, resource.MIMEType)
	require.NotEmpty(t, resource.Meta)

	result, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: runInspectorURI})
	require.NoError(t, err)
	require.Len(t, result.Contents, 1)
	require.Equal(t, mcpAppMIMEType, result.Contents[0].MIMEType)
	require.Contains(t, result.Contents[0].Text, "<!doctype html>")
	require.Contains(t, result.Contents[0].Text, `name: "`+toolExecute+`"`)
	require.NotEmpty(t, result.Contents[0].Meta)
}

func TestServerExposesStepLogResource(t *testing.T) {
	ctx := context.Background()
	session := connectTestClient(t, ctx, NewServer(nil))

	templates, err := session.ListResourceTemplates(ctx, nil)
	require.NoError(t, err)
	require.True(t, slices.ContainsFunc(templates.ResourceTemplates, func(template *mcpsdk.ResourceTemplate) bool {
		return template.URITemplate == "dagu://runs/{name}/{dagRunId}/steps/{stepName}/logs"
	}))

	const expectedURI = "dagu://runs/demo%20dag/run%2F1/steps/build%2Foutput/logs"
	require.Equal(t, expectedURI, stepLogURI("demo dag", "run/1", "build/output"))
	input, readErr := parseReadResourceURI(expectedURI)
	require.Nil(t, readErr)
	require.Equal(t, readTargetStepLog, input.Target)
	require.Equal(t, "demo dag", input.Name)
	require.Equal(t, "run/1", input.DAGRunID)
	require.Equal(t, "build/output", input.StepName)

	input, readErr = parseReadToolInput(json.RawMessage(`{
		"target":"step_log",
		"name":"demo dag",
		"dagRunId":"run/1",
		"stepName":"build/output"
	}`))
	require.Nil(t, readErr)
	require.Equal(t, expectedURI, input.URI)

	_, err = session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: expectedURI + "?tail=100"})
	require.Error(t, err)
}

func TestHTTPHandlerServesStreamableMCP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testConnectTimeout)
	defer cancel()
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

	resource, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: runInspectorURI})
	require.NoError(t, err)
	require.Len(t, resource.Contents, 1)
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

	authoring, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: "dagu://reference/authoring"})
	require.NoError(t, err)
	require.Len(t, authoring.Contents, 1)
	require.Contains(t, authoring.Contents[0].Text, "human.task")
	require.Contains(t, authoring.Contents[0].Text, "Human task form properties")

	prompts, err := session.ListPrompts(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(prompts.Prompts))
	for _, prompt := range prompts.Prompts {
		names = append(names, prompt.Name)
	}
	require.Contains(t, names, "dagu_create_dag")
	require.Contains(t, names, "dagu_edit_dag")
	require.Contains(t, names, "dagu_create_doc")
	require.Contains(t, names, "dagu_edit_doc")
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

func TestDocumentPromptsIncludeRequiredUpsertFields(t *testing.T) {
	ctx := context.Background()
	session := connectTestClient(t, ctx, NewServer(nil))

	tests := []struct {
		name      string
		arguments map[string]string
	}{
		{
			name: "dagu_create_doc",
			arguments: map[string]string{
				"workspace": "operations",
				"path":      "runbooks/restart",
				"goal":      "Document a safe restart.",
			},
		},
		{
			name: "dagu_edit_doc",
			arguments: map[string]string{
				"workspace": "operations",
				"path":      "runbooks/restart",
				"change":    "Add the rollback steps.",
			},
		},
	}
	for _, test := range tests {
		result, err := session.GetPrompt(ctx, &mcpsdk.GetPromptParams{
			Name:      test.name,
			Arguments: test.arguments,
		})
		require.NoError(t, err)
		require.Len(t, result.Messages, 1)

		data, err := result.Messages[0].Content.MarshalJSON()
		require.NoError(t, err)
		text := string(data)
		require.Contains(t, text, "type=upsert_doc")
		require.Contains(t, text, "workspace=operations")
		require.Contains(t, text, "path=runbooks/restart")
		require.Contains(t, text, "content")
	}
}

func TestRunLogsURIWithQueryPreservesQuery(t *testing.T) {
	require.Equal(t,
		"dagu://runs/demo%20dag/run%2F1/logs?node=step%201&tail=true",
		runLogsURIWithQuery("demo dag", "run/1", "node=step%201&tail=true"),
	)
}

func TestRunWatcherStopsAfterPersistentErrors(t *testing.T) {
	const uri = "dagu://runs/missing/run-1"
	svc := &Service{
		watchers:          map[string]*resourceWatcher{uri: {id: 1}},
		watchPollInterval: 10 * time.Millisecond,
		watchMaxErrors:    2,
	}

	ctx, cancel := context.WithTimeout(context.Background(), testConnectTimeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.watchRunResource(ctx, uri, 1)
	}()

	require.Eventually(t, func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		_, ok := svc.watchers[uri]
		return !ok
	}, time.Second, 10*time.Millisecond)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run watcher did not exit after persistent polling errors")
	}
}

func connectTestClient(t *testing.T, ctx context.Context, server *mcpsdk.Server) *mcpsdk.ClientSession {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, testConnectTimeout)
	t.Cleanup(cancel)

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

func findTool(t *testing.T, tools []*mcpsdk.Tool, name string) *mcpsdk.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func findResource(t *testing.T, resources []*mcpsdk.Resource, uri string) *mcpsdk.Resource {
	t.Helper()
	for _, resource := range resources {
		if resource.URI == uri {
			return resource
		}
	}
	t.Fatalf("resource %q not found", uri)
	return nil
}
