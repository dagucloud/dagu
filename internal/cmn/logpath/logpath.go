// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package logpath

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
)

// Config defines configuration for DAG run log file creation.
type Config struct {
	BaseDir   string
	DAGLogDir string
	Name      string
	DAGRunID  string
}

// Generate expands the configured log directories, creates the DAG-run log
// directory if needed, and returns the full log file path.
func Generate(ctx context.Context, baseLogDir, dagLogDir, dagName, dagRunID string) (string, error) {
	dir, err := GenerateDir(ctx, baseLogDir, dagLogDir, dagName, dagRunID)
	if err != nil {
		return "", err
	}

	cfg := Config{
		Name:     dagName,
		DAGRunID: dagRunID,
	}

	return filepath.Join(dir, cfg.LogFile()), nil
}

// GenerateDir expands the configured directories, creates the DAG-run
// directory if needed, and returns the per-run directory path.
func GenerateDir(ctx context.Context, baseDir, dagDir, dagName, dagRunID string) (string, error) {
	baseDir, err := cmnvalue.String(ctx, baseDir, cmnvalue.WithOSExpansion(), cmnvalue.WithoutSubstitute())
	if err != nil {
		return "", fmt.Errorf("failed to expand base directory: %w", err)
	}

	dagDir, err = cmnvalue.String(ctx, dagDir, cmnvalue.WithOSExpansion(), cmnvalue.WithoutSubstitute())
	if err != nil {
		return "", fmt.Errorf("failed to expand DAG directory: %w", err)
	}

	cfg := Config{
		BaseDir:   baseDir,
		DAGLogDir: dagDir,
		Name:      dagName,
		DAGRunID:  dagRunID,
	}

	if err := cfg.Validate(); err != nil {
		return "", fmt.Errorf("invalid run directory settings: %w", err)
	}

	dir, err := cfg.RunDir()
	if err != nil {
		return "", fmt.Errorf("failed to setup run directory: %w", err)
	}

	return dir, nil
}

// Validate checks that essential fields are provided.
func (cfg Config) Validate() error {
	if cfg.Name == "" {
		return fmt.Errorf("DAGName cannot be empty")
	}
	if cfg.BaseDir == "" && cfg.DAGLogDir == "" {
		return fmt.Errorf("either base directory or DAG-specific directory must be specified")
	}
	return nil
}

// RunDir creates and returns the per-run directory based on the configuration.
func (cfg Config) RunDir() (string, error) {
	baseDir := cfg.BaseDir
	if cfg.DAGLogDir != "" {
		baseDir = cfg.DAGLogDir
	}
	if baseDir == "" {
		return "", fmt.Errorf("base log directory is not set")
	}

	utcTimestamp := time.Now().UTC().Format("20060102_150405Z")

	safeName := fileutil.SafeName(cfg.Name)
	logDir := filepath.Join(baseDir, safeName, "dag-run_"+utcTimestamp+"_"+cfg.DAGRunID)
	if err := os.MkdirAll(logDir, 0750); err != nil {
		return "", fmt.Errorf("failed to initialize directory %s: %w", logDir, err)
	}

	return logDir, nil
}

// LogDir is kept for backward compatibility with log-specific call sites.
func (cfg Config) LogDir() (string, error) {
	return cfg.RunDir()
}

// LogFile constructs the scheduler log filename using the current timestamp and
// a truncated DAG-run ID.
func (cfg Config) LogFile() string {
	timestamp := time.Now().Format("20060102.150405.000")
	truncDAGRunID := stringutil.TruncString(cfg.DAGRunID, 8)

	return fmt.Sprintf("dag-run_%s.%s.log",
		timestamp,
		truncDAGRunID,
	)
}
