// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/test/peercert"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	peerTLSToken        = "peer-tls-token"
	coordinatorCertName = "coordinator.dagu.invalid"
	requireTLSMessage   = "the coordinator requires TLS: upgrade this process, or remove peer.insecure: true"
)

// peerTLSServer is a coordinator that serves built-in TLS and h2c on addr.
type peerTLSServer struct {
	addr   string
	caCert *x509.Certificate
	caKey  crypto.Signer
}

// startPeerTLSServer serves join readiness and the health service with the
// peer transport credentials of a new peer CA, behind the peer token.
func startPeerTLSServer(t *testing.T, opts ...grpc.ServerOption) peerTLSServer {
	t.Helper()
	files, err := coordinator.WritePeerInit(filepath.Join(t.TempDir(), "peer"))
	require.NoError(t, err)
	ca, err := coordinator.LoadPeerCA(files[0], files[1])
	require.NoError(t, err)
	creds, err := coordinator.NewPeerTransportCredentials(ca, "127.0.0.1", "dagu.example.com", "")
	require.NoError(t, err)
	pair, err := tls.LoadX509KeyPair(files[0], files[1])
	require.NoError(t, err)

	auth := coordinator.NewPeerTokenAuth(peerTLSToken, true, false)
	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		JoinReadiness: coordinator.JoinReadinessConfig{ListenHost: "0.0.0.0", Port: 50055, BuiltinTLS: true},
	})
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	opts = append([]grpc.ServerOption{grpc.Creds(creds)}, opts...)
	opts = append(opts,
		grpc.ChainUnaryInterceptor(auth.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(auth.StreamInterceptor()),
	)
	server, addr := startMockServerWithHealth(t, handler, healthServer, opts...)
	t.Cleanup(server.Stop)
	return peerTLSServer{addr: addr, caCert: pair.Leaf, caKey: pair.PrivateKey.(crypto.Signer)}
}

// tlsConfig verifies the coordinator as a client on built-in TLS does.
func (s peerTLSServer) tlsConfig() *tls.Config {
	roots := x509.NewCertPool()
	roots.AddCert(s.caCert)
	return &tls.Config{RootCAs: roots, ServerName: coordinatorCertName, MinVersion: tls.VersionTLS13}
}

// dial returns a coordinator client and a health client on one connection.
func (s peerTLSServer) dial(t *testing.T, creds credentials.TransportCredentials) (coordinatorv1.CoordinatorServiceClient, grpc_health_v1.HealthClient) {
	t.Helper()
	conn, err := grpc.NewClient(s.addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return coordinatorv1.NewCoordinatorServiceClient(conn), grpc_health_v1.NewHealthClient(conn)
}

// readinessClient returns the client of today's h2c peers for the server.
func (s peerTLSServer) readinessClient(t *testing.T) coordinator.JoinReadinessClient {
	t.Helper()
	host, port := parseHostPort(s.addr)
	monitor := &mockServiceMonitor{members: []serviceregistry.HostInfo{
		{ID: "coordinator-1", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive},
	}}
	config := coordinator.DefaultConfig()
	config.Token = peerTLSToken
	client, ok := coordinator.New(monitor, config).(coordinator.JoinReadinessClient)
	require.True(t, ok)
	return client
}

func callContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return metadata.AppendToOutgoingContext(ctx, "dagu-peer-token", peerTLSToken)
}

// Today's h2c client and a client on built-in TLS call one coordinator on
// one port (Spec 108 §One Port).
func TestPeerTransportOnePort(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t)

	reports, err := server.readinessClient(t).GetJoinReadiness(callContext(t))
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.NoError(t, reports[0].Err)
	assert.True(t, reports[0].Report.BuiltinTls)

	client, _ := server.dial(t, credentials.NewTLS(server.tlsConfig()))
	report, err := client.GetJoinReadiness(callContext(t), &coordinatorv1.GetJoinReadinessRequest{})
	require.NoError(t, err)
	assert.True(t, report.BuiltinTls)
}

// The coordinator certificate never expires, names coordinatorCertName and
// the given hosts, allows server authentication only, and comes with the CA
// certificate (Spec 108 §Coordinator Certificate).
func TestPeerTransportCertificate(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t)

	conn, err := tls.Dial("tcp", server.addr, server.tlsConfig())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	chain := conn.ConnectionState().PeerCertificates
	require.Len(t, chain, 2)
	assert.True(t, chain[1].Equal(server.caCert))
	leaf := chain[0]
	assert.Equal(t, time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC), leaf.NotAfter)
	assert.ElementsMatch(t, []string{coordinatorCertName, "dagu.example.com"}, leaf.DNSNames)
	require.Len(t, leaf.IPAddresses, 1)
	assert.Equal(t, "127.0.0.1", leaf.IPAddresses[0].String())
	assert.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, leaf.ExtKeyUsage)
	assert.False(t, leaf.IsCA)
}

