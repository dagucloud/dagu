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

	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestStreamTypeToExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		streamType coordinatorv1.LogStreamType
		expected   string
	}{
		{
			name:       "STDOUT",
			streamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
			expected:   "stdout.log",
		},
		{
			name:       "STDERR",
			streamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR,
			expected:   "stderr.log",
		},
		{
			name:       "SCHEDULER",
			streamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER,
			expected:   "scheduler.log",
		},
		{
			name:       "UNSPECIFIED",
			streamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_UNSPECIFIED,
			expected:   "log",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := StreamTypeToExtension(tt.streamType)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestLogHandler_StreamKey(t *testing.T) {
	t.Parallel()

	h := newLogHandler("/tmp/logs")

	t.Run("UniqueKeyGeneration", func(t *testing.T) {
		t.Parallel()

		chunk := &coordinatorv1.LogChunk{
			DagName:    "test-dag",
			DagRunId:   "run-123",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
		}

		key := h.streamKey(chunk)
		require.Equal(t, "test-dag/run-123/attempt-1/step1/LOG_STREAM_TYPE_STDOUT", key)
	})

	t.Run("DifferentStreamTypesProduceDifferentKeys", func(t *testing.T) {
		t.Parallel()

		chunkStdout := &coordinatorv1.LogChunk{
			DagName:    "test-dag",
			DagRunId:   "run-123",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
		}

		chunkStderr := &coordinatorv1.LogChunk{
			DagName:    "test-dag",
			DagRunId:   "run-123",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR,
		}

		keyStdout := h.streamKey(chunkStdout)
		keyStderr := h.streamKey(chunkStderr)

		require.NotEqual(t, keyStdout, keyStderr)
	})
}

func TestLogHandler_LogFilePath(t *testing.T) {
	t.Parallel()

	logDir := filepath.Join(string(os.PathSeparator), "var", "logs")
	h := newLogHandler(logDir)

	t.Run("BasicPathGeneration", func(t *testing.T) {
		t.Parallel()

		chunk := &coordinatorv1.LogChunk{
			DagName:    "test-dag",
			DagRunId:   "run-123",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
		}

		path := h.logFilePath(chunk)
		expected := filepath.Join(logDir, "test-dag", "run-123", "attempt-1", "step1.stdout.log")
		require.Equal(t, expected, path)
	})

	t.Run("SubDAGPathUsesRootDAG", func(t *testing.T) {
		t.Parallel()

		chunk := &coordinatorv1.LogChunk{
			DagName:        "sub-dag",
			DagRunId:       "sub-run-456",
			AttemptId:      "attempt-1",
			StepName:       "step1",
			StreamType:     coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
			RootDagRunName: "root-dag",
			RootDagRunId:   "root-run-123",
		}

		path := h.logFilePath(chunk)
		// Should use root DAG's directory
		expected := filepath.Join(logDir, "root-dag", "root-run-123", "attempt-1", "step1.stdout.log")
		require.Equal(t, expected, path)
	})

	t.Run("EmptyAttemptIdFallback", func(t *testing.T) {
		t.Parallel()

		chunk := &coordinatorv1.LogChunk{
			DagName:    "test-dag",
			DagRunId:   "run-123",
			AttemptId:  "", // Empty
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR,
		}

		path := h.logFilePath(chunk)
		// Should fallback to dagRunID
		expected := filepath.Join(logDir, "test-dag", "run-123", "run-123", "step1.stderr.log")
		require.Equal(t, expected, path)
	})

	t.Run("SafeNameSanitization", func(t *testing.T) {
		t.Parallel()

		chunk := &coordinatorv1.LogChunk{
			DagName:    "test/dag:with:special",
			DagRunId:   "run-123",
			AttemptId:  "attempt-1",
			StepName:   "step/with/slashes",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
		}

		path := h.logFilePath(chunk)
		// Path should be sanitized but still contain the components
		require.Contains(t, path, logDir)
		require.NotContains(t, filepath.Base(path), "/")
	})
}

func TestLogHandler_GetOrCreateWriter(t *testing.T) {
	t.Parallel()

	t.Run("CreatesNewWriter", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)
		defer h.Close(context.Background())

		chunk := &coordinatorv1.LogChunk{
			DagName:    "test-dag",
			DagRunId:   "run-123",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
		}

		writer, err := h.getOrCreateWriter(chunk)
		require.NoError(t, err)
		require.NotNil(t, writer)
		require.NotNil(t, writer.file)
	})

	t.Run("ReturnsExistingWriter", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)
		defer h.Close(context.Background())

		chunk := &coordinatorv1.LogChunk{
			DagName:    "test-dag",
			DagRunId:   "run-123",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
		}

		writer1, err := h.getOrCreateWriter(chunk)
		require.NoError(t, err)

		writer2, err := h.getOrCreateWriter(chunk)
		require.NoError(t, err)

		// Should be the same writer
		require.Same(t, writer1, writer2)
	})

	t.Run("CreatesDirectoryStructure", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)
		defer h.Close(context.Background())

		chunk := &coordinatorv1.LogChunk{
			DagName:    "nested-dag",
			DagRunId:   "run-456",
			AttemptId:  "attempt-2",
			StepName:   "deep-step",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR,
		}

		_, err := h.getOrCreateWriter(chunk)
		require.NoError(t, err)

		// Verify directory was created
		expectedDir := filepath.Join(logDir, "nested-dag", "run-456", "attempt-2")
		_, err = os.Stat(expectedDir)
		require.NoError(t, err)
	})

	t.Run("DifferentChunksGetDifferentWriters", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)
		defer h.Close(context.Background())

		chunk1 := &coordinatorv1.LogChunk{
			DagName:    "dag1",
			DagRunId:   "run-1",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
		}

		chunk2 := &coordinatorv1.LogChunk{
			DagName:    "dag2",
			DagRunId:   "run-2",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
		}

		writer1, err := h.getOrCreateWriter(chunk1)
		require.NoError(t, err)

		writer2, err := h.getOrCreateWriter(chunk2)
		require.NoError(t, err)

		require.NotSame(t, writer1, writer2)
	})
}

