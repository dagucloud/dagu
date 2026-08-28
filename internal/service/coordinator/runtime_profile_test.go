// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	profilepkg "github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	runtestutil "github.com/dagucloud/dagu/v2/internal/testutil"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestProfileResolverFallback(t *testing.T) {
	t.Parallel()

	want := &profilepkg.RuntimeResolved{Selected: &profilepkg.Resolved{Name: "prod"}}
	tests := []struct {
		name         string
		remoteErr    error
		remoteResult *profilepkg.RuntimeResolved
		wantResult   *profilepkg.RuntimeResolved
		wantCode     codes.Code
		wantFallback bool
	}{
		{name: "remote", remoteResult: want, wantResult: want},
		{
			name: "legacy coordinator", remoteErr: fmt.Errorf("rpc failed: %w", status.Error(codes.Unimplemented, "missing")),
			wantResult: want, wantFallback: true,
		},
		{
			name: "coordinator unavailable", remoteErr: status.Error(codes.Unavailable, "offline"),
			wantCode: codes.Unavailable,
		},
		{
			name: "access denied", remoteErr: status.Error(codes.PermissionDenied, "denied"),
			wantCode: codes.PermissionDenied,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fallbackCalled := false
			resolver := coordinator.NewRuntimeProfileResolver(
				runtimeProfileClientFunc(func(context.Context, serviceregistry.HostInfo, profilepkg.RuntimeRequest, coordinator.RuntimeProfileRun) (*profilepkg.RuntimeResolved, error) {
					return tt.remoteResult, tt.remoteErr
				}),
				serviceregistry.HostInfo{},
				coordinator.RuntimeProfileRun{},
				runtimeProfileResolverFunc(func(context.Context, profilepkg.RuntimeRequest) (*profilepkg.RuntimeResolved, error) {
					fallbackCalled = true
					return want, nil
				}),
			)

			got, err := resolver.ResolveRuntime(context.Background(), profilepkg.RuntimeRequest{ProfileName: "prod"})
			if tt.wantCode != codes.OK {
				require.Error(t, err)
				assert.Equal(t, tt.wantCode, status.Code(err))
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantResult, got)
			}
			assert.Equal(t, tt.wantFallback, fallbackCalled)
		})
	}
}

