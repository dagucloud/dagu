// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/persis/filewatermark"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture provides setup for queue integration tests.
type fixture struct {
	t            *testing.T
	th           test.Command
	dag          *core.DAG
	queue        string
	runIDs       []string
	schedDone    chan error
	cancel       context.CancelFunc
	globalQueues []config.QueueConfig
	retryWindow  time.Duration
	procConfig   *procConfig
	schedConfig  *schedulerConfig
}

type procConfig struct {
	heartbeatInterval     time.Duration
	heartbeatSyncInterval time.Duration
	staleThreshold        time.Duration
}

type schedulerConfig struct {
	zombieDetectionInterval time.Duration
	failureThreshold        int
}

// newFixture creates a new queue integration test fixture.
func newFixture(t *testing.T, dagYAML string, opts ...func(*fixture)) *fixture {
	t.Helper()
	if runtime.GOOS != "windows" && !raceEnabled() {
		t.Parallel()
	}

	f := &fixture{t: t, schedDone: make(chan error, 1)}

	for _, opt := range opts {
		opt(f)
	}

	helperOpts := []test.HelperOption{
		test.WithBuiltExecutable(),
		test.WithConfigMutator(func(c *config.Config) {
			c.Queues.Enabled = true
			if len(f.globalQueues) > 0 {
				c.Queues.Config = f.globalQueues
			}
			if f.retryWindow > 0 {
				c.Scheduler.RetryFailureWindow = f.retryWindow
			}
			c.Scheduler.Port = 0
			if f.procConfig != nil {
				c.Proc.HeartbeatInterval = f.procConfig.heartbeatInterval
				c.Proc.HeartbeatSyncInterval = f.procConfig.heartbeatSyncInterval
				c.Proc.StaleThreshold = f.procConfig.staleThreshold
			}
			if f.schedConfig != nil {
				c.Scheduler.ZombieDetectionInterval = f.schedConfig.zombieDetectionInterval
				c.Scheduler.FailureThreshold = f.schedConfig.failureThreshold
			}
		}),
	}
	f.th = test.SetupCommand(t, helperOpts...)

	require.NoError(t, os.MkdirAll(f.th.Config.Paths.DAGsDir, 0755))
	dagFile := filepath.Join(f.th.Config.Paths.DAGsDir, "test.yaml")
	require.NoError(t, os.WriteFile(dagFile, []byte(dagYAML), 0644))

	dag, err := spec.Load(f.th.Context, dagFile)
	require.NoError(t, err)
	f.dag = dag
	if f.queue == "" {
		f.queue = dag.ProcGroup()
	}

	t.Cleanup(f.cleanup)
	return f
}

func queueTestTimeout(timeout time.Duration) time.Duration {
	switch {
	case runtime.GOOS == "windows" && raceEnabled():
		return timeout * 8
	case runtime.GOOS == "windows":
		return timeout * 4
	case raceEnabled():
		return timeout * 2
	default:
		return timeout
	}
}

func queueTestConcurrencyAllowance(maxDiff time.Duration) time.Duration {
	switch {
	case runtime.GOOS == "windows" && raceEnabled():
		return maxDiff + time.Second
	case runtime.GOOS == "windows":
		return maxDiff + 500*time.Millisecond
	default:
		return maxDiff
	}
}

// WithQueue sets a custom queue name.
func WithQueue(name string) func(*fixture) {
	return func(f *fixture) { f.queue = name }
}

// WithGlobalQueue adds a global queue configuration.
func WithGlobalQueue(name string, maxActiveRuns int) func(*fixture) {
	return func(f *fixture) {
		f.globalQueues = append(f.globalQueues, config.QueueConfig{
			Name:          name,
			MaxActiveRuns: maxActiveRuns,
		})
	}
}

// WithRetryWindow overrides scheduler.retry_failure_window for the fixture.
func WithRetryWindow(window time.Duration) func(*fixture) {
	return func(f *fixture) { f.retryWindow = window }
}

func WithProcConfig(heartbeatInterval, heartbeatSyncInterval, staleThreshold time.Duration) func(*fixture) {
	return func(f *fixture) {
		f.procConfig = &procConfig{
			heartbeatInterval:     heartbeatInterval,
			heartbeatSyncInterval: heartbeatSyncInterval,
			staleThreshold:        staleThreshold,
		}
	}
}

func WithZombieConfig(zombieDetectionInterval time.Duration, failureThreshold int) func(*fixture) {
	return func(f *fixture) {
		f.schedConfig = &schedulerConfig{
			zombieDetectionInterval: zombieDetectionInterval,
			failureThreshold:        failureThreshold,
		}
	}
}

