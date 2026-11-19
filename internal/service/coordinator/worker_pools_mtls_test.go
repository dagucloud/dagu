// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/test/mtls"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// poolsServer is a coordinator with mutual TLS and the worker pools of the
// example of Spec 092, on a real gRPC server.
type poolsServer struct {
	addr     string
	ca       *mtls.Authority
	dispatch *store.DispatchTaskStore
	leases   *store.DAGRunLeaseStore
}

func startPoolsServer(t *testing.T) *poolsServer {
	t.Helper()
	dir := t.TempDir()
	ca := mtls.NewAuthority(t, dir, "dagu-ca")
	serverPair := ca.Server(t, "coordinator")

	pools, err := coordinator.NewWorkerPools([]config.WorkerPool{
		{Name: "agent-claude", Identities: []string{"agent-claude"}, Labels: map[string]string{"agent.provider.claude": "true"}},
		{Name: "general", Identities: []string{"general-worker"}, Labels: map[string]string{"zone": "*"}},
	}, []string{"server"}, config.Peer{CertFile: serverPair.CertFile, KeyFile: serverPair.KeyFile, ClientCaFile: ca.CAFile})
	require.NoError(t, err)

	serverCert, err := tls.LoadX509KeyPair(serverPair.CertFile, serverPair.KeyFile)
	require.NoError(t, err)
	caPEM, err := os.ReadFile(ca.CAFile)
	require.NoError(t, err)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(caPEM))
	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		MinVersion:   tls.VersionTLS12,
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	base := filepath.Join(dir, "distributed")
	s := &poolsServer{
		addr:     listener.Addr().String(),
		ca:       ca,
		dispatch: store.NewDispatchTaskStore(file.NewCollection(base)),
		leases:   store.NewDAGRunLeaseStore(file.NewCollection(filepath.Join(base, "leases"))),
	}
	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		DispatchTaskStore:         s.dispatch,
		DAGRunLeaseStore:          s.leases,
		WorkerHeartbeatStore:      store.NewWorkerHeartbeatStore(file.NewCollection(filepath.Join(base, "workers"))),
		ActiveDistributedRunStore: store.NewActiveDistributedRunStore(file.NewCollection(filepath.Join(base, "active-runs"))),
		WorkerPools:               pools,
		Owner:                     dispatch.CoordinatorEndpoint{ID: "coord", Host: "127.0.0.1", Port: port},
	})
	server := grpc.NewServer(append([]grpc.ServerOption{grpc.Creds(creds)}, pools.ServerOptions()...)...)
	coordinatorv1.RegisterCoordinatorServiceServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return s
}

// client connects with the client certificate of the identity, issued by the
// authority, and trusts the authority of the server.
func (s *poolsServer) client(t *testing.T, issuer *mtls.Authority, identity string) coordinatorv1.CoordinatorServiceClient {
	t.Helper()
	pair := issuer.Client(t, identity)
	cert, err := tls.LoadX509KeyPair(pair.CertFile, pair.KeyFile)
	require.NoError(t, err)
	return s.clientWith(t, []tls.Certificate{cert})
}

func (s *poolsServer) clientWith(t *testing.T, certs []tls.Certificate) coordinatorv1.CoordinatorServiceClient {
	t.Helper()
	caPEM, err := os.ReadFile(s.ca.CAFile)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(caPEM))
	conn, err := grpc.NewClient(s.addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		Certificates: certs,
		RootCAs:      roots,
		MinVersion:   tls.VersionTLS12,
	})))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return coordinatorv1.NewCoordinatorServiceClient(conn)
}

func (s *poolsServer) enqueue(t *testing.T, id string, selector map[string]string) {
	t.Helper()
	require.NoError(t, s.dispatch.Enqueue(context.Background(), &dispatch.DispatchTask{
		DAGRunID: id, Target: "dag-" + id, AttemptID: "attempt-" + id, AttemptKey: "key-" + id,
		RootDAGRunName: "dag-" + id, RootDAGRunID: id, WorkerSelector: selector,
	}))
}

func poll(t *testing.T, client coordinatorv1.CoordinatorServiceClient, workerID string, labels map[string]string) (*coordinatorv1.Task, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := client.Poll(ctx, &coordinatorv1.PollRequest{
		WorkerId: workerID, PollerId: workerID + "-poller", Labels: labels, Capabilities: []string{ir.CapabilityAIRelay},
	})
	if err != nil {
		return nil, err
	}
	return resp.Task, nil
}

var agentLabels = map[string]string{"agent.provider.claude": "true", "os": "linux"}

