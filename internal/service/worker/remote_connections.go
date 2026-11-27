// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/connection/oauthrefresh"
	"github.com/dagucloud/dagu/v2/internal/ir"
	rtagent "github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// maxCachedConnectionTokens bounds the tokens one run keeps. A run asks
// only for the connections its DAG declares, so the bound is reached only
// by steps that name many resources; their tokens beyond it are not kept.
const maxCachedConnectionTokens = 64

// errCoordinatorUpgrade fails a token request to a coordinator that cannot
// answer it.
var errCoordinatorUpgrade = errors.New("the coordinator must be upgraded to hand out connection tokens to workers")

// remoteConnections checks and hands out the connections of one run on a
// worker through the coordinator (Spec 107 §Workers). It keeps each token
// it receives, as a run process does, until the token falls below the
// lifetime a caller asks for or is rejected, and then asks again. It never
// refreshes a token itself. It keeps the latest binding of each connection
// that an answer carried.
type remoteConnections struct {
	client coordinator.ConnectionTokenClient
	owner  serviceregistry.HostInfo
	run    coordinator.SecretReferenceRun
	now    func() time.Time

	mu    sync.Mutex
	gates map[tokenKey]*oauthrefresh.Gate
	// bindings holds a connection per name; the coordinator answers only
	// the connections the run's DAG declares.
	bindings map[string]*connection.Connection
}

// tokenKey names the token of one connection at one resource.
type tokenKey struct {
	connection string
	resource   string
}

var (
	_ broker.Source             = (*remoteConnections)(nil)
	_ rtagent.ConnectionChecker = (*remoteConnections)(nil)
)

func newRemoteConnections(client coordinator.ConnectionTokenClient, owner serviceregistry.HostInfo, run coordinator.SecretReferenceRun) *remoteConnections {
	return &remoteConnections{
		client:   client,
		owner:    owner,
		run:      run,
		now:      time.Now,
		gates:    make(map[tokenKey]*oauthrefresh.Gate),
		bindings: make(map[string]*connection.Connection),
	}
}

// Connection returns the binding of the connection named name, as the
// connection that Token takes: its name, provider, services, every host
// that may receive its token, and its resource. A binding an earlier answer
// carried is reused; otherwise a check-only request asks for it.
func (c *remoteConnections) Connection(ctx context.Context, name string) (*connection.Connection, error) {
	c.mu.Lock()
	conn, ok := c.bindings[name]
	c.mu.Unlock()
	if ok {
		return conn, nil
	}
	if _, err := c.call(ctx, &coordinatorv1.ResolveConnectionTokenRequest{Connection: name, CheckOnly: true}); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bindings[name], nil
}

// CheckConnection asks the coordinator to check ref. The coordinator reads
// the workspace from the run's DAG snapshot, which the workspace given here
// also comes from. Under a coordinator that cannot check connections, the
// check is skipped, and a step that uses ref fails when it asks for a token.
func (c *remoteConnections) CheckConnection(ctx context.Context, _ string, ref ir.ConnectionRef) error {
	_, err := c.call(ctx, &coordinatorv1.ResolveConnectionTokenRequest{Connection: ref.Name, CheckOnly: true})
	if errors.Is(err, errCoordinatorUpgrade) {
		logger.Warn(ctx, "Connection is not checked before the run starts", tag.Name(ref.Name), tag.Error(err))
		return nil
	}
	return err
}

func (c *remoteConnections) Token(ctx context.Context, conn *connection.Connection, resource string, minTTL time.Duration) (string, time.Duration, error) {
	return c.token(ctx, conn, resource, minTTL, "")
}

func (c *remoteConnections) Rejected(ctx context.Context, conn *connection.Connection, resource, token string, minTTL time.Duration) (string, time.Duration, error) {
	return c.token(ctx, conn, resource, minTTL, token)
}

