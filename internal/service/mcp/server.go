// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dagstore"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	toolRead    = "dagu_read"
	toolChange  = "dagu_change"
	toolExecute = "dagu_execute"

	resourceMIMEJSON = "application/json"
	resourceMIMEText = "text/markdown"
	resourceMIMEYAML = "application/yaml"

	defaultRunWatchPollInterval = 2 * time.Second
	defaultRunWatchMaxErrors    = 30
)

// Service owns the Dagu MCP server and the small amount of state needed for
// resource subscriptions.
type Service struct {
	api *frontendapi.API

	mu       sync.Mutex
	server   *mcpsdk.Server
	watchers map[string]*resourceWatcher
	nextID   uint64

	watchPollInterval time.Duration
	watchMaxErrors    int
}

type resourceWatcher struct {
	id     uint64
	cancel func()
	refs   int
}

// NewHTTPHandler returns a Streamable HTTP MCP handler backed by the Dagu API.
func NewHTTPHandler(api *frontendapi.API) http.Handler {
	server := NewServer(api)
	return mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return server },
		&mcpsdk.StreamableHTTPOptions{JSONResponse: true},
	)
}

// NewServer builds the MCP server used by the Streamable HTTP transport.
func NewServer(api *frontendapi.API) *mcpsdk.Server {
	svc := &Service{
		api:               api,
		watchers:          make(map[string]*resourceWatcher),
		watchPollInterval: defaultRunWatchPollInterval,
		watchMaxErrors:    defaultRunWatchMaxErrors,
	}
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "dagu",
		Version: config.Version,
	}, &mcpsdk.ServerOptions{
		Capabilities: &mcpsdk.ServerCapabilities{
			Extensions: map[string]any{
				mcpAppsExtensionURI: mcpAppsCapability(),
			},
			Prompts:   &mcpsdk.PromptCapabilities{},
			Resources: &mcpsdk.ResourceCapabilities{Subscribe: true},
			Tools:     &mcpsdk.ToolCapabilities{},
		},
		Instructions:       instructions,
		SubscribeHandler:   svc.subscribe,
		UnsubscribeHandler: svc.unsubscribe,
	})
	svc.server = server

	registerTools(server, svc)
	registerResources(server, svc)
	registerPrompts(server)

	return server
}

type changeInput struct {
	Mode      string `json:"mode,omitempty" jsonschema:"preview or apply. Defaults to preview."`
	Type      string `json:"type,omitempty" jsonschema:"Change type: upsert_dag, upsert_doc, rename_doc, or delete_doc."`
	Name      string `json:"name,omitempty" jsonschema:"DAG name for upsert_dag."`
	Spec      string `json:"spec,omitempty" jsonschema:"DAG YAML specification for upsert_dag."`
	Workspace string `json:"workspace,omitempty" jsonschema:"Document workspace for document changes: default or a named workspace."`
	Path      string `json:"path,omitempty" jsonschema:"Document or directory path for document changes."`
	Content   string `json:"content,omitempty" jsonschema:"Markdown content for upsert_doc."`
	NewPath   string `json:"newPath,omitempty" jsonschema:"Destination document or directory path for rename_doc."`
}

type executeInput struct {
	Action     string   `json:"action" jsonschema:"Execution action: start, enqueue, retry, or stop."`
	TargetType string   `json:"targetType,omitempty" jsonschema:"Target type: dag, inline_spec, or run. Defaults from action and spec."`
	Name       string   `json:"name,omitempty" jsonschema:"DAG name, or optional inline spec name."`
	Spec       string   `json:"spec,omitempty" jsonschema:"Inline DAG YAML spec for start or enqueue with targetType inline_spec."`
	DAGRunID   string   `json:"dagRunId,omitempty" jsonschema:"DAG-run ID for start/enqueue override, retry, and stop."`
	Params     string   `json:"params,omitempty" jsonschema:"Runtime parameters as a JSON string."`
	Queue      string   `json:"queue,omitempty" jsonschema:"Queue override for enqueue."`
	Singleton  bool     `json:"singleton,omitempty" jsonschema:"Prevent duplicate running or queued DAG-runs when supported by the action."`
	NoReuse    bool     `json:"noReuse,omitempty" jsonschema:"Execute eligible incremental steps without reusing prior materializations for start or enqueue."`
	Labels     []string `json:"labels,omitempty" jsonschema:"Additional labels, each as key=value or key-only."`
	StepName   string   `json:"stepName,omitempty" jsonschema:"Optional step name for retry."`
}

