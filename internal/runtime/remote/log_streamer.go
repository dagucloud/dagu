// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

const (
	// logBufferSize is the size of the buffer for accumulating log data before flushing.
	logBufferSize = 32 * 1024 // 32KB

	// maxChunkSize is the maximum size of a single log chunk sent via gRPC.
	// Keep below 4MB to leave room for proto overhead and stay within gRPC limits.
	maxChunkSize = 3 * 1024 * 1024 // 3MB
)

var _ exec.LogWriterFactory = (*LogStreamer)(nil)

// LogStreamer streams logs to coordinator via gRPC
type LogStreamer struct {
	client    coordinator.Client
	workerID  string
	dagRunID  string
	dagName   string
	attemptID string
	rootRef   exec.DAGRunRef
	owner     exec.HostInfo
	mu        sync.RWMutex
}

// NewLogStreamer creates a new LogStreamer
func NewLogStreamer(
	client coordinator.Client,
	workerID string,
	dagRunID string,
	dagName string,
	attemptID string,
	rootRef exec.DAGRunRef,
	owner ...exec.HostInfo,
) *LogStreamer {
	var target exec.HostInfo
	if len(owner) > 0 {
		target = owner[0]
	}
	return &LogStreamer{
		client:    client,
		workerID:  workerID,
		dagRunID:  dagRunID,
		dagName:   dagName,
		attemptID: attemptID,
		rootRef:   rootRef,
		owner:     target,
	}
}

// SetAttemptID updates the attemptID after the agent creates the attempt
func (s *LogStreamer) SetAttemptID(attemptID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attemptID = attemptID
}

// getAttemptID returns the current attemptID
func (s *LogStreamer) getAttemptID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.attemptID
}

func (s *LogStreamer) openStream(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	if s.owner.Host != "" {
		return s.client.StreamLogsTo(ctx, s.owner)
	}
	return s.client.StreamLogs(ctx)
}

// NewStepWriter creates a writer that streams to coordinator
// streamType should be execution.StreamTypeStdout or execution.StreamTypeStderr
func (s *LogStreamer) NewStepWriter(ctx context.Context, stepName string, streamType int) io.WriteCloser {
	return &stepLogWriter{
		ctx:        ctx,
		streamer:   s,
		stepName:   stepName,
		streamType: streamType,
		buffer:     make([]byte, 0, logBufferSize),
	}
}

// NewSchedulerLogWriter creates a writer that writes to both a local file
// and streams to the coordinator in real-time. This enables viewing scheduler
// logs while the DAG is still running.
func (s *LogStreamer) NewSchedulerLogWriter(ctx context.Context, localFile *os.File) io.WriteCloser {
	return &schedulerLogWriter{
		ctx:       ctx,
		streamer:  s,
		localFile: localFile,
		buffer:    make([]byte, 0, logBufferSize),
	}
}

// StreamSchedulerLog reads the local scheduler.log file and streams it to the coordinator.
// This should be called after DAG execution completes.
func (s *LogStreamer) StreamSchedulerLog(ctx context.Context, logFilePath string) error {
	// Read the scheduler.log file
	// #nosec G304 - logFilePath is a controlled internal path from createAgentEnv
	data, err := fileutil.ReadFile(logFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No scheduler log, nothing to stream
		}
		return fmt.Errorf("failed to read scheduler log: %w", err)
	}

	if len(data) == 0 {
		return nil // Empty file, nothing to stream
	}

	// Create a stream to the coordinator
	stream, err := s.openStream(ctx)
	if err != nil {
		return fmt.Errorf("failed to create log stream: %w", err)
	}
	// Ensure stream is closed on all paths to prevent resource leaks
	defer func() {
		_, _ = stream.CloseAndRecv()
	}()

	// Split into chunks if necessary (scheduler logs can be large)
	var sequence uint64 = 0
	for len(data) > 0 {
		chunkSize := min(len(data), maxChunkSize)

		chunkData := make([]byte, chunkSize)
		copy(chunkData, data[:chunkSize])
		data = data[chunkSize:]

		sequence++
		chunk := &coordinatorv1.LogChunk{
			WorkerId:           s.workerID,
			DagRunId:           s.dagRunID,
			DagName:            s.dagName,
			StepName:           "scheduler",
			StreamType:         coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER,
			Data:               chunkData,
			Sequence:           sequence,
			RootDagRunName:     s.rootRef.Name,
			RootDagRunId:       s.rootRef.ID,
			AttemptId:          s.getAttemptID(),
			OwnerCoordinatorId: s.owner.ID,
		}

		if err := stream.Send(chunk); err != nil {
			return fmt.Errorf("failed to send scheduler log chunk: %w", err)
		}
	}

	// Send final marker
	finalChunk := &coordinatorv1.LogChunk{
		WorkerId:           s.workerID,
		DagRunId:           s.dagRunID,
		DagName:            s.dagName,
		StepName:           "scheduler",
		StreamType:         coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER,
		IsFinal:            true,
		Sequence:           sequence + 1,
		RootDagRunName:     s.rootRef.Name,
		RootDagRunId:       s.rootRef.ID,
		AttemptId:          s.getAttemptID(),
		OwnerCoordinatorId: s.owner.ID,
	}

	if err := stream.Send(finalChunk); err != nil {
		return fmt.Errorf("failed to send final marker: %w", err)
	}

	return nil
}

