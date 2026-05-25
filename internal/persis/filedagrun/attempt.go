// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filedagrun

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/eventstore"
)

// Error definitions for common issues
var (
	ErrStatusFileOpen    = errors.New("status file already open")
	ErrStatusFileNotOpen = errors.New("status file not open")
	ErrReadFailed        = errors.New("failed to read status file")
	ErrWriteFailed       = errors.New("failed to write to status file")
	ErrCompactFailed     = errors.New("failed to compact status file")
	ErrContextCanceled   = errors.New("operation canceled by context")
)

// DAGDefinition is the name of the file where the DAG definition is stored.
const DAGDefinition = "dag.json"

// OutputsFile is the name of the file where collected step outputs are stored.
const OutputsFile = "outputs.json"

// MessagesDir is the directory where per-step LLM messages are stored.
const MessagesDir = "messages"

// CancelRequestedFlag is a special flag used to indicate that a cancel request has been made.
const CancelRequestedFlag = "CANCEL_REQUESTED"

var _ exec.DAGRunAttempt = (*Attempt)(nil)

// Attempt manages an append-only status file with read, write, and compaction capabilities.
// It provides thread-safe operations and supports metrics collection.
type Attempt struct {
	id                   string                              // Attempt ID, extracted from the file path
	file                 string                              // Path to the status file
	writer               *Writer                             // Writer for appending status updates
	mu                   sync.RWMutex                        // Mutex for thread safety
	cache                *fileutil.Cache[*exec.DAGRunStatus] // Optional cache for read operations
	isClosing            atomic.Bool                         // Flag to prevent writes during Close/Compact
	dag                  *core.DAG                           // DAG associated with the status file
	lastEmittedEventType eventstore.EventType
}

// AttemptOption defines a functional option for configuring an Attempt.
type AttemptOption func(*Attempt)

// WithDAG sets the DAG associated with the Attempt.
// This allows the Attempt to store DAG metadata alongside the status data.
func WithDAG(dag *core.DAG) AttemptOption {
	return func(att *Attempt) {
		att.dag = dag
	}
}

// ID implements models.DAGRunAttempt.
func (att *Attempt) ID() string {
	return att.id
}

// SetDAG sets the DAG for this attempt. Must be called before Open for DAG to be persisted.
func (att *Attempt) SetDAG(dag *core.DAG) {
	att.dag = dag
}

// NewAttempt creates a new Run for the specified file.
func NewAttempt(file string, cache *fileutil.Cache[*exec.DAGRunStatus], opts ...AttemptOption) (*Attempt, error) {
	dirName := filepath.Base(filepath.Dir(file))
	matches := reAttemptDir.FindStringSubmatch(strings.TrimPrefix(dirName, "."))
	if len(matches) != 3 {
		return nil, fmt.Errorf("invalid file path for run data: %s", file)
	}
	att := &Attempt{id: matches[2], file: file, cache: cache}
	for _, opt := range opts {
		opt(att)
	}

	return att, nil
}

// Exists returns true if the status file exists.
func (att *Attempt) Exists() bool {
	_, err := os.Stat(att.file)
	return err == nil || !os.IsNotExist(err)
}

// ModTime returns the last modification time of the status file.
// This is used to determine when the file was last updated.
func (att *Attempt) ModTime() (time.Time, error) {
	info, err := os.Stat(att.file)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// ReadDAG implements models.DAGRunAttempt.
func (att *Attempt) ReadDAG(_ context.Context) (*core.DAG, error) {
	// Determine the path to the DAG definition file
	dir := filepath.Dir(att.file)
	dagFile := filepath.Join(dir, DAGDefinition)

	// Check if the file exists
	if _, err := os.Stat(dagFile); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("DAG definition file not found: %w", err)
		}
		return nil, fmt.Errorf("failed to access DAG definition file: %w", err)
	}

	// Read the file
	data, err := fileutil.ReadFile(dagFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read DAG definition file: %w", err)
	}

	// Parse the JSON data
	var dag core.DAG
	if err := json.Unmarshal(data, &dag); err != nil {
		return nil, fmt.Errorf("failed to unmarshal DAG definition: %w", err)
	}

	return &dag, nil
}

