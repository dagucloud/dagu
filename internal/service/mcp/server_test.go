// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
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

func TestChangeToolRenamesAndDeletesDAG(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewFileDAGRepository(t.TempDir(), filedag.WithSkipExamples(true))
	require.NoError(t, store.Create(ctx, "mcp-original", []byte(`
name: mcp-original
steps:
  - run: echo hello
`)))
	require.NoError(t, store.Create(ctx, "mcp-existing", []byte(`
name: mcp-existing
steps:
  - run: echo existing
`)))
	cfg := &config.Config{}
	cfg.Server.Permissions = map[config.Permission]bool{config.PermissionWriteDAGs: true}
	api := frontendapi.New(
		store,
		nil,
		nil,
		nil,
		runtime.Manager{},
		cfg,
		nil,
		nil,
		prometheus.NewRegistry(),
		nil,
	)
	session := connectTestClient(t, ctx, NewServer(api))

	conflict := callTool(t, ctx, session, toolChange, changeInput{
		Type:    changeTypeRenameDAG,
		Name:    "mcp-original",
		NewName: "mcp-existing",
	})
	require.True(t, conflict.IsError)
	require.Equal(t, changeErrorConflict, structuredMap(t, conflict)["code"])

	rename := changeInput{
		Type:    changeTypeRenameDAG,
		Name:    "mcp-original",
		NewName: "mcp-renamed",
	}
	preview := callTool(t, ctx, session, toolChange, rename)
	require.False(t, preview.IsError)
	require.Equal(t, false, structuredMap(t, preview)["applied"])
	_, err := store.GetSpec(ctx, rename.Name)
	require.NoError(t, err)
	_, err = store.GetSpec(ctx, rename.NewName)
	require.ErrorIs(t, err, persis.ErrDAGNotFound)

	rename.Mode = changeModeApply
	applied := callTool(t, ctx, session, toolChange, rename)
	require.False(t, applied.IsError)
	require.Equal(t, true, structuredMap(t, applied)["applied"])
	require.Equal(t, dagSpecURI(rename.NewName), structuredMap(t, applied)["newDagUri"])
	require.NotContains(t, structuredMap(t, applied), "dagUri")
	_, err = store.GetSpec(ctx, rename.Name)
	require.ErrorIs(t, err, persis.ErrDAGNotFound)
	spec, err := store.GetSpec(ctx, rename.NewName)
	require.NoError(t, err)
	require.Contains(t, spec, "name: mcp-original")

	remove := changeInput{Type: changeTypeDeleteDAG, Name: rename.NewName}
	preview = callTool(t, ctx, session, toolChange, remove)
	require.False(t, preview.IsError)
	_, err = store.GetSpec(ctx, remove.Name)
	require.NoError(t, err)

	remove.Mode = changeModeApply
	deleted := callTool(t, ctx, session, toolChange, remove)
	require.False(t, deleted.IsError)
	require.Equal(t, true, structuredMap(t, deleted)["applied"])
	require.NotContains(t, structuredMap(t, deleted), "dagUri")
	_, err = store.GetSpec(ctx, remove.Name)
	require.ErrorIs(t, err, persis.ErrDAGNotFound)

	missing := callTool(t, ctx, session, toolChange, remove)
	require.True(t, missing.IsError)
	require.Equal(t, changeErrorResourceNotFound, structuredMap(t, missing)["code"])
}

func TestDAGChangeInputValidation(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantField string
	}{
		{
			name:  "rename",
			input: `{"type":"rename_dag","name":"old","newName":"new"}`,
		},
		{
			name:  "delete",
			input: `{"type":"delete_dag","name":"old"}`,
		},
		{
			name:      "rename requires destination",
			input:     `{"type":"rename_dag","name":"old"}`,
			wantField: changeFieldNewName,
		},
		{
			name:      "rename rejects blank destination",
			input:     `{"type":"rename_dag","name":"old","newName":" "}`,
			wantField: changeFieldNewName,
		},
		{
			name:      "rename requires different destination",
			input:     `{"type":"rename_dag","name":"old","newName":"old"}`,
			wantField: changeFieldNewName,
		},
		{
			name:      "rename validates destination",
			input:     `{"type":"rename_dag","name":"old","newName":"bad name"}`,
			wantField: changeFieldNewName,
		},
		{
			name:      "delete rejects spec",
			input:     `{"type":"delete_dag","name":"old","spec":"steps: []"}`,
			wantField: changeFieldSpec,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input, changeErr := parseChangeToolInput(json.RawMessage(test.input))
			if test.wantField == "" {
				require.Nil(t, changeErr)
				require.Equal(t, changeModePreview, input.Mode)
				return
			}
			require.NotNil(t, changeErr)
			require.Equal(t, changeErrorInvalidToolInput, changeErr.Code)
			require.Equal(t, test.wantField, changeErr.Field)
		})
	}
}

