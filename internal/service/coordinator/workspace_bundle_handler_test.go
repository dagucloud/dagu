// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWorkspaceBundleUploadAndDownloadRoundTrip(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte("workspace"), workspaceBundleChunkSize/len("workspace")+1)
	digest := workspaceBundleDigest(data)
	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: t.TempDir()})
	upload := &workspaceBundleUploadStream{
		ctx: context.Background(),
		chunks: []*coordinatorv1.WorkspaceBundleChunk{
			{Sequence: 0, Bundle: &coordinatorv1.WorkspaceBundle{Digest: digest, Size: int64(len(data))}, Data: data[:workspaceBundleChunkSize]},
			{Sequence: 1, Data: data[workspaceBundleChunkSize:], IsFinal: true},
		},
	}
	require.NoError(t, handler.PutWorkspaceBundle(upload))
	require.True(t, upload.response.Accepted)

	download := &workspaceBundleDownloadStream{ctx: context.Background()}
	require.NoError(t, handler.GetWorkspaceBundle(&coordinatorv1.GetWorkspaceBundleRequest{Digest: digest}, download))
	require.Len(t, download.chunks, 2)
	assert.Equal(t, byte('w'), download.chunks[0].Data[0])
	assert.Equal(t, data, append(download.chunks[0].Data, download.chunks[1].Data...))
}

func TestPutWorkspaceBundleRejectsDescriptorAfterFirstChunk(t *testing.T) {
	t.Parallel()

	digest := workspaceBundleDigest(nil)
	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: t.TempDir()})
	upload := &workspaceBundleUploadStream{
		ctx: context.Background(),
		chunks: []*coordinatorv1.WorkspaceBundleChunk{
			{Sequence: 0, Bundle: &coordinatorv1.WorkspaceBundle{Digest: digest}},
			{Sequence: 1, Bundle: &coordinatorv1.WorkspaceBundle{Digest: digest}},
		},
	}

	err := handler.PutWorkspaceBundle(upload)
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetWorkspaceBundleReportsCorruptStoredBundle(t *testing.T) {
	t.Parallel()

	data := []byte("workspace")
	digest := workspaceBundleDigest(data)
	dir := t.TempDir()
	store := workspacebundle.NewStore(dir, workspacebundle.DefaultLimits())
	require.NoError(t, store.Put(t.Context(), workspacebundle.Descriptor{
		Digest: digest,
		Size:   int64(len(data)),
	}, data))
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*.tar.gz"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	require.NoError(t, os.WriteFile(paths[0], []byte("corrupt"), 0o600))

	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: dir})
	err = handler.GetWorkspaceBundle(
		&coordinatorv1.GetWorkspaceBundleRequest{Digest: digest},
		&workspaceBundleDownloadStream{ctx: context.Background()},
	)
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestGetWorkspaceBundleReportsMissingBundle(t *testing.T) {
	t.Parallel()

	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: t.TempDir()})
	err := handler.GetWorkspaceBundle(
		&coordinatorv1.GetWorkspaceBundleRequest{Digest: workspaceBundleDigest([]byte("missing"))},
		&workspaceBundleDownloadStream{ctx: context.Background()},
	)
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestDispatchRetainsWorkspaceBundle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	data := []byte("workspace")
	desc := workspacebundle.Descriptor{Digest: workspaceBundleDigest(data), Size: int64(len(data))}
	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: t.TempDir()})
	received := make(chan *coordinatorv1.Task, 1)
	handler.waitingPollers["poller-1"] = &workerInfo{workerID: "worker-1", taskChan: received}
	require.NoError(t, handler.workspaceBundleStore.Put(ctx, desc, data))

	_, err := handler.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: workspaceDispatchTask(desc)})
	require.NoError(t, err)
	task := <-received
	references, err := handler.workspaceBundleStore.ListReferences(ctx)
	require.NoError(t, err)
	require.Len(t, references, 1)
	assert.Equal(t, task.AttemptKey, references[0].AttemptKey)
	assert.Equal(t, desc.Digest, references[0].Digest)
}

