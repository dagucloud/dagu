// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrNotFound        = errors.New("connection not found")
	ErrAlreadyExists   = errors.New("connection already exists")
	ErrAppNotFound     = errors.New("oauth app not found")
	ErrCatalogNotFound = errors.New("tool catalog not found")
	// ErrInvalid reports a record or key that cannot be stored.
	ErrInvalid = errors.New("invalid connection record")
	// ErrReserved reports a value reserved for Dagu Cloud.
	ErrReserved = errors.New("reserved for Dagu Cloud")
)

// Store keeps OAuth apps, connections, their token state, and their tool
// catalogs. Secret fields are encrypted at rest and returned in plaintext.
// Each update function receives a copy of the current record and may be
// called more than once, so it must not have side effects.
type Store interface {
	// CreateApp stores a new app, assigning its ID and an empty status
	// AppStatusOK, and returns it.
	CreateApp(ctx context.Context, app *App) (*App, error)
	// GetApp returns the app with id, or ErrAppNotFound.
	GetApp(ctx context.Context, id string) (*App, error)
	// ListApps returns every app, ordered by name.
	ListApps(ctx context.Context) ([]*App, error)
	// UpdateApp applies fn to the app with id and stores the result, or
	// returns ErrAppNotFound. A change of the client ID or secret resets
	// the status to AppStatusOK.
	UpdateApp(ctx context.Context, id string, fn func(*App) error) (*App, error)
	// DeleteApp removes the app with id, or returns ErrAppNotFound.
	DeleteApp(ctx context.Context, id string) error

	// Create stores a new connection, assigning its ID and an empty holder
	// HolderLocal, and returns it. It returns ErrAlreadyExists when the key
	// is taken. The connection starts with pending token state and no
	// catalogs, replacing any left under its key.
	Create(ctx context.Context, conn *Connection) (*Connection, error)
	// Get returns the connection with key, or ErrNotFound.
	Get(ctx context.Context, key Key) (*Connection, error)
	// List returns the connections of one workspace, ordered by name.
	List(ctx context.Context, workspace string) ([]*Connection, error)
	// ListAll returns the connections of every workspace and the
	// notification connection.
	ListAll(ctx context.Context) ([]*Connection, error)
	// Update applies fn to the connection with key and stores the result,
	// or returns ErrNotFound. fn cannot change the ID or key.
	Update(ctx context.Context, key Key, fn func(*Connection) error) (*Connection, error)
	// Delete removes the connection with key, with its token state and
	// catalogs, also when they cannot be read, or returns ErrNotFound.
	Delete(ctx context.Context, key Key) error

	// TokenState returns the token state of conn. When the state is missing
	// or belongs to another connection with the same key, it returns
	// pending state without tokens.
	TokenState(ctx context.Context, conn *Connection) (*TokenState, error)
	// UpdateTokenState applies fn to the token state of conn and stores the
	// result only when no other write came between the read and the write;
	// otherwise it reads again and reapplies fn. It never creates token
	// state: it returns ErrNotFound when the state is missing, or when conn
	// was removed or replaced by a connection with the same key.
	UpdateTokenState(ctx context.Context, conn *Connection, fn func(*TokenState) error) (*TokenState, error)

	// Catalog returns the catalog of service that was saved for conn, or
	// ErrCatalogNotFound.
	Catalog(ctx context.Context, conn *Connection, service string) (*Catalog, error)
	// SaveCatalog replaces the catalog of catalog.Service for conn and
	// returns the stored catalog, with each tool's hash set. catalog is not
	// modified. It returns ErrNotFound when conn was removed or replaced by
	// a connection with the same key.
	SaveCatalog(ctx context.Context, conn *Connection, catalog *Catalog) (*Catalog, error)
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
