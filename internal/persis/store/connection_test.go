// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
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

// connectionFixture exposes the collections so tests can leave records
// behind as an interrupted write would.
type connectionFixture struct {
	store  *store.ConnectionStore
	conns  persis.Collection
	tokens persis.Collection
}

func newConnectionFixture(t *testing.T) connectionFixture {
	t.Helper()
	backend := testutil.NewMemoryBackend()
	return newConnectionFixtureWith(t, backend, backend.Collection(persis.CollectionConnectionTokens), backend.Lock(persis.CollectionConnections))
}

func newConnectionFixtureWith(
	t *testing.T,
	backend *testutil.MemoryBackend,
	tokens persis.Collection,
	lifecycle persis.Lock,
) connectionFixture {
	t.Helper()
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	conns := backend.Collection(persis.CollectionConnections)
	s, err := store.NewConnectionStore(
		backend.Collection(persis.CollectionOAuthApps),
		conns,
		tokens,
		backend.Collection(persis.CollectionConnectionCatalogs),
		lifecycle,
		enc,
	)
	require.NoError(t, err)
	return connectionFixture{store: s, conns: conns, tokens: tokens}
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

// Workspace names may differ only in case, and each keeps its own
// connections also where record IDs ignore case, as on macOS and Windows.
func TestConnectionWorkspacesDifferingInCase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	backend := testutil.NewMemoryBackend()
	fold := func(name string) persis.Collection { return caseFoldingCollection{backend.Collection(name)} }
	s, err := store.NewConnectionStore(
		fold(persis.CollectionOAuthApps),
		fold(persis.CollectionConnections),
		fold(persis.CollectionConnectionTokens),
		fold(persis.CollectionConnectionCatalogs),
		backend.Lock(persis.CollectionConnections),
		enc,
	)
	require.NoError(t, err)

	lower, err := s.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)
	upper, err := s.Create(ctx, newTestConnection("Payments", "mail"))
	require.NoError(t, err)
	_, err = s.UpdateTokenState(ctx, lower, setRefreshToken("lower-refresh"))
	require.NoError(t, err)

	got, err := s.Get(ctx, upper.Key())
	require.NoError(t, err)
	assert.Equal(t, upper.ID, got.ID)
	state, err := s.TokenState(ctx, upper)
	require.NoError(t, err)
	assert.Empty(t, state.RefreshToken)

	require.NoError(t, s.Delete(ctx, upper.Key()))
	state, err = s.TokenState(ctx, lower)
	require.NoError(t, err)
	assert.Equal(t, "lower-refresh", state.RefreshToken)
}

// caseFoldingCollection keys records by their lowercased ID, as a file
// system that ignores case does.
type caseFoldingCollection struct {
	persis.Collection
}

func foldRecord(rec *persis.Record) *persis.Record {
	folded := *rec
	folded.ID = strings.ToLower(rec.ID)
	return &folded
}

func (c caseFoldingCollection) Get(ctx context.Context, id string) (*persis.Record, error) {
	return c.Collection.Get(ctx, strings.ToLower(id))
}

func (c caseFoldingCollection) Put(ctx context.Context, rec *persis.Record) error {
	return c.Collection.Put(ctx, foldRecord(rec))
}

func (c caseFoldingCollection) Create(ctx context.Context, rec *persis.Record) error {
	return c.Collection.Create(ctx, foldRecord(rec))
}

func (c caseFoldingCollection) Delete(ctx context.Context, id string) error {
	return c.Collection.Delete(ctx, strings.ToLower(id))
}

func (c caseFoldingCollection) CompareAndDelete(ctx context.Context, expected *persis.Record) error {
	return c.Collection.CompareAndDelete(ctx, foldRecord(expected))
}

func (c caseFoldingCollection) CompareAndSwap(ctx context.Context, id string, expected, next []byte) error {
	return c.Collection.CompareAndSwap(ctx, strings.ToLower(id), expected, next)
}

func (c caseFoldingCollection) List(ctx context.Context, q persis.ListQuery) (*persis.Page, error) {
	q.Prefix = strings.ToLower(q.Prefix)
	return c.Collection.List(ctx, q)
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
	_, err = f.store.SaveCatalog(ctx, old, &connection.Catalog{Service: "gmail"})
	require.NoError(t, err)
	require.NoError(t, f.conns.Delete(ctx, "payments/mail"))

	created, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)

	state, err := f.store.TokenState(ctx, created)
	require.NoError(t, err)
	assert.Empty(t, state.RefreshToken)
	assert.Equal(t, connection.TokenStatusPending, state.Status)
	_, err = f.store.Catalog(ctx, created, "gmail")
	assert.ErrorIs(t, err, connection.ErrCatalogNotFound)
}

