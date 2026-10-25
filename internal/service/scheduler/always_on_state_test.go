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

func TestDescribeAlwaysOn(t *testing.T) {
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
		want      AlwaysOnState
	}{
		{name: "NoRun", latest: &ir.DAGRunStatus{}, want: AlwaysOnStatePending},
		{name: "NoRunSuspended", latest: &ir.DAGRunStatus{}, suspended: true, want: AlwaysOnStateSuspended},
		{name: "Running", latest: run(ir.Running), want: AlwaysOnStateRunning},
		{name: "RunningWhileSuspended", latest: run(ir.Running), suspended: true, want: AlwaysOnStateRunning},
		{name: "WaitingApproval", latest: run(ir.Waiting), want: AlwaysOnStateRunning},
		{name: "Queued", latest: run(ir.Queued), want: AlwaysOnStatePending},
		{name: "Succeeded", latest: run(ir.Succeeded), want: AlwaysOnStateRestarting},
		{name: "PartiallySucceeded", latest: run(ir.PartiallySucceeded), want: AlwaysOnStateRestarting},
		{name: "Failed", latest: run(ir.Failed), want: AlwaysOnStateFailing},
		{name: "SucceededSuspended", latest: run(ir.Succeeded), suspended: true, want: AlwaysOnStateSuspended},
		{name: "FailedSuspended", latest: run(ir.Failed), suspended: true, want: AlwaysOnStateSuspended},
		{
			name: "StoppedByOperator", policy: ir.RestartPolicyUnlessStopped,
			latest: run(ir.Aborted), stop: stopped, want: AlwaysOnStateStopped,
		},
		{
			name: "AbortedWithoutStopRequest", policy: ir.RestartPolicyUnlessStopped,
			latest: run(ir.Aborted), stop: notStopped, want: AlwaysOnStateFailing,
		},
		{
			name: "AlwaysIgnoresStop", policy: ir.RestartPolicyAlways,
			latest: run(ir.Aborted), stop: stopped, want: AlwaysOnStateFailing,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := DescribeAlwaysOn(tt.policy, tt.latest, tt.suspended, tt.stop)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("StopCheckFails", func(t *testing.T) {
		t.Parallel()
		_, err := DescribeAlwaysOn(ir.RestartPolicyUnlessStopped, run(ir.Aborted), false,
			func() (bool, error) { return false, errors.New("boom") })
		require.Error(t, err)
	})

	t.Run("StopCheckSkippedWhenNotNeeded", func(t *testing.T) {
		t.Parallel()
		calls := 0
		_, err := DescribeAlwaysOn(ir.RestartPolicyUnlessStopped, run(ir.Failed), false,
			func() (bool, error) { calls++; return true, nil })
		require.NoError(t, err)
		assert.Zero(t, calls)
	})
}
