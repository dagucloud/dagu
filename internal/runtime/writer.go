// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"bufio"
	"errors"
	"io"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
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
	w  io.Writer
	bw *bufio.Writer
}

// newSafeBufferedWriter creates a thread-safe buffered writer
func newSafeBufferedWriter(w io.Writer) *safeBufferedWriter {
	return &safeBufferedWriter{w: w, bw: bufio.NewWriter(w)}
}

func (s *safeBufferedWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bw.Write(p)
}

// FlushIfDue writes buffered output through. Text the wrapped writer holds
// back, such as the start of a secret awaiting the rest, stays held.
func (s *safeBufferedWriter) FlushIfDue() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bw.Flush()
}

// Flush writes all buffered output through, including text the wrapped
// writer holds back.
func (s *safeBufferedWriter) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.bw.Flush(); err != nil {
		return err
	}
	if f, ok := s.w.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}

// maskingStreamWriter masks secrets and provider token formats in step output
// streamed to a remote log, as local step logs do. Text that could still
// start a secret is held until the rest arrives or Flush or Close is
// called, so a secret split across writes is masked whole.
type maskingStreamWriter struct {
	mu     sync.Mutex
	masked *masking.MaskingWriter
	stream io.WriteCloser
}

func newMaskingStreamWriter(stream io.WriteCloser, masker *masking.Masker) *maskingStreamWriter {
	return &maskingStreamWriter{masked: masking.NewLogMaskingWriter(stream, masker), stream: stream}
}

func (w *maskingStreamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.masked.Write(p)
}

// FlushIfDue sends due output to the stream. Held text stays held.
func (w *maskingStreamWriter) FlushIfDue() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if f, ok := w.stream.(interface{ FlushIfDue() error }); ok {
		return f.FlushIfDue()
	}
	return nil
}

// Flush sends all output to the stream, including held text.
func (w *maskingStreamWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.masked.Flush(); err != nil {
		return err
	}
	if f, ok := w.stream.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}

// Close sends held text and closes the stream.
func (w *maskingStreamWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return errors.Join(w.masked.Flush(), w.stream.Close())
}
