// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func directStartStatusTimeout() time.Duration {
	switch {
	case runtime.GOOS == "windows" && raceEnabled():
		return 45 * time.Second
	case runtime.GOOS == "windows":
		return 30 * time.Second
	default:
		return 20 * time.Second
	}
}

func executionStatusTimeout() time.Duration {
	switch {
	case runtime.GOOS == "windows" && raceEnabled():
		return 45 * time.Second
	case runtime.GOOS == "windows":
		return 30 * time.Second
	default:
		return 20 * time.Second
	}
}

func artifactExecutionStatusTimeout() time.Duration {
	switch {
	case runtime.GOOS == "windows" && raceEnabled():
		return 60 * time.Second
	case runtime.GOOS == "windows":
		// Distributed artifact persistence has to archive worker output and
		// stream it back to the coordinator, which is materially slower on the
		// GitHub-hosted Windows runners than ordinary distributed execution.
		return 45 * time.Second
	default:
		return 20 * time.Second
	}
}

func artifactStepShellYAML() string {
	if runtime.GOOS == "windows" {
		return "    shell: powershell\n"
	}
	return "    shell: /bin/sh\n"
}

func indentYAMLBlock(s string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func artifactWriteCommand(content string, fail bool) string {
	var commands []string
	if runtime.GOOS == "windows" {
		commands = append(commands,
			"if (-not $env:DAG_RUN_ARTIFACTS_DIR) { throw 'DAG_RUN_ARTIFACTS_DIR not set' }",
			"$reportsDir = Join-Path $env:DAG_RUN_ARTIFACTS_DIR 'reports'",
			"New-Item -ItemType Directory -Path $reportsDir -Force | Out-Null",
			fmt.Sprintf("[System.IO.File]::WriteAllText((Join-Path $reportsDir 'summary.md'), %s)", test.PowerShellQuote(content)),
		)
	} else {
		commands = append(commands,
			`test -n "${DAG_RUN_ARTIFACTS_DIR}"`,
			`mkdir -p "${DAG_RUN_ARTIFACTS_DIR}/reports"`,
			fmt.Sprintf(`printf '%%s\n' %s > "$DAG_RUN_ARTIFACTS_DIR/reports/summary.md"`, test.PosixQuote(content)),
		)
	}
	if fail {
		commands = append(commands, "exit 1")
	} else if runtime.GOOS == "windows" {
		commands = append(commands, "exit 0")
	}
	return test.JoinLines(commands...)
}

func artifactNoWriteCommand() string {
	if runtime.GOOS == "windows" {
		return test.JoinLines(
			"if (-not $env:DAG_RUN_ARTIFACTS_DIR) { throw 'DAG_RUN_ARTIFACTS_DIR not set' }",
			"Write-Output 'no artifacts written'",
		)
	}
	return test.JoinLines(
		`test -n "${DAG_RUN_ARTIFACTS_DIR}"`,
		`echo "no artifacts written"`,
	)
}

func TestExecution_StatusPushing(t *testing.T) {
	t.Run("statusUpdatesPersistedToCoordinatorStore", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
name: status-push-test
worker_selector:
  test: "true"
steps:
  - name: step1
    run: echo "step1"
  - name: step2
    run: echo "step2"
    depends: [step1]
`)
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, executionStatusTimeout())

		require.Equal(t, core.Succeeded, status.Status)
		require.Len(t, status.Nodes, 2)
		f.assertWorkerID(status, "worker-1")
		f.assertAllNodesSucceeded(status)
	})
}

func TestExecution_LogStreaming(t *testing.T) {
	t.Run("logsStreamedToCoordinatorFilesystem", func(t *testing.T) {
		expectedOutput := "EXPECTED_OUTPUT_12345"
		f := newTestFixture(t, `
name: log-stream-test
worker_selector:
  test: "true"
steps:
  - name: echo-step
    run: echo "`+expectedOutput+`"
`, withLogPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, executionStatusTimeout())

		require.Equal(t, core.Succeeded, status.Status)
		assertLogContains(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "echo-step", expectedOutput)
	})
}

func TestExecution_DistributedWorkerLogsVisibleFromCoordinator(t *testing.T) {
	firstStdout := "distributed-first-stdout"
	firstStderr := "distributed-first-stderr"
	secondStdout := "distributed-second-stdout"
	secondStderr := "distributed-second-stderr"

	f := newTestFixture(t, `
name: distributed-worker-visible-logs-test
worker_selector:
  test: "true"
steps:
  - name: first
`+artifactStepShellYAML()+`    command: |
`+indentYAMLBlock(test.JoinLines(
		test.Output(firstStdout),
		test.Stderr(firstStderr),
	), 6)+`
  - name: second
`+artifactStepShellYAML()+`    command: |
`+indentYAMLBlock(test.JoinLines(
		test.Output(secondStdout),
		test.Stderr(secondStderr),
	), 6)+`
    depends: [first]
`, withLogPersistence())
	defer f.cleanup()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	status := f.waitForStatus(core.Succeeded, executionStatusTimeout())

	require.Equal(t, core.Succeeded, status.Status)
	f.assertAllNodesSucceeded(status)

	expectedStepLogs := []struct {
		stepName string
		suffix   string
		lines    []string
	}{
		{stepName: "first", suffix: "stdout", lines: []string{firstStdout}},
		{stepName: "first", suffix: "stderr", lines: []string{firstStderr}},
		{stepName: "second", suffix: "stdout", lines: []string{secondStdout}},
		{stepName: "second", suffix: "stderr", lines: []string{secondStderr}},
	}
	for _, expected := range expectedStepLogs {
		matches := findLogFiles(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, expected.stepName, expected.suffix)
		require.NotEmpty(t, matches, "no %s log file found for step %s", expected.suffix, expected.stepName)
		content := getLogContent(t, matches[0])
		for _, line := range expected.lines {
			assert.Contains(t, content, line, "%s log file for step %s should contain expected content", expected.suffix, expected.stepName)
		}
	}

	require.NotEmpty(t, status.Log, "run-level scheduler log path should be persisted")
	require.FileExists(t, status.Log, "run-level scheduler log should exist on the coordinator")
	runLog := getLogContent(t, status.Log)
	for _, line := range []string{firstStdout, firstStderr, secondStdout, secondStderr} {
		assert.Contains(t, runLog, line, "run-level scheduler log should contain distributed step output evidence")
	}
}

func TestExecution_LargeOutput(t *testing.T) {
	t.Run("largeOutputStreamedCorrectly", func(t *testing.T) {
		command := `      for i in $(seq 1 2000); do
        echo "Line $i: This is a test line to generate large output that exceeds the 64KB buffer size used in log streaming"
      done`
		if runtime.GOOS == "windows" {
			command = `      1..2000 | ForEach-Object {
        Write-Output ("Line {0}: This is a test line to generate large output that exceeds the 64KB buffer size used in log streaming" -f $_)
      }`
		}

		f := newTestFixture(t, `
name: large-output-test
worker_selector:
  test: "true"
steps:
  - name: big-output
    run: |
`+command+`
`, withLogPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(60 * time.Second)

		status := f.waitForStatus(core.Succeeded, 45*time.Second)

		require.Equal(t, core.Succeeded, status.Status)

		logPath := assertLogExists(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "big-output")

		fileInfo, err := os.Stat(logPath)
		require.NoError(t, err)
		assert.Greater(t, fileInfo.Size(), int64(64*1024), "log file should exceed 64KB")

		content := getLogContent(t, logPath)
		assert.Contains(t, content, "Line 1:")
		assert.Contains(t, content, "Line 2000:")

		lineCount := strings.Count(content, "\n")
		assert.GreaterOrEqual(t, lineCount, 2000, "should have at least 2000 lines")
	})
}

func TestExecution_Artifacts(t *testing.T) {
	t.Run("workerUploadsArtifactsToCoordinatorFilesystem", func(t *testing.T) {
		f := newTestFixture(t, `
name: worker-artifact-test
worker_selector:
  test: "true"
artifacts:
  enabled: true
steps:
  - name: write-artifacts
`+artifactStepShellYAML()+`    command: |
`+indentYAMLBlock(artifactWriteCommand("artifact from worker", false), 6)+`
`, withArtifactPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, artifactExecutionStatusTimeout())

		require.Equal(t, core.Succeeded, status.Status)
		require.NotEmpty(t, status.ArchiveDir)
		require.DirExists(t, status.ArchiveDir)
		assert.True(t, strings.HasPrefix(status.ArchiveDir, filepath.Join(f.artifactDir(), f.dagWrapper.Name)+string(os.PathSeparator)))
		assertArtifactContains(t, status.ArchiveDir, "reports/summary.md", "artifact from worker")
	})

	t.Run("workerFailedRunsStillUploadArtifactsToCoordinatorFilesystem", func(t *testing.T) {
		f := newTestFixture(t, `
name: worker-failed-artifact-test
worker_selector:
  test: "true"
artifacts:
  enabled: true
steps:
  - name: write-artifacts-and-fail
`+artifactStepShellYAML()+`    command: |
`+indentYAMLBlock(artifactWriteCommand("artifact from failed worker", true), 6)+`
`, withArtifactPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Failed, artifactExecutionStatusTimeout())

		require.Equal(t, core.Failed, status.Status)
		require.NotEmpty(t, status.ArchiveDir)
		require.DirExists(t, status.ArchiveDir)
		assert.True(t, strings.HasPrefix(status.ArchiveDir, filepath.Join(f.artifactDir(), f.dagWrapper.Name)+string(os.PathSeparator)))
		assertArtifactContains(t, status.ArchiveDir, "reports/summary.md", "artifact from failed worker")
	})

	t.Run("workerCreatesEmptyArtifactDirectoryWhenNoFilesAreWritten", func(t *testing.T) {
		f := newTestFixture(t, `
name: worker-empty-artifact-test
worker_selector:
  test: "true"
artifacts:
  enabled: true
steps:
  - name: no-artifacts-written
`+artifactStepShellYAML()+`    command: |
`+indentYAMLBlock(artifactNoWriteCommand(), 6)+`
`, withArtifactPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, artifactExecutionStatusTimeout())

		require.Equal(t, core.Succeeded, status.Status)
		require.NotEmpty(t, status.ArchiveDir)
		require.DirExists(t, status.ArchiveDir)
		assert.True(t, strings.HasPrefix(status.ArchiveDir, filepath.Join(f.artifactDir(), f.dagWrapper.Name)+string(os.PathSeparator)))

		entries, err := os.ReadDir(status.ArchiveDir)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("coordinatorRejectsStaleAttemptArtifactChunks", func(t *testing.T) {
		f := newTestFixture(t, `
name: stale-attempt-artifact-test
worker_selector:
  test: "true"
artifacts:
  enabled: true
steps:
  - name: write-artifacts
`+artifactStepShellYAML()+`    command: |
`+indentYAMLBlock(artifactWriteCommand("artifact from latest attempt", false), 6)+`
`, withArtifactPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, artifactExecutionStatusTimeout())
		require.Equal(t, core.Succeeded, status.Status)

		stream, err := f.coordinatorClient.StreamArtifacts(f.coord.Context)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&coordinatorv1.ArtifactChunk{
			WorkerId:           "stale-worker",
			DagRunId:           status.DAGRunID,
			DagName:            f.dagWrapper.Name,
			AttemptId:          "stale-attempt",
			OwnerCoordinatorId: "test-coordinator",
			RelativePath:       "reports/stale.txt",
			Data:               []byte("stale artifact"),
		}))

		_, err = stream.CloseAndRecv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not match latest attempt")

		_, statErr := os.Stat(filepath.Join(status.ArchiveDir, "reports", "stale.txt"))
		require.Error(t, statErr)
		assert.True(t, os.IsNotExist(statErr))
	})
}

func TestExecution_StartCommand(t *testing.T) {
	t.Run("directStartCommandExecution", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
name: direct-start-test
worker_selector:
  test: "true"
steps:
  - name: step1
    run: echo "step1 output"
  - name: step2
    run: echo "step2 output"
    depends: [step1]
`)
		defer f.cleanup()

		f.startScheduler(30 * time.Second)

		require.NoError(t, f.start())

		status := f.waitForStatus(core.Succeeded, directStartStatusTimeout())

		require.Equal(t, core.Succeeded, status.Status)
		require.Len(t, status.Nodes, 2)
		f.assertAllNodesSucceeded(status)
	})

	t.Run("directStartCommandExecution_NoNameField", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
worker_selector:
  test: "true"
steps:
  - name: step1
    run: echo "no name field"
  - name: step2
    run: echo "step2 output"
    depends: [step1]
`)
		defer f.cleanup()

		f.startScheduler(30 * time.Second)

		require.NoError(t, f.start())

		status := f.waitForStatus(core.Succeeded, directStartStatusTimeout())

		require.Equal(t, core.Succeeded, status.Status)
		require.Len(t, status.Nodes, 2)
		f.assertAllNodesSucceeded(status)
	})
}

func TestExecution_LabelsPropagation(t *testing.T) {
	t.Run("labelsPreservedThroughCoordinator", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
name: labels-propagation-test
worker_selector:
  test: "true"
steps:
  - name: step1
    run: echo "tagged run"
`)
		defer f.cleanup()

		f.startScheduler(30 * time.Second)

		require.NoError(t, f.startWithLabels("env=prod,team=backend"))

		status := f.waitForStatus(core.Succeeded, 20*time.Second)

		require.Equal(t, core.Succeeded, status.Status)
		require.Contains(t, status.Labels, "env=prod")
		require.Contains(t, status.Labels, "team=backend")
	})

}

