// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"net"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// inviteOutput is what dagu worker invite prints on success, with the
// invite captured.
var inviteOutput = regexp.MustCompile(`^Invite \(single use, expires \d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ\):\n` +
	`  (dagu1\.[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{43})\n` +
	`Run on the worker host:\n` +
	`  dagu worker --worker\.coordinators=192\.168\.1\.20:50055 --worker\.invite=(dagu1\.[^ \n]+)\n$`)

// dagu worker invite creates an invite on a coordinator that accepts
// workers from other hosts and prints it with the command for the worker
// host. The invite enrolls a worker and appears in no log (Spec 108
// §dagu worker invite).
func TestWorkerInvite(t *testing.T) {
	dagu := harness.NewRunner(t)
	peer := newPeerFixture(t, dagu)
	// The later --coordinator.host wins over the loopback default of
	// startCommandCoordinator.
	coordinator := startCommandCoordinator(t, dagu, peer, "--coordinator.host=0.0.0.0")

	result := dagu.RunWithEnv(coordinator.env, "worker", "invite", "--address", "192.168.1.20:50055")
	result.ExpectExitCode(0)
	match := inviteOutput.FindStringSubmatch(result.Stdout())
	require.NotNil(t, match, "stdout: %s", result.Stdout())
	invite := match[1]
	assert.Equal(t, invite, match[2])
	result.ExpectStderrNotContains(invite)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	require.NoError(t, err)
	client := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, coordinator.addr, peer.builtinTLS()))
	_, err = client.Enroll(callContext(t), &coordinatorv1.EnrollRequest{Invite: invite, Csr: csr, WorkerId: "worker-1", Host: "host-a", KeepsCertificate: true})
	require.NoError(t, err)

	invites := coordinator.invites(t, peer)
	require.Len(t, invites, 1)
	assert.Equal(t, "192.168.1.20:50055", invites[0].Address)

	// The coordinator logged everything it handled once it has stopped.
	coordinator.process.Stop()
	assert.Contains(t, coordinator.process.Output(), "Worker invite created")
	assert.NotContains(t, coordinator.process.Output(), invite)
}

// A worker started with exactly the printed command enrolls and connects
// with its certificate. The worker dials an address of this machine other
// than loopback, which the address rule refuses (Spec 108
// §dagu worker invite).
func TestWorkerInviteJoinsWorker(t *testing.T) {
	host := machineAddress(t)
	dagu := harness.NewRunner(t)
	peer := newPeerFixture(t, dagu)
	coordinator := startCommandCoordinator(t, dagu, peer, "--coordinator.host=0.0.0.0")
	_, port, err := net.SplitHostPort(coordinator.addr)
	require.NoError(t, err)

	result := dagu.RunWithEnv(coordinator.env, "worker", "invite", "--address", net.JoinHostPort(host, port))
	result.ExpectExitCode(0)
	_, command, ok := strings.Cut(result.Stdout(), "Run on the worker host:\n  ")
	require.True(t, ok, result.Stdout())
	words := strings.Fields(command)
	require.Equal(t, "dagu", words[0])

	// The worker host shares no data directory with the coordinator, so
	// the invite is its only credential.
	worker := dagu.StartWithEnv([]string{"DAGU_HOME=" + dagu.ProjectPath("worker-host")},
		append(words[1:], "--worker.id=joined", "--worker.health-port=0")...)
	waitForOutput(t, worker, builtinFromInvite, workerEnrolled)
	admin := inviteCoordinator{
		addr:  coordinator.addr,
		admin: coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, coordinator.addr, peer.builtinTLS())),
		token: peer.token,
	}
	admin.waitForWorker(t, "joined")
	invite := strings.TrimPrefix(words[len(words)-1], "--worker.invite=")
	worker.Stop()
	assert.NotContains(t, worker.Output(), invite)
}

// machineAddress returns an IPv4 address of this machine that is neither
// loopback nor link-local, or skips the test when it has none.
func machineAddress(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	require.NoError(t, err)
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return ip.String()
		}
	}
	t.Skip("this machine has no IPv4 address other than loopback and link-local")
	return ""
}

