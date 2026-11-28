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

// enrollConfig is the configuration of a client with invite.
func enrollConfig(t *testing.T, invite string) *coordinator.Config {
	t.Helper()
	parsed, ok := coordinator.ParseInvite(invite)
	require.True(t, ok)
	config := coordinator.DefaultConfig()
	config.DialTimeout = 5 * time.Second
	return config.WithInvite(parsed)
}

// A worker enrolls with a coordinator whose CA has the invite's pin, and
// gets a certificate from that CA. The single-use invite then enrolls no
// other worker (Spec 108 §Worker Side).
func TestEnrollClient(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})
	req := enrollRequest(t, created.Invite, newWorkerKey(t))

	resp, err := coordinator.Enroll(t.Context(), enrollConfig(t, created.Invite), server.addr, req)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(resp.Certificate)
	require.NoError(t, err)
	assert.NoError(t, cert.CheckSignatureFrom(server.caCert))
	assert.Equal(t, server.caCert.Raw, resp.CaCertificate)
	assert.Equal(t, 1, server.inviteUses(t, created.Record.Id))
	invite, ok := coordinator.ParseInvite(created.Invite)
	require.True(t, ok)
	assert.True(t, invite.Names(server.caCert))

	_, err = coordinator.Enroll(t.Context(), enrollConfig(t, created.Invite), server.addr, enrollRequest(t, created.Invite, newWorkerKey(t)))
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
	ownAddr := startOwnTLSServer(t, "coordinator.test")
	// Own certificates may name the built-in name without a CA behind them.
	ownBuiltinNameAddr := startOwnTLSServer(t, "coordinator.dagu.invalid")

	for _, tc := range []struct {
		name    string
		addr    string
		wantErr error
	}{
		{name: "OtherDagu", addr: other.addr, wantErr: coordinator.ErrInviteCAMismatch},
		{name: "H2COnly", addr: h2cAddr, wantErr: coordinator.ErrNoBuiltinTLS},
		{name: "OwnCertificates", addr: ownAddr, wantErr: coordinator.ErrOwnCertificates},
		{name: "OwnCertificatesWithBuiltinName", addr: ownBuiltinNameAddr, wantErr: coordinator.ErrOwnCertificates},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := coordinator.Enroll(t.Context(), enrollConfig(t, invite), tc.addr, enrollRequest(t, invite, newWorkerKey(t)))
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
	t.Run("Unreachable", func(t *testing.T) {
		t.Parallel()
		_, err := coordinator.Enroll(t.Context(), enrollConfig(t, invite), fmt.Sprintf("127.0.0.1:%d", closedPort(t)), enrollRequest(t, invite, newWorkerKey(t)))
		assert.Equal(t, codes.Unavailable, status.Code(err), "%v", err)
		assert.NotErrorIs(t, err, coordinator.ErrNoBuiltinTLS)
	})
}

// startOwnTLSServer serves the coordinator with an own certificate for
// name, and returns its address.
func startOwnTLSServer(t *testing.T, name string) string {
	t.Helper()
	certFile, keyFile := peercert.Write(t, t.TempDir(), peercert.Options{Name: "server", DNSNames: []string{name}})
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)
	server, addr := startMockServerWithHealth(t, coordinator.NewHandler(coordinator.HandlerConfig{}), health.NewServer(),
		grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13})))
	t.Cleanup(server.Stop)
	return addr
}

// An invite has one canonical spelling, which its digest identifies.
func TestInviteDigest(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	first := server.createInvite(t, &coordinatorv1.CreateInviteRequest{}).Invite
	second := server.createInvite(t, &coordinatorv1.CreateInviteRequest{}).Invite
	parsed, ok := coordinator.ParseInvite(first)
	require.True(t, ok)
	other, ok := coordinator.ParseInvite(second)
	require.True(t, ok)

	assert.Equal(t, first, parsed.String())
	assert.NotEqual(t, parsed.Digest(), other.Digest())
	assert.NotContains(t, parsed.Digest(), inviteSecret(first))
	// The last character of a secret carries four unused bits, which a
	// canonical spelling leaves zero. Setting one spells the same secret
	// another way, which is refused.
	last := first[len(first)-1]
	_, ok = coordinator.ParseInvite(first[:len(first)-1] + string(last^1))
	assert.False(t, ok)
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
