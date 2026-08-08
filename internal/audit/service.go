// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"context"
	"encoding/json"
)

// Store defines the interface for persisting audit entries.
type Store interface {
	// Append adds a new audit entry to the store.
	Append(ctx context.Context, entry *Entry) error
	// Query retrieves audit entries matching the filter.
	Query(ctx context.Context, filter QueryFilter) (*QueryResult, error)
}

// Service provides audit logging functionality.
type Service struct {
	store Store
}

// New creates a new audit service.
func New(store Store) *Service {
	return &Service{store: store}
}

// Log records an audit entry.
func (s *Service) Log(ctx context.Context, entry *Entry) error {
	return s.store.Append(ctx, entry)
}

// Query retrieves audit entries matching the filter.
func (s *Service) Query(ctx context.Context, filter QueryFilter) (*QueryResult, error) {
	return s.store.Query(ctx, filter)
}

// LogTerminalConnectionStart logs the start of a terminal connection.
func (s *Service) LogTerminalConnectionStart(ctx context.Context, userID, username, connectionID, ipAddress string) error {
	details, _ := json.Marshal(map[string]string{"connection_id": connectionID})
	entry := NewEntry(CategoryTerminal, "connection_start", userID, username).
		WithDetails(string(details)).
		WithIPAddress(ipAddress)
	return s.Log(ctx, entry)
}

// LogTerminalCommand logs a command executed in a terminal connection.
func (s *Service) LogTerminalCommand(ctx context.Context, userID, username, connectionID, command, ipAddress string) error {
	details, _ := json.Marshal(map[string]string{
		"connection_id": connectionID,
		"command":       command,
	})
	entry := NewEntry(CategoryTerminal, "command", userID, username).
		WithDetails(string(details)).
		WithIPAddress(ipAddress)
	return s.Log(ctx, entry)
}

// LogTerminalConnectionEnd logs the end of a terminal connection.
func (s *Service) LogTerminalConnectionEnd(ctx context.Context, userID, username, connectionID, reason, ipAddress string) error {
	details, _ := json.Marshal(map[string]string{
		"connection_id": connectionID,
		"reason":        reason,
	})
	entry := NewEntry(CategoryTerminal, "connection_end", userID, username).
		WithDetails(string(details)).
		WithIPAddress(ipAddress)
	return s.Log(ctx, entry)
}
