// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package launcher

import (
	"bytes"
	"io"
	"sync"

	"github.com/dagucloud/dagu/internal/diagnostic"
)

type diagnosticFilteringWriter struct {
	mu     sync.Mutex
	target io.Writer
	sink   diagnostic.Sink
	buffer []byte
}

func newDiagnosticFilteringWriter(target io.Writer, sink diagnostic.Sink) io.Writer {
	if sink == nil || target == nil {
		return target
	}
	return &diagnosticFilteringWriter{
		target: target,
		sink:   sink,
	}
}

func (w *diagnosticFilteringWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buffer = append(w.buffer, p...)
	for {
		index := bytes.IndexByte(w.buffer, '\n')
		if index < 0 {
			if len(w.buffer) > defaultMaxBufferSize {
				if _, err := w.target.Write(w.buffer); err != nil {
					return len(p), err
				}
				w.buffer = w.buffer[:0]
			}
			return len(p), nil
		}

		line := append([]byte(nil), w.buffer[:index+1]...)
		w.buffer = w.buffer[index+1:]
		if d, ok, err := diagnostic.ParseStreamLine(line); ok && err == nil {
			w.sink.Report(d)
			continue
		}
		if _, err := w.target.Write(line); err != nil {
			return len(p), err
		}
	}
}
