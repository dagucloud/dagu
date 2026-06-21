// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"bufio"
	"bytes"
	"io"
	"sync"
)

// flushableMultiWriter creates a MultiWriter that can flush all underlying writers
type flushableMultiWriter struct {
	writers []io.Writer
}

// newFlushableMultiWriter creates a new flushableMultiWriter
func newFlushableMultiWriter(writers ...io.Writer) *flushableMultiWriter {
	return &flushableMultiWriter{writers: writers}
}

// Write writes to all underlying writers
func (fw *flushableMultiWriter) Write(p []byte) (n int, err error) {
	for _, w := range fw.writers {
		n, err = w.Write(p)
		if err != nil {
			return
		}
		if n != len(p) {
			err = io.ErrShortWrite
			return
		}
	}
	return len(p), nil
}

// Flush flushes all underlying writers that support flushing
func (fw *flushableMultiWriter) Flush() error {
	var lastErr error
	for _, w := range fw.writers {
		// Try different flush interfaces
		switch v := w.(type) {
		case *bufio.Writer:
			if err := v.Flush(); err != nil {
				lastErr = err
			}
		case interface{ Flush() error }:
			if err := v.Flush(); err != nil {
				lastErr = err
			}
		case interface{ Sync() error }:
			if err := v.Sync(); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

// safeBufferedWriter wraps bufio.Writer with a mutex to make concurrent
// Write and Flush safe across goroutines.
type safeBufferedWriter struct {
	mu sync.Mutex
	bw *bufio.Writer
}

// newSafeBufferedWriter creates a thread-safe buffered writer
func newSafeBufferedWriter(w io.Writer) *safeBufferedWriter {
	return &safeBufferedWriter{bw: bufio.NewWriter(w)}
}

func (s *safeBufferedWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bw.Write(p)
}

func (s *safeBufferedWriter) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bw.Flush()
}

// directWriter wraps an io.Writer with a mutex for thread safety without any
// buffering. Every Write call is passed directly to the underlying writer.
// It is used when OutputBufferingNone is selected.
type directWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// newDirectWriter creates an unbuffered thread-safe writer.
func newDirectWriter(w io.Writer) *directWriter {
	return &directWriter{w: w}
}

func (d *directWriter) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.w.Write(p)
}

// Flush is a no-op since there is no buffer to flush.
func (d *directWriter) Flush() error {
	return nil
}

const maxLineBufferSize = 32 * 1024 // auto-flush threshold for lines without a newline

// lineBufferedWriter wraps an io.Writer and flushes on every newline character.
// Partial lines (without a trailing newline) are kept in the buffer until either
// a newline arrives, the buffer exceeds maxLineBufferSize, or Flush() is called.
type lineBufferedWriter struct {
	mu  sync.Mutex
	buf []byte
	w   io.Writer
}

// newLineBufferedWriter creates a writer that flushes the underlying writer
// on every newline character.
func newLineBufferedWriter(w io.Writer) *lineBufferedWriter {
	return &lineBufferedWriter{w: w, buf: make([]byte, 0, 4096)}
}

func (lw *lineBufferedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	lw.buf = append(lw.buf, p...)

	// Flush complete lines, keep trailing partial data
	for {
		idx := bytes.IndexByte(lw.buf, '\n')
		if idx < 0 {
			break
		}
		line := lw.buf[:idx+1]
		lw.buf = lw.buf[idx+1:]
		if _, err := lw.w.Write(line); err != nil {
			return len(p), err
		}
	}
	// Release backing array when fully drained to avoid retaining large allocations.
	if len(lw.buf) == 0 {
		lw.buf = lw.buf[:0:0]
	}

	// Flush if buffer exceeds max size (handles newline-less output like progress bars)
	if len(lw.buf) >= maxLineBufferSize {
		if _, err := lw.w.Write(lw.buf); err != nil {
			return len(p), err
		}
		lw.buf = lw.buf[:0:0]
	}

	return len(p), nil
}

// Flush writes any remaining data in the buffer that hasn't been flushed by a
// newline.
func (lw *lineBufferedWriter) Flush() error {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if len(lw.buf) > 0 {
		_, err := lw.w.Write(lw.buf)
		lw.buf = lw.buf[:0:0]
		return err
	}
	return nil
}
