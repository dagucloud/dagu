// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

type mockStreamArtifactsServer struct {
	chunks   []*coordinatorv1.ArtifactChunk
	idx      int
	response *coordinatorv1.StreamArtifactsResponse
	ctx      context.Context
	recvErr  error
	recvHook func(int)
}

func (m *mockStreamArtifactsServer) Recv() (*coordinatorv1.ArtifactChunk, error) {
	if m.idx >= len(m.chunks) {
		if m.recvErr != nil {
			return nil, m.recvErr
		}
		return nil, io.EOF
	}
	if m.recvHook != nil {
		m.recvHook(m.idx)
	}
	chunk := m.chunks[m.idx]
	m.idx++
	return chunk, nil
}

func (m *mockStreamArtifactsServer) SendAndClose(resp *coordinatorv1.StreamArtifactsResponse) error {
	m.response = resp
	return nil
}

func (m *mockStreamArtifactsServer) SetHeader(_ metadata.MD) error  { return nil }
func (m *mockStreamArtifactsServer) SendHeader(_ metadata.MD) error { return nil }
func (m *mockStreamArtifactsServer) SetTrailer(_ metadata.MD)       {}
func (m *mockStreamArtifactsServer) Context() context.Context       { return m.ctx }
func (m *mockStreamArtifactsServer) SendMsg(_ any) error            { return nil }
func (m *mockStreamArtifactsServer) RecvMsg(_ any) error            { return nil }

func TestArtifactHandlerHandleStreamCreatesEmptyFileOnFinalChunk(t *testing.T) {
	t.Parallel()

	store := newMockDAGRunStore()
	archiveDir := t.TempDir()
	store.addAttempt(ir.DAGRunRef{Name: "test-dag", ID: "run-123"}, &ir.DAGRunStatus{
		Name:       "test-dag",
		DAGRunID:   "run-123",
		AttemptID:  "attempt-1",
		ArchiveDir: archiveDir,
	})

	handler := newArtifactHandler(store, "")
	stream := &mockStreamArtifactsServer{
		ctx: context.Background(),
		chunks: []*coordinatorv1.ArtifactChunk{
			{
				DagName:      "test-dag",
				DagRunId:     "run-123",
				AttemptId:    "attempt-1",
				RelativePath: "empty.txt",
				IsFinal:      true,
			},
		},
	}

	err := handler.handleStream(stream)
	require.NoError(t, err)
	require.NotNil(t, stream.response)
	assert.Equal(t, uint64(1), stream.response.ChunksReceived)

	info, err := os.Stat(filepath.Join(archiveDir, "empty.txt"))
	require.NoError(t, err)
	assert.Zero(t, info.Size())
}

