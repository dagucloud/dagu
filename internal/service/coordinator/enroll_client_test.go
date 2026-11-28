// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/test/peercert"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// enrollConfig is the client configuration Enroll dials with.
func enrollConfig() *coordinator.Config {
	config := coordinator.DefaultConfig()
	config.DialTimeout = 5 * time.Second
	return config
}

// A worker enrolls with a coordinator whose CA has the invite's pin, and
// gets a certificate from that CA. The single-use invite then enrolls no
// other worker (Spec 108 §Worker Side).
func TestEnrollClient(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})
	req := enrollRequest(t, created.Invite, newWorkerKey(t))

	resp, err := coordinator.Enroll(t.Context(), enrollConfig(), server.addr, req)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(resp.Certificate)
	require.NoError(t, err)
	assert.NoError(t, cert.CheckSignatureFrom(server.caCert))
	assert.Equal(t, server.caCert.Raw, resp.CaCertificate)
	assert.Equal(t, 1, server.inviteUses(t, created.Record.Id))
	pin, ok := coordinator.InvitePin(created.Invite)
	require.True(t, ok)
	assert.Equal(t, pin, coordinator.CAPin(server.caCert))

	_, err = coordinator.Enroll(t.Context(), enrollConfig(), server.addr, enrollRequest(t, created.Invite, newWorkerKey(t)))
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	assert.NotContains(t, err.Error(), inviteSecret(created.Invite))
}

// Enroll tells a coordinator of another Dagu, and one without built-in TLS,
// from a coordinator it cannot reach, so the worker gives the message of
// each (Spec 108 §Messages, §Errors).
func TestEnrollClientRefusesCoordinator(t *testing.T) {
	t.Parallel()
	invite := startPeerTLSServer(t, certHosts).createInvite(t, &coordinatorv1.CreateInviteRequest{Reusable: true}).Invite
	other := startPeerTLSServer(t, certHosts)
	h2cOnly, h2cAddr := startMockServerWithHealth(t, coordinator.NewHandler(coordinator.HandlerConfig{}), health.NewServer())
	t.Cleanup(h2cOnly.Stop)
	certFile, keyFile := peercert.Write(t, t.TempDir(), peercert.Options{Name: "server", DNSNames: []string{"coordinator.test"}})
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)
	own, ownAddr := startMockServerWithHealth(t, coordinator.NewHandler(coordinator.HandlerConfig{}), health.NewServer(),
		grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13})))
	t.Cleanup(own.Stop)

	for _, tc := range []struct {
		name    string
		addr    string
		wantErr error
	}{
		{name: "OtherDagu", addr: other.addr, wantErr: coordinator.ErrInviteCAMismatch},
		{name: "H2COnly", addr: h2cAddr, wantErr: coordinator.ErrNoBuiltinTLS},
		{name: "OwnCertificates", addr: ownAddr, wantErr: coordinator.ErrNoBuiltinTLS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := coordinator.Enroll(t.Context(), enrollConfig(), tc.addr, enrollRequest(t, invite, newWorkerKey(t)))
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
	t.Run("Unreachable", func(t *testing.T) {
		t.Parallel()
		_, err := coordinator.Enroll(t.Context(), enrollConfig(), fmt.Sprintf("127.0.0.1:%d", closedPort(t)), enrollRequest(t, invite, newWorkerKey(t)))
		assert.Equal(t, codes.Unavailable, status.Code(err), "%v", err)
		assert.NotErrorIs(t, err, coordinator.ErrNoBuiltinTLS)
	})
}

// closedPort returns a loopback port that nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}
