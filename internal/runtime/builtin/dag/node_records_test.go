// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
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

	var records ir.NodeRuns
	data, err := fileutil.ReadFile(filepath.Join(dir, logpath.NodeRunsFile))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &records))
	var counts ir.NodeRunCounts
	data, err = fileutil.ReadFile(filepath.Join(dir, logpath.NodeRunsSummaryFile))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &counts))

	statuses := make([]string, 0, len(records.Runs))
	for _, run := range records.Runs {
		statuses = append(statuses, run.Node+"="+string(run.Status))
	}
	return statuses, records, counts
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