// TLS accepts a client certificate that chains to the peer CA and refuses
// one from another CA (Spec 108 §One Port).
func TestPeerTransportClientCertificate(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t)
	dir := t.TempDir()
	otherCert, otherKey := peercert.Write(t, dir, peercert.Options{
		Name:        "other",
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	other, err := tls.LoadX509KeyPair(otherCert, otherKey)
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		cert     tls.Certificate
		wantCode codes.Code
	}{
		{name: "PeerCA", cert: server.clientCertificate(t), wantCode: codes.OK},
		{name: "OtherCA", cert: other, wantCode: codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := server.tlsConfig()
			// The callback sends the certificate even when it does not match
			// the CAs the server asks for.
			config.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				return &tc.cert, nil
			}
			client, _ := server.dial(t, credentials.NewTLS(config))
			_, err := client.GetJoinReadiness(callContext(t), &coordinatorv1.GetJoinReadinessRequest{})
			assert.Equal(t, tc.wantCode, status.Code(err), "%v", err)
		})
	}
}

// coordinator.require_tls refuses calls over h2c, keeps serving the health
// service on the same connection, and leaves TLS calls alone (Spec 108
// §Require TLS).
func TestRequireTLS(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t,
		grpc.ChainUnaryInterceptor(coordinator.RequireTLSUnaryInterceptor()),
		grpc.ChainStreamInterceptor(coordinator.RequireTLSStreamInterceptor()),
	)

	reports, err := server.readinessClient(t).GetJoinReadiness(callContext(t))
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assertRequireTLS(t, reports[0].Err)

	client, healthClient := server.dial(t, insecure.NewCredentials())
	_, err = client.GetJoinReadiness(callContext(t), &coordinatorv1.GetJoinReadinessRequest{})
	assertRequireTLS(t, err)
	stream, err := client.GetWorkspaceBundle(callContext(t), &coordinatorv1.GetWorkspaceBundleRequest{})
	require.NoError(t, err)
	_, err = stream.Recv()
	assertRequireTLS(t, err)
	health, err := healthClient.Check(callContext(t), &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, health.Status)

	tlsClient, _ := server.dial(t, credentials.NewTLS(server.tlsConfig()))
	_, err = tlsClient.GetJoinReadiness(callContext(t), &coordinatorv1.GetJoinReadinessRequest{})
	require.NoError(t, err)
}

func assertRequireTLS(t *testing.T, err error) {
	t.Helper()
	// A wrapped status keeps its own message only through GRPCStatus.
	var grpcErr interface{ GRPCStatus() *status.Status }
	require.ErrorAs(t, err, &grpcErr)
	st := grpcErr.GRPCStatus()
	assert.Equal(t, codes.Unauthenticated, st.Code(), "%v", err)
	assert.Equal(t, requireTLSMessage, st.Message())
}

// A connection that sends no byte, or only the first byte of a TLS record,
// holds no other connection, and the server closes it at its connection
// timeout.
func TestPeerTransportSilentClient(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, grpc.ConnectionTimeout(200*time.Millisecond))

	for _, tc := range []struct {
		name string
		sent []byte
	}{
		{name: "Nothing"},
		{name: "TLSRecordType", sent: []byte{0x16}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			conn, err := net.Dial("tcp", server.addr)
			require.NoError(t, err)
			defer func() { _ = conn.Close() }()
			_, err = conn.Write(tc.sent)
			require.NoError(t, err)

			_, healthClient := server.dial(t, insecure.NewCredentials())
			_, err = healthClient.Check(callContext(t), &grpc_health_v1.HealthCheckRequest{})
			require.NoError(t, err)

			require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
			_, err = io.ReadAll(conn)
			var netErr net.Error
			if errors.As(err, &netErr) {
				assert.False(t, netErr.Timeout(), "the server did not close the connection")
			}
		})
	}
}

// clientCertificate returns a client certificate that the peer CA signed.
func (s peerTLSServer) clientCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "worker"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, s.caCert, key.Public(), s.caKey)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
