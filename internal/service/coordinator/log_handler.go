// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// logHandler handles log streaming from workers
type logHandler struct {
	logDir           string
	attemptValidator func(context.Context, attemptIdentity) error
	// stepNames returns the names of the steps of a chunk's run, or none
	// when the run cannot be read.
	stepNames func(*coordinatorv1.LogChunk) []string

	// Active writers: streamKey -> writer
	writers   map[string]*streamLogWriter
	writersMu sync.Mutex
}

// streamLogWriter writes streamed logs directly to a single log file.
type streamLogWriter struct {
	file       *os.File
	path       string
	positioned bool
	size       int64
	mu         sync.Mutex
}

func (w *streamLogWriter) write(chunk *coordinatorv1.LogChunk) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if chunk.HasByteOffset() != w.positioned {
		return 0, errors.New("log stream write mode changed")
	}
	if !w.positioned {
		n, err := w.file.Write(chunk.Data)
		w.size += int64(n) // #nosec G115 -- n is non-negative and bounded by the input buffer
		return n, err
	}
	byteOffset := chunk.GetByteOffset()
	if byteOffset > math.MaxInt64 {
		return 0, errors.New("log chunk byte offset exceeds supported file size")
	}
	offset := int64(byteOffset) // #nosec G115 -- bounds checked above
	if offset > w.size {
		return 0, errors.New("log chunk byte offset exceeds current file size")
	}
	if uint64(len(chunk.Data)) > uint64(math.MaxInt64-offset) { // #nosec G115 -- buffer length is non-negative
		return 0, errors.New("log chunk exceeds supported file size")
	}
	n, err := w.file.WriteAt(chunk.Data, offset)
	if end := offset + int64(n); end > w.size { // #nosec G115 -- n is non-negative and bounded above
		w.size = end
	}
	return n, err
}

func (w *streamLogWriter) close(finalSize *uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	var truncateErr error
	if finalSize != nil {
		if *finalSize > math.MaxInt64 {
			truncateErr = errors.New("final log size exceeds supported file size")
		} else if int64(*finalSize) > w.size { // #nosec G115 -- bounds checked above
			truncateErr = errors.New("final log size exceeds current file size")
		} else {
			truncateErr = w.file.Truncate(int64(*finalSize)) // #nosec G115 -- bounds checked above
			if truncateErr == nil {
				w.size = int64(*finalSize) // #nosec G115 -- bounds checked above
			}
		}
	}
	return errors.Join(truncateErr, w.file.Sync(), w.file.Close())
}

// newLogHandler creates a new log handler
func newLogHandler(logDir string) *logHandler {
	return &logHandler{
		logDir:  logDir,
		writers: make(map[string]*streamLogWriter),
	}
}

// handleStream processes the log stream from a worker
func (h *logHandler) handleStream(stream coordinatorv1.CoordinatorService_StreamLogsServer) error {
	ctx := stream.Context()
	var chunksReceived uint64
	var bytesWritten uint64
	var validatedIdentity *attemptIdentity

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			// Stream completed - send response
			return stream.SendAndClose(&coordinatorv1.StreamLogsResponse{
				ChunksReceived: chunksReceived,
				BytesWritten:   bytesWritten,
			})
		}
		if err != nil {
			return fmt.Errorf("failed to receive chunk: %w", err)
		}

		chunksReceived++

		if h.attemptValidator != nil {
			identity, identityErr := logChunkIdentity(chunk)
			if identityErr != nil {
				return status.Error(codes.InvalidArgument, identityErr.Error())
			}
			if validatedIdentity != nil {
				if identity != *validatedIdentity {
					return status.Error(codes.FailedPrecondition, "log stream attempt identity changed")
				}
			} else {
				if err := h.attemptValidator(ctx, identity); err != nil {
					return err
				}
				validatedIdentity = &identity
			}
		}

		// Handle final marker
		if chunk.IsFinal {
			if err := h.closeWriter(chunk); err != nil {
				return fmt.Errorf("failed to finalize log file: %w", err)
			}
			continue
		}

		// Skip empty data
		if len(chunk.Data) == 0 {
			continue
		}

		// Get or create writer for this stream
		writer, err := h.getOrCreateWriter(chunk)
		if err != nil {
			return fmt.Errorf("failed to create writer: %w", err)
		}

		// Write the data using thread-safe method
		n, err := writer.write(chunk)
		if err != nil {
			return fmt.Errorf("failed to write data: %w", err)
		}
		if n > 0 {
			bytesWritten += uint64(n) // #nosec G115 -- n is non-negative from successful Write
		}
	}
}

// streamKey creates a unique key for identifying a log stream.
// Includes AttemptId to prevent collisions during retry scenarios.
func (h *logHandler) streamKey(chunk *coordinatorv1.LogChunk) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s",
		chunk.DagName,
		chunk.DagRunId,
		chunk.AttemptId,
		chunk.StepName,
		chunk.StreamType.String(),
	)
}

// getOrCreateWriter returns an existing writer or creates a new one
func (h *logHandler) getOrCreateWriter(chunk *coordinatorv1.LogChunk) (*streamLogWriter, error) {
	key := h.streamKey(chunk)

	h.writersMu.Lock()
	defer h.writersMu.Unlock()

	// Check if writer already exists
	if w, ok := h.writers[key]; ok {
		if w.positioned != chunk.HasByteOffset() {
			return nil, errors.New("log stream write mode changed")
		}
		return w, nil
	}

	// Create the log file path
	logPath := h.logFilePath(chunk)

	// Ensure directory exists
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	var file *os.File
	var err error
	if chunk.HasByteOffset() {
		file, err = fileutil.OpenOrCreateFileForRandomWrite(logPath)
	} else {
		file, err = fileutil.OpenOrCreateFileWithoutSync(logPath)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to inspect log file: %w", err)
	}

	w := &streamLogWriter{
		file:       file,
		path:       logPath,
		positioned: chunk.HasByteOffset(),
		size:       info.Size(),
	}

	h.writers[key] = w
	return w, nil
}