// Enqueue adds n DAG runs to the queue.
func (f *fixture) Enqueue(n int) *fixture {
	f.runIDs = make([]string, n)
	for i := range n {
		f.runIDs[i] = f.enqueueOne()
	}
	return f
}

func (f *fixture) enqueueOne() string {
	return f.enqueueWithPriority(exec.QueuePriorityLow)
}

func (f *fixture) enqueueWithPriority(priority exec.QueuePriority) string {
	id := uuid.New().String()
	att, err := f.th.DAGRunStore.CreateAttempt(f.th.Context, f.dag, time.Now(), id, exec.NewDAGRunAttemptOptions{})
	require.NoError(f.t, err)
	logFile := filepath.Join(f.th.Config.Paths.LogDir, f.dag.Name, id+".log")
	require.NoError(f.t, os.MkdirAll(filepath.Dir(logFile), 0755))
	st := transform.NewStatusBuilder(f.dag).Create(id, core.Queued, 0, time.Time{},
		transform.WithLogFilePath(logFile),
		transform.WithAttemptID(att.ID()),
		transform.WithHierarchyRefs(exec.NewDAGRunRef(f.dag.Name, id), exec.DAGRunRef{}),
	)
	require.NoError(f.t, att.Open(f.th.Context))
	require.NoError(f.t, att.Write(f.th.Context, st))
	require.NoError(f.t, att.Close(f.th.Context))
	require.NoError(f.t, f.th.QueueStore.Enqueue(f.th.Context, f.queue, priority, exec.NewDAGRunRef(f.dag.Name, id)))
	return id
}

func (f *fixture) enqueueCatchup(scheduleTime time.Time) string {
	runID, err := f.th.DAGRunMgr.GenDAGRunID(f.th.Context)
	require.NoError(f.t, err)
	require.NoError(f.t, scheduler.EnqueueCatchupRun(
		f.th.Context,
		f.th.DAGRunStore,
		f.th.QueueStore,
		f.th.Config.Paths.LogDir,
		f.th.Config.Paths.ArtifactDir,
		f.th.Config.Paths.BaseConfig,
		f.dag,
		runID,
		core.TriggerTypeCatchUp,
		scheduleTime,
	))
	f.runIDs = append(f.runIDs, runID)
	return runID
}

// EnqueueWithPriority adds a single DAG run with specified priority.
func (f *fixture) EnqueueWithPriority(priority exec.QueuePriority) *fixture {
	f.runIDs = append(f.runIDs, f.enqueueWithPriority(priority))
	return f
}

// StartScheduler starts the scheduler in background.
func (f *fixture) StartScheduler(timeout time.Duration) *fixture {
	var ctx context.Context
	ctx, f.cancel = context.WithTimeout(f.th.Context, queueTestTimeout(timeout))
	home := filepath.Dir(f.th.Config.Paths.DAGsDir)
	go func() {
		th := f.th
		th.Context = ctx
		f.schedDone <- th.ExecuteCommand(cmd.Scheduler(), test.CmdTest{
			Args:        []string{"scheduler", "--dagu-home", home},
			ExpectedOut: []string{"Scheduler started"},
		})
	}()
	return f
}

// WaitDrain waits for the queue to empty.
func (f *fixture) WaitDrain(timeout time.Duration) *fixture {
	timeout = queueTestTimeout(timeout)
	require.Eventually(f.t, func() bool {
		items, err := f.th.QueueStore.List(f.th.Context, f.queue)
		return err == nil && len(items) == 0
	}, timeout, 200*time.Millisecond, "timed out waiting for queue %s to drain", f.queue)
	return f
}

func (f *fixture) WaitForStatus(runID string, expected core.Status, timeout time.Duration) *fixture {
	f.t.Helper()
	_, err := f.WaitForStatusMatch(runID, timeout, func(status *exec.DAGRunStatus) bool {
		return status.Status == expected
	})
	require.NoError(f.t, err)
	return f
}

func (f *fixture) WaitForStatusIn(runID string, expected []core.Status, timeout time.Duration) *fixture {
	f.t.Helper()
	_, err := f.WaitForStatusMatch(runID, timeout, func(status *exec.DAGRunStatus) bool {
		return slices.Contains(expected, status.Status)
	})
	require.NoError(f.t, err)
	return f
}

