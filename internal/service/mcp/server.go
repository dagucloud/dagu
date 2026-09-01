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
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
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
	Type      string `json:"type,omitempty" jsonschema:"DAG definition, DAG profile, or Wiki change type."`
	Name      string `json:"name,omitempty" jsonschema:"DAG name for DAG changes."`
	NewName   string `json:"newName,omitempty" jsonschema:"Destination DAG name for rename_dag."`
	Spec      string `json:"spec,omitempty" jsonschema:"DAG YAML specification for upsert_dag."`
	Profile   string `json:"profile,omitempty" jsonschema:"Runtime profile name for set_dag_profile."`
	Workspace string `json:"workspace,omitempty" jsonschema:"Wiki workspace for Wiki changes: default or a named workspace."`
	Path      string `json:"path,omitempty" jsonschema:"Wiki page or directory path for Wiki changes."`
	Content   string `json:"content,omitempty" jsonschema:"Markdown content for upsert_wiki_page."`
	NewPath   string `json:"newPath,omitempty" jsonschema:"Destination Wiki page or directory path for rename_wiki_page."`
}

func registerTools(server *mcpsdk.Server, svc *Service) {
	falsePtr := new(false)
	truePtr := new(true)

	server.AddTool(&mcpsdk.Tool{
		Meta:        runInspectorToolMeta(),
		Name:        toolRead,
		Title:       "Read Dagu state",
		Description: "Read DAG specs and default profiles, workspace-aware Wiki pages, DAG-run details, logs, list views, and Dagu MCP reference resources.",
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
		Description: "Validate and optionally apply DAG definitions, DAG default profiles, or Markdown Wiki changes. Wiki changes are workspace-aware. Use mode=preview before mode=apply unless the user explicitly asked to write immediately.",
		InputSchema: changeToolInputSchema(),
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: truePtr,
			OpenWorldHint:   falsePtr,
			Title:           "Preview or apply Dagu changes",
		},
	}, svc.changeTool)

	server.AddTool(&mcpsdk.Tool{
		Meta:        runInspectorToolMeta(),
		Name:        toolExecute,
		Title:       "Execute, enqueue, retry, or stop DAG-runs",
		Description: "Run control entry point. action=start or enqueue launches a DAG or inline spec; action=retry retries a DAG-run; action=stop terminates a DAG-run. Set wait=true to wait for the run result.",
		InputSchema: executeToolInputSchema(),
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
		Description: "Current YAML spec for a DAG. Server-side settings such as the default runtime profile are separate; read target=dag_profile.",
		MIMEType:    resourceMIMEYAML,
	}, svc.readResource)

	server.AddResource(&mcpsdk.Resource{
		URI:         readResourceWikiCollectionURI,
		Name:        "wiki",
		Title:       "Wiki",
		Description: "Wiki pages visible across accessible workspaces.",
		MIMEType:    resourceMIMEJSON,
	}, svc.readResource)

	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "dagu://wiki/{workspace}",
		Name:        "workspace_wiki",
		Title:       "Workspace Wiki",
		Description: "Wiki page tree for default or one named workspace.",
		MIMEType:    resourceMIMEJSON,
	}, svc.readResource)

	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "dagu://wiki/{workspace}/{path}",
		Name:        "wiki_page",
		Title:       "Wiki page",
		Description: "Current Markdown content for a Wiki page in default or one named workspace. Nested paths are encoded as one URI segment.",
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
		Description: "Standard output and standard error for a DAG-run step. Supports tail, head, offset, limit, and stream query parameters.",
		MIMEType:    resourceMIMEJSON,
	}, svc.readResource)

	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "dagu://runs/{name}/{dagRunId}/sub/{subRunId}",
		Name:        "sub_dag_run",
		Title:       "Sub DAG-run details",
		Description: "Current details for a child DAG-run addressed under its root run.",
		MIMEType:    resourceMIMEJSON,
	}, svc.readResource)

	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "dagu://runs/{name}/{dagRunId}/sub/{subRunId}/steps/{stepName}/logs",
		Name:        "sub_dag_run_step_log",
		Title:       "Sub DAG-run step log",
		Description: "Standard output and standard error for a child DAG-run step. Supports tail, head, offset, limit, and stream query parameters.",
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
		Name:        "dagu_create_wiki_page",
		Title:       "Create a Dagu Wiki page",
		Description: "Draft, preview, and create a workspace-aware Wiki page.",
		Arguments: []*mcpsdk.PromptArgument{
			{Name: "workspace", Description: "default or a workspace name.", Required: true},
			{Name: "path", Description: "Wiki page path without .md.", Required: true},
			{Name: "goal", Description: "What the Wiki page should contain.", Required: true},
		},
	}, promptCreateWikiPage)

	server.AddPrompt(&mcpsdk.Prompt{
		Name:        "dagu_edit_wiki_page",
		Title:       "Edit a Dagu Wiki page",
		Description: "Read an existing Wiki page, make a scoped edit, preview, then apply.",
		Arguments: []*mcpsdk.PromptArgument{
			{Name: "workspace", Description: "default or a workspace name.", Required: true},
			{Name: "path", Description: "Wiki page path without .md.", Required: true},
			{Name: "change", Description: "Requested change.", Required: true},
		},
	}, promptEditWikiPage)

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

