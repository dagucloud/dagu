// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordreport

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// logBufferSize is the size of the buffer for accumulating log data before flushing.
	logBufferSize = 32 * 1024 // 32KB

	// maxChunkSize is the maximum size of a single log chunk sent via gRPC.
	// Keep below 4MB to leave room for proto overhead and stay within gRPC limits.
	maxChunkSize = 3 * 1024 * 1024 // 3MB

	logFlushInterval          = 2 * time.Second
	logStreamOperationTimeout = 5 * time.Second
)

func isLogStreamingNotConfigured(err error) bool {
	st, ok := status.FromError(err)
	return ok &&
		st.Code() == codes.FailedPrecondition &&
		strings.Contains(st.Message(), "log streaming not configured")
}

var _ runctx.LogWriterFactory = (*LogStreamer)(nil)
var _ runtime.SchedulerLogStreamer = (*LogStreamer)(nil)

// LogStreamer streams logs to coordinator via gRPC
type LogStreamer struct {
	client    coordinator.Client
	workerID  string
	dagRunID  string
	dagName   string
	attemptID string
	rootRef   dagrun.DAGRunRef
	owner     serviceregistry.HostInfo
	mu        sync.RWMutex

	schedulerMu     sync.RWMutex
	schedulerWriter *schedulerLogWriter
}

// NewLogStreamer creates a new LogStreamer
func NewLogStreamer(
	client coordinator.Client,
	workerID string,
	dagRunID string,
	dagName string,
	attemptID string,
	rootRef dagrun.DAGRunRef,
	owner ...serviceregistry.HostInfo,
) *LogStreamer {
	var target serviceregistry.HostInfo
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

func (s *LogStreamer) registerSchedulerWriter(w *schedulerLogWriter) {
	s.schedulerMu.Lock()
	defer s.schedulerMu.Unlock()
	s.schedulerWriter = w
}

func (s *LogStreamer) unregisterSchedulerWriter(w *schedulerLogWriter) {
	s.schedulerMu.Lock()
	defer s.schedulerMu.Unlock()
	if s.schedulerWriter == w {
		s.schedulerWriter = nil
	}
}

func (s *LogStreamer) activeSchedulerWriter() *schedulerLogWriter {
	s.schedulerMu.RLock()
	defer s.schedulerMu.RUnlock()
	return s.schedulerWriter
}

func (s *LogStreamer) mirrorToSchedulerLog(data []byte) {
	writer := s.activeSchedulerWriter()
	if writer == nil {
		return
	}
	writer.mirrorStepOutput(data)
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
	if ctx == nil {
		ctx = context.Background()
	}
	streamCtx, cancel := context.WithCancel(ctx)
	return &stepLogWriter{
		ctx:        streamCtx,
		cancel:     cancel,
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
	if ctx == nil {
		ctx = context.Background()
	}
	streamCtx, cancel := context.WithCancel(ctx)
	w := &schedulerLogWriter{
		ctx:           streamCtx,
		cancel:        cancel,
		streamer:      s,
		localFile:     localFile,
		buffer:        make([]byte, 0, logBufferSize),
		flushStop:     make(chan struct{}),
		flushFinished: make(chan struct{}),
		flushWake:     make(chan struct{}, 1),
		closeDone:     make(chan struct{}),
	}
	s.registerSchedulerWriter(w)
	go w.runFlushLoop()
	return w
}

// StreamSchedulerLog reads the local scheduler.log file and streams it to the coordinator.
func (s *LogStreamer) StreamSchedulerLog(ctx context.Context, logFilePath string) (err error) {
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
		if isLogStreamingNotConfigured(err) {
			return nil
		}
		return fmt.Errorf("failed to create log stream: %w", err)
	}
	// Ensure stream is closed on all paths to prevent resource leaks
	defer func() {
		if _, closeErr := stream.CloseAndRecv(); closeErr != nil {
			if isLogStreamingNotConfigured(closeErr) {
				return
			}
			if err == nil {
				err = fmt.Errorf("failed to close scheduler log stream: %w", closeErr)
			}
		}
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
			if isLogStreamingNotConfigured(err) {
				return nil
			}
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
		if isLogStreamingNotConfigured(err) {
			return nil
		}
		return fmt.Errorf("failed to send final marker: %w", err)
	}

	return nil
}

// stepLogWriter implements io.WriteCloser for streaming logs
type stepLogWriter struct {
	ctx              context.Context
	cancel           context.CancelFunc
	streamer         *LogStreamer
	stepName         string
	streamType       int
	buffer           []byte
	sequence         uint64
	stream           coordinatorv1.CoordinatorService_StreamLogsClient
	mu               sync.Mutex
	closed           bool
	streamInitFailed bool // Tracks terminal stream failure
	pendingSince     time.Time
}

// Write implements io.Writer
func (w *stepLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, io.ErrClosedPipe
	}

	if len(w.buffer) == 0 {
		w.pendingSince = time.Now()
	}
	w.buffer = append(w.buffer, p...)

	// Flush when buffer exceeds threshold
	if len(w.buffer) >= logBufferSize {
		_ = w.flushLocked()
	}

	return len(p), nil
}