// Open initializes the status file for writing. It returns an error if the file is already open.
// The context can be used to cancel the operation.
func (att *Attempt) Open(ctx context.Context) error {
	att.mu.Lock()
	defer att.mu.Unlock()

	if att.writer != nil {
		return fmt.Errorf("status file already open: %w", ErrStatusFileOpen)
	}

	// Ensure the directory exists
	dir := filepath.Dir(att.file)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// If it's a new file, create it
	if att.dag != nil {
		dagJSON, err := json.Marshal(att.dag)
		if err != nil {
			return fmt.Errorf("failed to marshal DAG definition: %w", err)
		}
		if err := fileutil.WriteFileAtomic(filepath.Join(dir, DAGDefinition), dagJSON, 0600); err != nil {
			return fmt.Errorf("failed to write DAG definition: %w", err)
		}
	}

	// Create the per-run work directory so steps can use DAG_RUN_WORK_DIR immediately
	if err := os.MkdirAll(att.WorkDir(), 0750); err != nil {
		return fmt.Errorf("failed to create work directory %s: %w", att.WorkDir(), err)
	}

	logger.Debug(ctx, "Initializing status file",
		tag.File(att.file))

	writer := NewWriter(att.file)

	if err := writer.Open(); err != nil {
		return fmt.Errorf("failed to open writer: %w", err)
	}

	att.writer = writer
	att.lastEmittedEventType = att.lastPersistedEventTypeLocked(ctx)
	return nil
}

// Write adds a new status to the file. It returns an error if the file is not open
// or is currently being closed. The context can be used to cancel the operation.
func (att *Attempt) Write(ctx context.Context, status exec.DAGRunStatus) error {
	// Check if we're closing before acquiring the mutex to reduce contention
	if att.isClosing.Load() {
		return fmt.Errorf("cannot write while file is closing: %w", ErrStatusFileNotOpen)
	}

	att.mu.Lock()
	defer att.mu.Unlock()

	if att.writer == nil {
		return fmt.Errorf("status file not open: %w", ErrStatusFileNotOpen)
	}

	if writeErr := att.writer.Write(ctx, status); writeErr != nil {
		return fmt.Errorf("failed to write status: %w", ErrWriteFailed)
	}

	// Invalidate cache after successful write
	if att.cache != nil {
		att.cache.Invalidate(att.file)
	}

	nextEventType, _, err := eventstore.EmitPersistedStatusTransitionFromContext(
		ctx,
		att.lastEmittedEventType,
		&status,
		att.eventData(),
	)
	if err != nil {
		logger.Warn(ctx, "Failed to emit DAG-run event", tag.Error(err))
	} else {
		att.lastEmittedEventType = nextEventType
	}

	return nil
}

// Close properly closes the status file, performs compaction, and invalidates the cache.
// It's safe to call Close multiple times. The context can be used to cancel the operation.
func (att *Attempt) Close(ctx context.Context) error {
	// Set the closing flag to prevent new writes
	att.isClosing.Store(true)
	defer att.isClosing.Store(false)

	att.mu.Lock()
	defer att.mu.Unlock()

	if att.writer == nil {
		return nil
	}

	// Create a copy to avoid nil dereference in deferred function
	w := att.writer
	att.writer = nil

	// Close the writer before compaction so Windows can replace the file.
	if closeErr := w.Close(ctx); closeErr != nil {
		return fmt.Errorf("failed to close writer: %w", closeErr)
	}

	// Attempt to compact the file
	if compactErr := att.compactLocked(ctx); compactErr != nil {
		logger.Warn(ctx, "Failed to compact file during close",
			tag.Error(compactErr))
		// Continue with close even if compaction fails
	}

	// Invalidate the cache
	if att.cache != nil {
		att.cache.Invalidate(att.file)
	}

	return nil
}

// Compact performs file compaction to optimize storage and read performance.
// It's safe to call while the file is open or closed. The context can be used to cancel the operation.
func (att *Attempt) Compact(ctx context.Context) error {
	// Set the closing flag to prevent new writes during compaction
	att.isClosing.Store(true)
	defer att.isClosing.Store(false)

	att.mu.Lock()
	defer att.mu.Unlock()

	return att.compactLocked(ctx)
}

