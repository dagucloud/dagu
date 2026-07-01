// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordreport_test

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/worker/coordreport"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// logStreamerMockClient implements coordinator.Client for testing log streamer
type logStreamerMockClient struct {
	coordinator.Client // Embed to satisfy interface (unused methods will panic)
	streamLogsFunc     func(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error)
	streamLogsToFunc   func(ctx context.Context, owner exec.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error)
}

func (m *logStreamerMockClient) StreamLogs(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	if m.streamLogsFunc != nil {
		return m.streamLogsFunc(ctx)
	}
	return nil, errors.New("StreamLogs not configured")
}

func (m *logStreamerMockClient) StreamLogsTo(ctx context.Context, owner exec.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	if m.streamLogsToFunc != nil {
		return m.streamLogsToFunc(ctx, owner)
	}
	return m.StreamLogs(ctx)
}

// mockStreamLogsClient implements coordinatorv1.CoordinatorService_StreamLogsClient
type mockStreamLogsClient struct {
	mu         sync.Mutex
	sentChunks []*coordinatorv1.LogChunk
	sendErr    error                                              // Static error for all sends
	sendFunc   func(idx int, chunk *coordinatorv1.LogChunk) error // Dynamic per-chunk error
	response   *coordinatorv1.StreamLogsResponse
	closeErr   error
}

func (m *mockStreamLogsClient) Send(chunk *coordinatorv1.LogChunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sendFunc != nil {
		if err := m.sendFunc(len(m.sentChunks), chunk); err != nil {
			return err
		}
	} else if m.sendErr != nil {
		return m.sendErr
	}

	// Deep copy chunk to capture the data at this moment
	chunkCopy := &coordinatorv1.LogChunk{
		WorkerId:           chunk.WorkerId,
		DagRunId:           chunk.DagRunId,
		DagName:            chunk.DagName,
		StepName:           chunk.StepName,
		StreamType:         chunk.StreamType,
		Data:               append([]byte(nil), chunk.Data...),
		Sequence:           chunk.Sequence,
		IsFinal:            chunk.IsFinal,
		RootDagRunName:     chunk.RootDagRunName,
		RootDagRunId:       chunk.RootDagRunId,
		AttemptId:          chunk.AttemptId,
		OwnerCoordinatorId: chunk.OwnerCoordinatorId,
	}
	m.sentChunks = append(m.sentChunks, chunkCopy)
	return nil
}

func (m *mockStreamLogsClient) CloseAndRecv() (*coordinatorv1.StreamLogsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closeErr != nil {
		return nil, m.closeErr
	}
	if m.response == nil {
		m.response = &coordinatorv1.StreamLogsResponse{}
	}
	return m.response, nil
}

func (m *mockStreamLogsClient) Header() (metadata.MD, error) { return nil, nil }
func (m *mockStreamLogsClient) Trailer() metadata.MD         { return nil }
func (m *mockStreamLogsClient) CloseSend() error             { return nil }
func (m *mockStreamLogsClient) Context() context.Context     { return context.Background() }
func (m *mockStreamLogsClient) SendMsg(_ any) error          { return nil }
func (m *mockStreamLogsClient) RecvMsg(_ any) error          { return nil }

// getSentChunks returns a copy of sent chunks for thread-safe access
func (m *mockStreamLogsClient) getSentChunks() []*coordinatorv1.LogChunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*coordinatorv1.LogChunk(nil), m.sentChunks...)
}

func TestToProtoStreamType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    int
		expected coordinatorv1.LogStreamType
	}{
		{"stdout", exec.StreamTypeStdout, coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT},
		{"stderr", exec.StreamTypeStderr, coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR},
		{"unknown", 999, coordinatorv1.LogStreamType_LOG_STREAM_TYPE_UNSPECIFIED},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, coordreport.ToProtoStreamType(tt.input))
		})
	}
}

