// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ connection.Store = (*ConnectionStore)(nil)

const connectionStoreName = "connection store"

// notificationSegment holds the notification connection's records. It is
// not a valid workspace name, so no workspace shares it.
const notificationSegment = ".notification"

const upperCaseEscape = '~'

// ConnectionStore persists OAuth apps, connections, token state, and tool
// catalogs in four collections.
type ConnectionStore struct {
	apps     recordHelper
	conns    recordHelper
	tokens   persis.Collection
	catalogs recordHelper
	// lifecycle serializes Create and Delete, which each write several
	// records, across every process that shares the collections.
	lifecycle persis.Lock
	codec     connectionCodec
}

// NewConnectionStore creates a connection store that keeps apps,
// connection metadata, token state, and catalogs in the given collections,
// encrypting secrets with enc. lifecycle must be shared by every store of
// the same collections.
func NewConnectionStore(
	apps, conns, tokens, catalogs persis.Collection,
	lifecycle persis.Lock,
	enc *crypto.Encryptor,
) (*ConnectionStore, error) {
	if apps == nil || conns == nil || tokens == nil || catalogs == nil {
		return nil, errors.New("connection store: collections cannot be nil")
	}
	if lifecycle == nil {
		return nil, errors.New("connection store: lock cannot be nil")
	}
	if enc == nil {
		return nil, errors.New("connection store: encryptor cannot be nil")
	}
	return &ConnectionStore{
		apps:      recordHelper{col: apps, name: connectionStoreName},
		conns:     recordHelper{col: conns, name: connectionStoreName},
		tokens:    tokens,
		catalogs:  recordHelper{col: catalogs, name: connectionStoreName},
		lifecycle: lifecycle,
		codec:     connectionCodec{encryptor: enc},
	}, nil
}

func (s *ConnectionStore) CreateApp(ctx context.Context, app *connection.App) (*connection.App, error) {
	if app == nil {
		return nil, errors.New("connection store: app cannot be nil")
	}
	stored := *app
	stored.ID = uuid.NewString()
	stored.Status = cmp.Or(stored.Status, connection.AppStatusOK)
	stored.CreatedAt = time.Now().UTC()
	stored.UpdatedAt = stored.CreatedAt
	if err := stored.Validate(); err != nil {
		return nil, err
	}
	data, err := s.encodeApp(&stored)
	if err != nil {
		return nil, err
	}
	if err := s.apps.col.Create(ctx, &persis.Record{ID: stored.ID, Data: data, CreatedAt: stored.CreatedAt, UpdatedAt: stored.UpdatedAt}); err != nil {
		return nil, fmt.Errorf("connection store: create app: %w", err)
	}
	return &stored, nil
}

func (s *ConnectionStore) GetApp(ctx context.Context, id string) (*connection.App, error) {
	if uuid.Validate(id) != nil {
		return nil, connection.ErrAppNotFound
	}
	rec, err := s.apps.get(ctx, id, connection.ErrAppNotFound)
	if err != nil {
		return nil, err
	}
	return s.decodeApp(rec)
}

func (s *ConnectionStore) ListApps(ctx context.Context) ([]*connection.App, error) {
	recs, err := s.apps.listTolerant(ctx, "", "app")
	if err != nil {
		return nil, fmt.Errorf("connection store: list apps: %w", err)
	}
	apps := make([]*connection.App, 0, len(recs))
	for _, rec := range recs {
		app, err := s.decodeApp(rec)
		if err != nil {
			logger.Warn(ctx, "connection store: failed to load app", slog.String("record", rec.ID), tag.Error(err))
			continue
		}
		apps = append(apps, app)
	}
	slices.SortFunc(apps, func(a, b *connection.App) int { return cmp.Compare(a.Name, b.Name) })
	return apps, nil
}