func TestProfileRPCAuthorization(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now().UTC()
	backend := testutil.NewMemoryBackend()
	profileStore, err := store.NewProfileStore(backend.Collection("profiles"))
	require.NoError(t, err)
	enc, err := crypto.NewEncryptor("runtime-profile-test-key")
	require.NoError(t, err)
	secretStore, err := store.NewSecretStore(backend.Collection("secrets"), enc)
	require.NoError(t, err)

	prof, err := profilepkg.New(profilepkg.CreateInput{Name: "prod"}, now)
	require.NoError(t, err)
	require.NoError(t, prof.SetVariable("PROFILE_VALUE", "resolved", "test", now))
	require.NoError(t, profileStore.Create(ctx, prof))
	manager := profilepkg.NewManager(profileStore, secretStore)
	_, err = manager.SetSecret(ctx, prof, "ROTATED_SECRET", "before", "test")
	require.NoError(t, err)

	dag := &ir.DAG{
		Name:   "profile-dag",
		Labels: ir.NewLabels([]string{"workspace=payments"}),
		Steps:  []ir.Step{{SubDAG: &ir.SubDAG{Name: "external-profile"}}},
	}
	dagRepository := runtestutil.NewFileDAGRepository(filepath.Join(t.TempDir(), "dags"))
	require.NoError(t, dagRepository.Create(ctx, "external-profile", []byte(`
name: external-profile
steps:
  - name: child
    action: dag.run
    with:
      dag: nested-local-profile

---
name: nested-local-profile
labels:
  - workspace=payments
steps:
  - name: noop
    run: "true"
`)))
	repository := runtestutil.NewFileDAGRunRepository(filepath.Join(t.TempDir(), "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	attempt, err := repository.CreateAttempt(ctx, dag, now, "run-1", persis.DAGRunCreateAttemptOptions{AttemptID: "attempt-1"})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	t.Cleanup(func() { require.NoError(t, attempt.Close(context.Background())) })
	attemptKey := ir.GenerateAttemptKey(dag.Name, "run-1", dag.Name, "run-1", attempt.ID())
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
		Name: dag.Name, DAGRunID: "run-1", AttemptID: attempt.ID(), AttemptKey: attemptKey,
		Status: ir.Running, ProfileName: "prod", Labels: dag.Labels.Strings(),
	}))

	leaseStore := store.NewDAGRunLeaseStore(backend.Collection("leases"))
	require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
		AttemptKey: attemptKey, DAGRun: ir.NewDAGRunRef(dag.Name, "run-1"),
		Root: ir.NewDAGRunRef(dag.Name, "run-1"), AttemptID: attempt.ID(),
		WorkerID: "worker-1", ClaimedAt: now.UnixMilli(), LastHeartbeatAt: now.UnixMilli(),
	}))

	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		DAGRepository: dagRepository, DAGRunRepository: repository, DAGRunLeaseStore: leaseStore,
		ProfileStore: profileStore, SecretStore: secretStore, StaleLeaseThreshold: time.Minute,
	})
	valid := &coordinatorv1.ResolveRuntimeProfileRequest{
		WorkerId: "worker-1", AttemptKey: attemptKey, AttemptId: attempt.ID(),
		ProfileName: "prod", Workspace: "payments",
	}
	missingRepositoryHandler := coordinator.NewHandler(coordinator.HandlerConfig{
		DAGRunLeaseStore: leaseStore, ProfileStore: profileStore, SecretStore: secretStore,
		StaleLeaseThreshold: time.Minute,
	})
	_, err = missingRepositoryHandler.ResolveRuntimeProfile(ctx, valid)
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	resp, err := handler.ResolveRuntimeProfile(ctx, valid)
	require.NoError(t, err)
	require.Len(t, resp.GetSelected().GetEntries(), 2)
	assert.Equal(t, "before", runtimeProfileEntryValue(resp.GetSelected(), "ROTATED_SECRET"))
	lease, err := leaseStore.Get(ctx, attemptKey)
	require.NoError(t, err)
	assert.Equal(t, "prod", lease.ProfileName)

	_, err = manager.SetSecret(ctx, prof, "ROTATED_SECRET", "after", "test")
	require.NoError(t, err)
	rotated, err := handler.ResolveRuntimeProfile(ctx, valid)
	require.NoError(t, err)
	assert.Equal(t, "after", runtimeProfileEntryValue(rotated.GetSelected(), "ROTATED_SECRET"))

	nestedAttemptKey := attemptKey + "-nested"
	require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
		AttemptKey: nestedAttemptKey, DAGRun: ir.NewDAGRunRef(dag.Name, "run-1"),
		Root: ir.NewDAGRunRef(dag.Name, "run-1"), AttemptID: attempt.ID(),
		WorkerID: "worker-1", ClaimedAt: now.UnixMilli(), LastHeartbeatAt: now.UnixMilli(),
	}))
	_, err = handler.ResolveRuntimeProfile(ctx, &coordinatorv1.ResolveRuntimeProfileRequest{
		WorkerId: "worker-1", AttemptKey: nestedAttemptKey, AttemptId: attempt.ID(),
		ProfileName: "prod", Workspace: "payments", DagName: "nested-local-profile",
	})
	require.NoError(t, err)
	nestedLease, err := leaseStore.Get(ctx, nestedAttemptKey)
	require.NoError(t, err)
	assert.Equal(t, "prod", nestedLease.ProfileName)

	tests := []struct {
		name   string
		mutate func(*coordinatorv1.ResolveRuntimeProfileRequest)
	}{
		{name: "worker", mutate: func(req *coordinatorv1.ResolveRuntimeProfileRequest) { req.WorkerId = "other" }},
		{name: "attempt", mutate: func(req *coordinatorv1.ResolveRuntimeProfileRequest) { req.AttemptId = "other" }},
		{name: "profile", mutate: func(req *coordinatorv1.ResolveRuntimeProfileRequest) { req.ProfileName = "other" }},
		{name: "workspace", mutate: func(req *coordinatorv1.ResolveRuntimeProfileRequest) { req.Workspace = "other" }},
		{name: "lease", mutate: func(req *coordinatorv1.ResolveRuntimeProfileRequest) { req.AttemptKey = "other" }},
		{name: "dag", mutate: func(req *coordinatorv1.ResolveRuntimeProfileRequest) { req.DagName = "unreachable-profile" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := proto.Clone(valid).(*coordinatorv1.ResolveRuntimeProfileRequest)
			tt.mutate(req)
			_, err := handler.ResolveRuntimeProfile(ctx, req)
			require.Error(t, err)
			assert.Equal(t, codes.PermissionDenied, status.Code(err))
		})
	}

	unprofiledAttempt, err := repository.CreateAttempt(ctx, dag, now, "run-2", persis.DAGRunCreateAttemptOptions{AttemptID: "attempt-2"})
	require.NoError(t, err)
	require.NoError(t, unprofiledAttempt.Open(ctx))
	t.Cleanup(func() { require.NoError(t, unprofiledAttempt.Close(context.Background())) })
	unprofiledKey := ir.GenerateAttemptKey(dag.Name, "run-2", dag.Name, "run-2", unprofiledAttempt.ID())
	require.NoError(t, unprofiledAttempt.Write(ctx, ir.DAGRunStatus{
		Name: dag.Name, DAGRunID: "run-2", AttemptID: unprofiledAttempt.ID(), AttemptKey: unprofiledKey,
		Status: ir.Running, Labels: dag.Labels.Strings(),
	}))
	require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
		AttemptKey: unprofiledKey, DAGRun: ir.NewDAGRunRef(dag.Name, "run-2"),
		Root: ir.NewDAGRunRef(dag.Name, "run-2"), AttemptID: unprofiledAttempt.ID(),
		WorkerID: "worker-1", ClaimedAt: now.UnixMilli(), LastHeartbeatAt: now.UnixMilli(),
	}))
	_, err = handler.ResolveRuntimeProfile(ctx, &coordinatorv1.ResolveRuntimeProfileRequest{
		WorkerId: "worker-1", AttemptKey: unprofiledKey, AttemptId: unprofiledAttempt.ID(),
		ProfileName: "prod", Workspace: "payments",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	staleLeaseStore := store.NewDAGRunLeaseStore(backend.Collection("stale-leases"))
	require.NoError(t, staleLeaseStore.Upsert(ctx, dispatch.DAGRunLease{
		AttemptKey: attemptKey, DAGRun: ir.NewDAGRunRef(dag.Name, "run-1"),
		Root: ir.NewDAGRunRef(dag.Name, "run-1"), AttemptID: attempt.ID(),
		ProfileName: "prod", WorkerID: "worker-1", ClaimedAt: now.Add(-time.Hour).UnixMilli(),
		LastHeartbeatAt: now.Add(-time.Hour).UnixMilli(),
	}))
	staleHandler := coordinator.NewHandler(coordinator.HandlerConfig{
		DAGRunRepository: repository, DAGRunLeaseStore: staleLeaseStore,
		ProfileStore: profileStore, SecretStore: secretStore, StaleLeaseThreshold: time.Minute,
	})
	_, err = staleHandler.ResolveRuntimeProfile(ctx, valid)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

type runtimeProfileClientFunc func(context.Context, serviceregistry.HostInfo, profilepkg.RuntimeRequest, coordinator.RuntimeProfileRun) (*profilepkg.RuntimeResolved, error)

func (f runtimeProfileClientFunc) ResolveRuntimeProfile(ctx context.Context, owner serviceregistry.HostInfo, req profilepkg.RuntimeRequest, run coordinator.RuntimeProfileRun) (*profilepkg.RuntimeResolved, error) {
	return f(ctx, owner, req, run)
}

type runtimeProfileResolverFunc func(context.Context, profilepkg.RuntimeRequest) (*profilepkg.RuntimeResolved, error)

func (f runtimeProfileResolverFunc) ResolveRuntime(ctx context.Context, req profilepkg.RuntimeRequest) (*profilepkg.RuntimeResolved, error) {
	return f(ctx, req)
}

var _ coordinator.RuntimeProfileClient = runtimeProfileClientFunc(nil)
var _ profilepkg.RuntimeResolver = runtimeProfileResolverFunc(nil)

func runtimeProfileEntryValue(layer *coordinatorv1.RuntimeProfileLayer, key string) string {
	for _, entry := range layer.GetEntries() {
		if entry.GetKey() == key {
			return entry.GetValue()
		}
	}
	return ""
}