// While the coordinator listens on loopback only, dagu worker invite prints
// the change to make to standard error, creates no invite, and exits with code 1 (Spec 108
// §dagu worker invite).
func TestWorkerInviteLoopbackOnly(t *testing.T) {
	dagu := harness.NewRunner(t)
	peer := newPeerFixture(t, dagu)
	coordinator := startCommandCoordinator(t, dagu, peer)

	result := dagu.RunWithEnv(coordinator.env, "worker", "invite", "--address", "192.168.1.20:50055")
	result.ExpectExitCode(1)
	result.ExpectStdout("")
	result.ExpectStderrContains("Coordinator "+coordinator.addr+" cannot accept workers from other hosts:\n"+
		"  It listens on 127.0.0.1, so only its own machine can connect. Start it with --coordinator.host=0.0.0.0.\n"+
		"  Each change needs a restart of the coordinator.\n",
		"no invite created: 1 of 1 coordinators cannot accept workers from other hosts")
	assert.Empty(t, coordinator.invites(t, peer))
}

// A closed standard output makes the invite command delete the invite it
// could not print and exit with code 1, instead of ending on the signal
// with the invite still valid (Spec 108 §dagu worker invite).
func TestWorkerInviteClosedStdout(t *testing.T) {
	dagu := harness.NewRunner(t)
	peer := newPeerFixture(t, dagu)
	coordinator := startCommandCoordinator(t, dagu, peer, "--coordinator.host=0.0.0.0")
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	t.Cleanup(func() { _ = writer.Close() })

	result := dagu.RunWithStdout(coordinator.env, writer, "worker", "invite", "--address", "192.168.1.20:50055")

	result.ExpectExitCode(1)
	result.ExpectStderrContains("so it was deleted: run the command again")
	assert.Empty(t, coordinator.invites(t, peer))
}

// On a host that shares the data directory of a coordinator with a
// generated CA, the invite command logs no peer transport line, even at
// the debug level, so its output says nothing of where that CA is kept
// (Spec 108 §dagu worker invite).
func TestWorkerInviteDebugLog(t *testing.T) {
	dagu := harness.NewRunner(t)
	env := []string{"DAGU_HOME=" + dagu.ProjectPath("home")}
	port := harness.FreePort(t)
	coordinator := dagu.StartWithEnv(env,
		"coordinator",
		"--coordinator.host=0.0.0.0",
		"--coordinator.advertise=127.0.0.1",
		"--coordinator.port="+strconv.Itoa(port),
		"--coordinator.health-port=0",
	)
	waitForOutput(t, coordinator, "Coordinator transport: built-in TLS and h2c (from the generated CA)", "Starting to serve on coordinator service")

	result := dagu.RunWithEnv(append(env, "DEBUG=1"), "worker", "invite", "--address", "192.168.1.20:50055")

	result.ExpectExitCode(0)
	result.ExpectStderrNotContains("Peer transport", "data directory")
}

// inviteTestCoordinator is a coordinator that dagu worker invite finds in
// the service registry.
type inviteTestCoordinator struct {
	addr    string
	process *harness.Process
	// env gives a process the peer token and the coordinator's Dagu home,
	// and so its service registry.
	env []string
}

// startCommandCoordinator starts a coordinator on built-in TLS from peer,
// listening on loopback unless args say otherwise.
func startCommandCoordinator(t *testing.T, dagu *harness.Runner, peer peerFixture, args ...string) inviteTestCoordinator {
	t.Helper()
	port := harness.FreePort(t)
	env := append(slices.Clone(peer.env), "DAGU_HOME="+dagu.ProjectPath("home"))
	process := dagu.StartWithEnv(env, append([]string{
		"coordinator",
		"--coordinator.host=127.0.0.1",
		"--coordinator.advertise=127.0.0.1",
		"--coordinator.port=" + strconv.Itoa(port),
		"--coordinator.health-port=0",
	}, args...)...)
	waitForOutput(t, process, servingBuiltinTLS, "Starting to serve on coordinator service")
	return inviteTestCoordinator{addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), process: process, env: env}
}

// invites lists the coordinator's invites with the peer token.
func (c inviteTestCoordinator) invites(t *testing.T, peer peerFixture) []*coordinatorv1.InviteRecord {
	t.Helper()
	client := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, c.addr, peer.builtinTLS()))
	ctx := metadata.AppendToOutgoingContext(callContext(t), "dagu-peer-token", peer.token)
	resp, err := client.ListInvites(ctx, &coordinatorv1.ListInvitesRequest{})
	require.NoError(t, err)
	return resp.Invites
}
