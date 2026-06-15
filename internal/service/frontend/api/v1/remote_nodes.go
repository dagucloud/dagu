// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/remotenode"
	"github.com/dagucloud/dagu/internal/service/audit"
)

// ListRemoteNodes returns all remote nodes from both config and store sources.
func (a *API) ListRemoteNodes(ctx context.Context, _ api.ListRemoteNodesRequestObject) (api.ListRemoteNodesResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	if a.remoteNodeResolver == nil {
		return api.ListRemoteNodes200JSONResponse{RemoteNodes: []api.RemoteNodeResponse{}}, nil
	}

	nodes, err := a.remoteNodeResolver.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list remote nodes: %w", err)
	}

	response := make([]api.RemoteNodeResponse, 0, len(nodes))
	for _, n := range nodes {
		response = append(response, toRemoteNodeResponse(n))
	}

	return api.ListRemoteNodes200JSONResponse{RemoteNodes: response}, nil
}

// CreateRemoteNode creates a new store-managed remote node.
func (a *API) CreateRemoteNode(ctx context.Context, request api.CreateRemoteNodeRequestObject) (api.CreateRemoteNodeResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	if a.remoteNodeStore == nil {
		return nil, &Error{
			HTTPStatus: http.StatusServiceUnavailable,
			Code:       api.ErrorCodeInternalError,
			Message:    "Remote node store not configured",
		}
	}

	body := request.Body
	if body.Name == "" {
		return api.CreateRemoteNode400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: "Name is required",
		}, nil
	}
	if body.ApiBaseUrl == "" {
		return api.CreateRemoteNode400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: "API base URL is required",
		}, nil
	}
	if err := remotenode.ValidateAPIBaseURL(body.ApiBaseUrl); err != nil {
		return api.CreateRemoteNode400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: err.Error(),
		}, nil
	}

	// Check if name conflicts with a config-sourced node.
	// Store-level name uniqueness is enforced atomically by the store itself.
	if a.remoteNodeResolver != nil {
		if _, err := a.remoteNodeResolver.GetByName(ctx, body.Name); err == nil {
			return api.CreateRemoteNode409JSONResponse{
				Code:    api.ErrorCodeAlreadyExists,
				Message: fmt.Sprintf("Remote node with name %q already exists", body.Name),
			}, nil
		} else if !errors.Is(err, remotenode.ErrRemoteNodeNotFound) {
			return nil, fmt.Errorf("failed to check remote node name: %w", err)
		}
	}

	authMode := remotenode.AuthTypeNone
	if body.AuthType != nil {
		authMode = remotenode.AuthType(*body.AuthType)
	}

	node := remotenode.NewRemoteNode(body.Name, valueOf(body.Description), body.ApiBaseUrl, authMode)
	node.BasicAuthUsername = valueOf(body.BasicAuthUsername)
	node.BasicAuthPassword = valueOf(body.BasicAuthPassword)
	node.AuthToken = valueOf(body.AuthToken)
	node.SkipTLSVerify = valueOf(body.SkipTlsVerify)

	if err := a.remoteNodeStore.Create(ctx, node); err != nil {
		if errors.Is(err, remotenode.ErrRemoteNodeAlreadyExists) {
			return api.CreateRemoteNode409JSONResponse{
				Code:    api.ErrorCodeAlreadyExists,
				Message: "Remote node with this name already exists",
			}, nil
		}
		return nil, fmt.Errorf("failed to create remote node: %w", err)
	}

	a.logAudit(ctx, audit.CategoryRemoteNode, "remote_node_create", map[string]string{
		"id":   node.ID,
		"name": node.Name,
	})

	return api.CreateRemoteNode201JSONResponse(toRemoteNodeResponseFromNode(node, remotenode.SourceStore)), nil
}

// GetRemoteNode returns a single remote node by ID.
// Supports both store nodes (UUID) and config nodes (cfg:<name>).
func (a *API) GetRemoteNode(ctx context.Context, request api.GetRemoteNodeRequestObject) (api.GetRemoteNodeResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	node, err := a.resolveRemoteNode(ctx, request.RemoteNodeId)
	if err != nil {
		if errors.Is(err, remotenode.ErrRemoteNodeNotFound) {
			return api.GetRemoteNode404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: "Remote node not found",
			}, nil
		}
		return nil, fmt.Errorf("failed to get remote node: %w", err)
	}

	source := remotenode.SourceStore
	if strings.HasPrefix(request.RemoteNodeId, remotenode.ConfigNodeIDPrefix) {
		source = remotenode.SourceConfig
	}

	return api.GetRemoteNode200JSONResponse(toRemoteNodeResponseFromNode(node, source)), nil
}