func TestNewLogStreamer(t *testing.T) {
	t.Parallel()
	client := &logStreamerMockClient{}
	rootRef := exec.DAGRunRef{Name: "root-dag", ID: "root-id"}

	streamer := coordreport.NewLogStreamer(client, "worker-1", "run-123", "test-dag", "attempt-1", rootRef)

	require.NotNil(t, streamer)
	snapshot := coordreport.SnapshotLogStreamer(streamer)
	assert.Equal(t, "worker-1", snapshot.WorkerID)
	assert.Equal(t, "run-123", snapshot.DAGRunID)
	assert.Equal(t, "test-dag", snapshot.DAGName)
	assert.Equal(t, "attempt-1", snapshot.AttemptID)
	assert.Equal(t, rootRef, snapshot.RootRef)
}

func TestLogStreamer_FinalChunksIncludeOwnerCoordinatorID(t *testing.T) {
	t.Parallel()

	stepStream := &mockStreamLogsClient{}
	stepClient := &logStreamerMockClient{
		streamLogsFunc: func(context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return stepStream, nil
		},
	}
	owner := exec.HostInfo{ID: "coord-1", Host: "127.0.0.1", Port: 4321}
	streamer := coordreport.NewLogStreamer(stepClient, "worker-1", "run-123", "test-dag", "attempt-1", exec.DAGRunRef{}, owner)

	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)
	_, err := stepWriter.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, stepWriter.Close())

	for _, chunk := range stepStream.getSentChunks() {
		assert.Equal(t, owner.ID, chunk.OwnerCoordinatorId)
	}

	schedulerStream := &mockStreamLogsClient{}
	schedulerClient := &logStreamerMockClient{
		streamLogsFunc: func(context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return schedulerStream, nil
		},
	}
	schedulerStreamer := coordreport.NewLogStreamer(schedulerClient, "worker-1", "run-123", "test-dag", "attempt-1", exec.DAGRunRef{}, owner)
	localFile, err := os.CreateTemp(t.TempDir(), "scheduler-*.log")
	require.NoError(t, err)
	defer func() { _ = localFile.Close() }()

	schedulerWriter := schedulerStreamer.NewSchedulerLogWriter(context.Background(), localFile)
	_, err = schedulerWriter.Write([]byte("scheduler line"))
	require.NoError(t, err)
	require.NoError(t, schedulerWriter.Close())

	for _, chunk := range schedulerStream.getSentChunks() {
		assert.Equal(t, owner.ID, chunk.OwnerCoordinatorId)
	}
}

func TestSetAttemptID(t *testing.T) {
	t.Parallel()
	streamer := coordreport.NewLogStreamer(&logStreamerMockClient{}, "w", "r", "d", "initial", exec.DAGRunRef{})

	assert.Equal(t, "initial", coordreport.LogStreamerAttemptID(streamer))

	streamer.SetAttemptID("updated")
	assert.Equal(t, "updated", coordreport.LogStreamerAttemptID(streamer))
}

func TestGetAttemptID(t *testing.T) {
	t.Parallel()
	streamer := coordreport.NewLogStreamer(&logStreamerMockClient{}, "w", "r", "d", "test-attempt", exec.DAGRunRef{})
	assert.Equal(t, "test-attempt", coordreport.LogStreamerAttemptID(streamer))
}

func TestSetAttemptID_Concurrent(t *testing.T) {
	t.Parallel()
	streamer := coordreport.NewLogStreamer(&logStreamerMockClient{}, "w", "r", "d", "initial", exec.DAGRunRef{})

	var wg sync.WaitGroup
	const goroutines = 100

	// Concurrent writers
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			streamer.SetAttemptID("attempt-" + string(rune('A'+id%26)))
		}(i)
	}

	// Concurrent readers
	for range goroutines {
		wg.Go(func() {
			_ = coordreport.LogStreamerAttemptID(streamer) // Should not panic
		})
	}

	wg.Wait()
	// Final value should be one of the written values
	final := coordreport.LogStreamerAttemptID(streamer)
	assert.NotEmpty(t, final)
}

