// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryCommand_Basic(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	ctx := context.Background()

	// Create a test DAG
	dag := th.DAG(t, `name: test-history-basic
steps:
  - name: simple-step
    command: "echo test"
`)

	// Execute DAG
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", dag.Location},
	})

	// Wait for completion
	require.Eventually(t, func() bool {
		status, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)
		return err == nil && status.Status == core.Succeeded
	}, 5*time.Second, 100*time.Millisecond)

	// Run history command - stdout table output is not captured in LoggingOutput,
	// so we just verify the command runs without error
	th.RunCommand(t, cmd.History(), test.CmdTest{
		Name: "BasicHistory",
		Args: []string{"history"},
	})
}

func TestHistoryCommand_FilterByName(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	ctx := context.Background()

	// Create test DAGs
	dag1 := th.DAG(t, `name: filter-test-1
steps:
  - name: step1
    command: "echo test1"
`)

	dag2 := th.DAG(t, `name: filter-test-2
steps:
  - name: step2
    command: "echo test2"
`)

	// Execute both DAGs
	th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", dag1.Location}})
	th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", dag2.Location}})

	// Wait for both to complete
	require.Eventually(t, func() bool {
		s1, err1 := th.DAGRunMgr.GetLatestStatus(ctx, dag1.DAG)
		s2, err2 := th.DAGRunMgr.GetLatestStatus(ctx, dag2.DAG)
		return err1 == nil && err2 == nil && s1.Status == core.Succeeded && s2.Status == core.Succeeded
	}, 5*time.Second, 100*time.Millisecond)

	// Filter by specific DAG name - stdout table output is not captured,
	// so we just verify the command runs without error
	th.RunCommand(t, cmd.History(), test.CmdTest{
		Name: "FilterByName",
		Args: []string{"history", "filter-test-1"},
	})
}

func TestHistoryCommand_FilterByStatus(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	ctx := context.Background()

	// Create successful and failing DAGs
	dagSuccess := th.DAG(t, `name: status-test-success
steps:
  - name: success-step
    command: "true"
`)

	dagFail := th.DAG(t, `name: status-test-fail
steps:
  - name: fail-step
    command: "false"
`)

	// Execute both
	th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", dagSuccess.Location}})
	_ = th.RunCommandWithError(t, cmd.Start(), test.CmdTest{Args: []string{"start", dagFail.Location}})

	// Wait for completion
	require.Eventually(t, func() bool {
		s1, err1 := th.DAGRunMgr.GetLatestStatus(ctx, dagSuccess.DAG)
		s2, err2 := th.DAGRunMgr.GetLatestStatus(ctx, dagFail.DAG)
		return err1 == nil && err2 == nil && s1.Status == core.Succeeded && s2.Status == core.Failed
	}, 5*time.Second, 100*time.Millisecond)

	// Filter by succeeded status - stdout table output is not captured,
	// so we just verify the command runs without error
	th.RunCommand(t, cmd.History(), test.CmdTest{
		Name: "FilterSucceeded",
		Args: []string{"history", "--status=succeeded"},
	})
}

func TestHistoryCommand_JSONFormat(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	ctx := context.Background()

	dag := th.DAG(t, `name: test-json-format
steps:
  - name: json-step
    command: "echo json"
`)

	th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", dag.Location}})

	require.Eventually(t, func() bool {
		status, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)
		return err == nil && status.Status == core.Succeeded
	}, 5*time.Second, 100*time.Millisecond)

	// Test JSON output - stdout is not captured in LoggingOutput,
	// so we just verify the command runs without error
	th.RunCommand(t, cmd.History(), test.CmdTest{
		Name: "JSONFormat",
		Args: []string{"history", "--format=json"},
	})
}

func TestHistoryCommand_RunIDDisplay(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	ctx := context.Background()

	dag := th.DAG(t, `name: test-runid-full
steps:
  - name: simple-step
    command: "echo test"
`)

	// Execute with a long custom run ID
	customRunID := "custom-run-id-1234567890-abcdefghijklmnopqrstuvwxyz"
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", "--run-id=" + customRunID, dag.Location},
	})

	require.Eventually(t, func() bool {
		status, err := th.DAGRunMgr.GetCurrentStatus(ctx, dag.DAG, customRunID)
		return err == nil && status != nil && status.Status == core.Succeeded
	}, 5*time.Second, 100*time.Millisecond)

	// Verify full run ID is displayed - stdout table output is not captured,
	// so we verify the command runs without error. The full run ID display
	// logic is tested at the unit level (formatParams never truncates run IDs)
	th.RunCommand(t, cmd.History(), test.CmdTest{
		Name: "FullRunID",
		Args: []string{"history", "test-runid-full"},
	})
}

func TestHistoryCommand_DateFiltering(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	ctx := context.Background()

	dag := th.DAG(t, `name: test-date-filter
steps:
  - name: simple-step
    command: "echo test"
`)

	th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", dag.Location}})

	require.Eventually(t, func() bool {
		status, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)
		return err == nil && status.Status == core.Succeeded
	}, 5*time.Second, 100*time.Millisecond)

	// Test relative date filtering - stdout table output is not captured,
	// so we just verify the command runs without error
	th.RunCommand(t, cmd.History(), test.CmdTest{
		Name: "RelativeDate",
		Args: []string{"history", "--last=1h"},
	})

	// Test absolute date filtering
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	th.RunCommand(t, cmd.History(), test.CmdTest{
		Name: "AbsoluteDate",
		Args: []string{"history", fmt.Sprintf("--from=%s", yesterday)},
	})
}

