// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
)

// backwardChunkSize is how many bytes backward file scans read per step.
const backwardChunkSize = 64 * 1024

// ReadLinesBackward calls fn with the lines in the first size bytes of r,
// last first, until fn reports done or returns an error. Each line comes
// without its newline, and terminated reports whether a newline ended it;
// only the last line can lack one. A file that ends with a newline has no
// unterminated line.
//
// Reading ends near the start of the line where fn stops, so finding a file's
// last record costs about that record rather than the whole file. The line
// slice is valid only until fn returns. If r holds fewer than size bytes, the
// read fails with io.ErrUnexpectedEOF.
func ReadLinesBackward(ctx context.Context, r io.ReaderAt, size int64, fn func(line []byte, terminated bool) (done bool, err error)) error {
	buf := make([]byte, min(backwardChunkSize, size))
	// parts holds, last first, the pieces of a line that spans chunks.
	var parts [][]byte
	terminated := false
	emit := func(head []byte) (bool, error) {
		line := head
		if len(parts) > 0 {
			line = make([]byte, 0, len(head)+partsLen(parts))
			line = append(line, head...)
			for _, part := range slices.Backward(parts) {
				line = append(line, part...)
			}
			parts = parts[:0]
		}
		return fn(line, terminated)
	}

	pos := size
	first := true
	for pos > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(int64(len(buf)), pos)
		pos -= n
		chunk := buf[:n]
		if read, err := r.ReadAt(chunk, pos); read < len(chunk) {
			if err == nil || errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		if first {
			first = false
			// A trailing newline ends the last line rather than starting an empty one.
			if chunk[len(chunk)-1] == '\n' {
				chunk = chunk[:len(chunk)-1]
				terminated = true
			}
		}
		for {
			i := bytes.LastIndexByte(chunk, '\n')
			if i < 0 {
				if len(chunk) > 0 {
					parts = append(parts, bytes.Clone(chunk))
				}
				break
			}
			done, err := emit(chunk[i+1:])
			if err != nil || done {
				return err
			}
			chunk = chunk[:i]
			terminated = true
		}
	}
	// What is left is the first line, which no newline precedes.
	if size > 0 {
		_, err := emit(nil)
		return err
	}
	return nil
}

func partsLen(parts [][]byte) int {
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	return total
}
