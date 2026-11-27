// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/test/peercert"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// A worker enrolls with a single-use invite over built-in TLS, and its
// certificate then counts as the peer token under
// coordinator.require_token, except for the invite calls. The invite enrolls
// no second worker, and enrollment over h2c is refused (Spec 108
// §Enrollment, §Coordinator Calls).
func TestEnrollment(t *testing.T) {
	dagu := harness.NewRunner(t)
	peer := newPeerFixture(t, dagu)
	addr := serveCoordinator(t, dagu, append(peer.env, "DAGU_COORDINATOR_REQUIRE_TOKEN=true"), servingBuiltinTLS)
	admin := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, addr, peer.builtinTLS()))
	tokenCtx := metadata.AppendToOutgoingContext(callContext(t), "dagu-peer-token", peer.token)

	created, err := admin.CreateInvite(tokenCtx, &coordinatorv1.CreateInviteRequest{}, grpc.WaitForReady(true))
	require.NoError(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	require.NoError(t, err)
	req := &coordinatorv1.EnrollRequest{Invite: created.Invite, Csr: csr, WorkerId: "worker-1", Host: "host-a", KeepsCertificate: true}

	h2c := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, addr, insecure.NewCredentials()))
	_, err = h2c.Enroll(callContext(t), req)
	assert.Equal(t, codes.Unauthenticated, status.Code(err), "%v", err)

	enrolled, err := admin.Enroll(callContext(t), req)
	require.NoError(t, err)
	_, err = admin.Enroll(callContext(t), req)
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)

	config := &tls.Config{
		RootCAs:      peer.roots,
		ServerName:   coordinatorCertName,
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{enrolled.Certificate}, PrivateKey: key}},
	}
	worker := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, addr, credentials.NewTLS(config)))
	_, err = worker.GetWorkers(callContext(t), &coordinatorv1.GetWorkersRequest{})
	assert.NoError(t, err)
	_, err = worker.ListInvites(callContext(t), &coordinatorv1.ListInvitesRequest{})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	_, err = admin.GetWorkers(callContext(t), &coordinatorv1.GetWorkersRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err), "%v", err)
}

// A coordinator with own certificates serves no invite calls and says why
// (Spec 108 §Coordinator Calls).
func TestInvitesWithOwnCertificates(t *testing.T) {
	dagu := harness.NewRunner(t)
	certFile, keyFile := peercert.Write(t, t.TempDir(), peercert.Options{Name: "server", DNSNames: []string{"coordinator.test"}})
	const token = "own-certificates-token"
	addr := serveCoordinator(t, dagu, []string{"DAGU_PEER_TOKEN=" + token}, "Coordinator transport: TLS (from peer.cert_file)",
		"--peer.cert-file="+certFile, "--peer.key-file="+keyFile)
	client := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, addr, credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS12,
		// The check is about the call, not the certificate.
		InsecureSkipVerify: true,
	})))

	ctx := metadata.AppendToOutgoingContext(callContext(t), "dagu-peer-token", token)
	_, err := client.CreateInvite(ctx, &coordinatorv1.CreateInviteRequest{}, grpc.WaitForReady(true))
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "%v", err)
	assert.Equal(t, "invites need the built-in CA: this coordinator uses peer.cert_file and peer.key_file", status.Convert(err).Message())
}
