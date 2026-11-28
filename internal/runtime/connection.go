// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// connectionTokenLabel keys a step's connection token among the values its
// output masks. No variable name holds a space, so it never shadows one.
const connectionTokenLabel = "connection token"

// stepConnection returns the name of the connection whose token step
// sends, or "".
func stepConnection(step ir.Step) string {
	if name := registry.ExecutorCapabilitiesFor(step.ExecutorConfig.Type).Connection; name != nil {
		return name(step)
	}
	return ""
}

// withConnectionToken gets the token of the connection that step uses
// before the step's output is set up, so that its logs and outputs mask the
// token from the start (Spec 107 §Masking). The run's connections keep the
// token, and the step's executor takes the same one. A failure is left to
// the executor, which reports it in the step's terms.
func withConnectionToken(ctx context.Context, step ir.Step) context.Context {
	name := stepConnection(step)
	rCtx, ok := LookupDAGContext(ctx)
	if name == "" || !ok || rCtx.Connections == nil {
		return ctx
	}
	conn, err := rCtx.Connections.Connection(ctx, name)
	if err != nil {
		return ctx
	}
	// A step's token is for the connection's only resource.
	token, _, err := rCtx.Connections.Token(ctx, conn, "", broker.StepMinTTL)
	if err != nil {
		return ctx
	}
	rCtx.EnvScope = rCtx.EnvScope.WithMaskedSecrets(map[string]string{connectionTokenLabel: token})
	return WithDAGContext(ctx, rCtx)
}

// watchConnection returns ctx, whose run connections record how one attempt
// of step uses its connection, and a func that returns the record (Spec 107
// §Run Records). For a step that uses no connection, the func returns nil.
func watchConnection(ctx context.Context, step ir.Step) (context.Context, func() *ir.NodeConnection) {
	name := stepConnection(step)
	if name == "" {
		return ctx, func() *ir.NodeConnection { return nil }
	}
	use := &connectionUse{record: ir.NodeConnection{Name: name}}
	if rCtx, ok := LookupDAGContext(ctx); ok && rCtx.Connections != nil {
		use.Connections = rCtx.Connections
		rCtx.Connections = use
		ctx = WithDAGContext(ctx, rCtx)
	}
	return ctx, use.snapshot
}

// connectionUse is the run's connections as one step attempt uses them. It
// records the account of the step's connection, and whether the latest
// token request failed because the connection could hand out no token.
type connectionUse struct {
	Connections

	mu          sync.Mutex
	record      ir.NodeConnection
	accountRead bool
}

func (u *connectionUse) Token(ctx context.Context, conn *connection.Connection, resource string, minTTL time.Duration) (string, time.Duration, error) {
	token, expiresIn, err := u.Connections.Token(ctx, conn, resource, minTTL)
	u.observe(ctx, conn, err)
	return token, expiresIn, err
}

func (u *connectionUse) Rejected(ctx context.Context, conn *connection.Connection, resource, token string, minTTL time.Duration) (string, time.Duration, error) {
	token, expiresIn, err := u.Connections.Rejected(ctx, conn, resource, token, minTTL)
	u.observe(ctx, conn, err)
	return token, expiresIn, err
}

// observe records the outcome of a token request for the step's
// connection. The account is read once, also after a failed request, so
// that the run shows which account to reconnect.
func (u *connectionUse) observe(ctx context.Context, conn *connection.Connection, err error) {
	if conn == nil || conn.Name != u.record.Name {
		return
	}
	var failure ir.ConnectionFailure
	switch {
	case errors.Is(err, broker.ErrNeedsAttention):
		failure = ir.ConnectionNeedsAttention
	case errors.Is(err, broker.ErrReconnectRequired):
		failure = ir.ConnectionReconnectRequired
	}
	u.mu.Lock()
	u.record.Failure = failure
	readAccount := !u.accountRead
	u.accountRead = true
	u.mu.Unlock()
	if !readAccount {
		return
	}
	account, err := u.Account(ctx, conn)
	if err != nil {
		logger.Debug(ctx, "Failed to read the account of a connection", tag.Name(conn.Name), tag.Error(err))
		return
	}
	u.mu.Lock()
	u.record.Account = account
	u.mu.Unlock()
}

func (u *connectionUse) snapshot() *ir.NodeConnection {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.record.Clone()
}
