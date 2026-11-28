// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/audit"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type toolAuditMetadata struct {
	Action       string
	ResourceType string
	ResourceID   string
	Workspace    string
	Attributes   map[string]any
}

func auditToolCall[T any](
	ctx context.Context,
	api *frontendapi.API,
	req *mcpsdk.CallToolRequest,
	tool string,
	meta toolAuditMetadata,
	run func(context.Context) (*mcpsdk.CallToolResult, T, error),
) (*mcpsdk.CallToolResult, T, error) {
	ctx = withMCPToolSourceContext(ctx, req, tool, meta)
	details := meta.auditDetails(tool)
	logMCPAudit(ctx, api, "mcp.tool_call.received", withAuditResult(details, "received"))
	logMCPAudit(ctx, api, "mcp.tool_call.started", withAuditResult(details, "started"))

	start := time.Now()
	result, output, err := run(ctx)
	if err != nil {
		outcome := "failed"
		action := "mcp.tool_call.failed"
		if isAuthorizationFailure(err) {
			outcome = "denied"
			action = "mcp.tool_call.denied"
		}
		failureDetails := withAuditResult(details, outcome)
		failureDetails["duration_ms"] = time.Since(start).Milliseconds()
		failureDetails["error"] = strings.TrimSpace(err.Error())
		logMCPAudit(ctx, api, action, failureDetails)
		return nil, output, err
	}

	successDetails := withAuditResult(details, "succeeded")
	successDetails["duration_ms"] = time.Since(start).Milliseconds()
	mergeToolOutputAuditDetails(successDetails, output)
	logMCPAudit(ctx, api, "mcp.tool_call.succeeded", successDetails)
	return result, output, nil
}

func withMCPToolSourceContext(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	tool string,
	meta toolAuditMetadata,
) context.Context {
	source := ensureMCPSourceContext(ctx)
	source.MCPTool = tool
	source.MCPAction = meta.Action
	if meta.Workspace != "" {
		source.ResolvedWorkspace = meta.Workspace
	}
	if req != nil {
		applyMCPRequestMetadata(source, req.Session, req.Extra)
	}
	return audit.WithSourceContext(ctx, source)
}

func ensureMCPSourceContext(ctx context.Context) *audit.SourceContext {
	source, ok := audit.SourceContextFromContext(ctx)
	if !ok || source == nil {
		source = &audit.SourceContext{}
	}
	if source.Source == "" {
		source.Source = "mcp"
	}
	if source.Surface == "" {
		source.Surface = "mcp"
	}
	if source.RequestID == "" {
		source.RequestID = uuid.NewString()
	}
	if source.CorrelationID == "" {
		source.CorrelationID = uuid.NewString()
	}
	if source.Transport == "" {
		source.Transport = "streamable_http"
	}
	return source
}

func applyMCPRequestMetadata(source *audit.SourceContext, session *mcpsdk.ServerSession, extra *mcpsdk.RequestExtra) {
	if source == nil {
		return
	}
	if session != nil {
		source.SessionID = session.ID()
		if params := session.InitializeParams(); params != nil && params.ClientInfo != nil {
			source.ClientName = strings.TrimSpace(params.ClientInfo.Name)
			source.ClientVersion = strings.TrimSpace(params.ClientInfo.Version)
		}
	}
	if extra != nil && source.ClientName == "" {
		source.ClientName = strings.TrimSpace(extra.Header.Get("User-Agent"))
	}
}

func (m toolAuditMetadata) auditDetails(tool string) map[string]any {
	details := map[string]any{
		"mcp_tool":      tool,
		"mcp_action":    m.Action,
		"resource_type": m.ResourceType,
		"resource_id":   m.ResourceID,
	}
	if m.Workspace != "" {
		details["workspace"] = m.Workspace
	}
	maps.Copy(details, m.Attributes)
	return details
}

