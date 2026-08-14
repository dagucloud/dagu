// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/test/intgharness"
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

	dag, err := th.DAGRepository.GetDetails(th.Context, "one-off-restart-test", persis.DAGLoadOptions{})
	require.NoError(t, err)
	require.Len(t, dag.Schedule, 1)

	stateStore := store.NewSchedulerStateStore(
		file.NewCollection(filepath.Join(th.Config.Paths.DataDir, "scheduler")),
	)
	fingerprint := dag.Schedule[0].Fingerprint()
	runID := scheduler.GenerateOneOffRunID(dag.Name, fingerprint, scheduledAt)

	require.NoError(t, stateStore.Save(th.Context, &schedulerstate.State{
		DAGs: map[string]schedulerstate.DAGWatermark{
			dag.Name: {
				OneOffs: map[string]schedulerstate.OneOffScheduleState{
					fingerprint: {
						ScheduledTime: scheduledAt,
						Status:        schedulerstate.OneOffStatusPending,
					},
				},
			},
		},
	}))

	attempt, err := th.DAGRunRepository.CreateAttempt(th.Context, dag, scheduledAt, runID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	initialStatus := ir.InitialStatus(dag)
	initialStatus.DAGRunID = runID
	initialStatus.AttemptID = attempt.ID()
	initialStatus.TriggerType = ir.TriggerTypeScheduler
	initialStatus.ScheduleTime = scheduledAt.Format(time.RFC3339)
	require.NoError(t, attempt.Open(th.Context))
	require.NoError(t, attempt.Write(th.Context, initialStatus))
	require.NoError(t, attempt.Close(th.Context))

	sc, err := scheduler.New(th.Config, scheduler.Dependencies{
		EntryReader:         th.EntryReader,
		DAGRunManager:       th.DAGRunMgr,
		DAGRepository:       th.DAGRepository,
		DAGRunRepository:    th.DAGRunRepository,
		QueueStore:          th.QueueStore,
		ProcRepository:      th.ProcRepository,
		ServiceRegistry:     th.ServiceRegistry,
		CoordinatorClient:   th.CoordinatorCli,
		SchedulerStateStore: stateStore,
	})
	require.NoError(t, err)
	sc.SetClock(func() time.Time { return scheduledAt })

	var dispatchCount atomic.Int32
	sc.SetDispatchFunc(func(context.Context, scheduler.DAGEntry, string, ir.TriggerType, time.Time) error {
		dispatchCount.Add(1)
		return nil
	})

	ctx, cancel := context.WithCancel(th.Context)
	defer cancel()

	h := intgharness.New(t, th.Helper)
	probe := h.StartScheduler(ctx, sc, th.EntryReader)

	probe.RequireEventually("expected one-off schedule to be consumed", 5*time.Second, func() bool {
		state, err := stateStore.Load(th.Context)
		if err != nil {
			return false
		}
		entry, ok := state.DAGs[dag.Name]
		if !ok {
			return false
		}
		oneOff, ok := entry.OneOffs[fingerprint]
		return ok && oneOff.Status == schedulerstate.OneOffStatusConsumed
	})

	assert.Equal(t, int32(0), dispatchCount.Load())
	statuses, err := th.DAGRunRepository.RecentStatuses(th.Context, dag.Name, 10)
	require.NoError(t, err)
	assert.Len(t, statuses, 1)

	probe.Stop(context.Background(), cancel, 5*time.Second)
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

	dag, err := th.DAGRepository.GetDetails(th.Context, "one-off-env-secret-test", persis.DAGLoadOptions{})
	require.NoError(t, err)
	require.Len(t, dag.Schedule, 1)

	sc, err := th.NewSchedulerInstance(t)
	require.NoError(t, err)
	sc.SetClock(func() time.Time { return scheduledAt })

	ctx, cancel := context.WithCancel(th.Context)
	defer cancel()

	h := intgharness.New(t, th.Helper)
	probe := h.StartScheduler(ctx, sc, th.EntryReader)

	probe.RequireEventually("expected one-off env secret run to succeed", 30*time.Second, func() bool {
		statuses, err := th.DAGRunRepository.RecentStatuses(th.Context, dag.Name, 5)
		if err != nil {
			return false
		}
		return len(statuses) > 0 && statuses[0].Status == ir.Succeeded
	})

	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status)
	require.Equal(t, ir.TriggerTypeScheduler, status.TriggerType)
	require.Equal(t, masking.DefaultMaskString+"|", test.StatusOutputValue(t, &status, "RESULT"))

	probe.Stop(context.Background(), cancel, 5*time.Second)
}
