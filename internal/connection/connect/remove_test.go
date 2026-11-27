// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connect_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/connection/connect"
)

const (
	tokensDir   = "connection-tokens"
	catalogsDir = "connection-catalogs"
	// barrierTimeout ends a test whose operation hangs before a barrier.
	barrierTimeout = time.Minute
)

func TestRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.connect(t, "sub-1")

	require.NoError(t, f.engine.Remove(ctx, f.conn))
	assert.Equal(t, []string{"rt-0"}, f.provider.revokedTokens())
	assert.Equal(t, []connect.Revocation{{
		Connection:   f.conn.Key(),
		ConnectionID: f.conn.ID,
		App:          f.conn.App,
		Token:        connect.RevokedLive,
	}}, f.revoked.list())
	f.assertRemoved(t)
	assert.ErrorIs(t, f.engine.Remove(ctx, f.conn), connection.ErrNotFound)
}

// Another connection with the same account on the same client keeps the
// grant: the removed connection's token is not revoked. Apps count as the
// same client when their client IDs match.
func TestRemoveKeepsSharedGrant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		workspace string
		clientID  string
		accountID string
		want      []string
	}{
		{name: "OtherWorkspace", workspace: "dev", clientID: "client", accountID: "sub-1"},
		{name: "Notification", clientID: "client", accountID: "sub-1"},
		{name: "OtherAccount", workspace: "dev", clientID: "client", accountID: "sub-2", want: []string{"rt-0"}},
		{name: "OtherClient", workspace: "dev", clientID: "other", accountID: "sub-1", want: []string{"rt-0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.connect(t, "sub-1")
			f.addConnection(t, tt.workspace, tt.clientID, tt.accountID)

			require.NoError(t, f.engine.Remove(context.Background(), f.conn))
			assert.Equal(t, tt.want, f.provider.revokedTokens())
			f.assertRemoved(t)
		})
	}
}

// Two connections of one grant removed at once leave no holder, so at
// least one of them revokes: each checks for other holders after its own
// delete, and the later delete finds none.
func TestConcurrentRemovesRevokeGrant(t *testing.T) {
	t.Parallel()
	for i := range 20 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newFixture(t)
			f.connect(t, "sub-1")
			other := f.addConnection(t, "dev", "client", "sub-1")

			var firstErr, secondErr error
			var wg sync.WaitGroup
			wg.Go(func() { firstErr = f.engine.Remove(ctx, f.conn) })
			wg.Go(func() { secondErr = f.engine.Remove(ctx, other) })
			wg.Wait()

			require.NoError(t, firstErr)
			require.NoError(t, secondErr)
			revoked := f.provider.revokedTokens()
			assert.NotEmpty(t, revoked)
			assert.Subset(t, []string{"rt-0", "rt-other"}, revoked)
		})
	}
}

// Remove deletes before it revokes, so while the revocation runs, no
// session starts and no earlier session completes its consent.
func TestRemoveDeletesBeforeRevoking(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.connect(t, "sub-1")
	earlier := f.provider.consent(t, f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback}))
	revoking := make(chan struct{})
	release := newGate(t)
	f.provider.set(func(p *fakeProvider) {
		p.onRevoke = func() {
			close(revoking)
			release.wait()
		}
	})

	var removeErr error
	removed := goDone(func() { removeErr = f.engine.Remove(ctx, f.conn) })
	await(t, revoking, removed)
	_, err := f.engine.Start(ctx, f.conn, connect.StartOptions{User: starter, Redirect: serverCallback})
	assert.ErrorIs(t, err, connection.ErrNotFound)
	assert.ErrorIs(t, f.engine.Callback(ctx, earlier), connect.ErrCallbackRefused)
	release.open()
	<-removed

	require.NoError(t, removeErr)
	assert.Zero(t, f.provider.exchanges())
	assert.Equal(t, []string{"rt-0"}, f.provider.revokedTokens())
}