// stepLogWriter implements io.WriteCloser for streaming logs
type stepLogWriter struct {
	ctx              context.Context
	streamer         *LogStreamer
	stepName         string
	streamType       int
	buffer           []byte
	sequence         uint64
	stream           coordinatorv1.CoordinatorService_StreamLogsClient
	mu               sync.Mutex
	closed           bool
	streamInitFailed bool // Tracks permanent stream initialization failure
}

// Write implements io.Writer
func (w *stepLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, io.ErrClosedPipe
	}

	w.buffer = append(w.buffer, p...)

	// Flush when buffer exceeds threshold
	if len(w.buffer) >= logBufferSize {
		if err := w.flush(); err != nil {
			// Log streaming is best-effort - don't fail the command
			logger.Warn(w.ctx, "Failed to stream logs, discarding buffer",
				tag.Error(err),
				tag.Step(w.stepName),
			)
			w.buffer = w.buffer[:0] // Discard to prevent memory growth
		}
	}

	return len(p), nil
}

// flush sends buffered data to coordinator.
// Implements chunk splitting for large buffers to stay within gRPC message size limits.
// Sequence numbers are only incremented after successful Send to avoid gaps.
func (w *stepLogWriter) flush() error {
	if len(w.buffer) == 0 {
		return nil
	}

	// Check for permanent stream initialization failure
	if w.streamInitFailed {
		// Clear buffer to prevent memory growth on permanent failure
		w.buffer = w.buffer[:0]
		return nil // Silently fail - already logged on first failure
	}

	// Initialize stream if needed
	if w.stream == nil {
		var err error
		w.stream, err = w.streamer.openStream(w.ctx)
		if err != nil {
			// Mark as permanently failed to prevent tight retry loop
			w.streamInitFailed = true
			logger.Error(w.ctx, "Stream initialization failed permanently",
				tag.Error(err),
				tag.Step(w.stepName),
			)
			w.buffer = w.buffer[:0] // Discard to prevent memory growth
			return err
		}
	}

	// Split buffer into chunks if necessary to stay within gRPC limits
	data := w.buffer
	w.buffer = w.buffer[:0]

	for len(data) > 0 {
		chunkSize := min(len(data), maxChunkSize)

		// Copy chunk data to avoid corruption if Send buffers the message
		chunkData := make([]byte, chunkSize)
		copy(chunkData, data[:chunkSize])
		data = data[chunkSize:]

		// Use peek value for sequence - only increment after successful Send
		nextSeq := w.sequence + 1
		chunk := &coordinatorv1.LogChunk{
			WorkerId:           w.streamer.workerID,
			DagRunId:           w.streamer.dagRunID,
			DagName:            w.streamer.dagName,
			StepName:           w.stepName,
			StreamType:         toProtoStreamType(w.streamType),
			Data:               chunkData,
			Sequence:           nextSeq,
			RootDagRunName:     w.streamer.rootRef.Name,
			RootDagRunId:       w.streamer.rootRef.ID,
			AttemptId:          w.streamer.getAttemptID(),
			OwnerCoordinatorId: w.streamer.owner.ID,
		}

		if err := w.stream.Send(chunk); err != nil {
			return err // Return error without incrementing sequence
		}
		w.sequence = nextSeq // Only increment after successful Send
	}

	return nil
}