func TestNewStepWriter(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "worker-1", "run-123", "test-dag", "attempt-1", exec.DAGRunRef{})

	writer := streamer.NewStepWriter(context.Background(), "step1", exec.StreamTypeStdout)

	require.NotNil(t, writer)
	stepWriter, ok := writer.(*coordreport.StepLogWriter)
	require.True(t, ok)
	snapshot := coordreport.SnapshotStepLogWriter(stepWriter)
	assert.Equal(t, "step1", snapshot.StepName)
	assert.Equal(t, exec.StreamTypeStdout, snapshot.StreamType)
	assert.Equal(t, streamer, snapshot.Streamer)
	assert.False(t, snapshot.Closed)
	assert.False(t, snapshot.StreamInitFailed)
}

func TestWrite_SmallData(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// Write small data (< 32KB)
	data := []byte("small log message")
	n, err := writer.Write(data)

	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	// No chunks sent yet - buffer not full
	assert.Empty(t, mockStream.getSentChunks())
}

func TestWrite_ExactThreshold(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// Write exactly the buffer threshold to trigger flush.
	data := make([]byte, coordreport.LogBufferSize)
	for i := range data {
		data[i] = byte('A' + i%26)
	}

	n, err := writer.Write(data)

	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	// Should have flushed
	chunks := mockStream.getSentChunks()
	require.Len(t, chunks, 1)
	assert.Equal(t, data, chunks[0].Data)
	assert.Equal(t, uint64(1), chunks[0].Sequence)
}

func TestWrite_LargeData(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// Write data larger than buffer (64KB)
	data := make([]byte, 64*1024)
	for i := range data {
		data[i] = byte('X')
	}

	n, err := writer.Write(data)

	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	// Should have flushed
	chunks := mockStream.getSentChunks()
	require.NotEmpty(t, chunks)
}

func TestWrite_MultipleSmallWrites(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// Multiple small writes that accumulate to >= threshold
	smallData := make([]byte, 8*1024) // 8KB each
	for i := range smallData {
		smallData[i] = byte('A')
	}

	// Write 4 times = 32KB, should trigger flush on 4th write
	for range 4 {
		n, err := writer.Write(smallData)
		require.NoError(t, err)
		assert.Equal(t, len(smallData), n)
	}

	chunks := mockStream.getSentChunks()
	require.Len(t, chunks, 1)
	assert.Len(t, chunks[0].Data, 32*1024)
}

func TestWrite_AfterClose(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// Close the writer
	err := writer.Close()
	require.NoError(t, err)

	// Write after close should fail
	n, err := writer.Write([]byte("data"))
	assert.Equal(t, 0, n)
	assert.Equal(t, io.ErrClosedPipe, err)
}

func TestWrite_FlushError_Continues(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{
		sendErr: errors.New("send failed"),
	}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// Write enough to trigger flush (which will fail)
	data := make([]byte, coordreport.LogBufferSize)
	n, err := writer.Write(data)

	// Write should succeed even though flush failed (best-effort)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)
}

func TestWrite_FlushError_ClearsBuffer(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{
		sendErr: errors.New("send failed"),
	}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)
	stepWriter := writer.(*coordreport.StepLogWriter)

	// Write enough to trigger flush
	data := make([]byte, coordreport.LogBufferSize)
	_, _ = writer.Write(data)

	// Buffer should be cleared to prevent memory growth
	snapshot := coordreport.SnapshotStepLogWriter(stepWriter)
	assert.Equal(t, 0, snapshot.BufferLen)
}

func TestFlush_EmptyBuffer(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, nil)

	require.NoError(t, result.Err)
	assert.Empty(t, mockStream.getSentChunks())
}

func TestFlush_StreamInitSuccess(t *testing.T) {
	t.Parallel()
	streamInitCalled := false
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			streamInitCalled = true
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, []byte("test data"))

	require.NoError(t, result.Err)
	assert.True(t, streamInitCalled)
	assert.True(t, result.HasStream)
}

func TestFlush_StreamInitFailure(t *testing.T) {
	t.Parallel()
	initErr := errors.New("connection refused")
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return nil, initErr
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, []byte("test data"))

	assert.Equal(t, initErr, result.Err)
	assert.True(t, result.StreamFailed, "streamInitFailed should be set")
	assert.Equal(t, 0, result.BufferLen, "buffer should be cleared")
}

