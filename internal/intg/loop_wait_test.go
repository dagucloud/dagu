// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loopSuccessStepYAML is a step that always succeeds, whoever runs the test.
func loopSuccessStepYAML(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return directCommandYAML(t, "cmd", "/c", "exit", "0")
	}
	return directCommandYAML(t, "true")
}

func loopDAGYAML(step string, extra ...string) string {
	return loopDAGYAMLWithWait(1, step, extra...)
}

func loopDAGYAMLWithWait(waitSec int, step string, extra ...string) string {
	return fmt.Sprintf(`restart_policy: always
restart_wait_sec: 1
loop_wait_sec: %d
%ssteps:
  - %s
`, waitSec, strings.Join(extra, ""), step)
}

// runLoop starts the run of a looping DAG and returns a function that stops it
// and returns the error of the run.
func runLoop(t *testing.T, agent *test.Agent) (stop func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- agent.Run(agent.Context) }()
	return func() error {
		agent.Signal(agent.Context, os.Interrupt)
		select {
		case err := <-done:
			return err
		case <-time.After(intgTestTimeout(20 * time.Second)):
			t.Fatal("the run did not stop")
			return nil
		}
	}
}

// liveStatus returns the stored status of the latest run of the DAG. A test
// reads it while the run goes on, where Agent.Status would race with the start
// of the run.
func liveStatus(t *testing.T, th test.Helper, name string) ir.DAGRunStatus {
	t.Helper()
	runs, err := th.DAGRunRepository.RecentStatuses(th.Context, name, 1)
	if err != nil || len(runs) == 0 {
		return ir.DAGRunStatus{}
	}
	return runs[0]
}

// With loop_wait_sec, the steps run again in the same run, so the run counts
// what interrupts it and not each pass.
func TestLoopWaitRunsTheStepsAgainInOneRun(t *testing.T) {
	th := test.Setup(t)
	dag := th.DAG(t, loopDAGYAML(loopSuccessStepYAML(t)))
	agent := dag.Agent()
	stop := runLoop(t, agent)

	require.Eventually(t, func() bool {
		return liveStatus(t, th, dag.Name).Cycle >= 3
	}, intgTestTimeout(30*time.Second), 100*time.Millisecond, "the run should reach its third cycle")

	// A cycle ended and the next has not started, but the run has not ended.
	running := liveStatus(t, th, dag.Name)
	assert.Equal(t, ir.Running, running.Status)
	assert.Empty(t, running.FinishedAt)
	assert.Zero(t, running.FailedCycles)

	_ = stop()

	// A stop is an abort of the run, so unless-stopped sees it.
	final := agent.Status(agent.Context)
	assert.Equal(t, ir.Aborted, final.Status)
	assert.GreaterOrEqual(t, final.Cycle, 3)

	runs, err := th.DAGRunRepository.ListStatuses(th.Context, persis.DAGRunListOptions{
		ExactName:  dag.Name,
		AllHistory: true,
	})
	require.NoError(t, err)
	assert.Len(t, runs, 1, "every cycle is part of the one run")
}

// A stop during the wait between two cycles ends the run when it comes, so the
// restart policy waits from the stop and not from the end of the last cycle.
func TestLoopWaitStopDuringTheWaitEndsTheRunThen(t *testing.T) {
	th := test.Setup(t)
	dag := th.DAG(t, loopDAGYAMLWithWait(60, loopSuccessStepYAML(t)))
	agent := dag.Agent()
	stop := runLoop(t, agent)

	require.Eventually(t, func() bool {
		status := liveStatus(t, th, dag.Name)
		return status.Status == ir.Running && len(status.Nodes) == 1 &&
			status.Nodes[0].Status == ir.NodeSucceeded
	}, intgTestTimeout(30*time.Second), 50*time.Millisecond, "the run should wait for its second cycle")

	// The stop comes well after the cycle ended.
	time.Sleep(100 * time.Millisecond)
	stoppedAt := time.Now().Truncate(time.Millisecond)
	_ = stop()

	final := agent.Status(agent.Context)
	require.Equal(t, ir.Aborted, final.Status)
	finishedAt, err := stringutil.ParseTime(final.FinishedAt)
	require.NoError(t, err)
	assert.False(t, finishedAt.Before(stoppedAt), "the run should end when it was stopped")
}

