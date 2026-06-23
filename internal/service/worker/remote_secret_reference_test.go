// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/proto/convert"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	workersvc "github.com/dagucloud/dagu/internal/service/worker"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestRemoteTaskHandlerResolvesRegistrySecretsViaCoordinator(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	enc, err := crypto.NewEncryptor("test-key-for-secrets")
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
		Value:     "coordinator-secret-value",
		CreatedBy: "test",
		CreatedAt: now,
	}))

	client := &secretResolvingRemoteCoordinatorClient{
		resolveSecret: func(ctx context.Context, ref core.SecretRef, workspace string, checkOnly bool) (string, error) {
			resolver := secretpkg.NewReferenceResolver(coordinatorSecrets, workspace)
			if checkOnly {
				return "", resolver.CheckReferenceAccessibility(ctx, ref)
			}
			return resolver.ResolveReference(ctx, ref)
		},
	}
	workerID := "secret-reference-worker"
	dagRunID := "secret-reference-run"

	handler := workersvc.NewRemoteTaskHandler(workersvc.RemoteTaskHandlerConfig{
		WorkerID:          workerID,
		CoordinatorClient: client,
		Config: &config.Config{
			Paths: config.PathsConfig{DataDir: t.TempDir()},
		},
		AgentStoresFactory: func(ctx context.Context, cfg *config.Config) agent.RuntimeStores {
			return file.NewAgentStores(ctx, cfg)
		},
	})

	task := &coordinatorv1.Task{
		Operation:            coordinatorv1.Operation_OPERATION_START,
		Target:               "registry-secret-dag",
		RootDagRunName:       "registry-secret-dag",
		RootDagRunId:         dagRunID,
		DagRunId:             dagRunID,
		OwnerCoordinatorId:   "coord-1",
		OwnerCoordinatorHost: "127.0.0.1",
		OwnerCoordinatorPort: 4521,
		Definition: `
secrets:
  - name: MY_SECRET
    ref: prod/my-secret
steps:
  - name: main
    run: test "$MY_SECRET" = "coordinator-secret-value"
`,
	}

	err = handler.Handle(ctx, task)
	require.NoError(t, err)
	require.Equal(t, []core.SecretRef{{Name: "MY_SECRET", Ref: "prod/my-secret"}}, client.resolvedRefs())
	require.Equal(t, []exec.HostInfo{{ID: "coord-1", Host: "127.0.0.1", Port: 4521}}, client.resolvedOwners())

	reported := client.reportedStatuses()
	require.NotEmpty(t, reported)
	status, convErr := convert.ProtoToDAGRunStatus(reported[len(reported)-1].Status)
	require.NoError(t, convErr)
	assert.Equal(t, core.Succeeded, status.Status)
}

type secretResolvingRemoteCoordinatorClient struct {
	mu            sync.Mutex
	reported      []*coordinatorv1.ReportStatusRequest
	resolved      []core.SecretRef
	owners        []exec.HostInfo
	resolveSecret func(context.Context, core.SecretRef, string, bool) (string, error)
}

var _ coordinator.Client = (*secretResolvingRemoteCoordinatorClient)(nil)
var _ coordinator.SecretReferenceClient = (*secretResolvingRemoteCoordinatorClient)(nil)

func (c *secretResolvingRemoteCoordinatorClient) reportedStatuses() []*coordinatorv1.ReportStatusRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*coordinatorv1.ReportStatusRequest(nil), c.reported...)
}

func (c *secretResolvingRemoteCoordinatorClient) resolvedRefs() []core.SecretRef {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]core.SecretRef(nil), c.resolved...)
}

func (c *secretResolvingRemoteCoordinatorClient) resolvedOwners() []exec.HostInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]exec.HostInfo(nil), c.owners...)
}

func (c *secretResolvingRemoteCoordinatorClient) Dispatch(context.Context, exec.DispatchRequest) error {
	return nil
}

func (c *secretResolvingRemoteCoordinatorClient) Cleanup(context.Context) error {
	return nil
}

func (c *secretResolvingRemoteCoordinatorClient) GetDAGRunStatus(context.Context, string, string, *exec.DAGRunRef) (*exec.DAGRunStatusResult, error) {
	return &exec.DAGRunStatusResult{Found: false}, nil
}

func (c *secretResolvingRemoteCoordinatorClient) RequestCancel(context.Context, string, string, *exec.DAGRunRef) error {
	return nil
}

func (c *secretResolvingRemoteCoordinatorClient) Poll(context.Context, backoff.RetryPolicy, *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
	return nil, nil
}

func (c *secretResolvingRemoteCoordinatorClient) GetWorkers(context.Context) ([]*coordinatorv1.WorkerInfo, error) {
	return nil, nil
}

func (c *secretResolvingRemoteCoordinatorClient) Heartbeat(context.Context, *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
	return &coordinatorv1.HeartbeatResponse{}, nil
}

