// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	runtestutil "github.com/dagucloud/dagu/v2/internal/testutil"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// connectionRun is an attempt that worker-1 runs of a DAG in workspace ops
// that declares the connection work, calling its service mail. Its local
// DAG calendar calls work's calendar, which is not one of its services, and
// unlabeled declares notify, the name of the notification connection.
type connectionRun struct {
	handler    *coordinator.Handler
	attemptKey string
	attemptID  string
	// refreshes counts the provider's refresh requests.
	refreshes *atomic.Int32
	conns     connection.Store
	tokens    broker.Source
}

func newConnectionRun(t *testing.T) connectionRun {
	t.Helper()
	ctx := context.Background()
	refreshes := &atomic.Int32{}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshes.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-2", "token_type": "Bearer", "expires_in": 3600})
	}))
	t.Cleanup(endpoint.Close)

	stores := file.NewServerStores(&config.Config{Paths: config.PathsConfig{DataDir: t.TempDir()}})
	conns, err := stores.ConnectionStore()
	require.NoError(t, err)
	app, err := conns.CreateApp(ctx, &connection.App{
		Name: "Example", Provider: connection.ProviderOAuth2, Kind: connection.AppKindBYO,
		ClientID: "client", ClientSecret: "secret", ClientType: connection.ClientTypeWeb,
		Endpoints: &connection.Endpoints{Token: endpoint.URL},
	})
	require.NoError(t, err)
	for _, conn := range []*connection.Connection{
		{
			Workspace: "ops", Name: "work", Provider: connection.ProviderOAuth2, App: app.ID,
			Grant:      connection.GrantAuthorizationCode,
			Services:   []connection.Service{{Name: "mail", Access: connection.AccessRead}},
			BoundHosts: []string{"api.example.com"},
		},
		{Name: "notify", Provider: connection.ProviderOAuth2, App: app.ID, Grant: connection.GrantAuthorizationCode},
	} {
		created, err := conns.Create(ctx, conn)
		require.NoError(t, err)
		_, err = conns.UpdateTokenState(ctx, created, func(st *connection.TokenState) error {
			st.Status = connection.TokenStatusConnected
			st.RefreshToken = "rt-1"
			st.AccessTokens = map[string]connection.AccessToken{
				"": {Token: "at-1", ExpiresAt: time.Now().Add(time.Hour), IssuedAt: time.Now()},
			}
			return nil
		})
		require.NoError(t, err)
	}
	tokens, err := broker.New(broker.Config{Store: conns, Locks: stores.ConnectionLocks()})
	require.NoError(t, err)

	dag := &ir.DAG{
		Name:        "mail-dag",
		Labels:      ir.NewLabels([]string{"workspace=ops"}),
		Connections: []ir.ConnectionRef{{Name: "work", Servers: []string{"mail"}}},
		LocalDAGs: map[string]*ir.DAG{
			"calendar": {
				Name:        "calendar",
				Labels:      ir.NewLabels([]string{"workspace=ops"}),
				Connections: []ir.ConnectionRef{{Name: "work", Servers: []string{"calendar"}}},
			},
			"unlabeled": {Name: "unlabeled", Connections: []ir.ConnectionRef{{Name: "notify"}}},
		},
	}
	now := time.Now().UTC()
	repo := runtestutil.NewFileDAGRunRepository(filepath.Join(t.TempDir(), "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	attempt, err := repo.CreateAttempt(ctx, dag, now, "run-1", persis.DAGRunCreateAttemptOptions{AttemptID: "attempt-1"})
	require.NoError(t, err)
	attemptKey := ir.GenerateAttemptKey(dag.Name, "run-1", dag.Name, "run-1", attempt.ID())
	require.NoError(t, attempt.Open(ctx))
	t.Cleanup(func() { require.NoError(t, attempt.Close(context.Background())) })
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
		Name: dag.Name, DAGRunID: "run-1", AttemptID: attempt.ID(), AttemptKey: attemptKey, Status: ir.Running,
	}))
	leases := store.NewDAGRunLeaseStore(testutil.NewMemoryBackend().Collection(persis.CollectionDAGRunLeases))
	require.NoError(t, leases.Upsert(ctx, dispatch.DAGRunLease{
		AttemptKey: attemptKey, AttemptID: attempt.ID(), WorkerID: "worker-1",
		DAGRun:    ir.DAGRunRef{Name: dag.Name, ID: "run-1"},
		Root:      ir.DAGRunRef{Name: dag.Name, ID: "run-1"},
		ClaimedAt: now.UnixMilli(), LastHeartbeatAt: now.UnixMilli(),
	}))
	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		ConnectionStore:     conns,
		ConnectionTokens:    tokens,
		DAGRunRepository:    repo,
		DAGRunLeaseStore:    leases,
		StaleLeaseThreshold: time.Minute,
	})
	return connectionRun{handler: handler, attemptKey: attemptKey, attemptID: attempt.ID(), refreshes: refreshes, conns: conns, tokens: tokens}
}

// resolve sends req as worker-1's request for the run.
func (r connectionRun) resolve(req *coordinatorv1.ResolveConnectionTokenRequest) (*coordinatorv1.ResolveConnectionTokenResponse, error) {
	req.WorkerId, req.AttemptKey, req.AttemptId = "worker-1", r.attemptKey, r.attemptID
	return r.handler.ResolveConnectionToken(context.Background(), req)
}