// Each cycle executes the child DAG of a step again, rather than finding the
// child run of an earlier cycle.
func TestLoopWaitRunsAChildDAGInEachCycle(t *testing.T) {
	dir := filepath.ToSlash(t.TempDir())
	th := test.Setup(t)
	dag := th.DAG(t, loopDAGYAML(`action: dag.run
    with:
      dag: loop-child`)+`
---

name: loop-child
steps:
  - action: file.write
    with:
      path: "`+dir+`/${DAG_RUN_ID}"
      content: done
`)
	agent := dag.Agent()
	stop := runLoop(t, agent)

	// The third cycle starts after the second ended.
	require.Eventually(t, func() bool {
		return liveStatus(t, th, dag.Name).Cycle >= 3
	}, intgTestTimeout(30*time.Second), 100*time.Millisecond, "the run should reach its third cycle")
	_ = stop()

	// The child writes a file named after its run.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 2, "each cycle should run the child")
}

// A retry goes on with the cycle it retries: a retry of a step inside the
// child run of that cycle finds the child run, and the cycles after it run
// their own children.
func TestLoopWaitRetryOfAChildStepGoesOnWithItsCycle(t *testing.T) {
	th := test.Setup(t)
	dir := t.TempDir()
	seen := filepath.Join(dir, "seen")
	ready := filepath.Join(dir, "ready")
	// The child succeeds in the first cycle and fails in the second, until it
	// is ready.
	work := test.ForOS(
		fmt.Sprintf("if [ -f %[1]s ]; then exit 0; fi\nif [ -f %[2]s ]; then exit 1; fi\ntouch %[2]s",
			test.PosixQuote(test.ShellPath(ready)), test.PosixQuote(test.ShellPath(seen))),
		fmt.Sprintf("if (Test-Path %[1]s) { exit 0 }\nif (Test-Path %[2]s) { exit 1 }\nNew-Item -ItemType File -Path %[2]s -Force | Out-Null",
			test.PowerShellQuote(ready), test.PowerShellQuote(seen)),
	)
	dag := th.DAG(t, loopDAGYAML(`action: dag.run
    with:
      dag: loop-child`)+fmt.Sprintf(`
---

name: loop-child
shell: %s
steps:
  - name: work
    run: |
%s
`, test.ForOS("sh", "powershell"), indentCommandBlock(work, 6)))

	initial := dag.Agent()
	initial.RunError(t)
	status := initial.Status(th.Context)
	require.Equal(t, 2, status.Cycle)
	require.Len(t, status.Nodes, 1)
	require.Len(t, status.Nodes[0].SubRuns, 1)

	rootRef := ir.NewDAGRunRef(dag.Name, status.DAGRunID)
	child := status.Nodes[0].SubRuns[0].DAGRunID
	path, _, err := th.DAGRunRepository.ResolveRetryPath(th.Context, rootRef, child, "work")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(ready, nil, 0o600))

	retry := dag.Agent(test.WithAgentOptions(agent.Options{
		RetryTarget: &status,
		StepRetry:   path.RootStep(),
		RetryPath:   path,
	}))
	stop := runLoop(t, retry)

	// A cycle that fails ends the run, so the fourth cycle starts only after
	// the retried cycle and the one after it succeeded.
	require.Eventually(t, func() bool {
		return liveStatus(t, th, dag.Name).Cycle >= 4
	}, intgTestTimeout(30*time.Second), 100*time.Millisecond, "the run should loop after the retry")
	_ = stop()

	childStatus, _ := readChildRun(t, th, rootRef, child)
	assert.Equal(t, ir.Succeeded, childStatus, "the retry should run the child step again")
}

// By default a cycle that fails ends the run, so the restart policy starts a
// new one and the new run counts the interruption.
func TestLoopWaitEndsTheRunAfterAFailedCycle(t *testing.T) {
	th := test.Setup(t)
	dag := th.DAG(t, loopDAGYAML(portableDirectFailureStepYAML(t)))
	agent := dag.Agent()

	done := make(chan error, 1)
	go func() { done <- agent.Run(agent.Context) }()
	select {
	case err := <-done:
		require.Error(t, err, "the failed cycle is the error of the run")
	case <-time.After(intgTestTimeout(20 * time.Second)):
		t.Fatal("the run should end with the failed cycle")
	}

	status := agent.Status(agent.Context)
	assert.Equal(t, ir.Failed, status.Status)
	assert.Equal(t, 1, status.Cycle)
	assert.Equal(t, 1, status.FailedCycles)
	assert.NotEmpty(t, status.FinishedAt)
}