// UpdateRemoteNode updates a store-managed remote node with PATCH semantics.
func (a *API) UpdateRemoteNode(ctx context.Context, request api.UpdateRemoteNodeRequestObject) (api.UpdateRemoteNodeResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	if a.remoteNodeStore == nil {
		return api.UpdateRemoteNode404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: "Remote node not found",
		}, nil
	}

	existing, err := a.remoteNodeStore.GetByID(ctx, request.RemoteNodeId)
	if err != nil {
		if errors.Is(err, remotenode.ErrRemoteNodeNotFound) {
			return api.UpdateRemoteNode404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: "Remote node not found",
			}, nil
		}
		return nil, fmt.Errorf("failed to get remote node: %w", err)
	}

	body := request.Body

	// PATCH semantics: only update fields that are present
	if body.Name != nil && *body.Name != "" {
		existing.Name = *body.Name
	}
	if body.Description != nil {
		existing.Description = *body.Description
	}
	if body.ApiBaseUrl != nil && *body.ApiBaseUrl != "" {
		if err := remotenode.ValidateAPIBaseURL(*body.ApiBaseUrl); err != nil {
			return nil, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
			}
		}
		existing.APIBaseURL = *body.ApiBaseUrl
	}
	if body.AuthType != nil {
		existing.AuthType = remotenode.AuthType(*body.AuthType)
	}
	if body.BasicAuthUsername != nil {
		existing.BasicAuthUsername = *body.BasicAuthUsername
	}
	// Only update credentials if non-empty (empty means "keep existing")
	if body.BasicAuthPassword != nil && *body.BasicAuthPassword != "" {
		existing.BasicAuthPassword = *body.BasicAuthPassword
	}
	if body.AuthToken != nil && *body.AuthToken != "" {
		existing.AuthToken = *body.AuthToken
	}
	if body.SkipTlsVerify != nil {
		existing.SkipTLSVerify = *body.SkipTlsVerify
	}

	existing.UpdatedAt = time.Now().UTC()

	if err := a.remoteNodeStore.Update(ctx, existing); err != nil {
		if errors.Is(err, remotenode.ErrRemoteNodeAlreadyExists) {
			return api.UpdateRemoteNode409JSONResponse{
				Code:    api.ErrorCodeAlreadyExists,
				Message: "Remote node with this name already exists",
			}, nil
		}
		return nil, fmt.Errorf("failed to update remote node: %w", err)
	}

	a.logAudit(ctx, audit.CategoryRemoteNode, "remote_node_update", map[string]string{
		"id":   existing.ID,
		"name": existing.Name,
	})

	return api.UpdateRemoteNode200JSONResponse(toRemoteNodeResponseFromNode(existing, remotenode.SourceStore)), nil
}

// DeleteRemoteNode deletes a store-managed remote node.
func (a *API) DeleteRemoteNode(ctx context.Context, request api.DeleteRemoteNodeRequestObject) (api.DeleteRemoteNodeResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	if a.remoteNodeStore == nil {
		return api.DeleteRemoteNode404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: "Remote node not found",
		}, nil
	}

	// Load node for audit details before deletion
	node, err := a.remoteNodeStore.GetByID(ctx, request.RemoteNodeId)
	if err != nil {
		if errors.Is(err, remotenode.ErrRemoteNodeNotFound) {
			return api.DeleteRemoteNode404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: "Remote node not found",
			}, nil
		}
		return nil, fmt.Errorf("failed to get remote node: %w", err)
	}

	if err := a.remoteNodeStore.Delete(ctx, request.RemoteNodeId); err != nil {
		if errors.Is(err, remotenode.ErrRemoteNodeNotFound) {
			return api.DeleteRemoteNode404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: "Remote node not found",
			}, nil
		}
		return nil, fmt.Errorf("failed to delete remote node: %w", err)
	}

	a.logAudit(ctx, audit.CategoryRemoteNode, "remote_node_delete", map[string]string{
		"id":   node.ID,
		"name": node.Name,
	})

	return api.DeleteRemoteNode204Response{}, nil
}

