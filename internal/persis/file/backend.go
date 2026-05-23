// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package file implements [persis.Backend] on the local filesystem.
// Each collection maps to a subdirectory; each record maps to a .json file
// whose relative path mirrors the record ID with "/" as the path separator.
package file

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/internal/persis"
)

// Backend implements [persis.Backend] on the local filesystem.
type Backend struct {
	root string
	cols sync.Map // map[string]*Collection
}

var _ persis.Backend = (*Backend)(nil)

// New creates a Backend rooted at dir, creating it if necessary.
func New(dir string) (*Backend, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("file backend: create root %q: %w", dir, err)
	}
	return &Backend{root: dir}, nil
}

// Collection returns the collection with the given name, creating it lazily.
func (b *Backend) Collection(name string) persis.Collection {
	v, _ := b.cols.LoadOrStore(name, &Collection{
		dir: filepath.Join(b.root, name),
	})
	return v.(*Collection)
}

// NewCollection creates a [persis.Collection] backed by the given directory.
// Unlike [New]+[Collection], this skips the root MkdirAll — the directory
// is created lazily on the first write.
func NewCollection(dir string) persis.Collection {
	return &Collection{dir: dir}
}

// Close is a no-op; the file backend holds no persistent resources.
func (b *Backend) Close() error { return nil }
