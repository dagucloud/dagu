// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	runtestutil "github.com/dagucloud/dagu/v2/internal/testutil"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const stepSecretValue = "step-secret-value"

type stepSecretFixture struct {
	handler    *coordinator.Handler
	attempt    dagrun.Attempt
	attemptKey string
	status     ir.DAGRunStatus
}

// newStepSecretFixture makes a running attempt of a DAG with step scope. The
// step push lists the secret and runs, the step other has no list, the step
// verify is untrusted, and the step done lists the secret and succeeded.
func newStepSecretFixture(t *testing.T) *stepSecretFixture {
	t.Helper()
	ctx := context.Background()
	enc, err := crypto.NewEncryptor("test-key-for-step-secrets")
	require.NoError(t, err)
	secretStore, err := store.NewSecretStore(testutil.NewMemoryBackend().Collection("secrets"), enc)
	require.NoError(t, err)
	now := time.Now().UTC()
	sec, err := secretpkg.New(secretpkg.CreateInput{
		Workspace:    "payments",
		Ref:          "prod/git-token",
		ProviderType: secretpkg.ProviderDaguManaged,
		CreatedBy:    "test",
	}, now)
	require.NoError(t, err)
	require.NoError(t, secretStore.Create(ctx, sec, &secretpkg.WriteValueInput{
		Value: stepSecretValue, CreatedBy: "test", CreatedAt: now,
	}))

	dagRunRepository := runtestutil.NewFileDAGRunRepository(filepath.Join(t.TempDir(), "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	leaseStore := store.NewDAGRunLeaseStore(testutil.NewMemoryBackend().Collection("leases"))
	dag := &ir.DAG{
		Name:            "step-scope-dag",
		Labels:          ir.NewLabels([]string{"workspace=payments"}),
		Secrets:         []secretref.Ref{{Name: "GIT_TOKEN", Ref: "prod/git-token"}},
		SecretStepScope: true,
		Steps: []ir.Step{
			{Name: "push", Secrets: []string{"GIT_TOKEN"}},
			{Name: "other"},
			{Name: "verify", Untrusted: true},
			{Name: "done", Secrets: []string{"GIT_TOKEN"}},
		},
		HandlerOn: ir.HandlerOn{Exit: &ir.Step{Name: "on_exit", Secrets: []string{"GIT_TOKEN"}}},
	}
	attempt, err := dagRunRepository.CreateAttempt(ctx, dag, now, "run-1", persis.DAGRunCreateAttemptOptions{AttemptID: "attempt-1"})
	require.NoError(t, err)
	attemptKey := ir.GenerateAttemptKey(dag.Name, "run-1", dag.Name, "run-1", attempt.ID())
	require.NoError(t, attempt.Open(ctx))
	t.Cleanup(func() { require.NoError(t, attempt.Close(context.Background())) })
	runStatus := ir.DAGRunStatus{
		Name:       dag.Name,
		DAGRunID:   "run-1",
		AttemptID:  attempt.ID(),
		AttemptKey: attemptKey,
		Status:     ir.Running,
		Labels:     dag.Labels.Strings(),
		Nodes: []*ir.Node{
			{Step: dag.Steps[0], Status: ir.NodeRunning},
			{Step: dag.Steps[1], Status: ir.NodeNotStarted},
			{Step: dag.Steps[2], Status: ir.NodeNotStarted},
			{Step: dag.Steps[3], Status: ir.NodeSucceeded},
		},
	}
	require.NoError(t, attempt.Write(ctx, runStatus))
	require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
		AttemptKey:      attemptKey,
		DAGRun:          ir.DAGRunRef{Name: dag.Name, ID: "run-1"},
		Root:            ir.DAGRunRef{Name: dag.Name, ID: "run-1"},
		AttemptID:       attempt.ID(),
		WorkerID:        "worker-1",
		ClaimedAt:       now.UnixMilli(),
		LastHeartbeatAt: now.UnixMilli(),
	}))
	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		SecretStore:         secretStore,
		DAGRunRepository:    dagRunRepository,
		DAGRunLeaseStore:    leaseStore,
		StaleLeaseThreshold: time.Minute,
	})
	return &stepSecretFixture{handler: handler, attempt: attempt, attemptKey: attemptKey, status: runStatus}
}

func (f *stepSecretFixture) request(step string, checkOnly bool) *coordinatorv1.ResolveSecretReferenceRequest {
	return &coordinatorv1.ResolveSecretReferenceRequest{
		Name:       "GIT_TOKEN",
		Ref:        "prod/git-token",
		Workspace:  "payments",
		CheckOnly:  checkOnly,
		WorkerId:   "worker-1",
		AttemptKey: f.attemptKey,
		AttemptId:  f.attempt.ID(),
		StepName:   step,
	}
}

// Spec 092, "Resolution": the coordinator gives the value of a registry secret
// only to a step of the DAG that lists it, while neither the attempt nor the
// step has a final status. Each refusal is PermissionDenied, names the step and
// the secret, and has no value.
func TestStepSecretAuthorization(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newStepSecretFixture(t)

	resp, err := f.handler.ResolveSecretReference(ctx, f.request("push", false))
	require.NoError(t, err)
	assert.Equal(t, stepSecretValue, resp.GetValue())

	resp, err = f.handler.ResolveSecretReference(ctx, f.request("on_exit", false))
	require.NoError(t, err, "a handler that lists the secret gets it")
	assert.Equal(t, stepSecretValue, resp.GetValue())

	check, err := f.handler.ResolveSecretReference(ctx, f.request("", true))
	require.NoError(t, err, "the check at the start of the run names no step")
	assert.Empty(t, check.GetValue())

	for _, tc := range []struct {
		name string
		step string
		want []string
	}{
		{"no step", "", []string{`secret "GIT_TOKEN"`, "only to a step that lists it"}},
		{"step without the list", "other", []string{`step "other"`, `secret "GIT_TOKEN"`, "does not list"}},
		{"untrusted step", "verify", []string{`step "verify"`, "does not list"}},
		{"step of no DAG", "missing", []string{`step "missing"`, "has no such step"}},
		{"finished step", "done", []string{`step "done"`, "final status succeeded"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.handler.ResolveSecretReference(ctx, f.request(tc.step, false))
			require.Error(t, err)
			assert.Equal(t, codes.PermissionDenied, status.Code(err))
			for _, want := range tc.want {
				assert.Contains(t, err.Error(), want)
			}
			assert.NotContains(t, err.Error(), stepSecretValue)
		})
	}
}

// A request after the final status of the attempt fails.
func TestStepSecretAfterTheAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newStepSecretFixture(t)

	finished := f.status
	finished.Status = ir.Succeeded
	require.NoError(t, f.attempt.Write(ctx, finished))

	_, err := f.handler.ResolveSecretReference(ctx, f.request("push", false))
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Contains(t, err.Error(), "the attempt has the final status succeeded")
	assert.NotContains(t, err.Error(), stepSecretValue)
}