func registerTools(server *mcpsdk.Server, svc *Service) {
	falsePtr := new(false)
	truePtr := new(true)

	server.AddTool(&mcpsdk.Tool{
		Meta:        runInspectorToolMeta(),
		Name:        toolRead,
		Title:       "Read Dagu state",
		Description: "Read DAG specs, workspace-aware Markdown documents, DAG-run details, logs, list views, and Dagu MCP reference resources.",
		InputSchema: readToolInputSchema(),
		Annotations: &mcpsdk.ToolAnnotations{
			OpenWorldHint: falsePtr,
			ReadOnlyHint:  true,
			Title:         "Read Dagu state",
		},
	}, svc.readTool)

	server.AddTool(&mcpsdk.Tool{
		Name:        toolChange,
		Title:       "Preview or apply Dagu changes",
		Description: "Validate and optionally apply DAG YAML or Markdown document changes. Document changes are workspace-aware. Use mode=preview before mode=apply unless the user explicitly asked to write immediately.",
		InputSchema: changeToolInputSchema(),
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: truePtr,
			OpenWorldHint:   falsePtr,
			Title:           "Preview or apply Dagu changes",
		},
	}, svc.changeTool)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Meta:        runInspectorToolMeta(),
		Name:        toolExecute,
		Title:       "Execute, enqueue, retry, or stop DAG-runs",
		Description: "Run control entry point. action=start or enqueue launches a DAG or inline spec; action=retry retries a DAG-run; action=stop terminates a DAG-run.",
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: truePtr,
			OpenWorldHint:   falsePtr,
			Title:           "Execute, enqueue, retry, or stop DAG-runs",
		},
	}, svc.executeTool)
}

func registerResources(server *mcpsdk.Server, svc *Service) {
	server.AddResource(&mcpsdk.Resource{
		Meta:        runInspectorResourceMeta(),
		URI:         runInspectorURI,
		Name:        runInspectorResource,
		Title:       "Dagu run inspector",
		Description: "Interactive run status, step, and log view for MCP Apps hosts.",
		MIMEType:    mcpAppMIMEType,
	}, svc.readResource)

	for _, ref := range referenceResources() {
		server.AddResource(&mcpsdk.Resource{
			URI:         ref.uri,
			Name:        ref.name,
			Title:       ref.title,
			Description: ref.description,
			MIMEType:    resourceMIMEText,
		}, svc.readResource)
	}

	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "dagu://dags/{name}/spec",
		Name:        "dag_spec",
		Title:       "DAG spec",
		Description: "Current YAML spec for a DAG.",
		MIMEType:    resourceMIMEYAML,
	}, svc.readResource)

	server.AddResource(&mcpsdk.Resource{
		URI:         readResourceDocsCollectionURI,
		Name:        "documents",
		Title:       "Documents",
		Description: "Documents visible across accessible workspaces.",
		MIMEType:    resourceMIMEJSON,
	}, svc.readResource)

	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "dagu://docs/{workspace}",
		Name:        "workspace_documents",
		Title:       "Workspace documents",
		Description: "Document tree for default or one named workspace.",
		MIMEType:    resourceMIMEJSON,
	}, svc.readResource)

	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "dagu://docs/{workspace}/{path}",
		Name:        "document",
		Title:       "Markdown document",
		Description: "Current Markdown content for a document in default or one named workspace. Nested paths are encoded as one URI segment.",
		MIMEType:    resourceMIMEText,
	}, svc.readResource)

	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "dagu://runs/{name}/{dagRunId}",
		Name:        "dag_run",
		Title:       "DAG-run details",
		Description: "Current DAG-run details. Clients may subscribe to receive a resource update notification when the run reaches a terminal state.",
		MIMEType:    resourceMIMEJSON,
	}, svc.readResource)

	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "dagu://runs/{name}/{dagRunId}/logs",
		Name:        "dag_run_logs",
		Title:       "DAG-run logs",
		Description: "DAG-run logs. Supports query parameters accepted by Dagu log readers, such as tail=100.",
		MIMEType:    resourceMIMEJSON,
	}, svc.readResource)

	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "dagu://runs/{name}/{dagRunId}/steps/{stepName}/logs",
		Name:        "dag_run_step_log",
		Title:       "DAG-run step log",
		Description: "Standard output and standard error for a DAG-run step.",
		MIMEType:    resourceMIMEJSON,
	}, svc.readResource)
}

