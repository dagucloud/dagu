// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	invalidInviteMessage = "the invite is invalid, expired, or already used"
	enrollWorkerID       = "worker-1"
	enrollHost           = "host-a"
)

// tlsClient returns a client on built-in TLS without a client certificate.
func (s peerTLSServer) tlsClient(t *testing.T) coordinatorv1.CoordinatorServiceClient {
	t.Helper()
	client, _ := s.dial(t, credentials.NewTLS(s.tlsConfig()))
	return client
}

// workerClient returns a client on built-in TLS with the worker certificate
// cert for key.
func (s peerTLSServer) workerClient(t *testing.T, cert []byte, key *ecdsa.PrivateKey) coordinatorv1.CoordinatorServiceClient {
	t.Helper()
	config := s.tlsConfig()
	config.Certificates = []tls.Certificate{{Certificate: [][]byte{cert}, PrivateKey: key}}
	client, _ := s.dial(t, credentials.NewTLS(config))
	return client
}

// createInvite creates an invite with the peer token.
func (s peerTLSServer) createInvite(t *testing.T, req *coordinatorv1.CreateInviteRequest) *coordinatorv1.CreateInviteResponse {
	t.Helper()
	resp, err := s.tlsClient(t).CreateInvite(callContext(t), req)
	require.NoError(t, err)
	return resp
}

// enroll enrolls a new key with invite and returns the key and the issued
// certificate.
func (s peerTLSServer) enroll(t *testing.T, invite string) (*ecdsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	key := newWorkerKey(t)
	resp, err := s.tlsClient(t).Enroll(t.Context(), enrollRequest(t, invite, key))
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(resp.Certificate)
	require.NoError(t, err)
	return key, cert
}

// inviteUses returns the use count of the invite with id.
func (s peerTLSServer) inviteUses(t *testing.T, id string) int {
	t.Helper()
	invite, err := s.invites.Get(t.Context(), id)
	require.NoError(t, err)
	return invite.UseCount
}

func newWorkerKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	return key
}

// newCSR returns a certificate signing request for key that asks for names
// and extensions a worker certificate never carries.
func newCSR(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "coordinator.dagu.invalid"},
		DNSNames: []string{"coordinator.dagu.invalid"},
	}, key)
	require.NoError(t, err)
	return der
}

// enrollRequest is the request of a worker that keeps its certificate.
func enrollRequest(t *testing.T, invite string, key *ecdsa.PrivateKey) *coordinatorv1.EnrollRequest {
	t.Helper()
	return &coordinatorv1.EnrollRequest{
		Invite:           invite,
		Csr:              newCSR(t, key),
		WorkerId:         enrollWorkerID,
		Host:             enrollHost,
		KeepsCertificate: true,
	}
}

// inviteSecret returns the secret part of invite.
func inviteSecret(invite string) string {
	return invite[strings.LastIndex(invite, ".")+1:]
}

// The invite calls and join readiness take the peer token and refuse a
// worker certificate, with or without the token (Spec 108 §Coordinator
// Calls).
func TestInviteCallsAuth(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	invite := server.createInvite(t, &coordinatorv1.CreateInviteRequest{Reusable: true})
	key, cert := server.enroll(t, invite.Invite)

	calls := map[string]func(context.Context, coordinatorv1.CoordinatorServiceClient) error{
		"CreateInvite": func(ctx context.Context, c coordinatorv1.CoordinatorServiceClient) error {
			_, err := c.CreateInvite(ctx, &coordinatorv1.CreateInviteRequest{})
			return err
		},
		"ListInvites": func(ctx context.Context, c coordinatorv1.CoordinatorServiceClient) error {
			_, err := c.ListInvites(ctx, &coordinatorv1.ListInvitesRequest{})
			return err
		},
		"DeleteInvite": func(ctx context.Context, c coordinatorv1.CoordinatorServiceClient) error {
			_, err := c.DeleteInvite(ctx, &coordinatorv1.DeleteInviteRequest{Id: "missing"})
			// NotFound means the call passed the credential check.
			if status.Code(err) == codes.NotFound {
				return nil
			}
			return err
		},
		"GetJoinReadiness": func(ctx context.Context, c coordinatorv1.CoordinatorServiceClient) error {
			_, err := c.GetJoinReadiness(ctx, &coordinatorv1.GetJoinReadinessRequest{})
			return err
		},
	}
	tlsClient := server.tlsClient(t)
	workerClient := server.workerClient(t, cert.Raw, key)
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, call(callContext(t), tlsClient), "peer token")
			assert.Equal(t, codes.Unauthenticated, status.Code(call(t.Context(), tlsClient)), "no credential")
			assert.Equal(t, codes.PermissionDenied, status.Code(call(t.Context(), workerClient)), "worker certificate")
			assert.Equal(t, codes.PermissionDenied, status.Code(call(callContext(t), workerClient)), "worker certificate and peer token")
		})
	}
}