// compactLocked performs actual compaction with the lock already held
func (att *Attempt) compactLocked(ctx context.Context) (retErr error) {
	reopenWriter := att.writer
	if reopenWriter != nil {
		att.writer = nil
		if err := reopenWriter.close(); err != nil {
			att.writer = reopenWriter
			return fmt.Errorf("failed to close writer before compaction: %w", err)
		}
		defer func() {
			if err := reopenWriter.Open(); err != nil {
				reopenErr := fmt.Errorf("failed to reopen writer after compaction: %w", err)
				if retErr != nil {
					retErr = errors.Join(retErr, reopenErr)
					return
				}
				retErr = reopenErr
				return
			}
			att.writer = reopenWriter
		}()
	}

	status, err := att.parseLocked(ctx)
	if err == io.EOF {
		return nil // Empty file, nothing to compact
	}
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrCompactFailed, att.file, err)
	}

	// Create a temporary file in the same directory
	dir := filepath.Dir(att.file)
	tempFile, err := os.CreateTemp(dir, "compact_*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempFilePath := tempFile.Name()

	// Close the temp file so we can use our writer
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Ensure temp file is cleaned up on error
	success := false
	defer func() {
		if !success {
			if removeErr := fileutil.Remove(tempFilePath); removeErr != nil {
				logger.Error(ctx, "Failed to remove temp file", tag.Error(removeErr))
			}
		}
	}()

	// Write the compacted data to the temp file
	writer := NewWriter(tempFilePath)

	if err := writer.Open(); err != nil {
		return fmt.Errorf("failed to open temp file writer: %w", err)
	}

	if err := writer.Write(ctx, *status); err != nil {
		_ = writer.close() // Best effort close
		return fmt.Errorf("failed to write compacted data: %w", err)
	}

	if err := writer.close(); err != nil {
		return fmt.Errorf("failed to close temp file writer: %w", err)
	}

	// Use atomic rename for safer file replacement
	if err := safeRename(tempFilePath, att.file); err != nil {
		return fmt.Errorf("failed to replace original file: %w", err)
	}

	// Invalidate the cache after successful compaction
	if att.cache != nil {
		att.cache.Invalidate(att.file)
	}

	success = true
	return nil
}

// safeRename safely replaces the target file with the source file,
// handling platform-specific differences
func safeRename(source, target string) error {
	if err := fileutil.ReplaceFile(source, target); err != nil {
		if _, statErr := os.Stat(target); statErr == nil {
			return fmt.Errorf("failed to remove target file: %w", err)
		}
		return err
	}
	return nil
}

// ReadStatus reads the latest status from the file, using cache if available.
// The context can be used to cancel the operation.
func (att *Attempt) ReadStatus(ctx context.Context) (*exec.DAGRunStatus, error) {
	// Try to use cache first if available
	if att.cache != nil {
		status, cacheErr := att.cache.LoadLatest(att.file, func() (*exec.DAGRunStatus, error) {
			att.mu.RLock()
			defer att.mu.RUnlock()
			return att.parseLocked(ctx)
		})

		if cacheErr == nil {
			return status, nil
		}
	}

	// Cache miss or disabled, perform a direct read
	att.mu.RLock()
	parsed, parseErr := att.parseLocked(ctx)
	att.mu.RUnlock()

	if parseErr != nil {
		if errors.Is(parseErr, io.EOF) {
			return nil, exec.ErrCorruptedStatusFile // This means no valid status was found in the file
		}
		return nil, fmt.Errorf("failed to parse status file: %w", parseErr)
	}

	return parsed, nil

}

// parseLocked reads the status file and returns the last valid status.
// Must be called with a lock (read or write) already held.
func (att *Attempt) parseLocked(ctx context.Context) (*exec.DAGRunStatus, error) {
	return parseStatusFileWithContext(ctx, att.file)
}

func (att *Attempt) lastPersistedEventTypeLocked(ctx context.Context) eventstore.EventType {
	status, err := att.parseLocked(ctx)
	if err != nil {
		return ""
	}
	eventType, ok := eventstore.PersistedDAGRunEventTypeForStatus(status.Status)
	if !ok {
		return ""
	}
	return eventType
}

func (att *Attempt) eventData() map[string]any {
	if att.dag == nil {
		return nil
	}
	fileName := att.dag.FileName()
	if fileName == "" && att.dag.SourceFile != "" {
		fileName = fileutil.TrimYAMLFileExtension(filepath.Base(att.dag.SourceFile))
	}
	if fileName == "" {
		return nil
	}
	return map[string]any{
		eventstore.DAGFileNameDataKey: fileName,
	}
}

// ParseStatusFile reads the status file and returns the last valid status.
// The bufferSize parameter controls the size of the read buffer.
func ParseStatusFile(file string) (*exec.DAGRunStatus, error) {
	return parseStatusFileWithContext(context.Background(), file)
}

