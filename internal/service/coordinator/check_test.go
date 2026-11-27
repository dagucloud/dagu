// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/test/peercert"
)

func TestCheckH2C(t *testing.T) {
	t.Parallel()

	addr := startCheckServer(t)
	config := coordinator.DefaultConfig()
	config.Token = "client-token"

	result := coordinator.Check(t.Context(), config, checkMember(t, addr))

	assert.True(t, result.Passed(), "%+v", result)
	assert.Nil(t, result.TLS)
	assert.Equal(t, coordinator.PeerTokenConfigured, result.Credential.PeerToken)
}

func TestCheckUnreachable(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	result := coordinator.Check(t.Context(), coordinator.DefaultConfig(), checkMember(t, addr))

	assert.False(t, result.Passed())
	assert.Error(t, result.DialErr)
	assert.Equal(t, addr, result.Address)
}

func TestCheckTokenRefused(t *testing.T) {
	t.Parallel()

	auth := coordinator.NewPeerTokenAuth("coordinator-token", true, true)
	addr := startCheckServer(t, grpc.ChainUnaryInterceptor(auth.UnaryInterceptor()))
	config := coordinator.DefaultConfig()
	config.Token = "other-token"

	result := coordinator.Check(t.Context(), config, checkMember(t, addr))

	assert.False(t, result.Passed())
	assert.ErrorContains(t, result.AnswerErr, "refused the call, the peer token does not match")
}

// An h2c client on a port that serves only TLS learns that, instead of a
// transport error.
func TestCheckH2CToTLSOnly(t *testing.T) {
	t.Parallel()

	certFile, keyFile := peercert.Write(t, t.TempDir(), peercert.Options{Name: "server"})
	addr := startCheckServer(t, tlsCreds(t, certFile, keyFile))

	result := coordinator.Check(t.Context(), coordinator.DefaultConfig(), checkMember(t, addr))

	assert.False(t, result.Passed())
	assert.ErrorContains(t, result.AnswerErr, "serves only TLS on this port")
}

// A failed handshake names what the certificate covers and the CA it
// chains to, whichever check fails.
func TestCheckTLS(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	loopback := []net.IP{net.IPv4(127, 0, 0, 1)}
	named, namedKey := peercert.Write(t, dir, peercert.Options{Name: "named", DNSNames: []string{"coordinator.test"}})
	covering, coveringKey := peercert.Write(t, dir, peercert.Options{Name: "covering", IPAddresses: loopback})
	other, _ := peercert.Write(t, dir, peercert.Options{Name: "other", IPAddresses: loopback})

	for _, tc := range []struct {
		name      string
		certFile  string
		keyFile   string
		caFile    string
		wantErr   string
		wantNames []string
		wantCA    string
	}{
		{name: "Passes", certFile: covering, keyFile: coveringKey, caFile: covering,
			wantNames: []string{"127.0.0.1"}, wantCA: "covering"},
		{name: "NameNotCovered", certFile: named, keyFile: namedKey, caFile: named,
			wantErr: "the certificate does not cover 127.0.0.1", wantNames: []string{"coordinator.test"}, wantCA: "named"},
		{name: "OtherCA", certFile: covering, keyFile: coveringKey, caFile: other,
			wantErr: "the certificate does not chain to a CA this client trusts", wantNames: []string{"127.0.0.1"}, wantCA: "covering"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			addr := startCheckServer(t, tlsCreds(t, tc.certFile, tc.keyFile))
			config := coordinator.DefaultConfig()
			config.Insecure = false
			config.CAFile = tc.caFile

			result := coordinator.Check(t.Context(), config, checkMember(t, addr))

			require.NotNil(t, result.TLS)
			if tc.wantErr == "" {
				assert.True(t, result.Passed(), "%+v", result)
			} else {
				assert.False(t, result.Passed())
				assert.EqualError(t, result.TLS.Err, tc.wantErr)
			}
			require.NotNil(t, result.TLS.Certificate)
			assert.Equal(t, tc.wantNames, result.TLS.Certificate.Names)
			assert.Equal(t, tc.wantCA, result.TLS.Certificate.CA)
			assert.True(t, result.TLS.Certificate.SelfSigned)
		})
	}
}

// startCheckServer starts a coordinator that serves the health service and
// the worker list, and returns its address.
func startCheckServer(t *testing.T, opts ...grpc.ServerOption) string {
	t.Helper()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	server, addr := startMockServerWithHealth(t, coordinator.NewHandler(coordinator.HandlerConfig{}), healthServer, opts...)
	t.Cleanup(server.Stop)
	return addr
}

func tlsCreds(t *testing.T, certFile, keyFile string) grpc.ServerOption {
	t.Helper()
	creds, err := credentials.NewServerTLSFromFile(certFile, keyFile)
	require.NoError(t, err)
	return grpc.Creds(creds)
}

func checkMember(t *testing.T, addr string) serviceregistry.HostInfo {
	t.Helper()
	host, portText, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return serviceregistry.HostInfo{ID: addr, Host: host, Port: port}
}