// Flush sends pending log data to the coordinator.
func (w *stepLogWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	return w.flushLocked()
}

// FlushIfDue sends pending log data after the buffering interval has elapsed.
func (w *stepLogWriter) FlushIfDue() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed || len(w.buffer) == 0 || time.Since(w.pendingSince) < logFlushInterval {
		return nil
	}
	return w.flushLocked()
}

// flushLocked sends buffered data to coordinator.
// Implements chunk splitting for large buffers to stay within gRPC message size limits.
// Sequence numbers are only incremented after successful Send to avoid gaps.
func (w *stepLogWriter) flushLocked() error {
	if len(w.buffer) == 0 {
		return nil
	}

	// Split buffer into chunks if necessary to stay within gRPC limits
	data := w.buffer
	w.buffer = w.buffer[:0]
	w.pendingSince = time.Time{}
	streamingDisabled := w.streamInitFailed
	var firstErr error

	for len(data) > 0 {
		chunkSize := min(len(data), maxChunkSize)

		// Copy chunk data to avoid corruption if Send buffers the message
		chunkData := make([]byte, chunkSize)
		copy(chunkData, data[:chunkSize])
		data = data[chunkSize:]

		w.streamer.mirrorToSchedulerLog(chunkData)
		if streamingDisabled {
			continue
		}

		// Initialize stream if needed
		if w.stream == nil {
			var stream coordinatorv1.CoordinatorService_StreamLogsClient
			err := w.withOperationTimeout(func() error {
				var err error
				stream, err = w.streamer.openStream(w.ctx)
				return err
			})
			if err != nil {
				w.disableStreamLocked(err)
				streamingDisabled = true
				if isLogStreamingNotConfigured(err) {
					continue
				}
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			w.stream = stream
		}

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

		if err := w.withOperationTimeout(func() error {
			return w.stream.Send(chunk)
		}); err != nil {
			w.disableStreamLocked(err)
			streamingDisabled = true
			if isLogStreamingNotConfigured(err) {
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		w.sequence = nextSeq // Only increment after successful Send
	}

	return firstErr
}

func (w *stepLogWriter) disableStreamLocked(err error) {
	if w.streamInitFailed {
		return
	}
	w.streamInitFailed = true
	if isLogStreamingNotConfigured(err) {
		return
	}
	logger.Warn(w.ctx, "Step log streaming disabled after stream failure",
		tag.Error(err),
		tag.Step(w.stepName),
	)
}

func (w *stepLogWriter) cancelStream() {
	if w.cancel != nil {
		w.cancel()
	}
}

func (w *stepLogWriter) withOperationTimeout(operation func() error) error {
	cancelTimer := time.AfterFunc(logStreamOperationTimeout, w.cancelStream)
	defer cancelTimer.Stop()
	return operation()
}

// Close implements io.Closer
func (w *stepLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true
	defer w.cancelStream()

	var firstErr error

	// Flush any remaining data
	if err := w.flushLocked(); err != nil {
		firstErr = err
	}

	// Send final marker
	if w.stream != nil {
		if !w.streamInitFailed {
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
			if err := w.withOperationTimeout(func() error {
				return w.stream.Send(finalChunk)
			}); err != nil {
				w.disableStreamLocked(err)
				if !isLogStreamingNotConfigured(err) {
					if firstErr == nil {
						firstErr = err
					}
				}
			} else {
				w.sequence = nextSeq // Only increment after successful Send
			}
		}

		err := w.withOperationTimeout(func() error {
			_, err := w.stream.CloseAndRecv()
			return err
		})
		w.stream = nil
		if err != nil {
			if isLogStreamingNotConfigured(err) {
				w.streamInitFailed = true
			} else {
				logger.Error(w.ctx, "Failed to close log stream", tag.Error(err))
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}

	return firstErr
}

// toProtoStreamType converts streamType int to proto LogStreamType
func toProtoStreamType(streamType int) coordinatorv1.LogStreamType {
	switch streamType {
	case runctx.StreamTypeStdout:
		return coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT
	case runctx.StreamTypeStderr:
		return coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR
	default:
		return coordinatorv1.LogStreamType_LOG_STREAM_TYPE_UNSPECIFIED
	}
}

// schedulerLogWriter writes to both local file and streams to coordinator in real-time.
// This enables viewing scheduler logs while the DAG is still running.
type schedulerLogWriter struct {
	ctx              context.Context
	cancel           context.CancelFunc
	streamer         *LogStreamer
	localFile        *os.File
	buffer           []byte
	sequence         uint64
	localBytes       int64
	streamedBytes    int64
	stream           coordinatorv1.CoordinatorService_StreamLogsClient
	mu               sync.Mutex
	closed           bool
	streamMu         sync.Mutex
	streamInitFailed bool // Tracks permanent stream initialization failure
	flushStop        chan struct{}
	flushFinished    chan struct{}
	flushWake        chan struct{}
	flushStopOnce    sync.Once
	closeOnce        sync.Once
	closeDone        chan struct{}
}

func (w *schedulerLogWriter) cancelStream() {
	if w.cancel != nil {
		w.cancel()
	}
}

func (w *schedulerLogWriter) runFlushLoop() {
	defer close(w.flushFinished)

	ticker := time.NewTicker(logFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.flushStop:
			return
		case <-w.flushWake:
			_ = w.Flush()
		case <-ticker.C:
			_ = w.Flush()
		}
	}
}

func (w *schedulerLogWriter) stopFlushLoop() {
	w.flushStopOnce.Do(func() {
		close(w.flushStop)
	})
	<-w.flushFinished
}

func (w *schedulerLogWriter) requestFlush() {
	select {
	case w.flushWake <- struct{}{}:
	default:
	}
}

// Write implements io.Writer - writes to local file and buffers for streaming
func (w *schedulerLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return 0, io.ErrClosedPipe
	}

	n, shouldFlush, err := w.writeLocalAndBufferLocked(p)
	w.mu.Unlock()
	if shouldFlush {
		w.requestFlush()
	}
	if err != nil {
		return n, err
	}

	return n, nil
}

func (w *schedulerLogWriter) mirrorStepOutput(p []byte) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	_, _, _ = w.writeLocalAndBufferLocked(p)
	w.mu.Unlock()

	w.requestFlush()
}

func (w *schedulerLogWriter) takePendingData() ([]byte, int64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil, w.localBytes, true
	}
	if len(w.buffer) == 0 {
		return nil, w.localBytes, false
	}

	data := append([]byte(nil), w.buffer...)
	w.buffer = w.buffer[:0]
	return data, w.localBytes, false
}

func (w *schedulerLogWriter) writeLocalAndBufferLocked(p []byte) (int, bool, error) {
	// Always write to local file first (primary storage)
	n, err := w.localFile.Write(p)
	if n > 0 {
		w.localBytes += int64(n)
		if len(w.buffer)+n >= logBufferSize {
			w.buffer = w.buffer[:0]
			return n, true, err
		}
		w.buffer = append(w.buffer, p[:n]...)
	}
	return n, false, err
}

// Flush sends pending scheduler log data to the coordinator.
func (w *schedulerLogWriter) Flush() error {
	data, localBytes, closed := w.takePendingData()
	if closed {
		return nil
	}

	w.streamMu.Lock()
	defer w.streamMu.Unlock()
	return w.flushDataLocked(data, localBytes)
}

func (w *schedulerLogWriter) flushDataLocked(data []byte, localBytes int64) error {
	// Check for permanent stream initialization failure
	if w.streamInitFailed {
		return nil // Silently fail - already logged on first failure
	}

	if w.streamedBytes >= localBytes {
		return nil
	}

	bufferStart := localBytes - int64(len(data))
	if len(data) == 0 || w.streamedBytes < bufferStart {
		return w.streamUnsentLocalFileLocked(localBytes)
	}

	offset := w.streamedBytes - bufferStart
	if offset >= int64(len(data)) {
		return nil
	}
	return w.sendSchedulerDataLocked(data[offset:])
}

func (w *schedulerLogWriter) ensureStreamLocked() error {
	if w.streamInitFailed || w.stream != nil {
		return nil
	}

	stream, err := w.streamer.openStream(w.ctx)
	if err != nil {
		if isLogStreamingNotConfigured(err) {
			w.streamInitFailed = true
			return nil
		}
		return err
	}
	w.stream = stream
	return nil
}

func (w *schedulerLogWriter) sendSchedulerDataLocked(data []byte) error {
	if err := w.ensureStreamLocked(); err != nil {
		return err
	}
	if w.streamInitFailed {
		return nil
	}

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
			if isLogStreamingNotConfigured(err) {
				w.streamInitFailed = true
				return nil
			}
			w.stream = nil
			return err
		}
		w.sequence = nextSeq
		w.streamedBytes += int64(len(chunkData))
	}

	return nil
}