// A token state read that fails for a reason other than a corrupt record
// fails the remove and changes nothing, so a retry still revokes.
func TestRemoveFailsOnStoreError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.connect(t, "sub-1")
	errDisk := errors.New("disk unavailable")
	f.writes.fail(errDisk)

	assert.ErrorIs(t, f.engine.Remove(ctx, f.conn), errDisk)
	_, err := f.store.Get(ctx, f.conn.Key())
	require.NoError(t, err)
	assert.Empty(t, f.provider.revokedTokens())

	f.writes.fail(nil)
	require.NoError(t, f.engine.Remove(ctx, f.conn))
	assert.Equal(t, []string{"rt-0"}, f.provider.revokedTokens())
}

// A connection whose token state is missing, as a crash inside create
// leaves it, or whose token state or catalog cannot be read is still
// removed, with every file of it. Nothing is revoked without readable
// token state.
func TestRemoveUnreadableRecords(t *testing.T) {
	t.Parallel()
	remove := func(t *testing.T, path string) { require.NoError(t, os.Remove(path)) }
	corrupt := func(t *testing.T, path string) { require.NoError(t, os.WriteFile(path, []byte("{"), 0o600)) }
	tests := []struct {
		name   string
		dir    string
		damage func(t *testing.T, path string)
		want   []string
	}{
		{name: "MissingTokenState", dir: tokensDir, damage: remove},
		{name: "CorruptTokenState", dir: tokensDir, damage: corrupt},
		{name: "CorruptCatalog", dir: catalogsDir, damage: corrupt, want: []string{"rt-0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.connect(t, "sub-1")
			_, err := f.store.SaveCatalog(context.Background(), f.conn, &connection.Catalog{Service: "svc", ListedAt: time.Now()})
			require.NoError(t, err)
			files := f.files(t, tt.dir)
			require.Len(t, files, 1)
			tt.damage(t, files[0])

			require.NoError(t, f.engine.Remove(context.Background(), f.conn))
			f.assertRemoved(t)
			assert.Empty(t, f.files(t, tokensDir))
			assert.Empty(t, f.files(t, catalogsDir))
			assert.Equal(t, tt.want, f.provider.revokedTokens())
		})
	}
}

// Remove and a refresh take the same lock. A remove that waits for a
// refresh revokes the token the refresh rotated to. A refresh that waits
// for a remove calls no provider and fails.
func TestRemoveRacesRefresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("RefreshFirst", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.connect(t, "sub-1")
		refreshing := make(chan struct{})
		release := newGate(t)
		f.provider.set(func(p *fakeProvider) {
			p.rotateTo = "rt-rotated"
			p.onRefresh = func() {
				close(refreshing)
				release.wait()
			}
		})

		b := f.broker(t)
		var tokenErr, removeErr error
		refreshed := goDone(func() { _, _, tokenErr = b.Token(ctx, f.conn, "", time.Minute) })
		await(t, refreshing, refreshed)
		removed := goDone(func() { removeErr = f.engine.Remove(ctx, f.conn) })
		await(t, f.locks.contended, removed)
		release.open()
		<-refreshed
		<-removed

		require.NoError(t, tokenErr)
		require.NoError(t, removeErr)
		assert.Equal(t, []string{"rt-rotated"}, f.provider.revokedTokens())
		f.assertRemoved(t)
	})
	t.Run("RemoveFirst", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.connect(t, "sub-1")
		deleting := make(chan struct{})
		release := newGate(t)
		f.writes.beforeDelete(func() {
			close(deleting)
			release.wait()
		})

		b := f.broker(t)
		var tokenErr, removeErr error
		removed := goDone(func() { removeErr = f.engine.Remove(ctx, f.conn) })
		await(t, deleting, removed)
		refreshed := goDone(func() { _, _, tokenErr = b.Token(ctx, f.conn, "", time.Minute) })
		await(t, f.locks.contended, refreshed)
		release.open()
		<-removed
		<-refreshed

		require.NoError(t, removeErr)
		assert.ErrorIs(t, tokenErr, broker.ErrReconnectRequired)
		assert.Zero(t, f.provider.refreshCount())
		f.assertRemoved(t)
	})
}

