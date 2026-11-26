// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package masking

import (
	"bytes"
	"io"
)

// MaskingWriter wraps an io.Writer and masks sensitive values in the output.
// It uses line buffering to ensure values split across multiple Write() calls
// are properly masked.
type MaskingWriter struct {
	writer io.Writer
	masker *Masker
	// tokens reports that the writer also masks provider token formats.
	tokens bool
	buffer bytes.Buffer
}

// NewMaskingWriter creates a new MaskingWriter that wraps the given writer.
// If masker is nil, it returns a writer that passes through without masking.
func NewMaskingWriter(w io.Writer, masker *Masker) *MaskingWriter {
	return &MaskingWriter{
		writer: w,
		masker: masker,
	}
}

// NewLogMaskingWriter creates a MaskingWriter for a step log. Besides the
// values of masker, it masks provider OAuth token formats, such as Google
// access and refresh tokens. If masker is nil, it passes through without
// masking.
func NewLogMaskingWriter(w io.Writer, masker *Masker) *MaskingWriter {
	return &MaskingWriter{
		writer: w,
		masker: masker,
		tokens: true,
	}
}

// Write implements io.Writer interface.
// It buffers data until newlines and masks complete lines before writing.
func (w *MaskingWriter) Write(p []byte) (n int, err error) {
	// If no masker, pass through directly
	if w.masker == nil {
		return w.writer.Write(p)
	}

	// Write to buffer first
	n = len(p)
	w.buffer.Write(p)

	// Process complete lines from buffer
	if err := w.flushLines(); err != nil {
		return n, err
	}

	return n, nil
}

// flushLines writes complete lines (ending with \n) to the underlying writer
func (w *MaskingWriter) flushLines() error {
	data := w.buffer.Bytes()
	lastNewline := bytes.LastIndexByte(data, '\n')

	// No complete lines yet
	if lastNewline == -1 {
		return nil
	}

	// Extract complete lines (including the final newline)
	completeLines := data[:lastNewline+1]
	remaining := data[lastNewline+1:]

	// Mask and write complete lines
	masked := w.mask(completeLines)
	if _, err := w.writer.Write(masked); err != nil {
		return err
	}

	// Reset buffer with remaining incomplete line
	w.buffer.Reset()
	if len(remaining) > 0 {
		w.buffer.Write(remaining)
	}

	return nil
}

// Flush writes any remaining buffered data (even if incomplete line)
func (w *MaskingWriter) Flush() error {
	if w.buffer.Len() == 0 {
		return nil
	}

	// Mask and write remaining buffer
	if w.masker != nil {
		masked := w.mask(w.buffer.Bytes())
		if _, err := w.writer.Write(masked); err != nil {
			return err
		}
	} else {
		if _, err := w.writer.Write(w.buffer.Bytes()); err != nil {
			return err
		}
	}

	w.buffer.Reset()
	return nil
}

// mask returns data with the values the writer hides masked.
func (w *MaskingWriter) mask(data []byte) []byte {
	return []byte(w.masker.mask(string(data), w.tokens))
}

// Close flushes any remaining data and closes the underlying writer if it implements io.Closer
func (w *MaskingWriter) Close() error {
	if err := w.Flush(); err != nil {
		return err
	}

	if closer, ok := w.writer.(io.Closer); ok {
		return closer.Close()
	}

	return nil
}