func (f *fixture) WaitForAllStatuses(expected core.Status, timeout time.Duration) *fixture {
	f.t.Helper()
	for _, runID := range f.runIDs {
		f.WaitForStatus(runID, expected, timeout)
	}
	return f
}

func (f *fixture) WaitForAllStopped(timeout time.Duration) *fixture {
	f.t.Helper()
	timeout = queueTestTimeout(timeout)
	require.Eventually(f.t, func() bool {
		for _, runID := range f.runIDs {
			alive, err := f.th.ProcStore.IsRunAlive(f.th.Context, f.queue, exec.NewDAGRunRef(f.dag.Name, runID))
			if err != nil || alive {
				return false
			}
		}
		return true
	}, timeout, 50*time.Millisecond, "timed out waiting for queued runs to stop")
	return f
}

// Stop stops the scheduler.
func (f *fixture) Stop() {
	if f.cancel != nil {
		f.cancel()
	}
	f.th.Cancel()
	select {
	case err := <-f.schedDone:
		require.NoError(f.t, err)
	case <-time.After(5 * time.Second):
	}
}

// Status returns the latest persisted status for the given DAG run.
func (f *fixture) Status(runID string) (*exec.DAGRunStatus, error) {
	ctx := f.th.Context
	cancel := func() {}
	if ctx.Err() != nil {
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	}
	defer cancel()

	ref := exec.NewDAGRunRef(f.dag.Name, runID)
	store := filedagrun.New(
		f.th.Config.Paths.DAGRunsDir,
		filedagrun.WithLatestStatusToday(f.th.Config.Server.LatestStatusToday),
		filedagrun.WithLocation(f.th.Config.Core.Location),
	)
	attempt, err := store.FindAttempt(ctx, ref)
	if err != nil {
		return nil, err
	}
	return attempt.ReadStatus(ctx)
}

// MustStatus returns the latest persisted status and fails the test on error.
func (f *fixture) MustStatus(runID string) *exec.DAGRunStatus {
	f.t.Helper()
	status, err := f.Status(runID)
	require.NoError(f.t, err)
	return status
}

func (f *fixture) WaitForStatusMatch(
	runID string,
	timeout time.Duration,
	match func(*exec.DAGRunStatus) bool,
) (*exec.DAGRunStatus, error) {
	f.t.Helper()

	var matched *exec.DAGRunStatus
	var lastErr error
	timeout = queueTestTimeout(timeout)
	ok := assert.Eventually(f.t, func() bool {
		status, err := f.Status(runID)
		if err != nil {
			lastErr = err
			return false
		}
		if match(status) {
			matched = status
			return true
		}
		return false
	}, timeout, 50*time.Millisecond)

	if !ok {
		return nil, fmt.Errorf("timed out waiting for matching status for run %s: %w", runID, lastErr)
	}
	return matched, nil
}

// AssertConcurrent verifies all DAGs started within maxDiff of each other.
func (f *fixture) AssertConcurrent(maxDiff time.Duration) {
	maxDiff = queueTestConcurrencyAllowance(maxDiff)
	times := f.collectStartTimes()
	var max time.Duration
	for i := range times {
		for j := i + 1; j < len(times); j++ {
			if d := times[i].Sub(times[j]).Abs(); d > max {
				max = d
			}
		}
	}
	f.t.Logf("Start times: %v, max diff: %v", times, max)
	require.LessOrEqual(f.t, max, maxDiff)
}

func (f *fixture) collectStartTimes() []time.Time {
	var times []time.Time
	for _, id := range f.runIDs {
		var startedAt string
		require.Eventually(f.t, func() bool {
			st, err := f.Status(id)
			if err != nil {
				return false
			}
			if st.StartedAt == "" {
				return false
			}
			startedAt = st.StartedAt
			return true
		}, queueTestTimeout(10*time.Second), 50*time.Millisecond, "timed out waiting for run %s to record a start time", id)

		t, err := stringutil.ParseTime(startedAt)
		require.NoError(f.t, err)
		times = append(times, t)
	}
	return times
}

func (f *fixture) waitForRecentStatus(timeout time.Duration, match func(exec.DAGRunStatus) bool) exec.DAGRunStatus {
	f.t.Helper()

	var matched exec.DAGRunStatus
	timeout = queueTestTimeout(timeout)
	require.Eventually(f.t, func() bool {
		for _, status := range f.th.DAGRunMgr.ListRecentStatus(f.th.Context, f.dag.Name, 10) {
			if match(status) {
				matched = status
				return true
			}
		}
		return false
	}, timeout, 200*time.Millisecond, "timed out waiting for recent status match")

	return matched
}