func (svc *Service) searchDAGs(ctx context.Context, workspace, search, cursor string, limit int) (map[string]any, error) {
	params := daguapi.SearchDAGFeedParams{Q: search}
	if workspace != "" {
		value := daguapi.Workspace(workspace)
		params.Workspace = &value
	}
	if cursor != "" {
		value := daguapi.SearchCursor(cursor)
		params.Cursor = &value
	}
	if limit != 0 {
		value := daguapi.SearchLimit(limit)
		params.Limit = &value
	}

	resp, err := svc.api.SearchDAGFeed(ctx, daguapi.SearchDAGFeedRequestObject{Params: params})
	if err != nil {
		return nil, err
	}
	var data daguapi.DAGSearchFeedResponse
	switch result := resp.(type) {
	case daguapi.SearchDAGFeed200JSONResponse:
		data = daguapi.DAGSearchFeedResponse(result)
	case *daguapi.SearchDAGFeed200JSONResponse:
		data = daguapi.DAGSearchFeedResponse(*result)
	default:
		return nil, fmt.Errorf("unexpected DAG search response %T", resp)
	}

	items := make([]map[string]any, 0, len(data.Results))
	for _, item := range data.Results {
		name := item.FileName
		if name == "" {
			name = item.Name
		}
		entry := map[string]any{
			"name":           name,
			"uri":            dagSpecURI(name),
			"matches":        item.Matches,
			"hasMoreMatches": item.HasMoreMatches,
		}
		if item.Workspace != nil && *item.Workspace != "" {
			entry["workspace"] = *item.Workspace
		}
		if item.NextMatchesCursor != nil && *item.NextMatchesCursor != "" {
			entry["nextMatchesCursor"] = *item.NextMatchesCursor
		}
		items = append(items, entry)
	}
	output := map[string]any{
		"results": items,
		"hasMore": data.HasMore,
	}
	if data.NextCursor != nil && *data.NextCursor != "" {
		output["nextCursor"] = *data.NextCursor
	}
	return output, nil
}