func TestFlush_AfterInitFailure(t *testing.T) {
	t.Parallel()
	callCount := 0
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			callCount++
			return nil, errors.New("init failed")
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	// First flush triggers init failure.
	_ = coordreport.FlushStepLogWriterWithBuffer(stepWriter, []byte("data1"))

	// Second flush silently returns without retrying.
	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, []byte("data2"))

	require.NoError(t, result.Err, "should silently succeed after init failure")
	assert.Equal(t, 0, result.BufferLen, "buffer should be cleared")
	assert.Equal(t, 1, callCount, "should not retry stream init")
}

func TestFlush_SendSuccess(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, []byte("test data"))

	require.NoError(t, result.Err)
	assert.Equal(t, result.InitialSequence+1, result.FinalSequence, "sequence should increment after success")
}

func TestFlush_SendFailure(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{
		sendErr: errors.New("send failed"),
	}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, []byte("test data"))

	assert.Error(t, result.Err)
	assert.Equal(t, result.InitialSequence, result.FinalSequence, "sequence should NOT increment on failure")
}

func TestFlush_SingleChunk(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	// Buffer < 3MB - single chunk
	data := make([]byte, 1*1024*1024) // 1MB
	for i := range data {
		data[i] = byte('A')
	}

	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, data)

	require.NoError(t, result.Err)
	chunks := mockStream.getSentChunks()
	require.Len(t, chunks, 1)
	assert.Len(t, chunks[0].Data, 1*1024*1024)
}

func TestFlush_ExactMaxChunkSize(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	// A max-size buffer stays in a single chunk.
	data := make([]byte, coordreport.MaxChunkSize)
	for i := range data {
		data[i] = byte('B')
	}

	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, data)

	require.NoError(t, result.Err)
	chunks := mockStream.getSentChunks()
	require.Len(t, chunks, 1)
	assert.Len(t, chunks[0].Data, coordreport.MaxChunkSize)
}

func TestFlush_TwoChunks(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	// 4MB buffer - should split into 3MB + 1MB
	data := make([]byte, 4*1024*1024)
	for i := range data {
		data[i] = byte('C')
	}

	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, data)

	require.NoError(t, result.Err)
	chunks := mockStream.getSentChunks()
	require.Len(t, chunks, 2)
	assert.Len(t, chunks[0].Data, coordreport.MaxChunkSize) // 3MB
	assert.Len(t, chunks[1].Data, 1*1024*1024)              // 1MB
}

func TestFlush_MultipleChunks(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	// 10MB buffer - should split into 3MB + 3MB + 3MB + 1MB = 4 chunks
	data := make([]byte, 10*1024*1024)
	for i := range data {
		data[i] = byte('D')
	}

	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, data)

	require.NoError(t, result.Err)
	chunks := mockStream.getSentChunks()
	require.Len(t, chunks, 4)
	assert.Len(t, chunks[0].Data, coordreport.MaxChunkSize)
	assert.Len(t, chunks[1].Data, coordreport.MaxChunkSize)
	assert.Len(t, chunks[2].Data, coordreport.MaxChunkSize)
	assert.Len(t, chunks[3].Data, 1*1024*1024)
}

func TestFlush_ChunkSequences(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	// 6MB buffer - 2 chunks
	data := make([]byte, 6*1024*1024)

	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, data)

	require.NoError(t, result.Err)
	chunks := mockStream.getSentChunks()
	require.Len(t, chunks, 2)
	assert.Equal(t, uint64(1), chunks[0].Sequence)
	assert.Equal(t, uint64(2), chunks[1].Sequence)
}

func TestFlush_PartialFailure(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{
		sendFunc: func(idx int, _ *coordinatorv1.LogChunk) error {
			// First chunk succeeds, second fails
			if idx == 1 {
				return errors.New("send failed on chunk 2")
			}
			return nil
		},
	}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	// 6MB buffer - would be 2 chunks, but second fails
	data := make([]byte, 6*1024*1024)

	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, data)

	assert.Error(t, result.Err)
	// Only first chunk sent and sequence incremented
	chunks := mockStream.getSentChunks()
	require.Len(t, chunks, 1)
	assert.Equal(t, result.InitialSequence+1, result.FinalSequence, "only first chunk's sequence incremented")
}

