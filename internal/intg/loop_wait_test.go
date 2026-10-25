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

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
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
	return fmt.Sprintf(`restart_policy: always
restart_wait_sec: 1
loop_wait_sec: 1
%ssteps:
  - %s
`, strings.Join(extra, ""), step)
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
