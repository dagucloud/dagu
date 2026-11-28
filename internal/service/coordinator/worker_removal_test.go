// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/persis"
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

// listed returns the worker id as GetWorkers lists it.
func (s peerTLSServer) listed(t *testing.T, id string) *coordinatorv1.WorkerInfo {
	t.Helper()
	resp, err := s.tlsClient(t).GetWorkers(callContext(t), &coordinatorv1.GetWorkersRequest{})
	require.NoError(t, err)
	for _, worker := range resp.Workers {
		if worker.WorkerId == id {
			return worker
		}
	}
	require.Failf(t, "worker not listed", "%s", id)
	return nil
}

// GetWorkers lists the certificate a worker connects with, its invite, and
// its removal, on every coordinator that shares the records. A worker on
// the peer token has none (Spec 108 §Workers List).
func TestGetWorkersListsCertificate(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	other := server.another(t)
	key, cert, invite := server.enrolled(t)
	worker := server.workerClient(t, cert.Raw, key)
	_, err := worker.Heartbeat(t.Context(), &coordinatorv1.HeartbeatRequest{WorkerId: enrollWorkerID, Host: enrollHost})
	require.NoError(t, err)
	_, err = server.tlsClient(t).Heartbeat(callContext(t), &coordinatorv1.HeartbeatRequest{WorkerId: "token-worker"})
	require.NoError(t, err)

	for _, s := range []peerTLSServer{server, other} {
		listed := s.listed(t, enrollWorkerID)
		assert.Equal(t, cert.Subject.CommonName, listed.CertificateId)
		assert.Equal(t, invite.Record.Id, listed.InviteId)
		assert.False(t, listed.Removing)
		token := s.listed(t, "token-worker")
		assert.Empty(t, token.CertificateId)
		assert.Empty(t, token.InviteId)
	}

	server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	assert.True(t, server.listed(t, enrollWorkerID).Removing)
	coordinator.RereadRemovalsForTest(other.handler)
	assert.True(t, other.listed(t, enrollWorkerID).Removing)
}

// removeCertificate removes the enrolled worker, naming the certificate
// certID, with the peer token.
func (s peerTLSServer) removeCertificate(t *testing.T, certID string) (*coordinatorv1.RemoveWorkerResponse, error) {
	t.Helper()
	return s.tlsClient(t).RemoveWorker(callContext(t), &coordinatorv1.RemoveWorkerRequest{WorkerId: enrollWorkerID, CertificateId: certID})
}

// A retried removal names the certificate the worker was listed with, so
// it leaves alone the new certificate the worker joined again with under
// the same ID, and still answers removed (Spec 108 §Removal).
func TestRemoveWorkerRetryAfterRejoin(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	_, old, invite := server.enrolled(t)

	resp, err := server.removeCertificate(t, old.Subject.CommonName)
	require.NoError(t, err)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED, resp.State)

	key, replacement := server.enroll(t, invite.Invite)
	worker := server.workerClient(t, replacement.Raw, key)
	_, err = worker.Heartbeat(t.Context(), &coordinatorv1.HeartbeatRequest{WorkerId: enrollWorkerID, Host: enrollHost})
	require.NoError(t, err)

	resp, err = server.removeCertificate(t, old.Subject.CommonName)
	require.NoError(t, err)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED, resp.State)
	assert.Equal(t, []string{old.Subject.CommonName}, resp.CertificateIds)
	assertServed(t, worker)
	assert.False(t, server.listed(t, enrollWorkerID).Removing)
}

// A removal that names a certificate the worker neither connects with nor
// holds is refused (Spec 108 §Removal).
func TestRemoveWorkerOtherCertificate(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)

	_, err := server.removeCertificate(t, "another-certificate")
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "not connected: %v", err)

	worker := server.workerClient(t, cert.Raw, key)
	_, err = worker.Heartbeat(t.Context(), &coordinatorv1.HeartbeatRequest{WorkerId: enrollWorkerID, Host: enrollHost})
	require.NoError(t, err)
	_, err = server.removeCertificate(t, "another-certificate")
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "connected: %v", err)
	assertServed(t, worker)
}

