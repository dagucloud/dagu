// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

type connectionFixture struct {
	store *store.ConnectionStore
	conns persis.Collection
}

func newConnectionFixture(t *testing.T) connectionFixture {
	t.Helper()
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	backend := testutil.NewMemoryBackend()
	conns := backend.Collection(persis.CollectionConnections)
	s, err := store.NewConnectionStore(
		backend.Collection(persis.CollectionOAuthApps),
		conns,
		backend.Collection(persis.CollectionConnectionTokens),
		backend.Collection(persis.CollectionConnectionCatalogs),
		enc,
	)
	require.NoError(t, err)
	return connectionFixture{store: s, conns: conns}
}

func newTestApp() *connection.App {
	return &connection.App{
		Name:         "Google",
		Provider:     connection.ProviderGoogle,
		Kind:         connection.AppKindBYO,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		ClientType:   connection.ClientTypeWeb,
	}
}

func newTestConnection(workspace, name string) *connection.Connection {
	return &connection.Connection{
		Workspace: workspace,
		Name:      name,
		Provider:  connection.ProviderGoogle,
		App:       "app-1",
		Grant:     connection.GrantAuthorizationCode,
		Services:  []connection.Service{{Name: "gmail", Access: connection.AccessRead}},
	}
}

func setRefreshToken(token string) func(*connection.TokenState) error {
	return func(state *connection.TokenState) error {
		state.RefreshToken = token
		state.Status = connection.TokenStatusConnected
		return nil
	}
}

func TestConnectionCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newConnectionFixture(t)

	created, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, connection.HolderLocal, created.Holder)

	got, err := f.store.Get(ctx, created.Key())
	require.NoError(t, err)
	assert.Equal(t, created, got)

	_, err = f.store.Create(ctx, newTestConnection("payments", "mail"))
	assert.ErrorIs(t, err, connection.ErrAlreadyExists)

	state, err := f.store.TokenState(ctx, created)
	require.NoError(t, err)
	assert.Equal(t, connection.TokenStatusPending, state.Status)
}

// A name is unique only in its workspace, and the notification connection,
// which has no workspace, is apart from the global workspace's.
func TestConnectionNameScopedToWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newConnectionFixture(t)

	for _, ws := range []string{"payments", "billing", secret.GlobalWorkspace, ""} {
		_, err := f.store.Create(ctx, newTestConnection(ws, "mail"))
		require.NoError(t, err, ws)
	}

	for _, ws := range []string{"payments", secret.GlobalWorkspace, ""} {
		list, err := f.store.List(ctx, ws)
		require.NoError(t, err)
		require.Len(t, list, 1, ws)
		assert.Equal(t, ws, list[0].Workspace)
	}
}

func TestConnectionCreateRefusesCloudHolder(t *testing.T) {
	t.Parallel()
	f := newConnectionFixture(t)
	conn := newTestConnection("payments", "mail")
	conn.Holder = connection.HolderCloud

	_, err := f.store.Create(context.Background(), conn)
	assert.ErrorIs(t, err, connection.ErrReserved)
}

// Token state and catalogs left under a key, as by a delete that stopped
// partway, never reach a new connection with that key.
func TestConnectionCreateReplacesLeftState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newConnectionFixture(t)
	old, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)
	_, err = f.store.UpdateTokenState(ctx, old, setRefreshToken("old-refresh"))
	require.NoError(t, err)
	require.NoError(t, f.store.SaveCatalog(ctx, old.Key(), &connection.Catalog{Service: "gmail"}))
	require.NoError(t, f.conns.Delete(ctx, "payments/mail"))

	created, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)

	state, err := f.store.TokenState(ctx, created)
	require.NoError(t, err)
	assert.Empty(t, state.RefreshToken)
	assert.Equal(t, connection.TokenStatusPending, state.Status)
	_, err = f.store.Catalog(ctx, created.Key(), "gmail")
	assert.ErrorIs(t, err, connection.ErrCatalogNotFound)
}

// A writer that read a connection before it was removed and created again
// under the same name cannot write the new connection's token state.
func TestUpdateTokenStateRefusesReplacedConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newConnectionFixture(t)
	old, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)
	require.NoError(t, f.store.Delete(ctx, old.Key()))
	created, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)

	_, err = f.store.UpdateTokenState(ctx, old, setRefreshToken("old-refresh"))
	assert.ErrorIs(t, err, connection.ErrNotFound)

	state, err := f.store.TokenState(ctx, created)
	require.NoError(t, err)
	assert.Empty(t, state.RefreshToken)
}

func TestUpdateTokenStateRefusesRemovedConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newConnectionFixture(t)
	conn, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)
	require.NoError(t, f.store.Delete(ctx, conn.Key()))

	_, err = f.store.UpdateTokenState(ctx, conn, setRefreshToken("refresh"))
	assert.ErrorIs(t, err, connection.ErrNotFound)
	_, err = f.store.Get(ctx, conn.Key())
	assert.ErrorIs(t, err, connection.ErrNotFound)
}

// Concurrent writers each see the previous writer's result, so none is lost.
func TestUpdateTokenStateConcurrentWriters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newConnectionFixture(t)
	conn, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)

	const writers = 8
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			_, err := f.store.UpdateTokenState(ctx, conn, func(state *connection.TokenState) error {
				state.Generation++
				return nil
			})
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	state, err := f.store.TokenState(ctx, conn)
	require.NoError(t, err)
	assert.Equal(t, int64(writers), state.Generation)
}

func TestTokenStateRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newConnectionFixture(t)
	conn, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)
	expiry := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	want, err := f.store.UpdateTokenState(ctx, conn, func(state *connection.TokenState) error {
		state.Tokens = connection.Tokens{
			RefreshToken:  "refresh",
			AccessTokens:  map[string]connection.AccessToken{"graph": {Token: "access", ExpiresAt: expiry}},
			GrantedScopes: []string{"openid"},
		}
		state.Session = &connection.Session{
			ID:     "session-1",
			Phase:  connection.SessionPhaseConfirm,
			Tokens: connection.Tokens{RefreshToken: "session-refresh"},
		}
		return nil
	})
	require.NoError(t, err)

	got, err := f.store.TokenState(ctx, conn)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestConnectionUpdateKeepsKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newConnectionFixture(t)
	conn, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)

	updated, err := f.store.Update(ctx, conn.Key(), func(c *connection.Connection) error {
		c.Account = "user@example.com"
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "user@example.com", updated.Account)

	_, err = f.store.Update(ctx, conn.Key(), func(c *connection.Connection) error {
		c.Name = "other"
		return nil
	})
	assert.ErrorIs(t, err, connection.ErrInvalid)
}

func TestAppCreateRefusesHostedKind(t *testing.T) {
	t.Parallel()
	f := newConnectionFixture(t)
	app := newTestApp()
	app.Kind = connection.AppKindHosted

	_, err := f.store.CreateApp(context.Background(), app)
	assert.ErrorIs(t, err, connection.ErrReserved)
}

// The broker marks a rejected client invalid, and an admin's new secret
// makes it usable again.
func TestAppUpdateResetsStatusOnNewSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newConnectionFixture(t)
	app, err := f.store.CreateApp(ctx, newTestApp())
	require.NoError(t, err)
	assert.Equal(t, connection.AppStatusOK, app.Status)

	_, err = f.store.UpdateApp(ctx, app.ID, func(a *connection.App) error {
		a.Status, a.StatusReason = connection.AppStatusInvalid, "invalid_client"
		return nil
	})
	require.NoError(t, err)

	updated, err := f.store.UpdateApp(ctx, app.ID, func(a *connection.App) error {
		a.ClientSecret = "new-secret"
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, connection.AppStatusOK, updated.Status)
	assert.Empty(t, updated.StatusReason)

	got, err := f.store.GetApp(ctx, app.ID)
	require.NoError(t, err)
	assert.Equal(t, updated, got)
}

func TestSaveCatalogHashesTools(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newConnectionFixture(t)
	conn, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)

	hashOf := func(description, schema string) string {
		t.Helper()
		require.NoError(t, f.store.SaveCatalog(ctx, conn.Key(), &connection.Catalog{
			Service: "gmail",
			Tools:   []connection.Tool{{Name: "search", Description: description, InputSchema: json.RawMessage(schema)}},
		}))
		catalog, err := f.store.Catalog(ctx, conn.Key(), "gmail")
		require.NoError(t, err)
		require.Len(t, catalog.Tools, 1)
		return catalog.Tools[0].Hash
	}

	original := hashOf("Search mail", `{"type":"object"}`)
	assert.NotEmpty(t, original)
	assert.Equal(t, original, hashOf("Search mail", `{ "type": "object" }`))
	assert.NotEqual(t, original, hashOf("Search and delete mail", `{"type":"object"}`))
}

func TestSaveCatalogRequiresConnection(t *testing.T) {
	t.Parallel()
	f := newConnectionFixture(t)

	err := f.store.SaveCatalog(context.Background(), connection.Key{Workspace: "payments", Name: "mail"}, &connection.Catalog{Service: "gmail"})
	assert.ErrorIs(t, err, connection.ErrNotFound)
}