// token returns the kept token of conn at resource while it is not rejected
// and keeps minTTL, or asks the coordinator for one. Callers that miss
// together cause one request.
func (c *remoteConnections) token(ctx context.Context, conn *connection.Connection, resource string, minTTL time.Duration, rejected string) (string, time.Duration, error) {
	if conn == nil {
		return "", 0, connection.ErrNotFound
	}
	gate := c.gate(tokenKey{connection: conn.Name, resource: resource})
	if rejected != "" {
		gate.Forget(rejected)
	}
	usable := func(t *oauth2.Token) bool {
		return t != nil && t.AccessToken != rejected && (t.Expiry.IsZero() || t.Expiry.Sub(c.now()) >= minTTL)
	}
	token, err := gate.TokenIf(ctx, usable, func(ctx context.Context, _ *oauth2.Token) (*oauth2.Token, error) {
		resp, err := c.call(ctx, &coordinatorv1.ResolveConnectionTokenRequest{
			Connection:    conn.Name,
			Resource:      resource,
			MinTtlSeconds: int64((minTTL + time.Second - 1) / time.Second),
			RejectedToken: rejected,
		})
		if err != nil {
			return nil, err
		}
		received := &oauth2.Token{AccessToken: resp.GetAccessToken()}
		if seconds := resp.GetExpiresInSeconds(); seconds > 0 {
			received.Expiry = c.now().Add(time.Duration(seconds) * time.Second)
		}
		return received, nil
	})
	if err != nil {
		return "", 0, err
	}
	var expiresIn time.Duration
	if !token.Expiry.IsZero() {
		expiresIn = token.Expiry.Sub(c.now())
	}
	return token.AccessToken, expiresIn, nil
}

// gate returns the gate of key. Beyond maxCachedConnectionTokens a new gate
// is not kept, so its token is asked for on each call.
func (c *remoteConnections) gate(key tokenKey) *oauthrefresh.Gate {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gate, ok := c.gates[key]; ok {
		return gate
	}
	gate := oauthrefresh.NewGate()
	if len(c.gates) < maxCachedConnectionTokens {
		c.gates[key] = gate
	}
	return gate
}

func (c *remoteConnections) call(ctx context.Context, req *coordinatorv1.ResolveConnectionTokenRequest) (*coordinatorv1.ResolveConnectionTokenResponse, error) {
	req.WorkerId, req.AttemptKey, req.AttemptId, req.DagName = c.run.WorkerID, c.run.AttemptKey, c.run.AttemptID, c.run.DAGName
	resp, err := c.client.ResolveConnectionToken(ctx, c.owner, req)
	if err != nil {
		return nil, connectionTokenError(req.GetConnection(), err)
	}
	if binding := resp.GetBinding(); binding != nil {
		c.mu.Lock()
		c.bindings[req.GetConnection()] = bindingConnection(req.GetConnection(), binding)
		c.mu.Unlock()
	}
	return resp, nil
}

// bindingConnection returns the connection named name that binding
// describes. Its workspace is left empty: the coordinator resolves the name
// in the run's workspace.
func bindingConnection(name string, binding *coordinatorv1.ConnectionBinding) *connection.Connection {
	conn := &connection.Connection{
		Name:       name,
		Provider:   connection.Provider(binding.GetProvider()),
		BoundHosts: binding.GetBoundHosts(),
		Resource:   binding.GetResource(),
	}
	for _, svc := range binding.GetServices() {
		conn.Services = append(conn.Services, connection.Service{Name: svc.GetName(), Access: connection.Access(svc.GetAccess())})
	}
	return conn
}

// connectionTokenError restores the kind of a coordinator's error, so a
// caller tells a missing connection or one that needs reconnecting from
// other failures. The coordinator answers FailedPrecondition only for a
// connection that needs reconnecting.
func connectionTokenError(name string, err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("connection %q: %w", name, err)
	}
	switch st.Code() { //nolint:exhaustive // Only codes that carry a connection error kind are translated.
	case codes.Unimplemented:
		return fmt.Errorf("connection %q: %w", name, errCoordinatorUpgrade)
	case codes.NotFound:
		return &remoteConnectionError{message: namingConnection(name, st.Message()), kind: connection.ErrNotFound}
	case codes.FailedPrecondition:
		return &remoteConnectionError{message: namingConnection(name, st.Message()), kind: broker.ErrReconnectRequired}
	default:
		return errors.New(namingConnection(name, st.Message()))
	}
}

// namingConnection returns message, prefixed with the connection's name
// unless it names the connection already.
func namingConnection(name, message string) string {
	if strings.Contains(message, strconv.Quote(name)) {
		return message
	}
	return fmt.Sprintf("connection %q: %s", name, message)
}

// remoteConnectionError is a coordinator's error message with the kind it
// reported.
type remoteConnectionError struct {
	message string
	kind    error
}

func (e *remoteConnectionError) Error() string { return e.message }

func (e *remoteConnectionError) Unwrap() error { return e.kind }