// A removal that names one of two certificates of a worker that is not
// connected revokes that one only, so a replacement enrolled under the
// same ID before its first heartbeat keeps working (Spec 108 §Removal).
func TestRemoveWorkerNamedCertificateOnly(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	oldKey, old, invite := server.enrolled(t)
	newKey, replacement := server.enroll(t, invite.Invite)

	resp, err := server.removeCertificate(t, old.Subject.CommonName)
	require.NoError(t, err)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED, resp.State)
	assert.Equal(t, []string{old.Subject.CommonName}, resp.CertificateIds)
	assert.Equal(t, enrollWorkerID, resp.WorkerId)
	assertRemoved(t, server.workerClient(t, old.Raw, oldKey))
	assertServed(t, server.workerClient(t, replacement.Raw, newKey))
}

// A revoked certificate of another worker answers nothing for this one:
// a known worker gets FailedPrecondition, so no removal is reported for a
// worker it did not touch.
func TestRemoveWorkerOtherWorkersCertificate(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	_, _, invite := server.enrolled(t)
	otherReq := enrollRequest(t, invite.Invite, newWorkerKey(t))
	otherReq.WorkerId = "other-worker"
	enrolled, err := server.tlsClient(t).Enroll(t.Context(), otherReq)
	require.NoError(t, err)
	other, err := x509.ParseCertificate(enrolled.Certificate)
	require.NoError(t, err)
	_, err = server.tlsClient(t).RemoveWorker(callContext(t), &coordinatorv1.RemoveWorkerRequest{WorkerId: "other-worker", CertificateId: other.Subject.CommonName})
	require.NoError(t, err)

	_, err = server.removeCertificate(t, other.Subject.CommonName)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "%v", err)
	assert.Equal(t, "certificate_changed", coordinator.RefusalReason(err))
}

// The refusals of a removal carry their reasons as status details.
func TestRemoveWorkerRefusalReasons(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	client := server.tlsClient(t)
	_, err := client.Heartbeat(callContext(t), &coordinatorv1.HeartbeatRequest{WorkerId: "token-worker"})
	require.NoError(t, err)
	_, err = client.RemoveWorker(callContext(t), &coordinatorv1.RemoveWorkerRequest{WorkerId: "token-worker", CertificateId: "any"})
	assert.Equal(t, "peer_token_worker", coordinator.RefusalReason(err), "%v", err)

	server.enrolled(t)
	_, err = server.removeCertificate(t, "another-certificate")
	assert.Equal(t, "certificate_changed", coordinator.RefusalReason(err), "%v", err)
}

// A retried removal of a revoked certificate answers removed, also after
// the worker ID came back on the peer token, which it leaves alone.
func TestRemoveWorkerReplayAfterPeerTokenReturn(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	_, cert, _ := server.enrolled(t)
	_, err := server.removeCertificate(t, cert.Subject.CommonName)
	require.NoError(t, err)
	_, err = server.tlsClient(t).Heartbeat(callContext(t), &coordinatorv1.HeartbeatRequest{WorkerId: enrollWorkerID, Host: enrollHost})
	require.NoError(t, err)

	resp, err := server.removeCertificate(t, cert.Subject.CommonName)
	require.NoError(t, err)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED, resp.State)
	assert.Empty(t, server.listed(t, enrollWorkerID).CertificateId, "the peer token worker keeps connecting")
}

// A worker ID that nothing knows answers NotFound, also with the
// certificate of another worker.
func TestRemoveWorkerUnknownWorker(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	_, cert, _ := server.enrolled(t)

	_, err := server.tlsClient(t).RemoveWorker(callContext(t), &coordinatorv1.RemoveWorkerRequest{WorkerId: "nobody", CertificateId: cert.Subject.CommonName})
	assert.Equal(t, codes.NotFound, status.Code(err), "%v", err)
}

// renameHolder makes the enrolled worker connect with its certificate
// under a new ID while the certificate's record still names the earlier
// one, as before the heartbeat's record update reaches it.
func (s peerTLSServer) renameHolder(t *testing.T, key *ecdsa.PrivateKey, cert *x509.Certificate) {
	t.Helper()
	_, err := s.workerClient(t, cert.Raw, key).Heartbeat(t.Context(), &coordinatorv1.HeartbeatRequest{WorkerId: "renamed", Host: enrollHost})
	require.NoError(t, err)
	record, err := s.shared.certs.Get(t.Context(), cert.Subject.CommonName)
	require.NoError(t, err)
	record.WorkerID = enrollWorkerID
	record.LastHeartbeatAt = record.LastHeartbeatAt.Add(2 * time.Minute)
	_, err = s.shared.certs.RecordHeartbeat(t.Context(), record)
	require.NoError(t, err)
}