// TestRemoteNodeConnection tests connectivity to a remote node.
// Supports both store nodes (UUID) and config nodes (cfg:<name>).
func (a *API) TestRemoteNodeConnection(ctx context.Context, request api.TestRemoteNodeConnectionRequestObject) (api.TestRemoteNodeConnectionResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	node, err := a.resolveRemoteNode(ctx, request.RemoteNodeId)
	if err != nil {
		if errors.Is(err, remotenode.ErrRemoteNodeNotFound) {
			return api.TestRemoteNodeConnection404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: "Remote node not found",
			}, nil
		}
		return nil, fmt.Errorf("failed to get remote node: %w", err)
	}

	result := testNodeConnection(ctx, node)
	return api.TestRemoteNodeConnection200JSONResponse(result), nil
}

// resolveRemoteNode looks up a remote node by ID.
// IDs prefixed with "cfg:" are config-sourced nodes resolved by name.
// All other IDs are looked up in the store by UUID.
func (a *API) resolveRemoteNode(ctx context.Context, id string) (*remotenode.RemoteNode, error) {
	if name, ok := strings.CutPrefix(id, remotenode.ConfigNodeIDPrefix); ok {
		if a.remoteNodeResolver == nil {
			return nil, remotenode.ErrRemoteNodeNotFound
		}
		// Use GetConfigByName to resolve only from config source,
		// avoiding store-first precedence of GetByName.
		return a.remoteNodeResolver.GetConfigByName(name)
	}

	// Store-managed node — look up by UUID
	if a.remoteNodeStore == nil {
		return nil, remotenode.ErrRemoteNodeNotFound
	}
	return a.remoteNodeStore.GetByID(ctx, id)
}

// testNodeConnection performs a health check against a remote node.
func testNodeConnection(ctx context.Context, node *remotenode.RemoteNode) api.TestRemoteNodeConnectionResponse {
	healthURL := fmt.Sprintf("%s/health", node.APIBaseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return api.TestRemoteNodeConnectionResponse{
			Success: false,
			Error:   ptrOf(fmt.Sprintf("Failed to create request: %s", err.Error())),
		}
	}

	node.ApplyAuth(req)

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: node.SkipTLSVerify, //nolint:gosec
				MinVersion:         tls.VersionTLS12,
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return api.TestRemoteNodeConnectionResponse{
			Success: false,
			Error:   ptrOf(fmt.Sprintf("Connection failed: %s", err.Error())),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return api.TestRemoteNodeConnectionResponse{
			Success: true,
			Message: ptrOf(fmt.Sprintf("Connected successfully (HTTP %d)", resp.StatusCode)),
		}
	}

	return api.TestRemoteNodeConnectionResponse{
		Success: false,
		Error:   ptrOf(fmt.Sprintf("Health check returned HTTP %d", resp.StatusCode)),
	}
}

// toRemoteNodeResponse converts a resolved node to an API response.
func toRemoteNodeResponse(n remotenode.ResolvedNode) api.RemoteNodeResponse {
	return toRemoteNodeResponseFromNode(n.RemoteNode, n.Source)
}

// toRemoteNodeResponseFromNode converts a domain node to an API response.
func toRemoteNodeResponseFromNode(n *remotenode.RemoteNode, source remotenode.Source) api.RemoteNodeResponse {
	hasCreds := n.BasicAuthPassword != "" || n.AuthToken != ""

	resp := api.RemoteNodeResponse{
		Id:             n.ID,
		Name:           n.Name,
		Description:    ptrOf(n.Description),
		ApiBaseUrl:     n.APIBaseURL,
		AuthType:       api.RemoteNodeResponseAuthType(n.AuthType),
		HasCredentials: ptrOf(hasCreds),
		SkipTlsVerify:  ptrOf(n.SkipTLSVerify),
		Source:         api.RemoteNodeResponseSource(source),
	}

	if !n.CreatedAt.IsZero() {
		resp.CreatedAt = ptrOf(n.CreatedAt)
	}
	if !n.UpdatedAt.IsZero() {
		resp.UpdatedAt = ptrOf(n.UpdatedAt)
	}

	return resp
}