// A confirm and a remove that race revoke both tokens once, whichever
// comes first: the confirm revokes the token it replaced and the remove
// the new one, or the remove revokes the live token and the session's.
func TestRemoveRacesConfirm(t *testing.T) {
	t.Parallel()
	for i := range 20 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newFixture(t)
			f.provider.set(func(p *fakeProvider) { p.scope = connect.RevokesToken })
			f.connect(t, "sub-1")
			auth := f.consented(t)

			var removeErr error
			var wg sync.WaitGroup
			wg.Go(func() { removeErr = f.engine.Remove(ctx, f.conn) })
			wg.Go(func() { _ = f.engine.Confirm(ctx, f.conn, auth.SessionID, starter) })
			wg.Wait()

			require.NoError(t, removeErr)
			assert.ElementsMatch(t, []string{"rt-0", "rt-1"}, f.provider.revokedTokens())
			f.assertRemoved(t)
		})
	}
}

// A confirmed reconnect revokes the token it replaced only where that
// leaves the rest of the grant, and never a token it keeps live. A
// reconnect that the expected account confirms at once does the same, and
// a token that a refresh rotated before the confirm is the one replaced.
func TestConfirmRevokesReplacedToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name    string
		scope   connect.RevokeScope
		live    string
		atOnce  bool
		rotated bool
		want    []string
	}{
		{name: "RevokesToken", scope: connect.RevokesToken, live: "rt-0", want: []string{"rt-0"}},
		{name: "RevokesGrant", scope: connect.RevokesGrant, live: "rt-0"},
		// A provider may return the live refresh token again.
		{name: "SameToken", scope: connect.RevokesToken, live: "rt-1"},
		{name: "AtOnce", scope: connect.RevokesToken, live: "rt-0", atOnce: true, want: []string{"rt-0"}},
		{name: "Rotated", scope: connect.RevokesToken, live: "rt-0", rotated: true, want: []string{"rt-rotated"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.provider.set(func(p *fakeProvider) {
				p.scope = tt.scope
				p.rotateTo = "rt-rotated"
			})
			f.connect(t, "sub-1")
			f.update(t, func(st *connection.TokenState) { st.RefreshToken = tt.live })
			if tt.atOnce {
				f.provider.vouched = "user@example.com"
				auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback, ExpectedAccount: "user@example.com"})
				require.NoError(t, f.engine.Callback(ctx, f.provider.consent(t, auth)))
			} else {
				auth := f.consented(t)
				if tt.rotated {
					_, _, err := f.broker(t).Token(ctx, f.conn, "", time.Minute)
					require.NoError(t, err)
					require.Equal(t, "rt-rotated", f.state(t).RefreshToken)
				}
				require.NoError(t, f.engine.Confirm(ctx, f.conn, auth.SessionID, starter))
			}
			assert.Equal(t, "rt-1", f.state(t).RefreshToken)
			assert.Equal(t, tt.want, f.provider.revokedTokens())
		})
	}
}

// A confirm that races a refresh revokes exactly one token, never the one
// it made live. Which one depends on the order: TestConfirmRevokesReplacedToken
// pins the rotated case.
func TestConfirmRacesRefresh(t *testing.T) {
	t.Parallel()
	for i := range 20 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newFixture(t)
			f.provider.set(func(p *fakeProvider) {
				p.scope = connect.RevokesToken
				p.rotateTo = "rt-rotated"
			})
			f.connect(t, "sub-1")
			auth := f.consented(t)

			b := f.broker(t)
			var tokenErr, confirmErr error
			var wg sync.WaitGroup
			wg.Go(func() { _, _, tokenErr = b.Token(ctx, f.conn, "", time.Minute) })
			wg.Go(func() { confirmErr = f.engine.Confirm(ctx, f.conn, auth.SessionID, starter) })
			wg.Wait()

			require.NoError(t, tokenErr)
			require.NoError(t, confirmErr)
			assert.Equal(t, "rt-1", f.state(t).RefreshToken)
			revoked := f.provider.revokedTokens()
			require.Len(t, revoked, 1)
			if f.provider.refreshCount() == 0 {
				assert.Equal(t, "rt-0", revoked[0])
			} else {
				assert.Contains(t, []string{"rt-0", "rt-rotated"}, revoked[0])
			}
		})
	}
}