// A removal of a certificate whose holder connects under a new ID names
// that holder, and the answer, the audit's source, names the removal's
// worker as the record keeps it.
func TestRemoveWorkerNamesHolderAfterRename(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)
	server.renameHolder(t, key, cert)

	resp, err := server.removeCertificate(t, cert.Subject.CommonName)
	require.NoError(t, err)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING, resp.State)
	removals, err := server.shared.certs.ListRemovals(t.Context())
	require.NoError(t, err)
	require.Len(t, removals, 1)
	assert.Equal(t, removals[0].WorkerID, resp.WorkerId)
}

// heartbeat sends a heartbeat of the enrolled worker under id with its
// certificate.
func (s peerTLSServer) heartbeat(t *testing.T, key *ecdsa.PrivateKey, cert *x509.Certificate, id string) {
	t.Helper()
	_, err := s.workerClient(t, cert.Raw, key).Heartbeat(t.Context(), &coordinatorv1.HeartbeatRequest{WorkerId: id, Host: enrollHost})
	require.NoError(t, err)
}

// removeAs removes the certificate cert naming the worker id with runs.
func (s peerTLSServer) removeAs(t *testing.T, id string, cert *x509.Certificate, runs coordinatorv1.RemoveWorkerRuns) (*coordinatorv1.RemoveWorkerResponse, error) {
	t.Helper()
	return s.tlsClient(t).RemoveWorker(callContext(t), &coordinatorv1.RemoveWorkerRequest{WorkerId: id, CertificateId: cert.Subject.CommonName, Runs: runs})
}

// A removal accepted under the worker's ID, then a heartbeat under a new
// ID and the revocation: a replay of the original pair on another
// coordinator answers removed, since the record keeps the IDs its
// heartbeats reported.
func TestRemoveWorkerReplayOnOtherCoordinatorAfterRename(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	other := server.another(t)
	key, cert, _ := server.enrolled(t)
	server.heartbeat(t, key, cert, enrollWorkerID)
	_, err := server.removeAs(t, enrollWorkerID, cert, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	require.NoError(t, err)
	server.heartbeat(t, key, cert, "renamed")
	_, err = server.shared.certs.SetState(t.Context(), cert.Subject.CommonName, persis.WorkerCertificateRevoked)
	require.NoError(t, err)

	resp, err := other.removeAs(t, enrollWorkerID, cert, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	require.NoError(t, err)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED, resp.State)
}

// A stop under the worker's new ID upgrades a finish removal without
// changing the holder the removal named, and its immediate replay pairs
// with the removal it left.
func TestRemoveWorkerStopReplayAfterRename(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)
	server.heartbeat(t, key, cert, enrollWorkerID)
	_, err := server.removeAs(t, enrollWorkerID, cert, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	require.NoError(t, err)
	server.heartbeat(t, key, cert, "renamed")

	for range 2 {
		resp, err := server.removeAs(t, "renamed", cert, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_STOP)
		require.NoError(t, err)
		assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING, resp.State)
		assert.Equal(t, enrollWorkerID, resp.WorkerId)
	}
	record, err := server.shared.certs.Get(t.Context(), cert.Subject.CommonName)
	require.NoError(t, err)
	assert.False(t, record.StopRunsAt.IsZero(), "the stop moved the removal to stop")
	assert.Equal(t, enrollWorkerID, record.HolderID)
}

// A removal through the record's earlier ID names the holder that
// connects under a new ID. An identical retry, while removing and after
// the revocation, pairs with it and names that holder again, as the audit
// does.
func TestRemoveWorkerReplayAfterRename(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)
	server.renameHolder(t, key, cert)
	for _, want := range []coordinatorv1.WorkerRemovalState{
		coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING,
		coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING,
	} {
		resp, err := server.removeCertificate(t, cert.Subject.CommonName)
		require.NoError(t, err)
		assert.Equal(t, want, resp.State)
		assert.Equal(t, "renamed", resp.WorkerId)
	}
	_, err := server.shared.certs.SetState(t.Context(), cert.Subject.CommonName, persis.WorkerCertificateRevoked)
	require.NoError(t, err)

	resp, err := server.removeCertificate(t, cert.Subject.CommonName)
	require.NoError(t, err)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED, resp.State)
	assert.Equal(t, "renamed", resp.WorkerId, "the answer names the holder the removal recorded, not the replay's ID")
}