// An invite is dagu1.<pin>.<secret>, and a kind without an expiry gets its
// default one (Spec 108 §Invites).
func TestCreateInvite(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	pin := sha256.Sum256(server.caCert.Raw)

	for _, tc := range []struct {
		name       string
		req        *coordinatorv1.CreateInviteRequest
		wantKind   coordinatorv1.InviteKind
		wantExpiry time.Duration
		wantBy     string
	}{
		{
			name:       "SingleUse",
			req:        &coordinatorv1.CreateInviteRequest{Address: "192.168.1.20:50055"},
			wantKind:   coordinatorv1.InviteKind_INVITE_KIND_SINGLE_USE,
			wantExpiry: 24 * time.Hour,
			wantBy:     "peer token",
		},
		{
			name:     "Reusable",
			req:      &coordinatorv1.CreateInviteRequest{Reusable: true, CreatedBy: "admin"},
			wantKind: coordinatorv1.InviteKind_INVITE_KIND_REUSABLE,
			wantBy:   "admin",
		},
		{
			name:       "ReusableWithExpiry",
			req:        &coordinatorv1.CreateInviteRequest{Reusable: true, ExpiresInSeconds: 3600},
			wantKind:   coordinatorv1.InviteKind_INVITE_KIND_REUSABLE,
			wantExpiry: time.Hour,
			wantBy:     "peer token",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := server.createInvite(t, tc.req)
			parts := strings.Split(resp.Invite, ".")
			require.Len(t, parts, 3)
			assert.Equal(t, "dagu1", parts[0])
			assert.Equal(t, base64.RawURLEncoding.EncodeToString(pin[:]), parts[1])
			secret, err := base64.RawURLEncoding.DecodeString(parts[2])
			require.NoError(t, err)
			assert.Len(t, secret, 32)

			record := resp.Record
			assert.NotEmpty(t, record.Id)
			assert.Equal(t, tc.wantKind, record.Kind)
			assert.Equal(t, tc.req.Address, record.Address)
			assert.Equal(t, tc.wantBy, record.CreatedBy)
			assert.Zero(t, record.UseCount)
			if tc.wantExpiry == 0 {
				assert.Zero(t, record.ExpiresAt)
			} else {
				assert.Equal(t, int64(tc.wantExpiry/time.Second), record.ExpiresAt-record.CreatedAt)
			}
		})
	}
}

func TestCreateInviteInvalid(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)

	for name, req := range map[string]*coordinatorv1.CreateInviteRequest{
		"NegativeExpiry": {ExpiresInSeconds: -1},
		"NoPort":         {Address: "192.168.1.20"},
		"PortZero":       {Address: "192.168.1.20:0"},
		"NoHost":         {Address: ":50055"},
	} {
		_, err := server.tlsClient(t).CreateInvite(callContext(t), req)
		assert.Equal(t, codes.InvalidArgument, status.Code(err), name)
	}
}