// A session's tokens that are never confirmed leave the record when the
// session is cancelled, replaced, or expires, or its connection is
// removed. They are revoked only where that leaves the rest of the grant,
// and once, also when the expired session is read again.
func TestUnconfirmedTokenDropped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name string
		end  func(t *testing.T, f *fixture, auth *connect.Authorization)
	}{
		{name: "Cancel", end: func(t *testing.T, f *fixture, auth *connect.Authorization) {
			require.NoError(t, f.engine.Cancel(ctx, f.conn, auth.SessionID))
		}},
		{name: "Replace", end: func(t *testing.T, f *fixture, _ *connect.Authorization) {
			f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
		}},
		{name: "ExpirePoll", end: func(t *testing.T, f *fixture, auth *connect.Authorization) {
			f.clock.advance(sessionTTL)
			for range 2 {
				status, err := f.engine.Session(ctx, f.conn, auth.SessionID)
				require.NoError(t, err)
				assert.Equal(t, connect.ReasonExpired, status.Reason)
			}
		}},
		{name: "ExpireConfirm", end: func(t *testing.T, f *fixture, auth *connect.Authorization) {
			f.clock.advance(sessionTTL)
			assert.ErrorIs(t, f.engine.Confirm(ctx, f.conn, auth.SessionID, starter), connect.ErrSessionEnded)
			assert.ErrorIs(t, f.engine.Cancel(ctx, f.conn, auth.SessionID), connect.ErrSessionEnded)
		}},
		{name: "Remove", end: func(t *testing.T, f *fixture, _ *connect.Authorization) {
			require.NoError(t, f.engine.Remove(ctx, f.conn))
		}},
	}
	for _, tt := range tests {
		for _, scope := range []connect.RevokeScope{connect.RevokesToken, connect.RevokesGrant} {
			t.Run(tt.name+"/"+string(scope), func(t *testing.T) {
				t.Parallel()
				f := newFixture(t)
				f.provider.set(func(p *fakeProvider) { p.scope = scope })
				auth := f.consented(t)

				tt.end(t, f, auth)
				if s := f.state(t).Session; s != nil {
					assert.Empty(t, s.Tokens.RefreshToken)
				}
				if scope == connect.RevokesGrant {
					assert.Empty(t, f.provider.revokedTokens())
					return
				}
				assert.Equal(t, []string{"rt-1"}, f.provider.revokedTokens())
				revs := f.revoked.list()
				require.Len(t, revs, 1)
				assert.Equal(t, connect.RevokedUnconfirmed, revs[0].Token)
			})
		}
	}
}

// A consent whose session was replaced during its provider calls is never
// stored, and its token is revoked.
func TestCallbackForEndedSessionRevokes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.provider.set(func(p *fakeProvider) { p.scope = connect.RevokesToken })
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	f.provider.onProbe = func() { f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback}) }

	err := f.engine.Callback(context.Background(), f.provider.consent(t, auth))
	assert.ErrorIs(t, err, connect.ErrCallbackRefused)
	assert.Equal(t, []string{"rt-1"}, f.provider.revokedTokens())
}

// A revocation that fails is reported without the token, and the confirm
// or remove that dropped the token completes.
func TestRevokeFailureNeverBlocks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.provider.set(func(p *fakeProvider) { p.scope = connect.RevokesToken })
	f.provider.failRevoke.Store(true)
	f.connect(t, "sub-1")
	auth := f.consented(t)

	require.NoError(t, f.engine.Confirm(ctx, f.conn, auth.SessionID, starter))
	assert.Equal(t, "rt-1", f.state(t).RefreshToken)
	require.NoError(t, f.engine.Remove(ctx, f.conn))
	f.assertRemoved(t)

	revs := f.revoked.list()
	require.Len(t, revs, 2)
	assert.Equal(t, connect.RevokedReplaced, revs[0].Token)
	assert.Equal(t, connect.RevokedLive, revs[1].Token)
	for _, rev := range revs {
		assert.Contains(t, rev.Reason, "503")
		assert.NotContains(t, rev.Reason, "rt-0")
		assert.NotContains(t, rev.Reason, "rt-1")
	}
}

