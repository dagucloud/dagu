// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	workerRemoved     = "This worker was removed from the coordinator"
	workerRemovedNext = `Get a new command from "Add worker" to add it again.`
)

// A connected worker that is removed exits with the message once its
// certificate is revoked. Remove holds across a restart: the same invite
// exits at once with the same message, and the check reports the refusal,
// and fails on the worker's record when a coordinator still accepts it.
// A new invite brings the worker back (Spec 108 §Removed Worker, §Worker
// Start, §Remove With Running Runs).
func TestWorkerRemoved(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	coord := startInviteCoordinator(t, dagu)
	invite := coord.createInvite(t, true).Invite
	args := inviteWorkerArgs("worker-a", coord.addr)
	// The message's next step depends on Kubernetes, which an inherited
	// variable would select.
	env := func(invite string) []string {
		return append(inviteWorkerEnv(dagu, "host-a", invite), "KUBERNETES_SERVICE_HOST=")
	}

	worker := dagu.StartWithEnv(env(invite), args...)
	waitForOutput(t, worker, workerEnrolled)
	coord.waitForWorker(t, "worker-a")
	removal, err := coord.admin.RemoveWorker(coord.context(t), &coordinatorv1.RemoveWorkerRequest{WorkerId: "worker-a"})
	require.NoError(t, err)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING, removal.State)

	select {
	case <-worker.Done():
	case <-time.After(removingMinimum + harness.WaitTimeout(t)):
		t.Fatalf("the removed worker did not exit: %s", worker.Output())
	}
	waitForOutput(t, worker, workerRemoved, workerRemovedNext)
	assert.Equal(t, 1, worker.ExitCode())

	restarted := dagu.RunWithEnv(env(invite), args...)
	restarted.ExpectExitCode(1)
	restarted.ExpectStderrContains(workerRemoved)
	restarted.ExpectStderrContains(workerRemovedNext)
	assert.NotContains(t, restarted.Stdout()+restarted.Stderr(), workerEnrolled)

	check := dagu.RunWithEnv(env(invite), "worker", "--check", "--worker.coordinators="+coord.addr)
	check.ExpectExitCode(1)
	assert.Contains(t, check.Stdout(), "  Invite pin: matches the coordinator's CA\n")
	assert.Contains(t, check.Stdout(), "  Coordinator answered: "+workerRemoved+"\n  "+workerRemovedNext+"\n")

	// A coordinator of the same CA that keeps no record of the removal still
	// accepts the certificate, and the check fails all the same.
	unaware := serveCoordinator(t, dagu, coord.env, servingBuiltinTLS)
	check = dagu.RunWithEnv(env(invite), "worker", "--check", "--worker.coordinators="+unaware)
	check.ExpectExitCode(1)
	assert.Contains(t, check.Stdout(), "  Coordinator answered: serving, and it accepted the worker certificate\n")
	assert.Contains(t, check.Stdout(), "This worker keeps a record that a coordinator refused its certificate as revoked, so it exits at start\n")
	check.ExpectStderrContains(workerRemoved)

	rejoined := dagu.StartWithEnv(env(coord.createInvite(t, false).Invite), inviteWorkerArgs("worker-a-again", coord.addr)...)
	waitForOutput(t, rejoined, workerEnrolled)
	coord.waitForWorker(t, "worker-a-again")
}
