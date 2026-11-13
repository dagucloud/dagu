// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/slotindex"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/command"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The slot history a backfill reads keeps what retention removed and forgets
// what an operator deleted.
func TestLoadHistoryThroughRunLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	repository := testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{
		SlotIndex: slotindex.New(filepath.Join(dir, "slot-index")),
		Location:  time.UTC,
	})
	dag, err := spec.LoadYAML(ctx, []byte("name: etl\nschedule: \"0 2 * * *\"\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	write := func(runID string, slot time.Time, status ir.Status) {
		t.Helper()
		// Runs are created in slot order, which retention follows.
		attempt, err := repository.CreateAttempt(ctx, dag, slot, runID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, attempt.Open(ctx))
		require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
			Name: dag.Name, DAGRunID: runID, AttemptID: attempt.ID(), Status: status,
			ScheduleTime: slot.Format(time.RFC3339), TriggerType: ir.TriggerTypeScheduler,
			StartedAt: "2026-11-01T00:00:00Z",
		}))
		require.NoError(t, attempt.Close(ctx))
	}
	write("retired", at(1, 2), ir.Succeeded)
	write("deleted", at(2, 2), ir.Succeeded)
	write("failed", at(3, 2), ir.Failed)

	require.NoError(t, repository.RemoveDAGRun(ctx, ir.NewDAGRunRef(dag.Name, "deleted"), persis.DAGRunRemoveOptions{}))
	removed, err := repository.RemoveOldDAGRuns(ctx, dag.Name, 0, persis.DAGRunRetentionOptions{RetentionRuns: new(1)})
	require.NoError(t, err)
	require.Equal(t, []string{"retired"}, removed)

	sched := backfill.SlotSchedule{Schedules: dag.Schedule, Location: time.UTC}
	slots := []time.Time{at(1, 2), at(2, 2), at(3, 2), at(4, 2)}
	history, err := backfill.LoadHistory(ctx, repository, dag, sched, slots[0], slots[len(slots)-1])
	require.NoError(t, err)

	var got []backfill.SlotStatus
	for _, state := range backfill.SlotStates(slots, sched, history) {
		got = append(got, state.Status)
	}
	assert.Equal(t, []backfill.SlotStatus{
		backfill.SlotSucceeded, backfill.SlotMissing, backfill.SlotFailed, backfill.SlotMissing,
	}, got)
}
