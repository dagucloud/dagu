// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intgharness

import (
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	testutil "github.com/dagucloud/dagu/internal/test"
)

// RunProbe observes a DAG-run through the same stores used by production code.
type RunProbe struct {
	h         Harness
	ref       exec.DAGRunRef
	procGroup string
}

// Run returns a semantic probe for a DAG-run.
func (h Harness) Run(ref exec.DAGRunRef, procGroup string) RunProbe {
	return RunProbe{
		h:         h,
		ref:       ref,
		procGroup: procGroup,
	}
}

// RequireRunning waits until the run reaches running status.
func (r RunProbe) RequireRunning(timeout time.Duration) {
	r.RequireStatus(core.Running, timeout)
}

// RequireStatus waits until the run reaches status.
func (r RunProbe) RequireStatus(status core.Status, timeout time.Duration) {
	r.h.t.Helper()

	r.h.Wait.EventuallyEvery(fmt.Sprintf("expected %s to reach status %s", r.ref.String(), status), timeout, defaultPollInterval, func() bool {
		current, ok := r.readStatusIfPresent()
		return ok && current.Status == status
	})
}

// RequireHeartbeatAdvance waits until the run's proc heartbeat advances.
func (r RunProbe) RequireHeartbeatAdvance(timeout time.Duration) {
	r.h.t.Helper()

	testutil.RequireProcHeartbeatAdvance(
		r.h.t,
		r.h.Helper.Context,
		r.h.Helper.ProcStore,
		r.procGroup,
		r.ref,
		r.h.Timeout(timeout),
	)
}

// ReadStatus loads the persisted run status.
func (r RunProbe) ReadStatus() *exec.DAGRunStatus {
	r.h.t.Helper()
	return testutil.ReadRunStatus(r.h.Helper.Context, r.h.t, r.h.Helper.DAGRunStore, r.ref)
}

func (r RunProbe) readStatusIfPresent() (*exec.DAGRunStatus, bool) {
	attempt, err := r.h.Helper.DAGRunStore.FindAttempt(r.h.Helper.Context, r.ref)
	if err != nil {
		return nil, false
	}
	status, err := attempt.ReadStatus(r.h.Helper.Context)
	if err != nil {
		return nil, false
	}
	return status, true
}
