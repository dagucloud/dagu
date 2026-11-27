// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"fmt"
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/test/peercert"
)

// dagu worker --check prints the start line and the ignored-setting warnings
// instead of logging them, then what the coordinator answered. The worker
// shares the coordinator's data directory, so it sends the token there
// (Spec 108 §dagu worker --check).
func TestWorkerCheckPasses(t *testing.T) {
	dagu := harness.NewRunner(t)
	sharedHome := []string{"DAGU_HOME=" + t.TempDir()}
	addr := startCoordinator(t, dagu, sharedHome)

	result := dagu.RunWithEnv(sharedHome, "worker", "--check", "--worker.coordinators="+addr,
		"--peer.client-ca-file=/etc/dagu/ca.crt")

	result.ExpectExitCode(0)
	for _, want := range []string{
		ignoredFileCA + "\n" + h2cNoSource + "\nCoordinator " + addr + "\n",
		"  Reachable: yes\n",
		"  Handshake: none, the worker uses h2c\n",
		"  Credential: peer token from the coordinator's data directory\n",
		"  Coordinator answered: serving, and it accepted the call\n",
	} {
		assert.Contains(t, result.Stdout(), want)
	}
	result.ExpectStderrNotContains("Peer transport:", "Peer TLS setting ignored:")
}

// An address that does not answer gets the message of Spec 108 §Messages.
func TestWorkerCheckUnreachable(t *testing.T) {
	port := strconv.Itoa(harness.FreePort(t))
	addr := net.JoinHostPort("127.0.0.1", port)

	result := harness.NewRunner(t).Run("worker", "--check", "--worker.coordinators="+addr)

	result.ExpectExitCode(1)
	assert.Contains(t, result.Stdout(), "  Cannot reach coordinator at "+addr+"\n"+
		"  Check that the coordinator accepts workers from other hosts, that port "+port+
		" is open on the way, and that the address is right.\n")
}

// A failed handshake names what the certificate covers, the address dialed,
// and the CA the certificate chains to (Spec 108 §dagu worker --check).
func TestWorkerCheckHandshake(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := peercert.Write(t, dir, peercert.Options{Name: "server", DNSNames: []string{"coordinator.test"}})
	otherCA, _ := peercert.Write(t, dir, peercert.Options{Name: "other"})
	dagu := harness.NewRunner(t)
	addr := startCoordinator(t, dagu, nil, "--peer.cert-file="+certFile, "--peer.key-file="+keyFile)

	for _, tc := range []struct {
		name   string
		caFile string
		want   string
	}{
		{name: "NameNotCovered", caFile: certFile, want: "  Handshake: failed, the certificate does not cover 127.0.0.1\n"},
		{name: "OtherCA", caFile: otherCA, want: "the certificate does not chain to a CA this client trusts\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := dagu.Run("worker", "--check", "--worker.coordinators="+addr,
				"--peer.insecure=false", "--peer.client-ca-file="+tc.caFile)

			result.ExpectExitCode(1)
			for _, want := range []string{
				tc.want,
				"    Address dialed: " + addr + "\n",
				"    Certificate covers: DNS:coordinator.test\n",
				"    Certificate chains to: server (the certificate signs itself)\n",
			} {
				assert.Contains(t, result.Stdout(), want)
			}
		})
	}
}

// A coordinator that requires a client certificate refuses none, and one
// another CA signed, and the check says which (Spec 108 §dagu worker
// --check).
func TestWorkerCheckClientCertificate(t *testing.T) {
	dir := t.TempDir()
	serverCert, serverKey := peercert.Write(t, dir, peercert.Options{Name: "server", IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}})
	clientCA, _ := peercert.Write(t, dir, peercert.Options{Name: "client-ca"})
	otherCert, otherKey := peercert.Write(t, dir, peercert.Options{Name: "other-client"})
	dagu := harness.NewRunner(t)
	addr := startCoordinator(t, dagu, nil,
		"--peer.cert-file="+serverCert, "--peer.key-file="+serverKey, "--peer.client-ca-file="+clientCA)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "None", want: "  Handshake: failed, the coordinator requires a client certificate, and the worker sends none"},
		{name: "OtherCA", args: []string{"--peer.cert-file=" + otherCert, "--peer.key-file=" + otherKey},
			want: "  Handshake: failed, the coordinator requires a client certificate that its peer.client_ca_file signed, and " +
				otherCert + " is not one, so the worker sends none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := dagu.Run(append([]string{"worker", "--check", "--worker.coordinators=" + addr,
				"--peer.insecure=false", "--peer.client-ca-file=" + serverCert}, tc.args...)...)

			result.ExpectExitCode(1)
			assert.Contains(t, result.Stdout(), tc.want)
		})
	}
}

// Without worker.coordinators, the check dials the coordinators in the
// service registry of its data directory, then fails since the worker does
// not start without worker.coordinators (Spec 108 §dagu worker --check).
func TestWorkerCheckRegistry(t *testing.T) {
	dagu := harness.NewRunner(t)
	sharedHome := []string{"DAGU_HOME=" + t.TempDir()}
	addr := startCoordinator(t, dagu, sharedHome)

	result := dagu.RunWithEnv(sharedHome, "worker", "--check")

	result.ExpectExitCode(1)
	assert.Contains(t, result.Stdout(), "Coordinator "+addr+"\n  Reachable: yes\n")
	assert.Contains(t, result.Stdout(), "  Coordinator answered: serving, and it accepted the call\n")
	result.ExpectStderrContains("worker.coordinators is required: set it to the address of a coordinator above that passed")
}

// startCoordinator starts a coordinator on a loopback port with env and
// returns its address once it serves.
func startCoordinator(t *testing.T, dagu *harness.Runner, env []string, args ...string) string {
	t.Helper()
	port := harness.FreePort(t)
	coordinator := dagu.StartWithEnv(env, append([]string{
		"coordinator",
		"--coordinator.host=127.0.0.1",
		"--coordinator.advertise=127.0.0.1",
		fmt.Sprintf("--coordinator.port=%d", port),
		"--coordinator.health-port=0",
	}, args...)...)
	waitForOutput(t, coordinator, "Starting to serve on coordinator service")
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}
