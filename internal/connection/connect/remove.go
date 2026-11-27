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
// dropped its token.
const revokeTimeout = 10 * time.Second

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

// Remove deletes conn with its token state and tool catalogs. It holds the
// connection's lock, so no refresh runs meanwhile, and first revokes the
// live refresh token and the token of a session in progress, as Engine
// states. A connection whose token state is missing or cannot be read is
// deleted without revocation. It returns connection.ErrNotFound when conn
// was removed or replaced by a connection with the same key.
func (e *Engine) Remove(ctx context.Context, conn *connection.Connection) error {
	lock, err := e.lock(ctx, conn.Key())
	if err != nil {
		return fmt.Errorf("connection %q: remove: %w", conn.Name, err)
	}
	defer lock.Unlock()

	live, staged, accountID, err := e.endForRemove(ctx, conn)
	switch {
	case errors.Is(err, connection.ErrNotFound):
	case err != nil:
		logger.Warn(ctx, "Removing a connection whose token state cannot be read, without revocation",
			slog.String("workspace", conn.Workspace), slog.String("connection", conn.Name), tag.Error(err))
	default:
		e.revoke(ctx, conn, staged, RevokedUnconfirmed)
		if live != "" && !e.grantShared(ctx, conn, accountID) {
			e.revoke(ctx, conn, live, RevokedLive)
		}
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
	}
	if err != nil {
		return fmt.Errorf("connection %q: remove: %w", conn.Name, err)
	}
	return nil
}

// lock takes the lock of key, waiting while another holder, such as a
// refresh, has it.
func (e *Engine) lock(ctx context.Context, key connection.Key) (connection.Lock, error) {
	for {
		lock, err := e.locks.TryLock(ctx, key)
		if !errors.Is(err, connection.ErrLocked) {
			return lock, err
		}
		if err := e.locks.WaitUnlocked(ctx, key); err != nil {
			return nil, err
		}
	}
}

// endForRemove ends conn's session, and returns the live refresh token, the
// session's own one, and the live account ID, as read in that write. With
// the session gone, no confirm or callback changes the tokens before the
// delete, and the broker waits for the lock.
func (e *Engine) endForRemove(ctx context.Context, conn *connection.Connection) (live, staged, accountID string, err error) {
	_, err = e.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		live, staged, accountID = st.RefreshToken, stagedToken(st), st.AccountID
		if st.Session == nil {
			return errUnchanged
		}
		st.Session = nil
		return nil
	})
	if errors.Is(err, errUnchanged) {
		err = nil
	}
	return live, staged, accountID, err
}

// grantShared reports whether a connection other than conn, in any
// workspace or for notifications, holds accountID on conn's client, live
// or in a session. When that cannot be read, the grant counts as shared.
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
	apps, err := e.store.ListApps(ctx)
	if err != nil {
		return false, err
	}
	// Apps are compared by client, since two apps may hold one client.
	client := make(map[string]bool)
	for _, a := range apps {
		if a.Provider == app.Provider && a.ClientID == app.ClientID {
			client[a.ID] = true
		}
	}
	conns, err := e.store.ListAll(ctx)
	if err != nil {
		return false, err
	}
	for _, other := range conns {
		if other.ID == conn.ID || !client[other.App] {
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

// revoke revokes token, which a write of conn dropped, when conn's
// provider can revoke it as which, and reports the outcome to OnRevoke.
func (e *Engine) revoke(ctx context.Context, conn *connection.Connection, token string, which RevokedToken) {
	if token == "" {
		return
	}
	r, ok := e.providers[conn.Provider].(Revoker)
	if !ok || (which != RevokedLive && r.RevokeScope() != RevokesToken) {
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