// A declared connection answers its stored token with the token's lifetime
// and the binding; a check-only request answers the binding alone.
func TestResolveConnectionToken(t *testing.T) {
	t.Parallel()
	run := newConnectionRun(t)
	wantBinding := &coordinatorv1.ConnectionBinding{
		Provider:   "oauth2",
		Services:   []*coordinatorv1.ConnectionService{{Name: "mail", Access: "read"}},
		BoundHosts: []string{"api.example.com"},
	}

	resp, err := run.resolve(&coordinatorv1.ResolveConnectionTokenRequest{Connection: "work", MinTtlSeconds: 300})
	require.NoError(t, err)
	assert.Equal(t, "at-1", resp.GetAccessToken())
	assert.InDelta(t, 3600, resp.GetExpiresInSeconds(), 60)
	assert.True(t, proto.Equal(wantBinding, resp.GetBinding()), resp.GetBinding())

	resp, err = run.resolve(&coordinatorv1.ResolveConnectionTokenRequest{Connection: "work", CheckOnly: true})
	require.NoError(t, err)
	assert.Empty(t, resp.GetAccessToken())
	assert.True(t, proto.Equal(wantBinding, resp.GetBinding()), resp.GetBinding())
	assert.Zero(t, run.refreshes.Load())
}

// A reported token refreshes only while it is the stored one.
func TestResolveConnectionTokenRejected(t *testing.T) {
	t.Parallel()
	run := newConnectionRun(t)

	resp, err := run.resolve(&coordinatorv1.ResolveConnectionTokenRequest{Connection: "work", RejectedToken: "at-0"})
	require.NoError(t, err)
	assert.Equal(t, "at-1", resp.GetAccessToken(), "an older token takes the stored one")
	assert.Zero(t, run.refreshes.Load())

	resp, err = run.resolve(&coordinatorv1.ResolveConnectionTokenRequest{Connection: "work", RejectedToken: "at-1"})
	require.NoError(t, err)
	assert.Equal(t, "at-2", resp.GetAccessToken())
	assert.Equal(t, int32(1), run.refreshes.Load())
}

// A check-only request runs the admission checks on the servers the DAG
// snapshot names beside the connection.
func TestResolveConnectionTokenCheckFails(t *testing.T) {
	t.Parallel()
	run := newConnectionRun(t)

	_, err := run.resolve(&coordinatorv1.ResolveConnectionTokenRequest{Connection: "work", DagName: "calendar", CheckOnly: true})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.ErrorContains(t, err, `server "calendar" is not one of its services`)
}

func TestResolveConnectionTokenDenied(t *testing.T) {
	t.Parallel()
	run := newConnectionRun(t)

	_, err := run.resolve(&coordinatorv1.ResolveConnectionTokenRequest{Connection: "other"})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "a connection the DAG does not declare")

	_, err = run.handler.ResolveConnectionToken(context.Background(), &coordinatorv1.ResolveConnectionTokenRequest{
		WorkerId: "worker-2", AttemptKey: run.attemptKey, AttemptId: run.attemptID, Connection: "work",
	})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "another worker")

	// A DAG without a workspace label resolves in .global, never to the
	// notification connection.
	_, err = run.resolve(&coordinatorv1.ResolveConnectionTokenRequest{Connection: "notify", DagName: "unlabeled"})
	assert.Equal(t, codes.NotFound, status.Code(err))

	// A child the run does not name as written, such as one named by a
	// reference, is denied with the reason.
	_, err = run.resolve(&coordinatorv1.ResolveConnectionTokenRequest{Connection: "work", DagName: "mail-ops"})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.ErrorContains(t, err, "a child named by a reference cannot use connections on the worker that runs its parent")

	_, err = coordinator.NewHandler(coordinator.HandlerConfig{}).ResolveConnectionToken(context.Background(),
		&coordinatorv1.ResolveConnectionTokenRequest{Connection: "work"})
	assert.Equal(t, codes.Unavailable, status.Code(err), "no connection store")

	// FailedPrecondition is kept for a connection that needs reconnecting.
	_, err = coordinator.NewHandler(coordinator.HandlerConfig{ConnectionStore: run.conns, ConnectionTokens: run.tokens}).ResolveConnectionToken(context.Background(),
		&coordinatorv1.ResolveConnectionTokenRequest{WorkerId: "worker-1", AttemptKey: run.attemptKey, AttemptId: run.attemptID, Connection: "work"})
	assert.Equal(t, codes.Internal, status.Code(err), "no lease store")
}

// The client keeps the coordinator's status, so a worker tells a missing
// connection from other failures.
func TestClientResolveConnectionToken(t *testing.T) {
	t.Parallel()
	run := newConnectionRun(t)
	server, addr := startMockServer(t, run.handler)
	t.Cleanup(server.Stop)
	host, port := parseHostPort(addr)
	client := coordinator.New(&mockServiceMonitor{members: []serviceregistry.HostInfo{{
		ID: "coord-1", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive,
	}}}, coordinator.DefaultConfig())
	tokens, ok := client.(coordinator.ConnectionTokenClient)
	require.True(t, ok)
	request := func(name, dagName string) *coordinatorv1.ResolveConnectionTokenRequest {
		return &coordinatorv1.ResolveConnectionTokenRequest{
			WorkerId: "worker-1", AttemptKey: run.attemptKey, AttemptId: run.attemptID,
			Connection: name, DagName: dagName,
		}
	}

	resp, err := tokens.ResolveConnectionToken(context.Background(), serviceregistry.HostInfo{}, request("work", ""))
	require.NoError(t, err)
	assert.Equal(t, "at-1", resp.GetAccessToken())

	_, err = tokens.ResolveConnectionToken(context.Background(), serviceregistry.HostInfo{}, request("notify", "unlabeled"))
	assert.Equal(t, codes.NotFound, status.Code(err))
}