// Only the create response holds an invite: no listing, error, or log line
// does (Spec 108 §Secrets in Output).
func TestInviteSecretOnlyInCreateResponse(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})
	secret := inviteSecret(created.Invite)
	server.enroll(t, created.Invite)
	_, enrollErr := server.tlsClient(t).Enroll(t.Context(), enrollRequest(t, created.Invite, newWorkerKey(t)))
	require.Error(t, enrollErr)
	// An invite pasted in place of its ID.
	_, deleteErr := server.tlsClient(t).DeleteInvite(callContext(t), &coordinatorv1.DeleteInviteRequest{Id: created.Invite})
	assert.Equal(t, codes.NotFound, status.Code(deleteErr))

	listed, err := server.tlsClient(t).ListInvites(callContext(t), &coordinatorv1.ListInvitesRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Invites, 1)
	assert.Equal(t, created.Record.Id, listed.Invites[0].Id)
	assert.Equal(t, int32(1), listed.Invites[0].UseCount)
	assert.NotContains(t, listed.String(), secret)
	assert.NotContains(t, enrollErr.Error(), secret)
	assert.NotContains(t, deleteErr.Error(), secret)
	logs := server.logs.String()
	assert.Contains(t, logs, created.Record.Id)
	assert.NotContains(t, logs, secret)
	assert.NotContains(t, logs, peerTLSToken)
}

// A deleted invite enrolls no worker, and workers that enrolled with it keep
// their certificates (Spec 108 §Kinds).
func TestDeleteInvite(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{Reusable: true})
	key, cert := server.enroll(t, created.Invite)
	client := server.tlsClient(t)

	_, err := client.DeleteInvite(callContext(t), &coordinatorv1.DeleteInviteRequest{Id: created.Record.Id})
	require.NoError(t, err)
	listed, err := client.ListInvites(callContext(t), &coordinatorv1.ListInvitesRequest{})
	require.NoError(t, err)
	assert.Empty(t, listed.Invites)
	_, err = client.Enroll(t.Context(), enrollRequest(t, created.Invite, newWorkerKey(t)))
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = client.DeleteInvite(callContext(t), &coordinatorv1.DeleteInviteRequest{Id: created.Record.Id})
	assert.Equal(t, codes.NotFound, status.Code(err))

	_, err = server.workerClient(t, cert.Raw, key).GetWorkers(t.Context(), &coordinatorv1.GetWorkersRequest{})
	assert.NoError(t, err)
}

// The worker certificate names a random certificate ID and the invite,
// allows client authentication only, has no expiry date, and takes only the
// key from the request. Its record is active (Spec 108 §Worker Certificate,
// §Certificate Records).
func TestEnroll(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})
	key := newWorkerKey(t)

	resp, err := server.tlsClient(t).Enroll(t.Context(), enrollRequest(t, created.Invite, key))
	require.NoError(t, err)
	assert.Equal(t, server.caCert.Raw, resp.CaCertificate)
	cert, err := x509.ParseCertificate(resp.Certificate)
	require.NoError(t, err)

	roots := x509.NewCertPool()
	roots.AddCert(server.caCert)
	_, err = cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	require.NoError(t, err)
	assert.True(t, key.PublicKey.Equal(cert.PublicKey))
	assert.Equal(t, time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC), cert.NotAfter)
	assert.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, cert.ExtKeyUsage)
	assert.False(t, cert.IsCA)
	assert.Empty(t, cert.DNSNames)
	require.Len(t, cert.URIs, 1)
	assert.Equal(t, "urn:dagu:invite:"+created.Record.Id, cert.URIs[0].String())
	certID := cert.Subject.CommonName
	assert.NotEqual(t, "coordinator.dagu.invalid", certID)

	record, err := server.certs.Get(t.Context(), certID)
	require.NoError(t, err)
	assert.Equal(t, persis.WorkerCertificateActive, record.State)
	assert.Equal(t, created.Record.Id, record.InviteID)
	assert.Equal(t, enrollWorkerID, record.WorkerID)
	assert.Equal(t, enrollHost, record.Host)
	fingerprint := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	assert.Equal(t, fingerprint[:], record.KeyFingerprint)

	invite, err := server.invites.Get(t.Context(), created.Record.Id)
	require.NoError(t, err)
	assert.Equal(t, 1, invite.UseCount)
	assert.False(t, invite.LastUsedAt.IsZero())

	// A worker that lost the response retries with the used invite and is
	// refused like any used invite.
	_, err = server.tlsClient(t).Enroll(t.Context(), enrollRequest(t, created.Invite, key))
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Equal(t, invalidInviteMessage, status.Convert(err).Message())
}

