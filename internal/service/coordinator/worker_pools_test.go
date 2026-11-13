// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

var poolsTestPeer = config.Peer{CertFile: "server.crt", KeyFile: "server.key", ClientCaFile: "ca.crt"}

var poolsTestDispatchers = []string{"server"}

// poolsTestDefs are the pools of the example of Spec 092.
func poolsTestDefs() []config.WorkerPool {
	return []config.WorkerPool{
		{Name: "agent-claude", Identities: []string{"agent-claude"}, Labels: map[string]string{"agent.provider.claude": "true"}},
		{Name: "general", Identities: []string{"general-worker"}, Labels: map[string]string{"zone": "*", "tier": "gold"}},
	}
}

func newPoolsForTest(t *testing.T) *WorkerPools {
	t.Helper()
	pools, err := NewWorkerPools(poolsTestDefs(), poolsTestDispatchers, poolsTestPeer)
	require.NoError(t, err)
	require.True(t, pools.Enabled())
	return pools
}

// identityContext returns the context of a call whose verified leaf client
// certificate has the Common Name cn.
func identityContext(cn string) context.Context {
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: cn}}
	return peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}},
	}})
}

func requirePermissionDenied(t *testing.T, err error, message string) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err), err.Error())
	assert.Contains(t, status.Convert(err).Message(), message)
}

