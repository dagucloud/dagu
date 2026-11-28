// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// Removing a worker that is not connected revokes its certificate at once:
// the next call on its open connection fails, the coordinator still refuses
// it after a restart, and Enroll refuses its key. The removal takes the peer
// token and refuses the worker's own certificate (Spec 108 §Revocation,
// §Removal).
func TestRemoveWorker(t *testing.T) {
	const removed = "this worker was removed from the coordinator"
	dagu := harness.NewRunner(t)
	peer := newPeerFixture(t, dagu)
	port := harness.FreePort(t)
	// Each start gets its own home unless told otherwise, and the restart
	// below needs the same data directory.
	env := append(peer.env, "DAGU_HOME="+t.TempDir())
	coordinator := startCoordinatorOnPort(t, dagu, env, port, servingBuiltinTLS)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	admin := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, addr, peer.builtinTLS()))
	tokenCtx := metadata.AppendToOutgoingContext(callContext(t), "dagu-peer-token", peer.token)

	created, err := admin.CreateInvite(tokenCtx, &coordinatorv1.CreateInviteRequest{Reusable: true}, grpc.WaitForReady(true))
	require.NoError(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	require.NoError(t, err)
	req := &coordinatorv1.EnrollRequest{Invite: created.Invite, Csr: csr, WorkerId: "worker-1", Host: "host-a", KeepsCertificate: true}
	enrolled, err := admin.Enroll(callContext(t), req)
	require.NoError(t, err)
	worker := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, addr, credentials.NewTLS(&tls.Config{
		RootCAs:      peer.roots,
		ServerName:   coordinatorCertName,
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{enrolled.Certificate}, PrivateKey: key}},
	})))
	_, err = worker.GetWorkers(callContext(t), &coordinatorv1.GetWorkersRequest{})
	require.NoError(t, err)

	removal := &coordinatorv1.RemoveWorkerRequest{WorkerId: "worker-1"}
	_, err = worker.RemoveWorker(callContext(t), removal)
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	_, err = admin.RemoveWorker(callContext(t), removal)
	assert.Equal(t, codes.Unauthenticated, status.Code(err), "%v", err)
	resp, err := admin.RemoveWorker(tokenCtx, removal)
	require.NoError(t, err)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED, resp.State)

	_, err = worker.GetWorkers(callContext(t), &coordinatorv1.GetWorkersRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err), "%v", err)
	assert.Equal(t, removed, status.Convert(err).Message())
	_, err = admin.Enroll(callContext(t), req)
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	assert.Equal(t, "this key belongs to a removed worker", status.Convert(err).Message())

	coordinator.Stop()
	startCoordinatorOnPort(t, dagu, env, port, servingBuiltinTLS)
	_, err = worker.GetWorkers(callContext(t), &coordinatorv1.GetWorkersRequest{}, grpc.WaitForReady(true))
	assert.Equal(t, codes.Unauthenticated, status.Code(err), "%v", err)
	assert.Equal(t, removed, status.Convert(err).Message())
}

// connectedWorker is a worker enrolled on its own coordinator, as a gRPC
// client on the worker certificate, with the coordinator's admin client.
type connectedWorker struct {
	admin, worker coordinatorv1.CoordinatorServiceClient
	tokenCtx      context.Context
}

func startConnectedWorker(t *testing.T) connectedWorker {
	t.Helper()
	dagu := harness.NewRunner(t)
	peer := newPeerFixture(t, dagu)
	addr := serveCoordinator(t, dagu, peer.env, servingBuiltinTLS)
	admin := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, addr, peer.builtinTLS()))
	tokenCtx := metadata.AppendToOutgoingContext(t.Context(), "dagu-peer-token", peer.token)
	created, err := admin.CreateInvite(tokenCtx, &coordinatorv1.CreateInviteRequest{Reusable: true}, grpc.WaitForReady(true))
	require.NoError(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	require.NoError(t, err)
	enrolled, err := admin.Enroll(callContext(t), &coordinatorv1.EnrollRequest{
		Invite: created.Invite, Csr: csr, WorkerId: "worker-1", Host: "host-a", KeepsCertificate: true,
	})
	require.NoError(t, err)
	worker := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, addr, credentials.NewTLS(&tls.Config{
		RootCAs:      peer.roots,
		ServerName:   coordinatorCertName,
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{enrolled.Certificate}, PrivateKey: key}},
	})))
	return connectedWorker{admin: admin, worker: worker, tokenCtx: tokenCtx}
}

