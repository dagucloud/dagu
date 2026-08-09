// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// flushThreshold is the number of buffered bytes after which artifact data is flushed.
const flushThreshold = 65536

// logHandler handles log streaming from workers
type logHandler struct {
	logDir         string
	ownerID        string
	ownerValidator func(context.Context, string) (bool, error)

	// Active writers: streamKey -> writer
	writers   map[string]*streamLogWriter
	writersMu sync.Mutex
}

// streamLogWriter writes streamed logs directly to a single log file.
type streamLogWriter struct {
	file *os.File
	path string
	mu   sync.Mutex
}

// write appends data to the log file.
func (w *streamLogWriter) write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.file.Write(data)
}

// close syncs and closes the file.
// Errors are logged but not returned since this is typically called during cleanup.
func (w *streamLogWriter) close(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Sync(); err != nil {
		logger.Warn(ctx, "Failed to sync log file",
			slog.String("path", w.path),
			slog.String("error", err.Error()))
	}
	if err := w.file.Close(); err != nil {
		logger.Warn(ctx, "Failed to close log file",
			slog.String("path", w.path),
			slog.String("error", err.Error()))
	}
}

// logWriter manages buffered artifact writes to a single file.
type logWriter struct {
	file            *os.File
	writer          *bufio.Writer
	path            string
	bytesSinceFlush uint64 // Track bytes written since last flush
	mu              sync.Mutex
}

// write writes data to the buffer and flushes if threshold is exceeded.
// Returns the number of bytes written and any error.
func (w *logWriter) write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n, err := w.writer.Write(data)
	if err != nil {
		return n, err
	}
	if n > 0 {
		w.bytesSinceFlush += uint64(n)
	}

	// Flush periodically to ensure data is visible
	if w.bytesSinceFlush >= flushThreshold {
		if err := w.writer.Flush(); err != nil {
			return n, fmt.Errorf("failed to flush log buffer for %s: %w", w.path, err)
		}
		w.bytesSinceFlush = 0
	}

	return n, nil
}

// close flushes the buffer, syncs to disk, and closes the file.
func (w *logWriter) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return errors.Join(w.writer.Flush(), w.file.Sync(), w.file.Close())
}

// newLogHandler creates a new log handler
func newLogHandler(logDir string, ownerID ...string) *logHandler {
	var expectedOwner string
	if len(ownerID) > 0 {
		expectedOwner = ownerID[0]
	}
	return &logHandler{
		logDir:  logDir,
		ownerID: expectedOwner,
		writers: make(map[string]*streamLogWriter),
	}
}

// handleStream processes the log stream from a worker
func (h *logHandler) handleStream(stream coordinatorv1.CoordinatorService_StreamLogsServer) error {
	ctx := stream.Context()
	var chunksReceived uint64
	var bytesWritten uint64
	var validatedOwnerID string
	var ownerValidated bool

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

		if ownerValidated {
			if chunk.OwnerCoordinatorId != validatedOwnerID {
				return status.Error(codes.FailedPrecondition, "log chunk sent to non-owner coordinator")
			}
		} else if h.ownerValidator != nil {
			accepted, err := h.ownerValidator(ctx, chunk.OwnerCoordinatorId)
			if err != nil {
				return status.Error(codes.Internal, "failed to validate log chunk owner: "+err.Error())
			}
			if !accepted {
				return status.Error(codes.FailedPrecondition, "log chunk sent to non-owner coordinator")
			}
			validatedOwnerID = chunk.OwnerCoordinatorId
			ownerValidated = true
		} else if h.ownerID != "" && chunk.OwnerCoordinatorId != h.ownerID {
			return status.Error(codes.FailedPrecondition, "log chunk sent to non-owner coordinator")
		} else if h.ownerID != "" {
			validatedOwnerID = chunk.OwnerCoordinatorId
			ownerValidated = true
		}

		// Handle final marker
		if chunk.IsFinal {
			h.closeWriter(ctx, chunk)
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
		n, err := writer.write(chunk.Data)
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
		return w, nil
	}

	// Create the log file path
	logPath := h.logFilePath(chunk)

	// Ensure directory exists
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Open or create the file
	file, err := fileutil.OpenOrCreateFileWithoutSync(logPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	w := &streamLogWriter{
		file: file,
		path: logPath,
	}

	h.writers[key] = w
	return w, nil
}

// closeWriter closes and removes a writer
func (h *logHandler) closeWriter(ctx context.Context, chunk *coordinatorv1.LogChunk) {
	key := h.streamKey(chunk)

	h.writersMu.Lock()
	defer h.writersMu.Unlock()

	if w, ok := h.writers[key]; ok {
		w.close(ctx)
		delete(h.writers, key)
	}
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

	ext := StreamTypeToExtension(chunk.StreamType)

	// For scheduler logs, use just "scheduler.log" without stepName prefix
	var filename string
	if chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER {
		filename = "scheduler.log"
	} else {
		filename = fmt.Sprintf("%s.%s", fileutil.SafeName(chunk.StepName), ext)
	}

	return filepath.Join(
		h.logDir,
		fileutil.SafeName(dagName),
		fileutil.SafeName(dagRunID),
		fileutil.SafeName(attemptDir),
		filename,
	)
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
		w.close(ctx)
	}
	h.writers = make(map[string]*streamLogWriter)
}