func registerPrompts(server *mcpsdk.Server) {
	server.AddPrompt(&mcpsdk.Prompt{
		Name:        "dagu_create_dag",
		Title:       "Create a Dagu DAG",
		Description: "Draft, validate, and apply a new DAG using Dagu's compact MCP tool surface.",
		Arguments: []*mcpsdk.PromptArgument{
			{Name: "goal", Description: "What the DAG should do.", Required: true},
		},
	}, promptCreateDAG)

	server.AddPrompt(&mcpsdk.Prompt{
		Name:        "dagu_edit_dag",
		Title:       "Edit a Dagu DAG",
		Description: "Read an existing DAG spec, make a scoped edit, preview validation, then apply.",
		Arguments: []*mcpsdk.PromptArgument{
			{Name: "name", Description: "DAG name.", Required: true},
			{Name: "change", Description: "Requested change.", Required: true},
		},
	}, promptEditDAG)

	server.AddPrompt(&mcpsdk.Prompt{
		Name:        "dagu_create_doc",
		Title:       "Create a Dagu document",
		Description: "Draft, preview, and create a workspace-aware Markdown document.",
		Arguments: []*mcpsdk.PromptArgument{
			{Name: "workspace", Description: "default or a workspace name.", Required: true},
			{Name: "path", Description: "Document path without .md.", Required: true},
			{Name: "goal", Description: "What the document should contain.", Required: true},
		},
	}, promptCreateDoc)

	server.AddPrompt(&mcpsdk.Prompt{
		Name:        "dagu_edit_doc",
		Title:       "Edit a Dagu document",
		Description: "Read an existing Markdown document, make a scoped edit, preview, then apply.",
		Arguments: []*mcpsdk.PromptArgument{
			{Name: "workspace", Description: "default or a workspace name.", Required: true},
			{Name: "path", Description: "Document path without .md.", Required: true},
			{Name: "change", Description: "Requested change.", Required: true},
		},
	}, promptEditDoc)

	server.AddPrompt(&mcpsdk.Prompt{
		Name:        "dagu_debug_failed_run",
		Title:       "Debug a failed Dagu run",
		Description: "Read a run and logs, explain the likely failure, then offer retry or stop as appropriate.",
		Arguments: []*mcpsdk.PromptArgument{
			{Name: "name", Description: "DAG name.", Required: true},
			{Name: "dagRunId", Description: "DAG-run ID.", Required: true},
		},
	}, promptDebugRun)
}

func (svc *Service) executeTool(ctx context.Context, req *mcpsdk.CallToolRequest, input executeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	return auditToolCall(ctx, svc.api, req, toolExecute, executeAuditMetadata(input), func(ctx context.Context) (*mcpsdk.CallToolResult, map[string]any, error) {
		return svc.executeToolImpl(ctx, input)
	})
}

func (svc *Service) executeToolImpl(ctx context.Context, input executeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	if err := svc.requireAPI(); err != nil {
		return nil, nil, err
	}

	action := strings.TrimSpace(input.Action)
	if action == "" {
		return nil, nil, errors.New("action is required")
	}

	targetType := strings.TrimSpace(input.TargetType)
	if targetType == "" {
		switch {
		case action == "retry" || action == "stop":
			targetType = "run"
		case strings.TrimSpace(input.Spec) != "":
			targetType = "inline_spec"
		default:
			targetType = "dag"
		}
	}

	var (
		dagRunID string
		err      error
	)

	switch action {
	case "start":
		dagRunID, err = svc.startDAG(ctx, targetType, input)
	case "enqueue":
		dagRunID, err = svc.enqueueDAG(ctx, targetType, input)
	case "retry":
		err = requireRun(input.Name, input.DAGRunID)
		if err == nil {
			err = svc.retryDAGRun(ctx, input)
			dagRunID = input.DAGRunID
		}
	case "stop":
		err = requireRun(input.Name, input.DAGRunID)
		if err == nil {
			err = svc.stopDAGRun(ctx, input)
			dagRunID = input.DAGRunID
		}
	default:
		err = fmt.Errorf("unsupported execute action %q", action)
	}
	if err != nil {
		return nil, nil, err
	}

	output := map[string]any{
		"action":     action,
		"targetType": targetType,
		"dagName":    input.Name,
		"dagRunId":   dagRunID,
		"references": defaultReferenceURIs(),
	}
	links := []resourceLink{}
	if input.Name != "" && dagRunID != "" {
		run := runURI(input.Name, dagRunID)
		logs := runLogsURI(input.Name, dagRunID)
		output["runUri"] = run
		output["logsUri"] = logs
		output["subscribe"] = "Subscribe to " + run + " to receive an MCP resource update notification when the run reaches a terminal state."
		links = append(links, resourceLink{
			uri:         run,
			name:        "dag_run",
			title:       "DAG-run details",
			description: "Subscribe to this resource for completion notification.",
			mimeType:    resourceMIMEJSON,
		}, resourceLink{
			uri:         logs,
			name:        "dag_run_logs",
			title:       "DAG-run logs",
			description: "Logs for this DAG-run.",
			mimeType:    resourceMIMEJSON,
		})
	}

	return resultWithLinks("Dagu execute action completed.", links...), output, nil
}