func parseStatusFileWithContext(ctx context.Context, file string) (*exec.DAGRunStatus, error) {
	f, err := openStatusFileWithRetry(file)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrReadFailed, err)
	}
	defer func() {
		_ = f.Close()
	}()

	var (
		offset int64
		result *exec.DAGRunStatus
	)

	// Read append-only file from the beginning and find the last status
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrReadFailed, err)
		}
		line, nextOffset, err := readLineFrom(f, offset)
		if err == io.EOF {
			if result == nil {
				return nil, err
			}
			return result, nil
		} else if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrReadFailed, err)
		}

		offset = nextOffset
		if len(line) > 0 {
			status, err := exec.StatusFromJSON(string(line))
			if err == nil {
				result = status
			}
		}
	}
}

func openStatusFileWithRetry(path string) (*os.File, error) {
	var file *os.File
	err := retryTransientStatusRead(func() error {
		opened, err := os.Open(path) //nolint:gosec
		if err != nil {
			return err
		}
		file = opened
		return nil
	})
	if err != nil {
		return nil, err
	}
	return file, nil
}

func retryTransientStatusRead(op func() error) error {
	err := op()
	if err == nil || !isTransientStatusReadError(err) {
		return err
	}

	wait := 10 * time.Millisecond
	for range 12 {
		time.Sleep(wait)
		err = op()
		if err == nil || !isTransientStatusReadError(err) {
			return err
		}
		wait *= 2
		if wait > 100*time.Millisecond {
			wait = 100 * time.Millisecond
		}
	}

	return err
}

func isTransientStatusReadError(err error) bool {
	if runtime.GOOS != "windows" || err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "used by another process") ||
		strings.Contains(msg, "cannot access the file") ||
		strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "sharing violation")
}

// Abort implements models.DAGRunAttempt.
// It creates a flag to indicate that the attempt should be canceled.
func (att *Attempt) Abort(ctx context.Context) error {
	dir := filepath.Dir(att.file)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	cancelFile := filepath.Join(dir, CancelRequestedFlag)
	if _, err := os.Stat(cancelFile); err == nil {
		return nil
	}
	if err := fileutil.WriteFileAtomic(cancelFile, []byte{}, 0600); err != nil {
		return fmt.Errorf("failed to create cancel request file: %w", err)
	}
	logger.Info(ctx, "Cancel request created for attempt",
		tag.AttemptID(att.id),
		tag.File(cancelFile))
	return nil
}

// IsAborting checks if a cancel request has been made for this attempt.
func (att *Attempt) IsAborting(ctx context.Context) (bool, error) {
	cancelFile := filepath.Join(filepath.Dir(att.file), CancelRequestedFlag)
	if _, err := os.Stat(cancelFile); err != nil {
		if os.IsNotExist(err) {
			return false, nil // No cancel request found
		}
		return false, fmt.Errorf("failed to check cancel request file: %w", err)
	}

	logger.Info(ctx, "Cancel request found for attempt",
		tag.AttemptID(att.id),
		tag.File(cancelFile))
	return true, nil
}

// Hidden returns true if the attempt is hidden from normal operations.
func (att *Attempt) Hidden() bool {
	att.mu.RLock()
	defer att.mu.RUnlock()

	// Check if the directory name starts with a dot
	dir := filepath.Dir(att.file)
	baseName := filepath.Base(dir)
	return strings.HasPrefix(baseName, ".")
}

// Hide renames the attempt directory to hide it from normal operations.
// It prefixes the directory name with a dot to make it hidden.
func (att *Attempt) Hide(ctx context.Context) error {
	att.mu.Lock()
	defer att.mu.Unlock()

	// Cannot hide if attempt is open
	if att.writer != nil {
		return fmt.Errorf("cannot hide open attempt: %w", ErrStatusFileOpen)
	}

	// Get current directory path
	currentDir := filepath.Dir(att.file)
	baseName := filepath.Base(currentDir)

	// Check if already hidden (idempotent)
	if strings.HasPrefix(baseName, ".") {
		return nil
	}

	// Add dot prefix to hide the directory
	newBaseName := "." + baseName
	newDir := filepath.Join(filepath.Dir(currentDir), newBaseName)

	// Check if target already exists
	if _, err := os.Stat(newDir); err == nil {
		return fmt.Errorf("target directory already exists: %s", newDir)
	}

	// Perform atomic rename
	if err := safeRename(currentDir, newDir); err != nil {
		return fmt.Errorf("failed to hide attempt: %w", err)
	}

	// Update internal file path
	att.file = filepath.Join(newDir, filepath.Base(att.file))

	// Invalidate cache if present
	if att.cache != nil {
		att.cache.Invalidate(att.file)
	}

	// Log the operation
	logger.Info(ctx, "Hidden attempt",
		tag.AttemptID(att.id),
		slog.String("from", currentDir),
		slog.String("to", newDir))

	return nil
}