// A successful cycle does not end the run, whatever loop_on_failure says.
func TestLoopWaitOnFailureRestartKeepsLoopingWhileCyclesSucceed(t *testing.T) {
	th := test.Setup(t)
	dag := th.DAG(t, loopDAGYAML(loopSuccessStepYAML(t), "loop_on_failure: restart\n"))
	agent := dag.Agent()
	stop := runLoop(t, agent)

	require.Eventually(t, func() bool {
		return liveStatus(t, th, dag.Name).Cycle >= 2
	}, intgTestTimeout(30*time.Second), 100*time.Millisecond)

	_ = stop()
}

// With loop_on_failure set to continue, a cycle that fails does not end the
// loop, as in a while-true loop.
func TestLoopWaitContinuesAfterAFailedCycleWhenAsked(t *testing.T) {
	th := test.Setup(t)
	dag := th.DAG(t, loopDAGYAML(portableDirectFailureStepYAML(t), "loop_on_failure: continue\n"))
	agent := dag.Agent()
	stop := runLoop(t, agent)

	require.Eventually(t, func() bool {
		status := liveStatus(t, th, dag.Name)
		return status.Cycle >= 3 && status.FailedCycles >= 2
	}, intgTestTimeout(30*time.Second), 100*time.Millisecond)

	assert.Equal(t, ir.Running, liveStatus(t, th, dag.Name).Status)

	_ = stop()
}

// The logs of earlier cycles are removed, so a run that loops for months does
// not fill the disk, but the logs of the latest failed cycle stay.
func TestLoopWaitRemovesTheLogsOfEarlierCycles(t *testing.T) {
	th := test.Setup(t)
	dag := th.DAG(t, loopDAGYAML(loopSuccessStepYAML(t)))
	agent := dag.Agent()
	stop := runLoop(t, agent)

	var firstLog string
	require.Eventually(t, func() bool {
		status := liveStatus(t, th, dag.Name)
		if status.Cycle == 1 && len(status.Nodes) == 1 && status.Nodes[0].Stdout != "" {
			firstLog = status.Nodes[0].Stdout
		}
		return status.Cycle >= 3
	}, intgTestTimeout(30*time.Second), 50*time.Millisecond)
	require.NotEmpty(t, firstLog, "the first cycle should have a log")

	_ = stop()

	_, err := os.Stat(firstLog)
	assert.True(t, os.IsNotExist(err), "the log of the first cycle should be gone")
	last := agent.Status(agent.Context)
	require.Len(t, last.Nodes, 1)
	_, err = os.Stat(last.Nodes[0].Stdout)
	assert.NoError(t, err, "the log of the cycle that ended last should stay")
}

// A run that loops appends a status at each step, so the file of a run that
// lasts for weeks would grow without end. It is compacted between cycles.
func TestLoopWaitCompactsTheStatusFile(t *testing.T) {
	th := test.Setup(t)
	dag := th.DAG(t, loopDAGYAML(loopSuccessStepYAML(t)))
	agent := dag.Agent()
	stop := runLoop(t, agent)

	require.Eventually(t, func() bool {
		return liveStatus(t, th, dag.Name).Cycle >= 5
	}, intgTestTimeout(40*time.Second), 100*time.Millisecond)

	var files []string
	require.NoError(t, filepath.WalkDir(th.Config.Paths.DAGRunsDir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Name() == "status.jsonl" {
			files = append(files, path)
		}
		return nil
	}))
	require.Len(t, files, 1)
	data, err := os.ReadFile(files[0])
	require.NoError(t, err)
	// Five cycles appended more than ten statuses; compaction keeps the file to
	// the lines of the cycle in progress.
	assert.LessOrEqual(t, strings.Count(string(data), "\n"), 6)

	_ = stop()
}
