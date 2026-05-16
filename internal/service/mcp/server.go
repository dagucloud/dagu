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

	daguapi "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
	frontendapi "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	toolRead    = "dagu_read"
	toolChange  = "dagu_change"
	toolExecute = "dagu_execute"

	resourceMIMEJSON = "application/json"
	resourceMIMEText = "text/markdown"
	resourceMIMEYAML = "application/yaml"
)

// Service owns the Dagu MCP server and the small amount of state needed for
// resource subscriptions.
type Service struct {
	api *frontendapi.API

	mu       sync.Mutex
	server   *mcpsdk.Server
	watchers map[string]*resourceWatcher
	nextID   uint64
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
		api:      api,
		watchers: make(map[string]*resourceWatcher),
	}
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "dagu",
		Version: config.Version,
	}, &mcpsdk.ServerOptions{
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

type readInput struct {
	Target   string `json:"target" jsonschema:"Read target: dags, dag, dag_spec, runs, run, run_logs, or reference."`
	Name     string `json:"name,omitempty" jsonschema:"DAG name for dag, dag_spec, run, and run_logs targets."`
	DAGRunID string `json:"dagRunId,omitempty" jsonschema:"DAG-run ID for run and run_logs targets. The value latest is accepted where Dagu accepts it."`
	Query    string `json:"query,omitempty" jsonschema:"URL query string for list targets, for example page=1&perPage=100 or status=running."`
	URI      string `json:"uri,omitempty" jsonschema:"Resource URI to read directly, for example dagu://reference/authoring."`
}

type changeInput struct {
	Mode string `json:"mode,omitempty" jsonschema:"preview or apply. Defaults to preview."`
	Type string `json:"type,omitempty" jsonschema:"Change type. Currently upsert_dag."`
	Name string `json:"name" jsonschema:"DAG name to create or update."`
	Spec string `json:"spec" jsonschema:"DAG YAML specification."`
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
	Labels     []string `json:"labels,omitempty" jsonschema:"Additional labels, each as key=value or key-only."`
	StepName   string   `json:"stepName,omitempty" jsonschema:"Optional step name for retry."`
}

func registerTools(server *mcpsdk.Server, svc *Service) {
	falsePtr := boolPtr(false)
	truePtr := boolPtr(true)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        toolRead,
		Title:       "Read Dagu state",
		Description: "Read DAG specs, DAG details, DAG-run details, logs, list views, and Dagu MCP reference resources.",
		Annotations: &mcpsdk.ToolAnnotations{
			OpenWorldHint: falsePtr,
			ReadOnlyHint:  true,
			Title:         "Read Dagu state",
		},
	}, svc.readTool)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        toolChange,
		Title:       "Preview or apply DAG changes",
		Description: "Validate and optionally apply a DAG YAML change. Use mode=preview before mode=apply unless the user explicitly asked to write immediately.",
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: truePtr,
			OpenWorldHint:   falsePtr,
			Title:           "Preview or apply DAG changes",
		},
	}, svc.changeTool)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
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
	for _, ref := range referenceResources() {
		ref := ref
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
		Name:        "dagu_debug_failed_run",
		Title:       "Debug a failed Dagu run",
		Description: "Read a run and logs, explain the likely failure, then offer retry or stop as appropriate.",
		Arguments: []*mcpsdk.PromptArgument{
			{Name: "name", Description: "DAG name.", Required: true},
			{Name: "dagRunId", Description: "DAG-run ID.", Required: true},
		},
	}, promptDebugRun)
}

