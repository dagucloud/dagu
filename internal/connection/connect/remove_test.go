// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connect_test

import (
	"context"
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

// A connection whose token state is missing, as a crash inside create
// leaves it, or cannot be read is still removed, with nothing revoked.
func TestRemoveUnreadableTokenState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		damage func(t *testing.T, path string)
	}{
		{name: "Missing", damage: func(t *testing.T, path string) { require.NoError(t, os.Remove(path)) }},
		{name: "Corrupt", damage: func(t *testing.T, path string) { require.NoError(t, os.WriteFile(path, []byte("{"), 0o600)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.connect(t, "sub-1")
			files := f.tokenFiles(t)
			require.Len(t, files, 1)
			tt.damage(t, files[0])

			require.NoError(t, f.engine.Remove(context.Background(), f.conn))
			f.assertRemoved(t)
			assert.Empty(t, f.tokenFiles(t))
			assert.Empty(t, f.provider.revokedTokens())
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
		refreshing, release := make(chan struct{}), make(chan struct{})
		f.provider.set(func(p *fakeProvider) {
			p.rotateTo = "rt-rotated"
			p.onRefresh = func() {
				close(refreshing)
				<-release
			}
		})

		b := f.broker(t)
		var tokenErr, removeErr error
		var wg sync.WaitGroup
		wg.Go(func() { _, _, tokenErr = b.Token(ctx, f.conn, "", time.Minute) })
		<-refreshing
		wg.Go(func() { removeErr = f.engine.Remove(ctx, f.conn) })
		close(release)
		wg.Wait()

		require.NoError(t, tokenErr)
		require.NoError(t, removeErr)
		assert.Equal(t, []string{"rt-rotated"}, f.provider.revokedTokens())
		f.assertRemoved(t)
	})
	t.Run("RemoveFirst", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.connect(t, "sub-1")
		revoking, release := make(chan struct{}), make(chan struct{})
		f.provider.set(func(p *fakeProvider) {
			p.onRevoke = func() {
				close(revoking)
				<-release
			}
		})

		b := f.broker(t)
		var tokenErr, removeErr error
		var wg sync.WaitGroup
		wg.Go(func() { removeErr = f.engine.Remove(ctx, f.conn) })
		<-revoking
		wg.Go(func() { _, _, tokenErr = b.Token(ctx, f.conn, "", time.Minute) })
		close(release)
		wg.Wait()

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
// reconnect that the expected account confirms at once does the same.
func TestConfirmRevokesReplacedToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name   string
		scope  connect.RevokeScope
		live   string
		atOnce bool
		want   []string
	}{
		{name: "RevokesToken", scope: connect.RevokesToken, live: "rt-0", want: []string{"rt-0"}},
		{name: "RevokesGrant", scope: connect.RevokesGrant, live: "rt-0"},
		// Some providers return the same refresh token on each consent.
		{name: "SameToken", scope: connect.RevokesToken, live: "rt-1"},
		{name: "AtOnce", scope: connect.RevokesToken, live: "rt-0", atOnce: true, want: []string{"rt-0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.provider.set(func(p *fakeProvider) { p.scope = tt.scope })
			f.connect(t, "sub-1")
			f.update(t, func(st *connection.TokenState) { st.RefreshToken = tt.live })
			if tt.atOnce {
				f.provider.vouched = "user@example.com"
				auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback, ExpectedAccount: "user@example.com"})
				require.NoError(t, f.engine.Callback(ctx, f.provider.consent(t, auth)))
			} else {
				auth := f.consented(t)
				require.NoError(t, f.engine.Confirm(ctx, f.conn, auth.SessionID, starter))
			}
			assert.Equal(t, "rt-1", f.state(t).RefreshToken)
			assert.Equal(t, tt.want, f.provider.revokedTokens())
		})
	}
}

// A confirm revokes the live token as it reads it in its own write, so a
// token that a refresh rotated just before is the one revoked.
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
	b, err := broker.New(broker.Config{Store: f.store, Locks: f.stores.ConnectionLocks()})
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
func (f *fixture) addConnection(t *testing.T, workspace, clientID, accountID string) {
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
}

func (f *fixture) assertRemoved(t *testing.T) {
	t.Helper()
	_, err := f.store.Get(context.Background(), f.conn.Key())
	assert.ErrorIs(t, err, connection.ErrNotFound)
}

// tokenFiles returns the files of the token state collection.
func (f *fixture) tokenFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(filepath.Join(f.dataDir, "connection-tokens"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return err
	})
	require.NoError(t, err)
	return files
}
