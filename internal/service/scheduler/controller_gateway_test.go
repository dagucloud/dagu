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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type controllerGatewayAttempt struct {
	exec.DAGRunAttempt
	status     *exec.DAGRunStatus
	outputs    *exec.DAGRunOutputs
	outputsErr error
}

func (a *controllerGatewayAttempt) ReadStatus(context.Context) (*exec.DAGRunStatus, error) {
	return a.status, nil
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
		ControllerID: "ctrl_aaaaaaaaaaaaaaaa",
		DAG:          "controller-child",
		DAGRunID:     "controller-run-1",
		Params:       []byte(`{}`),
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
		config: &config.Config{Scheduler: config.Scheduler{RetryFailureWindow: time.Hour}},
		clock:  func() time.Time { return now },
	}
	gateway := &controllerChildRunGateway{scheduler: scheduler, dagRunStore: store}
	request := controller.ChildRunRequest{DAG: status.Name, DAGRunID: status.DAGRunID}

	observation, err := gateway.Observe(context.Background(), request)
	require.NoError(t, err)
	assert.True(t, observation.Exists)
	assert.Equal(t, core.Running, observation.Status)

	require.NoError(t, gateway.Stop(context.Background(), request))
	assert.Equal(t, core.Aborted, status.Status)

	status.Status = core.Failed
	status.AutoRetryCount = status.AutoRetryLimit
	assert.False(t, gateway.autoRetryPending(status))
	status.AutoRetryCount = 0
	status.CreatedAt = now.Add(-2 * time.Hour).UnixMilli()
	assert.False(t, gateway.autoRetryPending(status))
	status.CreatedAt = now.Add(-time.Minute).UnixMilli()
	scheduler.config.Scheduler.RetryFailureWindow = 0
	assert.False(t, gateway.autoRetryPending(status))
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
	gateway := &controllerChildRunGateway{scheduler: &Scheduler{config: &config.Config{}}, dagRunStore: store}

	observation, err := gateway.Observe(context.Background(), controller.ChildRunRequest{
		DAG: status.Name, DAGRunID: status.DAGRunID,
	})

	require.Error(t, err)
	assert.True(t, observation.Exists)
	assert.Equal(t, core.Succeeded, observation.Status)
}
