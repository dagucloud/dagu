// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	workersvc "github.com/dagucloud/dagu/v2/internal/service/worker"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 092, "Resolution": in a DAG with step scope, the worker checks a
// registry secret at the start of the run with no value, and asks for the value
// when the step that lists it starts. The request names the step. A step that
// does not list the secret does not get it.
func TestRemoteTaskHandlerFetchesRegistrySecretForTheStep(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the steps use a POSIX shell")
	}

	ctx := context.Background()
	enc, err := crypto.NewEncryptor("test-key-for-step-secrets")
	require.NoError(t, err)
	coordinatorSecrets, err := store.NewSecretStore(testutil.NewMemoryBackend().Collection("secrets"), enc)
	require.NoError(t, err)
	now := time.Now().UTC()
	sec, err := secretpkg.New(secretpkg.CreateInput{
		Ref:          "prod/my-secret",
		ProviderType: secretpkg.ProviderDaguManaged,
		CreatedBy:    "test",
	}, now)
	require.NoError(t, err)
	require.NoError(t, coordinatorSecrets.Create(ctx, sec, &secretpkg.WriteValueInput{
		Value: "coordinator-secret-value", CreatedBy: "test", CreatedAt: now,
	}))

	client := &secretResolvingRemoteCoordinatorClient{
		resolveSecret: func(ctx context.Context, ref secretref.Ref, workspace string, checkOnly bool) (string, error) {
			resolver := secretpkg.NewReferenceResolver(coordinatorSecrets, workspace)
			if checkOnly {
				return "", resolver.CheckReferenceAccessibility(ctx, ref)
			}
			return resolver.ResolveReference(ctx, ref)
		},
	}
	cfg := &config.Config{Paths: config.PathsConfig{DataDir: t.TempDir()}}
	backend := file.NewBackend(cfg.Paths)
	handler := workersvc.NewRemoteTaskHandler(workersvc.RemoteTaskHandlerConfig{
		WorkerID:          "step-secret-worker",
		CoordinatorClient: client,
		Config:            cfg,
		SecretStore:       file.NewSecretStore(ctx, cfg, backend.Collection(persis.CollectionSecrets)),
		ProfileStore:      file.NewProfileStore(ctx, cfg, backend.Collection(persis.CollectionProfiles)),
	})

	task := &coordinatorv1.Task{
		Operation:            coordinatorv1.Operation_OPERATION_START,
		Target:               "step-secret-dag",
		RootDagRunName:       "step-secret-dag",
		RootDagRunId:         "step-secret-run",
		DagRunId:             "step-secret-run",
		AttemptId:            "attempt-1",
		AttemptKey:           "attempt-key-1",
		OwnerCoordinatorId:   "coord-1",
		OwnerCoordinatorHost: "127.0.0.1",
		OwnerCoordinatorPort: 4521,
		Definition: `
secrets:
  - name: MY_SECRET
    ref: prod/my-secret
steps:
  - name: other
    run: "! env | grep -q '^MY_SECRET='"
  - name: main
    depends: [other]
    secrets: [MY_SECRET]
    run: test "$MY_SECRET" = "coordinator-secret-value"
`,
	}
	require.NoError(t, handler.Handle(ctx, task))

	base := coordinator.SecretReferenceRun{
		WorkerID: "step-secret-worker", AttemptKey: "attempt-key-1", AttemptID: "attempt-1", DAGName: "step-secret-dag",
	}
	withStep := base
	withStep.StepName = "main"
	assert.Equal(t, []coordinator.SecretReferenceRun{base, withStep}, client.resolvedRuns())
	assert.Equal(t, []bool{true, false}, client.resolvedChecks(), "a check with no value, then the value for the step")

	reported := client.reportedStatuses()
	require.NotEmpty(t, reported)
	status, err := convert.ProtoToDAGRunStatus(reported[len(reported)-1].Status)
	require.NoError(t, err)
	assert.Equal(t, ir.Succeeded, status.Status)
}
