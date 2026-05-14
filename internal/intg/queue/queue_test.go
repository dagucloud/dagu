// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBasicProcessing(t *testing.T) {
	f := newFixture(t, `
name: echo-dag
steps:
  - name: echo
    run: echo hello
`).Enqueue(3).StartScheduler(30 * time.Second)

	f.WaitDrain(35 * time.Second)
	f.WaitForAllStatuses(core.Succeeded, 20*time.Second)
	f.Stop()

	items, err := f.th.QueueStore.List(f.th.Context, f.queue)
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestParallelQueueFixturesRemainIsolated(t *testing.T) {
	for i := range 2 {
		t.Run(fmt.Sprintf("fixture-%d", i), func(t *testing.T) {
			f := newFixture(t, `
name: echo-dag
steps:
  - name: echo
    run: echo hello
`).Enqueue(1).StartScheduler(20 * time.Second)
			defer f.Stop()

			f.WaitDrain(15 * time.Second)
			f.WaitForStatus(f.runIDs[0], core.Succeeded, 10*time.Second)
		})
	}
}

func TestGlobalConcurrency(t *testing.T) {
	startedDir := t.TempDir()
	releaseFile := filepath.Join(t.TempDir(), "release")
	maxDiff := 2 * time.Second
	switch {
	case runtime.GOOS == "windows" && raceEnabled():
		maxDiff = 10 * time.Second
	case runtime.GOOS == "windows":
		// StartedAt is second-granularity in persisted queue statuses, so give
		// Windows enough overlap budget to avoid false negatives from rounding.
		maxDiff = 5 * time.Second
	}

	f := newFixture(t, fmt.Sprintf(`
name: sleep-dag
queue: global-queue
steps:
  - name: sleep
    command: |
%s
`, indentQueueTestScript(markQueueRunStartedAndWaitCommand(startedDir, releaseFile), 6)), WithQueue("global-queue"), WithGlobalQueue("global-queue", 3)).
		Enqueue(3).StartScheduler(30 * time.Second)
	released := false
	stopped := false
	defer func() {
		if !released {
			_ = os.WriteFile(releaseFile, []byte("release"), 0600)
		}
		if !stopped {
			f.Stop()
		}
	}()

	f.WaitDrain(35 * time.Second)
	waitForStartedQueueRuns(t, startedDir, 3, 20*time.Second)
	f.AssertConcurrent(maxDiff)

	require.NoError(t, os.WriteFile(releaseFile, []byte("release"), 0600))
	released = true
	f.WaitForAllStatuses(core.Succeeded, 20*time.Second)
	f.WaitForAllStopped(10 * time.Second)
	f.Stop()
	stopped = true
}

func markQueueRunStartedAndWaitCommand(startedDir, releaseFile string) string {
	return test.ForOS(
		fmt.Sprintf(`mkdir -p %s
: > %s/"started-$$"
while [ ! -f %s ]; do
  sleep 0.05
done`, test.PosixQuote(startedDir), test.PosixQuote(startedDir), test.PosixQuote(releaseFile)),
		fmt.Sprintf(`New-Item -ItemType Directory -Path %s -Force | Out-Null
New-Item -ItemType File -Path (Join-Path %s ("started-" + [guid]::NewGuid().ToString())) -Force | Out-Null
while (-not (Test-Path %s)) {
  Start-Sleep -Milliseconds 50
}`, test.PowerShellQuote(startedDir), test.PowerShellQuote(startedDir), test.PowerShellQuote(releaseFile)),
	)
}

func indentQueueTestScript(script string, spaces int) string {
	indent := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimRight(script, "\n"), "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

func waitForStartedQueueRuns(t *testing.T, startedDir string, want int, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		entries, err := os.ReadDir(startedDir)
		return err == nil && len(entries) >= want
	}, queueTestTimeout(timeout), 50*time.Millisecond)
}

func TestLocalQueueFIFOProcessing(t *testing.T) {
	f := newFixture(t, fmt.Sprintf(`
name: batch-dag
max_active_runs: 3
steps:
  - name: sleep
    run: %s
`, test.ShellQuote(test.Sleep(time.Second)))).Enqueue(3).StartScheduler(30 * time.Second)
	defer f.Stop()

	f.WaitDrain(20 * time.Second)
	times := f.collectStartTimes()
	require.Len(t, times, 3)
	for i := 1; i < len(times); i++ {
		diff := times[i].Sub(times[i-1])
		require.GreaterOrEqual(t, diff, 900*time.Millisecond,
			"Local queue should process sequentially (FIFO), not concurrently")
	}
}

