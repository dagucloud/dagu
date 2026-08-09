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

// flushThreshold is the number of buffered bytes after which artifact data is flushed.
const flushThreshold = 65536

// logHandler handles log streaming from workers
type logHandler struct {
	logDir           string
	attemptValidator func(context.Context, remoteAttemptIdentity) error

	// Active writers: streamKey -> writer
	writers   map[string]*streamLogWriter
	writersMu sync.Mutex
}

// streamLogWriter writes streamed logs directly to a single log file.
type streamLogWriter struct {
	file       *os.File
	path       string
	positioned bool
	mu         sync.Mutex
}

func (w *streamLogWriter) write(chunk *coordinatorv1.LogChunk) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if chunk.UseByteOffset != w.positioned {
		return 0, errors.New("log stream write mode changed")
	}
	if !w.positioned {
		return w.file.Write(chunk.Data)
	}
	if chunk.ByteOffset > math.MaxInt64 {
		return 0, errors.New("log chunk byte offset exceeds supported file size")
	}
	return w.file.WriteAt(chunk.Data, int64(chunk.ByteOffset)) // #nosec G115 -- bounds checked above
}

func (w *streamLogWriter) close(finalSize *uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	var truncateErr error
	if finalSize != nil {
		if *finalSize > math.MaxInt64 {
			truncateErr = errors.New("final log size exceeds supported file size")
		} else {
			truncateErr = w.file.Truncate(int64(*finalSize)) // #nosec G115 -- bounds checked above
		}
	}
	return errors.Join(truncateErr, w.file.Sync(), w.file.Close())
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
	var validatedIdentity *remoteAttemptIdentity

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
		if w.positioned != chunk.UseByteOffset {
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
	if chunk.UseByteOffset {
		file, err = fileutil.OpenOrCreateFileForRandomWrite(logPath)
	} else {
		file, err = fileutil.OpenOrCreateFileWithoutSync(logPath)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	w := &streamLogWriter{
		file:       file,
		path:       logPath,
		positioned: chunk.UseByteOffset,
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
	return w.close(&chunk.ByteOffset)
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
		if err := w.close(nil); err != nil {
			logger.Warn(ctx, "Failed to close log file",
				slog.String("path", w.path),
				slog.String("error", err.Error()))
		}
	}
	h.writers = make(map[string]*streamLogWriter)
}
