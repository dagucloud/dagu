// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/require"
)

func schedulerTestTimeout(base time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return base * 3
	}
	return base
}

func setupSchedulerWithoutDAGs(t *testing.T) *test.Scheduler {
	t.Helper()
	return test.SetupScheduler(t, test.WithDAGsDir(t.TempDir()))
}

func TestScheduler(t *testing.T) {
	t.Parallel()

	t.Run("Restart", func(t *testing.T) {
		ctx := context.Background()
		now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

		// Parse a cron that fires at minute 0 (matches "now")
		cronParser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		parsed, err := cronParser.Parse("0 * * * *")
		require.NoError(t, err)

		entryReader := newMockJobManager()
		entryReader.LoadedDAGs = []*core.DAG{
			{
				Name: "restart-dag",
				RestartSchedule: []core.Schedule{
					{Expression: "0 * * * *", Parsed: parsed},
				},
			},
		}

		th := test.SetupScheduler(t)
		sc, err := scheduler.New(th.Config, entryReader, th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
		require.NoError(t, err)
		sc.SetClock(func() time.Time { return now })

		// Track restart calls via the planner's Restart function
		var restartCount atomic.Int32
		restartScheduleTimeCh := make(chan time.Time, 1)
		sc.SetRestartFunc(func(_ context.Context, _ *core.DAG, scheduleTime time.Time) error {
			restartCount.Add(1)
			select {
			case restartScheduleTimeCh <- scheduleTime:
			default:
			}
			return nil
		})

		errCh := startSchedulerAsync(t, sc, ctx)
		defer stopSchedulerAndWait(t, sc, errCh, ctx)

		require.Eventually(t, func() bool {
			return restartCount.Load() >= int32(1)
		}, 5*time.Second, 10*time.Millisecond, "restart should have been called")
		select {
		case restartScheduleTime := <-restartScheduleTimeCh:
			require.False(t, restartScheduleTime.IsZero())
		default:
			t.Fatal("restart schedule time was not recorded")
		}
	})
	t.Run("Start", func(t *testing.T) {
		ctx := context.Background()
		now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

		cronParser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		parsed, err := cronParser.Parse("0 * * * *")
		require.NoError(t, err)

		entryReader := newMockJobManager()
		entryReader.LoadedDAGs = []*core.DAG{
			{
				Name: "start-dag",
				Schedule: []core.Schedule{
					{Expression: "0 * * * *", Parsed: parsed},
				},
			},
		}

		th := test.SetupScheduler(t)
		sc, err := scheduler.New(th.Config, entryReader, th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
		require.NoError(t, err)
		sc.SetClock(func() time.Time { return now })

		var dispatchCount atomic.Int32
		sc.SetDispatchFunc(func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
			dispatchCount.Add(1)
			return nil
		})

		errCh := startSchedulerAsync(t, sc, ctx)
		defer stopSchedulerAndWait(t, sc, errCh, ctx)

		require.Eventually(t, func() bool {
			return dispatchCount.Load() >= int32(1)
		}, 5*time.Second, 10*time.Millisecond, "dispatch should have been called for start schedule")
	})
	t.Run("NextTick", func(t *testing.T) {
		th := test.SetupScheduler(t)
		schedulerInstance, err := scheduler.New(th.Config, newMockJobManager(), th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
		require.NoError(t, err)

		tests := []struct {
			name     string
			now      time.Time
			expected time.Time
		}{
			{
				name:     "MidMinute",
				now:      time.Date(2020, 1, 1, 1, 0, 50, 0, time.UTC),
				expected: time.Date(2020, 1, 1, 1, 1, 0, 0, time.UTC),
			},
			{
				name:     "OnMinuteBoundary",
				now:      time.Date(2020, 1, 1, 1, 0, 0, 0, time.UTC),
				expected: time.Date(2020, 1, 1, 1, 1, 0, 0, time.UTC),
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				next := schedulerInstance.NextTick(tt.now)
				require.Equal(t, tt.expected, next)
			})
		}
	})
}

func TestFileLockPreventsMultipleInstances(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	entryReader := newMockJobManager()

	th := setupSchedulerWithoutDAGs(t)

	// Create first scheduler instance
	sc1, err := scheduler.New(th.Config, entryReader, th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
	require.NoError(t, err)
	sc1.SetClock(clock)

	// Start first scheduler
	ctx := context.Background()
	errCh1 := startSchedulerAsync(t, sc1, ctx)
	waitStarted := make(chan struct{}, 1)

	// Create second scheduler instance with same config
	sc2, err := scheduler.NewWithHooksForTest(
		th.Config,
		newMockJobManager(),
		th.DAGRunMgr,
		th.DAGRunStore,
		th.QueueStore,
		th.ProcStore,
		th.ServiceRegistry,
		th.CoordinatorCli,
		nil,
		scheduler.TestHooks{
			OnLockWait: func() {
				select {
				case waitStarted <- struct{}{}:
				default:
				}
			},
		},
	)
	require.NoError(t, err)
	sc2.SetClock(clock)

	// Try to start second scheduler - should wait for lock
	errCh2 := startWaitingSchedulerAsync(t, sc2, ctx, waitStarted)
	// Check if second scheduler is still not running
	require.False(t, sc2.IsRunning(), "Second scheduler should not be running while first one is active")

	// Stop first scheduler
	sc1.Stop(ctx)

	// Wait for the first scheduler to finish
	select {
	case err := <-errCh1:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("First scheduler did not stop in time")
	}

	require.False(t, sc1.IsRunning(), "First scheduler should not be running after stop")

	// Give second scheduler time to start
	requireSchedulerRunning(t, sc2, errCh2)

	// Stop second scheduler to clean up
	stopSchedulerAndWait(t, sc2, errCh2, ctx)
}

func TestScheduler_StopSchedule(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	cronParser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	parsed, err := cronParser.Parse("0 * * * *")
	require.NoError(t, err)

	entryReader := newMockJobManager()
	entryReader.LoadedDAGs = []*core.DAG{
		{
			Name: "stop-dag",
			StopSchedule: []core.Schedule{
				{Expression: "0 * * * *", Parsed: parsed},
			},
		},
	}

	th := setupSchedulerWithoutDAGs(t)
	sc, err := scheduler.New(th.Config, entryReader, th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
	require.NoError(t, err)
	sc.SetClock(func() time.Time { return now })
	sc.SetDispatchFunc(func(_ context.Context, _ *core.DAG, _ string, _ core.TriggerType, _ time.Time) error {
		return nil
	})

	// GetLatestStatus must return Running for the stop guard
	sc.SetGetLatestStatusFunc(func(_ context.Context, _ *core.DAG) (exec.DAGRunStatus, error) {
		return exec.DAGRunStatus{Status: core.Running}, nil
	})

	var stopCount atomic.Int32
	sc.SetStopFunc(func(_ context.Context, _ *core.DAG) error {
		stopCount.Add(1)
		return nil
	})

	ctx := context.Background()
	errCh := startSchedulerAsync(t, sc, ctx)
	defer stopSchedulerAndWait(t, sc, errCh, ctx)

	require.Eventually(t, func() bool {
		return stopCount.Load() >= int32(1)
	}, 5*time.Second, 10*time.Millisecond, "stop function should have been called")
}

func TestScheduler_GracefulShutdown(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	cronParser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	parsed, err := cronParser.Parse("*/5 * * * *")
	require.NoError(t, err)

	entryReader := newMockJobManager()
	entryReader.LoadedDAGs = []*core.DAG{
		{
			Name: "shutdown-dag",
			Schedule: []core.Schedule{
				{Expression: "*/5 * * * *", Parsed: parsed},
			},
		},
	}

	th := setupSchedulerWithoutDAGs(t)
	sc, err := scheduler.New(th.Config, entryReader, th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
	require.NoError(t, err)
	sc.SetClock(func() time.Time { return now })

	ctx := context.Background()
	errCh := startSchedulerAsync(t, sc, ctx)

	// Stop and verify it completes within 5 seconds
	done := make(chan struct{})
	go func() {
		sc.Stop(ctx)
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return within 5 seconds")
	}

	require.Eventually(t, func() bool {
		return !sc.IsRunning()
	}, schedulerTestTimeout(5*time.Second), 10*time.Millisecond, "scheduler should not be running after stop")

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler Start() did not return after Stop()")
	}
}

func TestScheduler_StopReleasesLock(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	th := setupSchedulerWithoutDAGs(t)
	ctx := context.Background()

	// Start and stop first scheduler.
	sc1, err := scheduler.New(th.Config, newMockJobManager(), th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
	require.NoError(t, err)
	sc1.SetClock(clock)

	errCh := startSchedulerAsync(t, sc1, ctx)
	stopSchedulerAndWait(t, sc1, errCh, ctx)

	// A second scheduler must be able to acquire the lock immediately
	// (no 30s stale wait) because Stop() released it.
	sc2, err := scheduler.New(th.Config, newMockJobManager(), th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
	require.NoError(t, err)
	sc2.SetClock(clock)

	errCh2 := startSchedulerAsync(t, sc2, ctx)
	defer stopSchedulerAndWait(t, sc2, errCh2, ctx)
}

func TestScheduler_StopAfterContextCancellation(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	th := setupSchedulerWithoutDAGs(t)

	sc, err := scheduler.New(th.Config, newMockJobManager(), th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
	require.NoError(t, err)
	sc.SetClock(clock)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := startSchedulerAsync(t, sc, ctx)

	// Cancel context first (simulates SIGINT in startall), then call Stop().
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler Start() did not return after context cancellation")
	}

	// Stop() after Start() returned must still complete cleanup (lock release).
	done := make(chan struct{})
	go func() {
		sc.Stop(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return within 5 seconds after context cancellation")
	}

	// Verify lock was released: a new scheduler can start immediately.
	sc2, err := scheduler.New(th.Config, newMockJobManager(), th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
	require.NoError(t, err)
	sc2.SetClock(clock)

	errCh2 := startSchedulerAsync(t, sc2, context.Background())
	defer stopSchedulerAndWait(t, sc2, errCh2, context.Background())
}

func TestScheduler_StopWhileWaitingForLock(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	th := setupSchedulerWithoutDAGs(t)

	sc1, err := scheduler.New(th.Config, newMockJobManager(), th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
	require.NoError(t, err)
	sc1.SetClock(clock)

	ctx := context.Background()
	errCh1 := startSchedulerAsync(t, sc1, ctx)
	defer stopSchedulerAndWait(t, sc1, errCh1, ctx)
	waitStarted := make(chan struct{}, 1)

	sc2, err := scheduler.NewWithHooksForTest(
		th.Config,
		newMockJobManager(),
		th.DAGRunMgr,
		th.DAGRunStore,
		th.QueueStore,
		th.ProcStore,
		th.ServiceRegistry,
		th.CoordinatorCli,
		nil,
		scheduler.TestHooks{
			OnLockWait: func() {
				select {
				case waitStarted <- struct{}{}:
				default:
				}
			},
		},
	)
	require.NoError(t, err)
	sc2.SetClock(clock)

	errCh2 := startWaitingSchedulerAsync(t, sc2, ctx, waitStarted)
	require.False(t, sc2.IsRunning(), "Second scheduler should still be waiting on the lock")

	stopDone := make(chan struct{})
	go func() {
		sc2.Stop(ctx)
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() blocked while Start() was waiting on the scheduler lock")
	}

	select {
	case err := <-errCh2:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("waiting scheduler Start() did not return after Stop()")
	}
}

func TestScheduler_StartFailureCleansUpPartialStartup(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	th := setupSchedulerWithoutDAGs(t)

	sc1, err := scheduler.New(
		th.Config,
		&failingInitEntryReader{mockJobManager: newMockJobManager(), initErr: errors.New("init failed")},
		th.DAGRunMgr,
		th.DAGRunStore,
		th.QueueStore,
		th.ProcStore,
		th.ServiceRegistry,
		th.CoordinatorCli,
		nil,
	)
	require.NoError(t, err)
	sc1.SetClock(clock)

	err = sc1.Start(context.Background())
	require.Error(t, err)
	require.ErrorContains(t, err, "init failed")
	require.False(t, sc1.IsRunning())

	sc2, err := scheduler.New(th.Config, newMockJobManager(), th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
	require.NoError(t, err)
	sc2.SetClock(clock)

	ctx := context.Background()
	errCh2 := startSchedulerAsync(t, sc2, ctx)
	defer stopSchedulerAndWait(t, sc2, errCh2, ctx)
}

func TestScheduler_SelfFencesOnOwnershipLoss(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	th := setupSchedulerWithoutDAGs(t)
	ctx := context.Background()

	sc, err := scheduler.New(th.Config, newMockJobManager(), th.DAGRunMgr, th.DAGRunStore, th.QueueStore, th.ProcStore, th.ServiceRegistry, th.CoordinatorCli, nil)
	require.NoError(t, err)
	sc.SetClock(clock)

	errCh := startSchedulerAsync(t, sc, ctx)

	// Simulate lock theft: remove the lock dir and recreate it with a different token
	lockDir := filepath.Join(th.Config.Paths.DataDir, "scheduler", "locks", ".dagu_lock")
	require.NoError(t, os.RemoveAll(lockDir))
	require.NoError(t, os.MkdirAll(lockDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(lockDir, "owner"), []byte("stolen-token"), 0600))

	// The heartbeat runs every 7s; the scheduler should self-fence and stop
	require.Eventually(t, func() bool {
		return !sc.IsRunning()
	}, 15*time.Second, 100*time.Millisecond, "scheduler should self-fence and stop after lock theft")

	// The replacement lock directory must still exist (not deleted by the old scheduler)
	data, err := os.ReadFile(filepath.Join(lockDir, "owner"))
	require.NoError(t, err)
	require.Equal(t, "stolen-token", string(data))

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler Start() did not return after self-fencing")
	}
}

func startSchedulerAsync(t *testing.T, sc *scheduler.Scheduler, ctx context.Context) chan error {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		errCh <- sc.Start(ctx)
	}()

	requireSchedulerRunning(t, sc, errCh)
	return errCh
}

func startWaitingSchedulerAsync(t *testing.T, sc *scheduler.Scheduler, ctx context.Context, waitStarted <-chan struct{}) chan error {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		errCh <- sc.Start(ctx)
	}()

	select {
	case <-waitStarted:
		return errCh
	case err := <-errCh:
		require.NoError(t, err)
		t.Fatal("scheduler exited before waiting on the lock")
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not begin waiting on the lock")
	}
	return errCh
}

func requireSchedulerRunning(t *testing.T, sc *scheduler.Scheduler, errCh <-chan error) {
	t.Helper()

	var startErr error
	var exited bool
	require.Eventually(t, func() bool {
		if sc.IsRunning() {
			return true
		}
		select {
		case err := <-errCh:
			startErr = err
			exited = true
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond, "scheduler did not start in time")

	if exited {
		require.NoError(t, startErr)
		t.Fatal("scheduler exited before reporting running")
	}
}

func stopSchedulerAndWait(t *testing.T, sc *scheduler.Scheduler, errCh <-chan error, ctx context.Context) {
	t.Helper()

	sc.Stop(ctx)
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler Start() did not return after Stop()")
	}
}

type failingInitEntryReader struct {
	*mockJobManager
	initErr error
}

func (er *failingInitEntryReader) Init(context.Context) error {
	return er.initErr
}
