// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/connection"
)

// revokeTimeout bounds one revocation, which runs after the write that
// dropped its token. A callback that revokes still answers within the
// server's write timeout: consentTimeout, then writeTimeout, then this.
const revokeTimeout = 5 * time.Second

// RevokedToken names the refresh token that a revocation is of.
type RevokedToken string

const (
	// RevokedLive is the live token of a removed connection.
	RevokedLive RevokedToken = "live"
	// RevokedReplaced is the live token that a confirmed reconnect
	// replaced.
	RevokedReplaced RevokedToken = "replaced"
	// RevokedUnconfirmed is a consent's token that was never confirmed:
	// its session expired, was cancelled or replaced, or its connection
	// was removed.
	RevokedUnconfirmed RevokedToken = "unconfirmed"
)

// Revocation is the outcome of one revocation. It holds no secret.
type Revocation struct {
	Connection   connection.Key
	ConnectionID string
	// App is the ID of the connection's app.
	App   string
	Token RevokedToken
	// Reason is the provider's error, without secrets, or empty when the
	// token was revoked.
	Reason string
}

// Remove deletes conn with its token state and tool catalogs, then revokes
// its live refresh token and the token of a session in progress, as Engine
// states. It deletes under the connection's lock, so no refresh runs
// meanwhile, and once it holds the lock it runs to its end even when the
// caller goes away. Token state that is missing or cannot be decoded is
// deleted without revocation. It returns connection.ErrNotFound when conn
// was removed or replaced by a connection with the same key.
func (e *Engine) Remove(ctx context.Context, conn *connection.Connection) error {
	tokens, err := e.removeLocked(ctx, conn)
	// What follows the lock runs to its end, like the delete.
	ctx = context.WithoutCancel(ctx)
	// The write that ended the session dropped its token, also when the
	// delete failed after it.
	e.revoke(ctx, conn, tokens.staged, RevokedUnconfirmed)
	// The check follows the delete, so of two removes that share a grant,
	// the later one finds no other holder and revokes.
	if tokens.deleted && tokens.live != "" && e.revoker(conn, RevokedLive) != nil && !e.grantShared(ctx, conn, tokens.accountID) {
		e.revoke(ctx, conn, tokens.live, RevokedLive)
	}
	if err != nil {
		return fmt.Errorf("connection %q: remove: %w", conn.Name, err)
	}
	return nil
}

// removedTokens are the tokens that Remove's write read as it ended the
// session.
type removedTokens struct {
	live      string
	staged    string
	accountID string
	// deleted reports that the token state is gone, also when a later
	// step of the delete failed, so no retry can read the live token.
	deleted bool
}

// removeLocked ends conn's session and deletes conn while holding its
// lock, and returns the tokens that the ending write read. With the
// session gone, only a session started between that write and the delete
// can change the tokens; a callback after the delete finds the connection
// removed and revokes its own token.
func (e *Engine) removeLocked(ctx context.Context, conn *connection.Connection) (removedTokens, error) {
	lock, err := e.lock(ctx, conn.Key())
	if err != nil {
		return removedTokens{}, err
	}
	defer lock.Unlock()
	ctx = context.WithoutCancel(ctx)

	tokens, err := e.endForRemove(ctx, conn)
	switch {
	case errors.Is(err, connection.ErrNotFound):
	case errors.Is(err, connection.ErrCorrupt):
		logger.Warn(ctx, "Removing a connection whose token state cannot be read, without revocation",
			slog.String("workspace", conn.Workspace), slog.String("connection", conn.Name), tag.Error(err))
	case err != nil:
		return removedTokens{}, err
	}
	// Every remove of the key holds its lock, so a connection that this
	// check finds stays until the delete.
	current, err := e.store.Get(ctx, conn.Key())
	if err == nil && current.ID != conn.ID {
		err = connection.ErrNotFound
	}
	if err == nil {
		err = lock.Held()
	}
	if err == nil {
		err = e.store.Delete(ctx, conn.Key())
		tokens.deleted = err == nil || e.tokenStateGone(ctx, conn)
	}
	return tokens, err
}

// tokenStateGone reports whether conn's token state no longer holds a
// refresh token, as after a delete that removed it and then failed on a
// catalog.
func (e *Engine) tokenStateGone(ctx context.Context, conn *connection.Connection) bool {
	st, err := e.store.TokenState(ctx, conn)
	return err == nil && st.RefreshToken == ""
}

