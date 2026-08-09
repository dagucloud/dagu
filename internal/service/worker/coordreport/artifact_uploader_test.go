// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordreport_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/worker/coordreport"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type artifactUploaderMockClient struct {
	coordinator.Client
	streamArtifactsFunc   func(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error)
	streamArtifactsToFunc func(context.Context, serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error)
}

func (m *artifactUploaderMockClient) StreamArtifacts(ctx context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	if m.streamArtifactsFunc != nil {
		return m.streamArtifactsFunc(ctx)
	}
	return nil, errors.New("StreamArtifacts not configured")
}

func (m *artifactUploaderMockClient) StreamArtifactsTo(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	if m.streamArtifactsToFunc != nil {
		return m.streamArtifactsToFunc(ctx, owner)
	}
	return m.StreamArtifacts(ctx)
}

type mockStreamArtifactsClient struct {
	mu         sync.Mutex
	sentChunks []*coordinatorv1.ArtifactChunk
	response   *coordinatorv1.StreamArtifactsResponse
	sendHook   func(*coordinatorv1.ArtifactChunk)
	sendFunc   func(int, *coordinatorv1.ArtifactChunk) error
	closeErr   error
}

func (m *mockStreamArtifactsClient) Send(chunk *coordinatorv1.ArtifactChunk) error {
	chunkCopy := &coordinatorv1.ArtifactChunk{
		WorkerId:           chunk.WorkerId,
		DagRunId:           chunk.DagRunId,
		DagName:            chunk.DagName,
		RelativePath:       chunk.RelativePath,
		Data:               append([]byte(nil), chunk.Data...),
		Sequence:           chunk.Sequence,
		IsFinal:            chunk.IsFinal,
		RootDagRunName:     chunk.RootDagRunName,
		RootDagRunId:       chunk.RootDagRunId,
		AttemptId:          chunk.AttemptId,
		OwnerCoordinatorId: chunk.OwnerCoordinatorId,
	}

	m.mu.Lock()
	if m.sendFunc != nil {
		if err := m.sendFunc(len(m.sentChunks), chunkCopy); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	m.sentChunks = append(m.sentChunks, chunkCopy)
	sendHook := m.sendHook
	m.mu.Unlock()

	if sendHook != nil {
		sendHook(chunkCopy)
	}
	return nil
}

func (m *mockStreamArtifactsClient) CloseAndRecv() (*coordinatorv1.StreamArtifactsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closeErr != nil {
		return nil, m.closeErr
	}
	if m.response == nil {
		m.response = &coordinatorv1.StreamArtifactsResponse{}
	}
	return m.response, nil
}

func (m *mockStreamArtifactsClient) Header() (metadata.MD, error) { return nil, nil }
func (m *mockStreamArtifactsClient) Trailer() metadata.MD         { return nil }
func (m *mockStreamArtifactsClient) CloseSend() error             { return nil }
func (m *mockStreamArtifactsClient) Context() context.Context     { return context.Background() }
func (m *mockStreamArtifactsClient) SendMsg(_ any) error          { return nil }
func (m *mockStreamArtifactsClient) RecvMsg(_ any) error          { return nil }

func (m *mockStreamArtifactsClient) chunksForPath(path string) []*coordinatorv1.ArtifactChunk {
	m.mu.Lock()
	defer m.mu.Unlock()

	var chunks []*coordinatorv1.ArtifactChunk
	for _, chunk := range m.sentChunks {
		if chunk.RelativePath == path {
			chunks = append(chunks, chunk)
		}
	}
	return chunks
}

func TestArtifactUploaderUploadDirIncludesEmptyFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "empty.txt"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "non-empty.txt"), []byte("hello"), 0o600))

	stream := &mockStreamArtifactsClient{}
	client := &artifactUploaderMockClient{
		streamArtifactsFunc: func(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
			return stream, nil
		},
	}

	uploader := coordreport.NewArtifactUploader(client, "worker-1", "run-123", "test-dag", "attempt-1", ir.DAGRunRef{})
	err := uploader.UploadDir(context.Background(), dir)
	require.NoError(t, err)

	emptyChunks := stream.chunksForPath("empty.txt")
	require.Len(t, emptyChunks, 1)
	assert.True(t, emptyChunks[0].IsFinal)
	assert.Empty(t, emptyChunks[0].Data)

	nonEmptyChunks := stream.chunksForPath("non-empty.txt")
	require.Len(t, nonEmptyChunks, 2)
	assert.Equal(t, []byte("hello"), nonEmptyChunks[0].Data)
	assert.True(t, nonEmptyChunks[1].IsFinal)
}