func (svc *Service) getDAGSpec(ctx context.Context, name string) (map[string]any, error) {
	resp, err := svc.api.GetDAGSpec(ctx, daguapi.GetDAGSpecRequestObject{
		FileName: daguapi.DAGFileName(name),
	})
	if err != nil {
		return nil, err
	}
	switch r := resp.(type) {
	case *daguapi.GetDAGSpec200JSONResponse:
		return map[string]any{"spec": r.Spec, "dag": r.Dag, "errors": r.Errors}, nil
	case daguapi.GetDAGSpec200JSONResponse:
		return map[string]any{"spec": r.Spec, "dag": r.Dag, "errors": r.Errors}, nil
	default:
		return nil, fmt.Errorf("unexpected get DAG spec response %T", resp)
	}
}

func (svc *Service) validateDAGSpec(ctx context.Context, name, spec string) (*daguapi.ValidateDAGSpec200JSONResponse, error) {
	body := &daguapi.ValidateDAGSpecJSONRequestBody{
		Name: &name,
		Spec: spec,
	}
	resp, err := svc.api.ValidateDAGSpec(ctx, daguapi.ValidateDAGSpecRequestObject{Body: body})
	if err != nil {
		return nil, err
	}
	switch r := resp.(type) {
	case *daguapi.ValidateDAGSpec200JSONResponse:
		return r, nil
	case daguapi.ValidateDAGSpec200JSONResponse:
		return &r, nil
	default:
		return nil, fmt.Errorf("unexpected validate DAG spec response %T", resp)
	}
}

func (svc *Service) upsertDAG(ctx context.Context, name, spec string) (bool, error) {
	exists := true
	if _, err := svc.api.GetDAGSpec(ctx, daguapi.GetDAGSpecRequestObject{
		FileName: daguapi.DAGFileName(name),
	}); err != nil {
		if !isDAGNotFound(err) {
			return false, err
		}
		exists = false
	}

	if !exists {
		body := &daguapi.CreateNewDAGJSONRequestBody{
			Name: daguapi.DAGName(name),
			Spec: &spec,
		}
		resp, err := svc.api.CreateNewDAG(ctx, daguapi.CreateNewDAGRequestObject{Body: body})
		if err != nil {
			return false, err
		}
		switch resp.(type) {
		case *daguapi.CreateNewDAG201JSONResponse, daguapi.CreateNewDAG201JSONResponse:
			return true, nil
		default:
			return false, fmt.Errorf("unexpected create DAG response %T", resp)
		}
	}

	body := &daguapi.UpdateDAGSpecJSONRequestBody{Spec: spec}
	resp, err := svc.api.UpdateDAGSpec(ctx, daguapi.UpdateDAGSpecRequestObject{
		FileName: daguapi.DAGFileName(name),
		Body:     body,
	})
	if err != nil {
		return false, err
	}
	switch resp.(type) {
	case *daguapi.UpdateDAGSpec200JSONResponse, daguapi.UpdateDAGSpec200JSONResponse:
		return false, nil
	default:
		return false, fmt.Errorf("unexpected update DAG response %T", resp)
	}
}

