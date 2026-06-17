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

// ResolveFilePath attempts to find a file in multiple locations in the following order:
func (r *FileResolver) ResolveFilePath(file string) (string, error) {
	return r.resolveFilePath(file, ResolvePath)
}

// ResolveFilePathLiteral resolves a path without expanding environment variables.
func (r *FileResolver) ResolveFilePathLiteral(file string) (string, error) {
	return r.resolveFilePath(file, resolvePathLiteral)
}

func (r *FileResolver) resolveFilePath(file string, resolvePath func(string) (string, error)) (string, error) {
	if filepath.IsAbs(file) || strings.HasPrefix(file, "~") {
		resolved, err := resolvePath(file)
		if err == nil && FileExists(resolved) {
			return resolved, nil
		}
		return "", &FileNotFoundError{Path: file}
	}

	searchPaths, err := r.getSearchPaths(file)
	if err != nil {
		return "", fmt.Errorf("getting search paths: %w", err)
	}

	for _, path := range searchPaths {
		resolved, err := resolvePath(path)
		if err == nil && FileExists(resolved) {
			return resolved, nil
		}
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
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		path = filepath.Join(homeDir, path[1:])
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