// An invite that is malformed, unknown, for another CA, or ended enrolls no
// worker, and the refusal does not say which.
func TestEnrollRefusesInvite(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})
	pin, _, _ := strings.Cut(strings.TrimPrefix(created.Invite, "dagu1."), ".")
	otherSecret := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	otherPin := base64.RawURLEncoding.EncodeToString(make([]byte, 32))

	expiredSecret := make([]byte, 32)
	_, err := rand.Read(expiredSecret)
	require.NoError(t, err)
	expiredHash := sha256.Sum256(expiredSecret)
	_, err = server.invites.Create(t.Context(), persis.WorkerInvite{
		SecretHash: expiredHash[:],
		Kind:       persis.WorkerInviteReusable,
		ExpiresAt:  time.Now().Add(-time.Minute),
		CreatedBy:  "peer token",
		CreatedAt:  time.Now().Add(-time.Hour),
	})
	require.NoError(t, err)

	for name, invite := range map[string]string{
		"WrongSecret": "dagu1." + pin + "." + otherSecret,
		"OtherCA":     "dagu1." + otherPin + "." + inviteSecret(created.Invite),
		"Malformed":   "dagu1." + pin,
		"Oversized":   "dagu1." + strings.Repeat(".", 1<<20),
		"Expired":     "dagu1." + pin + "." + base64.RawURLEncoding.EncodeToString(expiredSecret),
	} {
		_, err := server.tlsClient(t).Enroll(t.Context(), enrollRequest(t, invite, newWorkerKey(t)))
		assert.Equal(t, codes.PermissionDenied, status.Code(err), name)
		assert.Equal(t, invalidInviteMessage, status.Convert(err).Message(), name)
	}
	assert.Zero(t, server.inviteUses(t, created.Record.Id))
}

// Of concurrent enrollments with one single-use invite, exactly one
// succeeds (Spec 108 §Invite Records).
func TestEnrollSingleUseOnce(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})
	client := server.tlsClient(t)

	const workers = 8
	reqs := make([]*coordinatorv1.EnrollRequest, workers)
	for i := range reqs {
		reqs[i] = enrollRequest(t, created.Invite, newWorkerKey(t))
	}
	codesSeen := make([]codes.Code, workers)
	var wg sync.WaitGroup
	for i, req := range reqs {
		wg.Go(func() {
			_, err := client.Enroll(t.Context(), req)
			codesSeen[i] = status.Code(err)
		})
	}
	wg.Wait()

	var enrolled int
	for _, code := range codesSeen {
		if code == codes.OK {
			enrolled++
		} else {
			assert.Equal(t, codes.PermissionDenied, code)
		}
	}
	assert.Equal(t, 1, enrolled)
	assert.Equal(t, 1, server.inviteUses(t, created.Record.Id))
	records, err := server.certs.List(t.Context())
	require.NoError(t, err)
	assert.Len(t, records, 1)
}

// A reusable invite enrolls each worker with a new certificate and counts
// its uses.
func TestEnrollReusable(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{Reusable: true})

	ids := map[string]bool{}
	for range 3 {
		_, cert := server.enroll(t, created.Invite)
		ids[cert.Subject.CommonName] = true
	}
	assert.Len(t, ids, 3)
	assert.Equal(t, 3, server.inviteUses(t, created.Record.Id))
}