func TestLogHandler_CloseWriter(t *testing.T) {
	t.Parallel()

	t.Run("ClosesAndRemovesWriter", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)

		chunk := &coordinatorv1.LogChunk{
			DagName:    "test-dag",
			DagRunId:   "run-123",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
		}

		// Create a writer
		_, err := h.getOrCreateWriter(chunk)
		require.NoError(t, err)

		key := h.streamKey(chunk)
		h.writersMu.Lock()
		_, exists := h.writers[key]
		h.writersMu.Unlock()
		require.True(t, exists)

		// Close the writer
		require.NoError(t, h.closeWriter(chunk))

		// Verify it's removed
		h.writersMu.Lock()
		_, exists = h.writers[key]
		h.writersMu.Unlock()
		require.False(t, exists)
	})

	t.Run("NoOpForNonExistentKey", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)

		chunk := &coordinatorv1.LogChunk{
			DagName:    "nonexistent-dag",
			DagRunId:   "run-999",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
		}

		// Should not panic
		require.NoError(t, h.closeWriter(chunk))
	})
}

func TestLogHandler_Close(t *testing.T) {
	t.Parallel()

	t.Run("ClosesAllWriters", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)

		// Create multiple writers
		chunks := []*coordinatorv1.LogChunk{
			{
				DagName:    "dag1",
				DagRunId:   "run-1",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
			},
			{
				DagName:    "dag2",
				DagRunId:   "run-2",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR,
			},
			{
				DagName:    "dag3",
				DagRunId:   "run-3",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
			},
		}

		for _, chunk := range chunks {
			_, err := h.getOrCreateWriter(chunk)
			require.NoError(t, err)
		}

		h.writersMu.Lock()
		require.Len(t, h.writers, 3)
		h.writersMu.Unlock()

		// Close all
		h.Close(context.Background())

		// Verify all are removed
		h.writersMu.Lock()
		require.Empty(t, h.writers)
		h.writersMu.Unlock()
	})
}

func TestStreamLogWriter_Write(t *testing.T) {
	t.Parallel()

	t.Run("WritesDataToFile", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)
		defer h.Close(context.Background())

		chunk := &coordinatorv1.LogChunk{
			DagName:    "test-dag",
			DagRunId:   "run-123",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
		}

		writer, err := h.getOrCreateWriter(chunk)
		require.NoError(t, err)

		// Write some data using thread-safe method
		testData := []byte("Hello, World!\n")
		chunk.Data = testData
		n, err := writer.write(chunk)
		require.NoError(t, err)
		require.Equal(t, len(testData), n)

		// Close writer (which flushes)
		require.NoError(t, h.closeWriter(chunk))

		// Verify file contents
		filePath := h.logFilePath(chunk)
		contents, err := os.ReadFile(filePath)
		require.NoError(t, err)
		require.Equal(t, testData, contents)
	})
}