func (svc *Service) startDAG(ctx context.Context, targetType string, input executeInput) (string, error) {
	body := executeBody(input)
	switch targetType {
	case "dag":
		if err := requireName(input.Name); err != nil {
			return "", err
		}
		resp, err := svc.api.ExecuteDAG(ctx, daguapi.ExecuteDAGRequestObject{
			FileName: daguapi.DAGFileName(input.Name),
			Body:     body,
		})
		if err != nil {
			return "", err
		}
		switch r := resp.(type) {
		case daguapi.ExecuteDAG200JSONResponse:
			return string(r.DagRunId), nil
		case *daguapi.ExecuteDAG200JSONResponse:
			return string(r.DagRunId), nil
		default:
			return "", fmt.Errorf("unexpected execute DAG response %T", resp)
		}
	case "inline_spec":
		if strings.TrimSpace(input.Spec) == "" {
			return "", errors.New("spec is required for inline_spec target")
		}
		inlineBody := &daguapi.ExecuteDAGRunFromSpecJSONRequestBody{
			DagRunId:  body.DagRunId,
			Labels:    body.Labels,
			Name:      stringPtr(input.Name),
			NoReuse:   body.NoReuse,
			Params:    body.Params,
			Singleton: body.Singleton,
			Spec:      input.Spec,
		}
		resp, err := svc.api.ExecuteDAGRunFromSpec(ctx, daguapi.ExecuteDAGRunFromSpecRequestObject{Body: inlineBody})
		if err != nil {
			return "", err
		}
		switch r := resp.(type) {
		case daguapi.ExecuteDAGRunFromSpec200JSONResponse:
			return string(r.DagRunId), nil
		case *daguapi.ExecuteDAGRunFromSpec200JSONResponse:
			return string(r.DagRunId), nil
		default:
			return "", fmt.Errorf("unexpected execute inline DAG response %T", resp)
		}
	default:
		return "", fmt.Errorf("unsupported start targetType %q", targetType)
	}
}

func (svc *Service) enqueueDAG(ctx context.Context, targetType string, input executeInput) (string, error) {
	body := enqueueBody(input)
	switch targetType {
	case "dag":
		if err := requireName(input.Name); err != nil {
			return "", err
		}
		resp, err := svc.api.EnqueueDAGDAGRun(ctx, daguapi.EnqueueDAGDAGRunRequestObject{
			FileName: daguapi.DAGFileName(input.Name),
			Body:     body,
		})
		if err != nil {
			return "", err
		}
		switch r := resp.(type) {
		case daguapi.EnqueueDAGDAGRun200JSONResponse:
			return string(r.DagRunId), nil
		case *daguapi.EnqueueDAGDAGRun200JSONResponse:
			return string(r.DagRunId), nil
		default:
			return "", fmt.Errorf("unexpected enqueue DAG response %T", resp)
		}
	case "inline_spec":
		if strings.TrimSpace(input.Spec) == "" {
			return "", errors.New("spec is required for inline_spec target")
		}
		inlineBody := &daguapi.EnqueueDAGRunFromSpecJSONRequestBody{
			DagRunId:  body.DagRunId,
			Labels:    body.Labels,
			Name:      stringPtr(input.Name),
			NoReuse:   body.NoReuse,
			Params:    body.Params,
			Queue:     body.Queue,
			Singleton: body.Singleton,
			Spec:      input.Spec,
		}
		resp, err := svc.api.EnqueueDAGRunFromSpec(ctx, daguapi.EnqueueDAGRunFromSpecRequestObject{Body: inlineBody})
		if err != nil {
			return "", err
		}
		switch r := resp.(type) {
		case daguapi.EnqueueDAGRunFromSpec200JSONResponse:
			return string(r.DagRunId), nil
		case *daguapi.EnqueueDAGRunFromSpec200JSONResponse:
			return string(r.DagRunId), nil
		default:
			return "", fmt.Errorf("unexpected enqueue inline DAG response %T", resp)
		}
	default:
		return "", fmt.Errorf("unsupported enqueue targetType %q", targetType)
	}
}

func (svc *Service) retryDAGRun(ctx context.Context, input executeInput) error {
	body := &daguapi.RetryDAGRunJSONRequestBody{DagRunId: input.DAGRunID}
	if input.StepName != "" {
		body.StepName = &input.StepName
	}
	resp, err := svc.api.RetryDAGRun(ctx, daguapi.RetryDAGRunRequestObject{
		Name:     daguapi.DAGName(input.Name),
		DagRunId: daguapi.DAGRunId(input.DAGRunID),
		Body:     body,
	})
	if err != nil {
		return err
	}
	switch resp.(type) {
	case daguapi.RetryDAGRun200Response, *daguapi.RetryDAGRun200Response:
		return nil
	default:
		return fmt.Errorf("unexpected retry DAG-run response %T", resp)
	}
}