// heartbeat sends the worker's heartbeat, listing the runs runIDs.
func (w connectedWorker) heartbeat(t *testing.T, runIDs ...string) error {
	t.Helper()
	stats := &coordinatorv1.WorkerStats{}
	for _, id := range runIDs {
		stats.RunningTasks = append(stats.RunningTasks, &coordinatorv1.RunningTask{DagName: "dag", DagRunId: id, AttemptKey: "attempt-" + id})
	}
	_, err := w.worker.Heartbeat(callContext(t), &coordinatorv1.HeartbeatRequest{WorkerId: "worker-1", Host: "host-a", Stats: stats})
	return err
}

// waitRemoved sends heartbeats listing runIDs until the coordinator refuses
// them as removed, and fails the test if that takes longer than within.
func (w connectedWorker) waitRemoved(t *testing.T, within time.Duration, runIDs ...string) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		err := w.heartbeat(t, runIDs...)
		if status.Code(err) == codes.Unauthenticated {
			assert.Equal(t, "this worker was removed from the coordinator", status.Convert(err).Message())
			return
		}
		require.NoError(t, err)
		require.True(t, time.Now().Before(deadline), "the worker was not removed within %v", within)
		time.Sleep(time.Second)
	}
}

// dispatch queues a run of a one-step DAG through the coordinator, for
// targetWorker when it is set.
func (w connectedWorker) dispatch(t *testing.T, runID, targetWorker string) {
	t.Helper()
	_, err := w.admin.Dispatch(w.tokenCtx, &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_START,
		DagRunId:       runID,
		Target:         "removal",
		Definition:     "steps:\n  - name: wait\n    run: sleep 1\n",
		AttemptId:      "a1b2c3d4e5f6",
		TargetWorkerId: targetWorker,
	}})
	require.NoError(t, err)
}

// poll polls for a task as workerID with client and returns it, or nil when
// none came within two seconds.
func poll(t *testing.T, ctx context.Context, client coordinatorv1.CoordinatorServiceClient, workerID string) *coordinatorv1.Task {
	t.Helper()
	pollCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := client.Poll(pollCtx, &coordinatorv1.PollRequest{WorkerId: workerID, PollerId: workerID + "-poller"})
	if status.Code(err) == codes.DeadlineExceeded {
		return nil
	}
	require.NoError(t, err)
	return resp.Task
}

// A connected worker in removal with finish keeps reporting the run it
// holds. It cannot accept a task it claimed before the removal, gets no
// queued task, which another worker takes, and is removed once it holds no
// run and 30 seconds have passed (Spec 108 §Removal).
func TestRemoveConnectedWorkerFinish(t *testing.T) {
	t.Parallel()
	w := startConnectedWorker(t)
	require.NoError(t, w.heartbeat(t, "run-0"))
	_, err := w.admin.Heartbeat(w.tokenCtx, &coordinatorv1.HeartbeatRequest{WorkerId: "token-worker", Host: "host-b"})
	require.NoError(t, err)
	w.dispatch(t, "run-1", "worker-1")
	claimed := poll(t, callContext(t), w.worker, "worker-1")
	require.NotNil(t, claimed, "the worker claims its task before the removal")

	// The lower bound of the removal's start comes before the call.
	removalCalled := time.Now()
	resp, err := w.admin.RemoveWorker(w.tokenCtx, &coordinatorv1.RemoveWorkerRequest{WorkerId: "worker-1"})
	require.NoError(t, err)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING, resp.State)
	require.NoError(t, w.heartbeat(t, "run-0"), "the held run still reports")

	ack, err := w.worker.AckTaskClaim(callContext(t), &coordinatorv1.AckTaskClaimRequest{
		WorkerId: "worker-1", ClaimToken: claimed.ClaimToken, AttemptKey: claimed.AttemptKey,
	})
	require.NoError(t, err)
	assert.False(t, ack.Accepted, "the task claimed before the removal is refused")
	w.dispatch(t, "run-2", "")
	assert.Nil(t, poll(t, callContext(t), w.worker, "worker-1"), "the removing worker gets no queued task")
	taken := poll(t, w.tokenCtx, w.admin, "token-worker")
	require.NotNil(t, taken, "another worker takes the queued task")
	assert.Equal(t, "run-2", taken.DagRunId)

	w.waitRemoved(t, time.Minute)
	assert.GreaterOrEqual(t, time.Since(removalCalled), 30*time.Second, "not before 30 seconds in removing")
}

