// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dagdiscovery enumerates DAG definition files and watchable directories.
package dagdiscovery

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/workspace"
)

// File describes a discovered DAG definition.
type File struct {
	Path         string
	RelativePath string
	Size         int64
	ModTime      int64
}

// Result contains discoverable files, directories, and non-fatal traversal errors.
type Result struct {
	Files       []File
	Directories []string
	Errors      []error
}

// Scan enumerates DAG definitions beneath root.
func Scan(root string, recursive bool) (Result, error) {
	root = filepath.Clean(root)
	if !recursive {
		return scanRoot(root)
	}

	walkRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(walkRoot)
	if err != nil {
		return Result{}, err
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("%s is not a directory", root)
	}

	result := Result{}
	err = filepath.WalkDir(walkRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		rel := relativePath(walkRoot, path)
		discoveredPath := root
		if rel != "." {
			discoveredPath = filepath.Join(root, filepath.FromSlash(rel))
		}
		if walkErr != nil {
			if path == walkRoot {
				return walkErr
			}
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", rel, walkErr))
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if path != walkRoot && entry.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if entry.IsDir() {
			if path != walkRoot && excludedDirectory(walkRoot, path) {
				return filepath.SkipDir
			}
			result.Directories = append(result.Directories, discoveredPath)
			return nil
		}
		if !fileutil.IsYAMLFile(entry.Name()) {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", rel, err))
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		result.Files = append(result.Files, File{
			Path:         discoveredPath,
			RelativePath: rel,
			Size:         info.Size(),
			ModTime:      info.ModTime().UnixNano(),
		})
		return nil
	})
	if err != nil {
		return Result{}, err
	}

	sortResult(&result)
	return result, nil
}

func scanRoot(root string) (Result, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return Result{}, err
	}

	result := Result{Directories: []string{root}}
	for _, entry := range entries {
		if entry.IsDir() || !fileutil.IsYAMLFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		result.Files = append(result.Files, File{
			Path:         filepath.Join(root, entry.Name()),
			RelativePath: filepath.ToSlash(entry.Name()),
			Size:         info.Size(),
			ModTime:      info.ModTime().UnixNano(),
		})
	}

	sortResult(&result)
	return result, nil
}

func excludedDirectory(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) == 0 {
		return false
	}
	if parts[0] == workspace.BaseConfigDirName {
		return true
	}
	for _, part := range parts {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func relativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func sortResult(result *Result) {
	sort.Slice(result.Files, func(i, j int) bool {
		return result.Files[i].RelativePath < result.Files[j].RelativePath
	})
	sort.Strings(result.Directories)
	sort.Slice(result.Errors, func(i, j int) bool {
		return result.Errors[i].Error() < result.Errors[j].Error()
	})
}