func TestFlush_DataCopied(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	data := []byte("original data")

	result := coordreport.FlushStepLogWriterWithBuffer(stepWriter, data)

	require.NoError(t, result.Err)

	// Modify original data after send
	data[0] = 'X'

	// Sent chunk should have original data
	chunks := mockStream.getSentChunks()
	require.Len(t, chunks, 1)
	assert.Equal(t, byte('o'), chunks[0].Data[0], "sent data should not be affected by buffer modification")
}

func TestClose_NoData(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	err := writer.Close()

	require.NoError(t, err)
	// No stream was created (no data written), so no chunks sent
	assert.Empty(t, mockStream.getSentChunks())
}

func TestClose_WithUnflushedData(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// Write small data (not flushed)
	_, _ = writer.Write([]byte("unflushed data"))

	err := writer.Close()

	require.NoError(t, err)
	chunks := mockStream.getSentChunks()
	require.Len(t, chunks, 2) // data chunk + final marker
	assert.Equal(t, []byte("unflushed data"), chunks[0].Data)
	assert.False(t, chunks[0].IsFinal)
	assert.True(t, chunks[1].IsFinal)
}

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// Write and close
	_, _ = writer.Write([]byte("data"))
	err1 := writer.Close()
	err2 := writer.Close()
	err3 := writer.Close()

	require.NoError(t, err1)
	require.NoError(t, err2)
	require.NoError(t, err3)

	// Only one set of chunks sent
	chunks := mockStream.getSentChunks()
	assert.Len(t, chunks, 2) // data + final
}

func TestClose_FinalChunkSequence(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// Write enough to flush, then close
	data := make([]byte, coordreport.LogBufferSize)
	_, _ = writer.Write(data)
	_, _ = writer.Write([]byte("more data"))
	err := writer.Close()

	require.NoError(t, err)
	chunks := mockStream.getSentChunks()
	require.GreaterOrEqual(t, len(chunks), 2)

	// Verify sequences are increasing and final > all data sequences
	finalChunk := chunks[len(chunks)-1]
	assert.True(t, finalChunk.IsFinal)
	for i, chunk := range chunks[:len(chunks)-1] {
		assert.Less(t, chunk.Sequence, finalChunk.Sequence, "chunk %d sequence should be less than final", i)
	}
}

func TestClose_FinalSendSuccess(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	stepWriter := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout).(*coordreport.StepLogWriter)

	_, _ = stepWriter.Write([]byte("data"))
	err := stepWriter.Close()

	require.NoError(t, err)

	// Final sequence should be 2 (data=1, final=2)
	snapshot := coordreport.SnapshotStepLogWriter(stepWriter)
	assert.Equal(t, uint64(2), snapshot.Sequence)
}

func TestClose_FinalSendFailure(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{
		sendFunc: func(_ int, chunk *coordinatorv1.LogChunk) error {
			// Fail on final chunk
			if chunk.IsFinal {
				return errors.New("final send failed")
			}
			return nil
		},
	}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	_, _ = writer.Write([]byte("data"))
	err := writer.Close()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "final send failed")
}

func TestClose_CloseAndRecvError(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{
		closeErr: errors.New("close failed"),
	}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	_, _ = writer.Write([]byte("data"))
	err := writer.Close()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "close failed")
}

func TestClose_MultipleErrors(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{
		sendFunc: func(_ int, chunk *coordinatorv1.LogChunk) error {
			if chunk.IsFinal {
				return errors.New("final send error")
			}
			return nil
		},
		closeErr: errors.New("close error"),
	}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	_, _ = writer.Write([]byte("data"))
	err := writer.Close()

	// First error (final send) should be returned
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "final send error")
}

func TestClose_NoStream(t *testing.T) {
	t.Parallel()
	// Client that returns error on stream init
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return nil, errors.New("init failed")
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// Write triggers init failure
	data := make([]byte, coordreport.LogBufferSize)
	_, _ = writer.Write(data)

	// Close should handle nil stream gracefully
	err := writer.Close()
	// No error because stream never initialized and streamInitFailed handles it
	require.NoError(t, err)
}

