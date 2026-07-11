// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test/intgharness"
	"github.com/stretchr/testify/require"
)

func TestIssue2378_ParallelInlineSubDAGsStayRunningWhileClaimIsAlive(t *testing.T) {
	heartbeatThreshold := testStaleHeartbeatThreshold
	leaseThreshold := testStaleLeaseThreshold
	statusTimeout := 20 * time.Second
	if runtime.GOOS == "windows" {
		heartbeatThreshold = 12 * time.Second
		leaseThreshold = 20 * time.Second
		statusTimeout = 90 * time.Second
	}

	releaseFile := filepath.Join(t.TempDir(), "parallel-subdags.release")
	waitCommand := intgharness.PortableCommands().WaitForFile(releaseFile)
	f := newTestFixture(t, fmt.Sprintf(`
name: issue-2378-parent
worker_selector:
  test: "true"
steps:
  - name: call-child-a
    call: issue-2378-child-a
  - name: call-child-b
    call: issue-2378-child-b
---
name: issue-2378-child-a
steps:
  - name: wait
    command: |
%s
---
name: issue-2378-child-b
steps:
  - name: wait
    command: |
%s
`, indentYAMLBlock(waitCommand, 6), indentYAMLBlock(waitCommand, 6)),
		withStaleThresholds(heartbeatThreshold, leaseThreshold),
		withZombieDetectionInterval(testZombieDetectorInterval),
	)
	defer func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0o600)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			status, err := f.latestStatus()
			if err == nil && !status.Status.IsActive() {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		f.cleanup()
	}()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(statusTimeout)

	var rootRef exec.DAGRunRef
	var subRuns []exec.DAGRunStatus
	require.Eventually(t, func() bool {
		rootStatus, err := f.latestStatus()
		if err != nil || rootStatus.Status != core.Running || len(rootStatus.Nodes) != 2 {
			return false
		}

		currentSubRuns := make([]exec.DAGRunStatus, 0, len(rootStatus.Nodes))
		currentRootRef := exec.NewDAGRunRef(rootStatus.Name, rootStatus.DAGRunID)
		for _, node := range rootStatus.Nodes {
			if node == nil || node.Status != core.NodeRunning || len(node.SubRuns) != 1 {
				return false
			}
			subStatus, err := readSubDAGRunStatus(f, currentRootRef, node.SubRuns[0].DAGRunID)
			if err != nil || subStatus == nil || subStatus.Status != core.Running || subStatus.AttemptKey == "" {
				return false
			}
			currentSubRuns = append(currentSubRuns, *subStatus)
		}

		rootRef = currentRootRef
		subRuns = currentSubRuns
		return true
	}, distrTestTimeout(statusTimeout), 100*time.Millisecond, "both inline sub-DAGs should be running")
	require.Len(t, subRuns, 2)

	time.Sleep(leaseThreshold + 2*testZombieDetectorInterval)

	currentStatuses := make([]core.Status, 0, len(subRuns))
	currentErrors := make([]string, 0, len(subRuns))
	for _, subRun := range subRuns {
		current, err := readSubDAGRunStatus(f, rootRef, subRun.DAGRunID)
		require.NoError(t, err)
		currentStatuses = append(currentStatuses, current.Status)
		currentErrors = append(currentErrors, current.Error)
	}
	require.Equalf(t,
		[]core.Status{core.Running, core.Running},
		currentStatuses,
		"parallel sub-DAGs stopped while their worker was healthy: %v",
		currentErrors,
	)

	require.NoError(t, os.WriteFile(releaseFile, []byte("ok"), 0o600))
	finalStatus := f.waitForStatus(core.Succeeded, statusTimeout)
	require.Equal(t, core.Succeeded, finalStatus.Status)
}

func TestIssue2378_InlineSubDAGFailsWhenExecutionOwnerDies(t *testing.T) {
	releaseFile := filepath.Join(t.TempDir(), "inline-subdag.release")
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0o600)
	})

	f := newTestFixture(t, fmt.Sprintf(`
name: issue-2378-owner-parent
worker_selector:
  test: "true"
steps:
  - name: call-child
    call: issue-2378-owner-child
---
name: issue-2378-owner-child
steps:
  - name: wait
    command: |
%s
`, indentYAMLBlock(intgharness.PortableCommands().WaitForFile(releaseFile), 6)),
		withWorkerCount(0),
		withStaleThresholds(testStaleHeartbeatThreshold, testStaleLeaseThreshold),
		withZombieDetectionInterval(testZombieDetectorInterval),
	)
	defer f.cleanup()

	workerCmd, _ := startWorkerProcess(t, f, "issue-2378-owner-worker", "test=true")
	t.Cleanup(func() {
		_ = cmdutil.TerminateProcessGroup(workerCmd, cmdutil.ForceTermination())
	})

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(45 * time.Second)

	var rootRef exec.DAGRunRef
	var childRunID string
	require.Eventually(t, func() bool {
		rootStatus, err := f.latestStatus()
		if err != nil || rootStatus.Status != core.Running || len(rootStatus.Nodes) != 1 {
			return false
		}
		node := rootStatus.Nodes[0]
		if node == nil || node.Status != core.NodeRunning || len(node.SubRuns) != 1 {
			return false
		}

		rootRef = exec.NewDAGRunRef(rootStatus.Name, rootStatus.DAGRunID)
		childRunID = node.SubRuns[0].DAGRunID
		childStatus, err := readSubDAGRunStatus(f, rootRef, childRunID)
		return err == nil && childStatus != nil && childStatus.Status == core.Running
	}, distrTestTimeout(15*time.Second), 100*time.Millisecond, "inline sub-DAG should be running before its execution owner stops")

	require.NoError(t, cmdutil.TerminateProcessGroup(workerCmd, cmdutil.ForceTermination()))

	childStatus := waitForSubDAGRunStatus(t, f, rootRef, childRunID, core.Failed, 20*time.Second)
	require.Equal(t, exec.DistributedLeaseExpiredReason("issue-2378-owner-worker"), childStatus.Error)
	f.waitForStatus(core.Failed, 15*time.Second)
}
