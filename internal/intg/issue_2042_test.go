// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/dagucloud/dagu/internal/test/intgharness"
	"github.com/stretchr/testify/require"
)

func TestIssue2042_EditedSuspendedScheduleDispatchesWithSkipIfSuccessful(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dagsDir := filepath.Join(tmpDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0o755))

	const dagName = "issue-2042-skip-if-successful"
	dagFile := filepath.Join(dagsDir, dagName+".yaml")
	require.NoError(t, os.WriteFile(dagFile, []byte(issue2042DAGSpec(dagName, "34 * * * *")), 0o600))

	th := test.SetupScheduler(t, test.WithDAGsDir(dagsDir))
	h := intgharness.New(t, th.Helper)

	dispatchedAt := make(chan time.Time, 4)
	dispatchStub := func(ctx context.Context, dag *core.DAG, runID string, trigger core.TriggerType, scheduleTime time.Time) error {
		attempt, err := th.DAGRunStore.CreateAttempt(ctx, dag, scheduleTime, runID, exec.NewDAGRunAttemptOptions{})
		if err != nil {
			return err
		}

		status := transform.NewStatusBuilder(dag).Create(
			runID,
			core.Succeeded,
			0,
			scheduleTime,
			transform.WithAttemptID(attempt.ID()),
			transform.WithHierarchyRefs(exec.NewDAGRunRef(dag.Name, runID), exec.DAGRunRef{}),
			transform.WithFinishedAt(scheduleTime.Add(time.Second)),
			transform.WithScheduleTime(exec.FormatTime(scheduleTime)),
			transform.WithTriggerType(trigger),
		)

		if err := attempt.Open(ctx); err != nil {
			return err
		}
		if err := attempt.Write(ctx, status); err != nil {
			return err
		}
		if err := attempt.Close(ctx); err != nil {
			return err
		}

		dispatchedAt <- scheduleTime
		return nil
	}

	runScheduledTick := func(tickTime time.Time) time.Time {
		t.Helper()

		schedulerInstance, err := th.NewSchedulerInstance(t)
		require.NoError(t, err)
		schedulerInstance.SetClock(func() time.Time { return tickTime })
		schedulerInstance.SetDispatchFunc(dispatchStub)

		ctx, cancel := context.WithCancel(th.Context)
		defer cancel()

		probe := h.StartScheduler(ctx, schedulerInstance, th.EntryReader)

		var dispatched time.Time
		probe.RequireEventually("expected edited schedule to dispatch", 5*time.Second, func() bool {
			select {
			case dispatched = <-dispatchedAt:
				return true
			default:
				return false
			}
		})
		probe.Stop(context.Background(), cancel, 5*time.Second)

		return dispatched
	}

	firstDispatch := runScheduledTick(time.Date(2026, 2, 7, 12, 34, 0, 0, time.UTC))
	require.Equal(t, time.Date(2026, 2, 7, 12, 34, 0, 0, time.UTC), firstDispatch)

	require.NoError(t, th.DAGStore.ToggleSuspend(th.Context, dagName, true))
	require.NoError(t, os.WriteFile(dagFile, []byte(issue2042DAGSpec(dagName, "43 * * * *")), 0o600))
	require.NoError(t, th.DAGStore.ToggleSuspend(th.Context, dagName, false))

	secondDispatch := runScheduledTick(time.Date(2026, 2, 7, 12, 43, 0, 0, time.UTC))
	require.Equal(t, time.Date(2026, 2, 7, 12, 43, 0, 0, time.UTC), secondDispatch)
}

func issue2042DAGSpec(name, schedule string) string {
	return "name: " + name + "\n" +
		"schedule: \"" + schedule + "\"\n" +
		"skip_if_successful: true\n" +
		"steps:\n" +
		"  - command: echo issue-2042\n"
}