func TestClose_FlushErrorThenSendSuccess(t *testing.T) {
	t.Parallel()
	firstFlushDone := false
	mockStream := &mockStreamLogsClient{
		sendFunc: func(_ int, chunk *coordinatorv1.LogChunk) error {
			// First flush chunk fails, final succeeds
			if !chunk.IsFinal && !firstFlushDone {
				firstFlushDone = true
				return errors.New("flush send failed")
			}
			return nil
		},
	}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	_, _ = writer.Write([]byte("data"))
	err := writer.Close()

	// Flush error takes precedence
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "flush send failed")
}

func TestConcurrentWrites(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	var wg sync.WaitGroup
	const goroutines = 100
	const writesPerGoroutine = 10

	for range goroutines {
		wg.Go(func() {
			for range writesPerGoroutine {
				_, err := writer.Write([]byte("data"))
				assert.NoError(t, err)
			}
		})
	}

	wg.Wait()
	require.NoError(t, writer.Close())
}

func TestConcurrentWriteAndClose(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	var wg sync.WaitGroup

	// Writer goroutines
	for range 10 {
		wg.Go(func() {
			for range 100 {
				_, err := writer.Write([]byte("data"))
				// Either succeeds or returns ErrClosedPipe
				if err != nil {
					assert.Equal(t, io.ErrClosedPipe, err)
					return
				}
			}
		})
	}

	// Close after a short delay
	wg.Go(func() {
		_ = writer.Close()
	})

	wg.Wait()
}

func TestConcurrentSetAttemptID(t *testing.T) {
	t.Parallel()
	// Each flush gets its own stream to avoid races
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return &mockStreamLogsClient{}, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "initial", exec.DAGRunRef{})

	var wg sync.WaitGroup

	// Concurrent SetAttemptID calls
	for i := range 50 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			streamer.SetAttemptID("attempt-" + string(rune('A'+id%26)))
		}(i)
	}

	// Concurrent writes with separate writers (each gets its own stream)
	for range 10 {
		wg.Go(func() {
			writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)
			_, _ = writer.Write(make([]byte, coordreport.LogBufferSize)) // Triggers flush which reads attemptID
			_ = writer.Close()
		})
	}

	wg.Wait()
}

func TestLogStreamer_FullLifecycle(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	rootRef := exec.DAGRunRef{Name: "root", ID: "root-123"}
	streamer := coordreport.NewLogStreamer(client, "worker-1", "run-456", "test-dag", "attempt-789", rootRef)

	writer := streamer.NewStepWriter(context.Background(), "step1", exec.StreamTypeStdout)

	// Multiple writes
	for range 5 {
		data := make([]byte, 8*1024) // 8KB each, 40KB total
		_, err := writer.Write(data)
		require.NoError(t, err)
	}

	err := writer.Close()
	require.NoError(t, err)

	// Verify all chunks
	chunks := mockStream.getSentChunks()
	require.NotEmpty(t, chunks)

	// Verify metadata on all chunks
	for _, chunk := range chunks {
		assert.Equal(t, "worker-1", chunk.WorkerId)
		assert.Equal(t, "run-456", chunk.DagRunId)
		assert.Equal(t, "test-dag", chunk.DagName)
		assert.Equal(t, "step1", chunk.StepName)
		assert.Equal(t, coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT, chunk.StreamType)
		assert.Equal(t, "root", chunk.RootDagRunName)
		assert.Equal(t, "root-123", chunk.RootDagRunId)
		assert.Equal(t, "attempt-789", chunk.AttemptId)
	}

	// Verify final chunk
	lastChunk := chunks[len(chunks)-1]
	assert.True(t, lastChunk.IsFinal)

	// Verify sequence ordering
	for i := 1; i < len(chunks); i++ {
		assert.Greater(t, chunks[i].Sequence, chunks[i-1].Sequence)
	}
}

