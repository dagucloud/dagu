// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/filewatermark"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOneOffScheduleRestartConsumesExistingRun(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dagsDir := filepath.Join(tmpDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	scheduledAt := time.Date(2026, 3, 29, 2, 10, 0, 0, time.UTC)
	dagContent := fmt.Sprintf(`name: one-off-restart-test
schedule:
  start:
    - at: "%s"
steps:
  - name: step1
    run: echo "hello"
`, scheduledAt.Format(time.RFC3339))
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "one-off-restart-test.yaml"), []byte(dagContent), 0644))

	th := test.SetupScheduler(t, test.WithDAGsDir(dagsDir))
	th.Config.Scheduler.RetryFailureWindow = 0

	dag, err := th.DAGStore.GetDetails(th.Context, "one-off-restart-test")
	require.NoError(t, err)
	require.Len(t, dag.Schedule, 1)

	watermarkStore := filewatermark.New(filepath.Join(th.Config.Paths.DataDir, "scheduler"))
	fingerprint := dag.Schedule[0].Fingerprint()
	runID := scheduler.GenerateOneOffRunID(dag.Name, fingerprint, scheduledAt)

	require.NoError(t, watermarkStore.Save(th.Context, &scheduler.SchedulerState{
		Version: scheduler.SchedulerStateVersion,
		DAGs: map[string]scheduler.DAGWatermark{
			dag.Name: {
				OneOffs: map[string]scheduler.OneOffScheduleState{
					fingerprint: {
						ScheduledTime: scheduledAt,
						Status:        scheduler.OneOffStatusPending,
					},
				},
			},
		},
	}))

	attempt, err := th.DAGRunStore.CreateAttempt(th.Context, dag, scheduledAt, runID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	initialStatus := exec.InitialStatus(dag)
	initialStatus.DAGRunID = runID
	initialStatus.AttemptID = attempt.ID()
	initialStatus.TriggerType = core.TriggerTypeScheduler
	initialStatus.ScheduleTime = scheduledAt.Format(time.RFC3339)
	require.NoError(t, attempt.Open(th.Context))
	require.NoError(t, attempt.Write(th.Context, initialStatus))
	require.NoError(t, attempt.Close(th.Context))

	sc, err := scheduler.New(
		th.Config,
		th.EntryReader,
		th.DAGRunMgr,
		th.DAGRunStore,
		th.QueueStore,
		th.ProcStore,
		th.ServiceRegistry,
		th.CoordinatorCli,
		watermarkStore,
	)
	require.NoError(t, err)
	sc.SetClock(func() time.Time { return scheduledAt })

	var dispatchCount atomic.Int32
	sc.SetDispatchFunc(func(context.Context, *core.DAG, string, core.TriggerType, time.Time) error {
		dispatchCount.Add(1)
		return nil
	})

	ctx, cancel := context.WithCancel(th.Context)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- sc.Start(ctx)
	}()

	require.Eventually(t, func() bool {
		state, err := watermarkStore.Load(th.Context)
		if err != nil {
			return false
		}
		entry, ok := state.DAGs[dag.Name]
		if !ok {
			return false
		}
		oneOff, ok := entry.OneOffs[fingerprint]
		return ok && oneOff.Status == scheduler.OneOffStatusConsumed
	}, intgTestTimeout(5*time.Second), 50*time.Millisecond)

	assert.Equal(t, int32(0), dispatchCount.Load())
	assert.Len(t, th.DAGRunStore.RecentAttempts(th.Context, dag.Name, 10), 1)

	sc.Stop(context.Background())
	cancel()

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

func TestOneOffScheduleResolvesEnvSecretsWithoutLeakingSourceEnv(t *testing.T) {
	const rawVar = "ONE_OFF_ENV_SECRET_SOURCE"

	t.Setenv(rawVar, "from-host")

	tmpDir := t.TempDir()
	dagsDir := filepath.Join(tmpDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0755))

	scheduledAt := time.Date(2026, 3, 29, 2, 20, 0, 0, time.UTC)
	dagContent := fmt.Sprintf(`name: one-off-env-secret-test
schedule:
  start:
    - at: "%s"
secrets:
  - name: EXPORTED_SECRET
    provider: env
    key: %s
steps:
  - name: capture
    run: printf '%%s|%%s' "$EXPORTED_SECRET" "${%s:-}"
    output: RESULT
`, scheduledAt.Format(time.RFC3339), rawVar, rawVar)
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "one-off-env-secret-test.yaml"), []byte(dagContent), 0644))

	th := test.SetupScheduler(t, test.WithBuiltExecutable(), test.WithDAGsDir(dagsDir))

	dag, err := th.DAGStore.GetDetails(th.Context, "one-off-env-secret-test")
	require.NoError(t, err)
	require.Len(t, dag.Schedule, 1)

	sc, err := th.NewSchedulerInstance(t)
	require.NoError(t, err)
	sc.SetClock(func() time.Time { return scheduledAt })

	ctx, cancel := context.WithCancel(th.Context)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- sc.Start(ctx)
	}()

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

	require.Eventually(t, func() bool {
		if err := pollSchedulerErr(); err != nil {
			return true
		}
		statuses := th.DAGRunMgr.ListRecentStatus(th.Context, dag.Name, 5)
		return len(statuses) > 0 && statuses[0].Status == core.Succeeded
	}, 10*time.Second, 100*time.Millisecond)
	require.NoError(t, schedulerErr)

	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, status.Status)
	require.Equal(t, core.TriggerTypeScheduler, status.TriggerType)
	require.Equal(t, "from-host|", test.StatusOutputValue(t, &status, "RESULT"))

	sc.Stop(context.Background())
	cancel()

	if !schedulerStopped {
		select {
		case err = <-errCh:
			require.True(t,
				err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded),
				"unexpected scheduler shutdown error: %v", err,
			)
		case <-time.After(5 * time.Second):
			t.Fatal("scheduler did not stop within 5 seconds")
		}
	}
}
