// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filelicense

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/license"
)

const (
	activationFile = "activation.json"
	dirPerm        = 0700
	filePerm       = 0600
)

// Store implements license.ActivationStore using file-based persistence.
type Store struct {
	dir string
	mu  sync.RWMutex
}

// New creates a new file-based activation store at the given directory.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Load reads the activation data from disk.
// Returns nil, nil when the file does not exist.
func (s *Store) Load() (*license.ActivationData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.dir, activationFile)
	data, err := fileutil.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read activation file: %w", err)
	}

	var ad license.ActivationData
	if err := json.Unmarshal(data, &ad); err != nil {
		return nil, fmt.Errorf("failed to unmarshal activation data: %w", err)
	}

	return &ad, nil
}

// Save writes the activation data to disk.
func (s *Store) Save(ad *license.ActivationData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, dirPerm); err != nil {
		return fmt.Errorf("failed to create license directory: %w", err)
	}

	data, err := json.MarshalIndent(ad, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal activation data: %w", err)
	}

	path := filepath.Join(s.dir, activationFile)

	// Atomic write: write to temp file, sync, then rename.
	tmp, err := os.CreateTemp(s.dir, ".activation-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = fileutil.Remove(tmpName) }() // best-effort cleanup on error

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, filePerm); err != nil {
		return fmt.Errorf("failed to set permissions on temp file: %w", err)
	}
	if err := fileutil.ReplaceFile(tmpName, path); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// Remove deletes the activation file from disk.
func (s *Store) Remove() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dir, activationFile)
	if err := fileutil.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove activation file: %w", err)
	}

	return nil
}
