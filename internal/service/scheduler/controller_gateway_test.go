// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/controller"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	persisfile "github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type controllerGatewayAttempt struct {
	exec.DAGRunAttempt
	status     *exec.DAGRunStatus
	dag        *core.DAG
	dagReads   int
	outputs    *exec.DAGRunOutputs
	outputsErr error
}

func (a *controllerGatewayAttempt) ID() string {
	if a.status == nil {
		return ""
	}
	return a.status.AttemptID
}

func (a *controllerGatewayAttempt) ReadStatus(context.Context) (*exec.DAGRunStatus, error) {
	return a.status, nil
}

func (a *controllerGatewayAttempt) ReadDAG(context.Context) (*core.DAG, error) {
	a.dagReads++
	return a.dag, nil
}

func (a *controllerGatewayAttempt) ReadOutputs(context.Context) (*exec.DAGRunOutputs, error) {
	return a.outputs, a.outputsErr
}

type controllerGatewayRunStore struct {
	exec.DAGRunStore
	attempt *controllerGatewayAttempt
}

func (s *controllerGatewayRunStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	return s.attempt, nil
}

type controllerGatewayRotatingRunStore struct {
	exec.DAGRunStore
	attempts []*controllerGatewayAttempt
	next     int
}

func (s *controllerGatewayRotatingRunStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	if s.next < len(s.attempts) {
		attempt := s.attempts[s.next]
		s.next++
		return attempt, nil
	}
	return s.attempts[len(s.attempts)-1], nil
}

func (s *controllerGatewayRunStore) CompareAndSwapLatestAttemptStatus(
	_ context.Context,
	_ exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
	_ ...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	if s.attempt.status.AttemptID != expectedAttemptID || s.attempt.status.Status != expectedStatus {
		return s.attempt.status, false, nil
	}
	if err := mutate(s.attempt.status); err != nil {
		return nil, false, err
	}
	return s.attempt.status, true, nil
}

