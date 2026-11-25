// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/noderuns"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/require"
)

// recordingStep returns a step with nodes over web01, web02 (disabled),
// web03, web04, and web05, and the context of a run that logs the step beside the
// directory its records go to.
func recordingStep(t *testing.T) (*parallelExecutor, context.Context, string) {
	t.Helper()

	logDir := t.TempDir()
	ctx := runtime.NewContext(context.Background(), &ir.DAG{Name: "deploy"}, "run-1", filepath.Join(logDir, "dag.log"))
	env := runtime.NewEnv(ctx, ir.Step{Name: "deploy"})
	env.Scope = env.Scope.WithEntries(map[string]string{
		runenv.EnvKeyDAGRunStepStdoutFile: filepath.Join(logDir, "deploy.out"),
	}, cmnvalue.EnvSourceStepEnv)

	e := nodesExecutorWith(4096, []executor.NodeRun{
		{Node: "web01", Run: executor.RunParams{RunID: "a", DAGName: "deploy-one"}},
		{Node: "web02", Disabled: true},
		{Node: "web03", Run: executor.RunParams{RunID: "c", DAGName: "deploy-one"}},
		{Node: "web04", Run: executor.RunParams{RunID: "d", DAGName: "deploy-one"}},
		{Node: "web05", Run: executor.RunParams{RunID: "e", DAGName: "deploy-one"}},
	}, map[string]*ir.RunStatus{})
	e.step = ir.Step{Name: "deploy"}
	return e, runtime.WithEnv(ctx, env), logpath.NodeRunsDir(logDir, "deploy")
}

// readNodeRecords returns each recorded node with its status, the records,
// and their counts.
func readNodeRecords(t *testing.T, dir string) ([]string, ir.NodeRuns, ir.NodeRunCounts) {
	t.Helper()

	records, err := noderuns.Read(dir)
	require.NoError(t, err)
	counts, err := noderuns.ReadCounts(dir)
	require.NoError(t, err)

	statuses := make([]string, 0, len(records.Runs))
	for _, run := range records.Runs {
		statuses = append(statuses, run.Node+"="+string(run.Status))
	}
	return statuses, records, counts
}

// A node disabled since an earlier attempt keeps that attempt's child run in
// its record and aggregate entry, so a retry that finds the node enabled
// again retries the same run. It still counts in the total only.
func TestNodeRunRecordsKeepDisabledRun(t *testing.T) {
	t.Parallel()

	e, ctx, dir := recordingStep(t)
	e.SetNodes([]executor.NodeRun{
		{Node: "web01", Disabled: true, Run: executor.RunParams{RunID: "a", DAGName: "deploy-one"}},
		{Node: "web02", Disabled: true},
	})
	var out bytes.Buffer
	e.SetStdout(&out)
	e.recordNodeRuns(ctx)()
	require.NoError(t, e.outputNodeResults())

	_, records, counts := readNodeRecords(t, dir)
	require.Equal(t, []ir.NodeRun{
		{Node: "web01", Status: ir.NodeRunDisabled, DAGName: "deploy-one", DAGRunID: "a"},
		{Node: "web02", Status: ir.NodeRunDisabled},
	}, records.Runs)
	require.Equal(t, ir.NodeRunCounts{Total: 2, Disabled: 2}, counts)

	aggregate := readNodeAggregate(t, out.Bytes())
	require.Equal(t, 2, aggregate.Summary.Total)
	require.Zero(t, aggregate.Summary.Succeeded+aggregate.Summary.Failed)
	require.Equal(t, "a", aggregate.Results[0].DAGRunID)
	require.Empty(t, aggregate.Results[1].DAGRunID)
}

// The records exist before any node starts, follow the nodes as they run,
// and hold the final statuses once the step ends.
func TestNodeRunRecords(t *testing.T) {
	t.Parallel()

	e, ctx, dir := recordingStep(t)
	finish := e.recordNodeRuns(ctx)

	statuses, _, counts := readNodeRecords(t, dir)
	require.Equal(t, []string{"web01=not_started", "web02=disabled", "web03=not_started", "web04=not_started", "web05=not_started"}, statuses)
	require.Equal(t, ir.NodeRunCounts{Total: 5, NotStarted: 4, Disabled: 1}, counts)

	e.noteNodeStart("a")
	e.lock.Lock()
	e.results["a"] = &ir.RunStatus{Status: ir.Failed, Steps: &ir.StepProgress{Done: 3, Total: 4}, FailedStep: "restart"}
	e.lock.Unlock()
	e.noteNodeFinish("a", e.results["a"], 0)
	e.noteNodeStart("c")
	// web04 returned without a result, as a child run that failed to start
	// does.
	e.noteNodeStart("d")
	e.noteNodeFinish("d", nil, 0)
	e.writeNodeRuns(ctx, dir, false)
	statuses, _, counts = readNodeRecords(t, dir)
	require.Equal(t, []string{"web01=failed", "web02=disabled", "web03=running", "web04=failed", "web05=not_started"}, statuses)
	require.Equal(t, ir.NodeRunCounts{Total: 5, NotStarted: 1, Running: 1, Failed: 2, Disabled: 1}, counts)

	// web03 was still running when the step ended, so it failed.
	finish()
	statuses, records, counts := readNodeRecords(t, dir)
	require.Equal(t, []string{"web01=failed", "web02=disabled", "web03=failed", "web04=failed", "web05=not_started"}, statuses)
	require.Equal(t, ir.NodeRunCounts{Total: 5, NotStarted: 1, Failed: 3, Disabled: 1}, counts)

	web01 := records.Runs[0]
	require.Equal(t, "deploy-one", web01.DAGName)
	require.Equal(t, "a", web01.DAGRunID)
	require.Equal(t, &ir.StepProgress{Done: 3, Total: 4}, web01.Steps)
	require.Equal(t, "restart", web01.FailedStep)
	require.NotEmpty(t, web01.StartedAt)
	require.NotEmpty(t, web01.FinishedAt)
	require.Equal(t, ir.NodeRun{Node: "web02", Status: ir.NodeRunDisabled}, records.Runs[1])
	require.Empty(t, records.Runs[4].StartedAt)
}

