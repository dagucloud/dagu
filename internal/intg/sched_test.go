// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/test/intgharness"
	"github.com/stretchr/testify/require"
)

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

	// Exercise live cron slots independently of inherited catch-up defaults.
	dagContent := `name: cron-test
schedule: "*/1 * * * *"
catchup_window: ""
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
	var dispatchMu sync.Mutex
	var dispatchTimes []time.Time
	schedulerInstance.SetDispatchFunc(func(_ context.Context, entry scheduler.DAGEntry, _ string, trigger ir.TriggerType, scheduled time.Time) error {
		dag := entry.DAG
		if dag != nil && dag.Name == "cron-test" && trigger == ir.TriggerTypeScheduler {
			dispatchMu.Lock()
			dispatchTimes = append(dispatchTimes, scheduled)
			dispatchMu.Unlock()
			dispatchCount.Add(1)
		}
		return nil
	})

	clockBase := time.Date(2026, 1, 1, 0, 0, 59, 0, time.UTC)
	// Keep startup time stable, then advance after the first scheduled dispatch.
	schedulerInstance.SetClock(func() time.Time {
		if dispatchCount.Load() > 0 {
			return clockBase.Add(time.Minute)
		}
		return clockBase
	})

	ctx, cancel := context.WithCancel(th.Context)
	defer cancel()

	h := intgharness.New(t, th.Helper)
	probe := h.StartScheduler(ctx, schedulerInstance, th.EntryReader)

	_, err = spec.Load(th.Context, dagFile)
	require.NoError(t, err)

	probe.RequireEventually("expected cron schedule to dispatch twice", 15*time.Second, func() bool {
		return dispatchCount.Load() >= 2
	})
	probe.Stop(context.Background(), cancel, 5*time.Second)

	dispatchMu.Lock()
	defer dispatchMu.Unlock()
	require.Len(t, dispatchTimes, 2)
	for i, scheduled := range dispatchTimes {
		want := clockBase.Truncate(time.Minute).Add(time.Duration(i) * time.Minute)
		require.True(t, want.Equal(scheduled), "unexpected scheduled time: %s", scheduled)
	}
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
	dag, err := th.DAGRepository.GetDetails(th.Context, dagName, persis.DAGLoadOptions{})
	require.NoError(t, err)

	// Seed the legacy location to exercise migration before scheduling starts.
	require.NoError(t, os.MkdirAll(th.Config.Paths.SuspendFlagsDirLegacy, 0o755))
	suspendFlag := filepath.Join(th.Config.Paths.SuspendFlagsDirLegacy, dag.SuspendFlagName()+".suspend")
	require.NoError(t, os.WriteFile(suspendFlag, []byte{}, 0o644))
	suspended, err := th.DAGRepository.IsSuspended(th.Context, dag.FileName())
	require.NoError(t, err)
	require.True(t, suspended)

	attempt, err := th.DAGRunRepository.CreateAttempt(th.Context, dag, oldSlot, "old-success", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)

	status := ir.InitialStatus(dag)
	status.DAGRunID = "old-success"
	status.AttemptID = attempt.ID()
	status.Status = ir.Succeeded
	status.TriggerType = ir.TriggerTypeScheduler
	status.ScheduleTime = stringutil.FormatTime(oldSlot)
	status.StartedAt = stringutil.FormatTime(oldSlot.Add(15 * time.Second))
	status.FinishedAt = stringutil.FormatTime(oldSlot.Add(45 * time.Second))

	require.NoError(t, attempt.Open(th.Context))
	require.NoError(t, attempt.Write(th.Context, status))
	require.NoError(t, attempt.Close(th.Context))

	sc, err := th.NewSchedulerInstance(t)
	require.NoError(t, err)

	var (
		dispatchCount    atomic.Int32
		lastDispatchMu   sync.Mutex
		lastDispatchTime time.Time
		lastDispatchType ir.TriggerType
	)
	sc.SetDispatchFunc(func(_ context.Context, entry scheduler.DAGEntry, _ string, trigger ir.TriggerType, scheduleTime time.Time) error {
		dag := entry.DAG
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

	h := intgharness.New(t, th.Helper)
	probe := h.StartScheduler(ctx, sc, th.EntryReader)
	probe.RequireRunningWithSchedule(dagName, "0 10 * * *", 2*time.Second)

	writeSpec("5 10 * * *")

	probe.RequireLoadedSchedule(dagName, "5 10 * * *", 5*time.Second)

	require.NoError(t, th.DAGRepository.SetSuspended(th.Context, dag.FileName(), false))

	probe.RequireEventually("expected edited schedule to dispatch", 35*time.Second, func() bool {
		return dispatchCount.Load() > 0
	})
	require.Equal(t, int32(1), dispatchCount.Load(), "edited schedules should dispatch exactly once")
	lastDispatchMu.Lock()
	require.Equal(t, ir.TriggerTypeScheduler, lastDispatchType)
	require.Equal(t, newSlot, lastDispatchTime)
	lastDispatchMu.Unlock()

	probe.Stop(context.Background(), cancel, 5*time.Second)
}

// observedPauseStore records the scheduler clock at each pause check, so a test
// can prove the planner evaluated a DAG at a given scheduled slot.
type observedPauseStore struct {
	schedulerstate.PauseStore
	now func() time.Time

	mu        sync.Mutex
	lastCheck time.Time
}

func (s *observedPauseStore) IsPaused(ctx context.Context) (bool, error) {
	s.mu.Lock()
	s.lastCheck = s.now()
	s.mu.Unlock()
	return s.PauseStore.IsPaused(ctx)
}

func (s *observedPauseStore) checkedAtOrAfter(t time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.lastCheck.IsZero() && !s.lastCheck.Before(t)
}

// A pause is written by the API process and must be observed by the running
// scheduler without a restart, and reversed the same way.
func TestSchedulerPauseSuppressesDispatchUntilResumed(t *testing.T) {
	tmpDir := t.TempDir()
	dagsDir := filepath.Join(tmpDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0o755))

	const dagName = "scheduler-pause-dispatch"
	dagSpec := "name: " + dagName + "\n" +
		"schedule: \"* * * * *\"\n" +
		"steps:\n" +
		"  - name: step\n" +
		"    command: echo \"hello\"\n"
	require.NoError(t, fileutil.WriteFileAtomic(filepath.Join(dagsDir, dagName+".yaml"), []byte(dagSpec), 0o644))

	th := test.SetupScheduler(t, test.WithDAGsDir(dagsDir))
	require.NoError(t, th.PauseStore.Set(th.Context, true, "admin", "maintenance"))

	// Start shortly before a slot so the paused slot and the slot after the
	// resume both fit inside the test budget.
	clockBase := time.Date(2026, 4, 27, 10, 0, 50, 0, time.UTC)
	clockStart := time.Now()
	schedulerNow := func() time.Time { return clockBase.Add(time.Since(clockStart)) }

	observed := &observedPauseStore{PauseStore: th.PauseStore, now: schedulerNow}
	th.PauseStore = observed

	sc, err := th.NewSchedulerInstance(t)
	require.NoError(t, err)
	sc.SetClock(schedulerNow)

	var dispatchCount atomic.Int32
	sc.SetDispatchFunc(func(_ context.Context, entry scheduler.DAGEntry, _ string, _ ir.TriggerType, _ time.Time) error {
		if entry.DAG != nil && entry.DAG.Name == dagName {
			dispatchCount.Add(1)
		}
		return nil
	})

	ctx, cancel := context.WithCancel(th.Context)
	defer cancel()

	h := intgharness.New(t, th.Helper)
	probe := h.StartScheduler(ctx, sc, th.EntryReader)
	probe.RequireRunningWithSchedule(dagName, "* * * * *", 30*time.Second)

	// Derive the slot from when registration actually finished, not from the
	// clock base. On a slow runner registration can outlast the first slot, and
	// a slot fixed in advance would then have passed before the DAG was
	// schedulable at all, making the assertion below vacuous.
	pausedSlot := schedulerNow().Truncate(time.Minute).Add(time.Minute)

	// Wait for that slot to elapse, plus enough grace for the tick to run. This
	// gate is independent of the pause mechanism on purpose: a pause that stops
	// working then shows up as a dispatch rather than as a missing signal.
	probe.RequireEventually("expected a scheduled slot to elapse while paused", 90*time.Second, func() bool {
		return schedulerNow().After(pausedSlot.Add(3 * time.Second))
	})
	require.Zero(t, dispatchCount.Load(), "paused scheduler must not dispatch scheduled runs")
	require.True(t, observed.checkedAtOrAfter(pausedSlot),
		"planner must consult the pause flag at a scheduled slot")

	require.NoError(t, th.PauseStore.Set(th.Context, false, "", ""))

	probe.RequireEventually("expected dispatch after resume", 120*time.Second, func() bool {
		return dispatchCount.Load() > 0
	})

	probe.Stop(context.Background(), cancel, 5*time.Second)
}
