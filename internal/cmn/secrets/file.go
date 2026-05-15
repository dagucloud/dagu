// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secrets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/internal/core"
)

func init() {
	registerResolver("file", func(baseDirs []string) Resolver {
		return &fileResolver{
			baseDirs: baseDirs,
		}
	})
}

// fileResolver reads secrets from files on the filesystem.
// This provider is designed for:
//   - Kubernetes Secret Store CSI Driver (production)
//   - Docker secrets (production)
//   - Local development with secret files outside git
//
// Security note: NOT recommended for storing plain text secrets in DAG directories
// or version control.
type fileResolver struct {
	baseDirs []string // Base directories to try for relative path resolution
}

// Name returns the provider identifier.
func (r *fileResolver) Name() string {
	return "file"
}

// Validate checks if the secret reference is valid for file access.
func (r *fileResolver) Validate(ref core.SecretRef) error {
	if ref.Key == "" {
		return fmt.Errorf("key (file path) is required")
	}
	return nil
}

// CheckCapability reports that file access checks use filesystem metadata and
// open permissions without reading file contents.
func (r *fileResolver) CheckCapability(core.SecretRef) CheckCapability {
	return CheckCapabilityNoFetch
}

// Resolve reads the secret value from a file.
func (r *fileResolver) Resolve(_ context.Context, ref core.SecretRef) (string, error) {
	filePath := r.resolvePath(ref.Key)

	// Read file content
	//nolint:gosec // File path is from user configuration, this is expected
	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("secret file not found: %s", filePath)
		}
		if os.IsPermission(err) {
			return "", fmt.Errorf("permission denied reading secret file: %s", filePath)
		}
		return "", fmt.Errorf("failed to read secret file %s: %w", filePath, err)
	}

	return string(content), nil
}

// CheckAccessibility verifies the file exists and is readable without fetching its content.
func (r *fileResolver) CheckAccessibility(_ context.Context, ref core.SecretRef) error {
	filePath := r.resolvePath(ref.Key)

	// Check if file exists and get info
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("secret file not found: %s", filePath)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied accessing secret file: %s", filePath)
		}
		return fmt.Errorf("failed to access secret file %s: %w", filePath, err)
	}

	// Verify it's a file, not a directory
	if info.IsDir() {
		return fmt.Errorf("secret path is a directory, not a file: %s", filePath)
	}

	// Verify file is readable (try opening)
	//nolint:gosec // File path is from user configuration, this is expected
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("secret file is not readable: %s: %w", filePath, err)
	}
	defer func() {
		_ = file.Close()
	}()

	return nil
}

// resolvePath converts relative paths to absolute paths.
// Absolute paths are returned as-is.
// Relative paths are resolved by trying each base directory in order until a file is found.
func (r *fileResolver) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}

	// Relative path: try each base directory in order
	for _, baseDir := range r.baseDirs {
		if baseDir == "" {
			continue
		}
		absPath := filepath.Join(baseDir, path)
		// Check if file exists at this path
		if _, err := os.Stat(absPath); err == nil {
			return absPath
		}
	}

	// If no base directories or file not found in any of them, return the path as-is
	// This will cause a "file not found" error in Resolve()
	return path
}