// The runner hears of each change of the counts once the records show it,
// so the status write it makes wakes readers that find the new counts. A
// write that leaves the counts as they were tells it nothing, and neither
// does the last write, which the end of the step follows.
func TestNodeRunRecordsReport(t *testing.T) {
	t.Parallel()

	e, ctx, dir := recordingStep(t)
	// The record ticker may make a write in place of the test.
	var mu sync.Mutex
	var reported []ir.NodeRunCounts
	(&nodesExecutor{parallelExecutor: e}).SetProgressCallback(func() {
		_, _, counts := readNodeRecords(t, dir)
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, counts)
	})
	finish := e.recordNodeRuns(ctx)

	e.noteNodeStart("a")
	e.writeNodeRuns(ctx, dir, false)
	// A failed attempt that queues a step retry leaves the node running.
	e.noteNodeFinish("a", &ir.RunStatus{Status: ir.Failed}, 1)
	e.writeNodeRuns(ctx, dir, false)
	finish()

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []ir.NodeRunCounts{
		{Total: 5, NotStarted: 4, Disabled: 1},
		{Total: 5, NotStarted: 3, Running: 1, Disabled: 1},
	}, reported)
}

// A write that fails tells the runner nothing and stays due, so the next
// write stores the records and reports their counts.
func TestNodeRunRecordsReportAfterFailedWrite(t *testing.T) {
	t.Parallel()

	e, ctx, _ := recordingStep(t)
	reports := 0
	(&nodesExecutor{parallelExecutor: e}).SetProgressCallback(func() { reports++ })
	// A file where the directory of the records goes stops their writes.
	blocked := filepath.Join(t.TempDir(), "blocked")
	require.NoError(t, os.WriteFile(blocked, nil, 0o600))
	dir := filepath.Join(blocked, "deploy")

	e.noteNodeStart("a")
	e.writeNodeRuns(ctx, dir, false)
	require.Zero(t, reports)

	require.NoError(t, os.Remove(blocked))
	e.writeNodeRuns(ctx, dir, false)
	require.Equal(t, 1, reports)
	statuses, _, _ := readNodeRecords(t, dir)
	require.Equal(t, "web01=running", statuses[0])
}

// A node runs while a step retry of it is queued. Once its last retry
// returns no result, the node failed, whatever its first attempt returned.
func TestNodeRunRecordsRetry(t *testing.T) {
	t.Parallel()

	e, ctx, dir := recordingStep(t)
	finish := e.recordNodeRuns(ctx)
	defer finish()
	web01 := func(t *testing.T) string {
		t.Helper()
		e.writeNodeRuns(ctx, dir, false)
		statuses, _, _ := readNodeRecords(t, dir)
		return statuses[0]
	}

	// The first attempt fails and queues a retry for each of two child steps.
	first := &ir.RunStatus{Status: ir.Failed, PendingStepRetries: []ir.PendingStepRetry{{StepName: "restart"}, {StepName: "check"}}}
	e.noteNodeStart("a")
	e.lock.Lock()
	e.results["a"] = first
	e.lock.Unlock()
	e.noteNodeFinish("a", first, 2)
	require.Equal(t, "web01=running", web01(t))

	e.noteNodeStart("a")
	e.noteNodeFinish("a", nil, 0)
	require.Equal(t, "web01=running", web01(t), "the second retry is still queued")

	e.noteNodeStart("a")
	e.noteNodeFinish("a", nil, 0)
	require.Equal(t, "web01=failed", web01(t))
}

// A retry that targets one node reuses the others, whose child runs return
// at once. Their records keep when the child runs ran, not when they were
// reused.
func TestNodeRunRecordsReused(t *testing.T) {
	t.Parallel()

	e, ctx, dir := recordingStep(t)
	finish := e.recordNodeRuns(ctx)
	reused := &ir.RunStatus{
		Status:     ir.Succeeded,
		StartedAt:  "2026-11-01T10:00:00.000Z",
		FinishedAt: "2026-11-01T10:05:00.000Z",
	}
	e.noteNodeStart("a")
	e.lock.Lock()
	e.results["a"] = reused
	e.lock.Unlock()
	e.noteNodeFinish("a", reused, 0)
	finish()

	_, records, _ := readNodeRecords(t, dir)
	require.Equal(t, reused.StartedAt, records.Runs[0].StartedAt)
	require.Equal(t, reused.FinishedAt, records.Runs[0].FinishedAt)
}

// A node running when the step is aborted is aborted. A node that never
// started stays not started.
func TestNodeRunRecordsAborted(t *testing.T) {
	t.Parallel()

	e, ctx, dir := recordingStep(t)
	finish := e.recordNodeRuns(ctx)
	e.noteNodeStart("a")
	e.isCanceled.Store(true)
	finish()

	statuses, _, _ := readNodeRecords(t, dir)
	require.Equal(t, []string{"web01=aborted", "web02=disabled", "web03=not_started", "web04=not_started", "web05=not_started"}, statuses)
}

// A step that has no log file has nowhere to keep records, so it keeps none.
func TestNodeRunRecordsWithoutLogFile(t *testing.T) {
	t.Parallel()

	ctx := runtime.NewContext(context.Background(), &ir.DAG{Name: "deploy"}, "run-1", "")
	require.Empty(t, nodeRunsDir(ctx, "deploy"))
}