func TestDispatchReportsMissingWorkspaceBundle(t *testing.T) {
	t.Parallel()

	data := []byte("missing")
	desc := workspacebundle.Descriptor{Digest: workspaceBundleDigest(data), Size: int64(len(data))}
	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: t.TempDir()})
	handler.waitingPollers["poller-1"] = &workerInfo{workerID: "worker-1", taskChan: make(chan *coordinatorv1.Task, 1)}

	_, err := handler.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: workspaceDispatchTask(desc)})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestDispatchRetryKeepsAdmissionForMissingWorkspace(t *testing.T) {
	t.Parallel()
	registerCommandExecutorCapsForCoordinatorTest()

	ctx := context.Background()
	data := []byte("workspace")
	desc := workspacebundle.Descriptor{Digest: workspaceBundleDigest(data), Size: int64(len(data))}
	baseDir := filepath.Join(t.TempDir(), "distributed")
	dispatchStore, leaseStore, activeStore := newTestDispatchAdmissionTaskStore(baseDir)
	heartbeatStore := newTestWorkerHeartbeatStore(baseDir)
	require.NoError(t, heartbeatStore.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID: "worker-1", LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))
	handler := NewHandler(HandlerConfig{
		DAGRunRepository:          newMockDAGRunStore().repository,
		DispatchTaskStore:         dispatchStore,
		WorkerHeartbeatStore:      heartbeatStore,
		DAGRunLeaseStore:          leaseStore,
		ActiveDistributedRunStore: activeStore,
		WorkspaceBundleDir:        t.TempDir(),
	})
	runRef := ir.NewDAGRunRef("test", "run-1")
	attemptID := "test-attempt"
	decision, err := dispatchStore.ReserveAdmission(ctx, dispatch.DispatchAdmissionRequest{
		QueueName: "test-queue", MaxConcurrency: 1,
		AttemptKey: ir.GenerateAttemptKey(runRef.Name, runRef.ID, runRef.Name, runRef.ID, attemptID),
		AttemptID:  attemptID, DAGRun: runRef, StaleThreshold: time.Minute,
	})
	require.NoError(t, err)
	require.True(t, decision.Reserved)
	task := workspaceDispatchTask(desc)
	task.Operation = coordinatorv1.Operation_OPERATION_RETRY
	task.QueueName = "test-queue"
	req := &coordinatorv1.DispatchRequest{AdmissionReservationToken: decision.ReservationToken, Task: task}

	_, err = handler.Dispatch(ctx, req)
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
	require.NoError(t, handler.workspaceBundleStore.Put(ctx, desc, data))
	_, err = handler.Dispatch(ctx, req)
	require.NoError(t, err)

	claimed, err := dispatchStore.ClaimNext(ctx, dispatch.DispatchTaskClaim{WorkerID: "worker-1"})
	require.NoError(t, err)
	require.NotNil(t, claimed)
}

func TestDispatchPinsWorkspaceBundleOwner(t *testing.T) {
	t.Parallel()
	registerCommandExecutorCapsForCoordinatorTest()

	ctx := context.Background()
	data := []byte("workspace")
	desc := workspacebundle.Descriptor{Digest: workspaceBundleDigest(data), Size: int64(len(data))}
	baseDir := filepath.Join(t.TempDir(), "distributed")
	dispatchStore := newTestDispatchTaskStore(baseDir)
	heartbeatStore := newTestWorkerHeartbeatStore(baseDir)
	require.NoError(t, heartbeatStore.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID: "worker-1", LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))
	owner := dispatch.CoordinatorEndpoint{ID: "coord-upload", Host: "upload.example.test", Port: 50055}
	handler := NewHandler(HandlerConfig{
		DAGRunRepository:     newMockDAGRunStore().repository,
		DispatchTaskStore:    dispatchStore,
		WorkerHeartbeatStore: heartbeatStore,
		WorkspaceBundleDir:   t.TempDir(),
		Owner:                owner,
	})
	require.NoError(t, handler.workspaceBundleStore.Put(ctx, desc, data))

	_, err := handler.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: workspaceDispatchTask(desc)})
	require.NoError(t, err)
	claimed, err := dispatchStore.ClaimNext(ctx, dispatch.DispatchTaskClaim{
		WorkerID: "worker-1",
		PollerID: "poller-1",
		Owner:    dispatch.CoordinatorEndpoint{ID: "coord-poll", Host: "poll.example.test", Port: 50056},
	})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, owner, claimed.Task.Owner)
}

