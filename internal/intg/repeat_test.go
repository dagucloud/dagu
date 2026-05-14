// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func repeatPolicyTimeout(base time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		if raceEnabled() {
			return intgTestTimeout(base * 6)
		}
		return intgTestTimeout(base * 4)
	}
	return base
}

func repeatExitCodeScript(counterFile string, successAfter int, removeOnSuccess bool) string {
	if runtime.GOOS == "windows" {
		removeBlock := ""
		if removeOnSuccess {
			removeBlock = "Remove-Item -Path $counterFile -Force -ErrorAction SilentlyContinue\n"
		}
		return fmt.Sprintf(`
$counterFile = %s
$count = 0
if ([System.IO.File]::Exists($counterFile)) {
  $count = [int][System.IO.File]::ReadAllText($counterFile).Trim()
}
$count++
[System.IO.File]::WriteAllText($counterFile, [string]$count)
if ($count -lt %d) {
  exit 1
}
%sexit 0
`, test.PowerShellQuote(counterFile), successAfter, removeBlock)
	}

	counterFile = test.ShellPath(counterFile)
	removeBlock := ""
	if removeOnSuccess {
		removeBlock = fmt.Sprintf("rm -f %s\n", test.PosixQuote(counterFile))
	}
	return fmt.Sprintf(`
if [ ! -f %s ]; then
  printf '%%s' "1" > %s
  echo "Count: 1"
  exit 1
fi

count=$(cat %s)
count=$((count + 1))
printf '%%s' "$count" > %s
echo "Count: $count"
if [ "$count" -lt %d ]; then
  exit 1
fi
%sexit 0
`, test.PosixQuote(counterFile), test.PosixQuote(counterFile), test.PosixQuote(counterFile), test.PosixQuote(counterFile), successAfter, removeBlock)
}

func repeatLiteralCommandSubstitution(value string) string {
	return "`" + test.Output(value) + "`"
}

func repeatPolicyParallel(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "windows" {
		t.Parallel()
	}
}

func TestRepeatPolicy_WithLimit(t *testing.T) {
	repeatPolicyParallel(t)
	th := test.Setup(t)

	// Load DAG with repeat limit
	dag := th.DAG(t, fmt.Sprintf(`steps:
  - %s
    repeat_policy:
      repeat: true
      limit: 3
      interval_sec: 0
`, portableDirectSuccessStepYAML(t)))
	agent := dag.Agent()

	// Run with timeout
	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(10*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err, "DAG should complete successfully")

	// Verify successful completion
	dag.AssertLatestStatus(t, core.Succeeded)

	// Get the latest status
	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.NotNil(t, dagRunStatus)

	// Verify the step completed successfully
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)

	// Verify it executed exactly 3 times (as per limit)
	assert.Equal(t, 3, dagRunStatus.Nodes[0].DoneCount, "Step should have executed exactly 3 times")
}

func TestRepeatPolicy_WithLimitAndCondition(t *testing.T) {
	repeatPolicyParallel(t)
	th := test.Setup(t)

	// Keep the condition present but constant so the test covers the limit path
	// without spending minutes in Windows PowerShell script startup.
	dag := th.DAG(t, fmt.Sprintf(`steps:
  - %s
    repeat_policy:
      repeat: until
      limit: 5
      interval_sec: 0
      condition: %q
      expected: "10"
`, portableDirectSuccessStepYAML(t), repeatLiteralCommandSubstitution("0")))
	agent := dag.Agent()

	// Run with timeout
	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(10*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err, "DAG should complete successfully")

	// Verify successful completion
	dag.AssertLatestStatus(t, core.Succeeded)

	// Get the latest status
	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.NotNil(t, dagRunStatus)

	// Verify the step completed successfully
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)

	// Verify it stopped at the limit (5) even though condition would continue
	assert.Equal(t, 5, dagRunStatus.Nodes[0].DoneCount, "Step should have stopped at limit of 5")
}

func TestRepeatPolicy_CommandConditionWithHomeRelativeDAGEnvVar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Unix shell test on Windows")
	}

	repeatPolicyParallel(t)
	th := test.Setup(t)
	_, homeRelativePath := posixHomeRelativeTempPath(t, ".dagu-repeat-*")

	dag := th.DAG(t, fmt.Sprintf(`env:
  - TEST_FILE: %q
steps:
  - run: touch $TEST_FILE
    repeat_policy:
      repeat: while
      condition: test ! -f $TEST_FILE
      interval_sec: 0
      limit: 2
`, homeRelativePath))
	agent := dag.Agent()

	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(10*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err, "DAG should complete successfully")

	dag.AssertLatestStatus(t, core.Succeeded)

	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.NotNil(t, dagRunStatus)

	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)
	assert.Equal(t, 1, dagRunStatus.Nodes[0].DoneCount, "Step should stop after the first run once the file exists")
}