func (svc *Service) stopDAGRun(ctx context.Context, input executeInput) error {
	resp, err := svc.api.TerminateDAGRun(ctx, daguapi.TerminateDAGRunRequestObject{
		Name:     daguapi.DAGName(input.Name),
		DagRunId: daguapi.DAGRunId(input.DAGRunID),
	})
	if err != nil {
		return err
	}
	switch resp.(type) {
	case daguapi.TerminateDAGRun200Response, *daguapi.TerminateDAGRun200Response:
		return nil
	default:
		return fmt.Errorf("unexpected stop DAG-run response %T", resp)
	}
}

func executeBody(input executeInput) *daguapi.ExecuteDAGJSONRequestBody {
	body := &daguapi.ExecuteDAGJSONRequestBody{}
	if input.DAGRunID != "" {
		body.DagRunId = &input.DAGRunID
	}
	if input.Params != "" {
		body.Params = &input.Params
	}
	if input.Singleton {
		body.Singleton = &input.Singleton
	}
	if input.NoReuse {
		body.NoReuse = &input.NoReuse
	}
	if len(input.Labels) > 0 {
		labels := daguapi.Labels(input.Labels)
		body.Labels = &labels
	}
	return body
}

func enqueueBody(input executeInput) *daguapi.EnqueueDAGDAGRunJSONRequestBody {
	body := &daguapi.EnqueueDAGDAGRunJSONRequestBody{}
	if input.DAGRunID != "" {
		body.DagRunId = &input.DAGRunID
	}
	if input.Params != "" {
		body.Params = &input.Params
	}
	if input.Queue != "" {
		body.Queue = &input.Queue
	}
	if input.Singleton {
		body.Singleton = &input.Singleton
	}
	if input.NoReuse {
		body.NoReuse = &input.NoReuse
	}
	if len(input.Labels) > 0 {
		labels := daguapi.Labels(input.Labels)
		body.Labels = &labels
	}
	return body
}

func (svc *Service) readResourceText(ctx context.Context, rawURI string) (string, string, error) {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return "", "", err
	}
	if parsed.Scheme == "ui" {
		if rawURI == runInspectorURI {
			return runInspectorHTML, mcpAppMIMEType, nil
		}
		return "", "", mcpsdk.ResourceNotFoundError(rawURI)
	}
	if parsed.Scheme != "dagu" {
		return "", "", mcpsdk.ResourceNotFoundError(rawURI)
	}

	segments, err := uriPathSegments(parsed)
	if err != nil {
		return "", "", err
	}

	switch parsed.Host {
	case "reference":
		if len(segments) != 1 {
			return "", "", mcpsdk.ResourceNotFoundError(rawURI)
		}
		ref, ok := referenceByTopic(segments[0])
		if !ok {
			return "", "", mcpsdk.ResourceNotFoundError(rawURI)
		}
		return ref.text, resourceMIMEText, nil
	case "dags":
		if len(segments) != 2 || segments[1] != "spec" {
			return "", "", mcpsdk.ResourceNotFoundError(rawURI)
		}
		if err := svc.requireAPI(); err != nil {
			return "", "", err
		}
		spec, err := svc.getDAGSpec(ctx, segments[0])
		if err != nil {
			return "", "", err
		}
		rawSpec, _ := spec["spec"].(string)
		return rawSpec, resourceMIMEYAML, nil
	case "docs":
		input, readErr := parseReadResourceURI(rawURI)
		if readErr != nil {
			return "", "", mcpsdk.ResourceNotFoundError(rawURI)
		}
		if err := svc.requireAPI(); err != nil {
			return "", "", err
		}
		if input.Target == readTargetDoc {
			doc, err := svc.getDoc(ctx, input.Workspace, input.Path)
			if err != nil {
				return "", "", err
			}
			return doc.Content, resourceMIMEText, nil
		}
		data, err := svc.listDocs(ctx, input.Workspace, input.Query)
		if err != nil {
			return "", "", err
		}
		text, err := prettyJSON(data)
		if err != nil {
			return "", "", err
		}
		return text, resourceMIMEJSON, nil
	case "runs":
		if !isRunResourceSegments(segments) && !isStepLogResourceSegments(segments) {
			return "", "", mcpsdk.ResourceNotFoundError(rawURI)
		}
		if isStepLogResourceSegments(segments) && parsed.RawQuery != "" {
			return "", "", mcpsdk.ResourceNotFoundError(rawURI)
		}
		if err := svc.requireAPI(); err != nil {
			return "", "", err
		}
		identifier := segments[0] + "/" + segments[1]
		var data any
		if isStepLogResourceSegments(segments) {
			data, err = svc.api.GetStepLogDataByRef(
				ctx,
				dagrun.NewDAGRunRef(segments[0], segments[1]),
				segments[3],
			)
		} else if len(segments) == 3 {
			if parsed.RawQuery != "" {
				identifier += "?" + parsed.RawQuery
			}
			data, err = svc.api.GetDAGRunLogsData(ctx, identifier)
		} else {
			data, err = svc.api.GetDAGRunDetailsData(ctx, identifier)
		}
		if err != nil {
			return "", "", err
		}
		text, err := prettyJSON(data)
		if err != nil {
			return "", "", err
		}
		return text, resourceMIMEJSON, nil
	default:
		return "", "", mcpsdk.ResourceNotFoundError(rawURI)
	}
}

