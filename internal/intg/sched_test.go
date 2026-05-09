// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func cronScheduleRunsTwiceTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 30 * time.Second
	}
	return 15 * time.Second
}

// TestCronScheduleRunsTwice verifies that a DAG with */1 * * * * schedule
// runs twice in two minutes.
func TestCronScheduleRunsTwice(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Parallel()
	}

	tmpDir, err := os.MkdirTemp("", "dagu-cron-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	dagsDir := filepath.Join(tmpDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	dagContent := `name: cron-test
schedule: "*/1 * * * *"
steps:
  - name: test-step
    run: echo "hello"
`
	dagFile := filepath.Join(dagsDir, "cron-test.yaml")
	require.NoError(t, os.WriteFile(dagFile, []byte(dagContent), 0644))

	th := test.SetupScheduler(t, test.WithDAGsDir(dagsDir))
	schedulerInstance, err := th.NewSchedulerInstance(t)
	require.NoError(t, err)

	var dispatchCount atomic.Int32
	schedulerInstance.SetDispatchFunc(func(_ context.Context, dag *core.DAG, _ string, trigger core.TriggerType, _ time.Time) error {
		if dag != nil && dag.Name == "cron-test" && trigger == core.TriggerTypeScheduler {
			dispatchCount.Add(1)
		}
		return nil
	})

	clockBase := time.Date(2026, 1, 1, 0, 0, 59, 0, time.UTC)
	// Keep the simulated clock stable so scheduler startup latency cannot skip
	// the initial tick. The cron loop advances its tick cursor independently.
	schedulerInstance.SetClock(func() time.Time {
		return clockBase
	})

	ctx, cancel := context.WithCancel(th.Context)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- schedulerInstance.Start(ctx) }()
	var schedulerErr error
	var schedulerStopped bool
	pollSchedulerErr := func() error {
		if schedulerStopped {
			return schedulerErr
		}
		select {
		case err := <-errCh:
			schedulerStopped = true
			if err == nil {
				err = errors.New("scheduler exited unexpectedly before test completed")
			}
			schedulerErr = err
		default:
		}
		return schedulerErr
	}

	_, err = spec.Load(th.Context, dagFile)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		if err := pollSchedulerErr(); err != nil {
			return true
		}
		return dispatchCount.Load() >= 2
	}, cronScheduleRunsTwiceTimeout(), 50*time.Millisecond)
	require.NoError(t, schedulerErr)

	schedulerInstance.Stop(ctx)
	cancel()

	if !schedulerStopped {
		select {
		case err = <-errCh:
			require.True(t,
				err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded),
				"unexpected scheduler shutdown error: %v", err,
			)
		case <-time.After(5 * time.Second):
		}
	}

	require.GreaterOrEqual(t, dispatchCount.Load(), int32(2))
}

func TestScheduleEditWhileSuspendedDoesNotSuppressNewSlot(t *testing.T) {
	tmpDir := t.TempDir()
	dagsDir := filepath.Join(tmpDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0o755))

	const dagName = "issue-2042-skip-success"
	dagPath := filepath.Join(dagsDir, dagName+".yaml")

	writeSpec := func(schedule string) {
		spec := "name: " + dagName + "\n" +
			"schedule: \"" + schedule + "\"\n" +
			"skip_if_successful: true\n" +
			"steps:\n" +
			"  - name: step\n" +
			"    command: echo \"hello\"\n"
		require.NoError(t, fileutil.WriteFileAtomic(dagPath, []byte(spec), 0o644))
	}

	oldSlot := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	newSlot := time.Date(2026, 4, 27, 10, 5, 0, 0, time.UTC)
	writeSpec("0 10 * * *")

	th := test.SetupScheduler(t, test.WithDAGsDir(dagsDir))
	dag, err := th.DAGStore.GetDetails(th.Context, dagName)
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(th.Config.Paths.SuspendFlagsDir, 0o755))
	suspendFlag := filepath.Join(th.Config.Paths.SuspendFlagsDir, dag.SuspendFlagName())
	require.NoError(t, os.WriteFile(suspendFlag, []byte{}, 0o644))

	attempt, err := th.DAGRunStore.CreateAttempt(th.Context, dag, oldSlot, "old-success", exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)

	status := exec.InitialStatus(dag)
	status.DAGRunID = "old-success"
	status.AttemptID = attempt.ID()
	status.Status = core.Succeeded
	status.TriggerType = core.TriggerTypeScheduler
	status.ScheduleTime = exec.FormatTime(oldSlot)
	status.StartedAt = exec.FormatTime(oldSlot.Add(15 * time.Second))
	status.FinishedAt = exec.FormatTime(oldSlot.Add(45 * time.Second))

	require.NoError(t, attempt.Open(th.Context))
	require.NoError(t, attempt.Write(th.Context, status))
	require.NoError(t, attempt.Close(th.Context))

	sc, err := th.NewSchedulerInstance(t)
	require.NoError(t, err)

	var (
		dispatchCount    atomic.Int32
		lastDispatchMu   sync.Mutex
		lastDispatchTime time.Time
		lastDispatchType core.TriggerType
	)
	sc.SetDispatchFunc(func(_ context.Context, dag *core.DAG, _ string, trigger core.TriggerType, scheduleTime time.Time) error {
		if dag != nil && dag.Name == dagName {
			dispatchCount.Add(1)
			lastDispatchMu.Lock()
			lastDispatchType = trigger
			lastDispatchTime = scheduleTime
			lastDispatchMu.Unlock()
		}
		return nil
	})

	clockBase := time.Date(2026, 4, 27, 10, 4, 30, 0, time.UTC)
	clockStart := time.Now()
	sc.SetClock(func() time.Time {
		return clockBase.Add(time.Since(clockStart))
	})

	ctx, cancel := context.WithCancel(th.Context)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- sc.Start(ctx) }()

	var schedulerErr error
	var schedulerStopped bool
	pollSchedulerErr := func() error {
		if schedulerStopped {
			return schedulerErr
		}
		select {
		case err := <-errCh:
			schedulerStopped = true
			if err == nil {
				err = errors.New("scheduler exited unexpectedly before test completed")
			}
			schedulerErr = err
		default:
		}
		return schedulerErr
	}

	schedulerHasSchedule := func(expression string) bool {
		for _, loaded := range th.EntryReader.DAGs() {
			if loaded.Name != dagName || len(loaded.Schedule) != 1 {
				continue
			}
			return loaded.Schedule[0].Expression == expression
		}
		return false
	}

	require.Eventually(t, func() bool {
		if err := pollSchedulerErr(); err != nil {
			return true
		}
		return sc.IsRunning() && schedulerHasSchedule("0 10 * * *")
	}, intgTestTimeout(2*time.Second), 50*time.Millisecond)
	require.NoError(t, schedulerErr)

	writeSpec("5 10 * * *")

	require.Eventually(t, func() bool {
		if err := pollSchedulerErr(); err != nil {
			return true
		}
		return schedulerHasSchedule("5 10 * * *")
	}, intgTestTimeout(5*time.Second), 50*time.Millisecond)
	require.NoError(t, schedulerErr)

	require.NoError(t, os.Remove(suspendFlag))

	require.Eventually(t, func() bool {
		if err := pollSchedulerErr(); err != nil {
			return true
		}
		return dispatchCount.Load() > 0
	}, intgTestTimeout(35*time.Second), 50*time.Millisecond)
	require.NoError(t, schedulerErr)
	require.Equal(t, int32(1), dispatchCount.Load(), "edited schedules should dispatch exactly once")
	lastDispatchMu.Lock()
	require.Equal(t, core.TriggerTypeScheduler, lastDispatchType)
	require.Equal(t, newSlot, lastDispatchTime)
	lastDispatchMu.Unlock()

	sc.Stop(context.Background())
	cancel()

	if !schedulerStopped {
		select {
		case err := <-errCh:
			require.True(t,
				err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded),
				"unexpected scheduler shutdown error: %v", err,
			)
		case <-time.After(intgTestTimeout(5 * time.Second)):
			t.Fatal("scheduler did not stop within 5 seconds")
		}
	}
}