func (svc *Service) validateDAGSpec(ctx context.Context, name, spec string) (*daguapi.ValidateDAGSpec200JSONResponse, *ir.DAG, error) {
	return svc.api.ValidateDAGSpecData(ctx, name, spec)
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

func (svc *Service) renameDAG(ctx context.Context, name, newName string) error {
	resp, err := svc.api.RenameDAG(ctx, daguapi.RenameDAGRequestObject{
		FileName: daguapi.DAGFileName(name),
		Body: &daguapi.RenameDAGJSONRequestBody{
			NewFileName: newName,
		},
	})
	if err != nil {
		return err
	}
	switch resp.(type) {
	case *daguapi.RenameDAG200Response, daguapi.RenameDAG200Response:
		return nil
	default:
		return fmt.Errorf("unexpected rename DAG response %T", resp)
	}
}

func (svc *Service) deleteDAG(ctx context.Context, name string) error {
	resp, err := svc.api.DeleteDAG(ctx, daguapi.DeleteDAGRequestObject{
		FileName: daguapi.DAGFileName(name),
	})
	if err != nil {
		return err
	}
	switch resp.(type) {
	case *daguapi.DeleteDAG204Response, daguapi.DeleteDAG204Response:
		return nil
	default:
		return fmt.Errorf("unexpected delete DAG response %T", resp)
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
	if err := requireIncludeDownstreamStep(input); err != nil {
		return err
	}
	body := &daguapi.RetryDAGRunJSONRequestBody{DagRunId: input.DAGRunID}
	if input.StepName != "" {
		body.StepName = &input.StepName
	}
	if input.IncludeDownstream {
		body.IncludeDownstream = &input.IncludeDownstream
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
	body := &daguapi.ExecuteDAGJSONRequestBody{DagName: stringPtr(input.Name)}
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
	body := &daguapi.EnqueueDAGDAGRunJSONRequestBody{DagName: stringPtr(input.Name)}
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
	case "wiki", "docs":
		input, readErr := parseReadResourceURI(rawURI)
		if readErr != nil {
			return "", "", mcpsdk.ResourceNotFoundError(rawURI)
		}
		if err := svc.requireAPI(); err != nil {
			return "", "", err
		}
		if input.Target == readTargetWikiPage {
			page, err := svc.getWikiPage(ctx, input.Workspace, input.Path)
			if err != nil {
				return "", "", err
			}
			return page.Content, resourceMIMEText, nil
		}
		data, err := svc.listWikiPages(ctx, input.Workspace, input.Query)
		if err != nil {
			return "", "", err
		}
		text, err := jsonText(data)
		if err != nil {
			return "", "", err
		}
		return text, resourceMIMEJSON, nil
	case "runs":
		if !isRunResourceSegments(segments) && !isStepLogResourceSegments(segments) &&
			!isSubRunResourceSegments(segments) && !isSubStepLogResourceSegments(segments) {
			return "", "", mcpsdk.ResourceNotFoundError(rawURI)
		}
		if isStepLogResourceSegments(segments) || isSubStepLogResourceSegments(segments) {
			if readErr := validateReadQuery(readTargetStepLog, parsed.RawQuery, true, rawURI); readErr != nil {
				return "", "", mcpsdk.ResourceNotFoundError(rawURI)
			}
		}
		if err := svc.requireAPI(); err != nil {
			return "", "", err
		}
		identifier := segments[0] + "/" + segments[1]
		var data any
		switch {
		case isStepLogResourceSegments(segments):
			data, err = svc.api.GetStepLogDataByRef(
				ctx,
				ir.NewDAGRunRef(segments[0], segments[1]),
				segments[3],
				stepLogReadOptions(parsed.RawQuery),
			)
		case isSubStepLogResourceSegments(segments):
			data, err = svc.api.GetSubStepLogDataByRef(
				ctx,
				ir.NewDAGRunRef(segments[0], segments[1]),
				segments[3],
				segments[5],
				stepLogReadOptions(parsed.RawQuery),
			)
		case isSubRunResourceSegments(segments):
			if parsed.RawQuery != "" {
				return "", "", mcpsdk.ResourceNotFoundError(rawURI)
			}
			data, err = svc.api.GetSubDAGRunDetailsData(ctx, identifier+"/"+segments[3])
		case len(segments) == 3:
			if parsed.RawQuery != "" {
				identifier += "?" + parsed.RawQuery
			}
			data, err = svc.api.GetDAGRunLogsData(ctx, identifier)
		default:
			data, err = svc.api.GetDAGRunDetailsData(ctx, identifier)
		}
		if err != nil {
			return "", "", err
		}
		text, err := jsonText(data)
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

func isSubRunResourceSegments(segments []string) bool {
	return len(segments) == 4 && segments[2] == "sub"
}

func isSubStepLogResourceSegments(segments []string) bool {
	return len(segments) == 7 && segments[2] == "sub" && segments[4] == "steps" && segments[6] == "logs"
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

func requireIncludeDownstreamStep(input executeInput) error {
	if input.IncludeDownstream && strings.TrimSpace(input.StepName) == "" {
		return errors.New("includeDownstream requires stepName")
	}
	return nil
}

func isDAGNotFound(err error) bool {
	if errors.Is(err, persis.ErrDAGNotFound) {
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

func linkForWikiPage(workspace, path string) resourceLink {
	return resourceLink{
		uri:         wikiPageURI(workspace, path),
		name:        "wiki_page",
		title:       "Wiki page",
		description: "Current Markdown content for this Wiki page.",
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

func subRunURI(name, dagRunID, subRunID string) string {
	return runURI(name, dagRunID) + "/sub/" + pathEscape(subRunID)
}

func subStepLogURI(name, dagRunID, subRunID, stepName string) string {
	return subRunURI(name, dagRunID, subRunID) + "/steps/" + pathEscape(stepName) + "/logs"
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

// jsonText serializes resource content as compact JSON; indentation only
// inflates payloads read by MCP clients.
func jsonText(v any) (string, error) {
	data, err := json.Marshal(v)
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