func TestRetryRequiresStepNameForIncludeDownstream(t *testing.T) {
	t.Parallel()

	_, executeErr := parseExecuteToolInput(json.RawMessage(
		`{"action":"retry","name":"example","dagRunId":"run-1","includeDownstream":true}`,
	))
	require.NotNil(t, executeErr)
	require.Equal(t, executeErrorInvalidToolInput, executeErr.Code)
	require.Equal(t, executeFieldStepName, executeErr.Field)

	input, executeErr := parseExecuteToolInput(json.RawMessage(
		`{"action":"retry","name":"example","dagRunId":"run-1","stepName":"build","includeDownstream":true}`,
	))
	require.Nil(t, executeErr)
	require.True(t, input.IncludeDownstream)

	svc := &Service{}
	err := svc.retryDAGRun(context.Background(), executeInput{
		Name:              "example",
		DAGRunID:          "run-1",
		IncludeDownstream: true,
	})
	require.EqualError(t, err, "includeDownstream requires stepName")
}

func TestExecuteInputValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantField string
	}{
		{name: "missing action", input: `{}`, wantField: executeFieldAction},
		{name: "unknown action", input: `{"action":"restart"}`, wantField: executeFieldAction},
		{name: "unknown field", input: `{"action":"start","name":"etl","force":true}`, wantField: "force"},
		{name: "start requires name", input: `{"action":"start"}`, wantField: executeFieldName},
		{name: "inline spec requires spec", input: `{"action":"start","targetType":"inline_spec","name":"inline"}`, wantField: executeFieldSpec},
		{name: "inline spec requires name", input: `{"action":"start","spec":"steps: []"}`, wantField: executeFieldName},
		{name: "stop requires dagRunId", input: `{"action":"stop","name":"etl"}`, wantField: executeFieldDAGRunID},
		{name: "run target only for run actions", input: `{"action":"start","name":"etl","targetType":"run"}`, wantField: executeFieldTargetType},
		{name: "stored DAG rejects spec", input: `{"action":"start","targetType":"dag","name":"etl","spec":"steps: []"}`, wantField: executeFieldSpec},
		{name: "stored DAG rejects empty spec", input: `{"action":"start","targetType":"dag","name":"etl","spec":""}`, wantField: executeFieldSpec},
		{name: "start rejects queue", input: `{"action":"start","name":"etl","queue":"batch"}`, wantField: executeFieldQueue},
		{name: "enqueue rejects step name", input: `{"action":"enqueue","name":"etl","stepName":"build"}`, wantField: executeFieldStepName},
		{name: "retry rejects params", input: `{"action":"retry","name":"etl","dagRunId":"run-1","params":"x=1"}`, wantField: executeFieldParams},
		{name: "retry rejects disabled no reuse", input: `{"action":"retry","name":"etl","dagRunId":"run-1","noReuse":false}`, wantField: executeFieldNoReuse},
		{name: "stop rejects step name", input: `{"action":"stop","name":"etl","dagRunId":"run-1","stepName":"build"}`, wantField: executeFieldStepName},
		{name: "stop rejects disabled downstream", input: `{"action":"stop","name":"etl","dagRunId":"run-1","includeDownstream":false}`, wantField: executeFieldIncludeDownstream},
		{name: "stop rejects empty labels", input: `{"action":"stop","name":"etl","dagRunId":"run-1","labels":[]}`, wantField: executeFieldLabels},
		{name: "wait timeout requires wait", input: `{"action":"start","name":"etl","waitTimeoutSeconds":30}`, wantField: executeFieldWaitTimeoutSeconds},
		{name: "wait timeout rejects zero", input: `{"action":"start","name":"etl","wait":true,"waitTimeoutSeconds":0}`, wantField: executeFieldWaitTimeoutSeconds},
		{name: "wait timeout range", input: `{"action":"start","name":"etl","wait":true,"waitTimeoutSeconds":301}`, wantField: executeFieldWaitTimeoutSeconds},
		{name: "params must be string or object", input: `{"action":"start","name":"etl","params":[1]}`, wantField: executeFieldParams},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, executeErr := parseExecuteToolInput(json.RawMessage(test.input))
			require.NotNil(t, executeErr)
			require.Equal(t, executeErrorInvalidToolInput, executeErr.Code)
			require.Equal(t, test.wantField, executeErr.Field)
		})
	}
}

