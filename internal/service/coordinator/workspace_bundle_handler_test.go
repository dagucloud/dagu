// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
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
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*"))
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

func TestHasWorkspaceBundleRefreshesExpiration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now().UTC()
	dir := t.TempDir()
	data := []byte("workspace")
	digest := workspaceBundleDigest(data)
	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: dir})
	require.NoError(t, handler.workspaceBundleStore.Put(ctx, workspacebundle.Descriptor{
		Digest: digest,
		Size:   int64(len(data)),
	}, data))
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	require.NoError(t, os.Chtimes(paths[0], now.Add(-2*time.Hour), now.Add(-2*time.Hour)))

	resp, err := handler.HasWorkspaceBundle(ctx, &coordinatorv1.HasWorkspaceBundleRequest{Digest: digest})
	require.NoError(t, err)
	assert.True(t, resp.Exists)
	removed, err := handler.workspaceBundleStore.Cleanup(ctx, now.Add(-time.Hour), nil)
	require.NoError(t, err)
	assert.Zero(t, removed)
	assert.True(t, handler.workspaceBundleStore.Has(digest))
}

func TestWorkspaceBundleCleanupPreservesOutstandingTask(t *testing.T) {
	ctx := t.Context()
	now := time.Now().UTC()
	backend := testutil.NewMemoryBackend()
	dispatchStore := store.NewDispatchTaskStore(backend.Collection("dispatch_tasks"))
	leaseStore := store.NewDAGRunLeaseStore(backend.Collection("leases"))
	bundleDir := t.TempDir()
	handler := NewHandler(HandlerConfig{
		WorkspaceBundleDir: bundleDir,
		DispatchTaskStore:  dispatchStore,
		DAGRunLeaseStore:   leaseStore,
	})
	putExpired := func(name string) string {
		t.Helper()

		data := []byte(name)
		digest := workspaceBundleDigest(data)
		require.NoError(t, handler.workspaceBundleStore.Put(ctx, workspacebundle.Descriptor{
			Digest: digest,
			Size:   int64(len(data)),
		}, data))
		path := filepath.Join(bundleDir, digest[:2], digest+".tar.gz")
		require.NoError(t, os.Chtimes(path, now.Add(-2*time.Hour), now.Add(-2*time.Hour)))
		return digest
	}

	pendingDigest := putExpired("pending workspace")
	claimedDigest := putExpired("claimed workspace")
	leaseDigest := putExpired("running workspace")
	unreferencedDigest := putExpired("unreferenced workspace")
	require.NoError(t, dispatchStore.Enqueue(ctx, &dispatch.DispatchTask{
		DAGRunID:              "run-pending",
		Target:                "pending",
		WorkerSelector:        map[string]string{"state": "pending"},
		WorkspaceBundleDigest: pendingDigest,
	}))
	require.NoError(t, dispatchStore.Enqueue(ctx, &dispatch.DispatchTask{
		DAGRunID:              "run-claimed",
		Target:                "claimed",
		WorkerSelector:        map[string]string{"state": "claimed"},
		WorkspaceBundleDigest: claimedDigest,
	}))
	claimed, err := dispatchStore.ClaimNext(ctx, dispatch.DispatchTaskClaim{
		WorkerID: "worker-1",
		Labels:   map[string]string{"state": "claimed"},
	})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
		AttemptKey:            "running-attempt",
		WorkspaceBundleDigest: leaseDigest,
	}))

	handler.cleanupWorkspaceBundles(ctx, now)

	assert.True(t, handler.workspaceBundleStore.Has(pendingDigest))
	assert.True(t, handler.workspaceBundleStore.Has(claimedDigest))
	assert.True(t, handler.workspaceBundleStore.Has(leaseDigest))
	assert.False(t, handler.workspaceBundleStore.Has(unreferencedDigest))

	require.NoError(t, leaseStore.Delete(ctx, "running-attempt"))
	handler.cleanupWorkspaceBundles(ctx, now)
	assert.False(t, handler.workspaceBundleStore.Has(leaseDigest))
}

func TestWorkspaceBundleCleanupFailsClosed(t *testing.T) {
	ctx := t.Context()
	now := time.Now().UTC()
	bundleDir := t.TempDir()
	baseStore := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))
	handler := NewHandler(HandlerConfig{
		WorkspaceBundleDir: bundleDir,
		DispatchTaskStore: &bundleListErrorStore{
			DispatchTaskStore: baseStore,
		},
	})
	data := []byte("preserved workspace")
	digest := workspaceBundleDigest(data)
	require.NoError(t, handler.workspaceBundleStore.Put(ctx, workspacebundle.Descriptor{
		Digest: digest,
		Size:   int64(len(data)),
	}, data))
	path := filepath.Join(bundleDir, digest[:2], digest+".tar.gz")
	require.NoError(t, os.Chtimes(path, now.Add(-2*time.Hour), now.Add(-2*time.Hour)))

	handler.cleanupWorkspaceBundles(ctx, now)

	assert.True(t, handler.workspaceBundleStore.Has(digest))
}

func TestDispatchRejectsMissingWorkspaceBundle(t *testing.T) {
	dir := t.TempDir()
	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: dir})
	digest := workspaceBundleDigest([]byte("missing"))

	_, err := handler.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Target:                "task",
		DagRunId:              "run-1",
		Definition:            "name: task\nsteps: []\n",
		WorkspaceBundleDigest: digest,
	}})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))

	path := filepath.Join(dir, digest[:2], digest+".tar.gz")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("corrupt"), 0o600))
	_, err = handler.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Target:                "task",
		DagRunId:              "run-2",
		Definition:            "name: task\nsteps: []\n",
		WorkspaceBundleDigest: digest,
	}})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestDispatchPreservesBundleContextStatus(t *testing.T) {
	data := []byte("workspace")
	digest := workspaceBundleDigest(data)
	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: t.TempDir()})
	require.NoError(t, handler.workspaceBundleStore.Put(t.Context(), workspacebundle.Descriptor{
		Digest: digest,
		Size:   int64(len(data)),
	}, data))

	tests := []struct {
		name string
		ctx  func() context.Context
		code codes.Code
	}{
		{
			name: "canceled",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			code: codes.Canceled,
		},
		{
			name: "deadline exceeded",
			ctx: func() context.Context {
				ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				cancel()
				return ctx
			},
			code: codes.DeadlineExceeded,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handler.Dispatch(tc.ctx(), &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
				Target:                "task",
				DagRunId:              "run-" + tc.name,
				Definition:            "name: task\nsteps: []\n",
				WorkspaceBundleDigest: digest,
			}})

			require.Error(t, err)
			assert.Equal(t, tc.code, status.Code(err))
		})
	}
}

type bundleListErrorStore struct {
	dispatch.DispatchTaskStore
}

func (*bundleListErrorStore) ListBundleDigests(context.Context) ([]string, error) {
	return nil, errors.New("list failed")
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