// A writer that read a connection before it was removed and created again
// under the same name can neither read nor write the new connection's token
// state or catalogs.
func TestStaleConnectionCannotReachReplacement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newConnectionFixture(t)
	old, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)
	require.NoError(t, f.store.Delete(ctx, old.Key()))
	created, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)
	_, err = f.store.UpdateTokenState(ctx, created, setRefreshToken("new-refresh"))
	require.NoError(t, err)
	_, err = f.store.SaveCatalog(ctx, created, &connection.Catalog{Service: "gmail"})
	require.NoError(t, err)

	_, err = f.store.UpdateTokenState(ctx, old, setRefreshToken("old-refresh"))
	assert.ErrorIs(t, err, connection.ErrNotFound)
	stale, err := f.store.TokenState(ctx, old)
	require.NoError(t, err)
	assert.Empty(t, stale.RefreshToken)
	_, err = f.store.Catalog(ctx, old, "gmail")
	assert.ErrorIs(t, err, connection.ErrCatalogNotFound)
	_, err = f.store.SaveCatalog(ctx, old, &connection.Catalog{Service: "gmail"})
	assert.ErrorIs(t, err, connection.ErrNotFound)

	state, err := f.store.TokenState(ctx, created)
	require.NoError(t, err)
	assert.Equal(t, "new-refresh", state.RefreshToken)
}

// Only Create writes token state, so a write that finds none, as one that
// lands while Delete runs does, fails instead of bringing tokens back.
func TestUpdateTokenStateNeverCreatesState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newConnectionFixture(t)
	conn, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)
	require.NoError(t, f.tokens.Delete(ctx, "payments/mail"))

	_, err = f.store.UpdateTokenState(ctx, conn, setRefreshToken("refresh"))
	assert.ErrorIs(t, err, connection.ErrNotFound)
	state, err := f.store.TokenState(ctx, conn)
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

// A delete that starts while Create writes the token state waits for it and
// then removes that state, so no later token write for the removed
// connection succeeds.
func TestDeleteWaitsForCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	tokens := &claimingCollection{Collection: backend.Collection(persis.CollectionConnectionTokens)}
	lock := &enteringLock{Lock: backend.Lock(persis.CollectionConnections), entered: make(chan struct{})}
	f := newConnectionFixtureWith(t, backend, tokens, lock)
	deleted := make(chan error, 1)
	finishedEarly := false
	tokens.during = func() {
		lock.armed.Store(true)
		go func() { deleted <- f.store.Delete(ctx, connection.Key{Workspace: "payments", Name: "mail"}) }()
		// Create holds the lock here, so a Delete that asks for it cannot
		// finish until Create does.
		select {
		case <-deleted:
			finishedEarly = true
		case <-lock.entered:
		}
	}
	tokens.armed.Store(true)

	conn, err := f.store.Create(ctx, newTestConnection("payments", "mail"))
	require.NoError(t, err)
	require.False(t, finishedEarly, "Delete finished while Create was writing token state")
	require.Equal(t, int32(1), lock.holdersAtEntry.Load(), "Create did not hold the lock while writing token state")
	require.NoError(t, <-deleted)

	_, err = f.store.UpdateTokenState(ctx, conn, setRefreshToken("refresh"))
	assert.ErrorIs(t, err, connection.ErrNotFound)
}

// enteringLock, once armed, closes entered when the next caller asks for
// the lock, before it waits for it, and records how many callers held the
// lock at that moment.
type enteringLock struct {
	persis.Lock
	armed          atomic.Bool
	holders        atomic.Int32
	holdersAtEntry atomic.Int32
	entered        chan struct{}
}

func (l *enteringLock) Do(ctx context.Context, fn func(held func() error) error) error {
	if l.armed.CompareAndSwap(true, false) {
		l.holdersAtEntry.Store(l.holders.Load())
		close(l.entered)
	}
	return l.Lock.Do(ctx, func(held func() error) error {
		l.holders.Add(1)
		defer l.holders.Add(-1)
		return fn(held)
	})
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
			Account:       "user@example.com",
			AccountID:     "sub-1",
			RefreshToken:  "refresh",
			AccessTokens:  map[string]connection.AccessToken{"graph": {Token: "access", ExpiresAt: expiry, IssuedAt: expiry.Add(-time.Hour)}},
			GrantedScopes: []string{"openid"},
		}
		state.RefreshErrors = map[string]connection.RefreshError{"outlook": {Reason: "status 503", At: expiry}}
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
		c.Subject = "user@example.com"
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "user@example.com", updated.Subject)

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
		saved, err := f.store.SaveCatalog(ctx, conn, &connection.Catalog{
			Service: "gmail",
			Tools:   []connection.Tool{{Name: "search", Description: description, InputSchema: json.RawMessage(schema)}},
		})
		require.NoError(t, err)
		catalog, err := f.store.Catalog(ctx, conn, "gmail")
		require.NoError(t, err)
		assert.Equal(t, saved, catalog)
		require.Len(t, catalog.Tools, 1)
		return catalog.Tools[0].Hash
	}

	original := hashOf("Search mail", `{"type":"object","required":["q"]}`)
	assert.NotEmpty(t, original)
	assert.Equal(t, original, hashOf("Search mail", `{ "type": "object", "required": ["q"] }`))
	assert.Equal(t, original, hashOf("Search mail", `{"required":["q"],"type":"object"}`))
	assert.NotEqual(t, original, hashOf("Search and delete mail", `{"type":"object","required":["q"]}`))
}
