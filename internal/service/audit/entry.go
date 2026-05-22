// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package audit provides a generic audit logging system for tracking user actions.
package audit

import (
	"time"

	"github.com/google/uuid"
)

// Category represents the type of feature being audited.
type Category string

// Audit categories for different system components.
const (
	CategoryTerminal     Category = "terminal"
	CategoryUser         Category = "user"
	CategoryDAG          Category = "dag"
	CategoryAPIKey       Category = "api_key"
	CategoryWebhook      Category = "webhook"
	CategoryNotification Category = "notification"
	CategoryIncident     Category = "incident"
	CategoryGitSync      Category = "git_sync"
	CategoryAgent        Category = "agent"
	CategorySystem       Category = "system"
	CategoryRemoteNode   Category = "remote_node"
	CategoryWorkspace    Category = "workspace"
	CategorySecret       Category = "secret"
	CategoryMCP          Category = "mcp"
)

// Entry represents a single audit log entry.
type Entry struct {
	// ID is the unique identifier for the audit entry (UUID).
	ID string `json:"id"`
	// Timestamp is when the audit event occurred (UTC).
	Timestamp time.Time `json:"timestamp"`
	// Category indicates the type of feature being audited.
	Category Category `json:"category"`
	// Action describes the specific action performed.
	Action string `json:"action"`
	// Source identifies the producer surface, such as mcp, ui, rest, cli, or internal.
	Source string `json:"source,omitempty"`
	// Surface identifies the externally accepted API-key surface.
	Surface string `json:"surface,omitempty"`
	// Result identifies whether the event succeeded, failed, or was denied.
	Result string `json:"result,omitempty"`
	// CorrelationID connects MCP attempt events with downstream domain events.
	CorrelationID string `json:"correlation_id,omitempty"`
	// ResourceType identifies the affected resource class.
	ResourceType string `json:"resource_type,omitempty"`
	// ResourceID identifies the affected resource.
	ResourceID string `json:"resource_id,omitempty"`
	// Workspace is the canonical workspace used for audit filtering.
	Workspace string `json:"workspace,omitempty"`
	// CredentialID identifies the credential independently of the subject.
	CredentialID string `json:"credential_id,omitempty"`
	// CredentialType is api_key, jwt, basic, oidc, or none.
	CredentialType string `json:"credential_type,omitempty"`
	// MCPTool stores the MCP tool name for MCP-originated activity.
	MCPTool string `json:"mcp_tool,omitempty"`
	// UserID is the unique identifier of the user who performed the action.
	UserID string `json:"user_id"`
	// Username is the human-readable name of the user.
	Username string `json:"username"`
	// Details contains additional context about the action (JSON-encoded).
	Details string `json:"details,omitempty"`
	// IPAddress is the IP address from which the action was performed.
	IPAddress string `json:"ip_address,omitempty"`
}

// NewEntry creates a new audit entry with a generated ID and current timestamp.
func NewEntry(category Category, action, userID, username string) *Entry {
	return &Entry{
		ID:        uuid.New().String(),
		Timestamp: time.Now().UTC(),
		Category:  category,
		Action:    action,
		UserID:    userID,
		Username:  username,
	}
}

// WithDetails adds details to the entry.
func (e *Entry) WithDetails(details string) *Entry {
	e.Details = details
	return e
}

// WithIPAddress adds an IP address to the entry.
func (e *Entry) WithIPAddress(ip string) *Entry {
	e.IPAddress = ip
	return e
}

// QueryFilter defines filters for querying audit entries.
type QueryFilter struct {
	// Category filters entries by audit category.
	Category Category
	// Action filters entries by action name.
	Action string
	// Source filters entries by producer surface.
	Source string
	// Surface filters entries by API-key surface.
	Surface string
	// Result filters entries by outcome.
	Result string
	// CorrelationID filters entries by correlation ID.
	CorrelationID string
	// ResourceType filters entries by resource type.
	ResourceType string
	// ResourceID filters entries by resource ID.
	ResourceID string
	// Workspace filters entries by canonical workspace.
	Workspace string
	// CredentialID filters entries by credential ID.
	CredentialID string
	// CredentialType filters entries by credential type.
	CredentialType string
	// MCPTool filters entries by MCP tool name.
	MCPTool string
	// UserID filters entries by the user who performed the action.
	UserID string
	// IPAddress filters entries by client IP address.
	IPAddress string
	// StartTime is the inclusive start of the time range.
	StartTime time.Time
	// EndTime is the exclusive end of the time range.
	EndTime time.Time
	// Limit is the maximum number of entries to return.
	Limit int
	// Offset is the number of entries to skip for pagination.
	Offset int
}

// QueryResult contains the result of a query.
type QueryResult struct {
	// Entries contains the audit entries matching the query.
	Entries []*Entry `json:"entries"`
	// Total is the total count of matching entries before pagination.
	Total int `json:"total"`
}
