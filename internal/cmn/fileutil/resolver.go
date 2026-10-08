// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileResolver handles file path resolution across multiple locations
type FileResolver struct {
	relativeTos []string
}

// NewFileResolver creates a new FileResolver instance
func NewFileResolver(relativeTos []string) *FileResolver {
	return &FileResolver{
		relativeTos: relativeTos,
	}
}

// ResolveFilePath attempts to find a file in multiple locations.
// It returns a *FileNotFoundError when no candidate path exists, and the
// underlying error when a path cannot be resolved at all (for example an
// unsupported ~name path or an undetermined home directory).
func (r *FileResolver) ResolveFilePath(file string) (string, error) {
	return r.resolveFilePath(file, true, ResolvePath)
}

// ResolveFilePathLiteral resolves a path without expanding environment variables.
func (r *FileResolver) ResolveFilePathLiteral(file string) (string, error) {
	return r.resolveFilePath(file, false, resolvePathLiteral)
}

func (r *FileResolver) resolveFilePath(file string, expandEnv bool, resolvePath func(string) (string, error)) (string, error) {
	// Normalize before classifying: whitespace can hide a leading "~" and an
	// environment variable can expand to a "~name" path, both of which belong
	// in the absolute/tilde branch rather than search mode.
	file = strings.TrimSpace(file)
	if expandEnv {
		file = os.ExpandEnv(file)
	}
	if filepath.IsAbs(file) || strings.HasPrefix(file, "~") {
		resolved, err := resolvePath(file)
		if err != nil {
			return "", err
		}
		if FileExists(resolved) {
			return resolved, nil
		}
		return "", &FileNotFoundError{Path: file}
	}

	searchPaths, err := r.getSearchPaths(file)
	if err != nil {
		return "", fmt.Errorf("getting search paths: %w", err)
	}

	var resolveErr error
	for _, path := range searchPaths {
		resolved, err := resolvePath(path)
		if err != nil {
			// Keep searching: a later search path may still locate the file.
			if resolveErr == nil {
				resolveErr = err
			}
			continue
		}
		if FileExists(resolved) {
			return resolved, nil
		}
	}
	if resolveErr != nil {
		return "", resolveErr
	}

	return "", &FileNotFoundError{
		Path:          file,
		SearchedPaths: searchPaths,
	}
}

func resolvePathLiteral(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	path, err := expandHomeDir(path)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}
	return filepath.Clean(absPath), nil
}

// getSearchPaths returns a list of paths to search for the file
func (r *FileResolver) getSearchPaths(file string) ([]string, error) {
	var paths []string

	for _, relativeTo := range r.relativeTos {
		if IsDir(relativeTo) {
			paths = append(paths, filepath.Join(relativeTo, file))
		} else {
			dir := filepath.Dir(relativeTo)
			paths = append(paths, filepath.Join(dir, file))
		}
	}

	return paths, nil
}

// FileNotFoundError provides detailed information about file search failure
type FileNotFoundError struct {
	Path          string
	SearchedPaths []string
}

func (e *FileNotFoundError) Error() string {
	if len(e.SearchedPaths) == 0 {
		return fmt.Sprintf("file not found: %s", e.Path)
	}
	return fmt.Sprintf("file not found: %s (searched in: %v)", e.Path, e.SearchedPaths)
}