func withAuditResult(details map[string]any, result string) map[string]any {
	out := make(map[string]any, len(details)+1)
	maps.Copy(out, details)
	out["result"] = result
	return out
}

func logMCPAudit(ctx context.Context, api *frontendapi.API, action string, details map[string]any) {
	if api == nil {
		return
	}
	api.LogAudit(ctx, audit.CategoryMCP, action, details)
}

func isAuthorizationFailure(err error) bool {
	if apiErr, ok := errors.AsType[*frontendapi.Error](err); ok {
		return apiErr.HTTPStatus == http.StatusUnauthorized || apiErr.HTTPStatus == http.StatusForbidden
	}
	return false
}

func mergeToolOutputAuditDetails(details map[string]any, output any) {
	var out map[string]any
	if mapped, ok := any(output).(map[string]any); ok {
		out = mapped
	} else {
		data, err := json.Marshal(output)
		if err != nil {
			return
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return
		}
	}
	if out == nil {
		return
	}
	if dagRunID, ok := out["dagRunId"].(string); ok && dagRunID != "" {
		details["dag_run_id"] = dagRunID
	}
	if runURI, ok := out["runUri"].(string); ok && runURI != "" {
		details["run_uri"] = runURI
	}
	if applied, ok := out["applied"].(bool); ok {
		details["applied"] = applied
	}
	if valid, ok := out["valid"].(bool); ok {
		details["valid"] = valid
	}
	if inviteID, ok := out["inviteId"].(string); ok && inviteID != "" {
		details["invite_id"] = inviteID
	}
}

func readAuditMetadata(input readInput) toolAuditMetadata {
	target := strings.TrimSpace(input.Target)
	if target == "" && input.URI != "" {
		target = "reference"
	}
	resourceID := strings.TrimSpace(input.Name)
	if input.DAGRunID != "" {
		resourceID = strings.Trim(resourceID+"/"+input.DAGRunID, "/")
	}
	if input.URI != "" {
		resourceID = strings.TrimSpace(input.URI)
	}
	attrs := map[string]any{
		"target": target,
	}
	if input.Name != "" {
		if target == readTargetNode {
			attrs["node_name"] = input.Name
		} else {
			attrs["dag_name"] = input.Name
		}
	}
	if input.DAGRunID != "" {
		attrs["dag_run_id"] = input.DAGRunID
	}
	if input.StepName != "" {
		attrs["step_name"] = input.StepName
	}
	if input.Path != "" {
		if target == readTargetWorkbook {
			attrs["workbook_path"] = strings.TrimSpace(input.Path)
			if resourceID == "" {
				resourceID = strings.TrimSpace(input.Path)
			}
		} else {
			attrs["doc_path"] = input.Path
		}
	}
	if input.Prefix != "" {
		attrs["doc_prefix"] = input.Prefix
	}
	if input.Search != "" {
		attrs["has_search"] = true
	}
	if input.Cursor != "" {
		attrs["has_cursor"] = true
	}
	if input.Limit != 0 {
		attrs["limit"] = input.Limit
	}
	if keys := queryKeys(input.Query); len(keys) > 0 {
		attrs["query_keys"] = keys
	}
	return toolAuditMetadata{
		Action:       "read",
		ResourceType: target,
		ResourceID:   resourceID,
		Workspace:    input.Workspace,
		Attributes:   attrs,
	}
}

