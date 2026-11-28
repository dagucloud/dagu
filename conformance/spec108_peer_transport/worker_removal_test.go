// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
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
// A worker that kept no record, given a new invite, passes the check
// without using the invite. A new invite brings the worker back (Spec 108
// §Removed Worker, §Worker Start, §Remove With Running Runs,
// §dagu worker --check).
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
	// A copy of the enrollment taken before the removal keeps no record of
	// it, as a worker that was stopped while it was removed.
	offlineData := copyEnrollment(t, dagu, "host-a", "host-a-offline")
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

	fresh := coord.createInvite(t, false)
	offlineEnv := append(inviteWorkerEnv(dagu, "host-a-offline", fresh.Invite), "KUBERNETES_SERVICE_HOST=")
	check = dagu.RunWithEnv(offlineEnv, "worker", "--check", "--worker.coordinators="+coord.addr)
	check.ExpectExitCode(0)
	assert.Contains(t, check.Stdout(), "  Coordinator answered: this worker was removed from the coordinator; it enrolls again with worker.invite when it starts\n")
	assert.Contains(t, check.Stdout(), "Every coordinator passed the check\n")
	assert.Equal(t, int32(0), coord.inviteUses(t, fresh.Record.Id), "the check never enrolls")

	// A directory in place of the worker's write probe leaves its data
	// directory unwritable to it, also as root.
	require.NoError(t, os.MkdirAll(filepath.Join(offlineData, "worker", ".write-probe", "taken"), 0o700))
	check = dagu.RunWithEnv(offlineEnv, "worker", "--check", "--worker.coordinators="+coord.addr)
	check.ExpectExitCode(1)
	assert.Contains(t, check.Stdout(), "  Coordinator answered: This worker cannot replace the certificate it enrolled for with one from the invite's CA\n"+
		"  Make "+offlineData+" writable, then start the worker again.\n")
	check.ExpectStderrContains("1 of 1 coordinators did not pass the check")

	rejoined := dagu.StartWithEnv(env(coord.createInvite(t, false).Invite), inviteWorkerArgs("worker-a-again", coord.addr)...)
	waitForOutput(t, rejoined, workerEnrolled)
	coord.waitForWorker(t, "worker-a-again")
}

// copyEnrollment copies the enrollment that the worker whose home is from
// keeps into the data directory of the home to, and returns that directory.
func copyEnrollment(t *testing.T, dagu *harness.Runner, from, to string) string {
	t.Helper()
	data, err := fileutil.ReadFile(filepath.Join(dagu.ProjectPath(from), "data", "worker", "credential.json"))
	require.NoError(t, err)
	dataDir := filepath.Join(dagu.ProjectPath(to), "data")
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "worker"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "worker", "credential.json"), data, 0o600))
	return dataDir
}

// inviteUses returns how many enrollments the invite id admitted.
func (c inviteCoordinator) inviteUses(t *testing.T, id string) int32 {
	t.Helper()
	invites, err := c.admin.ListInvites(c.context(t), &coordinatorv1.ListInvitesRequest{})
	require.NoError(t, err)
	for _, invite := range invites.Invites {
		if invite.Id == id {
			return invite.UseCount
		}
	}
	t.Fatalf("no invite %s", id)
	return 0
}
