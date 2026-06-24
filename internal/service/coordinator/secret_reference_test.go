// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
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

	dagRunStore := dagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	leaseStore := store.NewDAGRunLeaseStore(testutil.NewMemoryBackend().Collection("leases"))
	dag := &core.DAG{
		Name:   "registry-secret-dag",
		Labels: core.NewLabels([]string{"workspace=payments"}),
		Secrets: []core.SecretRef{{
			Name: "MY_SECRET",
			Ref:  "prod/my-secret",
		}},
	}
	attempt, err := dagRunStore.CreateAttempt(ctx, dag, now, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-1"})
	require.NoError(t, err)
	attemptKey := exec.GenerateAttemptKey(dag.Name, "run-1", dag.Name, "run-1", attempt.ID())
	require.NoError(t, attempt.Open(ctx))
	t.Cleanup(func() {
		require.NoError(t, attempt.Close(context.Background()))
	})
	require.NoError(t, attempt.Write(ctx, exec.DAGRunStatus{
		Name:       dag.Name,
		DAGRunID:   "run-1",
		AttemptID:  attempt.ID(),
		AttemptKey: attemptKey,
		Status:     core.Running,
		Labels:     dag.Labels.Strings(),
	}))
	require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
		AttemptKey:      attemptKey,
		DAGRun:          exec.DAGRunRef{Name: dag.Name, ID: "run-1"},
		Root:            exec.DAGRunRef{Name: dag.Name, ID: "run-1"},
		AttemptID:       attempt.ID(),
		WorkerID:        "worker-1",
		ClaimedAt:       now.UnixMilli(),
		LastHeartbeatAt: now.UnixMilli(),
	}))

	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		SecretStore:         secretStore,
		DAGRunStore:         dagRunStore,
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
