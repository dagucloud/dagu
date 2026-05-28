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

// CollectionOption configures a file-backed [Collection].
type CollectionOption func(*Collection)

// WithIndentedJSON stores records as 2-space indented JSON on disk, matching
// the pre-refactor (<= v2.7.4) released format for human-readable stores such
// as users, API keys, secrets, and webhooks. Records are normalized back to
// compact JSON in memory on read, so callers see canonical Record.Data
// regardless of on-disk indentation.
func WithIndentedJSON() CollectionOption {
	return func(c *Collection) { c.indent = true }
}

// NewCollection creates a [persis.Collection] backed by the given directory.
// Unlike [New]+[Collection], this skips the root MkdirAll — the directory
// is created lazily on the first write.
func NewCollection(dir string, opts ...CollectionOption) persis.Collection {
	c := &Collection{dir: dir}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// NewCollectionWithLockRoot creates a collection whose records live under dir
// while its cross-process locks are scoped under lockRoot.
func NewCollectionWithLockRoot(dir, lockRoot string, opts ...CollectionOption) persis.Collection {
	c := &Collection{dir: dir, lockRoot: lockRoot}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Close is a no-op; the file backend holds no persistent resources.
func (b *Backend) Close() error { return nil }
