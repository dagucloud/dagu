// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/service/audit"
)

// ListAuditLogs returns audit log entries matching the filter criteria.
// Requires audit license and manager or admin role.
func (a *API) ListAuditLogs(ctx context.Context, request api.ListAuditLogsRequestObject) (api.ListAuditLogsResponseObject, error) {
	// Require manager or admin role (auth before license check)
	if err := a.requireManagerOrAbove(ctx); err != nil {
		return nil, err
	}

	if err := a.requireLicensedAudit(); err != nil {
		return nil, err
	}

	// Check that audit service is configured
	if a.auditService == nil {
		return nil, &Error{
			Code:       api.ErrorCodeInternalError,
			Message:    "Audit logging is not configured",
			HTTPStatus: http.StatusServiceUnavailable,
		}
	}

	// Build filter from query parameters
	filter := audit.QueryFilter{}

	if request.Params.Category != nil {
		filter.Category = audit.Category(*request.Params.Category)
	}
	if request.Params.Action != nil {
		filter.Action = *request.Params.Action
	}
	if request.Params.Source != nil {
		filter.Source = *request.Params.Source
	}
	if request.Params.Surface != nil {
		filter.Surface = *request.Params.Surface
	}
	if request.Params.Result != nil {
		filter.Result = *request.Params.Result
	}
	if request.Params.CorrelationId != nil {
		filter.CorrelationID = *request.Params.CorrelationId
	}
	if request.Params.ResourceType != nil {
		filter.ResourceType = *request.Params.ResourceType
	}
	if request.Params.ResourceId != nil {
		filter.ResourceID = *request.Params.ResourceId
	}
	if request.Params.Workspace != nil {
		filter.Workspace = *request.Params.Workspace
	}
	if request.Params.CredentialId != nil {
		filter.CredentialID = *request.Params.CredentialId
	}
	if request.Params.CredentialType != nil {
		filter.CredentialType = *request.Params.CredentialType
	}
	if request.Params.McpTool != nil {
		filter.MCPTool = *request.Params.McpTool
	}
	if request.Params.IpAddress != nil {
		filter.IPAddress = *request.Params.IpAddress
	}
	if request.Params.UserId != nil {
		filter.UserID = *request.Params.UserId
	}
	if request.Params.StartTime != nil {
		filter.StartTime = *request.Params.StartTime
	}
	if request.Params.EndTime != nil {
		filter.EndTime = *request.Params.EndTime
	}
	if request.Params.Limit != nil {
		filter.Limit = *request.Params.Limit
	}
	if request.Params.Offset != nil {
		filter.Offset = *request.Params.Offset
	}

	// Apply pagination defaults and caps
	const (
		defaultLimit = 50
		maxLimit     = 500
	)
	if filter.Limit <= 0 {
		filter.Limit = defaultLimit
	} else if filter.Limit > maxLimit {
		filter.Limit = maxLimit
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}

	// Query the audit service
	result, err := a.auditService.Query(ctx, filter)
	if err != nil {
		return nil, &Error{
			Code:       api.ErrorCodeInternalError,
			Message:    "Failed to query audit logs",
			HTTPStatus: http.StatusInternalServerError,
		}
	}

	// Convert to API response
	entries := make([]api.AuditEntry, 0, len(result.Entries))
	for _, e := range result.Entries {
		entry := api.AuditEntry{
			Id:        e.ID,
			Timestamp: e.Timestamp,
			Category:  string(e.Category),
			Action:    e.Action,
			UserId:    e.UserID,
			Username:  e.Username,
		}
		if e.Details != "" {
			entry.Details = &e.Details
		}
		if e.IPAddress != "" {
			entry.IpAddress = &e.IPAddress
		}
		if e.Source != "" {
			entry.Source = &e.Source
		}
		if e.Surface != "" {
			entry.Surface = &e.Surface
		}
		if e.Result != "" {
			entry.Result = &e.Result
		}
		if e.CorrelationID != "" {
			entry.CorrelationId = &e.CorrelationID
		}
		if e.ResourceType != "" {
			entry.ResourceType = &e.ResourceType
		}
		if e.ResourceID != "" {
			entry.ResourceId = &e.ResourceID
		}
		if e.Workspace != "" {
			entry.Workspace = &e.Workspace
		}
		if e.CredentialID != "" {
			entry.CredentialId = &e.CredentialID
		}
		if e.CredentialType != "" {
			entry.CredentialType = &e.CredentialType
		}
		if e.MCPTool != "" {
			entry.McpTool = &e.MCPTool
		}
		entries = append(entries, entry)
	}

	return api.ListAuditLogs200JSONResponse{
		Entries: entries,
		Total:   result.Total,
	}, nil
}