func TestArtifactHandlerHandleStreamWritesFinalChunkPayload(t *testing.T) {
	t.Parallel()

	store := newMockDAGRunStore()
	archiveDir := t.TempDir()
	store.addAttempt(ir.DAGRunRef{Name: "test-dag", ID: "run-123"}, &ir.DAGRunStatus{
		Name:       "test-dag",
		DAGRunID:   "run-123",
		AttemptID:  "attempt-1",
		ArchiveDir: archiveDir,
	})

	handler := newArtifactHandler(store, "")
	stream := &mockStreamArtifactsServer{
		ctx: context.Background(),
		chunks: []*coordinatorv1.ArtifactChunk{
			{
				DagName:      "test-dag",
				DagRunId:     "run-123",
				AttemptId:    "attempt-1",
				RelativePath: "artifact.txt",
				Data:         []byte("hello"),
				IsFinal:      true,
			},
		},
	}

	err := handler.handleStream(stream)
	require.NoError(t, err)
	require.NotNil(t, stream.response)
	assert.Equal(t, uint64(5), stream.response.BytesWritten)

	content, readErr := os.ReadFile(filepath.Join(archiveDir, "artifact.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, []byte("hello"), content)
}

func TestHandlerStreamArtifactsAcceptsPreviousOwnerAtDifferentCoordinatorEndpoint(t *testing.T) {
	t.Parallel()

	store := newMockDAGRunStore()
	archiveDir := t.TempDir()
	store.addAttempt(ir.DAGRunRef{Name: "test-dag", ID: "run-123"}, &ir.DAGRunStatus{
		Name:       "test-dag",
		DAGRunID:   "run-123",
		AttemptID:  "attempt-1",
		ArchiveDir: archiveDir,
	})
	leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
	attemptKey := ir.GenerateAttemptKey("test-dag", "run-123", "test-dag", "run-123", "attempt-1")
	require.NoError(t, leaseStore.Upsert(t.Context(), dispatch.DAGRunLease{
		AttemptKey:      attemptKey,
		DAGRun:          ir.NewDAGRunRef("test-dag", "run-123"),
		Root:            ir.NewDAGRunRef("test-dag", "run-123"),
		AttemptID:       "attempt-1",
		WorkerID:        "worker-1",
		Owner:           dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "coordinator", Port: 50055},
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))
	handler := NewHandler(HandlerConfig{
		DAGRunStore:      store,
		ArtifactDir:      archiveDir,
		DAGRunLeaseStore: leaseStore,
		Owner:            dispatch.CoordinatorEndpoint{ID: "coord-b", Host: "coordinator-b", Port: 50056},
	})
	stream := &mockStreamArtifactsServer{
		ctx: t.Context(),
		chunks: []*coordinatorv1.ArtifactChunk{{
			WorkerId:           "worker-1",
			DagName:            "test-dag",
			DagRunId:           "run-123",
			AttemptId:          "attempt-1",
			RelativePath:       "artifact.txt",
			Data:               []byte("continued"),
			IsFinal:            true,
			OwnerCoordinatorId: "coord-a",
			AttemptKey:         attemptKey,
		}},
	}

	require.NoError(t, handler.StreamArtifacts(stream))
	content, err := os.ReadFile(filepath.Join(archiveDir, "artifact.txt"))
	require.NoError(t, err)
	assert.Equal(t, "continued", string(content))
}

func TestArtifactHandlerHandleStreamRejectsMismatchedAttempt(t *testing.T) {
	t.Parallel()

	store := newMockDAGRunStore()
	archiveDir := t.TempDir()
	store.addAttempt(ir.DAGRunRef{Name: "test-dag", ID: "run-123"}, &ir.DAGRunStatus{
		Name:       "test-dag",
		DAGRunID:   "run-123",
		AttemptID:  "attempt-2",
		ArchiveDir: archiveDir,
	})

	handler := newArtifactHandler(store, "")
	stream := &mockStreamArtifactsServer{
		ctx: context.Background(),
		chunks: []*coordinatorv1.ArtifactChunk{
			{
				DagName:      "test-dag",
				DagRunId:     "run-123",
				AttemptId:    "attempt-1",
				RelativePath: "artifact.txt",
				Data:         []byte("hello"),
			},
		},
	}

	err := handler.handleStream(stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match latest attempt")

	_, statErr := os.Stat(filepath.Join(archiveDir, "artifact.txt"))
	require.Error(t, statErr)
	assert.True(t, os.IsNotExist(statErr))
}

func TestArtifactHandlerHandleStreamDiscardsPartialFileOnRecvError(t *testing.T) {
	t.Parallel()

	store := newMockDAGRunStore()
	archiveDir := t.TempDir()
	store.addAttempt(ir.DAGRunRef{Name: "test-dag", ID: "run-123"}, &ir.DAGRunStatus{
		Name:       "test-dag",
		DAGRunID:   "run-123",
		AttemptID:  "attempt-1",
		ArchiveDir: archiveDir,
	})

	handler := newArtifactHandler(store, "")
	stream := &mockStreamArtifactsServer{
		ctx:     context.Background(),
		recvErr: io.ErrUnexpectedEOF,
		chunks: []*coordinatorv1.ArtifactChunk{
			{
				DagName:      "test-dag",
				DagRunId:     "run-123",
				AttemptId:    "attempt-1",
				RelativePath: "artifact.txt",
				Data:         []byte("hello"),
			},
		},
	}

	err := handler.handleStream(stream)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	_, err = os.Stat(filepath.Join(archiveDir, "artifact.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	entries, err := os.ReadDir(archiveDir)
	require.NoError(t, err)
	assert.Empty(t, entries)

	retryStream := &mockStreamArtifactsServer{
		ctx: context.Background(),
		chunks: []*coordinatorv1.ArtifactChunk{{
			DagName:      "test-dag",
			DagRunId:     "run-123",
			AttemptId:    "attempt-1",
			RelativePath: "artifact.txt",
			Data:         []byte("complete"),
			IsFinal:      true,
		}},
	}
	require.NoError(t, handler.handleStream(retryStream))
	content, err := os.ReadFile(filepath.Join(archiveDir, "artifact.txt"))
	require.NoError(t, err)
	assert.Equal(t, "complete", string(content))
}

func TestArtifactHandlerHandleStreamRevalidatesAttemptBeforeFinalizing(t *testing.T) {
	t.Parallel()

	store := newMockDAGRunStore()
	archiveDir := t.TempDir()
	attempt := store.addAttempt(ir.DAGRunRef{Name: "test-dag", ID: "run-123"}, &ir.DAGRunStatus{
		Name:       "test-dag",
		DAGRunID:   "run-123",
		AttemptID:  "attempt-1",
		ArchiveDir: archiveDir,
	})

	handler := newArtifactHandler(store, "")
	stream := &mockStreamArtifactsServer{
		ctx: context.Background(),
		chunks: []*coordinatorv1.ArtifactChunk{
			{
				DagName:      "test-dag",
				DagRunId:     "run-123",
				AttemptId:    "attempt-1",
				RelativePath: "artifact.txt",
				Data:         []byte("stale"),
			},
			{
				DagName:      "test-dag",
				DagRunId:     "run-123",
				AttemptId:    "attempt-1",
				RelativePath: "artifact.txt",
				IsFinal:      true,
			},
		},
		recvHook: func(index int) {
			if index != 1 {
				return
			}
			attempt.mu.Lock()
			attempt.status.AttemptID = "attempt-2"
			attempt.mu.Unlock()
		},
	}

	err := handler.handleStream(stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match latest attempt")
	_, statErr := os.Stat(filepath.Join(archiveDir, "artifact.txt"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestArtifactHandlerStreamKeyNormalizesRelativePath(t *testing.T) {
	t.Parallel()

	handler := newArtifactHandler(nil, "")
	normalized := &coordinatorv1.ArtifactChunk{
		DagName:      "test-dag",
		DagRunId:     "run-123",
		AttemptId:    "attempt-1",
		RelativePath: "reports/summary.txt",
	}
	duplicate := &coordinatorv1.ArtifactChunk{
		DagName:      "test-dag",
		DagRunId:     "run-123",
		AttemptId:    "attempt-1",
		RelativePath: "./reports\\summary.txt",
	}

	assert.Equal(t, handler.streamKey(normalized), handler.streamKey(duplicate))
}