func TestControllerChildRunGatewayUsesExistingQueueIntakeAndRecovery(t *testing.T) {
	fixture := newQueueFixture(t)
	root := filepath.Dir(fixture.distributedDir)
	cfg := &config.Config{
		Core:   config.Core{SkipExamples: true},
		Queues: config.Queues{Enabled: true},
		Paths: config.PathsConfig{
			DAGsDir:         filepath.Join(root, "dags"),
			SuspendFlagsDir: filepath.Join(root, "suspend"),
			LogDir:          filepath.Join(root, "logs"),
			ArtifactDir:     filepath.Join(root, "artifacts"),
			DataDir:         filepath.Join(root, "data"),
		},
	}
	dagStore, err := persisfile.NewDAGStore(cfg, persisfile.WithDAGSkipExamples(true))
	require.NoError(t, err)
	require.NoError(t, dagStore.Create(fixture.ctx, "controller-child", []byte(`name: controller-child
params:
  - name: sequence
    type: integer
    required: true
steps:
  - name: work
    run: echo done
`)))

	scheduler := &Scheduler{
		config:      cfg,
		dagRunStore: fixture.dagRunStore,
		queueStore:  fixture.queueStore,
		procStore:   fixture.procStore,
	}
	gateway := scheduler.NewControllerChildRunGateway(dagStore, nil)
	request := controller.ChildRunRequest{
		DAG:      "controller-child",
		DAGRunID: "controller-run-1",
		Params:   []byte(`{"sequence":9007199254740993}`),
	}

	require.NoError(t, gateway.EnsureEnqueued(fixture.ctx, request))
	attempt, err := fixture.dagRunStore.FindAttempt(
		fixture.ctx,
		exec.NewDAGRunRef(request.DAG, request.DAGRunID),
	)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(fixture.ctx)
	require.NoError(t, err)
	assert.Equal(t, core.Queued, status.Status)
	assert.Equal(t, []string{"sequence=9007199254740993"}, status.ParamsList)
	assert.Equal(t, "sequence=9007199254740993", status.Params)

	items, err := fixture.queueStore.List(fixture.ctx, request.DAG)
	require.NoError(t, err)
	require.Len(t, items, 1)
	queuedRef, err := items[0].Data()
	require.NoError(t, err)
	assert.Equal(t, exec.NewDAGRunRef(request.DAG, request.DAGRunID), *queuedRef)

	require.NoError(t, gateway.EnsureEnqueued(fixture.ctx, request))
	items, err = fixture.queueStore.List(fixture.ctx, request.DAG)
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func TestControllerChildRunGatewayDoesNotEnqueueWhenQueuesAreDisabled(t *testing.T) {
	t.Parallel()

	gateway := (&Scheduler{config: &config.Config{Queues: config.Queues{Enabled: false}}}).NewControllerChildRunGateway(nil, nil)
	err := gateway.EnsureEnqueued(context.Background(), controller.ChildRunRequest{
		DAG:      "controller-child",
		DAGRunID: "controller-run-1",
		Params:   []byte(`{}`),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "queues are disabled")
}

func TestControllerChildRunGatewayWaitsForAndCancelsPendingDAGAutoRetry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	status := &exec.DAGRunStatus{
		Name:              "controller-child",
		DAGRunID:          "controller-run-1",
		AttemptID:         "attempt-1",
		Status:            core.Failed,
		CreatedAt:         now.Add(-time.Minute).UnixMilli(),
		FinishedAt:        now.Format(time.RFC3339),
		AutoRetryCount:    0,
		AutoRetryLimit:    2,
		AutoRetryInterval: 5 * time.Minute,
		ProcGroup:         "controller-child",
	}
	store := &controllerGatewayRunStore{attempt: &controllerGatewayAttempt{status: status}}
	scheduler := &Scheduler{
		config:      &config.Config{Scheduler: config.Scheduler{RetryFailureWindow: time.Hour}},
		clock:       func() time.Time { return now },
		dagRunStore: store,
		retryScanner: &RetryScanner{
			retryWindow: time.Hour,
			clock:       func() time.Time { return now },
		},
	}
	gateway := &controllerChildRunGateway{scheduler: scheduler}
	request := controller.ChildRunRequest{DAG: status.Name, DAGRunID: status.DAGRunID}

	observation, err := gateway.Observe(context.Background(), request)
	require.NoError(t, err)
	assert.True(t, observation.Exists)
	assert.Equal(t, core.Running, observation.Status)

	require.NoError(t, gateway.Stop(context.Background(), request))
	assert.Equal(t, core.Aborted, status.Status)

	status.Status = core.Failed
	status.AutoRetryCount = status.AutoRetryLimit
	pending, err := gateway.pendingAutoRetryStatus(context.Background(), status, store.attempt)
	require.NoError(t, err)
	assert.Nil(t, pending)
	status.AutoRetryCount = 0
	status.CreatedAt = now.Add(-2 * time.Hour).UnixMilli()
	pending, err = gateway.pendingAutoRetryStatus(context.Background(), status, store.attempt)
	require.NoError(t, err)
	assert.Nil(t, pending)
	status.CreatedAt = now.Add(-time.Minute).UnixMilli()
	scheduler.retryScanner.retryWindow = 0
	pending, err = gateway.pendingAutoRetryStatus(context.Background(), status, store.attempt)
	require.NoError(t, err)
	assert.Nil(t, pending)
	assert.Zero(t, store.attempt.dagReads)
}

func TestControllerChildRunGatewayTreatsSuspendedPendingAutoRetryAsTerminal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	status := &exec.DAGRunStatus{
		Name:              "controller-child",
		DAGRunID:          "controller-run-1",
		AttemptID:         "attempt-1",
		Status:            core.Failed,
		CreatedAt:         now.Add(-time.Minute).UnixMilli(),
		FinishedAt:        now.Format(time.RFC3339),
		AutoRetryCount:    0,
		AutoRetryLimit:    2,
		AutoRetryInterval: 5 * time.Minute,
		ProcGroup:         "controller-child",
		SuspendFlagName:   "controller-child-file",
	}
	attempt := &controllerGatewayAttempt{status: status}
	store := &controllerGatewayRunStore{attempt: attempt}
	scheduler := &Scheduler{
		dagRunStore: store,
		retryScanner: &RetryScanner{
			isSuspended: func(_ context.Context, name string) bool {
				assert.Equal(t, "controller-child-file", name)
				return true
			},
			retryWindow: time.Hour,
			clock:       func() time.Time { return now },
		},
	}
	gateway := &controllerChildRunGateway{scheduler: scheduler}
	request := controller.ChildRunRequest{DAG: status.Name, DAGRunID: status.DAGRunID}

	observation, err := gateway.Observe(context.Background(), request)
	require.NoError(t, err)
	assert.True(t, observation.Exists)
	assert.Equal(t, core.Failed, observation.Status)
	assert.Equal(t, "failed", observation.ErrorCategory)

	require.NoError(t, gateway.Stop(context.Background(), request))
	assert.Equal(t, core.Failed, status.Status)
	assert.Zero(t, attempt.dagReads)
}

