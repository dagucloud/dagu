// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec080_restart_policy_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func finished(runs []run, status string) int {
	n := 0
	for _, r := range runs {
		if r.Status == status && r.FinishedAt != "" {
			n++
		}
	}
	return n
}

func TestServiceRestartsAfterEverySuccessfulRun(t *testing.T) {
	t.Parallel()

	s := startService(t, "restarts_success")
	runs := s.waitForRuns("restarts_success", "three finished runs", func(runs []run) bool {
		return finished(runs, "succeeded") >= 3
	})

	ids := map[string]bool{}
	for _, r := range runs {
		assert.False(t, ids[r.ID], "every restart is a new run: %s", r.ID)
		ids[r.ID] = true
	}
	s.requireSingleActiveRun(runs)
	assert.Equal(t, "restart", s.triggerType("restarts_success", runs[len(runs)-1].ID), "the first run is started by the policy")
	assert.Equal(t, "restart", s.triggerType("restarts_success", runs[0].ID))
}

func TestServiceRestartsAfterEveryFailedRun(t *testing.T) {
	t.Parallel()

	s := startService(t, "restarts_failure")
	runs := s.waitForRuns("restarts_failure", "three failed runs", func(runs []run) bool {
		return finished(runs, "failed") >= 3
	})
	s.requireSingleActiveRun(runs)
}

func TestServiceOnlyAffectsDAGsWithAPolicy(t *testing.T) {
	t.Parallel()

	s := startService(t, "policy_only")
	s.waitForRuns("policy_service", "a finished run", func(runs []run) bool { return finished(runs, "succeeded") >= 1 })
	time.Sleep(quiet)
	assert.Empty(t, s.runs("policy_plain"), "a DAG without a policy is never started by the scheduler")
}

func TestRestartWaitSeparatesRuns(t *testing.T) {
	t.Parallel()

	s := startService(t, "wait")
	runs := s.waitForRuns("wait_service", "three finished runs", func(runs []run) bool {
		return finished(runs, "succeeded") >= 3
	})
	// Runs are listed newest first: each run starts at least the restart wait
	// (2 seconds) after the previous one finished. Timestamps have a resolution
	// of one second, so one second of slack is allowed.
	for i := 0; i+1 < len(runs); i++ {
		next, previous := runs[i], runs[i+1]
		if next.StartedAt == "" || previous.FinishedAt == "" {
			continue
		}
		started, err := time.Parse(time.RFC3339, next.StartedAt)
		require.NoError(t, err)
		ended, err := time.Parse(time.RFC3339, previous.FinishedAt)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, started.Sub(ended), time.Second,
			"run %s started too soon after %s", next.ID, previous.ID)
	}
}

func TestUnlessStoppedStaysStoppedAfterAStop(t *testing.T) {
	t.Parallel()

	s := startService(t, "unless_stopped")
	s.waitForRuns("unless_stopped_service", "a running run", func(runs []run) bool {
		return len(runs) == 1 && runs[0].Status == "running"
	})

	s.dagu.RunWithEnv(s.env, "stop", "unless_stopped_service").ExpectExitCode(0)
	runs := s.waitForRuns("unless_stopped_service", "an aborted run", func(runs []run) bool {
		return len(runs) >= 1 && runs[0].Status == "aborted"
	})

	time.Sleep(quiet)
	after := s.runs("unless_stopped_service")
	assert.Len(t, after, len(runs), "no run is started after a stopped run: %+v", after)
	assert.Equal(t, "aborted", after[0].Status)
}

func TestAlwaysRestartsAfterAStop(t *testing.T) {
	t.Parallel()

	s := startService(t, "always_stop")
	s.waitForRuns("always_stop_service", "a running run", func(runs []run) bool {
		return len(runs) == 1 && runs[0].Status == "running"
	})

	s.dagu.RunWithEnv(s.env, "stop", "always_stop_service").ExpectExitCode(0)
	runs := s.waitForRuns("always_stop_service", "a new run after the stop", func(runs []run) bool {
		return len(runs) >= 2 && runs[0].Status != "aborted"
	})
	assert.Equal(t, "aborted", runs[1].Status)
	s.requireSingleActiveRun(runs)
}

func started(runs []run) int {
	n := 0
	for _, r := range runs {
		if r.StartedAt != "" {
			n++
		}
	}
	return n
}

func TestSuspendedServiceIsNotRestarted(t *testing.T) {
	t.Parallel()

	s := startService(t, "suspend")
	s.waitForRuns("suspend_service", "a finished run", func(runs []run) bool {
		return finished(runs, "succeeded") >= 1
	})

	s.suspend("suspend_service", true)
	// A run that is running when the DAG is suspended finishes. One that is
	// queued stays queued until the DAG is resumed.
	s.waitForRuns("suspend_service", "no running run", func(runs []run) bool {
		for _, r := range runs {
			if r.Status == "running" {
				return false
			}
		}
		return len(runs) > 0
	})
	// Let a restart that was being decided when the DAG was suspended land.
	time.Sleep(quiet / 2)
	settled := s.runs("suspend_service")

	time.Sleep(quiet)
	after := s.runs("suspend_service")
	assert.Len(t, after, len(settled), "a suspended DAG gets no new run: %+v", after)
	assert.Equal(t, started(settled), started(after), "a suspended DAG starts no run: %+v", after)

	s.suspend("suspend_service", false)
	s.waitForRuns("suspend_service", "a run after the DAG is resumed", func(runs []run) bool {
		return started(runs) > started(settled)
	})
}
