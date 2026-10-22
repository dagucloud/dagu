// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue_test

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/test/intgharness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restartQuiet is long enough for a restart to be enqueued and dispatched once
// the one second restart wait of the fixtures has elapsed.
const restartQuiet = 5 * time.Second

// serviceDAGYAML returns a DAG that the scheduler keeps alive. Each test uses
// its own DAG name because the socket of a run is named after its DAG.
func serviceDAGYAML(name, policy, script string) string {
	return fmt.Sprintf(`name: %s
restart_policy: %s
restart_wait_sec: 1
hist_retention_runs: 100
steps:
  - name: main
    run: |
%s
`, name, policy, indentQueueTestScript(script, 6))
}

// runs returns the runs of the fixture DAG, newest first.
func (f *fixture) runs() []ir.DAGRunStatus {
	f.t.Helper()
	runs, err := f.th.DAGRunRepository.RecentStatuses(f.th.Context, f.dag.Name, 100)
	require.NoError(f.t, err)
	slices.SortStableFunc(runs, func(a, b ir.DAGRunStatus) int {
		return cmp.Compare(b.CreatedAt, a.CreatedAt)
	})
	return runs
}

// waitForRuns waits until the runs of the fixture DAG satisfy cond.
func (f *fixture) waitForRuns(label string, timeout time.Duration, cond func([]ir.DAGRunStatus) bool) []ir.DAGRunStatus {
	f.t.Helper()
	var runs []ir.DAGRunStatus
	f.h.Wait.EventuallyEveryWithin(label, queueTestTimeout(timeout), 100*time.Millisecond, func() bool {
		runs = f.runs()
		return cond(runs)
	})
	return runs
}

func countStatus(runs []ir.DAGRunStatus, status ir.Status) int {
	n := 0
	for _, run := range runs {
		if run.Status == status {
			n++
		}
	}
	return n
}

func TestRestartPolicyRestartsEndedRuns(t *testing.T) {
	for _, tt := range []struct {
		name   string
		script string
		status ir.Status
	}{
		{"Succeeded", "echo ok", ir.Succeeded},
		{"Failed", "exit 1", ir.Failed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, serviceDAGYAML("restart-ended-"+tt.name, "always", tt.script)).
				StartScheduler(60 * time.Second)
			defer f.Stop()

			runs := f.waitForRuns("three ended runs", 45*time.Second, func(runs []ir.DAGRunStatus) bool {
				return countStatus(runs, tt.status) >= 3
			})

			ended := slices.DeleteFunc(slices.Clone(runs), func(r ir.DAGRunStatus) bool {
				return r.Status != tt.status
			})
			for i := 0; i+1 < len(ended); i++ {
				next, previous := ended[i], ended[i+1]
				assert.Equal(t, ir.TriggerTypeRestart, next.TriggerType)
				started, err := time.Parse(time.RFC3339, next.StartedAt)
				require.NoError(t, err)
				finished, err := time.Parse(time.RFC3339, previous.FinishedAt)
				require.NoError(t, err)
				assert.GreaterOrEqual(t, started.Sub(finished), time.Second,
					"run %s started before the restart wait after %s", next.DAGRunID, previous.DAGRunID)
			}
		})
	}
}

func TestRestartPolicyKeepsOneRun(t *testing.T) {
	release := filepath.Join(t.TempDir(), "release")
	f := newFixture(t, serviceDAGYAML("restart-one-run", "always", intgharness.PortableCommands().WaitForFile(release))).
		StartScheduler(60 * time.Second)
	defer f.Stop()
	defer func() { _ = os.WriteFile(release, nil, 0600) }()

	f.waitForRuns("a running run", 20*time.Second, func(runs []ir.DAGRunStatus) bool {
		return len(runs) >= 1 && runs[0].Status == ir.Running
	})
	time.Sleep(restartQuiet)
	assert.Len(t, f.runs(), 1, "no run starts while one is running")

	require.NoError(t, os.WriteFile(release, nil, 0600))
	f.waitForRuns("a run after the first one ended", 20*time.Second, func(runs []ir.DAGRunStatus) bool {
		return len(runs) >= 2
	})
}

