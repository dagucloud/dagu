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
	RelPath string
	Size    int64
	ModTime int64
}

// Result contains discoverable files, directories, and non-fatal traversal errors.
type Result struct {
	Files  []File
	Dirs   []string
	Errors []error
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
		relPath, err := filepath.Rel(walkRoot, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		if walkErr != nil {
			if path == walkRoot {
				return walkErr
			}
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", relPath, walkErr))
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if path != walkRoot && entry.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if entry.IsDir() {
			if path != walkRoot &&
				(strings.HasPrefix(entry.Name(), ".") || relPath == workspace.BaseConfigDirName) {
				return filepath.SkipDir
			}
			dir := root
			if relPath != "." {
				dir = filepath.Join(root, filepath.FromSlash(relPath))
			}
			result.Dirs = append(result.Dirs, dir)
			return nil
		}
		if !fileutil.IsYAMLFile(entry.Name()) {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", relPath, err))
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		result.Files = append(result.Files, File{
			RelPath: relPath,
			Size:    info.Size(),
			ModTime: info.ModTime().UnixNano(),
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

	result := Result{Dirs: []string{root}}
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
			RelPath: filepath.ToSlash(entry.Name()),
			Size:    info.Size(),
			ModTime: info.ModTime().UnixNano(),
		})
	}

	sortResult(&result)
	return result, nil
}

func sortResult(result *Result) {
	sort.Slice(result.Files, func(i, j int) bool {
		return result.Files[i].RelPath < result.Files[j].RelPath
	})
	sort.Strings(result.Dirs)
	sort.Slice(result.Errors, func(i, j int) bool {
		return result.Errors[i].Error() < result.Errors[j].Error()
	})
}