func TestPriorityOrdering(t *testing.T) {
	f := newFixture(t, `
name: priority-dag
max_active_runs: 1
steps:
  - name: echo
    run: echo done
`).
		EnqueueWithPriority(exec.QueuePriorityLow).
		EnqueueWithPriority(exec.QueuePriorityLow).
		EnqueueWithPriority(exec.QueuePriorityHigh).
		EnqueueWithPriority(exec.QueuePriorityHigh).
		StartScheduler(30 * time.Second)

	f.WaitDrain(35 * time.Second)
	f.WaitForAllStatuses(core.Succeeded, 20*time.Second)
	f.Stop()

	times := f.collectStartTimes()
	require.Len(t, times, 4)
	highPriorityStart := times[2]
	if times[3].Before(highPriorityStart) {
		highPriorityStart = times[3]
	}
	lowPriorityStart := times[0]
	if times[1].Before(lowPriorityStart) {
		lowPriorityStart = times[1]
	}
	require.True(t, highPriorityStart.Before(lowPriorityStart) || highPriorityStart.Equal(lowPriorityStart),
		"High priority runs should start before or equal to low priority runs")
}

func TestRetryEnqueue(t *testing.T) {
	f := newFixture(t, `
name: retry-dag
queue: retry-queue
steps:
  - name: echo
    run: echo retried
`, WithQueue("retry-queue"), WithGlobalQueue("retry-queue", 1)).
		FailedRun()

	runID := f.runIDs[0]

	ref := exec.NewDAGRunRef(f.dag.Name, runID)
	att, err := f.th.DAGRunStore.FindAttempt(f.th.Context, ref)
	require.NoError(t, err)
	status, err := att.ReadStatus(f.th.Context)
	require.NoError(t, err)
	require.Equal(t, core.Failed, status.Status)

	f.RetryEnqueue(runID)

	att, err = f.th.DAGRunStore.FindAttempt(f.th.Context, ref)
	require.NoError(t, err)
	status, err = att.ReadStatus(f.th.Context)
	require.NoError(t, err)
	assert.Equal(t, core.Queued, status.Status)
	assert.NotEmpty(t, status.QueuedAt)
	assert.Equal(t, core.TriggerTypeRetry, status.TriggerType)
	assert.Zero(t, status.AutoRetryCount)

	items, err := f.th.QueueStore.List(f.th.Context, "retry-queue")
	require.NoError(t, err)
	require.Len(t, items, 1)
	data, err := items[0].Data()
	require.NoError(t, err)
	assert.Equal(t, f.dag.Name, data.Name)
	assert.Equal(t, runID, data.ID)
}

func TestExplicitEnvParityAcrossDirectStartAndQueueRetry(t *testing.T) {
	const rawVar = "QUEUE_EXPLICIT_ENV_SECRET"

	prevValue, hadPrev := os.LookupEnv(rawVar)
	require.NoError(t, os.Setenv(rawVar, "from-host"))
	t.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv(rawVar, prevValue)
			return
		}
		_ = os.Unsetenv(rawVar)
	})

	f := newFixture(t, fmt.Sprintf(`
name: queue-explicit-env
queue: queue-explicit-env
env:
  - EXPORTED_SECRET: ${%s}
steps:
  - name: capture
    run: %q
    output: RESULT
`, rawVar, test.EnvOutput("EXPORTED_SECRET", rawVar)), WithQueue("queue-explicit-env"), WithGlobalQueue("queue-explicit-env", 1))

	test.RunBuiltCLI(t, f.th.Helper, []string{rawVar + "=from-host"}, "start", f.dag.Location)

	directStatus, err := f.th.DAGRunMgr.GetLatestStatus(f.th.Context, f.dag)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, directStatus.Status)
	directOutput := test.StatusOutputValue(t, &directStatus, "RESULT")
	directParts := strings.SplitN(directOutput, "|", 2)
	require.Len(t, directParts, 2)
	require.Equal(t, "from-host", directParts[0])
	require.Empty(t, directParts[1])

	queuedRunID := "queued-explicit-env-run"
	test.RunBuiltCLI(t, f.th.Helper, []string{rawVar + "=from-host"}, "enqueue", "--run-id", queuedRunID, f.dag.Location)
	f.runIDs = append(f.runIDs, queuedRunID)

	queueProcessor := scheduler.NewQueueProcessor(
		f.th.QueueStore,
		f.th.DAGRunStore,
		f.th.ProcStore,
		scheduler.NewDAGExecutor(
			coordinator.New(f.th.ServiceRegistry, coordinator.DefaultConfig()),
			f.th.SubCmdBuilder,
			f.th.Config.DefaultExecMode,
			f.th.Config.Paths.BaseConfig,
			nil,
		),
		config.Queues{
			Enabled: true,
			Config: []config.QueueConfig{
				{Name: "queue-explicit-env", MaxActiveRuns: 1},
			},
		},
	)
	queueProcessor.ProcessQueueItems(f.th.Context, "queue-explicit-env")

	f.WaitForStatus(queuedRunID, core.Succeeded, 10*time.Second)

	queuedStatus := f.MustStatus(queuedRunID)
	queuedOutput := test.StatusOutputValue(t, queuedStatus, "RESULT")
	queuedParts := strings.SplitN(queuedOutput, "|", 2)
	require.Len(t, queuedParts, 2)
	require.Equal(t, directParts[0], queuedParts[0])
	require.Equal(t, directParts[1], queuedParts[1])
}

