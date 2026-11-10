// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

func TestDescribeServiceDAG(t *testing.T) {
	t.Parallel()

	run := func(status ir.Status) *ir.DAGRunStatus {
		return &ir.DAGRunStatus{DAGRunID: "run-1", Status: status}
	}
	stopped := func() (bool, error) { return true, nil }
	notStopped := func() (bool, error) { return false, nil }

	tests := []struct {
		name      string
		policy    ir.RestartPolicy
		latest    *ir.DAGRunStatus
		suspended bool
		stop      func() (bool, error)
		want      ServiceDAGState
	}{
		{name: "NoRun", latest: &ir.DAGRunStatus{}, want: ServiceDAGStatePending},
		{name: "NoRunSuspended", latest: &ir.DAGRunStatus{}, suspended: true, want: ServiceDAGStateSuspended},
		{name: "Running", latest: run(ir.Running), want: ServiceDAGStateRunning},
		{name: "RunningWhileSuspended", latest: run(ir.Running), suspended: true, want: ServiceDAGStateRunning},
		{name: "WaitingApproval", latest: run(ir.Waiting), want: ServiceDAGStateWaiting},
		{name: "Queued", latest: run(ir.Queued), want: ServiceDAGStatePending},
		{name: "Succeeded", latest: run(ir.Succeeded), want: ServiceDAGStateRestarting},
		{name: "PartiallySucceeded", latest: run(ir.PartiallySucceeded), want: ServiceDAGStateRestarting},
		{name: "Failed", latest: run(ir.Failed), want: ServiceDAGStateFailing},
		{name: "SucceededSuspended", latest: run(ir.Succeeded), suspended: true, want: ServiceDAGStateSuspended},
		{name: "FailedSuspended", latest: run(ir.Failed), suspended: true, want: ServiceDAGStateSuspended},
		{
			name: "StoppedByOperator", policy: ir.RestartPolicyUnlessStopped,
			latest: run(ir.Aborted), stop: stopped, want: ServiceDAGStateStopped,
		},
		{
			name: "AbortedWithoutStopRequest", policy: ir.RestartPolicyUnlessStopped,
			latest: run(ir.Aborted), stop: notStopped, want: ServiceDAGStateFailing,
		},
		{
			// A stopped distributed run whose worker is gone ends as failed.
			name: "StoppedRunThatFailed", policy: ir.RestartPolicyUnlessStopped,
			latest: run(ir.Failed), stop: stopped, want: ServiceDAGStateStopped,
		},
		{
			name: "StoppedRunThatSucceeded", policy: ir.RestartPolicyUnlessStopped,
			latest: run(ir.Succeeded), stop: stopped, want: ServiceDAGStateStopped,
		},
		{
			name: "FailedWithoutStopRequest", policy: ir.RestartPolicyUnlessStopped,
			latest: run(ir.Failed), stop: notStopped, want: ServiceDAGStateFailing,
		},
		{
			name: "SucceededWithoutStopRequest", policy: ir.RestartPolicyUnlessStopped,
			latest: run(ir.Succeeded), stop: notStopped, want: ServiceDAGStateRestarting,
		},
		{
			// A stop does not keep an always DAG down: the next run follows.
			name: "AlwaysStoppedRestarts", policy: ir.RestartPolicyAlways,
			latest: run(ir.Aborted), stop: stopped, want: ServiceDAGStateRestarting,
		},
		{
			name: "AlwaysStoppedRunThatFailed", policy: ir.RestartPolicyAlways,
			latest: run(ir.Failed), stop: stopped, want: ServiceDAGStateRestarting,
		},
		{
			name: "AlwaysFailedWithoutStopRequest", policy: ir.RestartPolicyAlways,
			latest: run(ir.Failed), stop: notStopped, want: ServiceDAGStateFailing,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := DescribeServiceDAG(tt.policy, tt.latest, tt.suspended, tt.stop)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("StopCheckFails", func(t *testing.T) {
		t.Parallel()
		_, err := DescribeServiceDAG(ir.RestartPolicyUnlessStopped, run(ir.Aborted), false,
			func() (bool, error) { return false, errors.New("boom") })
		require.Error(t, err)
	})

	t.Run("StopCheckSkippedWhenNotNeeded", func(t *testing.T) {
		t.Parallel()
		calls := 0
		for _, tt := range []struct {
			policy ir.RestartPolicy
			status ir.Status
		}{
			{ir.RestartPolicyAlways, ir.Succeeded},
			{ir.RestartPolicyUnlessStopped, ir.Running},
		} {
			_, err := DescribeServiceDAG(tt.policy, run(tt.status), false,
				func() (bool, error) { calls++; return true, nil })
			require.NoError(t, err)
		}
		assert.Zero(t, calls)
	})
}

// The scheduler waits longer while the runs in a row fail soon after they
// start, so a crashing DAG does not restart restart_wait_sec after its end.
func TestNextRestart(t *testing.T) {
	t.Parallel()

	end := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	run := func(status ir.Status, ranFor time.Duration) ir.DAGRunStatus {
		return ir.DAGRunStatus{
			Status:     status,
			StartedAt:  end.Add(-ranFor).Format(time.RFC3339),
			FinishedAt: end.Format(time.RFC3339),
		}
	}
	crash := run(ir.Failed, time.Second)
	crashes := make([]ir.DAGRunStatus, 20)
	for i := range crashes {
		crashes[i] = crash
	}

	tests := []struct {
		name  string
		ended []ir.DAGRunStatus
		wait  time.Duration
	}{
		{name: "Succeeded", ended: []ir.DAGRunStatus{run(ir.Succeeded, time.Second)}, wait: 5 * time.Second},
		{name: "FailedAfterAWhile", ended: []ir.DAGRunStatus{run(ir.Failed, time.Minute)}, wait: 5 * time.Second},
		{name: "OneCrash", ended: []ir.DAGRunStatus{crash}, wait: 5 * time.Second},
		{name: "ThreeCrashes", ended: []ir.DAGRunStatus{crash, crash, crash}, wait: 20 * time.Second},
		{name: "CrashAfterASuccess", ended: []ir.DAGRunStatus{crash, run(ir.Succeeded, time.Second), crash}, wait: 5 * time.Second},
		{name: "ManyCrashes", ended: crashes, wait: restartBackoffMaxWait},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := NextRestart(5*time.Second, tt.ended)
			require.True(t, ok)
			// The end is stored to the second, so a second more is waited.
			assert.Equal(t, end.Add(time.Second+tt.wait), got)
		})
	}

	_, ok := NextRestart(5*time.Second, nil)
	assert.False(t, ok, "a DAG without an ended run has no restart time")
}
