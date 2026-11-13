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
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/test/mtls"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Attacks on the worker pools and the routing (Spec 092, "Worker pools" and
// "Routing"), with real certificates. Each test states the property of the
// spec. A test that fails shows a bypass.

const reviewPlainDAG = "name: plain\nsteps:\n  - name: s\n    run: echo hi\n"

const reviewAgentDAG = "name: coder\nsteps:\n  - id: code\n    action: agent.run\n    with:\n      provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      prompt: hi\n"

// reviewServer is a coordinator with mutual TLS, worker pools and DAG-run
// storage, so Dispatch, the claim and the status reports run as in production.
type reviewServer struct {
	addr   string
	ca     *mtls.Authority
	runs   *persis.DAGRunRepository
	leases *store.DAGRunLeaseStore
	// bundleDir is the folder of the workspace bundles. It is empty when the
	// server has no DAG repository.
	bundleDir string
}

func startReviewServer(t *testing.T) *reviewServer {
	t.Helper()
	return startReviewServerWithDAGs(t, nil)
}

// startReviewServerWithDAGs starts the server with a DAG repository, whose
// DAGs directory serves the skills of a definition.
func startReviewServerWithDAGs(t *testing.T, dags *persis.DAGRepository) *reviewServer {
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
	s := &reviewServer{
		addr:   listener.Addr().String(),
		ca:     ca,
		runs:   testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true}),
		leases: store.NewDAGRunLeaseStore(file.NewCollection(filepath.Join(base, "leases"))),
	}
	bundleDir := ""
	if dags != nil {
		bundleDir = filepath.Join(dir, "bundles")
	}
	s.bundleDir = bundleDir
	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		DAGRunRepository:          s.runs,
		WorkspaceBundleDir:        bundleDir,
		DispatchTaskStore:         store.NewDispatchTaskStore(file.NewCollection(base)),
		DAGRunLeaseStore:          s.leases,
		WorkerHeartbeatStore:      store.NewWorkerHeartbeatStore(file.NewCollection(filepath.Join(base, "workers"))),
		ActiveDistributedRunStore: store.NewActiveDistributedRunStore(file.NewCollection(filepath.Join(base, "active-runs"))),
		DAGRepository:             dags,
		WorkerPools:               pools,
		WorkerWaitTimeout:         time.Minute,
		Owner:                     dispatch.CoordinatorEndpoint{ID: "coord", Host: "127.0.0.1", Port: port},
	})
	server := grpc.NewServer(append([]grpc.ServerOption{grpc.Creds(creds)}, pools.ServerOptions()...)...)
	coordinatorv1.RegisterCoordinatorServiceServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	// The handler keeps the file of each attempt open until it closes. Windows cannot
	// remove the open files of a test folder, so the handler closes after the server stops.
	t.Cleanup(func() { handler.Close(context.Background()) })
	t.Cleanup(server.Stop)
	return s
}

func (s *reviewServer) client(t *testing.T, identity string) coordinatorv1.CoordinatorServiceClient {
	t.Helper()
	pair := s.ca.Client(t, identity)
	cert, err := tls.LoadX509KeyPair(pair.CertFile, pair.KeyFile)
	require.NoError(t, err)
	caPEM, err := os.ReadFile(s.ca.CAFile)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(caPEM))
	conn, err := grpc.NewClient(s.addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      roots,
		MinVersion:   tls.VersionTLS12,
	})))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return coordinatorv1.NewCoordinatorServiceClient(conn)
}

// heartbeat announces a worker that supports the AI relay: an agent.run step names an
// endpoint, so the coordinator hands its run only to such a worker.
func heartbeat(t *testing.T, client coordinatorv1.CoordinatorServiceClient, workerID string, labels map[string]string) {
	t.Helper()
	_, err := client.Heartbeat(context.Background(), &coordinatorv1.HeartbeatRequest{
		WorkerId: workerID, Labels: labels, Capabilities: []string{ir.CapabilityAIRelay},
	})
	require.NoError(t, err)
}