func TestExecuteInputDefaultsAndParams(t *testing.T) {
	t.Parallel()

	input, executeErr := parseExecuteToolInput(json.RawMessage(
		`{"action":"start","name":"etl","params":{"TARGET":"orders","LIMIT":10}}`,
	))
	require.Nil(t, executeErr)
	require.Equal(t, executeTargetTypeDAG, input.TargetType)
	require.JSONEq(t, `{"TARGET":"orders","LIMIT":10}`, input.Params)

	input, executeErr = parseExecuteToolInput(json.RawMessage(
		`{"action":"start","name":"etl","params":"  KEY=value  "}`,
	))
	require.Nil(t, executeErr)
	require.Equal(t, "KEY=value", input.Params)

	input, executeErr = parseExecuteToolInput(json.RawMessage(
		`{"action":"retry","name":"etl","dagRunId":"run-1","params":"  "}`,
	))
	require.Nil(t, executeErr)
	require.Empty(t, input.Params)

	input, executeErr = parseExecuteToolInput(json.RawMessage(
		`{"action":"enqueue","name":"inline","spec":"steps: []"}`,
	))
	require.Nil(t, executeErr)
	require.Equal(t, executeTargetTypeInlineSpec, input.TargetType)

	input, executeErr = parseExecuteToolInput(json.RawMessage(
		`{"action":"stop","name":"etl","dagRunId":"run-1"}`,
	))
	require.Nil(t, executeErr)
	require.Equal(t, executeTargetTypeRun, input.TargetType)
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

func TestExecuteBodiesUseRequestedDAGName(t *testing.T) {
	t.Parallel()

	input := executeInput{Name: "renamed-dag"}
	startBody := executeBody(input)
	require.NotNil(t, startBody.DagName)
	require.Equal(t, input.Name, *startBody.DagName)

	enqueueBody := enqueueBody(input)
	require.NotNil(t, enqueueBody.DagName)
	require.Equal(t, input.Name, *enqueueBody.DagName)
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
}

func TestStepLogQueryValidation(t *testing.T) {
	t.Parallel()

	valid := []string{"tail=10000", "head=10000", "offset=10001&limit=10000", "limit=10", "stream=stderr", "tail=5&stream=stdout"}
	for _, query := range valid {
		input, readErr := parseReadResourceURI("dagu://runs/demo/run-1/steps/main/logs?" + query)
		require.Nil(t, readErr, query)
		require.Equal(t, readTargetStepLog, input.Target)
		require.Equal(t, query, input.Query)
	}

	invalid := []string{
		"tail=0",
		"tail=x",
		"head=10001",
		"tail=10001",
		"limit=10001",
		"tail=10&head=10",
		"tail=10&limit=10",
		"head=10&offset=10",
		"stream=both",
		"unknown=1",
	}
	for _, query := range invalid {
		_, readErr := parseReadResourceURI("dagu://runs/demo/run-1/steps/main/logs?" + query)
		require.NotNil(t, readErr, query)
		require.Equal(t, readErrorInvalidResourceURI, readErr.Code, query)
	}

	input, readErr := parseReadToolInput(json.RawMessage(`{
		"target":"step_log",
		"name":"demo",
		"dagRunId":"run-1",
		"stepName":"main",
		"query":"tail=25&stream=stderr"
	}`))
	require.Nil(t, readErr)
	require.Equal(t, "dagu://runs/demo/run-1/steps/main/logs?tail=25&stream=stderr", input.URI)

	opts := stepLogReadOptions(input.Query)
	require.Equal(t, 25, opts.Tail)
	require.Equal(t, "stderr", opts.Stream)
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
	require.Contains(t, names, "dagu_debug_failed_run")
}

func TestReadToolListsAndReadsAltDAGsDir(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	altDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(altDir, "alt-dag.yaml"), []byte("name: alt-dag\nsteps: []\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "main-dag.yaml"), []byte("name: main-dag\nsteps: []\n"), 0600))

	cfg := &config.Config{}
	cfg.Paths.DAGsDir = baseDir
	cfg.Paths.AltDAGsDir = altDir
	cfg.Core.SkipExamples = true

	repo, err := persisfile.NewDAGRepository(cfg, persisfile.WithDAGSearchPaths([]string{altDir}))
	require.NoError(t, err)
	api := frontendapi.New(repo, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)
	session := connectTestClient(t, ctx, NewServer(api))

	list := callTool(t, ctx, session, toolRead, readInput{Target: readTargetDAGs})
	require.False(t, list.IsError)
	require.NotEmpty(t, list.Content)
	listText, ok := list.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok)
	require.Contains(t, listText.Text, "Dagu read completed.")
	require.Contains(t, listText.Text, `"name": "alt-dag"`)
	require.Contains(t, listText.Text, `"uri": "dagu://dags/alt-dag/spec"`)
	require.Contains(t, listText.Text, `"name": "main-dag"`)
	require.Contains(t, listText.Text, `"uri": "dagu://dags/main-dag/spec"`)
	listJSON := structuredJSON(t, list)
	require.Contains(t, listJSON, "dagu://dags/alt-dag/spec")
	require.Contains(t, listJSON, "dagu://dags/main-dag/spec")

	filtered := callTool(t, ctx, session, toolRead, readInput{Target: readTargetDAGs, Query: "name=alt-dag"})
	require.False(t, filtered.IsError)
	filteredJSON := structuredJSON(t, filtered)
	require.Contains(t, filteredJSON, "dagu://dags/alt-dag/spec")
	require.NotContains(t, filteredJSON, "dagu://dags/main-dag/spec")

	spec := callTool(t, ctx, session, toolRead, readInput{Target: readTargetDAGSpec, Name: "alt-dag"})
	require.False(t, spec.IsError)
	require.Contains(t, structuredJSON(t, spec), "name: alt-dag")
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

func TestRankNameSuggestions(t *testing.T) {
	t.Parallel()

	candidates := []string{"nightly-report", "nightly-cleanup", "billing-export", "etl"}

	require.Equal(t, []string{"nightly-report"}, rankNameSuggestions("nightly-reprot", candidates))
	// Substring matches rank ahead of larger edit distances.
	require.Equal(t, []string{"nightly-cleanup", "nightly-report"}, rankNameSuggestions("nightly", candidates))
	// The exact name is not suggested back and unrelated names are dropped.
	require.Empty(t, rankNameSuggestions("etl", []string{"etl"}))
	require.Empty(t, rankNameSuggestions("zzzz", candidates))
}

func TestReadToolNotFoundSuggestsCloseDAGNames(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewFileDAGRepository(t.TempDir(), filedag.WithSkipExamples(true))
	require.NoError(t, store.Create(ctx, "nightly-report", []byte("name: nightly-report\nsteps: []\n")))
	api := frontendapi.New(store, nil, nil, nil, runtime.Manager{}, &config.Config{}, nil, nil, prometheus.NewRegistry(), nil)
	session := connectTestClient(t, ctx, NewServer(api))

	result := callTool(t, ctx, session, toolRead, readInput{Target: readTargetDAGSpec, Name: "nightly-reprot"})
	require.True(t, result.IsError)
	output := structuredMap(t, result)
	require.Equal(t, readErrorResourceNotFound, output["code"])
	details, ok := output["details"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{"nightly-report"}, details["didYouMean"])
}

func TestDAGSearchInputValidation(t *testing.T) {
	t.Parallel()

	input, readErr := parseReadToolInput(json.RawMessage(`{"target":"dag_search","search":"postgres","limit":10}`))
	require.Nil(t, readErr)
	require.Equal(t, readTargetDAGSearch, input.Target)
	require.Equal(t, "postgres", input.Search)
	require.Equal(t, "all", input.Workspace)
	require.Equal(t, 10, input.Limit)

	_, readErr = parseReadToolInput(json.RawMessage(`{"target":"dag_search"}`))
	require.NotNil(t, readErr)
	require.Equal(t, readErrorInvalidToolInput, readErr.Code)
	require.Equal(t, readFieldSearch, readErr.Field)

	_, readErr = parseReadToolInput(json.RawMessage(`{"target":"dag_search","search":"x","prefix":"guides"}`))
	require.NotNil(t, readErr)
	require.Equal(t, readFieldPrefix, readErr.Field)

	_, readErr = parseReadToolInput(json.RawMessage(`{"target":"dag_search","search":"x","query":"page=1"}`))
	require.NotNil(t, readErr)
	require.Equal(t, readFieldQuery, readErr.Field)
}

func TestNormalizeRunDetailsIncludesStepsAndFailureDetails(t *testing.T) {
	t.Parallel()

	stepError := "exit status 1"
	queuedAt := "2026-08-23T01:00:00Z"
	params := `{"TARGET":"orders"}`
	raw := daguapi.GetDAGRunDetails200JSONResponse{
		DagRunDetails: daguapi.DAGRunDetails{
			Name:        "etl",
			DagRunId:    "run-1",
			Status:      2,
			StatusLabel: "failed",
			StartedAt:   "2026-08-23T01:00:05Z",
			FinishedAt:  "2026-08-23T01:00:09Z",
			QueuedAt:    &queuedAt,
			Params:      &params,
			Nodes: []daguapi.Node{
				{
					Step:        daguapi.Step{Name: "extract"},
					Status:      4,
					StatusLabel: "finished",
					StartedAt:   "2026-08-23T01:00:05Z",
					FinishedAt:  "2026-08-23T01:00:06Z",
				},
				{
					Step:        daguapi.Step{Name: "load"},
					Status:      2,
					StatusLabel: "failed",
					Error:       &stepError,
				},
			},
			OnFailure: &daguapi.Node{
				Step:        daguapi.Step{Name: "onFailure"},
				Status:      4,
				StatusLabel: "finished",
			},
		},
	}

	data, err := normalizeRunDetails(raw, runAddress{})
	require.NoError(t, err)
	require.Equal(t, "etl", data["name"])
	require.Equal(t, "run-1", data["dagRunId"])
	require.Equal(t, "dagu://runs/etl/run-1", data["uri"])
	require.Equal(t, "dagu://runs/etl/run-1/logs", data["logsUri"])
	require.Equal(t, "2026-08-23T01:00:05Z", data["startedAt"])
	require.Equal(t, "2026-08-23T01:00:09Z", data["finishedAt"])
	require.Equal(t, queuedAt, data["queuedAt"])
	require.Equal(t, params, data["params"])
	require.NotContains(t, data, "rootRun")
	require.NotContains(t, data, "parentRun")

	steps, ok := data["steps"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, steps, 2)
	require.Equal(t, "extract", steps[0]["name"])
	require.NotContains(t, steps[0], "error")
	require.Equal(t, "load", steps[1]["name"])
	require.Equal(t, stepError, steps[1]["error"])
	require.Equal(t, "dagu://runs/etl/run-1/steps/load/logs", steps[1]["logUri"])
	require.NotContains(t, steps[1], "startedAt")

	handlers, ok := data["handlers"].(map[string]any)
	require.True(t, ok)
	require.Len(t, handlers, 1)
	handler, ok := handlers["onFailure"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "onFailure", handler["name"])
}

func TestNormalizeRunDetailsIncludesRunHierarchy(t *testing.T) {
	t.Parallel()

	parentID := "parent-run"
	parentName := "root-dag"
	subDAGName := "child-dag"
	repeatedSubDAGName := "repeated-child-dag"
	raw := daguapi.GetDAGRunDetails200JSONResponse{
		DagRunDetails: daguapi.DAGRunDetails{
			Name:             "middle-dag",
			DagRunId:         "run-2",
			Status:           4,
			StatusLabel:      "finished",
			RootDAGRunId:     "root-run",
			RootDAGRunName:   "root-dag",
			ParentDAGRunId:   &parentID,
			ParentDAGRunName: &parentName,
			Nodes: []daguapi.Node{
				{
					Step:        daguapi.Step{Name: "call-child"},
					Status:      4,
					StatusLabel: "finished",
					SubRuns: &[]daguapi.SubDAGRun{
						{DagName: &subDAGName, DagRunId: "child-run"},
					},
					SubRunsRepeated: &[]daguapi.SubDAGRun{
						{DagName: &repeatedSubDAGName, DagRunId: "repeated-child-run"},
					},
				},
			},
		},
	}

	data, err := normalizeRunDetails(raw, runAddress{})
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"name":     "root-dag",
		"dagRunId": "root-run",
		"uri":      "dagu://runs/root-dag/root-run",
	}, data["rootRun"])
	require.Equal(t, map[string]any{"name": "root-dag", "dagRunId": "parent-run"}, data["parentRun"])

	steps, ok := data["steps"].([]map[string]any)
	require.True(t, ok)
	subRuns, ok := steps[0]["subRuns"].([]map[string]any)
	require.True(t, ok)
	require.Equal(t, []map[string]any{
		{
			"dagName":  "child-dag",
			"dagRunId": "child-run",
			"uri":      "dagu://runs/middle-dag/run-2/sub/child-run",
		},
		{
			"dagName":  "repeated-child-dag",
			"dagRunId": "repeated-child-run",
			"uri":      "dagu://runs/middle-dag/run-2/sub/repeated-child-run",
		},
	}, subRuns)
}