func TestLogHandler_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	logDir := t.TempDir()
	h := newLogHandler(logDir)
	defer h.Close(context.Background())

	// Launch multiple goroutines accessing the handler concurrently
	done := make(chan bool)
	for i := range 10 {
		go func(idx int) {
			chunk := &coordinatorv1.LogChunk{
				DagName:    "test-dag",
				DagRunId:   "run-123",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
			}

			// All goroutines try to get the same writer
			writer, err := h.getOrCreateWriter(chunk)
			if err != nil {
				t.Errorf("goroutine %d: getOrCreateWriter failed: %v", idx, err)
				done <- false
				return
			}

			// Write some data using thread-safe method
			chunk.Data = []byte("test\n")
			_, err = writer.write(chunk)
			if err != nil {
				t.Errorf("goroutine %d: Write failed: %v", idx, err)
				done <- false
				return
			}

			done <- true
		}(i)
	}

	// Wait for all goroutines
	for range 10 {
		<-done
	}
}

func TestLogHandler_HandleStream(t *testing.T) {
	t.Parallel()

	t.Run("ProcessesMultipleChunks", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)
		defer h.Close(context.Background())

		chunks := []*coordinatorv1.LogChunk{
			{
				DagName:    "test-dag",
				DagRunId:   "run-123",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
				Data:       []byte("line 1\n"),
			},
			{
				DagName:    "test-dag",
				DagRunId:   "run-123",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
				Data:       []byte("line 2\n"),
			},
			{
				DagName:    "test-dag",
				DagRunId:   "run-123",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
				IsFinal:    true,
			},
		}

		stream := &mockStreamLogsServer{
			chunks: chunks,
			ctx:    context.Background(),
		}

		err := h.handleStream(stream)
		require.NoError(t, err)
		require.NotNil(t, stream.response)
		assert.Equal(t, uint64(3), stream.response.ChunksReceived)
		assert.Equal(t, uint64(14), stream.response.BytesWritten) // "line 1\n" + "line 2\n"

		// Verify file was created with correct content
		expectedPath := filepath.Join(logDir, "test-dag", "run-123", "attempt-1", "step1.stdout.log")
		content, err := os.ReadFile(expectedPath)
		require.NoError(t, err)
		assert.Equal(t, "line 1\nline 2\n", string(content))
	})

	t.Run("ShorterReplayReplacesAndTruncatesPreviouslyWrittenBytes", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		baseChunk := &coordinatorv1.LogChunk{
			DagName:    "test-dag",
			DagRunId:   "run-replay",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
		}
		baseChunk.SetByteOffset(0)
		firstChunk := proto.Clone(baseChunk).(*coordinatorv1.LogChunk)
		firstChunk.Data = []byte("hello stale tail")

		firstHandler := newLogHandler(logDir)
		firstStream := &mockStreamLogsServer{
			chunks: []*coordinatorv1.LogChunk{firstChunk},
			ctx:    context.Background(),
		}
		require.NoError(t, firstHandler.handleStream(firstStream))
		firstHandler.Close(context.Background())

		replayedChunk := proto.Clone(baseChunk).(*coordinatorv1.LogChunk)
		replayedChunk.Data = []byte("hello")
		finalChunk := proto.Clone(baseChunk).(*coordinatorv1.LogChunk)
		finalChunk.IsFinal = true
		finalChunk.SetByteOffset(uint64(len(replayedChunk.Data)))
		secondHandler := newLogHandler(logDir)
		defer secondHandler.Close(context.Background())
		secondStream := &mockStreamLogsServer{
			chunks: []*coordinatorv1.LogChunk{replayedChunk, finalChunk},
			ctx:    context.Background(),
		}
		require.NoError(t, secondHandler.handleStream(secondStream))

		content, err := os.ReadFile(secondHandler.logFilePath(baseChunk))
		require.NoError(t, err)
		assert.Equal(t, replayedChunk.Data, content)
	})

	t.Run("MakesTinyChunkVisibleBeforeStreamCompletion", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)
		defer h.Close(context.Background())

		chunk := &coordinatorv1.LogChunk{
			DagName:    "test-dag",
			DagRunId:   "run-live",
			AttemptId:  "attempt-1",
			StepName:   "step1",
			StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
			Data:       []byte("live\n"),
		}
		stream := &blockingStreamLogsServer{
			mockStreamLogsServer: mockStreamLogsServer{ctx: context.Background()},
			chunk:                chunk,
			waiting:              make(chan struct{}),
			release:              make(chan struct{}),
		}

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.handleStream(stream)
		}()

		select {
		case <-stream.waiting:
		case <-time.After(5 * time.Second):
			t.Fatal("stream handler did not process the first chunk")
		}

		select {
		case err := <-errCh:
			t.Fatalf("stream handler completed before stream EOF: %v", err)
		default:
		}

		content, err := os.ReadFile(h.logFilePath(chunk))
		require.NoError(t, err)
		assert.Equal(t, chunk.Data, content)

		close(stream.release)
		require.NoError(t, <-errCh)
		require.NotNil(t, stream.response)
		assert.Equal(t, uint64(1), stream.response.ChunksReceived)
		assert.Equal(t, uint64(len(chunk.Data)), stream.response.BytesWritten)
	})

	t.Run("HandlesEmptyChunks", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)
		defer h.Close(context.Background())

		chunks := []*coordinatorv1.LogChunk{
			{
				DagName:    "test-dag",
				DagRunId:   "run-123",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
				Data:       []byte{}, // Empty data
			},
			{
				DagName:    "test-dag",
				DagRunId:   "run-123",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
				Data:       []byte("actual data\n"),
			},
		}

		stream := &mockStreamLogsServer{
			chunks: chunks,
			ctx:    context.Background(),
		}

		err := h.handleStream(stream)
		require.NoError(t, err)
		assert.Equal(t, uint64(2), stream.response.ChunksReceived)
		assert.Equal(t, uint64(12), stream.response.BytesWritten) // Only "actual data\n"
	})

	t.Run("HandlesFinalMarker", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)
		defer h.Close(context.Background())

		chunks := []*coordinatorv1.LogChunk{
			{
				DagName:    "test-dag",
				DagRunId:   "run-456",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR,
				Data:       []byte("error message\n"),
			},
			{
				DagName:    "test-dag",
				DagRunId:   "run-456",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR,
				IsFinal:    true, // Final marker
			},
		}

		stream := &mockStreamLogsServer{
			chunks: chunks,
			ctx:    context.Background(),
		}

		err := h.handleStream(stream)
		require.NoError(t, err)

		// Verify writer was closed (removed from map)
		key := h.streamKey(chunks[0])
		h.writersMu.Lock()
		_, exists := h.writers[key]
		h.writersMu.Unlock()
		assert.False(t, exists, "Writer should be closed after IsFinal marker")
	})

	t.Run("HandlesMultipleStreams", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := newLogHandler(logDir)
		defer h.Close(context.Background())

		chunks := []*coordinatorv1.LogChunk{
			// Stream 1 - stdout
			{
				DagName:    "test-dag",
				DagRunId:   "run-789",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
				Data:       []byte("stdout output\n"),
			},
			// Stream 2 - stderr
			{
				DagName:    "test-dag",
				DagRunId:   "run-789",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR,
				Data:       []byte("stderr output\n"),
			},
			// Final for stdout
			{
				DagName:    "test-dag",
				DagRunId:   "run-789",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
				IsFinal:    true,
			},
			// Final for stderr
			{
				DagName:    "test-dag",
				DagRunId:   "run-789",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR,
				IsFinal:    true,
			},
		}

		stream := &mockStreamLogsServer{
			chunks: chunks,
			ctx:    context.Background(),
		}

		err := h.handleStream(stream)
		require.NoError(t, err)

		// Verify both files were created
		stdoutPath := filepath.Join(logDir, "test-dag", "run-789", "attempt-1", "step1.stdout.log")
		stderrPath := filepath.Join(logDir, "test-dag", "run-789", "attempt-1", "step1.stderr.log")

		stdoutContent, err := os.ReadFile(stdoutPath)
		require.NoError(t, err)
		assert.Equal(t, "stdout output\n", string(stdoutContent))

		stderrContent, err := os.ReadFile(stderrPath)
		require.NoError(t, err)
		assert.Equal(t, "stderr output\n", string(stderrContent))
	})
}

type blockingStreamLogsServer struct {
	mockStreamLogsServer
	chunk     *coordinatorv1.LogChunk
	waiting   chan struct{}
	release   chan struct{}
	delivered bool
}

func (s *blockingStreamLogsServer) Recv() (*coordinatorv1.LogChunk, error) {
	if !s.delivered {
		s.delivered = true
		return s.chunk, nil
	}

	close(s.waiting)
	<-s.release
	return nil, io.EOF
}