// Spec 092, "Reserved labels" and "Match": Dagu sets the labels that start with
// agent., and a run that needs no provider never goes to an agent worker. The
// coordinator removes the agent. labels of the selector of a dispatch and adds
// the implied label of the DAG. A certificate in no pool that is not a
// dispatcher cannot dispatch at all.
func TestDispatchRefusesAgentLabelsOfTheCaller(t *testing.T) {
	t.Parallel()

	s := startReviewServer(t)
	agent := s.client(t, "agent-claude")
	rogue := s.client(t, "rogue")
	dispatcher := s.client(t, "server")
	heartbeat(t, agent, "agent-1", agentLabels)

	forged := &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_START,
		DagRunId:       "plain-run",
		Target:         "plain",
		Definition:     reviewPlainDAG,
		WorkerSelector: map[string]string{"agent.provider.claude": "true"},
	}}
	_, err := rogue.Dispatch(context.Background(), forged)
	require.Error(t, err, "a certificate in no pool that is not a dispatcher cannot dispatch")

	_, err = dispatcher.Dispatch(context.Background(), forged)
	require.NoError(t, err)
	task, _ := poll(t, agent, "agent-1", agentLabels)
	if task != nil {
		assert.Failf(t, "an agent worker claimed a run that needs no provider",
			"task %s of the DAG %q, selector %v", task.DagRunId, task.Target, task.WorkerSelector)
	}
}

// Spec 092, "Dispatch" and "Match": a task of a DAG that needs a provider has the
// implied label, so no general worker claims it. The coordinator adds the label
// for each task shape, also for a task whose root is another run and that has no
// parent. A general worker that holds no lease of that root cannot dispatch it.
func TestDispatchAddsTheImpliedLabelForEachTask(t *testing.T) {
	t.Parallel()

	s := startReviewServer(t)
	general := s.client(t, "general-worker")
	dispatcher := s.client(t, "server")
	heartbeat(t, general, "general-1", map[string]string{"zone": "eu"})

	orphan := &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_START,
		DagRunId:       "child-run",
		Target:         "coder",
		Definition:     reviewAgentDAG,
		RootDagRunName: "root",
		RootDagRunId:   "root-run",
	}}
	_, err := general.Dispatch(context.Background(), orphan)
	require.Error(t, err, "the general pool holds no lease of the root run")

	_, err = dispatcher.Dispatch(context.Background(), orphan)
	if err != nil {
		return // The coordinator refused the shape: no worker gets the task.
	}
	task, _ := poll(t, general, "general-1", map[string]string{"zone": "eu"})
	if task != nil {
		assert.Failf(t, "a general worker claimed a task of a DAG that needs the provider claude",
			"task %s of the DAG %q, selector %v", task.DagRunId, task.Target, task.WorkerSelector)
	}
}

// Control for the test above: a root run of the same DAG gets the implied label,
// so the general worker does not claim it. Only a dispatcher dispatches a root
// run.
func TestDispatchAddsTheImpliedLabelForARootRun(t *testing.T) {
	t.Parallel()

	s := startReviewServer(t)
	general := s.client(t, "general-worker")
	dispatcher := s.client(t, "server")
	heartbeat(t, general, "general-1", map[string]string{"zone": "eu"})

	root := &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Operation:  coordinatorv1.Operation_OPERATION_START,
		DagRunId:   "root-run",
		Target:     "coder",
		Definition: reviewAgentDAG,
	}}
	_, err := general.Dispatch(context.Background(), root)
	require.Error(t, err, "a general pool cannot dispatch a root run")
	_, err = dispatcher.Dispatch(context.Background(), root)
	require.NoError(t, err)
	task, _ := poll(t, general, "general-1", map[string]string{"zone": "eu"})
	assert.Nil(t, task, "the general worker must not claim the agent run")
}

