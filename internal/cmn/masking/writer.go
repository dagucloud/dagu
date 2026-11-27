// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package masking

import (
	"bytes"
	"io"
)

// maxPatternLine is the most of a partial line a MaskingWriter holds for
// the masker's patterns.
const maxPatternLine = 64 << 10

// MaskingWriter wraps an io.Writer and masks sensitive values in the text
// written through it, however the writes split that text. A value is
// masked once all of it is written, even when it spans lines.
//
// Each Write passes on all text but a tail that could still be the start
// of a value or provider token. The tail is shorter than the longest form
// of a value the masker hides or, for a writer that masks provider tokens,
// than the shortest provider token, whichever is longer. The masker's
// patterns match within a line, so for them up to 64 KiB of a partial line
// is held too, and a match longer than that can be cut. Flush and Close
// write the held tail.
type MaskingWriter struct {
	writer io.Writer
	masker *Masker
	// tokens reports that the writer also masks provider token formats.
	tokens bool
	// buffer holds the text not written yet.
	buffer []byte
	// out is reused to build the masked text written.
	out []byte
	// prev is the last byte written, which decides whether a token prefix
	// at the start of buffer starts a token; 0 before the first.
	prev byte
	// masked is the number of bytes at the start of buffer that a mask
	// already written covers.
	masked int
	// token is the format of a provider token that the written text ends
	// inside, so the token characters that start buffer are masked as part
	// of it; nil when there is none.
	token *providerToken
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
// access and refresh tokens. A token is masked through its last token
// character, however long it is and however many writes carry it. If
// masker is nil, it passes through without masking.
func NewLogMaskingWriter(w io.Writer, masker *Masker) *MaskingWriter {
	return &MaskingWriter{
		writer: w,
		masker: masker,
		tokens: true,
	}
}

// Write implements io.Writer. It writes p masked, but for a tail that later
// writes could still make part of a value or token.
func (w *MaskingWriter) Write(p []byte) (n int, err error) {
	if w.masker == nil {
		return w.writer.Write(p)
	}
	w.buffer = append(w.buffer, p...)
	return len(p), w.send(false)
}

// Flush writes the held tail, masked as far as it goes.
func (w *MaskingWriter) Flush() error {
	return w.send(true)
}

// send writes the buffered text that text written later cannot change,
// masked, and keeps the rest. With all set, it writes all buffered text.
func (w *MaskingWriter) send(all bool) error {
	if len(w.buffer) == 0 {
		return nil
	}
	line := len(w.buffer)
	if !all && len(w.masker.patterns) > 0 {
		line = max(bytes.LastIndexByte(w.buffer, '\n')+1, len(w.buffer)-maxPatternLine)
		if line == 0 {
			return nil
		}
	}

	text := string(w.buffer)
	r := w.masker.scan(text, w.prev, w.tokens)
	covered := w.masked
	if w.token != nil {
		// The token's mask covers all held text, so the token runs on from
		// its end, even where held text is part of the prefix.
		run := w.masked
		for run < len(text) && w.token.inBody(text[run]) {
			run++
		}
		covered = max(covered, run)
		if run == len(text) {
			r.token, r.tokenAt = w.token, 0
		}
	}
	if covered > 0 {
		r.spans = joinSpans(append(r.spans, span{end: covered}))
	}
	end := len(text)
	if !all {
		end = min(r.open, line)
	}
	if end == 0 {
		return nil
	}

	data, masked := w.buffer[:end], 0
	if len(r.spans) > 0 && r.spans[0].start < end {
		out, last := w.out[:0], 0
		for _, s := range r.spans {
			if s.start >= end {
				break
			}
			out = append(out, text[last:s.start]...)
			// A span that continues a mask already written adds no mask.
			if s.start > 0 || covered == 0 {
				out = append(out, DefaultMaskString...)
			}
			last = min(s.end, end)
			masked = s.end - last
		}
		w.out = append(out, text[last:end]...)
		data = w.out
	}
	if len(data) > 0 {
		if _, err := w.writer.Write(data); err != nil {
			return err
		}
	}

	w.masked = masked
	w.token = nil
	if r.token != nil && r.tokenAt < end {
		w.token = r.token
	}
	w.prev = text[end-1]
	w.buffer = w.buffer[:copy(w.buffer, w.buffer[end:])]
	return nil
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