func (svc *Service) subscribe(ctx context.Context, req *mcpsdk.SubscribeRequest) error {
	if !isRunResourceURI(req.Params.URI) {
		return nil
	}
	ctx = withMCPSubscriptionSourceContext(ctx, req, "subscribe_resource")
	details := resourceAuditDetails(req.Params.URI)

	svc.mu.Lock()
	if watcher, ok := svc.watchers[req.Params.URI]; ok {
		watcher.refs++
		svc.mu.Unlock()
		logMCPAudit(ctx, svc.api, "mcp.resource.subscribe.succeeded", withAuditResult(details, "succeeded"))
		return nil
	}

	watchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	svc.nextID++
	id := svc.nextID
	svc.watchers[req.Params.URI] = &resourceWatcher{id: id, cancel: cancel, refs: 1}
	svc.mu.Unlock()

	go svc.watchRunResource(watchCtx, req.Params.URI, id)
	logMCPAudit(ctx, svc.api, "mcp.resource.subscribe.succeeded", withAuditResult(details, "succeeded"))
	return nil
}

func (svc *Service) unsubscribe(ctx context.Context, req *mcpsdk.UnsubscribeRequest) error {
	ctx = withMCPUnsubscribeSourceContext(ctx, req)
	details := resourceAuditDetails(req.Params.URI)
	svc.mu.Lock()
	watcher, ok := svc.watchers[req.Params.URI]
	if ok {
		watcher.refs--
		if watcher.refs <= 0 {
			watcher.cancel()
			delete(svc.watchers, req.Params.URI)
		}
	}
	svc.mu.Unlock()

	logMCPAudit(ctx, svc.api, "mcp.resource.unsubscribe.succeeded", withAuditResult(details, "succeeded"))
	return nil
}

func (svc *Service) watchRunResource(ctx context.Context, uri string, id uint64) {
	defer svc.removeWatcher(uri, id)

	pollInterval := svc.watchPollInterval
	if pollInterval <= 0 {
		pollInterval = defaultRunWatchPollInterval
	}
	maxErrors := svc.watchMaxErrors
	if maxErrors <= 0 {
		maxErrors = defaultRunWatchMaxErrors
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	consecutiveErrors := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status, err := svc.runStatus(ctx, uri)
			if err != nil {
				consecutiveErrors++
				if consecutiveErrors >= maxErrors {
					return
				}
				continue
			}
			consecutiveErrors = 0
			if !isTerminalStatus(status) {
				continue
			}
			_ = svc.server.ResourceUpdated(ctx, &mcpsdk.ResourceUpdatedNotificationParams{URI: uri})
			return
		}
	}
}

func (svc *Service) removeWatcher(uri string, id uint64) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	watcher, ok := svc.watchers[uri]
	if ok && watcher.id == id {
		delete(svc.watchers, uri)
	}
}

