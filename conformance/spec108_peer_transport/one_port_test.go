// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/test/peercert"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	coordinatorCertName = "coordinator.dagu.invalid"
	servingBuiltinTLS   = "Coordinator transport: built-in TLS and h2c (from peer.ca_cert_file)"
)

// peerFixture is a peer CA and token from dagu peer init, and the
// environment that gives them to a coordinator.
type peerFixture struct {
	env   []string
	roots *x509.CertPool
	token string
}

func newPeerFixture(t *testing.T, dagu *harness.Runner) peerFixture {
	t.Helper()
	dagu.Run("peer", "init", "--out", "peer").ExpectExitCode(0)
	caPEM, err := fileutil.ReadFile(dagu.ProjectPath("peer/ca.crt"))
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(caPEM))
	token, err := fileutil.ReadFile(dagu.ProjectPath("peer/token"))
	require.NoError(t, err)
	return peerFixture{
		env: []string{
			"DAGU_PEER_CA_CERT_FILE=" + dagu.ProjectPath("peer/ca.crt"),
			"DAGU_PEER_CA_KEY_FILE=" + dagu.ProjectPath("peer/ca.key"),
			"DAGU_PEER_TOKEN=" + strings.TrimSpace(string(token)),
		},
		roots: roots,
		token: strings.TrimSpace(string(token)),
	}
}

// builtinTLS returns the credentials of a client on built-in TLS.
func (f peerFixture) builtinTLS() credentials.TransportCredentials {
	return credentials.NewTLS(&tls.Config{RootCAs: f.roots, ServerName: coordinatorCertName, MinVersion: tls.VersionTLS13})
}

// joinReadiness calls GetJoinReadiness at addr over creds with the peer
// token.
func (f peerFixture) joinReadiness(t *testing.T, addr string, creds credentials.TransportCredentials) (*coordinatorv1.GetJoinReadinessResponse, error) {
	t.Helper()
	conn := dialCoordinator(t, addr, creds)
	ctx := metadata.AppendToOutgoingContext(callContext(t), "dagu-peer-token", f.token)
	return coordinatorv1.NewCoordinatorServiceClient(conn).GetJoinReadiness(ctx, &coordinatorv1.GetJoinReadinessRequest{}, grpc.WaitForReady(true))
}

// An h2c client, as every client before Spec 108 is, and a client on
// built-in TLS call one coordinator on one port, and join readiness reports
// built-in TLS (Spec 108 §One Port, §Join Readiness).
func TestOnePort(t *testing.T) {
	dagu := harness.NewRunner(t)
	peer := newPeerFixture(t, dagu)
	addr := serveCoordinator(t, dagu, peer.env, servingBuiltinTLS)

	for name, creds := range map[string]credentials.TransportCredentials{
		"h2c":          insecure.NewCredentials(),
		"built-in TLS": peer.builtinTLS(),
	} {
		report, err := peer.joinReadiness(t, addr, creds)
		require.NoError(t, err, name)
		assert.True(t, report.BuiltinTls, name)
	}
}

// A coordinator without CA settings serves built-in TLS from the CA it
// generates (Spec 108 §Coordinator Line).
func TestGeneratedCATransport(t *testing.T) {
	dagu := harness.NewRunner(t)
	addr := serveCoordinator(t, dagu, nil, "Coordinator transport: built-in TLS and h2c (from the generated CA)")
	expectServing(t, addr, insecure.NewCredentials())
}

// A coordinator with its own certificates serves TLS only, as before Spec
// 108 (Spec 108 §One Port).
func TestOwnCertificatesTransport(t *testing.T) {
	dagu := harness.NewRunner(t)
	certFile, keyFile := peercert.Write(t, t.TempDir(), peercert.Options{Name: "server", DNSNames: []string{"coordinator.test"}})
	addr := serveCoordinator(t, dagu, nil, "Coordinator transport: TLS (from peer.cert_file)",
		"--peer.cert-file="+certFile, "--peer.key-file="+keyFile)

	expectServing(t, addr, credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS12,
		// The check is about the transport, not the certificate.
		InsecureSkipVerify: true,
	}))
	_, err := grpc_health_v1.NewHealthClient(dialCoordinator(t, addr, insecure.NewCredentials())).
		Check(callContext(t), &grpc_health_v1.HealthCheckRequest{})
	assert.Equal(t, codes.Unavailable, status.Code(err), "%v", err)
}

// serveCoordinator starts a coordinator on a loopback port with env and
// returns its address once it has logged line.
func serveCoordinator(t *testing.T, dagu *harness.Runner, env []string, line string, args ...string) string {
	t.Helper()
	port := harness.FreePort(t)
	startCoordinatorOnPort(t, dagu, env, port, line, args...)
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// startCoordinatorOnPort starts a coordinator on the loopback port with
// env and returns it once it has logged line.
func startCoordinatorOnPort(t *testing.T, dagu *harness.Runner, env []string, port int, line string, args ...string) *harness.Process {
	t.Helper()
	coordinator := dagu.StartWithEnv(env, append([]string{
		"coordinator",
		"--coordinator.host=127.0.0.1",
		"--coordinator.advertise=127.0.0.1",
		fmt.Sprintf("--coordinator.port=%d", port),
		"--coordinator.health-port=0",
	}, args...)...)
	waitForOutput(t, coordinator, line, "Starting to serve on coordinator service")
	return coordinator
}

func dialCoordinator(t *testing.T, addr string, creds credentials.TransportCredentials) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// expectServing fails the test unless the coordinator at addr answers a
// health check over creds.
func expectServing(t *testing.T, addr string, creds credentials.TransportCredentials) {
	t.Helper()
	health, err := grpc_health_v1.NewHealthClient(dialCoordinator(t, addr, creds)).
		Check(callContext(t), &grpc_health_v1.HealthCheckRequest{}, grpc.WaitForReady(true))
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, health.Status)
}

func callContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), harness.WaitTimeout(t))
	t.Cleanup(cancel)
	return ctx
}