func TestRestartPolicyStop(t *testing.T) {
	for _, tt := range []struct {
		policy    string
		restarted bool
	}{
		{"always", true},
		{"unless-stopped", false},
	} {
		t.Run(tt.policy, func(t *testing.T) {
			release := filepath.Join(t.TempDir(), "release")
			f := newFixture(t, serviceDAGYAML("restart-stop-"+tt.policy, tt.policy, intgharness.PortableCommands().WaitForFile(release))).
				StartScheduler(60 * time.Second)
			defer f.Stop()
			defer func() { _ = os.WriteFile(release, nil, 0600) }()

			f.waitForRuns("a running run", 20*time.Second, func(runs []ir.DAGRunStatus) bool {
				return len(runs) >= 1 && runs[0].Status == ir.Running
			})
			require.NoError(t, f.th.DAGRunMgr.Stop(f.th.Context, f.dag, ""))
			f.waitForRuns("the stopped run", 20*time.Second, func(runs []ir.DAGRunStatus) bool {
				return countStatus(runs, ir.Aborted) == 1
			})
			require.NoError(t, os.WriteFile(release, nil, 0600))

			if tt.restarted {
				f.waitForRuns("a run after the stop", 20*time.Second, func(runs []ir.DAGRunStatus) bool {
					return len(runs) >= 2 && runs[0].Status != ir.Aborted
				})
				return
			}
			time.Sleep(restartQuiet)
			runs := f.runs()
			require.Len(t, runs, 1, "a stopped run is not restarted")
			assert.Equal(t, ir.Aborted, runs[0].Status)
		})
	}
}

// A restart that is queued when the DAG is suspended is dropped as aborted. It
// was not stopped by an operator, so the DAG restarts once it is resumed.
func TestRestartPolicyResumesDroppedRestart(t *testing.T) {
	f := newFixture(t, serviceDAGYAML("restart-dropped", "unless-stopped", "echo ok"))
	require.NoError(t, f.th.DAGRepository.SetSuspended(f.th.Context, f.dag.FileName(), true))
	runID := f.enqueueByScheduler(ir.TriggerTypeRestart, time.Now())

	f.StartScheduler(60 * time.Second)
	defer f.Stop()

	f.WaitForStatus(runID, ir.Aborted, 20*time.Second)
	time.Sleep(restartQuiet)
	require.Len(t, f.runs(), 1, "a suspended DAG gets no new run")

	require.NoError(t, f.th.DAGRepository.SetSuspended(f.th.Context, f.dag.FileName(), false))
	f.waitForRuns("a run after the DAG is resumed", 20*time.Second, func(runs []ir.DAGRunStatus) bool {
		return countStatus(runs, ir.Succeeded) >= 1
	})
}

// Dequeuing a restart counts as an operator stop: an unless-stopped DAG stays
// stopped, and an always DAG goes on with a new run.
func TestRestartPolicyDequeue(t *testing.T) {
	for _, tt := range []struct {
		policy    string
		restarted bool
	}{
		{"always", true},
		{"unless-stopped", false},
	} {
		t.Run(tt.policy, func(t *testing.T) {
			f := newFixture(t, serviceDAGYAML("restart-dequeue-"+tt.policy, tt.policy, "echo ok"))
			runID := f.enqueueByScheduler(ir.TriggerTypeRestart, time.Now())
			f.th.RunCommand(t, cmd.Dequeue(), test.CmdTest{
				Args: []string{"dequeue", f.queue, "--dag-run", f.dag.Name + ":" + runID},
			})

			f.StartScheduler(60 * time.Second)
			defer f.Stop()

			var runs []ir.DAGRunStatus
			if tt.restarted {
				runs = f.waitForRuns("a run after the dequeued one", 20*time.Second, func(runs []ir.DAGRunStatus) bool {
					return countStatus(runs, ir.Succeeded) >= 1
				})
			} else {
				time.Sleep(restartQuiet)
				runs = f.runs()
				require.Len(t, runs, 1, "a dequeued restart is not queued again")
			}
			dequeued := runs[len(runs)-1]
			assert.Equal(t, runID, dequeued.DAGRunID)
			assert.Equal(t, ir.Aborted, dequeued.Status, "the dequeued run stays in the history")
		})
	}
}

