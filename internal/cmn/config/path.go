// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
)

// Paths holds various file system path settings used by the application.
type Paths struct {
	// ConfigDir is the primary configuration directory.
	ConfigDir string
	// DAGsDir is the directory containing DAG definitions.
	DAGsDir string
	// SuspendFlagsDir is the directory for storing flags that indicate DAG suspension.
	SuspendFlagsDir string
	// DataDir is the directory for persisting application data (e.g., history).
	DataDir string
	// LogsDir is the directory where application logs are stored.
	LogsDir string
	// ArtifactsDir is the directory where DAG run artifacts are stored.
	ArtifactsDir string
	// DAGStateDir is the directory where persistent DAG state files are stored.
	DAGStateDir string
	// AdminLogsDir is the directory where administrative logs are kept.
	AdminLogsDir string
	// EventStoreDir is the directory where centralized event logs and inbox files are kept.
	EventStoreDir string
	// BaseConfigFile is the full path to the base configuration file.
	BaseConfigFile string
	// Notices collects any non-fatal informational messages encountered during path resolution.
	Notices []string
}

// XDGConfig contains the standard XDG directories used as a fallback.
type XDGConfig struct {
	DataHome   string
	ConfigHome string
}

// resolverLock ensures ResolvePaths is thread-safe.
var resolverLock sync.Mutex

// ExistingHomeDirNoticePrefix identifies the informational message emitted when an
// existing unified Dagu home directory is detected at the default ~/.dagu path.
const ExistingHomeDirNoticePrefix = "Using existing Dagu home directory at "

// ResolvePaths determines application paths based on the provided application home environment variable,
// a legacy path, and an XDGConfig. It chooses the configuration directory based on these inputs.
//
// Resolution logic:
//  1. If the environment variable (appHomeEnv) is set, use its value and assume unified directory structure.
//     The path is converted to an absolute path, and if different, the environment variable is updated.
//  2. Else, if the legacyPath exists on disk, use it and emit a non-fatal notice.
//  3. Otherwise, fall back to XDG-compliant defaults.
func ResolvePaths(appHomeEnv, legacyPath string, xdg XDGConfig) (Paths, error) {
	resolverLock.Lock()
	defer resolverLock.Unlock()

	switch {
	// Use the directory from the environment variable if available.
	case os.Getenv(appHomeEnv) != "":
		configDir := os.Getenv(appHomeEnv)
		absConfigDir, err := filepath.Abs(configDir)
		if err != nil {
			return Paths{}, fmt.Errorf("failed to resolve absolute path for %s: %w", appHomeEnv, err)
		}
		if absConfigDir != configDir {
			// Update the environment variable to the absolute path to ensure
			// forked processes receive the correct path.
			if err := os.Setenv(appHomeEnv, absConfigDir); err != nil {
				return Paths{}, fmt.Errorf("failed to set environment variable %s: %w", appHomeEnv, err)
			}
		}
		return setUnifiedPaths(absConfigDir), nil
	// If the legacy path exists, keep using it and emit a notice.
	case legacyPath != "" && fileutil.FileExists(legacyPath):
		paths := setUnifiedPaths(legacyPath)
		notice := fmt.Sprintf("%s%s.", ExistingHomeDirNoticePrefix, legacyPath)
		paths.Notices = append(paths.Notices, notice)
		return paths, nil
	// Fallback to default XDG-based paths.
	default:
		configDir := filepath.Join(xdg.ConfigHome, AppSlug)
		return setXDGPaths(xdg, configDir), nil
	}
}

// setXDGPaths sets the paths based on XDG environment variables.
// This approach uses the standard XDG directories (DataHome and ConfigHome)
// to organize application data, logs, and configuration files.
func setXDGPaths(xdg XDGConfig, configDir string) Paths {
	return Paths{
		ConfigDir:       configDir,
		DataDir:         filepath.Join(xdg.DataHome, AppSlug, "data"),
		LogsDir:         filepath.Join(xdg.DataHome, AppSlug, "logs"),
		ArtifactsDir:    filepath.Join(xdg.DataHome, AppSlug, "data", "artifacts"),
		DAGStateDir:     filepath.Join(xdg.DataHome, AppSlug, "data", "dag-state"),
		BaseConfigFile:  filepath.Join(xdg.ConfigHome, AppSlug, "base.yaml"),
		AdminLogsDir:    filepath.Join(xdg.DataHome, AppSlug, "logs", "admin"),
		EventStoreDir:   filepath.Join(xdg.DataHome, AppSlug, "logs", "admin", "events"),
		SuspendFlagsDir: filepath.Join(xdg.DataHome, AppSlug, "suspend"),
		DAGsDir:         filepath.Join(xdg.ConfigHome, AppSlug, "dags"),
	}
}

// setUnifiedPaths sets the application paths using a unified directory structure,
// where all subdirectories (data, logs, suspend, dags) are placed within a single root directory.
func setUnifiedPaths(configDir string) Paths {
	return Paths{
		ConfigDir:       configDir,
		DataDir:         filepath.Join(configDir, "data"),
		LogsDir:         filepath.Join(configDir, "logs"),
		ArtifactsDir:    filepath.Join(configDir, "data", "artifacts"),
		DAGStateDir:     filepath.Join(configDir, "data", "dag-state"),
		BaseConfigFile:  filepath.Join(configDir, "base.yaml"),
		AdminLogsDir:    filepath.Join(configDir, "logs", "admin"),
		EventStoreDir:   filepath.Join(configDir, "logs", "admin", "events"),
		SuspendFlagsDir: filepath.Join(configDir, "suspend"),
		DAGsDir:         filepath.Join(configDir, "dags"),
	}
}