func (svc *Service) readTool(ctx context.Context, _ *mcpsdk.CallToolRequest, input readInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	target := strings.TrimSpace(input.Target)
	if target == "" && input.URI != "" {
		target = "reference"
	}

	var (
		data any
		uri  string
		err  error
	)

	switch target {
	case "reference":
		uri = input.URI
		if uri == "" {
			topic := strings.TrimSpace(input.Name)
			if topic == "" {
				topic = "authoring"
			}
			uri = "dagu://reference/" + pathEscape(topic)
		}
		content, mime, readErr := svc.readResourceText(ctx, uri)
		if readErr != nil {
			return nil, nil, readErr
		}
		data = map[string]any{"text": content, "mimeType": mime}
	case "dags":
		if err = svc.requireAPI(); err == nil {
			data, err = svc.api.GetDAGsListData(ctx, input.Query)
		}
	case "dag":
		if err = requireName(input.Name); err == nil {
			if err = svc.requireAPI(); err == nil {
				data, err = svc.api.GetDAGDetailsData(ctx, input.Name)
				uri = dagSpecURI(input.Name)
			}
		}
	case "dag_spec":
		if err = requireName(input.Name); err == nil {
			if err = svc.requireAPI(); err == nil {
				data, err = svc.getDAGSpec(ctx, input.Name)
				uri = dagSpecURI(input.Name)
			}
		}
	case "runs":
		if err = svc.requireAPI(); err == nil {
			data, err = svc.api.GetDAGRunsListData(ctx, input.Query)
		}
	case "run":
		if err = requireRun(input.Name, input.DAGRunID); err == nil {
			if err = svc.requireAPI(); err == nil {
				data, err = svc.api.GetDAGRunDetailsData(ctx, input.Name+"/"+input.DAGRunID)
				uri = runURI(input.Name, input.DAGRunID)
			}
		}
	case "run_logs":
		if err = requireRun(input.Name, input.DAGRunID); err == nil {
			if err = svc.requireAPI(); err == nil {
				identifier := input.Name + "/" + input.DAGRunID
				if input.Query != "" {
					identifier += "?" + input.Query
				}
				data, err = svc.api.GetDAGRunLogsData(ctx, identifier)
				uri = runLogsURI(input.Name, input.DAGRunID)
			}
		}
	default:
		err = fmt.Errorf("unknown read target %q", input.Target)
	}
	if err != nil {
		return nil, nil, err
	}

	output := map[string]any{
		"target":     target,
		"data":       data,
		"references": defaultReferenceURIs(),
	}
	if uri != "" {
		output["uri"] = uri
	}

	return resultWithLinks("Dagu read completed.", linksForURI(uri)...), output, nil
}

func (svc *Service) changeTool(ctx context.Context, _ *mcpsdk.CallToolRequest, input changeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	if err := svc.requireAPI(); err != nil {
		return nil, nil, err
	}
	if err := requireName(input.Name); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(input.Spec) == "" {
		return nil, nil, errors.New("spec is required")
	}

	changeType := input.Type
	if changeType == "" {
		changeType = "upsert_dag"
	}
	if changeType != "upsert_dag" {
		return nil, nil, fmt.Errorf("unsupported change type %q", changeType)
	}

	mode := input.Mode
	if mode == "" {
		mode = "preview"
	}
	if mode != "preview" && mode != "apply" {
		return nil, nil, fmt.Errorf("unsupported change mode %q", mode)
	}

	validation, err := svc.validateDAGSpec(ctx, input.Name, input.Spec)
	if err != nil {
		return nil, nil, err
	}

	output := map[string]any{
		"mode":       mode,
		"type":       changeType,
		"dagName":    input.Name,
		"valid":      validation.Valid,
		"errors":     validation.Errors,
		"dag":        validation.Dag,
		"applied":    false,
		"references": defaultReferenceURIs(),
		"dagUri":     dagSpecURI(input.Name),
	}

	if !validation.Valid {
		return resultWithLinks("DAG spec is not valid; no changes were applied.", linkForDAGSpec(input.Name)), output, nil
	}
	if mode == "preview" {
		return resultWithLinks("DAG spec is valid. Re-run with mode=apply to write it.", linkForDAGSpec(input.Name)), output, nil
	}

	created, err := svc.upsertDAG(ctx, input.Name, input.Spec)
	if err != nil {
		return nil, nil, err
	}
	output["applied"] = true
	output["created"] = created
	output["updated"] = !created

	return resultWithLinks("DAG change applied.", linkForDAGSpec(input.Name)), output, nil
}