func TestExecution_WorkDir(t *testing.T) {
	t.Run("workerWorkDir", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
name: workdir-worker-test
worker_selector:
  test: "true"
steps:
  - name: write-to-workdir
    run: echo "hello" > "${DAG_RUN_WORK_DIR}/test.txt"
  - name: read-from-workdir
    run: cat "${DAG_RUN_WORK_DIR}/test.txt"
    depends: [write-to-workdir]
`, withLogPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, 20*time.Second)

		require.Equal(t, core.Succeeded, status.Status)
		f.assertAllNodesSucceeded(status)
		assertLogContains(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "read-from-workdir", "hello")
	})

}

func TestExecution_QueueLifecycle(t *testing.T) {
	t.Run("queueItemRemovedAfterSuccess", func(t *testing.T) {
		f := newTestFixture(t, `
name: queue-cleanup-test
worker_selector:
  test: "true"
steps:
  - name: task1
    run: echo "done"
`)
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		require.Eventually(t, func() bool {
			status, err := f.latestStatus()
			if err != nil || status.Status != core.Succeeded {
				return false
			}

			items, err := f.coord.QueueStore.ListByDAGName(f.coord.Context, f.dagWrapper.ProcGroup(), f.dagWrapper.Name)
			return err == nil && len(items) == 0
		}, distrTestTimeout(25*time.Second), 200*time.Millisecond, "Queue should be empty after success")
	})

	t.Run("queuedStatusBeforeSchedulerStarts", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
name: scheduler-process-test
worker_selector:
  env: prod
steps:
  - name: step1
    run: echo "step1"
  - name: step2
    run: echo "step2"
    depends: [step1]
`, withLabels(map[string]string{"env": "prod"}))
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()

		latest, err := f.latestStatus()
		require.NoError(t, err)
		require.Equal(t, core.Queued, latest.Status, "DAG should be in queued state before scheduler starts")

		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, 20*time.Second)

		require.Equal(t, core.Succeeded, status.Status)
		require.Len(t, status.Nodes, 2)
		f.assertAllNodesSucceeded(status)
	})
}

func TestExecution_QueuedCatchupHappyPath(t *testing.T) {
	t.Run("distributedWorkerPreservesCatchupMetadata", func(t *testing.T) {
		scheduleTime := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
		expectedOutput := "distributed-catchup-remote"

		f := newTestFixture(t, `
name: distributed-catchup-remote-test
worker_selector:
  test: "true"
steps:
  - name: echo-step
    run: echo "`+expectedOutput+`"
`, withLogPersistence())
		defer f.cleanup()

		runID, err := f.enqueueCatchup(scheduleTime)
		require.NoError(t, err)

		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, 20*time.Second)

		require.Equal(t, runID, status.DAGRunID)
		require.Equal(t, core.TriggerTypeCatchUp, status.TriggerType)
		require.Equal(t, stringutil.FormatTime(scheduleTime), status.ScheduleTime)
		require.NotEmpty(t, status.Log)
		f.assertWorkerID(status, "worker-1")
		f.assertAllNodesSucceeded(status)
		assertLogContains(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "echo-step", expectedOutput)
	})

}