// Spec 092, "Worker pools", with real certificates: the coordinator takes the
// identity from the verified client certificate and binds the labels to it.
func TestWorkerPoolsWithMutualTLS(t *testing.T) {
	t.Parallel()

	s := startPoolsServer(t)
	s.enqueue(t, "agent-1", map[string]string{"agent.provider.claude": "true"})
	s.enqueue(t, "plain-1", nil)

	general := s.client(t, s.ca, "general-worker")
	agent := s.client(t, s.ca, "agent-claude")

	// A general worker cannot announce the label of an agent worker, and it
	// cannot claim an agent task: it gets the plain task only.
	_, err := poll(t, general, "general-1", agentLabels)
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	assert.Contains(t, status.Convert(err).Message(), `label "agent.provider.claude" with the value "true" is not allowed by the worker pool "general"`)
	task, err := poll(t, general, "general-1", map[string]string{"os": "linux", "zone": "eu"})
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, "plain-1", task.DagRunId)

	// The agent worker claims the agent task, and its claim carries its pool.
	task, err = poll(t, agent, "agent-1", agentLabels)
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, "agent-1", task.DagRunId)

	// A worker of another pool cannot acknowledge the claim.
	_, err = general.AckTaskClaim(context.Background(), &coordinatorv1.AckTaskClaimRequest{WorkerId: "agent-1", ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	ack, err := agent.AckTaskClaim(context.Background(), &coordinatorv1.AckTaskClaimRequest{WorkerId: "agent-1", ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey})
	require.NoError(t, err)
	require.True(t, ack.Accepted)
	lease, err := s.leases.Get(context.Background(), task.AttemptKey)
	require.NoError(t, err)
	assert.Equal(t, "agent-claude", lease.WorkerPool)

	// An agent worker cannot claim a task that needs no provider.
	s.enqueue(t, "plain-2", nil)
	_, err = poll(t, agent, "agent-1", agentLabels)
	require.Equal(t, codes.DeadlineExceeded, status.Code(err), "the agent worker claims nothing: %v", err)
	_, err = poll(t, agent, "agent-1", map[string]string{"os": "linux"})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "a worker of an agent pool must announce its provider: %v", err)
	task, err = poll(t, general, "general-1", nil)
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, "plain-2", task.DagRunId)
}

// Spec 092, "Behavior", with real certificates: a call that is not a call of a
// worker needs a dispatcher, or a pool that holds a fresh lease of the run tree.
// A certificate in no pool that is not a dispatcher cannot dispatch, and a
// certificate of a general pool cannot dispatch a root run.
func TestWorkerPoolsDispatchNeedsADispatcher(t *testing.T) {
	t.Parallel()

	s := startPoolsServer(t)
	rogue := s.client(t, s.ca, "rogue")
	general := s.client(t, s.ca, "general-worker")
	server := s.client(t, s.ca, "server")
	rootRun := &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Operation: coordinatorv1.Operation_OPERATION_START, DagRunId: "root-1", Target: "plain",
		Definition: "name: plain\nsteps:\n  - name: s\n    run: echo hi\n",
	}}

	_, err := rogue.Dispatch(context.Background(), rootRun)
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	assert.Equal(t, `certificate identity "rogue" is not a dispatcher and is in no worker pool`, status.Convert(err).Message())

	_, err = general.Dispatch(context.Background(), rootRun)
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	assert.Contains(t, status.Convert(err).Message(), "a worker cannot dispatch a root run")

	// A child of a root run whose lease the pool does not hold is refused too.
	child := &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Operation: coordinatorv1.Operation_OPERATION_START, DagRunId: "child-1", Target: "plain",
		Definition:     "name: plain\nsteps:\n  - name: s\n    run: echo hi\n",
		RootDagRunName: "parent", RootDagRunId: "parent-1", ParentDagRunName: "parent", ParentDagRunId: "parent-1",
	}}
	_, err = general.Dispatch(context.Background(), child)
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	assert.Contains(t, status.Convert(err).Message(), `the worker pool "general" holds no fresh lease of the root run`)

	// When the pool holds a fresh lease of the root run, the child passes the check.
	require.NoError(t, s.leases.Upsert(context.Background(), dispatch.DAGRunLease{
		AttemptKey: "key-parent", DAGRun: ir.NewDAGRunRef("parent", "parent-1"), Root: ir.NewDAGRunRef("parent", "parent-1"),
		AttemptID: "attempt-parent", WorkerID: "general-1", WorkerPool: "general", LastHeartbeatAt: time.Now().UnixMilli(),
	}))
	_, err = general.Dispatch(context.Background(), child)
	assert.NotEqual(t, codes.PermissionDenied, status.Code(err), "%v", err)

	// The dispatcher makes each call. This coordinator has no DAG-run storage, so
	// the dispatch fails after the check.
	_, err = server.Dispatch(context.Background(), rootRun)
	assert.NotEqual(t, codes.PermissionDenied, status.Code(err), "%v", err)
	_, err = server.GetWorkers(context.Background(), &coordinatorv1.GetWorkersRequest{})
	require.NoError(t, err)
}

// A certificate of the right authority with an identity in no pool, which is
// not a dispatcher either, can make no call of the coordinator.
func TestWorkerPoolsRefuseAnIdentityInNoPool(t *testing.T) {
	t.Parallel()

	s := startPoolsServer(t)
	rogue := s.client(t, s.ca, "rogue")

	_, err := poll(t, rogue, "rogue-1", nil)
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	assert.Equal(t, `certificate identity "rogue" is in no worker pool`, status.Convert(err).Message())
	_, err = rogue.Heartbeat(context.Background(), &coordinatorv1.HeartbeatRequest{WorkerId: "rogue-1"})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	stream, err := rogue.StreamLogs(context.Background())
	if err == nil {
		_, err = stream.CloseAndRecv()
	}
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)

	_, err = rogue.GetWorkers(context.Background(), &coordinatorv1.GetWorkersRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	_, err = rogue.GetDAGRunStatus(context.Background(), &coordinatorv1.GetDAGRunStatusRequest{DagName: "plain", DagRunId: "run-1"})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
}

// A certificate of another authority, and a call with no certificate, fail the
// handshake before any call.
func TestWorkerPoolsRefuseAnUntrustedCertificate(t *testing.T) {
	t.Parallel()

	s := startPoolsServer(t)
	other := mtls.NewAuthority(t, t.TempDir(), "other-ca")

	_, err := s.client(t, other, "agent-claude").GetWorkers(context.Background(), &coordinatorv1.GetWorkersRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err), "%v", err)

	_, err = s.clientWith(t, nil).GetWorkers(context.Background(), &coordinatorv1.GetWorkersRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err), "%v", err)
}
