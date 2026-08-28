// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
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

func TestResolveSecretReference(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	enc, err := crypto.NewEncryptor("test-key-for-secrets")
	require.NoError(t, err)
	secretStore, err := store.NewSecretStore(testutil.NewMemoryBackend().Collection("secrets"), enc)
	require.NoError(t, err)

	now := time.Now().UTC()
	sec, err := secretpkg.New(secretpkg.CreateInput{
		Workspace:    "payments",
		Ref:          "prod/my-secret",
		ProviderType: secretpkg.ProviderDaguManaged,
		CreatedBy:    "test",
	}, now)
	require.NoError(t, err)
	require.NoError(t, secretStore.Create(ctx, sec, &secretpkg.WriteValueInput{
		Value:     "secret-value",
		CreatedBy: "test",
		CreatedAt: now,
	}))

	dagRunRepository := runtestutil.NewFileDAGRunRepository(filepath.Join(t.TempDir(), "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	dagRepository := runtestutil.NewFileDAGRepository(filepath.Join(t.TempDir(), "dags"))
	require.NoError(t, dagRepository.Create(ctx, "external-child", []byte(`
name: external-child
labels:
  - workspace=payments
secrets:
  - name: MY_SECRET
    ref: prod/my-secret
steps:
  - name: child
    action: dag.run
    with:
      dag: nested-local-child

---
name: nested-local-child
labels:
  - workspace=payments
secrets:
  - name: MY_SECRET
    ref: prod/my-secret
steps:
  - name: noop
    run: "true"
`)))
	require.NoError(t, dagRepository.Create(ctx, "cycle-a", []byte(`
name: cycle-a
steps:
  - name: child
    action: dag.run
    with:
      dag: cycle-b
`)))
	require.NoError(t, dagRepository.Create(ctx, "cycle-b", []byte(`
name: cycle-b
steps:
  - name: child
    action: dag.run
    with:
      dag: cycle-a
`)))
	require.NoError(t, dagRepository.Create(ctx, "unrelated-child", []byte(`
name: unrelated-child
labels:
  - workspace=payments
secrets:
  - name: MY_SECRET
    ref: prod/my-secret
steps:
  - name: noop
    run: "true"
`)))
	leaseStore := store.NewDAGRunLeaseStore(testutil.NewMemoryBackend().Collection("leases"))
	dag := &ir.DAG{
		Name:   "registry-secret-dag",
		Labels: ir.NewLabels([]string{"workspace=payments"}),
		Secrets: []secretref.Ref{{
			Name: "MY_SECRET",
			Ref:  "prod/my-secret",
		}},
		LocalDAGs: map[string]*ir.DAG{
			"inline": {
				Name: "inline",
				Steps: []ir.Step{
					{
						Name: "external",
						SubDAG: &ir.SubDAG{
							Name: "external-child",
						},
					},
					{
						Name: "cycle",
						SubDAG: &ir.SubDAG{
							Name: "cycle-a",
						},
					},
				},
			},
		},
	}
	attempt, err := dagRunRepository.CreateAttempt(ctx, dag, now, "run-1", persis.DAGRunCreateAttemptOptions{AttemptID: "attempt-1"})
	require.NoError(t, err)
	attemptKey := ir.GenerateAttemptKey(dag.Name, "run-1", dag.Name, "run-1", attempt.ID())
	require.NoError(t, attempt.Open(ctx))
	t.Cleanup(func() {
		require.NoError(t, attempt.Close(context.Background()))
	})
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
		Name:       dag.Name,
		DAGRunID:   "run-1",
		AttemptID:  attempt.ID(),
		AttemptKey: attemptKey,
		Status:     ir.Running,
		Labels:     dag.Labels.Strings(),
	}))
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
		DAGRepository:       dagRepository,
		DAGRunRepository:    dagRunRepository,
		DAGRunLeaseStore:    leaseStore,
		StaleLeaseThreshold: time.Minute,
	})

	resp, err := handler.ResolveSecretReference(ctx, &coordinatorv1.ResolveSecretReferenceRequest{
		Name:       "MY_SECRET",
		Ref:        "prod/my-secret",
		Workspace:  "payments",
		WorkerId:   "worker-1",
		AttemptKey: attemptKey,
		AttemptId:  attempt.ID(),
	})
	require.NoError(t, err)
	assert.Equal(t, "secret-value", resp.GetValue())

	checkResp, err := handler.ResolveSecretReference(ctx, &coordinatorv1.ResolveSecretReferenceRequest{
		Name:       "MY_SECRET",
		Ref:        "prod/my-secret",
		Workspace:  "payments",
		CheckOnly:  true,
		WorkerId:   "worker-1",
		AttemptKey: attemptKey,
		AttemptId:  attempt.ID(),
	})
	require.NoError(t, err)
	assert.Empty(t, checkResp.GetValue())

	nestedResp, err := handler.ResolveSecretReference(ctx, &coordinatorv1.ResolveSecretReferenceRequest{
		Name:       "MY_SECRET",
		Ref:        "prod/my-secret",
		Workspace:  "payments",
		WorkerId:   "worker-1",
		AttemptKey: attemptKey,
		AttemptId:  attempt.ID(),
		DagName:    "external-child",
	})
	require.NoError(t, err)
	assert.Equal(t, "secret-value", nestedResp.GetValue())

	localNestedResp, err := handler.ResolveSecretReference(ctx, &coordinatorv1.ResolveSecretReferenceRequest{
		Name:       "MY_SECRET",
		Ref:        "prod/my-secret",
		Workspace:  "payments",
		WorkerId:   "worker-1",
		AttemptKey: attemptKey,
		AttemptId:  attempt.ID(),
		DagName:    "nested-local-child",
	})
	require.NoError(t, err)
	assert.Equal(t, "secret-value", localNestedResp.GetValue())

	_, err = handler.ResolveSecretReference(ctx, &coordinatorv1.ResolveSecretReferenceRequest{
		Name:       "MY_SECRET",
		Ref:        "prod/my-secret",
		Workspace:  "payments",
		WorkerId:   "worker-1",
		AttemptKey: attemptKey,
		AttemptId:  attempt.ID(),
		DagName:    "unrelated-child",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	_, err = handler.ResolveSecretReference(ctx, &coordinatorv1.ResolveSecretReferenceRequest{
		Name:       "MY_SECRET",
		Ref:        "prod/my-secret",
		Workspace:  "other",
		WorkerId:   "worker-1",
		AttemptKey: attemptKey,
		AttemptId:  attempt.ID(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	_, err = handler.ResolveSecretReference(ctx, &coordinatorv1.ResolveSecretReferenceRequest{
		Name:       "OTHER_SECRET",
		Ref:        "prod/other-secret",
		Workspace:  "payments",
		WorkerId:   "worker-1",
		AttemptKey: attemptKey,
		AttemptId:  attempt.ID(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}