// closeWriter closes and removes a writer.
func (h *logHandler) closeWriter(chunk *coordinatorv1.LogChunk) error {
	key := h.streamKey(chunk)

	h.writersMu.Lock()
	w, ok := h.writers[key]
	if ok {
		delete(h.writers, key)
	}
	h.writersMu.Unlock()
	if !ok {
		return nil
	}
	if !w.positioned {
		return w.close(nil)
	}
	finalSize := chunk.GetByteOffset()
	return w.close(&finalSize)
}

// logFilePath generates the log file path following the existing pattern.
// Path format: {logDir}/{dagName}/{dagRunID}/{attemptID}/{stepName}.{ext}
func (h *logHandler) logFilePath(chunk *coordinatorv1.LogChunk) string {
	dagName := chunk.DagName
	dagRunID := chunk.DagRunId

	// For sub-DAGs, store under root DAG's directory
	if chunk.RootDagRunId != "" {
		dagName = chunk.RootDagRunName
		dagRunID = chunk.RootDagRunId
	}

	attemptDir := chunk.AttemptId
	if attemptDir == "" {
		attemptDir = dagRunID
	}

	// For scheduler logs, use just "scheduler.log" without stepName prefix
	if chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER {
		return schedulerLogPath(h.logDir, dagName, dagRunID, attemptDir)
	}
	steps := func() []string {
		if h.stepNames == nil {
			return nil
		}
		return h.stepNames(chunk)
	}
	return stepLogPath(h.logDir, dagName, dagRunID, attemptDir, chunk.StepName, StreamTypeToExtension(chunk.StreamType), steps)
}

// schedulerLogFile names the scheduler's log in an attempt's directory.
const schedulerLogFile = "scheduler.log"

// stepLogPath returns the coordinator file of a step's log of an attempt.
// steps returns the names of the run's steps (see attemptLogPath).
func stepLogPath(logDir, dagName, dagRunID, attemptID, step, ext string, steps func() []string) string {
	return attemptLogPath(logDir, dagName, dagRunID, attemptID, stepLogFile(step, ext), legacyName(step)+"."+ext, func() bool {
		names := steps()
		_, owned := fileutil.LegacySafeName(step, names)
		return owned && len(names) > 0
	})
}

// schedulerLogPath returns the coordinator file of the scheduler's log of an
// attempt.
func schedulerLogPath(logDir, dagName, dagRunID, attemptID string) string {
	return attemptLogPath(logDir, dagName, dagRunID, attemptID, schedulerLogFile, schedulerLogFile, func() bool { return true })
}

// attemptLogPath returns the coordinator file named file in an attempt's
// directory. Released versions named the directory, and the file as
// legacyFile, by SafeName. A run in flight across an upgrade keeps writing to
// such a file when it exists and the new one does not, if owned reports that
// no other step of the run had the same SafeName. owned is called only then.
func attemptLogPath(logDir, dagName, dagRunID, attemptID, file, legacyFile string, owned func() bool) string {
	path := attemptLogFile(logDir, dagName, dagRunID, attemptID, file)
	legacy := filepath.Join(legacyName(dagName), legacyName(dagRunID), legacyName(attemptID), legacyFile)
	if filepath.Join(logDir, legacy) == path {
		return path
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return path
	}
	rel, ok := fileutil.LegacyEntry(logDir, legacy)
	if !ok || !owned() {
		return path
	}
	return filepath.Join(logDir, rel)
}

// attemptLogFile returns the coordinator file named file in the directory
// this version gives an attempt's logs.
func attemptLogFile(logDir, dagName, dagRunID, attemptID, file string) string {
	return filepath.Join(logDir, fileutil.SafeKey(dagName), fileutil.SafeKey(dagRunID), fileutil.SafeKey(attemptID), file)
}

// stepLogFile names the file of a step's log with extension ext.
func stepLogFile(step, ext string) string {
	return fileutil.SafeKey(step) + "." + ext
}

func legacyName(name string) string {
	legacy, _ := fileutil.LegacySafeName(name, nil)
	return legacy
}

// StreamTypeToExtension returns the file extension for a given stream type.
func StreamTypeToExtension(streamType coordinatorv1.LogStreamType) string {
	switch streamType {
	case coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT:
		return "stdout.log"
	case coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR:
		return "stderr.log"
	case coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER:
		return "scheduler.log"
	case coordinatorv1.LogStreamType_LOG_STREAM_TYPE_UNSPECIFIED:
		return "log"
	}
	return "log"
}

// Close closes all open writers using the provided context for logging.
// This preserves trace context for observability.
func (h *logHandler) Close(ctx context.Context) {
	h.writersMu.Lock()
	defer h.writersMu.Unlock()

	for _, w := range h.writers {
		if err := w.close(nil); err != nil {
			logger.Warn(ctx, "Failed to close log file",
				slog.String("path", w.path),
				slog.String("error", err.Error()))
		}
	}
	h.writers = make(map[string]*streamLogWriter)
}
