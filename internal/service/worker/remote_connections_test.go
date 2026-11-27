// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const tokenMinTTL = 5 * time.Minute

// tokenCoordinator answers ResolveConnectionToken with respond, recording
// each request.
type tokenCoordinator struct {
	respond func(*coordinatorv1.ResolveConnectionTokenRequest) (*coordinatorv1.ResolveConnectionTokenResponse, error)

	mu       sync.Mutex
	requests []*coordinatorv1.ResolveConnectionTokenRequest
}

func (c *tokenCoordinator) ResolveConnectionToken(_ context.Context, _ serviceregistry.HostInfo, req *coordinatorv1.ResolveConnectionTokenRequest) (*coordinatorv1.ResolveConnectionTokenResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, proto.Clone(req).(*coordinatorv1.ResolveConnectionTokenRequest))
	c.mu.Unlock()
	return c.respond(req)
}

func (c *tokenCoordinator) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

// issuing answers each token request with the next of tok-1, tok-2, ...,
// valid for an hour.
func issuing() *tokenCoordinator {
	var issued int
	return &tokenCoordinator{respond: func(req *coordinatorv1.ResolveConnectionTokenRequest) (*coordinatorv1.ResolveConnectionTokenResponse, error) {
		if req.GetCheckOnly() {
			return &coordinatorv1.ResolveConnectionTokenResponse{}, nil
		}
		issued++
		return &coordinatorv1.ResolveConnectionTokenResponse{
			AccessToken:      fmt.Sprintf("tok-%d", issued),
			ExpiresInSeconds: int64(time.Hour / time.Second),
		}, nil
	}}
}

// failing answers every request with code.
func failing(code codes.Code, message string) *tokenCoordinator {
	return &tokenCoordinator{respond: func(*coordinatorv1.ResolveConnectionTokenRequest) (*coordinatorv1.ResolveConnectionTokenResponse, error) {
		return nil, status.Error(code, message)
	}}
}

// testClock is a clock that moves only when the test moves it.
type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time          { return c.now }
func (c *testClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestConnections(client coordinator.ConnectionTokenClient) (*remoteConnections, *testClock) {
	clock := &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newRemoteConnections(client, serviceregistry.HostInfo{}, coordinator.SecretReferenceRun{
		WorkerID: "worker-1", AttemptKey: "key-1", AttemptID: "attempt-1", DAGName: "child",
	})
	c.now = clock.Now
	return c, clock
}

var workConnection = &connection.Connection{Workspace: "ops", Name: "work"}

// A kept token serves requests until it falls below the asked lifetime, and
// each request names the run's attempt and DAG.
func TestRemoteConnectionsKeepToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	coord := issuing()
	conns, clock := newTestConnections(coord)

	token, expiresIn, err := conns.Token(ctx, workConnection, "", tokenMinTTL)
	require.NoError(t, err)
	assert.Equal(t, "tok-1", token)
	assert.Equal(t, time.Hour, expiresIn)

	clock.Advance(time.Hour - tokenMinTTL)
	token, expiresIn, err = conns.Token(ctx, workConnection, "", tokenMinTTL)
	require.NoError(t, err)
	assert.Equal(t, "tok-1", token)
	assert.Equal(t, tokenMinTTL, expiresIn)
	assert.Equal(t, 1, coord.calls())

	clock.Advance(time.Second)
	token, _, err = conns.Token(ctx, workConnection, "", tokenMinTTL)
	require.NoError(t, err)
	assert.Equal(t, "tok-2", token)

	// Another resource has a token of its own.
	token, _, err = conns.Token(ctx, workConnection, "https://other.example.com/mcp", tokenMinTTL)
	require.NoError(t, err)
	assert.Equal(t, "tok-3", token)

	want := &coordinatorv1.ResolveConnectionTokenRequest{
		WorkerId: "worker-1", AttemptKey: "key-1", AttemptId: "attempt-1", DagName: "child",
		Connection: "work", MinTtlSeconds: 300,
	}
	assert.True(t, proto.Equal(want, coord.requests[0]), coord.requests[0])
}