// A removal, then two renames of its holder: a replay under each ID the
// worker used pairs with the removal, before and after the revocation,
// and the answer names the worker the removal recorded. An ID that only
// the certificate's earlier heartbeats reported still counts as a known
// worker when it names another certificate.
func TestRemoveWorkerReplayAfterTwoRenames(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)
	_, otherCert := server.enroll(t, server.createInvite(t, &coordinatorv1.CreateInviteRequest{}).Invite)
	server.heartbeat(t, key, cert, enrollWorkerID)
	_, err := server.removeAs(t, enrollWorkerID, cert, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	require.NoError(t, err)
	server.heartbeat(t, key, cert, "renamed")
	server.heartbeat(t, key, cert, "renamed-again")

	ids := []string{enrollWorkerID, "renamed", "renamed-again"}
	for _, id := range ids {
		resp, err := server.removeAs(t, id, cert, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
		require.NoError(t, err, id)
		assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING, resp.State, id)
		assert.Equal(t, enrollWorkerID, resp.WorkerId, id)
	}
	_, err = server.shared.certs.SetState(t.Context(), cert.Subject.CommonName, persis.WorkerCertificateRevoked)
	require.NoError(t, err)
	for _, id := range ids {
		resp, err := server.removeAs(t, id, cert, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
		require.NoError(t, err, id)
		assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED, resp.State, id)
		assert.Equal(t, enrollWorkerID, resp.WorkerId, "%s: the holder stays as the removal named it", id)
	}

	// With no heartbeat row left, only the certificate record knows the
	// earlier ID.
	_, err = server.shared.heartbeats.DeleteStale(t.Context(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = server.removeAs(t, "renamed", otherCert, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "%v", err)
}

// A request that does not pair a certificate in removal with its worker
// changes nothing: a stop from another worker ID does not move a finish
// removal to stop.
func TestRemoveWorkerMismatchKeepsFinish(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)
	_, err := server.workerClient(t, cert.Raw, key).Heartbeat(t.Context(), &coordinatorv1.HeartbeatRequest{WorkerId: enrollWorkerID, Host: enrollHost})
	require.NoError(t, err)
	_, err = server.removeCertificate(t, cert.Subject.CommonName)
	require.NoError(t, err)
	_, err = server.tlsClient(t).Heartbeat(callContext(t), &coordinatorv1.HeartbeatRequest{WorkerId: "token-worker"})
	require.NoError(t, err)

	for id, want := range map[string]codes.Code{"token-worker": codes.FailedPrecondition, "nobody": codes.NotFound} {
		_, err = server.tlsClient(t).RemoveWorker(callContext(t), &coordinatorv1.RemoveWorkerRequest{
			WorkerId: id, CertificateId: cert.Subject.CommonName, Runs: coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_STOP,
		})
		assert.Equal(t, want, status.Code(err), "%s: %v", id, err)
	}
	record, err := server.shared.certs.Get(t.Context(), cert.Subject.CommonName)
	require.NoError(t, err)
	assert.True(t, record.StopRunsAt.IsZero(), "the removal still lets the runs finish")
}

// The workers list leaves out the heartbeat row of a revoked certificate,
// which stays until stale cleanup, and still lists a removing one.
func TestGetWorkersLeavesOutRevoked(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)
	server.heartbeat(t, key, cert, enrollWorkerID)
	_, err := server.removeCertificate(t, cert.Subject.CommonName)
	require.NoError(t, err)
	assert.True(t, server.listed(t, enrollWorkerID).Removing)

	_, err = server.shared.certs.SetState(t.Context(), cert.Subject.CommonName, persis.WorkerCertificateRevoked)
	require.NoError(t, err)
	coordinator.RereadRemovalsForTest(server.handler)
	resp, err := server.tlsClient(t).GetWorkers(callContext(t), &coordinatorv1.GetWorkersRequest{})
	require.NoError(t, err)
	for _, worker := range resp.Workers {
		assert.NotEqual(t, enrollWorkerID, worker.WorkerId, "a revoked certificate's worker is left out")
	}
}