func (f *fixture) broker(t *testing.T) *broker.Broker {
	t.Helper()
	b, err := broker.New(broker.Config{Store: f.store, Locks: f.locks})
	require.NoError(t, err)
	return b
}

// consented starts a session and consents, so the session holds rt-1 in
// confirm.
func (f *fixture) consented(t *testing.T) *connect.Authorization {
	t.Helper()
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	require.NoError(t, f.engine.Callback(context.Background(), f.provider.consent(t, auth)))
	return auth
}

// addConnection adds a connection in workspace, on a new app with
// clientID, whose live tokens are of accountID.
func (f *fixture) addConnection(t *testing.T, workspace, clientID, accountID string) *connection.Connection {
	t.Helper()
	ctx := context.Background()
	app, err := f.store.CreateApp(ctx, &connection.App{
		Name:         "Other",
		Provider:     connection.ProviderOAuth2,
		Kind:         connection.AppKindBYO,
		ClientID:     clientID,
		ClientSecret: "secret",
		ClientType:   connection.ClientTypeWeb,
	})
	require.NoError(t, err)
	conn, err := f.store.Create(ctx, &connection.Connection{
		Workspace: workspace,
		Name:      "work",
		Provider:  connection.ProviderOAuth2,
		App:       app.ID,
		Grant:     connection.GrantAuthorizationCode,
	})
	require.NoError(t, err)
	_, err = f.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		st.Status = connection.TokenStatusConnected
		st.AccountID = accountID
		st.RefreshToken = "rt-other"
		return nil
	})
	require.NoError(t, err)
	return conn
}

func (f *fixture) assertRemoved(t *testing.T) {
	t.Helper()
	_, err := f.store.Get(context.Background(), f.conn.Key())
	assert.ErrorIs(t, err, connection.ErrNotFound)
}

// files returns the record files of the collection in dir.
func (f *fixture) files(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(filepath.Join(f.dataDir, dir), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return err
	})
	require.NoError(t, err)
	return files
}

func (s *failingStore) beforeDelete(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDelete = fn
}

func (s *failingStore) Delete(ctx context.Context, key connection.Key) error {
	s.mu.Lock()
	hook := s.onDelete
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return s.Store.Delete(ctx, key)
}

// contendedLocks signals each TryLock that finds the lock held, so a test
// knows that an operation waits for another before it lets that one end.
type contendedLocks struct {
	connection.Locks
	contended chan struct{}
}

func (l *contendedLocks) TryLock(ctx context.Context, key connection.Key) (connection.Lock, error) {
	lock, err := l.Locks.TryLock(ctx, key)
	if errors.Is(err, connection.ErrLocked) {
		select {
		case l.contended <- struct{}{}:
		default:
		}
	}
	return lock, err
}

// gate holds back provider calls that wait on it until the test opens it,
// which the test's cleanup also does.
type gate struct {
	ch   chan struct{}
	once sync.Once
}

func newGate(t *testing.T) *gate {
	g := &gate{ch: make(chan struct{})}
	t.Cleanup(g.open)
	return g
}

func (g *gate) wait() { <-g.ch }

func (g *gate) open() { g.once.Do(func() { close(g.ch) }) }

// goDone runs fn in a goroutine and returns a channel closed when it ends.
func goDone(fn func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	return done
}

// await waits for signal, and fails the test if the operation behind done
// ends first, as one that fails before it reaches the barrier does, or if
// it hangs. The deadline only ends a hung test; no outcome depends on it.
func await(t *testing.T, signal, done <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-done:
		t.Fatal("the operation ended before it reached the barrier")
	case <-time.After(barrierTimeout):
		t.Fatal("the operation did not reach the barrier")
	}
}