// A rejected token is reported and replaced. A token rejected after another
// caller replaced it is not reported: the newer kept token is returned
// without a request.
func TestRemoteConnectionsRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	coord := issuing()
	conns, _ := newTestConnections(coord)

	_, _, err := conns.Token(ctx, workConnection, "", tokenMinTTL)
	require.NoError(t, err)
	token, _, err := conns.Rejected(ctx, workConnection, "", "tok-1", tokenMinTTL)
	require.NoError(t, err)
	assert.Equal(t, "tok-2", token)
	assert.Equal(t, "tok-1", coord.requests[1].GetRejectedToken())

	token, _, err = conns.Token(ctx, workConnection, "", tokenMinTTL)
	require.NoError(t, err)
	assert.Equal(t, "tok-2", token)

	token, _, err = conns.Rejected(ctx, workConnection, "", "tok-1", tokenMinTTL)
	require.NoError(t, err)
	assert.Equal(t, "tok-2", token)
	assert.Equal(t, 2, coord.calls())
}

// The binding an answer carries is kept, so the connection is built without
// another request.
func TestRemoteConnectionsKeepBinding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	coord := &tokenCoordinator{respond: func(*coordinatorv1.ResolveConnectionTokenRequest) (*coordinatorv1.ResolveConnectionTokenResponse, error) {
		return &coordinatorv1.ResolveConnectionTokenResponse{Binding: &coordinatorv1.ConnectionBinding{
			Provider:   "google",
			Services:   []*coordinatorv1.ConnectionService{{Name: "gmail", Access: "read"}},
			BoundHosts: []string{"gmailmcp.googleapis.com"},
		}}, nil
	}}
	conns, _ := newTestConnections(coord)
	require.NoError(t, conns.CheckConnection(ctx, "ops", ir.ConnectionRef{Name: "work"}))

	conn, err := conns.Connection(ctx, "work")
	require.NoError(t, err)
	assert.Equal(t, &connection.Connection{
		Name:       "work",
		Provider:   connection.ProviderGoogle,
		Services:   []connection.Service{{Name: "gmail", Access: connection.AccessRead}},
		BoundHosts: []string{"gmailmcp.googleapis.com"},
	}, conn)
	assert.Equal(t, 1, coord.calls())
}

// The check runs on the coordinator, and its failure names the connection.
func TestRemoteConnectionsCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ref := ir.ConnectionRef{Name: "work", Servers: []string{"gmail"}}

	coord := issuing()
	conns, _ := newTestConnections(coord)
	require.NoError(t, conns.CheckConnection(ctx, "ops", ref))
	require.Equal(t, 1, coord.calls())
	assert.True(t, coord.requests[0].GetCheckOnly())
	assert.Equal(t, "work", coord.requests[0].GetConnection())

	conns, _ = newTestConnections(failing(codes.InvalidArgument, `connection "work": server "gmail" is not one of its services (drive)`))
	assert.EqualError(t, conns.CheckConnection(ctx, "ops", ref), `connection "work": server "gmail" is not one of its services (drive)`)

	conns, _ = newTestConnections(failing(codes.PermissionDenied, "connection access denied"))
	assert.EqualError(t, conns.CheckConnection(ctx, "ops", ref), `connection "work": connection access denied`)
}

// A failure keeps the kind the coordinator reported.
func TestRemoteConnectionsErrorKinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	conns, _ := newTestConnections(failing(codes.FailedPrecondition, `reconnect required: connection "work": the connection was never connected`))
	_, _, err := conns.Token(ctx, workConnection, "", tokenMinTTL)
	assert.ErrorIs(t, err, broker.ErrReconnectRequired)
	assert.EqualError(t, err, `reconnect required: connection "work": the connection was never connected`)

	conns, _ = newTestConnections(failing(codes.NotFound, `connection not found: "work" in workspace "ops"`))
	err = conns.CheckConnection(ctx, "ops", ir.ConnectionRef{Name: "work"})
	assert.ErrorIs(t, err, connection.ErrNotFound)
}

// Under a coordinator without the RPC, the check passes and a token request
// fails, saying the coordinator must be upgraded.
func TestRemoteConnectionsOldCoordinator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	conns, _ := newTestConnections(failing(codes.Unimplemented, "unknown method ResolveConnectionToken"))

	require.NoError(t, conns.CheckConnection(ctx, "ops", ir.ConnectionRef{Name: "work"}))
	_, _, err := conns.Token(ctx, workConnection, "", tokenMinTTL)
	assert.EqualError(t, err, `connection "work": the coordinator must be upgraded to hand out connection tokens to workers`)
}