func TestRepeatPolicy_WithLimitReachedBeforeCondition(t *testing.T) {
	repeatPolicyParallel(t)
	th := test.Setup(t)

	// Load DAG that repeats with a limit
	dag := th.DAG(t, fmt.Sprintf(`steps:
  - %s
    repeat_policy:
      repeat: true
      limit: 3
      interval_sec: 0
`, portableDirectSuccessStepYAML(t)))
	agent := dag.Agent()

	// Run with timeout
	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(10*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err, "DAG should complete successfully")

	// Verify successful completion
	dag.AssertLatestStatus(t, core.Succeeded)

	// Get the latest status
	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.NotNil(t, dagRunStatus)

	// Verify it stopped at limit (3)
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)
	assert.Equal(t, 3, dagRunStatus.Nodes[0].DoneCount, "Step should have stopped at limit of 3")
}

func TestRepeatPolicy_BooleanModeWhileUnconditional(t *testing.T) {
	repeatPolicyParallel(t)
	th := test.Setup(t)

	// Load DAG with boolean repeat mode (should repeat while step succeeds, like unconditional while)
	dag := th.DAG(t, fmt.Sprintf(`steps:
  - %s
    repeat_policy:
      repeat: true
      limit: 3
      interval_sec: 0
`, portableDirectSuccessStepYAML(t)))
	agent := dag.Agent()

	// Run with timeout
	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(10*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err, "DAG should complete successfully")

	// Verify successful completion
	dag.AssertLatestStatus(t, core.Succeeded)

	// Get the latest status
	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.NotNil(t, dagRunStatus)

	// Verify the step completed successfully
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)

	// Verify it executed exactly 3 times (as per limit)
	assert.Equal(t, 3, dagRunStatus.Nodes[0].DoneCount, "Step should have executed exactly 3 times")
}

func TestRepeatPolicy_UntilWithExitCode(t *testing.T) {
	repeatPolicyParallel(t)
	th := test.Setup(t)

	counterFile := filepath.Join(t.TempDir(), "counter")

	dag := th.DAG(t, fmt.Sprintf(`env:
  - COUNTER_FILE: %q
steps:
  - run: |
%s
    repeat_policy:
      # Using backward compatibility mode: exitCode only infers "while" mode
      # but we can test "until" behavior with explicit condition that inverts logic
      repeat: "until"
      exit_code: [0]  # Repeat until we get exit code 0
      interval_sec: 0
    continue_on:
      failure: true
      mark_success: true
      exit_code: [1]
`, counterFile, indentScript(repeatExitCodeScript(counterFile, 3, false), 6)))
	agent := dag.Agent()

	// Run with timeout
	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(15*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err, "DAG should complete successfully")

	// Verify successful completion
	dag.AssertLatestStatus(t, core.Succeeded)

	// Get the latest status
	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.NotNil(t, dagRunStatus)

	// Verify the step completed successfully
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)

	// Verify it executed exactly 3 times (until it gets exit code 0)
	assert.Equal(t, 3, dagRunStatus.Nodes[0].DoneCount, "Step should have executed exactly 3 times until exit code 0")
}

func TestRepeatPolicy_BackwardCompatibilityTrue(t *testing.T) {
	repeatPolicyParallel(t)
	th := test.Setup(t)

	// Load DAG with repeat: true (should work as "while" mode)
	dag := th.DAG(t, fmt.Sprintf(`steps:
  - %s
    repeat_policy:
      repeat: true
      limit: 4
      interval_sec: 0
`, portableDirectSuccessStepYAML(t)))
	agent := dag.Agent()

	// Run with timeout
	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(10*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err, "DAG should complete successfully")

	// Verify successful completion
	dag.AssertLatestStatus(t, core.Succeeded)

	// Get the latest status
	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.NotNil(t, dagRunStatus)

	// Verify the step completed successfully
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)

	// Verify it executed exactly 4 times (as per limit, confirming repeat: true works)
	assert.Equal(t, 4, dagRunStatus.Nodes[0].DoneCount, "Step should have executed exactly 4 times")
}

