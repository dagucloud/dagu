// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package filerefs resolves the files a step's files field names.
package filerefs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Paths returns the files the entries name, in order and each once. An
// entry is a path or a glob pattern, several of them one per line, or JSON
// as an earlier step publishes it: a list of paths, of objects with a path
// such as saved mail attachments, or of emails from mail.search, whose saved
// attachments it names, or one such object. Relative paths resolve against
// workDir, or the current directory when it is empty. A path that names no
// file is returned as it is, for the caller to report; a pattern that
// matches nothing is an error.
func Paths(entries []string, workDir string) ([]string, error) {
	var files []string
	seen := map[string]bool{}
	for _, entry := range entries {
		paths, err := entryPaths(entry)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			matches, err := expand(path, workDir)
			if err != nil {
				return nil, err
			}
			for _, match := range matches {
				if seen[match] {
					continue
				}
				seen[match] = true
				files = append(files, match)
			}
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("files names no file")
	}
	return files, nil
}

// entryPaths returns the paths and patterns one entry names.
func entryPaths(entry string) ([]string, error) {
	text := strings.TrimSpace(entry)
	if strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{") {
		var value any
		if json.Unmarshal([]byte(text), &value) == nil {
			return jsonPaths(value)
		}
	}
	var paths []string
	for line := range strings.Lines(text) {
		if path := strings.TrimSpace(line); path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// jsonPaths returns the paths a JSON value lists.
func jsonPaths(value any) ([]string, error) {
	items, isList := value.([]any)
	if !isList {
		items = []any{value}
	}
	paths := make([]string, 0, len(items))
	for _, item := range items {
		switch item := item.(type) {
		case string:
			if strings.TrimSpace(item) != "" {
				paths = append(paths, strings.TrimSpace(item))
			}
		case map[string]any:
			// An email without attachments lists them as null.
			if attachments, isEmail := item["attachments"]; isEmail {
				list, _ := attachments.([]any)
				saved, err := jsonPaths(list)
				if err != nil {
					return nil, err
				}
				paths = append(paths, saved...)
				continue
			}
			path, _ := item["path"].(string)
			if strings.TrimSpace(path) == "" {
				name, _ := item["name"].(string)
				return nil, fmt.Errorf("files lists %s without a path; save mail attachments with save_attachments: true", listedName(name))
			}
			paths = append(paths, strings.TrimSpace(path))
		default:
			return nil, fmt.Errorf("files lists %v, which is neither a path nor an object with a path", item)
		}
	}
	return paths, nil
}

// listedName names a listed object by its name, if it has one.
func listedName(name string) string {
	if name == "" {
		return "an object"
	}
	return fmt.Sprintf("%q", name)
}

// expand returns the file at path, or the files a glob pattern matches,
// sorted.
func expand(path, workDir string) ([]string, error) {
	resolved, err := abs(path, workDir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(resolved); err == nil || !strings.ContainsAny(path, "*?[") {
		return []string{resolved}, nil
	}
	matches, err := filepath.Glob(resolved)
	if err != nil {
		return nil, fmt.Errorf("files pattern %s: %w", path, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no file matches %s", resolved)
	}
	slices.Sort(matches)
	return matches, nil
}

// abs makes path absolute: a leading ~ is the home directory, and a
// relative path is under workDir.
func abs(path, workDir string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", path, err)
		}
		return filepath.Join(home, path[1:]), nil
	}
	if !filepath.IsAbs(path) && workDir != "" {
		path = filepath.Join(workDir, path)
	}
	return filepath.Abs(path)
}