func (w *schedulerLogWriter) streamUnsentLocalFileLocked(localBytes int64) error {
	if w.streamInitFailed || w.localFile == nil {
		return nil
	}
	if err := w.ensureStreamLocked(); err != nil {
		return err
	}
	if w.streamInitFailed || w.streamedBytes >= localBytes {
		return nil
	}

	// #nosec G304 -- the path belongs to the scheduler log file opened by the runtime.
	replayFile, err := os.Open(w.localFile.Name())
	if err != nil {
		return err
	}
	defer func() { _ = replayFile.Close() }()

	for w.streamedBytes < localBytes {
		chunkSize := int(min(localBytes-w.streamedBytes, int64(maxChunkSize)))
		data := make([]byte, chunkSize)
		n, readErr := replayFile.ReadAt(data, w.streamedBytes)
		if n > 0 {
			if err := w.sendSchedulerDataLocked(data[:n]); err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr == io.EOF && w.streamedBytes >= localBytes {
				return nil
			}
			return readErr
		}
	}
	return nil
}

// Close implements io.Closer - flushes remaining data and closes the stream
func (w *schedulerLogWriter) Close() error {
	w.closeOnce.Do(func() {
		defer close(w.closeDone)
		w.close()
	})
	<-w.closeDone
	return nil
}

func (w *schedulerLogWriter) close() {
	cancelTimer := time.AfterFunc(logStreamOperationTimeout, w.cancelStream)
	defer cancelTimer.Stop()
	defer w.cancelStream()

	w.mu.Lock()
	w.closed = true
	data := w.buffer
	w.buffer = nil
	localBytes := w.localBytes
	w.mu.Unlock()

	w.stopFlushLoop()

	w.streamMu.Lock()
	defer w.streamMu.Unlock()

	_ = w.flushDataLocked(data, localBytes)
	if w.streamedBytes < localBytes {
		_ = w.streamUnsentLocalFileLocked(localBytes)
	}

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

	w.streamer.unregisterSchedulerWriter(w)
}

func (w *schedulerLogWriter) CloseWithContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	done := make(chan error, 1)
	go func() {
		done <- w.Close()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		w.cancelStream()
		return ctx.Err()
	}
}