// lock takes the lock of key, waiting while another holder, such as a
// refresh, has it. The lock renews itself until Unlock, also after ctx
// ends.
func (e *Engine) lock(ctx context.Context, key connection.Key) (connection.Lock, error) {
	for {
		lock, err := e.locks.TryLock(context.WithoutCancel(ctx), key)
		if !errors.Is(err, connection.ErrLocked) {
			return lock, err
		}
		if err := e.locks.WaitUnlocked(ctx, key); err != nil {
			return nil, err
		}
	}
}

// endForRemove ends conn's session and returns the tokens as read in that
// write.
func (e *Engine) endForRemove(ctx context.Context, conn *connection.Connection) (removedTokens, error) {
	var tokens removedTokens
	_, err := e.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		tokens = removedTokens{live: st.RefreshToken, staged: stagedToken(st), accountID: st.AccountID}
		if st.Session == nil {
			return errUnchanged
		}
		st.Session = nil
		return nil
	})
	if errors.Is(err, errUnchanged) {
		err = nil
	}
	if err != nil {
		return removedTokens{}, err
	}
	return tokens, nil
}

// grantShared reports whether a connection other than conn, in any
// workspace or for notifications, holds accountID on conn's client, live
// or in a session. When any record it needs cannot be read, the grant
// counts as shared.
func (e *Engine) grantShared(ctx context.Context, conn *connection.Connection, accountID string) bool {
	shared, err := e.holdsGrant(ctx, conn, accountID)
	if err != nil {
		logger.Warn(ctx, "Keeping a removed connection's grant, since other connections cannot be checked",
			slog.String("workspace", conn.Workspace), slog.String("connection", conn.Name), tag.Error(err))
		return true
	}
	return shared
}

func (e *Engine) holdsGrant(ctx context.Context, conn *connection.Connection, accountID string) (bool, error) {
	app, err := e.store.GetApp(ctx, conn.App)
	if err != nil {
		return false, err
	}
	conns, err := e.store.ListAll(ctx)
	if err != nil {
		return false, err
	}
	// Apps are compared by client, since two apps may hold one client.
	sameClient := map[string]bool{app.ID: true}
	for _, other := range conns {
		if other.ID == conn.ID {
			continue
		}
		same, seen := sameClient[other.App]
		if !seen {
			a, err := e.store.GetApp(ctx, other.App)
			switch {
			case errors.Is(err, connection.ErrAppNotFound):
			case err != nil:
				return false, fmt.Errorf("connection %q: %w", other.Name, err)
			default:
				same = a.Provider == app.Provider && a.ClientID == app.ClientID
			}
			sameClient[other.App] = same
		}
		if !same {
			continue
		}
		st, err := e.store.TokenState(ctx, other)
		if err != nil {
			return false, fmt.Errorf("connection %q: %w", other.Name, err)
		}
		if st.AccountID == accountID || (st.Session != nil && st.Session.Tokens.AccountID == accountID) {
			return true, nil
		}
	}
	return false, nil
}

// revoker returns conn's provider when it can revoke a token as which, or
// nil.
func (e *Engine) revoker(conn *connection.Connection, which RevokedToken) Revoker {
	r, ok := e.providers[conn.Provider].(Revoker)
	if !ok || (which != RevokedLive && r.RevokeScope() != RevokesToken) {
		return nil
	}
	return r
}

// revoke revokes token, which a write of conn dropped, when conn's
// provider can revoke it as which, and reports the outcome to OnRevoke.
func (e *Engine) revoke(ctx context.Context, conn *connection.Connection, token string, which RevokedToken) {
	if token == "" {
		return
	}
	r := e.revoker(conn, which)
	if r == nil {
		return
	}
	// The write that dropped the token is done, so the revocation runs to
	// its own deadline even when the caller has gone.
	ctx = context.WithoutCancel(ctx)
	revokeCtx, cancel := context.WithTimeout(ctx, revokeTimeout)
	defer cancel()
	var reason string
	app, err := e.store.GetApp(revokeCtx, conn.App)
	if err == nil {
		err = r.Revoke(revokeCtx, app, token)
		if err != nil {
			reason = redact(err.Error(), token, app.ClientSecret)
		}
	} else {
		reason = err.Error()
	}
	if reason != "" {
		reason = truncate(reason, maxReasonBytes)
		logger.Warn(ctx, "Failed to revoke a connection's refresh token",
			slog.String("workspace", conn.Workspace), slog.String("connection", conn.Name),
			slog.String("revoked", string(which)), slog.String("reason", reason))
	}
	if e.onRevoke != nil {
		e.onRevoke(ctx, Revocation{
			Connection:   conn.Key(),
			ConnectionID: conn.ID,
			App:          conn.App,
			Token:        which,
			Reason:       reason,
		})
	}
}