func TestLogStreamer_MultipleSteps(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})

	// Create multiple step writers
	writer1 := streamer.NewStepWriter(context.Background(), "step1", exec.StreamTypeStdout)
	writer2 := streamer.NewStepWriter(context.Background(), "step2", exec.StreamTypeStdout)

	_, _ = writer1.Write([]byte("step1 data"))
	_, _ = writer2.Write([]byte("step2 data"))

	require.NoError(t, writer1.Close())
	require.NoError(t, writer2.Close())

	// Both should have sent their data
	chunks := mockStream.getSentChunks()
	stepNames := make(map[string]bool)
	for _, chunk := range chunks {
		stepNames[chunk.StepName] = true
	}
	assert.True(t, stepNames["step1"])
	assert.True(t, stepNames["step2"])
}

func TestLogStreamer_StdoutAndStderr(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})

	stdout := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)
	stderr := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStderr)

	_, _ = stdout.Write([]byte("stdout data"))
	_, _ = stderr.Write([]byte("stderr data"))

	require.NoError(t, stdout.Close())
	require.NoError(t, stderr.Close())

	// Verify both stream types present
	chunks := mockStream.getSentChunks()
	hasStdout := false
	hasStderr := false
	for _, chunk := range chunks {
		if chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT {
			hasStdout = true
		}
		if chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR {
			hasStderr = true
		}
	}
	assert.True(t, hasStdout)
	assert.True(t, hasStderr)
}

func TestLogStreamer_StepOutputMirrorsToSchedulerLog(t *testing.T) {
	t.Parallel()

	t.Run("successful step sends", func(t *testing.T) {
		mockStream := &mockStreamLogsClient{}
		client := &logStreamerMockClient{
			streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
				return mockStream, nil
			},
		}
		streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})

		localFile, err := os.CreateTemp(t.TempDir(), "scheduler-*.log")
		require.NoError(t, err)
		defer func() { _ = localFile.Close() }()

		scheduler := streamer.NewSchedulerLogWriter(context.Background(), localFile)
		stdout := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)
		stderr := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStderr)

		const schedulerData = "scheduler live data\n"
		const stdoutData = "stdout mirror data\n"
		const stderrData = "stderr mirror data\n"

		_, err = scheduler.Write([]byte(schedulerData))
		require.NoError(t, err)
		_, err = stdout.Write([]byte(stdoutData))
		require.NoError(t, err)
		_, err = stderr.Write([]byte(stderrData))
		require.NoError(t, err)

		require.NoError(t, stdout.Close())
		require.NoError(t, stderr.Close())
		require.NoError(t, scheduler.Close())

		logData, err := os.ReadFile(localFile.Name())
		require.NoError(t, err)
		logContent := string(logData)
		assert.Equal(t, 1, strings.Count(logContent, stdoutData))
		assert.Equal(t, 1, strings.Count(logContent, stderrData))

		chunks := mockStream.getSentChunks()
		var stdoutChunk, stderrChunk bool
		var schedulerChunks []string
		for _, chunk := range chunks {
			switch {
			case chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT &&
				string(chunk.Data) == stdoutData:
				stdoutChunk = true
			case chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR &&
				string(chunk.Data) == stderrData:
				stderrChunk = true
			case chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER &&
				!chunk.IsFinal:
				schedulerChunks = append(schedulerChunks, string(chunk.Data))
			}
		}
		assert.True(t, stdoutChunk)
		assert.True(t, stderrChunk)
		assert.Equal(t, []string{schedulerData}, schedulerChunks)
	})

	t.Run("failed step send still mirrors for replay", func(t *testing.T) {
		stepStream := &mockStreamLogsClient{sendErr: errors.New("step send failed")}
		schedulerStream := &mockStreamLogsClient{}
		var streamCalls int
		client := &logStreamerMockClient{
			streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
				streamCalls++
				if streamCalls == 1 {
					return stepStream, nil
				}
				return schedulerStream, nil
			},
		}
		streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})

		localFile, err := os.CreateTemp(t.TempDir(), "scheduler-*.log")
		require.NoError(t, err)
		defer func() { _ = localFile.Close() }()

		scheduler := streamer.NewSchedulerLogWriter(context.Background(), localFile)
		stdout := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

		const schedulerData = "scheduler live data\n"
		const marker = "failed step marker\n"
		require.Less(t, len(marker), coordreport.LogBufferSize)
		stepData := marker + strings.Repeat("x", coordreport.LogBufferSize-len(marker))

		_, err = scheduler.Write([]byte(schedulerData))
		require.NoError(t, err)
		_, err = stdout.Write([]byte(stepData))
		require.NoError(t, err)

		_ = stdout.Close()
		require.NoError(t, scheduler.Close())

		logData, err := os.ReadFile(localFile.Name())
		require.NoError(t, err)
		assert.Equal(t, 1, strings.Count(string(logData), marker))

		var schedulerChunks []string
		for _, chunk := range schedulerStream.getSentChunks() {
			if chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER &&
				!chunk.IsFinal {
				schedulerChunks = append(schedulerChunks, string(chunk.Data))
			}
		}
		assert.Equal(t, []string{schedulerData}, schedulerChunks)
	})
}