// Spec 092, "Behavior": each later call for an attempt MUST come from a
// certificate of the pool that claimed it. The lease holds the pool, and the
// final status deletes the lease. A status report after the final status has
// no lease, so the coordinator checks no pool: a general worker rewrites the
// final status of an agent run.
func TestFinishedAttemptKeepsItsPool(t *testing.T) {
	t.Parallel()

	s := startReviewServer(t)
	agent := s.client(t, "agent-claude")
	general := s.client(t, "general-worker")
	dispatcher := s.client(t, "server")
	heartbeat(t, agent, "agent-1", agentLabels)

	_, err := dispatcher.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Operation:  coordinatorv1.Operation_OPERATION_START,
		DagRunId:   "agent-run",
		Target:     "coder",
		Definition: reviewAgentDAG,
	}})
	require.NoError(t, err)

	task, err := poll(t, agent, "agent-1", agentLabels)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, "true", task.WorkerSelector["agent.provider.claude"])
	ack, err := agent.AckTaskClaim(context.Background(), &coordinatorv1.AckTaskClaimRequest{
		WorkerId: "agent-1", ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey,
	})
	require.NoError(t, err)
	require.True(t, ack.Accepted, ack.Error)

	// The general pool holds no lease of the run, so it cannot read the status.
	// The test reads the identity of the attempt as a dispatcher: an attacker
	// can know it by other means.
	_, err = general.GetDAGRunStatus(context.Background(), &coordinatorv1.GetDAGRunStatusRequest{DagName: "coder", DagRunId: "agent-run"})
	require.Error(t, err)
	found, err := dispatcher.GetDAGRunStatus(context.Background(), &coordinatorv1.GetDAGRunStatusRequest{DagName: "coder", DagRunId: "agent-run"})
	require.NoError(t, err)
	require.True(t, found.Found)
	seen, err := convert.ProtoToDAGRunStatus(found.Status)
	require.NoError(t, err)
	attemptID, attemptKey := seen.AttemptID, seen.AttemptKey
	require.Equal(t, task.AttemptId, attemptID)
	require.Equal(t, task.AttemptKey, attemptKey)

	report := func(client coordinatorv1.CoordinatorServiceClient, st ir.Status, output string) *coordinatorv1.ReportStatusResponse {
		t.Helper()
		out := output
		proto, err := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name: "coder", DAGRunID: "agent-run", AttemptID: attemptID, AttemptKey: attemptKey,
			Status: st, WorkerID: "agent-1",
			Nodes: []*ir.Node{{Step: ir.Step{Name: "code", ID: "code"}, Status: ir.NodeSucceeded, StepOutputsValue: &out}},
		})
		require.NoError(t, err)
		resp, err := client.ReportStatus(context.Background(), &coordinatorv1.ReportStatusRequest{WorkerId: "agent-1", Status: proto})
		if err != nil {
			return &coordinatorv1.ReportStatusResponse{Accepted: false, Error: err.Error()}
		}
		return resp
	}

	// While the lease exists, a general worker cannot report a status.
	resp := report(general, ir.Running, `{"result":"forged"}`)
	require.False(t, resp.Accepted, "the lease of the agent pool refuses the general pool")

	resp = report(agent, ir.Running, `{"result":"real"}`)
	require.True(t, resp.Accepted, resp.Error)
	resp = report(agent, ir.Succeeded, `{"result":"real"}`)
	require.True(t, resp.Accepted, resp.Error)
	_, err = s.leases.Get(context.Background(), task.AttemptKey)
	require.ErrorIs(t, err, dispatch.ErrDAGRunLeaseNotFound, "the final status deletes the lease")

	// After the final status, the general worker reports the same status with
	// another output.
	resp = report(general, ir.Succeeded, `{"result":"forged"}`)
	attempt, err := s.runs.FindAttempt(context.Background(), ir.NewDAGRunRef("coder", "agent-run"))
	require.NoError(t, err)
	persisted, err := attempt.ReadStatus(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, persisted.Nodes)
	var stored string
	if persisted.Nodes[0].StepOutputsValue != nil {
		stored = *persisted.Nodes[0].StepOutputsValue
	}
	assert.False(t, resp.Accepted, "a certificate of the general pool rewrote the final status of an attempt of the agent pool")
	assert.Equal(t, `{"result":"real"}`, stored, "the final status of the agent run")
}

