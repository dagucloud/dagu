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

func TestExecuteToolSupportsNoReuse(t *testing.T) {
	ctx := context.Background()
	session := connectTestClient(t, ctx, NewServer(nil))

	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	schema, err := json.Marshal(findTool(t, result.Tools, toolExecute).InputSchema)
	require.NoError(t, err)
	require.Contains(t, string(schema), `"noReuse"`)

	startBody := executeBody(executeInput{NoReuse: true})
	require.NotNil(t, startBody.NoReuse)
	require.True(t, *startBody.NoReuse)

	enqueueBody := enqueueBody(executeInput{NoReuse: true})
	require.NotNil(t, enqueueBody.NoReuse)
	require.True(t, *enqueueBody.NoReuse)
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

func TestReadToolValidatesDAGQuery(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantError bool
	}{
		{name: "active true", query: "active=true"},
		{name: "active false", query: "active=false"},
		{name: "numeric active", query: "active=1", wantError: true},
		{name: "uppercase active", query: "active=TRUE", wantError: true},
		{name: "unsupported parameter", query: "unknown=value", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(readInput{Target: readTargetDAGs, Query: test.query})
			require.NoError(t, err)

			input, readErr := parseReadToolInput(payload)
			if test.wantError {
				require.NotNil(t, readErr)
				require.Equal(t, readErrorInvalidToolInput, readErr.Code)
				require.Equal(t, readFieldQuery, readErr.Field)
				return
			}
			require.Nil(t, readErr)
			require.Equal(t, test.query, input.Query)
		})
	}
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

	execute, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: "dagu://reference/execute-tool"})
	require.NoError(t, err)
	require.Len(t, execute.Contents, 1)
	require.Contains(t, execute.Contents[0].Text, "noReuse")

	authoring, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: "dagu://reference/authoring"})
	require.NoError(t, err)
	require.Len(t, authoring.Contents, 1)
	require.Contains(t, authoring.Contents[0].Text, "human.task")
	require.Contains(t, authoring.Contents[0].Text, "Human task form properties")
	require.Contains(t, authoring.Contents[0].Text, "type: build")
	require.Contains(t, authoring.Contents[0].Text, "${outputs.name}")
	require.Contains(t, authoring.Contents[0].Text, "Build workflows are local-only")

	prompts, err := session.ListPrompts(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(prompts.Prompts))
	for _, prompt := range prompts.Prompts {
		names = append(names, prompt.Name)
	}
	require.Contains(t, names, "dagu_create_dag")
	require.Contains(t, names, "dagu_edit_dag")
	require.Contains(t, names, "dagu_create_wiki_page")
	require.Contains(t, names, "dagu_edit_wiki_page")
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

func TestWikiPagePromptsIncludeRequiredUpsertFields(t *testing.T) {
	ctx := context.Background()
	session := connectTestClient(t, ctx, NewServer(nil))

	tests := []struct {
		name           string
		arguments      map[string]string
		request        string
		wantFieldBlock string
	}{
		{
			name: "dagu_create_wiki_page",
			arguments: map[string]string{
				"workspace": "operations",
				"path":      "runbooks/restart",
				"goal":      "Describe a safe restart.",
			},
			request:        "Describe a safe restart.",
			wantFieldBlock: "mode=preview, type=upsert_wiki_page, workspace=operations, path=runbooks/restart, and content set to the complete drafted Markdown",
		},
		{
			name: "dagu_edit_wiki_page",
			arguments: map[string]string{
				"workspace": "operations",
				"path":      "runbooks/restart",
				"change":    "Add the rollback steps.",
			},
			request:        "Add the rollback steps.",
			wantFieldBlock: "mode=preview, type=upsert_wiki_page, workspace=operations, path=runbooks/restart, and content set to the complete edited Markdown",
		},
		{
			name: "dagu_create_doc",
			arguments: map[string]string{
				"workspace": "operations",
				"path":      "runbooks/restart",
				"goal":      "Describe a safe restart.",
			},
			request:        "Describe a safe restart.",
			wantFieldBlock: "mode=preview, type=upsert_wiki_page, workspace=operations, path=runbooks/restart, and content set to the complete drafted Markdown",
		},
		{
			name: "dagu_edit_doc",
			arguments: map[string]string{
				"workspace": "operations",
				"path":      "runbooks/restart",
				"change":    "Add the rollback steps.",
			},
			request:        "Add the rollback steps.",
			wantFieldBlock: "mode=preview, type=upsert_wiki_page, workspace=operations, path=runbooks/restart, and content set to the complete edited Markdown",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := session.GetPrompt(ctx, &mcpsdk.GetPromptParams{
				Name:      test.name,
				Arguments: test.arguments,
			})
			require.NoError(t, err)
			require.Len(t, result.Messages, 1)

			data, err := result.Messages[0].Content.MarshalJSON()
			require.NoError(t, err)
			var content struct {
				Text string `json:"text"`
			}
			require.NoError(t, json.Unmarshal(data, &content))

			requestText, instruction, ok := strings.Cut(content.Text, "\n\nCall dagu_change with ")
			require.True(t, ok)
			require.Contains(t, requestText, test.request)
			fieldBlock, _, ok := strings.Cut(instruction, ". Apply only ")
			require.True(t, ok)
			require.Equal(t, test.wantFieldBlock, fieldBlock)
		})
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
