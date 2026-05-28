// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

const reverseLineReaderBlockSize = 64 * 1024

type reverseLineReader struct {
	file   *os.File
	pos    int64
	base   int64
	buffer []byte
}

func newReverseLineReader(file *os.File, offset int64) (*reverseLineReader, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file for reverse read: %w", err)
	}
	size := info.Size()
	if offset < 0 {
		offset = size
	}
	if offset > size {
		return nil, invalidQueryCursor("cursor offset is outside the committed log")
	}
	return &reverseLineReader{
		file: file,
		pos:  offset,
		base: offset,
	}, nil
}

func (r *reverseLineReader) Next() ([]byte, int64, error) {
	for {
		if idx := bytes.LastIndexByte(r.buffer, '\n'); idx >= 0 {
			lineStart := r.base + int64(idx+1)
			line := append([]byte(nil), r.buffer[idx+1:]...)
			r.buffer = r.buffer[:idx]
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			return line, lineStart, nil
		}

		if r.pos == 0 {
			if len(r.buffer) == 0 {
				return nil, 0, io.EOF
			}
			lineStart := r.base
			line := append([]byte(nil), r.buffer...)
			r.buffer = nil
			if len(bytes.TrimSpace(line)) == 0 {
				return nil, 0, io.EOF
			}
			return line, lineStart, nil
		}

		readSize := minInt64(reverseLineReaderBlockSize, r.pos)
		r.pos -= readSize

		chunk := make([]byte, int(readSize))
		n, err := r.file.ReadAt(chunk, r.pos)
		if err != nil && err != io.EOF {
			return nil, 0, fmt.Errorf("reverse read committed log: %w", err)
		}
		chunk = chunk[:n]
		r.buffer = append(chunk, r.buffer...)
		r.base = r.pos
	}
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
