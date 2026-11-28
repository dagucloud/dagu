// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const removedWorkerMessage = "this worker was removed from the coordinator"

// remove removes the enrolled worker with the peer token.
func (s peerTLSServer) remove(t *testing.T, runs coordinatorv1.RemoveWorkerRuns) *coordinatorv1.RemoveWorkerResponse {
	t.Helper()
	resp, err := s.tlsClient(t).RemoveWorker(callContext(t), &coordinatorv1.RemoveWorkerRequest{WorkerId: enrollWorkerID, Runs: runs})
	require.NoError(t, err)
	return resp
}

// enrolled enrolls a worker with a reusable invite.
func (s peerTLSServer) enrolled(t *testing.T) (*ecdsa.PrivateKey, *x509.Certificate, *coordinatorv1.CreateInviteResponse) {
	t.Helper()
	invite := s.createInvite(t, &coordinatorv1.CreateInviteRequest{Reusable: true})
	key, cert := s.enroll(t, invite.Invite)
	return key, cert, invite
}

// calls makes a unary and a streaming call with client and returns their
// errors.
func calls(t *testing.T, client coordinatorv1.CoordinatorServiceClient) map[string]error {
	t.Helper()
	_, unaryErr := client.GetWorkers(t.Context(), &coordinatorv1.GetWorkersRequest{})
	stream, err := client.StreamLogs(t.Context())
	if err == nil {
		_, err = stream.CloseAndRecv()
	}
	return map[string]error{"unary": unaryErr, "stream": err}
}

// assertServed fails the test unless the coordinator serves client's calls.
// The test coordinator streams no logs, so FailedPrecondition shows that the
// stream passed the credential check.
func assertServed(t *testing.T, client coordinatorv1.CoordinatorServiceClient) {
	t.Helper()
	errs := calls(t, client)
	assert.NoError(t, errs["unary"])
	assert.Equal(t, codes.FailedPrecondition, status.Code(errs["stream"]), "%v", errs["stream"])
}

func assertRemoved(t *testing.T, client coordinatorv1.CoordinatorServiceClient) {
	t.Helper()
	for kind, err := range calls(t, client) {
		assert.Equal(t, codes.Unauthenticated, status.Code(err), kind)
		assert.Equal(t, removedWorkerMessage, status.Convert(err).Message(), kind)
	}
}

// Removing a worker that is not connected revokes its certificate at once,
// and every later call on its open connection fails, unary and streaming
// alike (Spec 108 §Revocation).
func TestRemoveWorkerNotConnected(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)
	worker := server.workerClient(t, cert.Raw, key)
	_, err := worker.GetWorkers(t.Context(), &coordinatorv1.GetWorkersRequest{})
	require.NoError(t, err)

	resp := server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED, resp.State)
	assert.Equal(t, []string{cert.Subject.CommonName}, resp.CertificateIds)
	assertRemoved(t, worker)
	assert.Equal(t, resp.State, server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_STOP).State,
		"removing a removed worker again changes nothing")
}

// A removed certificate cannot enroll again by presenting itself, even
// with a valid invite and a new key. Health checks still answer it, so the
// worker learns of its removal from its real calls (Spec 108 §Revocation).
func TestRemovedCertificateEnrollAndHealth(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, invite := server.enrolled(t)
	server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)

	config := server.tlsConfig()
	config.Certificates = []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}}
	client, health := server.dial(t, credentials.NewTLS(config))
	_, err := client.Enroll(t.Context(), enrollRequest(t, invite.Invite, newWorkerKey(t)))
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Equal(t, removedWorkerMessage, status.Convert(err).Message())
	resp, err := health.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
	server.enroll(t, invite.Invite)
}

// Another coordinator on the same data directory refuses the certificate
// from its next reread of the removed workers, which its 30-second ticker
// runs (Spec 108 §Revocation).
func TestRemoveWorkerOtherCoordinator(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	other := server.another(t)
	key, cert, _ := server.enrolled(t)
	worker := other.workerClient(t, cert.Raw, key)

	server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	assertServed(t, worker)
	coordinator.RereadRemovalsForTest(other.handler)
	assertRemoved(t, worker)
}

// Enroll refuses the key of a removed worker, without using the invite,
// and enrolls a new key with the same invite (Spec 108 §Coordinator Side).
func TestEnrollRefusesRemovedKey(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, _, invite := server.enrolled(t)
	server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)

	_, err := server.another(t).tlsClient(t).Enroll(t.Context(), enrollRequest(t, invite.Invite, key))
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Equal(t, "this key belongs to a removed worker", status.Convert(err).Message())
	assert.Equal(t, 1, server.inviteUses(t, invite.Record.Id))
	server.enroll(t, invite.Invite)
}

// A connected worker in removal keeps working until it holds no run
// (Spec 108 §Removal).
func TestRemoveWorkerConnected(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)
	worker := server.workerClient(t, cert.Raw, key)
	_, err := worker.Heartbeat(t.Context(), &coordinatorv1.HeartbeatRequest{WorkerId: enrollWorkerID, Host: enrollHost})
	require.NoError(t, err)

	resp := server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING, resp.State)
	assertServed(t, worker)
	_, err = worker.Heartbeat(t.Context(), &coordinatorv1.HeartbeatRequest{WorkerId: enrollWorkerID, Host: enrollHost})
	assert.NoError(t, err)
}

// A worker on the peer token cannot be removed, and a removal of an
// unknown worker finds none (Spec 108 §Removal).
func TestRemoveWorkerWithoutCertificate(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	client := server.tlsClient(t)
	_, err := client.Heartbeat(callContext(t), &coordinatorv1.HeartbeatRequest{WorkerId: "token-worker"})
	require.NoError(t, err)

	_, err = client.RemoveWorker(callContext(t), &coordinatorv1.RemoveWorkerRequest{WorkerId: "token-worker"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Contains(t, status.Convert(err).Message(), "stop it on its host, or change the peer token")

	_, err = client.RemoveWorker(callContext(t), &coordinatorv1.RemoveWorkerRequest{WorkerId: "unknown"})
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = client.RemoveWorker(callContext(t), &coordinatorv1.RemoveWorkerRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
