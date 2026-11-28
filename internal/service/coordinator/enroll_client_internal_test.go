// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// replyingCoordinator answers Enroll with what reply returns for the
// request.
type replyingCoordinator struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
	reply func(*coordinatorv1.EnrollRequest) *coordinatorv1.EnrollResponse
}

func (c replyingCoordinator) Enroll(_ context.Context, req *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, error) {
	return c.reply(req), nil
}

// A reply that does not match the request or the invite's CA is refused,
// even from a coordinator that passed the pin check (Spec 108 §Worker
// Side).
func TestEnrollChecksReply(t *testing.T) {
	ca, other := newTestCA(t), newTestCA(t)
	invite, _, err := newInvite(ca.cert)
	require.NoError(t, err)
	parsed, ok := ParseInvite(invite)
	require.True(t, ok)

	csrKey := func(req *coordinatorv1.EnrollRequest) any {
		csr, err := x509.ParseCertificateRequest(req.Csr)
		require.NoError(t, err)
		return csr.PublicKey
	}
	issued := func(req *coordinatorv1.EnrollRequest) []byte {
		der, err := ca.issueWorkerCertificate(csrKey(req), "cert-1", "invite-1")
		require.NoError(t, err)
		return der
	}
	for _, tc := range []struct {
		name    string
		reply   func(*coordinatorv1.EnrollRequest) *coordinatorv1.EnrollResponse
		wantErr bool
	}{
		{name: "Valid", reply: func(req *coordinatorv1.EnrollRequest) *coordinatorv1.EnrollResponse {
			return &coordinatorv1.EnrollResponse{Certificate: issued(req), CaCertificate: ca.cert.Raw}
		}},
		{name: "OtherKey", wantErr: true, reply: func(*coordinatorv1.EnrollRequest) *coordinatorv1.EnrollResponse {
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			require.NoError(t, err)
			der, err := ca.issueWorkerCertificate(key.Public(), "cert-1", "invite-1")
			require.NoError(t, err)
			return &coordinatorv1.EnrollResponse{Certificate: der, CaCertificate: ca.cert.Raw}
		}},
		{name: "OtherCA", wantErr: true, reply: func(req *coordinatorv1.EnrollRequest) *coordinatorv1.EnrollResponse {
			der, err := other.issueWorkerCertificate(csrKey(req), "cert-1", "invite-1")
			require.NoError(t, err)
			return &coordinatorv1.EnrollResponse{Certificate: der, CaCertificate: other.cert.Raw}
		}},
		{name: "NotSignedByCA", wantErr: true, reply: func(req *coordinatorv1.EnrollRequest) *coordinatorv1.EnrollResponse {
			der, err := other.issueWorkerCertificate(csrKey(req), "cert-1", "invite-1")
			require.NoError(t, err)
			return &coordinatorv1.EnrollResponse{Certificate: der, CaCertificate: ca.cert.Raw}
		}},
		{name: "ServerAuth", wantErr: true, reply: func(req *coordinatorv1.EnrollRequest) *coordinatorv1.EnrollResponse {
			return &coordinatorv1.EnrollResponse{Certificate: signedBy(t, ca, csrKey(req), &x509.Certificate{
				Subject:     pkix.Name{CommonName: "cert-1"},
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			}), CaCertificate: ca.cert.Raw}
		}},
		{name: "NoInviteID", wantErr: true, reply: func(req *coordinatorv1.EnrollRequest) *coordinatorv1.EnrollResponse {
			return &coordinatorv1.EnrollResponse{Certificate: signedBy(t, ca, csrKey(req), &x509.Certificate{
				Subject:     pkix.Name{CommonName: "cert-1"},
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			}), CaCertificate: ca.cert.Raw}
		}},
		{name: "Unreadable", wantErr: true, reply: func(*coordinatorv1.EnrollRequest) *coordinatorv1.EnrollResponse {
			return &coordinatorv1.EnrollResponse{Certificate: []byte("garbage"), CaCertificate: ca.cert.Raw}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr := serveReplyingCoordinator(t, ca, tc.reply)
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			require.NoError(t, err)
			csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
			require.NoError(t, err)
			config := DefaultConfig()
			config.DialTimeout = 5 * time.Second
			_, err = Enroll(t.Context(), config.WithInvite(parsed), addr, &coordinatorv1.EnrollRequest{Csr: csr})
			if tc.wantErr {
				assert.ErrorIs(t, err, ErrInvalidEnrollReply)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func serveReplyingCoordinator(t *testing.T, ca *PeerCA, reply func(*coordinatorv1.EnrollRequest) *coordinatorv1.EnrollResponse) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer(grpc.Creds(builtinCredentials(t, ca)))
	coordinatorv1.RegisterCoordinatorServiceServer(server, replyingCoordinator{reply: reply})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

// ownChainCredentials serve TLS with own certificates that name
// coordinator.dagu.invalid and expire, sent with the CA that signed them.
func ownChainCredentials(t *testing.T) credentials.TransportCredentials {
	t.Helper()
	ca := newTestCA(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leaf := signedBy(t, ca, key.Public(), &x509.Certificate{
		Subject:     pkix.Name{CommonName: coordinatorCertName},
		DNSNames:    []string{coordinatorCertName},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{leaf, ca.cert.Raw}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS13,
	})
}

// signedBy returns a DER certificate from template for key, signed by ca.
func signedBy(t *testing.T, ca *PeerCA, key any, template *x509.Certificate) []byte {
	t.Helper()
	template.SerialNumber = big.NewInt(time.Now().UnixNano())
	template.NotBefore = time.Now().Add(-time.Hour)
	template.NotAfter = time.Now().Add(time.Hour)
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, key, ca.key)
	require.NoError(t, err)
	return der
}

// A coordinator that answered decides the outcome over a handshake that an
// earlier connection attempt failed, as when an address resolves to more
// than one coordinator.
func TestEnrollErrorOrder(t *testing.T) {
	refused := status.Error(codes.PermissionDenied, invalidInviteMessage)
	unavailable := status.Error(codes.Unavailable, "connection error")
	for _, tc := range []struct {
		name      string
		err       error
		handshake error
		want      error
	}{
		{name: "RefusalAfterOtherCA", err: refused, handshake: ErrInviteCAMismatch, want: refused},
		{name: "RefusalAfterOwnCertificates", err: refused, handshake: errNotBuiltinCertificate, want: refused},
		{name: "OtherCA", err: unavailable, handshake: ErrInviteCAMismatch, want: ErrInviteCAMismatch},
		{name: "OwnCertificates", err: unavailable, handshake: errNotBuiltinCertificate, want: ErrOwnCertificates},
		{name: "Unreachable", err: unavailable, want: unavailable},
		{name: "OlderCoordinator", err: status.Error(codes.Unimplemented, "unknown method"), want: ErrNoBuiltinTLS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.ErrorIs(t, enrollError(tc.err, tc.handshake), tc.want)
		})
	}
}

// Own certificates that name coordinator.dagu.invalid and come with the CA
// that signed them are not one of another Dagu: the worker counts the
// coordinator as unavailable instead of stopping (Spec 108 §Worker Side).
func TestEnrollOwnChainWithBuiltinName(t *testing.T) {
	invite, _, err := newInvite(newTestCA(t).cert)
	require.NoError(t, err)
	parsed, ok := ParseInvite(invite)
	require.True(t, ok)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer(grpc.Creds(ownChainCredentials(t)))
	coordinatorv1.RegisterCoordinatorServiceServer(server, coordinatorv1.UnimplementedCoordinatorServiceServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	config := DefaultConfig()
	config.DialTimeout = 5 * time.Second
	_, err = Enroll(t.Context(), config.WithInvite(parsed), listener.Addr().String(), &coordinatorv1.EnrollRequest{})
	assert.ErrorIs(t, err, ErrOwnCertificates)
}
