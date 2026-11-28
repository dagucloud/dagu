// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"net"
	"strconv"
	"testing"

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