// Pools need mutual TLS, a CLI provider in the label of an agent pool, and valid
// labels. The coordinator stops at start otherwise.
func TestNewWorkerPoolsChecks(t *testing.T) {
	t.Parallel()

	none, err := NewWorkerPools(nil, nil, config.Peer{})
	require.NoError(t, err)
	assert.False(t, none.Enabled())

	for _, key := range []string{"peer.cert_file", "peer.key_file", "peer.client_ca_file"} {
		peerCfg := poolsTestPeer
		switch key {
		case "peer.cert_file":
			peerCfg.CertFile = ""
		case "peer.key_file":
			peerCfg.KeyFile = ""
		default:
			peerCfg.ClientCaFile = ""
		}
		_, err := NewWorkerPools(poolsTestDefs(), poolsTestDispatchers, peerCfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "coordinator.worker_pools")
		assert.Contains(t, err.Error(), `"`+key+`" is not set`)
	}

	_, err = NewWorkerPools([]config.WorkerPool{{Name: "a", Identities: []string{"x"}, Labels: map[string]string{"agent.provider.gemini": "true"}}}, poolsTestDispatchers, poolsTestPeer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"gemini" is not a CLI provider`)

	_, err = NewWorkerPools([]config.WorkerPool{{Name: "a", Identities: []string{"x"}, Labels: map[string]string{"bad key": "x"}}}, poolsTestDispatchers, poolsTestPeer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "coordinator.worker_pools[0].labels.bad key")

	// The dispatchers are required with pools, invalid without them, and in no pool.
	_, err = NewWorkerPools(poolsTestDefs(), nil, poolsTestPeer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"coordinator.dispatchers" is not set`)
	_, err = NewWorkerPools(nil, poolsTestDispatchers, poolsTestPeer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"coordinator.dispatchers" needs coordinator.worker_pools`)
	_, err = NewWorkerPools(poolsTestDefs(), []string{"general-worker"}, poolsTestPeer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `identity "general-worker" is a dispatcher and a member of the pool "general"`)
}

// The identity is the Common Name of the verified leaf certificate. A call
// without a verified chain has no identity.
func TestCertificateIdentity(t *testing.T) {
	t.Parallel()

	cn, err := certificateIdentity(identityContext("agent-claude"))
	require.NoError(t, err)
	assert.Equal(t, "agent-claude", cn)

	_, err = certificateIdentity(context.Background())
	require.ErrorIs(t, err, errNoVerifiedCertificate)

	_, err = certificateIdentity(peer.NewContext(context.Background(), &peer.Peer{}))
	require.ErrorIs(t, err, errNoVerifiedCertificate, "a call without TLS has no identity")

	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: "agent-claude"}}
	unverified := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}},
	}})
	_, err = certificateIdentity(unverified)
	require.ErrorIs(t, err, errNoVerifiedCertificate, "a certificate that TLS did not verify gives no identity")

	_, err = certificateIdentity(identityContext(""))
	require.Error(t, err)
}

type poolsTestStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s poolsTestStream) Context() context.Context { return s.ctx }

// With pools, a call of a worker from an identity in no pool fails with
// PermissionDenied. Other calls need only the valid certificate.
func TestWorkerPoolsInterceptors(t *testing.T) {
	t.Parallel()

	pools := newPoolsForTest(t)
	unary := pools.UnaryServerInterceptor()
	called := false
	handler := func(context.Context, any) (any, error) { called = true; return "ok", nil }

	_, err := unary(identityContext("rogue"), nil, &grpc.UnaryServerInfo{FullMethod: coordinatorv1.CoordinatorService_Poll_FullMethodName}, handler)
	requirePermissionDenied(t, err, `certificate identity "rogue" is in no worker pool`)
	assert.False(t, called)

	dispatchInfo := &grpc.UnaryServerInfo{FullMethod: coordinatorv1.CoordinatorService_Dispatch_FullMethodName}
	rootTask := &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{DagRunId: "run-1"}}
	_, err = unary(identityContext("rogue"), rootTask, dispatchInfo, handler)
	requirePermissionDenied(t, err, `certificate identity "rogue" is not a dispatcher and is in no worker pool`)
	_, err = unary(identityContext("general-worker"), rootTask, dispatchInfo, handler)
	requirePermissionDenied(t, err, "a worker cannot dispatch a root run")
	assert.False(t, called)
	_, err = unary(identityContext("server"), rootTask, dispatchInfo, handler)
	require.NoError(t, err, "a dispatcher dispatches a root run")
	assert.True(t, called)

	called = false
	_, err = unary(identityContext("agent-claude"), nil, &grpc.UnaryServerInfo{FullMethod: coordinatorv1.CoordinatorService_ReportStatus_FullMethodName}, handler)
	require.NoError(t, err)
	assert.True(t, called)

	_, err = unary(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: coordinatorv1.CoordinatorService_Heartbeat_FullMethodName}, handler)
	requirePermissionDenied(t, err, "no verified client certificate")

	stream := pools.StreamServerInterceptor()
	streamCalled := false
	streamHandler := func(any, grpc.ServerStream) error { streamCalled = true; return nil }
	err = stream(nil, poolsTestStream{ctx: identityContext("rogue")}, &grpc.StreamServerInfo{FullMethod: coordinatorv1.CoordinatorService_StreamLogs_FullMethodName}, streamHandler)
	requirePermissionDenied(t, err, `certificate identity "rogue" is in no worker pool`)
	assert.False(t, streamCalled)
	putInfo := &grpc.StreamServerInfo{FullMethod: coordinatorv1.CoordinatorService_PutWorkspaceBundle_FullMethodName}
	err = stream(nil, poolsTestStream{ctx: identityContext("rogue")}, putInfo, streamHandler)
	requirePermissionDenied(t, err, `certificate identity "rogue" is not a dispatcher`)
	err = stream(nil, poolsTestStream{ctx: identityContext("general-worker")}, putInfo, streamHandler)
	requirePermissionDenied(t, err, `certificate identity "general-worker" is not a dispatcher`)
	assert.False(t, streamCalled)
	err = stream(nil, poolsTestStream{ctx: identityContext("server")}, putInfo, streamHandler)
	require.NoError(t, err)
	assert.True(t, streamCalled)

	// The health service needs no dispatcher.
	called = false
	_, err = unary(identityContext("rogue"), nil, &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}, handler)
	require.NoError(t, err)
	assert.True(t, called)

	for _, method := range []string{
		coordinatorv1.CoordinatorService_Poll_FullMethodName,
		coordinatorv1.CoordinatorService_Heartbeat_FullMethodName,
		coordinatorv1.CoordinatorService_AckTaskClaim_FullMethodName,
		coordinatorv1.CoordinatorService_RunHeartbeat_FullMethodName,
		coordinatorv1.CoordinatorService_ReportStatus_FullMethodName,
		coordinatorv1.CoordinatorService_StreamLogs_FullMethodName,
		coordinatorv1.CoordinatorService_StreamArtifacts_FullMethodName,
		coordinatorv1.CoordinatorService_ResolveSecretReference_FullMethodName,
		coordinatorv1.CoordinatorService_ResolveRuntimeProfile_FullMethodName,
		coordinatorv1.CoordinatorService_ClaimAgentSessionCleanup_FullMethodName,
		coordinatorv1.CoordinatorService_CompleteAgentSessionCleanup_FullMethodName,
	} {
		assert.True(t, IsWorkerMethod(method), method)
	}
	for _, method := range []string{
		coordinatorv1.CoordinatorService_Dispatch_FullMethodName,
		coordinatorv1.CoordinatorService_GetDAGRunStatus_FullMethodName,
		coordinatorv1.CoordinatorService_GetDAG_FullMethodName,
		coordinatorv1.CoordinatorService_RequestCancel_FullMethodName,
		coordinatorv1.CoordinatorService_GetWorkers_FullMethodName,
		coordinatorv1.CoordinatorService_GetState_FullMethodName,
		coordinatorv1.CoordinatorService_GetWorkspaceBundle_FullMethodName,
	} {
		assert.False(t, IsWorkerMethod(method), method)
	}

	none, err := NewWorkerPools(nil, nil, config.Peer{})
	require.NoError(t, err)
	assert.Nil(t, none.ServerOptions())
	called = false
	_, err = none.UnaryServerInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: coordinatorv1.CoordinatorService_Poll_FullMethodName}, handler)
	require.NoError(t, err)
	assert.True(t, called)
}

// Without pools, a label that starts with agent. is refused. With pools, each
// label must be allowed by the pool, and a worker of an agent pool must announce
// the label of its provider.
func TestAuthorizeLabels(t *testing.T) {
	t.Parallel()

	var none *WorkerPools
	requirePermissionDenied(t, none.authorizeLabels(nil, map[string]string{"os": "linux", "agent.provider.claude": "true"}),
		`label "agent.provider.claude" needs coordinator.worker_pools`)
	require.NoError(t, none.authorizeLabels(nil, map[string]string{"os": "linux", "zone": "eu"}))

	pools := newPoolsForTest(t)
	agentPool := pools.byIdentity["agent-claude"]
	generalPool := pools.byIdentity["general-worker"]

	require.NoError(t, pools.authorizeLabels(agentPool, map[string]string{"agent.provider.claude": "true", "os": "linux", "arch": "amd64"}))
	require.NoError(t, pools.authorizeLabels(generalPool, map[string]string{"zone": "eu", "tier": "gold"}))
	require.NoError(t, pools.authorizeLabels(generalPool, nil))

	requirePermissionDenied(t, pools.authorizeLabels(generalPool, map[string]string{"agent.provider.claude": "true"}),
		`label "agent.provider.claude" with the value "true" is not allowed by the worker pool "general"`)
	requirePermissionDenied(t, pools.authorizeLabels(generalPool, map[string]string{"tier": "silver"}),
		`label "tier" with the value "silver" is not allowed by the worker pool "general"`)
	requirePermissionDenied(t, pools.authorizeLabels(agentPool, map[string]string{"agent.provider.claude": "true", "zone": "eu"}),
		`label "zone"`)
	requirePermissionDenied(t, pools.authorizeLabels(agentPool, map[string]string{"os": "linux"}),
		`the worker pool "agent-claude" is an agent pool: the worker must announce the label "agent.provider.claude"`)
	requirePermissionDenied(t, pools.authorizeLabels(nil, map[string]string{}), "no verified client certificate")
}

func newPoolsTestHandler(t *testing.T, pools *WorkerPools) (*Handler, *dispatchStores) {
	t.Helper()
	baseDir := filepath.Join(t.TempDir(), "distributed")
	stores := &dispatchStores{
		dispatch:  newTestDispatchTaskStore(baseDir),
		lease:     newTestDAGRunLeaseStore(baseDir),
		heartbeat: newTestWorkerHeartbeatStore(baseDir),
	}
	h := NewHandler(HandlerConfig{
		DispatchTaskStore:         stores.dispatch,
		DAGRunLeaseStore:          stores.lease,
		WorkerHeartbeatStore:      stores.heartbeat,
		ActiveDistributedRunStore: newTestActiveDistributedRunStore(baseDir),
		WorkerPools:               pools,
		Owner:                     dispatch.CoordinatorEndpoint{ID: "coord", Host: "127.0.0.1", Port: 50055},
	})
	h.dispatchPollInitialWait = 10 * time.Millisecond
	h.dispatchPollMaxWait = 20 * time.Millisecond
	return h, stores
}

type dispatchStores struct {
	dispatch  dispatch.DispatchTaskStore
	lease     dispatch.DAGRunLeaseStore
	heartbeat dispatch.WorkerHeartbeatStore
}

func enqueueAgentTask(t *testing.T, store dispatch.DispatchTaskStore, id string, selector map[string]string) {
	t.Helper()
	require.NoError(t, store.Enqueue(context.Background(), &dispatch.DispatchTask{
		DAGRunID: id, Target: "dag-" + id, AttemptID: "attempt-" + id, AttemptKey: "key-" + id,
		RootDAGRunName: "dag-" + id, RootDAGRunID: id, WorkerSelector: selector,
	}))
}

// A poll or a heartbeat with a label that the pool does not allow fails, and the
// coordinator keeps nothing of it: no claim and no heartbeat.
func TestPollAndHeartbeatRefuseLabels(t *testing.T) {
	t.Parallel()

	h, stores := newPoolsTestHandler(t, newPoolsForTest(t))
	enqueueAgentTask(t, stores.dispatch, "a1", map[string]string{"agent.provider.claude": "true"})

	ctx, cancel := context.WithTimeout(identityContext("general-worker"), time.Second)
	defer cancel()
	_, err := h.Poll(ctx, &coordinatorv1.PollRequest{WorkerId: "w", PollerId: "p", Labels: map[string]string{"agent.provider.claude": "true"}})
	requirePermissionDenied(t, err, `label "agent.provider.claude" with the value "true" is not allowed by the worker pool "general"`)

	_, err = h.Heartbeat(identityContext("general-worker"), &coordinatorv1.HeartbeatRequest{WorkerId: "w", Labels: map[string]string{"agent.provider.claude": "true"}})
	requirePermissionDenied(t, err, "not allowed by the worker pool")
	_, err = h.Heartbeat(identityContext("rogue"), &coordinatorv1.HeartbeatRequest{WorkerId: "w"})
	requirePermissionDenied(t, err, `certificate identity "rogue" is in no worker pool`)
	records, err := stores.heartbeat.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, records, "a refused heartbeat keeps no label")

	// The task is still pending: the agent worker of the provider claims it.
	ctx2, cancel2 := context.WithTimeout(identityContext("agent-claude"), time.Second)
	defer cancel2()
	resp, err := h.Poll(ctx2, &coordinatorv1.PollRequest{WorkerId: "agent-1", PollerId: "p", Labels: map[string]string{"agent.provider.claude": "true"}})
	require.NoError(t, err)
	require.NotNil(t, resp.Task)
	assert.Equal(t, "a1", resp.Task.DagRunId)
}

// Without pools, a worker that announces a label agent. is refused, so no worker
// serves an agent run.
func TestPollWithoutPoolsRefusesAgentLabels(t *testing.T) {
	t.Parallel()

	h, _ := newPoolsTestHandler(t, nil)
	_, err := h.Poll(context.Background(), &coordinatorv1.PollRequest{WorkerId: "w", PollerId: "p", Labels: map[string]string{"agent.provider.claude": "true"}})
	requirePermissionDenied(t, err, `label "agent.provider.claude" needs coordinator.worker_pools`)
	_, err = h.Heartbeat(context.Background(), &coordinatorv1.HeartbeatRequest{WorkerId: "w", Labels: map[string]string{"agent.provider.claude": "true"}})
	requirePermissionDenied(t, err, `label "agent.provider.claude" needs coordinator.worker_pools`)
}

// The pool of the poller goes into the claim and into the lease. Each later call
// of the attempt must come from the same pool.
func TestClaimAndLeaseKeepThePool(t *testing.T) {
	t.Parallel()

	h, stores := newPoolsTestHandler(t, newPoolsForTest(t))
	enqueueAgentTask(t, stores.dispatch, "a1", map[string]string{"agent.provider.claude": "true"})

	ctx, cancel := context.WithTimeout(identityContext("agent-claude"), time.Second)
	defer cancel()
	resp, err := h.Poll(ctx, &coordinatorv1.PollRequest{WorkerId: "agent-1", PollerId: "p", Labels: map[string]string{"agent.provider.claude": "true"}})
	require.NoError(t, err)
	require.NotNil(t, resp.Task)
	claim, err := stores.dispatch.GetClaim(context.Background(), resp.Task.ClaimToken)
	require.NoError(t, err)
	assert.Equal(t, "agent-claude", claim.WorkerPool)

	ack := &coordinatorv1.AckTaskClaimRequest{WorkerId: "agent-1", ClaimToken: resp.Task.ClaimToken, AttemptKey: resp.Task.AttemptKey}
	_, err = h.AckTaskClaim(identityContext("general-worker"), ack)
	requirePermissionDenied(t, err, `the claim belongs to the worker pool "agent-claude", and the call comes from the worker pool "general"`)
	_, err = h.AckTaskClaim(identityContext("rogue"), ack)
	requirePermissionDenied(t, err, `certificate identity "rogue" is in no worker pool`)

	accepted, err := h.AckTaskClaim(identityContext("agent-claude"), ack)
	require.NoError(t, err)
	require.True(t, accepted.Accepted)
	lease, err := stores.lease.Get(context.Background(), resp.Task.AttemptKey)
	require.NoError(t, err)
	assert.Equal(t, "agent-claude", lease.WorkerPool)

	// A repeated acknowledgment checks the pool of the lease.
	_, err = h.AckTaskClaim(identityContext("general-worker"), ack)
	requirePermissionDenied(t, err, `the attempt belongs to the worker pool "agent-claude"`)
	again, err := h.AckTaskClaim(identityContext("agent-claude"), ack)
	require.NoError(t, err)
	assert.True(t, again.Accepted)

	identity := attemptIdentity{
		attemptKey: resp.Task.AttemptKey, claimKey: resp.Task.AttemptKey, workerID: "agent-1",
		dagRun: ir.NewDAGRunRef("dag-a1", "a1"), root: ir.NewDAGRunRef("dag-a1", "a1"), attemptID: "attempt-a1",
	}
	require.NoError(t, h.validateAttempt(identityContext("agent-claude"), identity))
	err = h.validateAttempt(identityContext("general-worker"), identity)
	requirePermissionDenied(t, err, `the attempt belongs to the worker pool "agent-claude", and the call comes from the worker pool "general"`)

	// A run heartbeat from another pool fails too.
	_, err = h.RunHeartbeat(identityContext("general-worker"), &coordinatorv1.RunHeartbeatRequest{WorkerId: "agent-1", RunningTasks: []*coordinatorv1.RunningTask{{
		AttemptKey: resp.Task.AttemptKey, DagName: "dag-a1", DagRunId: "a1", RootDagRunName: "dag-a1", RootDagRunId: "a1",
	}}})
	requirePermissionDenied(t, err, "the attempt belongs to the worker pool")
}

// A lease of a pool refuses a call from a coordinator that has no pools, and a
// lease with no pool refuses a call from a pool.
func TestCheckLeasePool(t *testing.T) {
	t.Parallel()

	plain, _ := newPoolsTestHandler(t, nil)
	require.NoError(t, plain.checkLeasePool(context.Background(), ""))
	requirePermissionDenied(t, plain.checkLeasePool(context.Background(), "agent-claude"), `belongs to the worker pool "agent-claude"`)

	pooled, _ := newPoolsTestHandler(t, newPoolsForTest(t))
	require.NoError(t, pooled.checkLeasePool(identityContext("agent-claude"), "agent-claude"))
	requirePermissionDenied(t, pooled.checkLeasePool(identityContext("agent-claude"), ""), `the attempt belongs to the worker pool ""`)
}

// The final error of a held dispatch names the provider (Spec 092, "No match").
func TestHeldDispatchFailureNamesTheProvider(t *testing.T) {
	t.Parallel()

	text := heldDispatchFailure(&dispatch.DispatchTask{WorkerSelector: map[string]string{"agent.provider.claude": "true"}}, 2*time.Minute)
	assert.Contains(t, text, `no agent worker serves the provider "claude": the selector is {agent.provider.claude: "true"}`)
	assert.Contains(t, text, "2m0s")

	assert.Equal(t, "no worker matched the selector {zone: eu} within 1m0s",
		heldDispatchFailure(&dispatch.DispatchTask{WorkerSelector: map[string]string{"zone": "eu"}}, time.Minute))
}

// A worker ID belongs to the pool of its last fresh heartbeat. A heartbeat and a
// cleanup call that name the worker ID from another pool fail.
func TestWorkerIDBelongsToItsPool(t *testing.T) {
	t.Parallel()

	h, stores := newPoolsTestHandler(t, newPoolsForTest(t))
	_, err := h.Heartbeat(identityContext("agent-claude"), &coordinatorv1.HeartbeatRequest{WorkerId: "agent-1", Labels: map[string]string{"agent.provider.claude": "true"}})
	require.NoError(t, err)
	record, err := stores.heartbeat.Get(context.Background(), "agent-1")
	require.NoError(t, err)
	assert.Equal(t, "agent-claude", record.WorkerPool)

	_, err = h.Heartbeat(identityContext("general-worker"), &coordinatorv1.HeartbeatRequest{WorkerId: "agent-1", Labels: map[string]string{"zone": "eu"}})
	requirePermissionDenied(t, err, `worker "agent-1" belongs to the worker pool "agent-claude", and the call comes from the worker pool "general"`)
	record, err = stores.heartbeat.Get(context.Background(), "agent-1")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"agent.provider.claude": "true"}, record.Labels, "the refused heartbeat changed nothing")

	_, err = h.ClaimAgentSessionCleanup(identityContext("general-worker"), &coordinatorv1.ClaimAgentSessionCleanupRequest{WorkerId: "agent-1"})
	requirePermissionDenied(t, err, `worker "agent-1" belongs to the worker pool "agent-claude"`)
	_, err = h.CompleteAgentSessionCleanup(identityContext("general-worker"), &coordinatorv1.CompleteAgentSessionCleanupRequest{WorkerId: "agent-1", JobId: "j", ClaimToken: "c"})
	requirePermissionDenied(t, err, `worker "agent-1" belongs to the worker pool "agent-claude"`)
	_, err = h.ClaimAgentSessionCleanup(identityContext("general-worker"), &coordinatorv1.ClaimAgentSessionCleanupRequest{WorkerId: "general-9"})
	requirePermissionDenied(t, err, `worker "general-9" has no fresh heartbeat of the worker pool "general"`)
	_, err = h.ClaimAgentSessionCleanup(identityContext("agent-claude"), &coordinatorv1.ClaimAgentSessionCleanupRequest{WorkerId: "agent-1"})
	assert.Equal(t, codes.Unimplemented, status.Code(err), "the owner passes the check: %v", err)
}

// Only the pool of a worker ID can mark the worker offline. The offline
// heartbeat has the checks of a heartbeat.
func TestOfflineHeartbeatKeepsThePool(t *testing.T) {
	t.Parallel()

	h, stores := newPoolsTestHandler(t, newPoolsForTest(t))
	claude := map[string]string{"agent.provider.claude": "true"}
	_, err := h.Heartbeat(identityContext("agent-claude"), &coordinatorv1.HeartbeatRequest{WorkerId: "agent-1", Labels: claude})
	require.NoError(t, err)

	_, err = h.Heartbeat(identityContext("general-worker"), &coordinatorv1.HeartbeatRequest{WorkerId: "agent-1", Labels: map[string]string{"zone": "eu"}, Offline: true})
	requirePermissionDenied(t, err, `worker "agent-1" belongs to the worker pool "agent-claude", and the call comes from the worker pool "general"`)
	_, err = h.Heartbeat(identityContext("rogue"), &coordinatorv1.HeartbeatRequest{WorkerId: "agent-1", Offline: true})
	requirePermissionDenied(t, err, `certificate identity "rogue" is in no worker pool`)
	_, err = stores.heartbeat.Get(context.Background(), "agent-1")
	require.NoError(t, err, "a refused offline heartbeat removes nothing")

	_, err = h.Heartbeat(identityContext("agent-claude"), &coordinatorv1.HeartbeatRequest{WorkerId: "agent-1", Labels: claude, Offline: true})
	require.NoError(t, err)
	_, err = stores.heartbeat.Get(context.Background(), "agent-1")
	require.ErrorIs(t, err, dispatch.ErrWorkerHeartbeatNotFound, "the pool of the worker removed it")
}

// The legacy matcher of the in-memory path applies the same rule.
func TestMatchesSelectorExclusiveAgentRule(t *testing.T) {
	t.Parallel()

	assert.False(t, matchesSelector(map[string]string{"agent.provider.claude": "true"}, nil))
	assert.False(t, matchesSelector(map[string]string{}, map[string]string{"agent.provider.claude": "true"}))
	assert.True(t, matchesSelector(map[string]string{"agent.provider.claude": "true"}, map[string]string{"agent.provider.claude": "true"}))
}
