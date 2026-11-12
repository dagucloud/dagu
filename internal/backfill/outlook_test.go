// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recentRuns is a history whose recent successful runs are fixed.
type recentRuns []*ir.DAGRunStatus

func (r recentRuns) SlotEvents(context.Context, *ir.DAG, time.Time, time.Time) ([]persis.SlotEvent, error) {
	return nil, nil
}

func (r recentRuns) ListStatuses(context.Context, persis.DAGRunListOptions) ([]*ir.DAGRunStatus, error) {
	return r, nil
}

func ranFor(minutes int) *ir.DAGRunStatus {
	started := time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)
	return &ir.DAGRunStatus{
		Status:     ir.Succeeded,
		StartedAt:  started.Format(time.RFC3339),
		FinishedAt: started.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339),
	}
}

func TestLoadOutlook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dag := &ir.DAG{Name: "etl"}

	history := recentRuns{ranFor(30), ranFor(10), {Status: ir.Succeeded}, ranFor(12)}
	outlook, err := backfill.LoadOutlook(ctx, history, dag, 4, 2)
	require.NoError(t, err)
	assert.Equal(t, 4, outlook.MaxConcurrency)
	assert.Equal(t, 2, outlook.Concurrency, "the queue's limit caps the backfill")
	assert.Equal(t, "etl", outlook.QueueName)
	assert.Equal(t, 12*time.Minute, outlook.RunDuration, "the median of the runs with both times")
	assert.Equal(t, 48*time.Minute, outlook.Estimate(7), "four rounds of two runs")
	assert.True(t, outlook.FillsQueue())

	outlook, err = backfill.LoadOutlook(ctx, recentRuns{ranFor(10), ranFor(20)}, dag, 1, 3)
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, outlook.RunDuration)
	assert.False(t, outlook.FillsQueue(), "the queue keeps places for live runs")

	outlook, err = backfill.LoadOutlook(ctx, recentRuns{}, dag, 1, 1)
	require.NoError(t, err)
	assert.Zero(t, outlook.Estimate(5), "no estimate without history")
}

func TestPlanWarningsOfOutlook(t *testing.T) {
	t.Parallel()

	plan := backfill.Plan{Slots: []backfill.PlanSlot{{Action: backfill.ActionRun}}}
	warnings := func(outlook backfill.Outlook) []string {
		return plan.Warnings(backfill.WarningInput{SchedulerRunning: true, Outlook: outlook})
	}

	assert.Equal(t, []string{
		"The queue etl runs 1 at once, so the backfill runs 1 at a time.",
		"Live runs may wait up to 5m behind an executing backfill run.",
	}, warnings(backfill.Outlook{MaxConcurrency: 3, Concurrency: 1, QueueName: "etl", QueueLimit: 1, RunDuration: 5 * time.Minute}))
	assert.Equal(t, []string{
		"Live runs may wait behind an executing backfill run.",
	}, warnings(backfill.Outlook{MaxConcurrency: 1, Concurrency: 1, QueueName: "etl", QueueLimit: 1}))
	assert.Empty(t, warnings(backfill.Outlook{MaxConcurrency: 1, Concurrency: 1, QueueName: "shared", QueueLimit: 2}))
}
