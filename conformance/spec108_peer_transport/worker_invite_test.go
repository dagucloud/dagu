// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	builtinFromInvite     = "Peer transport: built-in TLS (from worker.invite)"
	builtinFromWorkerCert = "Peer transport: built-in TLS (from worker certificate)"
	workerEnrolled        = "Worker enrolled"
	lockWaiting           = "Another worker holds the certificate of this data directory"
	// lockWait is how long a second worker waits for the certificate lock
	// before it exits.
	lockWait = 30 * time.Second
)

// inviteCoordinator is a coordinator on built-in TLS and a client that
// calls it with the peer token.
type inviteCoordinator struct {
	addr  string
	admin coordinatorv1.CoordinatorServiceClient
	token string
}

func startInviteCoordinator(t *testing.T, dagu *harness.Runner) inviteCoordinator {
	t.Helper()
	peer := newPeerFixture(t, dagu)
	addr := serveCoordinator(t, dagu, peer.env, servingBuiltinTLS)
	return inviteCoordinator{
		addr:  addr,
		admin: coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, addr, peer.builtinTLS())),
		token: peer.token,
	}
}

func (c inviteCoordinator) context(t *testing.T) context.Context {
	t.Helper()
	return metadata.AppendToOutgoingContext(callContext(t), "dagu-peer-token", c.token)
}

func (c inviteCoordinator) createInvite(t *testing.T, reusable bool) *coordinatorv1.CreateInviteResponse {
	t.Helper()
	created, err := c.admin.CreateInvite(c.context(t), &coordinatorv1.CreateInviteRequest{Reusable: reusable}, grpc.WaitForReady(true))
	require.NoError(t, err)
	return created
}

