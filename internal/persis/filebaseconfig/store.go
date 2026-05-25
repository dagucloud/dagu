// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filebaseconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core/baseconfig"
)

// Verify Store implements baseconfig.Store at compile time.
var _ baseconfig.Store = (*Store)(nil)

const filePermissions = 0600

// Store implements a file-based store for the base DAG configuration.
// Thread-safe through internal locking.
type Store struct {
	filePath    string
	mu          sync.RWMutex
	fileCache   *fileutil.Cache[string]
	skipDefault bool
}

// Option is a functional option for configuring the Store.
type Option func(*Store)

// WithFileCache sets the file cache for the store.
func WithFileCache(cache *fileutil.Cache[string]) Option {
	return func(s *Store) {
		s.fileCache = cache
	}
}

// WithSkipDefault disables auto-creation of the default base config file.
func WithSkipDefault(skip bool) Option {
	return func(s *Store) {
		s.skipDefault = skip
	}
}

// New creates a new file-based base config store.
// The filePath is the full path to the base.yaml file.
func New(filePath string, opts ...Option) (*Store, error) {
	if filePath == "" {
		return nil, errors.New("filebaseconfig: filePath cannot be empty")
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("filebaseconfig: failed to create directory %s: %w", dir, err)
	}

	s := &Store{filePath: filePath}
	for _, opt := range opts {
		opt(s)
	}

	return s, nil
}

// Initialize ensures a default base config exists for first-time users.
// Skipped if the file already exists, a marker file is present, or skipDefault is set.
func (s *Store) Initialize() error {
	if s.skipDefault {
		return nil
	}
	if fileutil.FileExists(s.filePath) {
		return nil
	}
	dir := filepath.Dir(s.filePath)
	markerFile := filepath.Join(dir, ".base-config-created")
	if fileutil.FileExists(markerFile) {
		return nil
	}
	if err := fileutil.WriteFileAtomic(s.filePath, []byte(defaultBaseConfig), filePermissions); err != nil {
		return fmt.Errorf("filebaseconfig: failed to create default base config: %w", err)
	}
	markerContent := []byte("# Marker: default base.yaml was auto-created.\n# Delete this file and base.yaml to re-create defaults on next startup.\n")
	_ = fileutil.WriteFileAtomic(markerFile, markerContent, filePermissions)
	return nil
}

// GetSpec returns the raw YAML content of the base configuration.
// Returns an empty string if the file does not exist.
func (s *Store) GetSpec(_ context.Context) (string, error) {
	if s.fileCache != nil {
		return s.fileCache.LoadLatest(s.filePath, s.readFromFile)
	}
	return s.readFromFile()
}

// readFromFile reads the config file directly from disk.
func (s *Store) readFromFile() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := fileutil.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("filebaseconfig: failed to read file: %w", err)
	}

	return string(data), nil
}

// UpdateSpec writes the given YAML content to the base configuration file.
func (s *Store) UpdateSpec(_ context.Context, spec []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := fileutil.WriteFileAtomic(s.filePath, spec, filePermissions); err != nil {
		return fmt.Errorf("filebaseconfig: failed to write file: %w", err)
	}

	if s.fileCache != nil {
		s.fileCache.Invalidate(s.filePath)
	}

	return nil
}