func TestSchedulerRetryScanner(t *testing.T) {
	t.Run("EligibleFailedRunRetries", func(t *testing.T) {
		markerPath := filepath.Join(t.TempDir(), "failure.marker")
		f := newFixture(t, fmt.Sprintf(`
type: graph
name: retry-dag
queue: retry-queue
retry_policy:
  limit: 1
  interval_sec: 1
  backoff: false
  max_interval_sec: 1
handler_on:
  failure:
    run: %q
steps:
  - id: retry_step
    run: echo retried
`, test.ForOS(
			fmt.Sprintf("printf '%%s' %s > %s", test.PosixQuote("failed"), test.PosixQuote(markerPath)),
			fmt.Sprintf("Set-Content -Path %s -Value %s -NoNewline", test.PowerShellQuote(markerPath), test.PowerShellQuote("failed")),
		)), WithQueue("retry-queue"), WithGlobalQueue("retry-queue", 1))

		failedAt := time.Now().UTC().Add(-30 * time.Second)
		runID := f.FailedRunWithMetadata(runStatusOptions{
			StartedAt:    failedAt.Add(-5 * time.Second),
			FinishedAt:   failedAt,
			ScheduleTime: failedAt.Add(-time.Minute),
			TriggerType:  core.TriggerTypeScheduler,
		})
		markerModTime := prepareFailureMarker(t, markerPath)
		originalAttemptID := f.MustStatus(runID).AttemptID

		f.StartScheduler(40 * time.Second)
		defer f.Stop()

		latest, err := f.WaitForStatusMatch(runID, 25*time.Second, func(status *exec.DAGRunStatus) bool {
			return status.Status == core.Succeeded &&
				status.AttemptID != originalAttemptID &&
				status.AutoRetryCount == 1
		})
		require.NoError(t, err)
		assert.Equal(t, core.Succeeded, latest.Status)
		assert.NotEqual(t, originalAttemptID, latest.AttemptID)
		assert.Equal(t, 1, latest.AutoRetryCount)

		f.WaitDrain(5 * time.Second)
		assert.Equal(t, markerModTime, readMarkerModTime(t, markerPath))
	})

	t.Run("MissingFinishedAtStillRetriesViaCreatedAt", func(t *testing.T) {
		markerPath := filepath.Join(t.TempDir(), "failure.marker")
		f := newFixture(t, fmt.Sprintf(`
type: graph
name: retry-dag
queue: retry-queue
retry_policy:
  limit: 1
  interval_sec: 1
  backoff: false
  max_interval_sec: 1
handler_on:
  failure:
    run: %q
steps:
  - id: retry_step
    run: echo retried
`, test.ForOS(
			fmt.Sprintf("printf '%%s' %s > %s", test.PosixQuote("failed"), test.PosixQuote(markerPath)),
			fmt.Sprintf("Set-Content -Path %s -Value %s -NoNewline", test.PowerShellQuote(markerPath), test.PowerShellQuote("failed")),
		)), WithQueue("retry-queue"), WithGlobalQueue("retry-queue", 1))

		failedAt := time.Now().UTC().Add(-30 * time.Second)
		runID := f.FailedRunWithMetadata(runStatusOptions{
			CreatedAt:    failedAt,
			StartedAt:    failedAt.Add(-5 * time.Second),
			ScheduleTime: failedAt.Add(-time.Minute),
			TriggerType:  core.TriggerTypeScheduler,
		})
		originalAttemptID := f.MustStatus(runID).AttemptID

		f.StartScheduler(40 * time.Second)
		defer f.Stop()

		latest, err := f.WaitForStatusMatch(runID, 25*time.Second, func(status *exec.DAGRunStatus) bool {
			return status.Status == core.Succeeded &&
				status.AttemptID != originalAttemptID &&
				status.AutoRetryCount == 1
		})
		require.NoError(t, err)
		assert.Equal(t, core.Succeeded, latest.Status)
		assert.NotEqual(t, originalAttemptID, latest.AttemptID)
		assert.Equal(t, 1, latest.AutoRetryCount)
	})

	t.Run("DisabledByChildSkipsInheritedBaseRetryPolicy", func(t *testing.T) {
		f := newFixture(t, `
type: graph
name: retry-disabled-dag
queue: retry-disabled-queue
retry_policy:
  limit: 0
steps:
  - id: retry_step
    run: echo retried
`, WithQueue("retry-disabled-queue"), WithGlobalQueue("retry-disabled-queue", 1))

		require.NoError(t, os.WriteFile(f.th.Config.Paths.BaseConfig, []byte(`
retry_policy:
  limit: 1
  interval_sec: 1
  backoff: false
  max_interval_sec: 1
`), 0600))

		dag, err := spec.Load(f.th.Context, f.dag.Location, spec.WithBaseConfig(f.th.Config.Paths.BaseConfig))
		require.NoError(t, err)
		f.dag = dag
		require.NotNil(t, f.dag.RetryPolicy)
		require.Equal(t, 0, f.dag.RetryPolicy.Limit)

		failedAt := time.Now().UTC().Add(-30 * time.Second)
		runID := f.FailedRunWithMetadata(runStatusOptions{
			StartedAt:    failedAt.Add(-5 * time.Second),
			FinishedAt:   failedAt,
			ScheduleTime: failedAt.Add(-time.Minute),
			TriggerType:  core.TriggerTypeScheduler,
		})
		originalStatus := f.MustStatus(runID)
		originalAttemptID := originalStatus.AttemptID
		require.Equal(t, 0, originalStatus.AutoRetryLimit)

		f.StartScheduler(10 * time.Second)
		defer f.Stop()

		require.Never(t, func() bool {
			status, err := f.Status(runID)
			if err != nil {
				return false
			}
			return status.AttemptID != originalAttemptID ||
				status.Status != core.Failed ||
				status.AutoRetryCount != 0
		}, 3*time.Second, 100*time.Millisecond)

		latest := f.MustStatus(runID)
		assert.Equal(t, core.Failed, latest.Status)
		assert.Equal(t, originalAttemptID, latest.AttemptID)
		assert.Equal(t, 0, latest.AutoRetryCount)
		assert.Equal(t, 0, latest.AutoRetryLimit)

		items, err := f.th.QueueStore.List(f.th.Context, "retry-disabled-queue")
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("NewerScheduledRunDoesNotSuppressRetry", func(t *testing.T) {
		markerPath := filepath.Join(t.TempDir(), "failure.marker")
		f := newFixture(t, fmt.Sprintf(`
type: graph
name: retry-dag
queue: retry-queue
retry_policy:
  limit: 1
  interval_sec: 1
  backoff: false
  max_interval_sec: 1
handler_on:
  failure:
    run: %q
steps:
  - id: retry_step
    run: echo retried
`, test.ForOS(
			fmt.Sprintf("printf '%%s' %s > %s", test.PosixQuote("failed"), test.PosixQuote(markerPath)),
			fmt.Sprintf("Set-Content -Path %s -Value %s -NoNewline", test.PowerShellQuote(markerPath), test.PowerShellQuote("failed")),
		)), WithQueue("retry-queue"), WithGlobalQueue("retry-queue", 1), WithRetryWindow(48*time.Hour))

		now := time.Now().UTC()
		midnight := retryScanReferenceMidnight(now)
		failedFinishedAt := midnight.Add(2 * time.Minute)
		failedScheduleTime := midnight.Add(-10 * time.Minute)
		newerScheduleTime := midnight.Add(-time.Minute)

		runID := f.FailedRunWithMetadata(runStatusOptions{
			StartedAt:    failedFinishedAt.Add(-5 * time.Second),
			FinishedAt:   failedFinishedAt,
			ScheduleTime: failedScheduleTime,
			TriggerType:  core.TriggerTypeScheduler,
		})
		_ = f.RunningRunWithMetadata(runStatusOptions{
			StartedAt:    now.Add(-time.Minute),
			ScheduleTime: newerScheduleTime,
			TriggerType:  core.TriggerTypeScheduler,
		})
		markerModTime := prepareFailureMarker(t, markerPath)
		originalAttemptID := f.MustStatus(runID).AttemptID

		f.StartScheduler(35 * time.Second)
		defer f.Stop()

		latest, err := f.WaitForStatusMatch(runID, 25*time.Second, func(status *exec.DAGRunStatus) bool {
			return status.Status == core.Succeeded &&
				status.AttemptID != originalAttemptID &&
				status.AutoRetryCount == 1
		})
		require.NoError(t, err)
		assert.Equal(t, core.Succeeded, latest.Status)
		assert.NotEqual(t, originalAttemptID, latest.AttemptID)
		assert.Equal(t, 1, latest.AutoRetryCount)
		assert.Equal(t, markerModTime, readMarkerModTime(t, markerPath))
	})
}

func TestCatchupQueuedHappyPath(t *testing.T) {
	scheduleTime := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)

	f := newFixture(t, `
name: catchup-local-test
steps:
  - name: echo-step
    run: echo catchup-local
`)

	runID := f.enqueueCatchup(scheduleTime)
	f.StartScheduler(30 * time.Second)
	defer f.Stop()

	f.WaitDrain(25 * time.Second)
	status := f.waitForRecentStatus(25*time.Second, func(st exec.DAGRunStatus) bool {
		return st.DAGRunID == runID && st.Status == core.Succeeded
	})

	require.Equal(t, core.TriggerTypeCatchUp, status.TriggerType)
	require.Equal(t, stringutil.FormatTime(scheduleTime), status.ScheduleTime)
	require.NotEmpty(t, status.Log)
	require.FileExists(t, status.Log)

	items, err := f.th.QueueStore.List(f.th.Context, f.queue)
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestSchedulerCatchupFromPersistedWatermark(t *testing.T) {
	scheduledTime := stableCurrentMinute(t)

	f := newFixture(t, `
name: catchup-watermark-test
schedule: "* * * * *"
catchup_window: "2m"
steps:
  - name: echo-step
    run: echo catchup-from-watermark
`)

	f.seedWatermark(scheduledTime.Add(-time.Minute), scheduledTime.Add(-time.Minute))
	f.StartScheduler(45 * time.Second)
	defer f.Stop()

	f.WaitDrain(30 * time.Second)
	status := f.waitForRecentStatus(30*time.Second, func(st exec.DAGRunStatus) bool {
		return st.Status == core.Succeeded &&
			st.TriggerType == core.TriggerTypeCatchUp &&
			st.ScheduleTime == stringutil.FormatTime(scheduledTime)
	})

	require.Equal(t, core.TriggerTypeCatchUp, status.TriggerType)
	require.Equal(t, stringutil.FormatTime(scheduledTime), status.ScheduleTime)
	require.NotEmpty(t, status.Log)
	require.FileExists(t, status.Log)
}

func prepareFailureMarker(t *testing.T, markerPath string) time.Time {
	t.Helper()

	require.NoError(t, os.WriteFile(markerPath, []byte("failed"), 0644))
	modTime := time.Unix(time.Now().Add(-time.Hour).Unix(), 0).UTC()
	require.NoError(t, os.Chtimes(markerPath, modTime, modTime))
	return readMarkerModTime(t, markerPath)
}

func readMarkerModTime(t *testing.T, markerPath string) time.Time {
	t.Helper()

	info, err := os.Stat(markerPath)
	require.NoError(t, err)
	return info.ModTime()
}

func retryScanReferenceMidnight(now time.Time) time.Time {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if now.Sub(midnight) < 5*time.Minute {
		return midnight.Add(-24 * time.Hour)
	}
	return midnight
}

func stableCurrentMinute(t *testing.T) time.Time {
	t.Helper()

	now := time.Now().UTC()
	if now.Second() >= 50 {
		return now.Truncate(time.Minute).Add(-time.Minute)
	}

	return now.Truncate(time.Minute)
}
