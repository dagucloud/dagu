// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileagentconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
)

// Verify Store implements agent.ConfigStore at compile time.
var _ agent.ConfigStore = (*Store)(nil)

const (
	configFileName  = "config.json"
	agentDirName    = "agent"
	dirPermissions  = 0750
	filePermissions = 0600
)

// Environment variable names for agent configuration overrides.
const (
	envAgentEnabled = "DAGU_AGENT_ENABLED"
)

// Store implements a file-based singleton store for agent configuration.
// The config is stored as a JSON file at {dataDir}/agent/config.json.
// Thread-safe through internal locking.
type Store struct {
	baseDir     string
	mu          sync.RWMutex
	configCache *fileutil.Cache[*agent.Config]
}

// Option is a functional option for configuring the Store.
type Option func(*Store)

// WithConfigCache sets the config cache for the store.
func WithConfigCache(cache *fileutil.Cache[*agent.Config]) Option {
	return func(s *Store) {
		s.configCache = cache
	}
}

// New creates a new file-based agent config store.
// The dataDir is the base data directory (e.g., DAGU_HOME/data).
// The config will be stored at {dataDir}/agent/config.json.
func New(dataDir string, opts ...Option) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("fileagentconfig: dataDir cannot be empty")
	}

	baseDir := filepath.Join(dataDir, agentDirName)
	if err := os.MkdirAll(baseDir, dirPermissions); err != nil {
		return nil, fmt.Errorf("fileagentconfig: failed to create directory %s: %w", baseDir, err)
	}

	s := &Store{
		baseDir: baseDir,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s, nil
}

// Load reads the agent configuration from the JSON file.
// If the file doesn't exist, returns the default configuration.
// Priority: Environment variables > JSON file > Defaults
// Uses cache if available to avoid reading file on every request.
func (s *Store) Load(_ context.Context) (*agent.Config, error) {
	if s.configCache != nil {
		return s.configCache.LoadLatest(s.configPath(), s.loadFromFile)
	}
	return s.loadFromFile()
}

// loadFromFile reads config directly from file.
func (s *Store) loadFromFile() (*agent.Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg := agent.DefaultConfig()

	data, err := fileutil.ReadFile(s.configPath())
	if err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("fileagentconfig: failed to parse config file: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("fileagentconfig: failed to read config file: %w", err)
	}

	applyEnvOverrides(cfg)

	return cfg, nil
}

// IsEnabled returns whether the agent is enabled.
// Reads from cache if available.
func (s *Store) IsEnabled(ctx context.Context) bool {
	cfg, err := s.Load(ctx)
	if err != nil {
		return false
	}
	return cfg.Enabled
}

// Save writes the agent configuration to the JSON file.
// Uses atomic write (temp file + rename) to prevent corruption.
func (s *Store) Save(_ context.Context, cfg *agent.Config) error {
	if cfg == nil {
		return errors.New("fileagentconfig: config cannot be nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.writeConfigToFile(s.configPath(), cfg); err != nil {
		return err
	}

	// Invalidate cache after successful write so next Load picks up new data
	if s.configCache != nil {
		s.configCache.Invalidate(s.configPath())
	}

	return nil
}

// writeConfigToFile writes the config to a JSON file atomically.
func (s *Store) writeConfigToFile(filePath string, cfg *agent.Config) error {
	if err := fileutil.WriteJSONAtomic(filePath, cfg, filePermissions); err != nil {
		return fmt.Errorf("fileagentconfig: %w", err)
	}
	return nil
}

// Exists returns true if the config file exists.
func (s *Store) Exists() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, err := os.Stat(s.configPath())
	return err == nil
}

// configPath returns the full path to the config file.
func (s *Store) configPath() string {
	return filepath.Join(s.baseDir, configFileName)
}

// applyEnvOverrides applies environment variable overrides to the config.
// Environment variables take precedence over JSON file values.
func applyEnvOverrides(cfg *agent.Config) {
	if v := os.Getenv(envAgentEnabled); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Enabled = enabled
		}
	}
}