// A worker that cannot keep its certificate would use up a single-use
// invite at each start, so it is refused without consuming the invite. A
// reusable invite enrolls it.
func TestEnrollSingleUseNeedsKeptCertificate(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	singleUse := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})
	reusable := server.createInvite(t, &coordinatorv1.CreateInviteRequest{Reusable: true})
	client := server.tlsClient(t)

	req := enrollRequest(t, singleUse.Invite, newWorkerKey(t))
	req.KeepsCertificate = false
	_, err := client.Enroll(t.Context(), req)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Zero(t, server.inviteUses(t, singleUse.Record.Id))

	req.Invite = reusable.Invite
	_, err = client.Enroll(t.Context(), req)
	assert.NoError(t, err)
}

// A request whose CSR cannot be read, does not verify, or holds a key other
// than ECDSA P-256 is refused without consuming the invite.
func TestEnrollRefusesCSR(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})

	badSignature := newCSR(t, newWorkerKey(t))
	badSignature[len(badSignature)-1] ^= 0xff
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	for name, csr := range map[string][]byte{
		"Unreadable":   []byte("not a CSR"),
		"BadSignature": badSignature,
		"P384":         newCSR(t, p384),
		"RSA":          newCSR(t, rsaKey),
	} {
		req := enrollRequest(t, created.Invite, newWorkerKey(t))
		req.Csr = csr
		_, err := server.tlsClient(t).Enroll(t.Context(), req)
		assert.Equal(t, codes.InvalidArgument, status.Code(err), name)
	}
	assert.Zero(t, server.inviteUses(t, created.Record.Id))
}

// Enrollment over h2c is refused and consumes nothing, since the worker can
// check the coordinator against its pin only over TLS (Spec 108 §Errors).
func TestEnrollOverH2C(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})
	client, _ := server.dial(t, insecure.NewCredentials())

	_, err := client.Enroll(t.Context(), enrollRequest(t, created.Invite, newWorkerKey(t)))
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Equal(t, "enrollment needs TLS", status.Convert(err).Message())
	assert.Zero(t, server.inviteUses(t, created.Record.Id))
}

// An enrolled certificate counts as the peer token under
// coordinator.require_token (Spec 108 §One Port).
func TestWorkerCertificateRequireToken(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServerWithAuth(t, coordinator.NewPeerTokenAuth(peerTLSToken, true, true), certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})
	key, cert := server.enroll(t, created.Invite)

	_, err := server.workerClient(t, cert.Raw, key).GetWorkers(t.Context(), &coordinatorv1.GetWorkersRequest{})
	assert.NoError(t, err)
	_, err = server.tlsClient(t).GetWorkers(t.Context(), &coordinatorv1.GetWorkersRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// A coordinator without a peer CA refuses the invite calls and enrollment
// and says why (Spec 108 §Coordinator Calls).
func TestInvitesWithoutPeerCA(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		reason  coordinatorv1.BuiltinTLSReason
		message string
	}{
		{
			reason:  coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_OWN_CERTIFICATES,
			message: "invites need the built-in CA: this coordinator uses peer.cert_file and peer.key_file",
		},
		{
			reason:  coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_CA_UNAVAILABLE,
			message: "invites need the built-in CA: this coordinator could not create or read its CA; see its start warning",
		},
	} {
		handler := coordinator.NewHandler(coordinator.HandlerConfig{
			JoinReadiness: coordinator.JoinReadinessConfig{BuiltinTLSReason: tc.reason},
		})
		ctx := t.Context()
		_, createErr := handler.CreateInvite(ctx, &coordinatorv1.CreateInviteRequest{})
		_, listErr := handler.ListInvites(ctx, &coordinatorv1.ListInvitesRequest{})
		_, deleteErr := handler.DeleteInvite(ctx, &coordinatorv1.DeleteInviteRequest{Id: "id"})
		_, enrollErr := handler.Enroll(ctx, &coordinatorv1.EnrollRequest{})
		for _, err := range []error{createErr, listErr, deleteErr, enrollErr} {
			assert.Equal(t, codes.FailedPrecondition, status.Code(err))
			assert.Equal(t, tc.message, status.Convert(err).Message())
		}
	}
}
