// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// OpenLog reads a regular log file up to its size when opened.
// The caller must close the reader. Missing files return an fs.ErrNotExist error.
func (s *Store) OpenLog(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Reject devices and pipes before opening, since opening them can block.
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("log %s is not a regular file", path)
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("log %s is not a regular file", path)
	}
	return &logReader{ctx: ctx, file: file, remaining: info.Size()}, nil
}

type logReader struct {
	ctx       context.Context
	file      *os.File
	remaining int64
}

func (r *logReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n, err := r.file.Read(p[:min(int64(len(p)), r.remaining)])
	r.remaining -= int64(n)
	if err == io.EOF && r.remaining > 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func (r *logReader) Close() error {
	return r.file.Close()
}
