// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileupgradecheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/upgrade"
)

const (
	upgradeDirName  = "upgrade"
	cacheFileName   = "upgrade-check.json"
	dirPermissions  = 0750
	filePermissions = 0600
)

var _ upgrade.CacheStore = (*store)(nil)

// store implements a file-based store for upgrade check cache data.
// The cache is stored as a JSON file at {dataDir}/upgrade/upgrade-check.json.
// Thread-safe through internal locking.
type store struct {
	baseDir string
	mu      sync.RWMutex
}

// New creates a new file-based upgrade check store.
// The dataDir is the base data directory (e.g., DAGU_HOME/data).
// The cache will be stored at {dataDir}/upgrade/upgrade-check.json.
func New(dataDir string) (upgrade.CacheStore, error) {
	if dataDir == "" {
		return nil, errors.New("fileupgradecheck: dataDir cannot be empty")
	}

	baseDir := filepath.Join(dataDir, upgradeDirName)
	if err := os.MkdirAll(baseDir, dirPermissions); err != nil {
		return nil, fmt.Errorf("fileupgradecheck: failed to create directory %s: %w", baseDir, err)
	}

	return &store{baseDir: baseDir}, nil
}

// Load reads the upgrade check cache from the JSON file.
// Returns nil, nil if the file doesn't exist or contains invalid JSON.
func (s *store) Load() (*upgrade.UpgradeCheckCache, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := fileutil.ReadFile(s.cachePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fileupgradecheck: failed to read cache file: %w", err)
	}

	var cache upgrade.UpgradeCheckCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, nil
	}

	return &cache, nil
}

// Save writes the upgrade check cache to the JSON file atomically.
func (s *store) Save(cache *upgrade.UpgradeCheckCache) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := fileutil.WriteJSONAtomic(s.cachePath(), cache, filePermissions); err != nil {
		return fmt.Errorf("fileupgradecheck: %w", err)
	}
	return nil
}

// cachePath returns the full path to the cache file.
func (s *store) cachePath() string {
	return filepath.Join(s.baseDir, cacheFileName)
}
