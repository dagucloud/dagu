// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package diagnostic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

const (
	// StreamEnvName enables the private diagnostic stream for child processes.
	StreamEnvName = "DAGU_DIAGNOSTIC_STREAM"
	// StreamStderrJSONL sends diagnostic records to stderr as prefixed JSON lines.
	StreamStderrJSONL = "stderr-jsonl-v1"
	// StreamLinePrefix marks one diagnostic stream record.
	StreamLinePrefix = "DAGU_DIAGNOSTIC\t"
)

// NewStreamSink returns a sink that writes diagnostics as stream records.
func NewStreamSink(w io.Writer) Sink {
	if w == nil {
		return nil
	}
	return &streamSink{writer: w}
}

// ParseStreamLine parses one diagnostic stream line.
func ParseStreamLine(line []byte) (Diagnostic, bool, error) {
	line = bytes.TrimSuffix(line, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\r"))
	if !bytes.HasPrefix(line, []byte(StreamLinePrefix)) {
		return Diagnostic{}, false, nil
	}

	var d Diagnostic
	err := json.Unmarshal(line[len(StreamLinePrefix):], &d)
	if err != nil {
		return Diagnostic{}, true, err
	}
	return d, true, nil
}

type streamSink struct {
	mu     sync.Mutex
	writer io.Writer
}

func (s *streamSink) Report(d Diagnostic) {
	payload, err := json.Marshal(d)
	if err != nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.writer, "%s%s\n", StreamLinePrefix, payload)
}
