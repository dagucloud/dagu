// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/test/peercert"
)

// A coordinator whose own key does not match its certificate exits with
// code 1, naming both files (Spec 108 §Bring Your Own Certificates).
func TestOwnCertificateKeyMismatch(t *testing.T) {
	dir := t.TempDir()
	certFile, _ := peercert.Write(t, dir, peercert.Options{Name: "server"})
	_, otherKey := peercert.Write(t, dir, peercert.Options{Name: "other"})

	result := harness.NewRunner(t).Run(
		"coordinator",
		fmt.Sprintf("--coordinator.port=%d", harness.FreePort(t)),
		"--peer.cert-file="+certFile,
		"--peer.key-file="+otherKey,
	)
	result.ExpectExitCode(1)
	result.ExpectStderrContains(certFile, otherKey)
}

// start-all serves with its own certificate and dials its coordinator with
// it under mTLS. A certificate that names another host, expires within 14
// days, and lacks client authentication gets three warnings, and the
// coordinator still serves (Spec 108 §Bring Your Own Certificates).
func TestOwnCertificateWarnings(t *testing.T) {
	certFile, keyFile := peercert.Write(t, t.TempDir(), peercert.Options{
		Name:        "server",
		DNSNames:    []string{"coordinator.test"},
		NotAfter:    time.Now().Add(7 * 24 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	coordinatorPort := strconv.Itoa(harness.FreePort(t))
	coordinatorAddr := net.JoinHostPort("127.0.0.1", coordinatorPort)
	services := harness.NewRunner(t).StartWithEnv([]string{"DAGU_AUTH_MODE=none"},
		"start-all",
		"--host=127.0.0.1",
		fmt.Sprintf("--port=%d", harness.FreePort(t)),
		"--coordinator.host=127.0.0.1",
		"--coordinator.advertise=127.0.0.1",
		"--coordinator.port="+coordinatorPort,
		"--peer.insecure=false",
		"--peer.cert-file="+certFile,
		"--peer.key-file="+keyFile,
		"--peer.client-ca-file="+certFile,
	)
	defer services.Stop()

	waitForOutput(t, services,
		"Coordinator certificate does not name the advertised address",
		"Coordinator certificate expires within 14 days",
		"Peer certificate does not allow client authentication",
	)
	expectClientCertRequired(t, coordinatorAddr)
}

// expectClientCertRequired fails the test unless a TLS server at addr
// completes a handshake and then refuses the client for sending no
// certificate. A process that exited, or one that listens without serving,
// fails it.
func expectClientCertRequired(t *testing.T, addr string) {
	t.Helper()
	dialer := &net.Dialer{Timeout: harness.WaitTimeout(t)}
	connection, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h2"},
		// The check is about the server's reply, not its identity.
		InsecureSkipVerify: true,
	})
	require.NoError(t, err)
	defer func() { _ = connection.Close() }()
	require.NoError(t, connection.SetReadDeadline(time.Now().Add(harness.WaitTimeout(t))))
	_, err = connection.Read(make([]byte, 1))
	require.ErrorContains(t, err, "certificate required")
}
