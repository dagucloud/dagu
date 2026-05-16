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
	CategoryTerminal   Category = "terminal"
	CategoryUser       Category = "user"
	CategoryDAG        Category = "dag"
	CategoryAPIKey     Category = "api_key"
	CategoryWebhook    Category = "webhook"
	CategoryGitSync    Category = "git_sync"
	CategoryAgent      Category = "agent"
	CategorySystem     Category = "system"
	CategoryRemoteNode Category = "remote_node"
	CategoryWorkspace  Category = "workspace"
	CategorySecret     Category = "secret"
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
	// UserID filters entries by the user who performed the action.
	UserID string
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
