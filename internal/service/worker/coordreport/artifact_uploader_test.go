// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordreport_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/worker/coordreport"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

type artifactUploaderMockClient struct {
	coordinator.Client
	streamArtifactsFunc   func(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error)
	streamArtifactsToFunc func(context.Context, exec.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error)
}

func (m *artifactUploaderMockClient) StreamArtifacts(ctx context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	if m.streamArtifactsFunc != nil {
		return m.streamArtifactsFunc(ctx)
	}
	return nil, errors.New("StreamArtifacts not configured")
}

func (m *artifactUploaderMockClient) StreamArtifactsTo(ctx context.Context, owner exec.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
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

	uploader := coordreport.NewArtifactUploader(client, "worker-1", "run-123", "test-dag", "attempt-1", exec.DAGRunRef{})
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

	uploader = coordreport.NewArtifactUploader(client, "worker-1", "run-123", "test-dag", "attempt-1", exec.DAGRunRef{})
	err := uploader.UploadDir(context.Background(), dir)
	require.NoError(t, err)

	chunks := stream.chunksForPath("artifact.txt")
	require.Len(t, chunks, 2)
	for _, chunk := range chunks {
		assert.Equal(t, "attempt-1", chunk.AttemptId)
	}
}