func changeAuditMetadata(input changeInput) toolAuditMetadata {
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = "preview"
	}
	changeType := strings.TrimSpace(input.Type)
	if changeType == "" {
		changeType = changeTypeUpsertDAG
	}
	canonicalType := canonicalChangeType(changeType)
	resourceType := "dag"
	resourceID := input.Name
	attributes := map[string]any{
		"mode": mode,
		"type": changeType,
	}
	workspace := ""
	switch canonicalType {
	case changeTypeUpsertDAG:
		attributes["dag_name"] = input.Name
		attributes["spec_bytes"] = len(input.Spec)
	case changeTypeRenameDAG:
		attributes["dag_name"] = input.Name
		attributes["new_dag_name"] = input.NewName
	case changeTypeDeleteDAG:
		attributes["dag_name"] = input.Name
	case changeTypeSetDAGProfile, changeTypeClearDAGProfile:
		attributes["dag_name"] = input.Name
		if input.Profile != "" {
			attributes["profile"] = input.Profile
		}
	default:
		resourceType = "doc"
		resourceID = input.Path
		workspace = input.Workspace
		attributes["doc_path"] = input.Path
		if input.NewPath != "" {
			attributes["new_path"] = input.NewPath
		}
		if canonicalType == changeTypeUpsertWikiPage {
			attributes["content_bytes"] = len(input.Content)
		}
	}
	return toolAuditMetadata{
		Action:       mode,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Workspace:    workspace,
		Attributes:   attributes,
	}
}

func executeAuditMetadata(input executeInput) toolAuditMetadata {
	action := strings.TrimSpace(input.Action)
	if action == executeActionAddWorker {
		return addWorkerAuditMetadata(input)
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
	resourceID := input.Name
	if input.DAGRunID != "" {
		resourceID = strings.Trim(resourceID+"/"+input.DAGRunID, "/")
	}
	attrs := map[string]any{
		"action":      action,
		"target_type": targetType,
		"dag_name":    input.Name,
		"singleton":   input.Singleton,
		"no_reuse":    input.NoReuse,
		"label_count": len(input.Labels),
	}
	if input.DAGRunID != "" {
		attrs["dag_run_id"] = input.DAGRunID
	}
	if input.Queue != "" {
		attrs["queue"] = input.Queue
	}
	if input.StepName != "" {
		attrs["step_name"] = input.StepName
	}
	if input.IncludeDownstream {
		attrs["include_downstream"] = true
	}
	if input.Wait {
		attrs["wait"] = true
	}
	if input.Spec != "" {
		attrs["has_spec"] = true
		attrs["spec_bytes"] = len(input.Spec)
	}
	if keys := jsonObjectKeys(input.Params); len(keys) > 0 {
		attrs["params_keys"] = keys
	}
	return toolAuditMetadata{
		Action:       action,
		ResourceType: targetType,
		ResourceID:   resourceID,
		Attributes:   attrs,
	}
}

// addWorkerAuditMetadata records the invite options of an add_worker call.
func addWorkerAuditMetadata(input executeInput) toolAuditMetadata {
	attrs := map[string]any{
		"action":      executeActionAddWorker,
		"reusable":    input.AddWorker.Reusable,
		"label_count": len(input.Labels),
	}
	if input.AddWorker.Address != "" {
		attrs["address"] = input.AddWorker.Address
	}
	if input.AddWorker.ExpiresIn != "" {
		attrs["expires_in"] = input.AddWorker.ExpiresIn
	}
	return toolAuditMetadata{Action: executeActionAddWorker, ResourceType: "worker_invite", Attributes: attrs}
}

func queryKeys(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return []string{"<invalid>"}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, strings.TrimSpace(key))
	}
	return sortedStrings(keys)
}

func jsonObjectKeys(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return []string{"<invalid>"}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, strings.TrimSpace(key))
	}
	return sortedStrings(keys)
}

func sortedStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	sort.Strings(values)
	return values
}