// Close implements io.Closer
func (w *stepLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	var firstErr error

	// Flush any remaining data
	if err := w.flush(); err != nil {
		logger.Error(w.ctx, "Failed to flush log buffer", tag.Error(err))
		firstErr = err
	}

	// Send final marker
	if w.stream != nil {
		// Use peek value for sequence - only increment after successful Send
		nextSeq := w.sequence + 1
		finalChunk := &coordinatorv1.LogChunk{
			WorkerId:           w.streamer.workerID,
			DagRunId:           w.streamer.dagRunID,
			DagName:            w.streamer.dagName,
			StepName:           w.stepName,
			StreamType:         toProtoStreamType(w.streamType),
			IsFinal:            true,
			Sequence:           nextSeq,
			RootDagRunName:     w.streamer.rootRef.Name,
			RootDagRunId:       w.streamer.rootRef.ID,
			AttemptId:          w.streamer.getAttemptID(),
			OwnerCoordinatorId: w.streamer.owner.ID,
		}
		if err := w.stream.Send(finalChunk); err != nil {
			logger.Error(w.ctx, "Failed to send final log chunk", tag.Error(err))
			if firstErr == nil {
				firstErr = err
			}
		} else {
			w.sequence = nextSeq // Only increment after successful Send
		}

		// Close and receive response
		if _, err := w.stream.CloseAndRecv(); err != nil {
			logger.Error(w.ctx, "Failed to close log stream", tag.Error(err))
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// toProtoStreamType converts streamType int to proto LogStreamType
func toProtoStreamType(streamType int) coordinatorv1.LogStreamType {
	switch streamType {
	case exec.StreamTypeStdout:
		return coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT
	case exec.StreamTypeStderr:
		return coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR
	default:
		return coordinatorv1.LogStreamType_LOG_STREAM_TYPE_UNSPECIFIED
	}
}

// schedulerLogWriter writes to both local file and streams to coordinator in real-time.
// This enables viewing scheduler logs while the DAG is still running.
type schedulerLogWriter struct {
	ctx              context.Context
	streamer         *LogStreamer
	localFile        *os.File
	buffer           []byte
	sequence         uint64
	stream           coordinatorv1.CoordinatorService_StreamLogsClient
	mu               sync.Mutex
	closed           bool
	streamInitFailed bool // Tracks permanent stream initialization failure
}

// Write implements io.Writer - writes to local file and buffers for streaming
func (w *schedulerLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, io.ErrClosedPipe
	}

	// Always write to local file first (primary storage)
	n, err := w.localFile.Write(p)
	if err != nil {
		return n, err
	}

	// Buffer for streaming (best-effort, don't fail on streaming errors)
	w.buffer = append(w.buffer, p...)

	// Flush to coordinator when buffer exceeds threshold
	if len(w.buffer) >= logBufferSize {
		if err := w.flush(); err != nil {
			// Log streaming is best-effort - don't fail the write
			// Avoid recursive logging by not using logger here
			w.buffer = w.buffer[:0] // Discard to prevent memory growth
		}
	}

	return n, nil
}

// flush sends buffered data to coordinator
func (w *schedulerLogWriter) flush() error {
	if len(w.buffer) == 0 {
		return nil
	}

	// Check for permanent stream initialization failure
	if w.streamInitFailed {
		w.buffer = w.buffer[:0]
		return nil // Silently fail - already logged on first failure
	}

	// Initialize stream if needed
	if w.stream == nil {
		var err error
		w.stream, err = w.streamer.openStream(w.ctx)
		if err != nil {
			w.streamInitFailed = true
			w.buffer = w.buffer[:0]
			return err
		}
	}

	// Split buffer into chunks if necessary
	data := w.buffer
	w.buffer = w.buffer[:0]

	for len(data) > 0 {
		chunkSize := min(len(data), maxChunkSize)

		chunkData := make([]byte, chunkSize)
		copy(chunkData, data[:chunkSize])
		data = data[chunkSize:]

		nextSeq := w.sequence + 1
		chunk := &coordinatorv1.LogChunk{
			WorkerId:           w.streamer.workerID,
			DagRunId:           w.streamer.dagRunID,
			DagName:            w.streamer.dagName,
			StepName:           "scheduler",
			StreamType:         coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER,
			Data:               chunkData,
			Sequence:           nextSeq,
			RootDagRunName:     w.streamer.rootRef.Name,
			RootDagRunId:       w.streamer.rootRef.ID,
			AttemptId:          w.streamer.getAttemptID(),
			OwnerCoordinatorId: w.streamer.owner.ID,
		}

		if err := w.stream.Send(chunk); err != nil {
			return err
		}
		w.sequence = nextSeq
	}

	return nil
}

// Close implements io.Closer - flushes remaining data and closes the stream
func (w *schedulerLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	// Flush any remaining buffered data
	_ = w.flush() // Ignore error - best effort

	// Send final marker if stream was initialized
	if w.stream != nil {
		nextSeq := w.sequence + 1
		finalChunk := &coordinatorv1.LogChunk{
			WorkerId:           w.streamer.workerID,
			DagRunId:           w.streamer.dagRunID,
			DagName:            w.streamer.dagName,
			StepName:           "scheduler",
			StreamType:         coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER,
			IsFinal:            true,
			Sequence:           nextSeq,
			RootDagRunName:     w.streamer.rootRef.Name,
			RootDagRunId:       w.streamer.rootRef.ID,
			AttemptId:          w.streamer.getAttemptID(),
			OwnerCoordinatorId: w.streamer.owner.ID,
		}
		_ = w.stream.Send(finalChunk)  // Ignore error - best effort
		_, _ = w.stream.CloseAndRecv() // Ignore error - best effort
	}

	// Note: localFile is NOT closed here - caller owns it
	return nil
}
