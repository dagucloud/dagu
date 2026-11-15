// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

var (
	ErrWriterNotOpen = errors.New("writer is not open")
)

type Writer struct {
	target     string
	buffer     *bufio.Writer
	encoder    *json.Encoder
	file       *os.File
	mu         sync.Mutex
	bufferSize int
}

// WriterOption defines functional options for configuring a Writer.
type WriterOption func(*Writer)

// NewWriter creates a new Writer instance for the specified target file path.
func NewWriter(target string, opts ...WriterOption) *Writer {
	w := &Writer{
		target:     target,
		bufferSize: 4096,
	}

	for _, opt := range opts {
		opt(w)
	}

	return w
}

// Open prepares the writer for writing by creating necessary directories
// and opening the target file.
func (w *Writer) Open() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.isOpenLocked() {
		return nil
	}

	dir := filepath.Dir(w.target)
	if err := fileutil.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	file, err := fileutil.OpenOrCreateFile(w.target)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", w.target, err)
	}

	w.file = file
	w.buffer = bufio.NewWriterSize(file, w.bufferSize)
	w.encoder = json.NewEncoder(w.buffer)
	w.encoder.SetEscapeHTML(false)
	w.endPartialLineLocked()
	return nil
}

// endPartialLineLocked starts the next record on a line of its own when the
// file ends in a partial line, as a write cut off by a crash or a full disk
// leaves it. A record appended to that line would join it, and neither could
// be read. The newline goes out with the next record.
func (w *Writer) endPartialLineLocked() {
	if endsMidLine(w.target) {
		_ = w.buffer.WriteByte('\n')
	}
}

// endsMidLine reports whether the file ends in a partial line. A file that
// cannot be read is taken to, since an extra newline only adds an empty line,
// which readers skip.
func endsMidLine(path string) bool {
	file, err := fileutil.Open(path)
	if err != nil {
		return true
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return true
	}
	if info.Size() == 0 {
		return false
	}
	last := make([]byte, 1)
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return true
	}
	return last[0] != '\n'
}

// reopenIfReplaced opens the target again when the path names another file
// than the open one, as after another writer of the same file compacted it,
// or when the open file can no longer be read, as an NFS handle goes stale
// when another host replaces the file. Appends to such a file would reach no
// reader. A usable open file is kept when the path cannot be read.
func (w *Writer) reopenIfReplaced() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.isOpenLocked() {
		return nil
	}
	open, openErr := w.file.Stat()
	current, err := os.Stat(w.target)
	if openErr == nil && (err != nil || os.SameFile(open, current)) {
		return nil
	}
	file, err := fileutil.OpenOrCreateFile(w.target)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", w.target, err)
	}
	// Each write flushes, so the buffer holds nothing for the replaced file.
	_ = w.file.Close()
	w.file = file
	w.buffer.Reset(file)
	w.endPartialLineLocked()
	return nil
}

// Write serializes the status to JSON and appends it to the file.
// It automatically flushes data to ensure durability.
func (w *Writer) Write(ctx context.Context, st ir.DAGRunStatus) error {
	if err := w.write(st); err != nil {
		logger.Error(ctx, "Failed to write status", tag.Error(err))
		return err
	}

	return nil
}

// write encodes a single status entry and persists it to disk.
func (w *Writer) write(st ir.DAGRunStatus) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.isOpenLocked() {
		return ErrWriterNotOpen
	}

	if err := w.encoder.Encode(ir.StoredStatus(st)); err != nil {
		w.discardLocked()
		return fmt.Errorf("failed to encode status: %w", err)
	}

	if err := w.flushAndSyncLocked(); err != nil {
		w.discardLocked()
		return err
	}
	return nil
}

// discardLocked closes the file after a failed write and drops what the write
// left in the buffer. A buffer that failed to write keeps failing, so the file
// must be opened again before the next write.
func (w *Writer) discardLocked() {
	_ = w.file.Close()
	w.file = nil
	w.buffer = nil
	w.encoder = nil
}

// Close flushes any buffered data and closes the underlying file.
// It's safe to call close multiple times.
func (w *Writer) Close(ctx context.Context) error {
	if err := w.close(); err != nil {
		logger.Error(ctx, "Failed to close writer", tag.Error(err))
		return err
	}

	return nil
}

// close flushes any buffered data and closes the underlying file.
func (w *Writer) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.isOpenLocked() {
		return nil
	}

	var errs []error

	if err := w.flushAndSyncLocked(); err != nil {
		errs = append(errs, err)
	}

	if err := w.file.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close error: %w", err))
	}

	w.file = nil
	w.buffer = nil
	w.encoder = nil

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// flushAndSyncLocked flushes the buffered encoder output and syncs the file.
func (w *Writer) flushAndSyncLocked() error {
	var errs []error

	if err := w.buffer.Flush(); err != nil {
		errs = append(errs, fmt.Errorf("flush error: %w", err))
	}

	if err := w.file.Sync(); err != nil {
		errs = append(errs, fmt.Errorf("sync error: %w", err))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// IsOpen returns true if the writer is currently open.
func (w *Writer) IsOpen() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.isOpenLocked()
}

// isOpenLocked reports whether the writer has an active file and encoder.
func (w *Writer) isOpenLocked() bool {
	return w.file != nil && w.buffer != nil && w.encoder != nil
}
