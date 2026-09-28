// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"bufio"
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

// FlushIfDue writes buffered output through. A partial line the wrapped
// writer holds back, such as one awaiting secret masking, stays held.
func (s *safeBufferedWriter) FlushIfDue() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bw.Flush()
}

// Flush writes all buffered output through, including a partial line the
// wrapped writer holds back.
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
