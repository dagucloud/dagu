// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileagentskill

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	bundledskills "github.com/dagucloud/dagu/skills"
)

const (
	dirPermissions  = 0750
	filePermissions = 0600
)

var assetsFS = bundledskills.Assets

const builtinKnowledgeEmbedDir = bundledskills.DaguReferencesDir

// SeedReferences extracts built-in reference documents to the given directory.
// These are read-only knowledge files the AI agent can read on demand.
// Returns the directory path if successful, empty string on failure.
// Files are always overwritten on each startup to keep them up-to-date with the binary.
func SeedReferences(destDir string) string {
	if err := os.MkdirAll(destDir, dirPermissions); err != nil {
		slog.Warn("Failed to create builtin knowledge directory", "dir", destDir, "error", err)
		return ""
	}

	err := fs.WalkDir(assetsFS, builtinKnowledgeEmbedDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		relPath := strings.TrimPrefix(path, builtinKnowledgeEmbedDir+"/")
		destPath := filepath.Join(destDir, relPath)

		data, readErr := assetsFS.ReadFile(path)
		if readErr != nil {
			slog.Warn("Failed to read embedded knowledge file", "path", path, "error", readErr)
			return nil
		}

		if err := fileutil.WriteFileAtomic(destPath, data, filePermissions); err != nil {
			slog.Warn("Failed to write knowledge file", "path", destPath, "error", err)
		}
		return nil
	})
	if err != nil {
		slog.Warn("Failed to walk embedded knowledge files", "error", err)
		return ""
	}

	return destDir
}
