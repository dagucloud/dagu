// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// workerCertConfig is the configuration of a worker that holds cert and key
// from the peer CA of server.
func workerCertConfig(server peerTLSServer, key *ecdsa.PrivateKey, cert *x509.Certificate) *coordinator.Config {
	config := coordinator.DefaultConfig()
	config.DialTimeout = 5 * time.Second
	return config.WithWorkerCertificate(tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}, server.caCert)
}

// A worker that holds a certificate asks the coordinator about it before it
// starts: the coordinator accepts it until the worker is removed, then
// refuses it as revoked, and a coordinator of another Dagu is told apart
// (Spec 108 §Worker Start, §Messages).
func TestConfirmWorkerCertificate(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	other := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)
	config := workerCertConfig(server, key, cert)

	require.NoError(t, coordinator.ConfirmWorkerCertificate(t.Context(), config, server.addr))
	assert.ErrorIs(t, coordinator.ConfirmWorkerCertificate(t.Context(), config, other.addr), coordinator.ErrOtherDagu)

	server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	err := coordinator.ConfirmWorkerCertificate(t.Context(), config, server.addr)
	assert.True(t, coordinator.IsWorkerRemoved(err), "%v", err)

	// A worker's heartbeat gets the same refusal through its client.
	registry, err := coordinator.NewStaticRegistry([]string{server.addr})
	require.NoError(t, err)
	client := coordinator.New(registry, config)
	t.Cleanup(func() { _ = client.Cleanup(t.Context()) })
	_, err = client.Heartbeat(t.Context(), &coordinatorv1.HeartbeatRequest{WorkerId: "worker-1"})
	assert.True(t, coordinator.IsWorkerRemoved(err), "%v", err)
}

// Only the coordinator's refusal of a revoked certificate counts as a
// removal, whatever wraps it.
func TestIsWorkerRemoved(t *testing.T) {
	t.Parallel()
	removed := status.Error(codes.Unauthenticated, "this worker was removed from the coordinator")
	assert.True(t, coordinator.IsWorkerRemoved(removed))
	assert.True(t, coordinator.IsWorkerRemoved(fmt.Errorf("heartbeat failed: %w", removed)))
	assert.False(t, coordinator.IsWorkerRemoved(status.Error(codes.PermissionDenied, "this worker was removed from the coordinator")))
	assert.False(t, coordinator.IsWorkerRemoved(status.Error(codes.Unauthenticated, "the peer token does not match the coordinator's peer token")))
	assert.False(t, coordinator.IsWorkerRemoved(fmt.Errorf("this worker was removed from the coordinator")))
	assert.False(t, coordinator.IsWorkerRemoved(nil))
}

// The check of a worker with a certificate names the certificate and its
// invite, and says whether the coordinator accepts it (Spec 108
// §dagu worker --check).
func TestCheckWorkerCertificate(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, invite := server.enrolled(t)
	config := workerCertConfig(server, key, cert)

	result := coordinator.Check(t.Context(), config, checkMember(t, server.addr))
	require.True(t, result.Passed(), "%+v", result)
	assert.Equal(t, "built-in TLS (from worker certificate)", result.Transport)
	assert.True(t, result.TLS.Verified)
	assert.True(t, result.Credential.CertSent)
	assert.Equal(t, cert.Subject.CommonName, result.Credential.CertificateID)
	assert.Equal(t, invite.Record.Id, result.Credential.InviteID)
	assert.Equal(t, coordinator.PeerTokenNone, result.Credential.PeerToken)

	server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	result = coordinator.Check(t.Context(), config, checkMember(t, server.addr))
	assert.False(t, result.Passed())
	assert.True(t, coordinator.IsWorkerRemoved(result.AnswerErr), "%v", result.AnswerErr)
}

// The check of a worker that enrolls verifies the coordinator against the
// invite's pin, and makes no call that needs a credential (Spec 108
// §dagu worker --check).
func TestCheckInvite(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	other := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})
	config := enrollConfig(t, created.Invite)

	result := coordinator.Check(t.Context(), config, checkMember(t, server.addr))
	require.True(t, result.Passed(), "%+v", result)
	assert.Equal(t, "built-in TLS (from worker.invite)", result.Transport)
	assert.True(t, result.TLS.Verified)
	assert.True(t, result.Credential.Invite)
	assert.Equal(t, coordinator.PeerTokenNone, result.Credential.PeerToken)
	assert.Equal(t, 0, server.inviteUses(t, created.Record.Id), "the check never enrolls")

	result = coordinator.Check(t.Context(), config, checkMember(t, other.addr))
	assert.False(t, result.Passed())
	require.NotNil(t, result.TLS)
	assert.EqualError(t, result.TLS.Err, "the coordinator belongs to a different Dagu: its CA does not match the invite")
	assert.Empty(t, result.TLS.Fallback, "an invite worker never falls back to h2c")
}
