// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import "context"

// Store keeps the metadata of the endpoints. It never keeps a key: the key is a
// managed secret of the secret store. An implementation is safe for concurrent
// use and makes the name unique in a scope.
type Store interface {
	// Create saves a new endpoint. It returns ErrAlreadyExists when the name is in
	// use in the scope.
	Create(ctx context.Context, endpoint *Endpoint) error
	// Get returns the endpoint of the scope, or ErrNotFound.
	Get(ctx context.Context, scope Scope, name string) (*Endpoint, error)
	// List returns the endpoints of all scopes, sorted by scope and name. The
	// global scope comes first.
	List(ctx context.Context) ([]*Endpoint, error)
	// Update reads the endpoint of the scope, applies change and saves the result
	// in one step: a write of another process between the read and the save makes
	// the store read again and apply change again. It returns the saved endpoint,
	// ErrNotFound when it does not exist, or the error of change.
	Update(ctx context.Context, scope Scope, name string, change func(*Endpoint) error) (*Endpoint, error)
	// Delete removes the endpoint of the scope. It returns ErrNotFound when it does
	// not exist.
	Delete(ctx context.Context, scope Scope, name string) error
}