func TestDispatchRollsBackWorkspaceBundleAfterFailedHandoff(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	data := []byte("workspace")
	desc := workspacebundle.Descriptor{Digest: workspaceBundleDigest(data), Size: int64(len(data))}
	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: t.TempDir()})
	handler.waitingPollers["poller-1"] = &workerInfo{workerID: "worker-1", taskChan: make(chan *coordinatorv1.Task)}
	require.NoError(t, handler.workspaceBundleStore.Put(ctx, desc, data))

	_, err := handler.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: workspaceDispatchTask(desc)})
	require.Error(t, err)
	references, listErr := handler.workspaceBundleStore.ListReferences(ctx)
	require.NoError(t, listErr)
	assert.Empty(t, references)
	assert.False(t, handler.workspaceBundleStore.Has(desc.Digest))
}

func TestTerminalStatusReleasesWorkspaceBundle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	data := []byte("workspace")
	desc := workspacebundle.Descriptor{Digest: workspaceBundleDigest(data), Size: int64(len(data))}
	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: t.TempDir()})
	require.NoError(t, handler.workspaceBundleStore.Put(ctx, desc, data))
	_, err := handler.workspaceBundleStore.Retain(ctx, "attempt-1", desc.Digest)
	require.NoError(t, err)

	handler.finalizeAttemptForStatus(ctx, &ir.DAGRunStatus{
		AttemptKey: "attempt-1",
		Status:     ir.Succeeded,
	}, "")

	assert.False(t, handler.workspaceBundleStore.Has(desc.Digest))
}

func TestWorkspaceBundleReconciliationReleasesOrphan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	data := []byte("workspace")
	desc := workspacebundle.Descriptor{Digest: workspaceBundleDigest(data), Size: int64(len(data))}
	handler := NewHandler(HandlerConfig{
		WorkspaceBundleDir:  t.TempDir(),
		DispatchTaskStore:   &failingDispatchTaskStore{},
		StaleLeaseThreshold: -time.Second,
	})
	require.NoError(t, handler.workspaceBundleStore.Put(ctx, desc, data))
	_, err := handler.workspaceBundleStore.Retain(ctx, "attempt-1", desc.Digest)
	require.NoError(t, err)

	handler.reconcileWorkspaceBundles(ctx, time.Now().UTC())

	assert.False(t, handler.workspaceBundleStore.Has(desc.Digest))
}

func workspaceDispatchTask(desc workspacebundle.Descriptor) *coordinatorv1.Task {
	return &coordinatorv1.Task{
		DagRunId:              "run-1",
		Target:                "test",
		Definition:            "name: test\nsteps: []\n",
		WorkspaceBundleDigest: desc.Digest,
		WorkspaceBundleSize:   desc.Size,
	}
}

func workspaceBundleDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type workspaceBundleUploadStream struct {
	grpc.ServerStream
	ctx      context.Context
	chunks   []*coordinatorv1.WorkspaceBundleChunk
	index    int
	response *coordinatorv1.PutWorkspaceBundleResponse
}

func (s *workspaceBundleUploadStream) Context() context.Context { return s.ctx }

func (s *workspaceBundleUploadStream) Recv() (*coordinatorv1.WorkspaceBundleChunk, error) {
	if s.index == len(s.chunks) {
		return nil, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

func (s *workspaceBundleUploadStream) SendAndClose(response *coordinatorv1.PutWorkspaceBundleResponse) error {
	s.response = response
	return nil
}

type workspaceBundleDownloadStream struct {
	grpc.ServerStream
	ctx    context.Context
	chunks []*coordinatorv1.WorkspaceBundleChunk
}

func (s *workspaceBundleDownloadStream) Context() context.Context { return s.ctx }

func (s *workspaceBundleDownloadStream) Send(chunk *coordinatorv1.WorkspaceBundleChunk) error {
	s.chunks = append(s.chunks, chunk)
	return nil
}
