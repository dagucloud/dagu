// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// The routes of the MCP server entries (Spec 095, "MCP servers with
// credentials"). All routes need the role admin, except the route of choices. No
// response and no log line holds a credential value. The audit lines hold the
// fingerprint of each value, never the value or its prefix.

func aiMCPServersUnavailable() *Error {
	return &Error{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       api.ErrorCodeInternalError,
		Message:    "MCP server entry service not configured",
	}
}

func aiMCPServerNotFound() api.Error {
	return api.Error{Code: api.ErrorCodeNotFound, Message: "MCP server entry not found"}
}

func aiMCPServerConflict() api.Error {
	return api.Error{Code: api.ErrorCodeAlreadyExists, Message: "A MCP server entry with this name exists in the scope"}
}

func (a *API) ListAIMCPServers(ctx context.Context, _ api.ListAIMCPServersRequestObject) (api.ListAIMCPServersResponseObject, error) {
	if a.aiMCPServers == nil {
		return nil, aiMCPServersUnavailable()
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	servers, err := a.aiMCPServers.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list MCP server entries: %w", err)
	}
	out := make([]api.AIMCPServer, 0, len(servers))
	for _, server := range servers {
		out = append(out, toAIMCPServerResponse(server))
	}
	return api.ListAIMCPServers200JSONResponse{Servers: out}, nil
}