// A run aborted because a DAG-level precondition is not met was not stopped by
// an operator, so the DAG keeps checking its precondition.
func TestRestartPolicyRetriesUnmetPrecondition(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	condition := test.ForOS(
		"test -f "+test.PosixQuote(ready),
		"if (Test-Path "+test.PowerShellQuote(ready)+") { exit 0 } else { exit 1 }",
	)
	f := newFixture(t, fmt.Sprintf(`name: restart-precondition
restart_policy: unless-stopped
restart_wait_sec: 1
hist_retention_runs: 100
preconditions:
  - condition: %q
steps:
  - name: main
    run: echo ok
`, condition)).StartScheduler(60 * time.Second)
	defer f.Stop()

	f.waitForRuns("two runs aborted by the precondition", 30*time.Second, func(runs []ir.DAGRunStatus) bool {
		return countStatus(runs, ir.Aborted) >= 2
	})
	require.NoError(t, os.WriteFile(ready, nil, 0600))
	f.waitForRuns("a run once the precondition is met", 30*time.Second, func(runs []ir.DAGRunStatus) bool {
		return countStatus(runs, ir.Succeeded) >= 1
	})
}

// A run whose process is killed leaves a running status behind. The zombie
// detector marks it failed, and the DAG is then restarted, once and only after
// that: a stale status keeps the DAG busy until it is repaired.
func TestRestartPolicyRestartsAfterRunProcessIsKilled(t *testing.T) {
	release := filepath.Join(t.TempDir(), "release")
	f := newFixture(t,
		serviceDAGYAML("restart-killed", "unless-stopped", intgharness.PortableCommands().WaitForFile(release)),
		WithProcConfig(queueTestProcHeartbeatInterval, queueTestProcStaleThreshold),
		WithZombieConfig(50*time.Millisecond, 1),
	).StartScheduler(60 * time.Second)
	defer f.Stop()
	defer func() { _ = os.WriteFile(release, nil, 0600) }()

	runs := f.waitForRuns("a running run with a process", 20*time.Second, func(runs []ir.DAGRunStatus) bool {
		return len(runs) >= 1 && runs[0].Status == ir.Running && runs[0].PID > 0
	})
	killed := runs[0]

	process, err := os.FindProcess(int(killed.PID))
	require.NoError(t, err)
	require.NoError(t, process.Kill())

	runs = f.waitForRuns("the killed run repaired and a restart running", 30*time.Second, func(runs []ir.DAGRunStatus) bool {
		return len(runs) >= 2 && runs[0].Status == ir.Running && runs[0].DAGRunID != killed.DAGRunID
	})
	assert.Equal(t, ir.TriggerTypeRestart, runs[0].TriggerType)

	var repaired *ir.DAGRunStatus
	for i := range runs {
		if runs[i].DAGRunID == killed.DAGRunID {
			repaired = &runs[i]
		}
	}
	require.NotNil(t, repaired)
	assert.Equal(t, ir.Failed, repaired.Status, "the killed run is marked failed, not left running")

	time.Sleep(restartQuiet)
	assert.Len(t, f.runs(), 2, "one restart follows the killed run")
}

func TestRestartPolicyLeavesOtherDAGsAlone(t *testing.T) {
	f := newFixture(t, `name: restart-plain
steps:
  - name: main
    run: echo ok
`).StartScheduler(30 * time.Second)
	defer f.Stop()

	time.Sleep(restartQuiet)
	assert.Empty(t, f.runs())
}