func TestNormalizeRunListIncludesTimestampsAndCursor(t *testing.T) {
	t.Parallel()

	cursor := "opaque-cursor"
	raw := daguapi.DAGRunsPageResponse{
		DagRuns: []daguapi.DAGRunSummary{
			{
				Name:        "etl",
				DagRunId:    "run-1",
				Status:      4,
				StatusLabel: "finished",
				StartedAt:   "2026-08-23T01:00:05Z",
				FinishedAt:  "2026-08-23T01:00:09Z",
			},
			{
				Name:        "etl",
				DagRunId:    "run-0",
				Status:      0,
				StatusLabel: "not started",
				StartedAt:   "-",
				FinishedAt:  "-",
			},
		},
		NextCursor: &cursor,
	}

	data, err := normalizeRunList(raw)
	require.NoError(t, err)
	require.Equal(t, cursor, data["nextCursor"])

	items, ok := data["items"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, items, 2)
	require.Equal(t, "2026-08-23T01:00:05Z", items[0]["startedAt"])
	require.Equal(t, "2026-08-23T01:00:09Z", items[0]["finishedAt"])
	require.NotContains(t, items[1], "startedAt")
	require.NotContains(t, items[1], "finishedAt")
}

func TestNormalizeDAGListIncludesSummaryFields(t *testing.T) {
	t.Parallel()

	description := "Nightly ETL"
	nextRun := time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC)
	raw := daguapi.ListDAGs200JSONResponse{
		Dags: []daguapi.DAGFile{
			{
				FileName: "etl",
				Dag: daguapi.DAG{
					Name:        "etl",
					Description: &description,
					Schedule:    &[]daguapi.Schedule{{Expression: "0 2 * * *"}},
				},
				Suspended: true,
				NextRun:   &nextRun,
				LatestDAGRun: daguapi.DAGRunSummary{
					Name:        "etl",
					DagRunId:    "run-1",
					Status:      2,
					StatusLabel: "failed",
					StartedAt:   "2026-08-23T02:00:00Z",
					FinishedAt:  "2026-08-23T02:00:05Z",
				},
			},
			{FileName: "never-ran", Dag: daguapi.DAG{Name: "never-ran"}},
		},
		Pagination: daguapi.Pagination{CurrentPage: 1, TotalPages: 1, TotalRecords: 2},
	}

	data, err := normalizeDAGList(raw)
	require.NoError(t, err)
	require.Equal(t, daguapi.Pagination{CurrentPage: 1, TotalPages: 1, TotalRecords: 2}, data["pagination"])

	items, ok := data["items"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, items, 2)

	require.Equal(t, "etl", items[0]["name"])
	require.Equal(t, description, items[0]["description"])
	require.Equal(t, []string{"0 2 * * *"}, items[0]["schedule"])
	require.Equal(t, true, items[0]["suspended"])
	require.Equal(t, "2026-08-24T02:00:00Z", items[0]["nextRun"])
	latest, ok := items[0]["latestRun"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "run-1", latest["dagRunId"])
	require.Equal(t, "dagu://runs/etl/run-1", latest["uri"])

	require.Equal(t, "never-ran", items[1]["name"])
	require.NotContains(t, items[1], "latestRun")
	require.NotContains(t, items[1], "description")
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
