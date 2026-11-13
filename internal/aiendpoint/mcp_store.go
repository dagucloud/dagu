// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import "context"

// MCPStore keeps the metadata of the MCP server entries. It never keeps a
// credential value: each value is a managed secret of the secret store. An
// implementation is safe for concurrent use and makes the name unique in a scope.
type MCPStore interface {
	// Create saves a new entry. It returns ErrAlreadyExists when the name is in use
	// in the scope.
	Create(ctx context.Context, server *MCPServer) error
	// Get returns the entry of the scope, or ErrNotFound.
	Get(ctx context.Context, scope Scope, name string) (*MCPServer, error)
	// List returns the entries of all scopes, sorted by scope and name. The global
	// scope comes first.
	List(ctx context.Context) ([]*MCPServer, error)
	// Update reads the entry of the scope, applies change and saves the result in
	// one step, as Store.Update does.
	Update(ctx context.Context, scope Scope, name string, change func(*MCPServer) error) (*MCPServer, error)
	// Delete removes the entry of the scope. It returns ErrNotFound when it does not
	// exist.
	Delete(ctx context.Context, scope Scope, name string) error
}