// runningHeartbeat sends the worker's heartbeat listing the run of task and
// renews the run's lease, as a worker does, and returns the attempt keys
// the coordinator asks it to cancel.
func (w connectedWorker) runningHeartbeat(t *testing.T, task *coordinatorv1.Task) ([]string, error) {
	t.Helper()
	running := []*coordinatorv1.RunningTask{{
		DagName: task.Target, DagRunId: task.DagRunId, AttemptKey: task.AttemptKey,
		RootDagRunName: task.RootDagRunName, RootDagRunId: task.RootDagRunId,
	}}
	resp, err := w.worker.Heartbeat(callContext(t), &coordinatorv1.HeartbeatRequest{
		WorkerId: "worker-1", Host: "host-a", Stats: &coordinatorv1.WorkerStats{RunningTasks: running},
	})
	if err != nil {
		return nil, err
	}
	if _, err := w.worker.RunHeartbeat(callContext(t), &coordinatorv1.RunHeartbeatRequest{WorkerId: "worker-1", RunningTasks: running}); err != nil {
		return nil, err
	}
	var keys []string
	for _, run := range resp.CancelledRuns {
		keys = append(keys, run.AttemptKey)
	}
	return keys, nil
}

// A connected worker in removal with stop gets the cancel request for the
// run it accepted, keeps reporting it, and is removed when the 2-minute
// wait ends, with the run still held (Spec 108 §Removal).
func TestRemoveConnectedWorkerStop(t *testing.T) {
	t.Parallel()
	w := startConnectedWorker(t)
	require.NoError(t, w.heartbeat(t))
	w.dispatch(t, "run-1", "worker-1")
	task := poll(t, callContext(t), w.worker, "worker-1")
	require.NotNil(t, task)
	ack, err := w.worker.AckTaskClaim(callContext(t), &coordinatorv1.AckTaskClaimRequest{
		WorkerId: "worker-1", ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey,
	})
	require.NoError(t, err)
	require.True(t, ack.Accepted, ack.Error)
	cancelled, err := w.runningHeartbeat(t, task)
	require.NoError(t, err)
	require.Empty(t, cancelled, "no cancel request before the removal")

	// The deadline is bracketed by the times around the call, so a slow
	// reply does not move it.
	calledAt := time.Now()
	resp, err := w.admin.RemoveWorker(w.tokenCtx, &coordinatorv1.RemoveWorkerRequest{
		WorkerId: "worker-1", Runs: coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_STOP,
	})
	repliedAt := time.Now()
	require.NoError(t, err)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING, resp.State)
	cancelled, err = w.runningHeartbeat(t, task)
	require.NoError(t, err, "the held run still reports")
	assert.Contains(t, cancelled, task.AttemptKey, "the run gets the cancel request")

	// The deadline wakes the coordinator at once; a heartbeat each second
	// sees the revocation within a second of it.
	const margin = 3 * time.Second
	for {
		_, err := w.runningHeartbeat(t, task)
		refusedAt := time.Now()
		if status.Code(err) == codes.Unauthenticated {
			assert.Equal(t, "this worker was removed from the coordinator", status.Convert(err).Message())
			assert.False(t, refusedAt.Before(calledAt.Add(2*time.Minute)), "held until the 2-minute wait ends")
			assert.True(t, refusedAt.Before(repliedAt.Add(2*time.Minute+margin)), "removed by the deadline")
			return
		}
		require.NoError(t, err)
		require.True(t, refusedAt.Before(repliedAt.Add(2*time.Minute+margin)), "not removed by the deadline")
		time.Sleep(time.Second)
	}
}
