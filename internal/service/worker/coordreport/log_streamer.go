// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordreport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
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
	maxRetainedStepLogSize    = 16 * 1024 * 1024
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
	claimKey  string
	rootRef   ir.DAGRunRef
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
	rootRef ir.DAGRunRef,
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

// SetClaimKey binds streamed logs to the task claim that authorizes the run.
func (s *LogStreamer) SetClaimKey(claimKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimKey = claimKey
}

// getAttemptID returns the current attemptID
func (s *LogStreamer) getAttemptID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.attemptID
}

func (s *LogStreamer) getAttemptKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.claimKey != "" {
		return s.claimKey
	}
	root := s.rootRef
	if root.Zero() {
		root = ir.NewDAGRunRef(s.dagName, s.dagRunID)
	}
	return ir.GenerateAttemptKey(root.Name, root.ID, s.dagName, s.dagRunID, s.attemptID)
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
		parentCtx:  ctx,
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
		parentCtx:     ctx,
		ctx:           streamCtx,
		cancel:        cancel,
		streamer:      s,
		localFile:     localFile,
		buffer:        make([]byte, 0, logBufferSize),
		flushStop:     make(chan struct{}),
		flushFinished: make(chan struct{}),
		flushWake:     make(chan struct{}, 1),
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
			AttemptKey:         s.getAttemptKey(),
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
		AttemptKey:         s.getAttemptKey(),
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
	parentCtx         context.Context
	ctx               context.Context
	cancel            context.CancelFunc
	streamer          *LogStreamer
	stepName          string
	streamType        int
	buffer            []byte
	remoteBuffer      []byte
	sequence          uint64
	byteOffset        uint64
	remoteSent        int
	remoteChunks      uint64
	stream            coordinatorv1.CoordinatorService_StreamLogsClient
	mu                sync.Mutex
	closed            bool
	streamingDisabled bool
	remoteTruncated   bool
	pendingSince      time.Time
}

// Write implements io.Writer
func (w *stepLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, io.ErrClosedPipe
	}

	if len(w.buffer) == 0 && w.remoteSent == len(w.remoteBuffer) {
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

	if w.closed || (len(w.buffer) == 0 && w.remoteSent == len(w.remoteBuffer)) || time.Since(w.pendingSince) < logFlushInterval {
		return nil
	}
	return w.flushLocked()
}

// flushLocked sends buffered data to coordinator.
// Implements chunk splitting for large buffers to stay within gRPC message size limits.
// Sent data remains buffered until the coordinator acknowledges the stream.
func (w *stepLogWriter) flushLocked() error {
	if len(w.buffer) > 0 {
		w.streamer.mirrorToSchedulerLog(w.buffer)
		w.remoteBuffer = append(w.remoteBuffer, w.buffer...)
		w.buffer = w.buffer[:0]
	}
	if len(w.remoteBuffer) == 0 {
		w.pendingSince = time.Time{}
		return nil
	}
	if w.streamingDisabled {
		w.remoteBuffer = nil
		w.remoteSent = 0
		w.remoteChunks = 0
		w.pendingSince = time.Time{}
		return nil
	}

	for w.remoteSent < len(w.remoteBuffer) {
		chunkSize := min(len(w.remoteBuffer)-w.remoteSent, maxChunkSize)

		// Copy chunk data to avoid corruption if Send buffers the message
		chunkData := make([]byte, chunkSize)
		copy(chunkData, w.remoteBuffer[w.remoteSent:w.remoteSent+chunkSize])

		// Initialize stream if needed
		if w.stream == nil {
			var stream coordinatorv1.CoordinatorService_StreamLogsClient
			err := w.withOperationTimeout(func() error {
				var err error
				stream, err = w.streamer.openStream(w.ctx)
				return err
			})
			if err != nil {
				w.handleStreamFailureLocked(err)
				if isLogStreamingNotConfigured(err) {
					w.remoteBuffer = nil
					w.pendingSince = time.Time{}
					return nil
				}
				w.capRemoteBufferLocked()
				w.pendingSince = time.Now()
				return err
			}
			w.stream = stream
		}

		nextSeq := w.sequence + w.remoteChunks + 1
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
			AttemptKey:         w.streamer.getAttemptKey(),
		}
		chunk.SetByteOffset(w.byteOffset + uint64(w.remoteSent)) // #nosec G115 -- remoteSent is non-negative

		if err := w.withOperationTimeout(func() error {
			return w.stream.Send(chunk)
		}); err != nil {
			w.handleStreamFailureLocked(err)
			if isLogStreamingNotConfigured(err) {
				w.remoteBuffer = nil
				w.pendingSince = time.Time{}
				return nil
			}
			w.capRemoteBufferLocked()
			w.pendingSince = time.Now()
			return err
		}
		w.remoteSent += chunkSize
		w.remoteChunks++
	}

	w.pendingSince = time.Time{}
	if len(w.remoteBuffer) >= maxRetainedStepLogSize {
		return w.checkpointLocked()
	}
	return nil
}

