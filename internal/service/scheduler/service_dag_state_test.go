// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"errors"
	"testing"

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
		{name: "WaitingApproval", latest: run(ir.Waiting), want: ServiceDAGStateRunning},
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