func (svc *Service) readResource(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	ctx = withMCPResourceSourceContext(ctx, req)
	details := resourceAuditDetails(req.Params.URI)
	logMCPAudit(ctx, svc.api, "mcp.resource.read.received", withAuditResult(details, "received"))
	start := time.Now()
	text, mime, err := svc.readResourceText(ctx, req.Params.URI)
	if err != nil {
		outcome := "failed"
		action := "mcp.resource.read.failed"
		if isAuthorizationFailure(err) {
			outcome = "denied"
			action = "mcp.resource.read.denied"
		}
		failureDetails := withAuditResult(details, outcome)
		failureDetails["duration_ms"] = time.Since(start).Milliseconds()
		failureDetails["error"] = strings.TrimSpace(err.Error())
		logMCPAudit(ctx, svc.api, action, failureDetails)
		return nil, err
	}
	successDetails := withAuditResult(details, "succeeded")
	successDetails["duration_ms"] = time.Since(start).Milliseconds()
	successDetails["mime_type"] = mime
	logMCPAudit(ctx, svc.api, "mcp.resource.read.succeeded", successDetails)
	content := &mcpsdk.ResourceContents{
		URI:      req.Params.URI,
		MIMEType: mime,
		Text:     text,
	}
	if req.Params.URI == runInspectorURI {
		content.Meta = runInspectorResourceMeta()
	}
	return &mcpsdk.ReadResourceResult{Contents: []*mcpsdk.ResourceContents{content}}, nil
}

func withMCPResourceSourceContext(ctx context.Context, req *mcpsdk.ReadResourceRequest) context.Context {
	source := ensureMCPSourceContext(ctx)
	source.MCPAction = "read_resource"
	if req != nil {
		applyMCPRequestMetadata(source, req.Session, req.Extra)
		if parsed, err := url.Parse(req.Params.URI); err == nil && parsed.Scheme == "dagu" && (parsed.Host == "wiki" || parsed.Host == "docs") {
			segments, _ := uriPathSegments(parsed)
			if len(segments) > 0 {
				source.ResolvedWorkspace = segments[0]
			} else {
				source.ResolvedWorkspace = "all"
			}
		}
	}
	return audit.WithSourceContext(ctx, source)
}

func withMCPSubscriptionSourceContext(ctx context.Context, req *mcpsdk.SubscribeRequest, action string) context.Context {
	source := ensureMCPSourceContext(ctx)
	source.MCPAction = action
	if req != nil {
		applyMCPRequestMetadata(source, req.Session, req.Extra)
	}
	return audit.WithSourceContext(ctx, source)
}

func withMCPUnsubscribeSourceContext(ctx context.Context, req *mcpsdk.UnsubscribeRequest) context.Context {
	source := ensureMCPSourceContext(ctx)
	source.MCPAction = "unsubscribe_resource"
	if req != nil {
		applyMCPRequestMetadata(source, req.Session, req.Extra)
	}
	return audit.WithSourceContext(ctx, source)
}

func resourceAuditDetails(rawURI string) map[string]any {
	resourceType := "resource"
	resourceID := strings.TrimSpace(rawURI)
	if parsed, err := url.Parse(rawURI); err == nil && parsed.Scheme == "ui" {
		resourceType = "mcp_app"
	} else if err == nil && parsed.Scheme == "dagu" {
		segments, _ := uriPathSegments(parsed)
		switch parsed.Host {
		case "reference":
			resourceType = "reference"
		case "dags":
			resourceType = "dags"
			if len(segments) > 0 {
				resourceType = "dag_spec"
				resourceID = segments[0]
			}
		case "runs":
			resourceType = "runs"
			if len(segments) > 0 {
				resourceType = "dag_run"
			}
			if isStepLogResourceSegments(segments) {
				resourceType = "dag_run_step_log"
				resourceID = segments[0] + "/" + segments[1] + "/" + segments[3]
			} else if len(segments) == 3 {
				resourceType = "dag_run_logs"
			}
			if len(segments) >= 2 && !isStepLogResourceSegments(segments) {
				resourceID = segments[0] + "/" + segments[1]
			}
		case "wiki", "docs":
			switch len(segments) {
			case 0:
				resourceType = "documents"
				resourceID = "all"
			case 1:
				resourceType = "documents"
				resourceID = segments[0]
			case 2:
				resourceType = "document"
				resourceID = segments[0] + "/" + segments[1]
			}
		}
	}
	return map[string]any{
		"resource_type": resourceType,
		"resource_id":   strings.TrimSpace(resourceID),
	}
}