func (w *stepLogWriter) checkpointLocked() error {
	if w.stream == nil {
		return nil
	}
	err := w.withOperationTimeout(func() error {
		_, err := w.stream.CloseAndRecv()
		return err
	})
	w.stream = nil
	if err != nil {
		w.handleStreamFailureLocked(err)
		if isLogStreamingNotConfigured(err) {
			w.remoteBuffer = nil
			w.remoteSent = 0
			w.remoteChunks = 0
			return nil
		}
		w.capRemoteBufferLocked()
		w.pendingSince = time.Now()
		return err
	}
	w.byteOffset += uint64(len(w.remoteBuffer)) // #nosec G115 -- buffer length is non-negative
	w.sequence += w.remoteChunks
	w.remoteBuffer = nil
	w.remoteSent = 0
	w.remoteChunks = 0
	return nil
}

func (w *stepLogWriter) handleStreamFailureLocked(err error) {
	w.cancelStream()
	w.stream = nil
	w.ctx, w.cancel = context.WithCancel(w.parentCtx)
	w.remoteSent = 0
	w.remoteChunks = 0
	if isLogStreamingNotConfigured(err) {
		w.streamingDisabled = true
		return
	}
	logger.Warn(w.ctx, "Step log stream interrupted; buffered output will retry",
		tag.Error(err),
		tag.Step(w.stepName),
	)
}

func (w *stepLogWriter) capRemoteBufferLocked() {
	if len(w.remoteBuffer) <= maxRetainedStepLogSize {
		return
	}
	w.remoteBuffer = append([]byte(nil), w.remoteBuffer[len(w.remoteBuffer)-maxRetainedStepLogSize:]...)
	// Retained output stays contiguous after the acknowledged file prefix.
	w.remoteSent = 0
	w.remoteChunks = 0
	if w.remoteTruncated {
		return
	}
	w.remoteTruncated = true
	logger.Warn(w.ctx, "Buffered step log output truncated during coordinator outage",
		tag.Step(w.stepName),
	)
}

func (w *stepLogWriter) cancelStream() {
	if w.cancel != nil {
		w.cancel()
	}
}

func (w *stepLogWriter) withOperationTimeout(operation func() error) error {
	cancel := w.cancel
	cancelTimer := time.AfterFunc(logStreamOperationTimeout, cancel)
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

	retryCtx, cancel := context.WithTimeout(w.parentCtx, finalDeliveryRetryTimeout)
	defer cancel()
	w.parentCtx = retryCtx

	return backoff.Retry(retryCtx, func(context.Context) error {
		if err := w.flushLocked(); err != nil {
			return err
		}
		return w.finishLocked()
	}, finalDeliveryRetryPolicy(), isRetryableStepLogError)
}

func (w *stepLogWriter) finishLocked() error {
	if w.streamingDisabled {
		return nil
	}
	if w.stream == nil && w.sequence == 0 && len(w.remoteBuffer) == 0 {
		return nil
	}
	if w.stream == nil {
		var stream coordinatorv1.CoordinatorService_StreamLogsClient
		err := w.withOperationTimeout(func() error {
			var err error
			stream, err = w.streamer.openStream(w.ctx)
			return err
		})
		if err != nil {
			w.handleStreamFailureLocked(err)
			if isLogStreamingNotConfigured(err) {
				return nil
			}
			return err
		}
		w.stream = stream
	}

	nextSeq := w.sequence + w.remoteChunks + 1
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
		AttemptKey:         w.streamer.getAttemptKey(),
	}
	finalChunk.SetByteOffset(w.byteOffset + uint64(len(w.remoteBuffer))) // #nosec G115 -- buffer length is non-negative
	if err := w.withOperationTimeout(func() error { return w.stream.Send(finalChunk) }); err != nil {
		w.handleStreamFailureLocked(err)
		if isLogStreamingNotConfigured(err) {
			return nil
		}
		return err
	}

	err := w.withOperationTimeout(func() error {
		_, err := w.stream.CloseAndRecv()
		return err
	})
	w.stream = nil
	if err != nil {
		w.handleStreamFailureLocked(err)
		if isLogStreamingNotConfigured(err) {
			return nil
		}
		return err
	}
	w.sequence = nextSeq
	w.byteOffset += uint64(len(w.remoteBuffer)) // #nosec G115 -- buffer length is non-negative
	w.remoteBuffer = nil
	w.remoteSent = 0
	w.remoteChunks = 0
	return nil
}