func TestArtifactUploaderUploadDirUsesSingleAttemptIDSnapshot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "artifact.txt"), []byte("hello"), 0o600))

	var uploader *coordreport.ArtifactUploader
	var once sync.Once

	stream := &mockStreamArtifactsClient{
		sendHook: func(chunk *coordinatorv1.ArtifactChunk) {
			if chunk.RelativePath != "artifact.txt" || len(chunk.Data) == 0 {
				return
			}
			once.Do(func() {
				uploader.SetAttemptID("attempt-2")
			})
		},
	}
	client := &artifactUploaderMockClient{
		streamArtifactsFunc: func(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
			return stream, nil
		},
	}

	uploader = coordreport.NewArtifactUploader(client, "worker-1", "run-123", "test-dag", "attempt-1", ir.DAGRunRef{})
	err := uploader.UploadDir(context.Background(), dir)
	require.NoError(t, err)

	chunks := stream.chunksForPath("artifact.txt")
	require.Len(t, chunks, 2)
	for _, chunk := range chunks {
		assert.Equal(t, "attempt-1", chunk.AttemptId)
	}
}

func TestArtifactUploaderUploadDirReplaysAfterStreamFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "artifact.txt"), []byte("complete"), 0o600))

	failedStream := &mockStreamArtifactsClient{
		sendFunc: func(index int, _ *coordinatorv1.ArtifactChunk) error {
			if index == 1 {
				return io.EOF
			}
			return nil
		},
		closeErr: status.Error(codes.Unavailable, "coordinator replaced"),
	}
	recoveredStream := &mockStreamArtifactsClient{}
	streamCount := 0
	client := &artifactUploaderMockClient{
		streamArtifactsFunc: func(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
			streamCount++
			if streamCount == 1 {
				return failedStream, nil
			}
			return recoveredStream, nil
		},
	}

	uploader := coordreport.NewArtifactUploader(client, "worker-1", "run-123", "test-dag", "attempt-1", ir.DAGRunRef{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, uploader.UploadDir(ctx, dir))
	assert.Equal(t, 2, streamCount)
	assert.Len(t, failedStream.chunksForPath("artifact.txt"), 1)
	recoveredChunks := recoveredStream.chunksForPath("artifact.txt")
	require.Len(t, recoveredChunks, 2)
	assert.Equal(t, []byte("complete"), recoveredChunks[0].Data)
	assert.True(t, recoveredChunks[1].IsFinal)
}

func TestArtifactUploaderUploadDirDoesNotReplayRejectedStream(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "artifact.txt"), []byte("stale"), 0o600))

	stream := &mockStreamArtifactsClient{
		sendFunc: func(index int, _ *coordinatorv1.ArtifactChunk) error {
			if index == 1 {
				return io.EOF
			}
			return nil
		},
		closeErr: status.Error(codes.FailedPrecondition, "artifact chunk attempt is stale"),
	}
	streamCount := 0
	client := &artifactUploaderMockClient{
		streamArtifactsFunc: func(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
			streamCount++
			return stream, nil
		},
	}

	uploader := coordreport.NewArtifactUploader(client, "worker-1", "run-123", "test-dag", "attempt-1", ir.DAGRunRef{})
	err := uploader.UploadDir(context.Background(), dir)
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Equal(t, 1, streamCount)
}