func (c *secretResolvingRemoteCoordinatorClient) AckTaskClaimTo(context.Context, exec.HostInfo, *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
	return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
}

func (c *secretResolvingRemoteCoordinatorClient) RunHeartbeatTo(context.Context, exec.HostInfo, *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
	return &coordinatorv1.RunHeartbeatResponse{}, nil
}

func (c *secretResolvingRemoteCoordinatorClient) ReportStatus(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	c.mu.Lock()
	c.reported = append(c.reported, req)
	c.mu.Unlock()
	return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
}

func (c *secretResolvingRemoteCoordinatorClient) ReportStatusTo(ctx context.Context, _ exec.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	return c.ReportStatus(ctx, req)
}

func (c *secretResolvingRemoteCoordinatorClient) StreamLogs(context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	return newSecretTestStreamLogsClient(), nil
}

func (c *secretResolvingRemoteCoordinatorClient) StreamLogsTo(ctx context.Context, _ exec.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	return c.StreamLogs(ctx)
}

func (c *secretResolvingRemoteCoordinatorClient) StreamArtifacts(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	return newSecretTestStreamArtifactsClient(), nil
}

func (c *secretResolvingRemoteCoordinatorClient) StreamArtifactsTo(ctx context.Context, _ exec.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	return c.StreamArtifacts(ctx)
}

func (c *secretResolvingRemoteCoordinatorClient) GetDAG(context.Context, string) (string, error) {
	return "", nil
}

func (c *secretResolvingRemoteCoordinatorClient) Metrics() coordinator.Metrics {
	return coordinator.Metrics{IsConnected: true}
}

func (c *secretResolvingRemoteCoordinatorClient) ResolveSecretReference(ctx context.Context, owner exec.HostInfo, ref core.SecretRef, workspace string, checkOnly bool) (string, error) {
	if owner == (exec.HostInfo{}) {
		return "", fmt.Errorf("secret resolution for %q did not target the owner coordinator", ref.Ref)
	}
	c.mu.Lock()
	c.resolved = append(c.resolved, ref)
	c.owners = append(c.owners, owner)
	c.mu.Unlock()
	return c.resolveSecret(ctx, ref, workspace, checkOnly)
}

type secretTestStreamLogsClient struct {
	mu     sync.Mutex
	chunks []*coordinatorv1.LogChunk
	ctx    context.Context
}

func newSecretTestStreamLogsClient() *secretTestStreamLogsClient {
	return &secretTestStreamLogsClient{ctx: context.Background()}
}

func (c *secretTestStreamLogsClient) Send(chunk *coordinatorv1.LogChunk) error {
	c.mu.Lock()
	c.chunks = append(c.chunks, chunk)
	c.mu.Unlock()
	return nil
}

func (c *secretTestStreamLogsClient) CloseAndRecv() (*coordinatorv1.StreamLogsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &coordinatorv1.StreamLogsResponse{ChunksReceived: uint64(len(c.chunks))}, nil
}

func (c *secretTestStreamLogsClient) Header() (metadata.MD, error) { return nil, nil }
func (c *secretTestStreamLogsClient) Trailer() metadata.MD         { return nil }
func (c *secretTestStreamLogsClient) CloseSend() error             { return nil }
func (c *secretTestStreamLogsClient) Context() context.Context     { return c.ctx }
func (c *secretTestStreamLogsClient) SendMsg(any) error            { return nil }
func (c *secretTestStreamLogsClient) RecvMsg(any) error            { return nil }

type secretTestStreamArtifactsClient struct {
	mu     sync.Mutex
	chunks []*coordinatorv1.ArtifactChunk
	ctx    context.Context
}

func newSecretTestStreamArtifactsClient() *secretTestStreamArtifactsClient {
	return &secretTestStreamArtifactsClient{ctx: context.Background()}
}

func (c *secretTestStreamArtifactsClient) Send(chunk *coordinatorv1.ArtifactChunk) error {
	c.mu.Lock()
	c.chunks = append(c.chunks, chunk)
	c.mu.Unlock()
	return nil
}

func (c *secretTestStreamArtifactsClient) CloseAndRecv() (*coordinatorv1.StreamArtifactsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &coordinatorv1.StreamArtifactsResponse{ChunksReceived: uint64(len(c.chunks))}, nil
}

func (c *secretTestStreamArtifactsClient) Header() (metadata.MD, error) { return nil, nil }
func (c *secretTestStreamArtifactsClient) Trailer() metadata.MD         { return nil }
func (c *secretTestStreamArtifactsClient) CloseSend() error             { return nil }
func (c *secretTestStreamArtifactsClient) Context() context.Context     { return c.ctx }
func (c *secretTestStreamArtifactsClient) SendMsg(any) error            { return nil }
func (c *secretTestStreamArtifactsClient) RecvMsg(any) error            { return nil }