func (a *API) CreateAIMCPServer(ctx context.Context, request api.CreateAIMCPServerRequestObject) (api.CreateAIMCPServerResponseObject, error) {
	if a.aiMCPServers == nil {
		return nil, aiMCPServersUnavailable()
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return api.CreateAIMCPServer400JSONResponse(aiEndpointBadRequest("Request body is required")), nil
	}
	body := request.Body

	scope, err := aiendpoint.ParseScope(string(body.Scope))
	if err != nil {
		return api.CreateAIMCPServer400JSONResponse(aiEndpointBadRequest(err.Error())), nil
	}
	if !scope.IsGlobal() {
		if a.workspaceStore == nil {
			return nil, workspaceStoreUnavailable()
		}
		if _, err := a.workspaceStore.GetByName(ctx, scope.Workspace); err != nil {
			if errors.Is(err, workspace.ErrWorkspaceNotFound) {
				return api.CreateAIMCPServer404JSONResponse(api.Error{
					Code:    api.ErrorCodeNotFound,
					Message: "Workspace of the scope not found",
				}), nil
			}
			return nil, err
		}
	}

	created, err := a.aiMCPServers.Create(ctx, aiendpoint.MCPCreateInput{
		Name:        string(body.Name),
		URL:         body.Url,
		Scope:       scope,
		Allow:       fromAIMCPServerAllow(body.Allow),
		Credentials: aiendpoint.MCPCredentialsInput{Token: valueOf(body.Token), Headers: valueOf(body.Headers)},
		Actor:       currentActorID(ctx),
	})
	if err != nil {
		switch {
		case errors.Is(err, aiendpoint.ErrInvalid):
			return api.CreateAIMCPServer400JSONResponse(aiEndpointBadRequest(err.Error())), nil
		case errors.Is(err, aiendpoint.ErrAlreadyExists):
			return api.CreateAIMCPServer409JSONResponse(aiMCPServerConflict()), nil
		}
		return nil, fmt.Errorf("failed to create MCP server entry: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "ai_mcp_server_create", aiMCPServerCredentialAuditDetails(created))
	return api.CreateAIMCPServer201JSONResponse(toAIMCPServerResponse(created)), nil
}

func (a *API) UpdateAIMCPServer(ctx context.Context, request api.UpdateAIMCPServerRequestObject) (api.UpdateAIMCPServerResponseObject, error) {
	if a.aiMCPServers == nil {
		return nil, aiMCPServersUnavailable()
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	scope, err := aiEndpointScopeFromQuery(request.Params.Workspace)
	if err != nil {
		return api.UpdateAIMCPServer400JSONResponse(aiEndpointBadRequest(err.Error())), nil
	}
	if request.Body == nil {
		return api.UpdateAIMCPServer400JSONResponse(aiEndpointBadRequest("Request body is required")), nil
	}

	input := aiendpoint.MCPUpdateInput{URL: request.Body.Url, Actor: currentActorID(ctx)}
	if request.Body.Allow != nil {
		allow := fromAIMCPServerAllow(*request.Body.Allow)
		input.Allow = &allow
	}
	updated, err := a.aiMCPServers.Update(ctx, scope, string(request.Name), input)
	if err != nil {
		switch {
		case errors.Is(err, aiendpoint.ErrNotFound):
			return api.UpdateAIMCPServer404JSONResponse(aiMCPServerNotFound()), nil
		case errors.Is(err, aiendpoint.ErrInvalid):
			return api.UpdateAIMCPServer400JSONResponse(aiEndpointBadRequest(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to update MCP server entry: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "ai_mcp_server_update", aiMCPServerAuditDetails(updated))
	return api.UpdateAIMCPServer200JSONResponse(toAIMCPServerResponse(updated)), nil
}

func (a *API) SetAIMCPServerCredentials(ctx context.Context, request api.SetAIMCPServerCredentialsRequestObject) (api.SetAIMCPServerCredentialsResponseObject, error) {
	if a.aiMCPServers == nil {
		return nil, aiMCPServersUnavailable()
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	scope, err := aiEndpointScopeFromQuery(request.Params.Workspace)
	if err != nil {
		return api.SetAIMCPServerCredentials400JSONResponse(aiEndpointBadRequest(err.Error())), nil
	}
	if request.Body == nil {
		return api.SetAIMCPServerCredentials400JSONResponse(aiEndpointBadRequest("Request body is required")), nil
	}

	updated, err := a.aiMCPServers.SetCredentials(ctx, scope, string(request.Name), aiendpoint.MCPCredentialsInput{
		Token:   valueOf(request.Body.Token),
		Headers: valueOf(request.Body.Headers),
	}, currentActorID(ctx))
	if err != nil {
		switch {
		case errors.Is(err, aiendpoint.ErrNotFound):
			return api.SetAIMCPServerCredentials404JSONResponse(aiMCPServerNotFound()), nil
		case errors.Is(err, aiendpoint.ErrInvalid):
			return api.SetAIMCPServerCredentials400JSONResponse(aiEndpointBadRequest(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to set the credentials of the MCP server entry: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "ai_mcp_server_key_rotate", aiMCPServerCredentialAuditDetails(updated))
	return api.SetAIMCPServerCredentials200JSONResponse(toAIMCPServerResponse(updated)), nil
}

func (a *API) DeleteAIMCPServer(ctx context.Context, request api.DeleteAIMCPServerRequestObject) (api.DeleteAIMCPServerResponseObject, error) {
	if a.aiMCPServers == nil {
		return nil, aiMCPServersUnavailable()
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	scope, err := aiEndpointScopeFromQuery(request.Params.Workspace)
	if err != nil {
		return api.DeleteAIMCPServer400JSONResponse(aiEndpointBadRequest(err.Error())), nil
	}
	existing, err := a.aiMCPServers.Get(ctx, scope, string(request.Name))
	if err != nil {
		if errors.Is(err, aiendpoint.ErrNotFound) {
			return api.DeleteAIMCPServer404JSONResponse(aiMCPServerNotFound()), nil
		}
		return nil, fmt.Errorf("failed to read MCP server entry: %w", err)
	}
	if err := a.aiMCPServers.Delete(ctx, scope, string(request.Name)); err != nil {
		if errors.Is(err, aiendpoint.ErrNotFound) {
			return api.DeleteAIMCPServer404JSONResponse(aiMCPServerNotFound()), nil
		}
		return nil, fmt.Errorf("failed to delete MCP server entry: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "ai_mcp_server_delete", aiMCPServerAuditDetails(existing))
	return api.DeleteAIMCPServer204Response{}, nil
}

func (a *API) ListAIMCPServerChoices(ctx context.Context, request api.ListAIMCPServerChoicesRequestObject) (api.ListAIMCPServerChoicesResponseObject, error) {
	if a.aiMCPServers == nil {
		return nil, aiMCPServersUnavailable()
	}
	workspaceName := ""
	if request.Params.Workspace != nil {
		workspaceName = *request.Params.Workspace
	}
	// The roles that edit DAGs of the workspace can read the choices.
	if err := a.requireWorkspaceWrite(ctx, workspaceName); err != nil {
		return nil, err
	}
	if request.Params.Dag == "" {
		return api.ListAIMCPServerChoices400JSONResponse(aiEndpointBadRequest("Query dag is required")), nil
	}
	choices, err := a.aiMCPServers.Choices(ctx, request.Params.Dag, workspaceName)
	if err != nil {
		return nil, fmt.Errorf("failed to list MCP server choices: %w", err)
	}
	out := make([]api.AIMCPServerChoice, 0, len(choices))
	for _, server := range choices {
		out = append(out, api.AIMCPServerChoice{
			Name:  api.AIMCPServerName(server.Name),
			Scope: api.AIEndpointScope(server.Scope.String()),
		})
	}
	return api.ListAIMCPServerChoices200JSONResponse{Choices: out}, nil
}

func fromAIMCPServerAllow(in api.AIMCPServerAllow) aiendpoint.Allow {
	out := aiendpoint.Allow{DAGs: in.Dags}
	if in.Workspaces != nil {
		out.Workspaces = *in.Workspaces
	}
	return out
}

// toAIMCPServerResponse converts an entry. It has no credential value: the entry
// holds the prefix and the fingerprint of each value only.
func toAIMCPServerResponse(server *aiendpoint.MCPServer) api.AIMCPServer {
	resp := api.AIMCPServer{
		Name:      api.AIMCPServerName(server.Name),
		Url:       server.URL,
		Scope:     api.AIEndpointScope(server.Scope.String()),
		Allow:     api.AIMCPServerAllow{Dags: append([]string{}, server.Allow.DAGs...)},
		CreatedAt: server.CreatedAt,
		UpdatedAt: server.UpdatedAt,
	}
	if len(server.Allow.Workspaces) > 0 {
		workspaces := append([]string{}, server.Allow.Workspaces...)
		resp.Allow.Workspaces = &workspaces
	}
	if server.Token != nil {
		resp.Token = &api.AIMCPCredential{
			KeyPrefix:      ptrOf(server.Token.Prefix),
			KeyFingerprint: server.Token.Fingerprint,
			KeyUpdatedAt:   server.Token.UpdatedAt,
		}
	}
	if len(server.Headers) > 0 {
		headers := make([]api.AIMCPHeaderCredential, 0, len(server.Headers))
		for _, header := range server.Headers {
			headers = append(headers, api.AIMCPHeaderCredential{
				Name:           header.Header,
				KeyPrefix:      ptrOf(header.Prefix),
				KeyFingerprint: header.Fingerprint,
				KeyUpdatedAt:   header.UpdatedAt,
			})
		}
		resp.Headers = &headers
	}
	return resp
}

// aiMCPServerAuditDetails gives the fields of an audit line: the name and the scope.
func aiMCPServerAuditDetails(server *aiendpoint.MCPServer) map[string]any {
	return map[string]any{
		"name":  server.Name,
		"scope": server.Scope.String(),
	}
}

// aiMCPServerCredentialAuditDetails adds the fingerprint of each credential. It
// never adds a value or a prefix.
func aiMCPServerCredentialAuditDetails(server *aiendpoint.MCPServer) map[string]any {
	details := aiMCPServerAuditDetails(server)
	if server.Token != nil {
		details["token_fingerprint"] = server.Token.Fingerprint
	}
	if len(server.Headers) > 0 {
		fingerprints := make(map[string]string, len(server.Headers))
		for _, header := range server.Headers {
			fingerprints[header.Header] = header.Fingerprint
		}
		details["header_fingerprints"] = fingerprints
	}
	return details
}