func TestControllerChildRunGatewayHonorsSuspensionForLegacyPendingAutoRetry(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name           string
		suspended      bool
		observedStatus core.Status
		stoppedStatus  core.Status
	}{
		{name: "not suspended", observedStatus: core.Running, stoppedStatus: core.Aborted},
		{name: "suspended", suspended: true, observedStatus: core.Failed, stoppedStatus: core.Failed},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
			status := &exec.DAGRunStatus{
				Name:           "controller-child",
				DAGRunID:       "controller-run-1",
				AttemptID:      "attempt-1",
				Status:         core.Failed,
				CreatedAt:      now.Add(-time.Minute).UnixMilli(),
				FinishedAt:     now.Format(time.RFC3339),
				AutoRetryCount: 0,
			}
			attempt := &controllerGatewayAttempt{
				status: status,
				dag: &core.DAG{
					Name:     status.Name,
					Location: "/dags/controller-child-file.yaml",
					RetryPolicy: &core.DAGRetryPolicy{
						Limit:    2,
						Interval: 5 * time.Minute,
					},
				},
			}
			store := &controllerGatewayRunStore{attempt: attempt}
			scheduler := &Scheduler{
				dagRunStore: store,
				retryScanner: &RetryScanner{
					isSuspended: func(_ context.Context, name string) bool {
						assert.Equal(t, "controller-child-file", name)
						return test.suspended
					},
					retryWindow: time.Hour,
					clock:       func() time.Time { return now },
				},
			}
			gateway := &controllerChildRunGateway{scheduler: scheduler}
			request := controller.ChildRunRequest{DAG: status.Name, DAGRunID: status.DAGRunID}

			observation, err := gateway.Observe(context.Background(), request)
			require.NoError(t, err)
			assert.True(t, observation.Exists)
			assert.Equal(t, test.observedStatus, observation.Status)

			require.NoError(t, gateway.Stop(context.Background(), request))
			assert.Equal(t, test.stoppedStatus, status.Status)
			assert.Equal(t, 2, attempt.dagReads)
		})
	}
}

func TestControllerChildRunGatewayPreservesAttemptIdentityWhenOutputsFail(t *testing.T) {
	t.Parallel()

	status := &exec.DAGRunStatus{
		Name:      "controller-child",
		DAGRunID:  "controller-run-1",
		AttemptID: "attempt-1",
		Status:    core.Succeeded,
	}
	store := &controllerGatewayRunStore{attempt: &controllerGatewayAttempt{
		status:     status,
		outputsErr: assert.AnError,
	}}
	gateway := &controllerChildRunGateway{scheduler: &Scheduler{
		config:       &config.Config{},
		dagRunStore:  store,
		retryScanner: &RetryScanner{},
	}}

	observation, err := gateway.Observe(context.Background(), controller.ChildRunRequest{
		DAG: status.Name, DAGRunID: status.DAGRunID,
	})

	require.Error(t, err)
	assert.True(t, observation.Exists)
	assert.Equal(t, core.Succeeded, observation.Status)
}

func TestControllerChildRunGatewayReadsOutputsFromObservedAttempt(t *testing.T) {
	t.Parallel()

	oldAttempt := &controllerGatewayAttempt{
		status: &exec.DAGRunStatus{
			Name: "controller-child", DAGRunID: "controller-run-1",
			AttemptID: "attempt-1", Status: core.Succeeded,
		},
		outputs: &exec.DAGRunOutputs{Outputs: map[string]string{"source": "old"}},
	}
	observedAttempt := &controllerGatewayAttempt{
		status: &exec.DAGRunStatus{
			Name: "controller-child", DAGRunID: "controller-run-1",
			AttemptID: "attempt-2", Status: core.Succeeded,
		},
		outputs: &exec.DAGRunOutputs{Outputs: map[string]string{"source": "observed"}},
	}
	store := &controllerGatewayRotatingRunStore{attempts: []*controllerGatewayAttempt{
		oldAttempt,
		observedAttempt,
	}}
	manager := runtime.NewManager(store, nil, &config.Config{})
	gateway := &controllerChildRunGateway{
		scheduler: &Scheduler{dagRunStore: store, retryScanner: &RetryScanner{}},
		manager:   &manager,
	}

	observation, err := gateway.Observe(context.Background(), controller.ChildRunRequest{
		DAG: "controller-child", DAGRunID: "controller-run-1",
	})

	require.NoError(t, err)
	assert.Equal(t, core.Succeeded, observation.Status)
	assert.Equal(t, map[string]string{"source": "observed"}, observation.Outputs)
}

func TestControllerChildRunGatewayWaitsWhenTerminalAttemptRotates(t *testing.T) {
	t.Parallel()

	terminalAttempt := &controllerGatewayAttempt{
		status: &exec.DAGRunStatus{
			Name: "controller-child", DAGRunID: "controller-run-1",
			AttemptID: "attempt-1", Status: core.Succeeded,
		},
		outputs: &exec.DAGRunOutputs{Outputs: map[string]string{"source": "terminal"}},
	}
	newAttempt := &controllerGatewayAttempt{status: &exec.DAGRunStatus{
		Name: "controller-child", DAGRunID: "controller-run-1",
		AttemptID: "attempt-2", Status: core.Queued,
	}}
	store := &controllerGatewayRotatingRunStore{attempts: []*controllerGatewayAttempt{
		terminalAttempt,
		terminalAttempt,
		newAttempt,
	}}
	manager := runtime.NewManager(store, nil, &config.Config{})
	gateway := &controllerChildRunGateway{
		scheduler: &Scheduler{dagRunStore: store, retryScanner: &RetryScanner{}},
		manager:   &manager,
	}

	observation, err := gateway.Observe(context.Background(), controller.ChildRunRequest{
		DAG: "controller-child", DAGRunID: "controller-run-1",
	})

	require.NoError(t, err)
	assert.True(t, observation.Exists)
	assert.Equal(t, core.Running, observation.Status)
	assert.Empty(t, observation.Outputs)
}