func TestRepeatPolicy_OnExitCode(t *testing.T) {
	repeatPolicyParallel(t)
	th := test.Setup(t)

	counterFile := filepath.Join(t.TempDir(), "counter")

	dag := th.DAG(t, fmt.Sprintf(`env:
  - COUNTER_FILE: %q
steps:
  - run: |
%s
    repeat_policy:
      exit_code: [1]
      limit: 5
      interval_sec: 0
`, counterFile, indentScript(repeatExitCodeScript(counterFile, 3, false), 6)))
	agent := dag.Agent()

	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(15*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err, "DAG should complete successfully")

	dag.AssertLatestStatus(t, core.Succeeded)

	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.NotNil(t, dagRunStatus)

	require.Len(t, dagRunStatus.Nodes, 1)
	nodeStatus := dagRunStatus.Nodes[0]

	assert.Equal(t, core.NodeSucceeded, nodeStatus.Status, "The final status of the node should be Success")
	assert.True(t, nodeStatus.Repeated, "The step should be marked as repeated")
	assert.GreaterOrEqual(t, nodeStatus.DoneCount, 3, "The step should have executed at least 3 times")
}

func TestRepeatPolicy_LimitFromEnvVar(t *testing.T) {
	t.Setenv("REPEAT_LIMIT", "3")
	th := test.Setup(t)

	dag := th.DAG(t, fmt.Sprintf(`steps:
  - %s
    repeat_policy:
      repeat: true
      limit: $REPEAT_LIMIT
      interval_sec: 0
`, portableDirectSuccessStepYAML(t)))
	agent := dag.Agent()

	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(10*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err)

	dag.AssertLatestStatus(t, core.Succeeded)

	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, 3, dagRunStatus.Nodes[0].DoneCount)
}

func TestRepeatPolicy_IntervalSecFromEnvVar(t *testing.T) {
	t.Setenv("INTERVAL_SEC", "0")
	th := test.Setup(t)

	dag := th.DAG(t, fmt.Sprintf(`steps:
  - %s
    repeat_policy:
      repeat: true
      limit: 3
      interval_sec: $INTERVAL_SEC
`, portableDirectSuccessStepYAML(t)))
	agent := dag.Agent()

	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(10*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err)

	dag.AssertLatestStatus(t, core.Succeeded)

	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, 3, dagRunStatus.Nodes[0].DoneCount)
}

func TestRepeatPolicy_MaxIntervalSecFromEnvVar(t *testing.T) {
	t.Setenv("MAX_INTERVAL", "10")
	th := test.Setup(t)

	dag := th.DAG(t, fmt.Sprintf(`steps:
  - %s
    repeat_policy:
      repeat: true
      limit: 2
      interval_sec: 0
      max_interval_sec: $MAX_INTERVAL
`, portableDirectSuccessStepYAML(t)))
	agent := dag.Agent()

	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(10*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err)

	dag.AssertLatestStatus(t, core.Succeeded)

	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, 2, dagRunStatus.Nodes[0].DoneCount)
}

func TestRepeatPolicy_LimitFromCommandSubstitution(t *testing.T) {
	repeatPolicyParallel(t)
	th := test.Setup(t)

	dag := th.DAG(t, fmt.Sprintf("steps:\n  - %s\n    repeat_policy:\n      repeat: true\n      limit: %q\n      interval_sec: 0\n", portableDirectSuccessStepYAML(t), repeatLiteralCommandSubstitution("3")))
	agent := dag.Agent()

	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(10*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err)

	dag.AssertLatestStatus(t, core.Succeeded)

	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, 3, dagRunStatus.Nodes[0].DoneCount)
}

func TestRepeatPolicy_MultipleDynamicFields(t *testing.T) {
	t.Setenv("DYN_LIMIT", "4")
	t.Setenv("DYN_INTERVAL", "0")
	th := test.Setup(t)

	dag := th.DAG(t, fmt.Sprintf(`steps:
  - %s
    repeat_policy:
      repeat: true
      limit: $DYN_LIMIT
      interval_sec: $DYN_INTERVAL
`, portableDirectSuccessStepYAML(t)))
	agent := dag.Agent()

	ctx, cancel := context.WithTimeout(agent.Context, repeatPolicyTimeout(10*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err)

	dag.AssertLatestStatus(t, core.Succeeded)

	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, 4, dagRunStatus.Nodes[0].DoneCount)
}

func indentScript(script string, spaces int) string {
	script = strings.TrimPrefix(script, "\n")
	lines := strings.Split(strings.TrimRight(script, "\n"), "\n")
	prefix := strings.Repeat(" ", spaces)
	return prefix + strings.Join(lines, "\n"+prefix)
}