// claimAndAck polls one task with the labels and acknowledges its claim.
func claimAndAck(t *testing.T, client coordinatorv1.CoordinatorServiceClient, workerID string, labels map[string]string) *coordinatorv1.Task {
	t.Helper()
	task, err := poll(t, client, workerID, labels)
	require.NoError(t, err)
	require.NotNil(t, task)
	ack, err := client.AckTaskClaim(context.Background(), &coordinatorv1.AckTaskClaimRequest{
		WorkerId: workerID, ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey,
	})
	require.NoError(t, err)
	require.True(t, ack.Accepted, ack.Error)
	return task
}

// Spec 092, "Dispatch" and "Behavior": a plain parent on a general worker
// starts a child that needs claude; the child goes to an agent worker, and its
// lease holds the agent pool. Each call for the attempt of the child MUST come
// from the agent pool. A status of the child that names the claim key of the
// parent is checked against the lease of the parent only ("inline
// descendants"), so the general worker of the parent rewrites the status of the
// running agent child and ends its lease.
func TestAgentChildKeepsItsPool(t *testing.T) {
	t.Parallel()

	s := startReviewServer(t)
	agent := s.client(t, "agent-claude")
	general := s.client(t, "general-worker")
	dispatcher := s.client(t, "server")
	heartbeat(t, agent, "agent-1", agentLabels)
	heartbeat(t, general, "general-1", map[string]string{"zone": "eu"})

	_, err := dispatcher.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Operation: coordinatorv1.Operation_OPERATION_START, DagRunId: "parent-run", Target: "plain", Definition: reviewPlainDAG,
	}})
	require.NoError(t, err)
	parent := claimAndAck(t, general, "general-1", map[string]string{"zone": "eu"})

	_, err = general.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Operation: coordinatorv1.Operation_OPERATION_START, DagRunId: "child-run", Target: "coder", Definition: reviewAgentDAG,
		RootDagRunName: "plain", RootDagRunId: "parent-run", ParentDagRunName: "plain", ParentDagRunId: "parent-run",
	}})
	require.NoError(t, err)
	child := claimAndAck(t, agent, "agent-1", agentLabels)
	require.Equal(t, "child-run", child.DagRunId)
	childLease, err := s.leases.Get(context.Background(), child.AttemptKey)
	require.NoError(t, err)
	require.Equal(t, "agent-claude", childLease.WorkerPool)

	root := ir.NewDAGRunRef("plain", "parent-run")
	childStatus := func(st ir.Status, claimKey, output string) *coordinatorv1.DAGRunStatusProto {
		t.Helper()
		out := output
		proto, err := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name: "coder", DAGRunID: "child-run", AttemptID: child.AttemptId, AttemptKey: child.AttemptKey, ClaimKey: claimKey,
			Root: root, Parent: root, Status: st,
			Nodes: []*ir.Node{{Step: ir.Step{Name: "code", ID: "code"}, Status: ir.NodeSucceeded, StepOutputsValue: &out}},
		})
		require.NoError(t, err)
		return proto
	}

	resp, err := agent.ReportStatus(context.Background(), &coordinatorv1.ReportStatusRequest{
		WorkerId: "agent-1", Status: childStatus(ir.Running, "", `{"result":"real"}`),
	})
	require.NoError(t, err)
	require.True(t, resp.Accepted, resp.Error)

	// The general worker names the claim key of the parent, which it holds.
	resp, err = general.ReportStatus(context.Background(), &coordinatorv1.ReportStatusRequest{
		WorkerId: "general-1", Status: childStatus(ir.Succeeded, parent.AttemptKey, `{"result":"forged"}`),
	})
	accepted := err == nil && resp.Accepted
	assert.False(t, accepted, "the general pool reported a status for the attempt of the agent pool")
	_, leaseErr := s.leases.Get(context.Background(), child.AttemptKey)
	assert.NoError(t, leaseErr, "the report of the general pool deleted the lease of the agent child")
}
