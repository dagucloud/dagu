// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/test/intgharness"
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
	dispatchStub := func(ctx context.Context, dag *ir.DAG, runID string, trigger ir.TriggerType, scheduleTime time.Time) error {
		attempt, err := th.DAGRunStore.CreateAttempt(ctx, dag, scheduleTime, runID, dagrun.CreateAttemptOptions{})
		if err != nil {
			return err
		}

		status := ir.NewStatusBuilder(dag).Create(
			runID,
			ir.Succeeded,
			0,
			scheduleTime,
			ir.WithAttemptID(attempt.ID()),
			ir.WithHierarchyRefs(ir.NewDAGRunRef(dag.Name, runID), ir.DAGRunRef{}),
			ir.WithFinishedAt(scheduleTime.Add(time.Second)),
			ir.WithScheduleTime(stringutil.FormatTime(scheduleTime)),
			ir.WithTriggerType(trigger),
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

	require.NoError(t, th.DAGRepository.SetSuspended(th.Context, dagName, true))
	require.NoError(t, os.WriteFile(dagFile, []byte(issue2042DAGSpec(dagName, "43 * * * *")), 0o600))
	require.NoError(t, th.DAGRepository.SetSuspended(th.Context, dagName, false))

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