// waitForWorker waits until the coordinator lists the worker id with the
// connection a worker certificate gives.
func (c inviteCoordinator) waitForWorker(t *testing.T, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(c.context(t), harness.WaitTimeout(t))
	defer cancel()
	for {
		resp, err := c.admin.GetWorkers(ctx, &coordinatorv1.GetWorkersRequest{})
		require.NoError(t, err)
		for _, worker := range resp.Workers {
			if worker.WorkerId == id && worker.Connection == coordinatorv1.WorkerConnection_WORKER_CONNECTION_CERTIFICATE {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("the coordinator did not list worker %s with a certificate", id)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// inviteWorkerEnv is the environment of a worker whose data directory is
// home under the project, with invite.
func inviteWorkerEnv(dagu *harness.Runner, home, invite string) []string {
	return []string{"DAGU_HOME=" + dagu.ProjectPath(home), "DAGU_WORKER_INVITE=" + invite}
}

func inviteWorkerArgs(id, addr string) []string {
	return []string{"worker", "--worker.id=" + id, "--worker.health-port=0", "--worker.coordinators=" + addr}
}

// A worker on another host joins with a single-use invite and nothing else:
// it enrolls and connects with its certificate. A restart keeps the
// certificate without using the invite again, and a second host with the
// same invite exits with the message of Spec 108 §Messages (Spec 108
// §Single-Use Invite).
func TestWorkerInviteEnrollment(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	coord := startInviteCoordinator(t, dagu)
	created := coord.createInvite(t, false)
	secret := created.Invite[strings.LastIndex(created.Invite, ".")+1:]

	worker := dagu.StartWithEnv(inviteWorkerEnv(dagu, "host-a", created.Invite), inviteWorkerArgs("worker-a", coord.addr)...)
	waitForOutput(t, worker, builtinFromInvite, workerEnrolled)
	coord.waitForWorker(t, "worker-a")
	worker.Stop()
	assert.NotContains(t, worker.Output(), secret, "the whole output, shutdown included")

	// A worker killed without its shutdown frees the certificate at once:
	// the restart takes it without waiting.
	restarted := dagu.StartWithEnv(inviteWorkerEnv(dagu, "host-a", created.Invite), inviteWorkerArgs("worker-a-restarted", coord.addr)...)
	waitForOutput(t, restarted, builtinFromWorkerCert)
	coord.waitForWorker(t, "worker-a-restarted")
	restarted.Kill()
	assert.NotContains(t, restarted.Output(), workerEnrolled)
	again := dagu.StartWithEnv(inviteWorkerEnv(dagu, "host-a", created.Invite), inviteWorkerArgs("worker-a-again", coord.addr)...)
	waitForOutput(t, again, builtinFromWorkerCert)
	coord.waitForWorker(t, "worker-a-again")
	assert.NotContains(t, again.Output(), lockWaiting)
	invites, err := coord.admin.ListInvites(coord.context(t), &coordinatorv1.ListInvitesRequest{})
	require.NoError(t, err)
	require.Len(t, invites.Invites, 1)
	assert.Equal(t, int32(1), invites.Invites[0].UseCount)

	other := dagu.RunWithEnv(inviteWorkerEnv(dagu, "host-b", created.Invite), inviteWorkerArgs("worker-b", coord.addr)...)
	other.ExpectExitCode(1)
	other.ExpectStderrContains("The invite is invalid, expired, or already used")
	other.ExpectStderrContains(`Get a new command from "Add worker". For several hosts, use a reusable invite.`)
	assert.NotContains(t, other.Stderr()+other.Stdout(), secret)
}

// A worker whose settings fail at start does so before it enrolls, so its
// single-use invite stays unused and enrolls a worker started right.
func TestWorkerInviteBadLabel(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	coord := startInviteCoordinator(t, dagu)
	invite := coord.createInvite(t, false).Invite

	bad := dagu.RunWithEnv(inviteWorkerEnv(dagu, "host-a", invite), append(inviteWorkerArgs("worker-a", coord.addr), "--worker.labels=team=data eng")...)
	bad.ExpectExitCode(1)
	assert.NotContains(t, bad.Stdout()+bad.Stderr(), workerEnrolled)

	// A health check port that another process holds stops the worker
	// before it enrolls too.
	busy, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	busyPort := fmt.Sprintf("--worker.health-port=%d", busy.Addr().(*net.TCPAddr).Port)
	held := dagu.RunWithEnv(inviteWorkerEnv(dagu, "host-a", invite), append(inviteWorkerArgs("worker-a", coord.addr), busyPort)...)
	require.NoError(t, busy.Close())
	held.ExpectExitCode(1)
	held.ExpectStderrContains("failed to start worker health check server")
	assert.NotContains(t, held.Stdout()+held.Stderr(), workerEnrolled)

	worker := dagu.StartWithEnv(inviteWorkerEnv(dagu, "host-a", invite), inviteWorkerArgs("worker-a", coord.addr)...)
	waitForOutput(t, worker, workerEnrolled)
	coord.waitForWorker(t, "worker-a")
}

// Two workers on one data directory never share a certificate: the second
// waits 30 seconds while the first keeps the lock, also while the first is
// paused, then exits (Spec 108 §Two Workers on One Volume).
func TestWorkerInviteSameDataDir(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	coord := startInviteCoordinator(t, dagu)
	invite := coord.createInvite(t, true).Invite

	first := dagu.StartWithEnv(inviteWorkerEnv(dagu, "volume", invite), inviteWorkerArgs("worker-1", coord.addr)...)
	waitForOutput(t, first, workerEnrolled)
	pauseProcess(t, first.PID())

	started := time.Now()
	second := dagu.WithCommandTimeout(lockWait+harness.WaitTimeout(t)).
		RunWithEnv(inviteWorkerEnv(dagu, "volume", invite), inviteWorkerArgs("worker-2", coord.addr)...)
	second.ExpectExitCode(1)
	second.ExpectStderrContains("Another worker uses this data directory")
	second.ExpectStderrContains("Give each worker its own data directory or volume.")
	assert.GreaterOrEqual(t, time.Since(started), lockWait)
	select {
	case <-first.Done():
		t.Fatalf("the first worker exited: %s", first.FailureOutput())
	default:
	}
}
