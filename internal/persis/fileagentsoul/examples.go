// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileagentsoul

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
)

//go:embed examples/*.md
var exampleSoulsFS embed.FS

const examplesMarkerFile = ".examples-created"

// ExampleSoulIDs holds the IDs of bundled example souls, derived from the embedded filesystem.
var ExampleSoulIDs []string

func init() {
	entries, err := exampleSoulsFS.ReadDir("examples")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".md")
		ExampleSoulIDs = append(ExampleSoulIDs, id)
	}
}

// SeedExampleSouls writes bundled example souls to baseDir if not already seeded.
// Returns true if examples were created this call, and an error if seeding failed.
func SeedExampleSouls(ctx context.Context, baseDir string) (bool, error) {
	markerPath := filepath.Join(baseDir, examplesMarkerFile)
	if _, err := os.Stat(markerPath); err == nil {
		return false, nil // already seeded
	}
	if hasExistingSouls(baseDir) {
		return false, nil
	}

	if err := os.MkdirAll(baseDir, soulDirPermissions); err != nil {
		return false, fmt.Errorf("failed to create souls directory %s: %w", baseDir, err)
	}

	logger.Info(ctx, "Creating example souls for first-time users", slog.String("dir", baseDir))

	entries, err := exampleSoulsFS.ReadDir("examples")
	if err != nil {
		return false, fmt.Errorf("failed to read embedded example souls: %w", err)
	}

	created := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".md" {
			continue
		}

		data, readErr := exampleSoulsFS.ReadFile("examples/" + entry.Name())
		if readErr != nil {
			return false, fmt.Errorf("failed to read embedded example soul %s: %w", entry.Name(), readErr)
		}

		destPath := filepath.Join(baseDir, entry.Name())
		if _, statErr := os.Stat(destPath); statErr == nil {
			continue // don't overwrite existing files
		}

		if err := fileutil.WriteFileAtomic(destPath, data, filePermissions); err != nil {
			return false, fmt.Errorf("failed to write example soul %s: %w", destPath, err)
		}
		created++
	}

	if created == 0 {
		return false, nil
	}

	markerContent := []byte("# This file indicates that example souls have been created.\n# Delete this file to re-create examples on next startup.\n")
	if err := fileutil.WriteFileAtomic(markerPath, markerContent, filePermissions); err != nil {
		return false, fmt.Errorf("failed to create examples marker file: %w", err)
	}

	logger.Info(ctx, "Example souls created successfully")
	return true, nil
}

// hasExistingSouls checks if the directory already contains soul .md files.
func hasExistingSouls(baseDir string) bool {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".md") {
			return true
		}
	}
	return false
}