// dagRunDir returns the dag-run directory (parent of the attempt directory).
// The dag-run dir contains attempt dirs, messages, work dir, etc.
func (att *Attempt) dagRunDir() string {
	return filepath.Dir(filepath.Dir(att.file))
}

// WorkDir returns the path to the per-DAG-run working directory.
// The work directory lives at the dag-run level (not attempt level)
// so it persists across retries.
func (att *Attempt) WorkDir() string {
	return filepath.Join(att.dagRunDir(), "work")
}

// readLineFrom reads a line from the file starting at the specified offset.
// It returns the line, the new offset, and any error encountered.
// The buffer is used to reduce allocations.
func readLineFrom(f *os.File, offset int64) ([]byte, int64, error) {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}

	reader := bufio.NewReader(f)
	var line []byte
	var err error

	// Read the line
	line, err = reader.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return nil, offset, err
	}

	// Calculate the new offset
	newOffset := offset + int64(len(line))

	// Trim the newline character if present
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}

	return line, newOffset, err
}

// WriteOutputs writes the collected step outputs to outputs.json.
// If outputs is nil or has no output entries, no file is created.
func (att *Attempt) WriteOutputs(_ context.Context, outputs *exec.DAGRunOutputs) error {
	if outputs == nil || len(outputs.Outputs) == 0 {
		return nil
	}

	dir := filepath.Dir(att.file)
	outputsFile := filepath.Join(dir, OutputsFile)

	data, err := json.MarshalIndent(outputs, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal outputs: %w", err)
	}

	if err := fileutil.WriteFileAtomic(outputsFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write outputs file: %w", err)
	}

	return nil
}

// ReadOutputs reads the collected step outputs from outputs.json.
// Returns nil if the file does not exist or if the file is in old format (no metadata field).
func (att *Attempt) ReadOutputs(_ context.Context) (*exec.DAGRunOutputs, error) {
	dir := filepath.Dir(att.file)
	outputsFile := filepath.Join(dir, OutputsFile)

	data, err := fileutil.ReadFile(outputsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read outputs file: %w", err)
	}

	var outputs exec.DAGRunOutputs
	if err := json.Unmarshal(data, &outputs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal outputs: %w", err)
	}

	// Ignore old format (no metadata) - returns nil
	if outputs.Metadata.DAGRunID == "" {
		return nil, nil
	}

	return &outputs, nil
}

// WriteStepMessages writes LLM messages for a single step.
// Messages are stored at the dag-run level in a messages/ directory for retry persistence.
func (att *Attempt) WriteStepMessages(_ context.Context, stepName string, messages []exec.LLMMessage) error {
	if len(messages) == 0 {
		return nil
	}

	// Store at dag-run level (parent of attempt directory) for retry persistence
	messagesDir := filepath.Join(att.dagRunDir(), MessagesDir)

	if err := os.MkdirAll(messagesDir, 0750); err != nil {
		return fmt.Errorf("failed to create messages directory: %w", err)
	}

	file := filepath.Join(messagesDir, stepName+".json")
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal messages: %w", err)
	}

	if err := fileutil.WriteFileAtomic(file, data, 0600); err != nil {
		return fmt.Errorf("failed to write messages file: %w", err)
	}

	return nil
}

// ReadStepMessages reads LLM messages for a single step.
// Messages are stored at the dag-run level in a messages/ directory for retry persistence.
// Returns nil if no messages exist for the step.
func (att *Attempt) ReadStepMessages(_ context.Context, stepName string) ([]exec.LLMMessage, error) {
	// Read from dag-run level (parent of attempt directory) for retry persistence
	file := filepath.Join(att.dagRunDir(), MessagesDir, stepName+".json")

	data, err := fileutil.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read messages file: %w", err)
	}

	var messages []exec.LLMMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("failed to unmarshal messages: %w", err)
	}

	return messages, nil
}