func (svc *Service) executeTool(ctx context.Context, _ *mcpsdk.CallToolRequest, input executeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
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
	if len(input.Labels) > 0 {
		labels := daguapi.Labels(input.Labels)
		body.Labels = &labels
	}
	return body
}

func (svc *Service) readResource(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	text, mime, err := svc.readResourceText(ctx, req.Params.URI)
	if err != nil {
		return nil, err
	}
	return &mcpsdk.ReadResourceResult{
		Contents: []*mcpsdk.ResourceContents{{
			URI:      req.Params.URI,
			MIMEType: mime,
			Text:     text,
		}},
	}, nil
}

func (svc *Service) readResourceText(ctx context.Context, rawURI string) (string, string, error) {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return "", "", err
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
	case "runs":
		if len(segments) != 2 && !(len(segments) == 3 && segments[2] == "logs") {
			return "", "", mcpsdk.ResourceNotFoundError(rawURI)
		}
		if err := svc.requireAPI(); err != nil {
			return "", "", err
		}
		identifier := segments[0] + "/" + segments[1]
		var data any
		if len(segments) == 3 {
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

	svc.mu.Lock()
	if watcher, ok := svc.watchers[req.Params.URI]; ok {
		watcher.refs++
		svc.mu.Unlock()
		return nil
	}

	watchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	svc.nextID++
	id := svc.nextID
	svc.watchers[req.Params.URI] = &resourceWatcher{id: id, cancel: cancel, refs: 1}
	svc.mu.Unlock()

	go svc.watchRunResource(watchCtx, req.Params.URI, id)
	return nil
}

func (svc *Service) unsubscribe(_ context.Context, req *mcpsdk.UnsubscribeRequest) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	watcher, ok := svc.watchers[req.Params.URI]
	if !ok {
		return nil
	}
	watcher.refs--
	if watcher.refs > 0 {
		return nil
	}
	watcher.cancel()
	delete(svc.watchers, req.Params.URI)
	return nil
}

func (svc *Service) watchRunResource(ctx context.Context, uri string, id uint64) {
	defer svc.removeWatcher(uri, id)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status, err := svc.runStatus(ctx, uri)
			if err != nil {
				continue
			}
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
	if errors.Is(err, exec.ErrDAGNotFound) {
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

func linksForURI(uri string) []resourceLink {
	if uri == "" {
		return nil
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil
	}
	switch parsed.Host {
	case "dags":
		segments, err := uriPathSegments(parsed)
		if err != nil || len(segments) != 2 {
			return nil
		}
		return []resourceLink{linkForDAGSpec(segments[0])}
	case "runs":
		segments, err := uriPathSegments(parsed)
		if err != nil || len(segments) < 2 {
			return nil
		}
		if len(segments) == 3 && segments[2] == "logs" {
			return []resourceLink{{
				uri:         uri,
				name:        "dag_run_logs",
				title:       "DAG-run logs",
				description: "Logs for this DAG-run.",
				mimeType:    resourceMIMEJSON,
			}}
		}
		return []resourceLink{{
			uri:         uri,
			name:        "dag_run",
			title:       "DAG-run details",
			description: "DAG-run details.",
			mimeType:    resourceMIMEJSON,
		}}
	default:
		return []resourceLink{{
			uri:         uri,
			name:        "dagu_reference",
			title:       "Dagu reference",
			description: "Dagu MCP reference.",
			mimeType:    resourceMIMEText,
		}}
	}
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

func dagSpecURI(name string) string {
	return "dagu://dags/" + pathEscape(name) + "/spec"
}

func runURI(name, dagRunID string) string {
	return "dagu://runs/" + pathEscape(name) + "/" + pathEscape(dagRunID)
}

func runLogsURI(name, dagRunID string) string {
	return runURI(name, dagRunID) + "/logs"
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

func boolPtr(v bool) *bool {
	return &v
}