func (s *ConnectionStore) UpdateApp(ctx context.Context, id string, fn func(*connection.App) error) (*connection.App, error) {
	if uuid.Validate(id) != nil {
		return nil, connection.ErrAppNotFound
	}
	var updated *connection.App
	err := swapRecord(ctx, s.apps.col, id, connection.ErrAppNotFound, func(current *persis.Record) ([]byte, error) {
		app, err := s.decodeApp(current)
		if err != nil {
			return nil, err
		}
		before := *app
		if err := fn(app); err != nil {
			return nil, err
		}
		app.ID, app.CreatedAt, app.UpdatedAt = before.ID, before.CreatedAt, time.Now().UTC()
		if app.ClientID != before.ClientID || app.ClientSecret != before.ClientSecret {
			app.Status, app.StatusReason = connection.AppStatusOK, ""
		}
		if err := app.Validate(); err != nil {
			return nil, err
		}
		updated = app
		return s.encodeApp(app)
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *ConnectionStore) DeleteApp(ctx context.Context, id string) error {
	if uuid.Validate(id) != nil {
		return connection.ErrAppNotFound
	}
	return s.apps.delete(ctx, id, connection.ErrAppNotFound, "app")
}

func (s *ConnectionStore) Create(ctx context.Context, conn *connection.Connection) (*connection.Connection, error) {
	if conn == nil {
		return nil, errors.New("connection store: connection cannot be nil")
	}
	stored := *conn
	stored.ID = uuid.NewString()
	stored.Holder = cmp.Or(stored.Holder, connection.HolderLocal)
	stored.CreatedAt = time.Now().UTC()
	stored.UpdatedAt = stored.CreatedAt
	if err := stored.Validate(); err != nil {
		return nil, err
	}
	data, err := persis.Encode(connectionToRecord(&stored))
	if err != nil {
		return nil, fmt.Errorf("connection store: encode connection: %w", err)
	}
	id := connectionRecordID(stored.Key())
	rec := &persis.Record{ID: id, Data: data, CreatedAt: stored.CreatedAt, UpdatedAt: stored.UpdatedAt}
	err = s.lifecycle.Do(ctx, func(held func() error) error {
		if err := held(); err != nil {
			return err
		}
		if err := s.conns.col.Create(ctx, rec); err != nil {
			if errors.Is(err, persis.ErrConflict) {
				return connection.ErrAlreadyExists
			}
			return fmt.Errorf("connection store: create connection: %w", err)
		}
		// The metadata claims the key first, so a failed create never
		// replaces the token state of a connection that holds the key.
		if err := s.resetTokenState(ctx, &stored, held); err != nil {
			if rollbackErr := s.conns.col.CompareAndDelete(ctx, rec); rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

// resetTokenState gives conn pending token state, replacing any left under
// its key, so a new connection never inherits an earlier one's tokens.
// Catalogs need no reset: Catalog returns only those saved for conn.
func (s *ConnectionStore) resetTokenState(ctx context.Context, conn *connection.Connection, held func() error) error {
	id := connectionRecordID(conn.Key())
	data, err := s.encodeTokenState(pendingTokenState(conn))
	if err != nil {
		return err
	}
	return retryConflict(ctx, func(ctx context.Context) error {
		current, err := s.tokens.Get(ctx, id)
		if err != nil && !errors.Is(err, persis.ErrNotFound) {
			return err
		}
		if err := held(); err != nil {
			return err
		}
		now := time.Now().UTC()
		return createOrSwap(ctx, s.tokens, current, &persis.Record{ID: id, Data: data, CreatedAt: now, UpdatedAt: now})
	})
}

func (s *ConnectionStore) Get(ctx context.Context, key connection.Key) (*connection.Connection, error) {
	if key.Validate() != nil {
		return nil, connection.ErrNotFound
	}
	rec, err := s.conns.get(ctx, connectionRecordID(key), connection.ErrNotFound)
	if err != nil {
		return nil, err
	}
	return decodeConnectionFor(rec, key)
}

func (s *ConnectionStore) List(ctx context.Context, workspace string) ([]*connection.Connection, error) {
	recs, err := s.conns.listTolerant(ctx, workspaceSegment(workspace)+"/", "connection")
	if err != nil {
		return nil, fmt.Errorf("connection store: list connections: %w", err)
	}
	conns := make([]*connection.Connection, 0, len(recs))
	for _, rec := range recs {
		conn, err := decodeConnection(rec)
		if err != nil {
			logger.Warn(ctx, "connection store: failed to load connection", slog.String("record", rec.ID), tag.Error(err))
			continue
		}
		if conn.Workspace != workspace {
			continue
		}
		conns = append(conns, conn)
	}
	slices.SortFunc(conns, func(a, b *connection.Connection) int { return cmp.Compare(a.Name, b.Name) })
	return conns, nil
}

func (s *ConnectionStore) Update(ctx context.Context, key connection.Key, fn func(*connection.Connection) error) (*connection.Connection, error) {
	if key.Validate() != nil {
		return nil, connection.ErrNotFound
	}
	var updated *connection.Connection
	err := swapRecord(ctx, s.conns.col, connectionRecordID(key), connection.ErrNotFound, func(current *persis.Record) ([]byte, error) {
		conn, err := decodeConnectionFor(current, key)
		if err != nil {
			return nil, err
		}
		before := *conn
		if err := fn(conn); err != nil {
			return nil, err
		}
		if conn.ID != before.ID || conn.Key() != before.Key() {
			return nil, fmt.Errorf("%w: the ID and key of a connection cannot change", connection.ErrInvalid)
		}
		conn.CreatedAt, conn.UpdatedAt = before.CreatedAt, time.Now().UTC()
		if err := conn.Validate(); err != nil {
			return nil, err
		}
		updated = conn
		data, err := persis.Encode(connectionToRecord(conn))
		if err != nil {
			return nil, fmt.Errorf("connection store: encode connection: %w", err)
		}
		return data, nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *ConnectionStore) Delete(ctx context.Context, key connection.Key) error {
	if key.Validate() != nil {
		return connection.ErrNotFound
	}
	id := connectionRecordID(key)
	return s.lifecycle.Do(ctx, func(held func() error) error {
		// The lock keeps Create out until every record of the connection
		// is gone. Each removal also checks that the record still carries
		// the connection's ID, so a delete that resumes after losing the
		// lock never removes a later connection's records.
		conn, err := s.Get(ctx, key)
		if err != nil {
			return err
		}
		// Secrets go first, so an interrupted delete leaves no tokens.
		err = deleteConnectionRecord(ctx, s.tokens, id, conn.ID, held, func(rec *persis.Record) (string, error) {
			var stored tokenStateRecord
			err := persis.Decode(rec, &stored)
			return stored.ConnectionID, err
		})
		if err != nil {
			return fmt.Errorf("connection store: delete token state: %w", err)
		}
		catalogs, err := listAll(ctx, s.catalogs.col, persis.ListQuery{Prefix: id + "/"})
		if err != nil {
			return fmt.Errorf("connection store: list catalogs: %w", err)
		}
		for _, rec := range catalogs {
			err := deleteConnectionRecord(ctx, s.catalogs.col, rec.ID, conn.ID, held, func(rec *persis.Record) (string, error) {
				var stored catalogRecord
				err := persis.Decode(rec, &stored)
				return stored.ConnectionID, err
			})
			if err != nil {
				return fmt.Errorf("connection store: delete catalog: %w", err)
			}
		}
		err = deleteConnectionRecord(ctx, s.conns.col, id, conn.ID, held, func(rec *persis.Record) (string, error) {
			var stored connectionRecord
			err := persis.Decode(rec, &stored)
			return stored.ID, err
		})
		if err != nil {
			return fmt.Errorf("connection store: delete connection: %w", err)
		}
		return nil
	})
}

// deleteConnectionRecord removes record id of col while owner reads
// connectionID from it. A record that is missing or belongs to another
// connection is kept.
func deleteConnectionRecord(
	ctx context.Context,
	col persis.Collection,
	id, connectionID string,
	held func() error,
	owner func(*persis.Record) (string, error),
) error {
	return retryConflict(ctx, func(ctx context.Context) error {
		rec, err := col.Get(ctx, id)
		if errors.Is(err, persis.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		got, err := owner(rec)
		if err != nil {
			return err
		}
		if got != connectionID {
			return nil
		}
		if err := held(); err != nil {
			return err
		}
		err = col.CompareAndDelete(ctx, rec)
		if errors.Is(err, persis.ErrNotFound) {
			return nil
		}
		return err
	})
}

func (s *ConnectionStore) TokenState(ctx context.Context, conn *connection.Connection) (*connection.TokenState, error) {
	if conn == nil || conn.Key().Validate() != nil {
		return nil, connection.ErrNotFound
	}
	rec, err := s.tokens.Get(ctx, connectionRecordID(conn.Key()))
	if errors.Is(err, persis.ErrNotFound) {
		return pendingTokenState(conn), nil
	}
	if err != nil {
		return nil, err
	}
	state, err := s.decodeTokenState(rec)
	if err != nil {
		return nil, err
	}
	if state.ConnectionID != conn.ID {
		return pendingTokenState(conn), nil
	}
	return state, nil
}

func (s *ConnectionStore) UpdateTokenState(
	ctx context.Context,
	conn *connection.Connection,
	fn func(*connection.TokenState) error,
) (*connection.TokenState, error) {
	if conn == nil || conn.Key().Validate() != nil {
		return nil, connection.ErrNotFound
	}
	id := connectionRecordID(conn.Key())
	var updated *connection.TokenState
	err := retryConflict(ctx, func(ctx context.Context) error {
		// Only Create writes a new record, so a write that races Delete
		// fails instead of bringing the tokens back.
		current, err := s.tokens.Get(ctx, id)
		if errors.Is(err, persis.ErrNotFound) {
			return connection.ErrNotFound
		}
		if err != nil {
			return err
		}
		state, err := s.decodeTokenState(current)
		if err != nil {
			return err
		}
		if state.ConnectionID != conn.ID {
			return connection.ErrNotFound
		}
		if err := fn(state); err != nil {
			return err
		}
		state.ConnectionID = conn.ID
		data, err := s.encodeTokenState(state)
		if err != nil {
			return err
		}
		next := &persis.Record{ID: id, Data: data, CreatedAt: current.CreatedAt, UpdatedAt: time.Now().UTC()}
		if err := createOrSwap(ctx, s.tokens, current, next); err != nil {
			return err
		}
		updated = state
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// ensureCurrent returns ErrNotFound unless conn is the connection that
// holds its key.
func (s *ConnectionStore) ensureCurrent(ctx context.Context, conn *connection.Connection) error {
	current, err := s.Get(ctx, conn.Key())
	if err != nil {
		return err
	}
	if current.ID != conn.ID {
		return connection.ErrNotFound
	}
	return nil
}

func (s *ConnectionStore) Catalog(ctx context.Context, conn *connection.Connection, service string) (*connection.Catalog, error) {
	if conn == nil || conn.Key().Validate() != nil || connection.ValidateName(service) != nil {
		return nil, connection.ErrCatalogNotFound
	}
	rec, err := s.catalogs.get(ctx, catalogRecordID(conn.Key(), service), connection.ErrCatalogNotFound)
	if err != nil {
		return nil, err
	}
	var stored catalogRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("connection store: decode catalog: %w", err)
	}
	if stored.ConnectionID != conn.ID {
		return nil, connection.ErrCatalogNotFound
	}
	return catalogFromRecord(&stored), nil
}

func (s *ConnectionStore) SaveCatalog(ctx context.Context, conn *connection.Connection, catalog *connection.Catalog) (*connection.Catalog, error) {
	if conn == nil || catalog == nil {
		return nil, errors.New("connection store: connection and catalog cannot be nil")
	}
	if err := connection.ValidateName(catalog.Service); err != nil {
		return nil, fmt.Errorf("service: %w", err)
	}
	if err := s.ensureCurrent(ctx, conn); err != nil {
		return nil, err
	}
	// A catalog saved for a connection that was replaced meanwhile keeps
	// the old ID, so Catalog never returns it for the new connection.
	stored := catalogToRecord(conn.ID, catalog)
	if err := s.catalogs.put(ctx, catalogRecordID(conn.Key(), catalog.Service), stored); err != nil {
		return nil, err
	}
	return catalogFromRecord(stored), nil
}

func (s *ConnectionStore) encodeApp(app *connection.App) ([]byte, error) {
	rec, err := s.codec.appToRecord(app)
	if err != nil {
		return nil, err
	}
	return persis.Encode(rec)
}

func (s *ConnectionStore) decodeApp(rec *persis.Record) (*connection.App, error) {
	var stored appRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("connection store: decode app: %w", err)
	}
	return s.codec.appFromRecord(&stored)
}

// decodeConnectionFor decodes rec and returns ErrNotFound unless it holds
// the connection with key.
func decodeConnectionFor(rec *persis.Record, key connection.Key) (*connection.Connection, error) {
	conn, err := decodeConnection(rec)
	if err != nil {
		return nil, err
	}
	if conn.Key() != key {
		return nil, connection.ErrNotFound
	}
	return conn, nil
}

func decodeConnection(rec *persis.Record) (*connection.Connection, error) {
	var stored connectionRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("connection store: decode connection: %w", err)
	}
	return connectionFromRecord(&stored), nil
}

func (s *ConnectionStore) encodeTokenState(state *connection.TokenState) ([]byte, error) {
	rec, err := s.codec.tokenStateToRecord(state)
	if err != nil {
		return nil, err
	}
	return persis.Encode(rec)
}

func (s *ConnectionStore) decodeTokenState(rec *persis.Record) (*connection.TokenState, error) {
	var stored tokenStateRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("connection store: decode token state: %w", err)
	}
	return s.codec.tokenStateFromRecord(&stored)
}

func pendingTokenState(conn *connection.Connection) *connection.TokenState {
	return &connection.TokenState{ConnectionID: conn.ID, Status: connection.TokenStatusPending}
}

// swapRecord replaces the record id with the data next derives from it,
// reading again and rederiving when another write came between. It returns
// notFound when the record does not exist.
func swapRecord(
	ctx context.Context,
	col persis.Collection,
	id string,
	notFound error,
	next func(current *persis.Record) ([]byte, error),
) error {
	return retryConflict(ctx, func(ctx context.Context) error {
		current, err := col.Get(ctx, id)
		if errors.Is(err, persis.ErrNotFound) {
			return notFound
		}
		if err != nil {
			return err
		}
		data, err := next(current)
		if err != nil {
			return err
		}
		return createOrSwap(ctx, col, current, &persis.Record{
			ID: id, Data: data, CreatedAt: current.CreatedAt, UpdatedAt: time.Now().UTC(),
		})
	})
}

// connectionRecordID names the records of the connection with key in the
// connection and token collections. Its catalogs are below it.
func connectionRecordID(key connection.Key) string {
	return workspaceSegment(key.Workspace) + "/" + key.Name
}

func catalogRecordID(key connection.Key, service string) string {
	return connectionRecordID(key) + "/" + service
}

// workspaceSegment writes each uppercase letter as upperCaseEscape and its
// lowercase form. Workspace names differ in case, and a file system that
// ignores case would otherwise give two workspaces one record. The escape
// is not a workspace name character, so no two names share a segment.
func workspaceSegment(workspace string) string {
	if workspace == "" {
		return notificationSegment
	}
	var b strings.Builder
	for _, r := range workspace {
		if unicode.IsUpper(r) {
			b.WriteRune(upperCaseEscape)
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