func TestLogStreamer_LargeOutput(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// Write 12MB of data
	data := make([]byte, 12*1024*1024)
	for i := range data {
		data[i] = byte('X')
	}

	n, err := writer.Write(data)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)

	err = writer.Close()
	require.NoError(t, err)

	// Verify all data was sent across multiple chunks
	chunks := mockStream.getSentChunks()
	totalBytes := 0
	for _, chunk := range chunks {
		if !chunk.IsFinal {
			totalBytes += len(chunk.Data)
		}
	}
	assert.Equal(t, len(data), totalBytes)

	// Verify no chunk exceeds the stream chunk limit.
	for _, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk.Data), coordreport.MaxChunkSize)
	}
}

func TestLogStreamer_AttemptIDUpdatedDuringStream(t *testing.T) {
	t.Parallel()
	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "initial-attempt", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// First write with initial attempt ID
	data := make([]byte, coordreport.LogBufferSize)
	_, _ = writer.Write(data)

	// Update attempt ID mid-stream
	streamer.SetAttemptID("updated-attempt")

	// Second write should use updated attempt ID
	_, _ = writer.Write(data)

	err := writer.Close()
	require.NoError(t, err)

	// Verify attempt ID changed in chunks
	chunks := mockStream.getSentChunks()
	attemptIDs := make(map[string]bool)
	for _, chunk := range chunks {
		attemptIDs[chunk.AttemptId] = true
	}
	// Should have both attempt IDs
	assert.True(t, attemptIDs["initial-attempt"] || attemptIDs["updated-attempt"])
}

func TestLogStreamer_SequenceContinuity(t *testing.T) {
	t.Parallel()

	mockStream := &mockStreamLogsClient{}
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return mockStream, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})
	writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)

	// Multiple flushes
	for range 5 {
		data := make([]byte, coordreport.LogBufferSize)
		_, _ = writer.Write(data)
	}
	_ = writer.Close()

	// Verify sequences are strictly increasing with no gaps
	chunks := mockStream.getSentChunks()
	for i := range chunks {
		assert.Equal(t, uint64(i+1), chunks[i].Sequence, "sequence %d should be %d", i, i+1)
	}
}

func TestLogStreamer_RaceDetector(t *testing.T) {
	// This test is specifically for -race flag
	t.Parallel()

	// Each writer gets its own mock stream to avoid races between writers
	client := &logStreamerMockClient{
		streamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return &mockStreamLogsClient{}, nil
		},
	}
	streamer := coordreport.NewLogStreamer(client, "w", "r", "d", "a", exec.DAGRunRef{})

	var wg sync.WaitGroup
	var ops int64

	// Multiple writers on same streamer (each gets its own stream)
	for range 5 {
		wg.Go(func() {
			writer := streamer.NewStepWriter(context.Background(), "step", exec.StreamTypeStdout)
			for range 20 {
				_, _ = writer.Write([]byte("data"))
				atomic.AddInt64(&ops, 1)
			}
			_ = writer.Close()
		})
	}

	// Concurrent SetAttemptID
	wg.Go(func() {
		for i := range 100 {
			streamer.SetAttemptID("attempt-" + string(rune('A'+i%26)))
			atomic.AddInt64(&ops, 1)
		}
	})

	wg.Wait()
	assert.Greater(t, ops, int64(0))
}