func (svc *Service) runStatus(ctx context.Context, uri string) (int, error) {
	if err := svc.requireAPI(); err != nil {
		return 0, err
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return 0, err
	}
	segments, err := uriPathSegments(parsed)
	if err != nil {
		return 0, err
	}
	if parsed.Host != "runs" || !isRunResourceSegments(segments) {
		return 0, mcpsdk.ResourceNotFoundError(uri)
	}
	data, err := svc.api.GetDAGRunDetailsData(ctx, segments[0]+"/"+segments[1])
	if err != nil {
		return 0, err
	}
	switch r := data.(type) {
	case daguapi.GetDAGRunDetails200JSONResponse:
		return int(r.DagRunDetails.Status), nil
	case *daguapi.GetDAGRunDetails200JSONResponse:
		return int(r.DagRunDetails.Status), nil
	default:
		return 0, fmt.Errorf("unexpected DAG-run details response %T", data)
	}
}

func isRunResourceURI(rawURI string) bool {
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme != "dagu" || parsed.Host != "runs" {
		return false
	}
	segments, err := uriPathSegments(parsed)
	return err == nil && isRunResourceSegments(segments)
}

func isRunResourceSegments(segments []string) bool {
	return len(segments) == 2 || (len(segments) == 3 && segments[2] == "logs")
}

func isStepLogResourceSegments(segments []string) bool {
	return len(segments) == 5 && segments[2] == "steps" && segments[4] == "logs"
}

func isTerminalStatus(status int) bool {
	switch status {
	case 2, 3, 4, 6, 8:
		return true
	default:
		return false
	}
}

func (svc *Service) requireAPI() error {
	if svc.api == nil {
		return errors.New("dagu API is not configured")
	}
	return nil
}

func requireName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("name is required")
	}
	return nil
}

func requireRun(name, dagRunID string) error {
	if err := requireName(name); err != nil {
		return err
	}
	if strings.TrimSpace(dagRunID) == "" {
		return errors.New("dagRunId is required")
	}
	return nil
}

func isDAGNotFound(err error) bool {
	if errors.Is(err, dagstore.ErrDAGNotFound) {
		return true
	}
	var apiErr *frontendapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == daguapi.ErrorCodeNotFound
}

type resourceLink struct {
	uri         string
	name        string
	title       string
	description string
	mimeType    string
}

func resultWithLinks(message string, links ...resourceLink) *mcpsdk.CallToolResult {
	content := []mcpsdk.Content{&mcpsdk.TextContent{Text: message}}
	for _, link := range links {
		if link.uri == "" {
			continue
		}
		content = append(content, &mcpsdk.ResourceLink{
			URI:         link.uri,
			Name:        link.name,
			Title:       link.title,
			Description: link.description,
			MIMEType:    link.mimeType,
		})
	}
	return &mcpsdk.CallToolResult{Content: content}
}

func linkForDAGSpec(name string) resourceLink {
	return resourceLink{
		uri:         dagSpecURI(name),
		name:        "dag_spec",
		title:       "DAG spec",
		description: "Current YAML spec for this DAG.",
		mimeType:    resourceMIMEYAML,
	}
}

func linkForDoc(workspace, path string) resourceLink {
	return resourceLink{
		uri:         docURI(workspace, path),
		name:        "document",
		title:       "Markdown document",
		description: "Current Markdown content for this document.",
		mimeType:    resourceMIMEText,
	}
}

func dagSpecURI(name string) string {
	return "dagu://dags/" + pathEscape(name) + "/spec"
}

func runURI(name, dagRunID string) string {
	return "dagu://runs/" + pathEscape(name) + "/" + pathEscape(dagRunID)
}

func runLogsURI(name, dagRunID string) string {
	return runURI(name, dagRunID) + "/logs"
}

func runLogsURIWithQuery(name, dagRunID, query string) string {
	uri := runLogsURI(name, dagRunID)
	if query == "" {
		return uri
	}
	return uri + "?" + query
}

func stepLogURI(name, dagRunID, stepName string) string {
	return runURI(name, dagRunID) + "/steps/" + pathEscape(stepName) + "/logs"
}

func pathEscape(s string) string {
	return url.PathEscape(s)
}

func uriPathSegments(uri *url.URL) ([]string, error) {
	raw := strings.Trim(uri.EscapedPath(), "/")
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return nil, err
		}
		out = append(out, decoded)
	}
	return out, nil
}

func prettyJSON(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