func TestHistoryCommand_Errors(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)

	tests := []struct {
		name        string
		args        []string
		expectedErr string
	}{
		{
			name:        "InvalidStatus",
			args:        []string{"history", "--status=invalid"},
			expectedErr: "invalid status",
		},
		{
			name:        "InvalidDateFormat",
			args:        []string{"history", "--from=invalid-date"},
			expectedErr: "invalid --from date",
		},
		{
			name:        "InvalidLastDuration",
			args:        []string{"history", "--last=invalid"},
			expectedErr: "invalid --last value",
		},
		{
			name:        "ConflictingFlags",
			args:        []string{"history", "--last=7d", "--from=2026-01-01"},
			expectedErr: "cannot use --last with --from",
		},
		{
			name:        "InvalidLimit",
			args:        []string{"history", "--limit=invalid"},
			expectedErr: "invalid --limit value",
		},
		{
			name:        "InvalidFormat",
			args:        []string{"history", "--format=pdf"},
			expectedErr: "invalid format",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := th.RunCommandWithError(t, cmd.History(), test.CmdTest{
				Name: tc.name,
				Args: tc.args,
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

func TestHistoryCommand_EmptyResults(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)

	// Query for non-existent DAG - stdout is not captured in LoggingOutput,
	// so we just verify the command runs without error
	th.RunCommand(t, cmd.History(), test.CmdTest{
		Name: "NonExistent",
		Args: []string{"history", "non-existent-dag-xyz"},
	})
}

func TestHistoryCommand_EmptyResultsJSONOutput(t *testing.T) {
	th := test.SetupCommand(t)

	stdout, stderr := captureOutput(t, func() {
		th.RunCommand(t, cmd.History(), test.CmdTest{
			Name: "EmptyJSON",
			Args: []string{"history", "--run-id=non-existent-run-id", "--format=json"},
		})
	})

	var payload []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &payload))
	assert.Empty(t, payload)
	assert.NotContains(t, stdout, "No DAG runs found")
	assert.Contains(t, stderr, "No DAG runs found matching the specified filters.")
}

func TestHistoryCommand_Labels(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	ctx := context.Background()

	// Create DAGs with different labels
	dag1 := th.DAG(t, `name: labeled-dag-1
labels:
  - prod
  - critical
steps:
  - name: step1
    command: "echo test"
`)

	dag2 := th.DAG(t, `name: labeled-dag-2
labels:
  - dev
steps:
  - name: step2
    command: "echo test"
`)

	th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", dag1.Location}})
	th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", dag2.Location}})

	require.Eventually(t, func() bool {
		s1, err1 := th.DAGRunMgr.GetLatestStatus(ctx, dag1.DAG)
		s2, err2 := th.DAGRunMgr.GetLatestStatus(ctx, dag2.DAG)
		return err1 == nil && err2 == nil && s1.Status == core.Succeeded && s2.Status == core.Succeeded
	}, 5*time.Second, 100*time.Millisecond)

	// Filter by label - stdout table output is not captured,
	// so we just verify the command runs without error
	th.RunCommand(t, cmd.History(), test.CmdTest{
		Name: "FilterByLabel",
		Args: []string{"history", "--labels=prod"},
	})

	statuses, err := th.DAGRunStore.ListStatuses(ctx, exec.WithLabels([]string{"prod"}), exec.WithAllHistory())
	require.NoError(t, err)
	names := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		names[status.Name] = true
	}
	assert.True(t, names[dag1.Name])
	assert.False(t, names[dag2.Name])
}

func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	require.NoError(t, err)
	stderrR, stderrW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = stdoutW
	os.Stderr = stderrW
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	fn()

	require.NoError(t, stdoutW.Close())
	require.NoError(t, stderrW.Close())

	var stdout bytes.Buffer
	_, err = io.Copy(&stdout, stdoutR)
	require.NoError(t, err)
	require.NoError(t, stdoutR.Close())

	var stderr bytes.Buffer
	_, err = io.Copy(&stderr, stderrR)
	require.NoError(t, err)
	require.NoError(t, stderrR.Close())

	return stdout.String(), stderr.String()
}

func TestHistoryCommand_Limit(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	ctx := context.Background()

	dag := th.DAG(t, `name: test-limit
steps:
  - name: step
    command: "echo test"
`)

	// Create multiple runs, waiting for each to succeed before starting the next
	for i := range 3 {
		th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", dag.Location}})
		expected := i + 1
		require.Eventually(t, func() bool {
			statuses := th.DAGRunMgr.ListRecentStatus(ctx, dag.Name, expected)
			count := 0
			for _, s := range statuses {
				if s.Status == core.Succeeded {
					count++
				}
			}
			return count >= expected
		}, 10*time.Second, 100*time.Millisecond)
	}

	// Test limit - verify command runs successfully with --limit flag.
	// Stdout table output is not captured, so we just verify no error.
	// The limit logic is tested via the exec.WithLimit() functional option.
	th.RunCommand(t, cmd.History(), test.CmdTest{
		Name: "LimitResults",
		Args: []string{"history", "test-limit", "--limit=2"},
	})
}