type runStatusOptions struct {
	RunID          string
	CreatedAt      time.Time
	StartedAt      time.Time
	FinishedAt     time.Time
	QueuedAt       time.Time
	ScheduleTime   time.Time
	AutoRetryCount int
	TriggerType    core.TriggerType
}

func (f *fixture) writeRunStatus(status core.Status, opts runStatusOptions) string {
	runID := opts.RunID
	if runID == "" {
		runID = uuid.New().String()
	}

	att, err := f.th.DAGRunStore.CreateAttempt(f.th.Context, f.dag, time.Now(), runID, exec.NewDAGRunAttemptOptions{})
	require.NoError(f.t, err)
	logFile := filepath.Join(f.th.Config.Paths.LogDir, f.dag.Name, runID+".log")
	require.NoError(f.t, os.MkdirAll(filepath.Dir(logFile), 0755))

	startedAt := opts.StartedAt
	if startedAt.IsZero() && status.IsActive() {
		startedAt = time.Now()
	}

	statusOpts := []transform.StatusOption{
		transform.WithLogFilePath(logFile),
		transform.WithAttemptID(att.ID()),
		transform.WithHierarchyRefs(exec.NewDAGRunRef(f.dag.Name, runID), exec.DAGRunRef{}),
		transform.WithAutoRetryCount(opts.AutoRetryCount),
	}
	if !opts.CreatedAt.IsZero() {
		statusOpts = append(statusOpts, transform.WithCreatedAt(opts.CreatedAt.UnixMilli()))
	}
	if !opts.FinishedAt.IsZero() {
		statusOpts = append(statusOpts, transform.WithFinishedAt(opts.FinishedAt))
	}
	if !opts.QueuedAt.IsZero() {
		statusOpts = append(statusOpts, transform.WithQueuedAt(exec.FormatTime(opts.QueuedAt)))
	}
	if !opts.ScheduleTime.IsZero() {
		statusOpts = append(statusOpts, transform.WithScheduleTime(exec.FormatTime(opts.ScheduleTime)))
	}
	if opts.TriggerType != core.TriggerTypeUnknown {
		statusOpts = append(statusOpts, transform.WithTriggerType(opts.TriggerType))
	}

	st := transform.NewStatusBuilder(f.dag).Create(runID, status, 0, startedAt, statusOpts...)
	require.NoError(f.t, att.Open(f.th.Context))
	require.NoError(f.t, att.Write(f.th.Context, st))
	require.NoError(f.t, att.Close(f.th.Context))
	return runID
}

// FailedRun creates a DAGRunAttempt with Failed status, simulating a completed but failed run.
func (f *fixture) FailedRun() *fixture {
	f.FailedRunWithMetadata(runStatusOptions{
		StartedAt:  time.Now(),
		FinishedAt: time.Now(),
	})
	return f
}

// FailedRunWithMetadata creates a failed DAG run with explicit persisted metadata.
func (f *fixture) FailedRunWithMetadata(opts runStatusOptions) string {
	runID := f.writeRunStatus(core.Failed, opts)
	f.runIDs = append(f.runIDs, runID)
	return runID
}

// RunningRunWithMetadata creates a running DAG run with explicit persisted metadata.
func (f *fixture) RunningRunWithMetadata(opts runStatusOptions) string {
	return f.writeRunStatus(core.Running, opts)
}

// RetryEnqueue enqueues a previously failed run for retry using exec.EnqueueRetry.
func (f *fixture) RetryEnqueue(runID string) *fixture {
	err := exec.EnqueueRetry(
		f.th.Context,
		f.th.DAGRunStore,
		f.th.QueueStore,
		f.dag,
		f.MustStatus(runID),
		exec.EnqueueRetryOptions{},
	)
	require.NoError(f.t, err)
	return f
}

func (f *fixture) cleanup() {
	if f.cancel != nil {
		f.cancel()
	}
}

func (f *fixture) seedWatermark(lastTick, lastScheduledTime time.Time) {
	f.t.Helper()

	store := filewatermark.New(filepath.Join(f.th.Config.Paths.DataDir, "scheduler"))
	state := &scheduler.SchedulerState{
		Version:  1,
		LastTick: lastTick,
		DAGs: map[string]scheduler.DAGWatermark{
			f.dag.Name: {LastScheduledTime: lastScheduledTime},
		},
	}
	require.NoError(f.t, store.Save(f.th.Context, state))
}