func isRetryableStepLogError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded
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
	parentCtx         context.Context
	ctx               context.Context
	cancel            context.CancelFunc
	streamer          *LogStreamer
	localFile         *os.File
	buffer            []byte
	sequence          uint64
	localBytes        int64
	streamedBytes     int64
	acknowledgedBytes int64
	stream            coordinatorv1.CoordinatorService_StreamLogsClient
	mu                sync.Mutex
	closed            bool
	streamMu          sync.Mutex
	streamInitFailed  bool // Tracks permanent stream initialization failure
	flushStop         chan struct{}
	flushFinished     chan struct{}
	flushWake         chan struct{}
	flushStopOnce     sync.Once
	closeOnce         sync.Once
	closeErr          error
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

	var stream coordinatorv1.CoordinatorService_StreamLogsClient
	err := w.withOperationTimeout(func() error {
		var err error
		stream, err = w.streamer.openStream(w.ctx)
		return err
	})
	if err != nil {
		if isLogStreamingNotConfigured(err) {
			w.streamInitFailed = true
			return nil
		}
		w.resetStreamLocked()
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
			AttemptKey:         w.streamer.getAttemptKey(),
		}
		chunk.SetByteOffset(uint64(w.streamedBytes)) // #nosec G115 -- streamedBytes is non-negative

		if err := w.withOperationTimeout(func() error { return w.stream.Send(chunk) }); err != nil {
			if isLogStreamingNotConfigured(err) {
				w.streamInitFailed = true
				return nil
			}
			w.resetStreamLocked()
			return err
		}
		w.sequence = nextSeq
		w.streamedBytes += int64(len(chunkData))
	}

	return nil
}

func (w *schedulerLogWriter) resetStreamLocked() {
	w.cancelStream()
	w.stream = nil
	w.streamedBytes = w.acknowledgedBytes
	w.ctx, w.cancel = context.WithCancel(w.parentCtx)
}

func (w *schedulerLogWriter) withOperationTimeout(operation func() error) error {
	cancel := w.cancel
	timer := time.AfterFunc(logStreamOperationTimeout, cancel)
	err := operation()
	if !timer.Stop() && err == nil {
		w.resetStreamLocked()
		return context.DeadlineExceeded
	}
	return err
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

// Close implements io.Closer.
func (w *schedulerLogWriter) Close() error {
	w.streamMu.Lock()
	parentCtx := w.parentCtx
	w.streamMu.Unlock()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(parentCtx), finalDeliveryRetryTimeout)
	defer cancel()
	return w.CloseWithContext(ctx)
}

func (w *schedulerLogWriter) close(ctx context.Context) error {
	w.mu.Lock()
	w.closed = true
	data := w.buffer
	w.buffer = nil
	localBytes := w.localBytes
	w.mu.Unlock()

	stopCancel := context.AfterFunc(ctx, w.cancelStream)
	defer stopCancel()
	cancelTimer := time.AfterFunc(logStreamOperationTimeout, w.cancelStream)
	w.stopFlushLoop()
	cancelTimer.Stop()

	w.streamMu.Lock()
	defer w.streamMu.Unlock()
	defer w.cancelStream()
	defer w.streamer.unregisterSchedulerWriter(w)

	w.parentCtx = ctx
	if w.stream == nil {
		w.cancelStream()
		w.ctx, w.cancel = context.WithCancel(ctx)
		w.streamedBytes = w.acknowledgedBytes
	}

	return backoff.Retry(ctx, func(context.Context) error {
		if err := w.flushDataLocked(data, localBytes); err != nil {
			return err
		}
		if w.streamedBytes < localBytes {
			if err := w.streamUnsentLocalFileLocked(localBytes); err != nil {
				return err
			}
		}
		if w.stream == nil {
			return nil
		}

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
			AttemptKey:         w.streamer.getAttemptKey(),
		}
		finalChunk.SetByteOffset(uint64(localBytes)) // #nosec G115 -- localBytes is non-negative
		if err := w.withOperationTimeout(func() error { return w.stream.Send(finalChunk) }); err != nil {
			if isLogStreamingNotConfigured(err) {
				w.streamInitFailed = true
				return nil
			}
			w.resetStreamLocked()
			return err
		}
		if err := w.withOperationTimeout(func() error {
			_, err := w.stream.CloseAndRecv()
			return err
		}); err != nil {
			if isLogStreamingNotConfigured(err) {
				w.streamInitFailed = true
				return nil
			}
			w.resetStreamLocked()
			return err
		}
		w.sequence = nextSeq
		w.acknowledgedBytes = localBytes
		w.streamedBytes = localBytes
		w.stream = nil
		return nil
	}, finalDeliveryRetryPolicy(), isRetryableStepLogError)
}

func (w *schedulerLogWriter) CloseWithContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	w.closeOnce.Do(func() {
		w.closeErr = w.close(ctx)
	})
	return w.closeErr
}
