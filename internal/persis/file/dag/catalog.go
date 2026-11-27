// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dag/dagindex"
	indexv1 "github.com/dagucloud/dagu/v2/proto/index/v1"
)

type catalog struct {
	entries []*indexv1.DAGIndexEntry
	byStem  map[string]*indexv1.DAGIndexEntry
	errors  []string
	// skippedLinks holds the stems of file symlinks that discovery passed
	// over because they lead out of the DAG directory.
	skippedLinks map[string]struct{}
}

func (store *Store) loadCatalog(ctx context.Context, includeSearchPaths bool) (*catalog, error) {
	scan, err := Discover(store.baseDir, store.discoveryOptions(store.recursive))
	if err != nil {
		return nil, err
	}

	entries, err := store.loadIndex(ctx, store.withoutBaseConfig(scan.Files))
	if err != nil {
		return nil, err
	}
	allErrors := append([]error(nil), scan.Errors...)

	if includeSearchPaths && len(store.searchPaths) > 1 {
		flags, err := store.readSuspendFlags(ctx)
		if err != nil {
			return nil, err
		}
		for _, searchPath := range store.searchPaths[1:] {
			altScan, err := Discover(searchPath, store.discoveryOptions(store.recursive))
			if err != nil {
				return nil, err
			}
			allErrors = append(allErrors, altScan.Errors...)
			altScan.Files = store.withoutBaseConfig(altScan.Files)
			altFiles := make([]dagindex.YAMLFileMeta, 0, len(altScan.Files))
			for _, file := range altScan.Files {
				rel, err := filepath.Rel(store.baseDir, filepath.Join(searchPath, filepath.FromSlash(file.RelPath)))
				if err != nil {
					continue
				}
				altFiles = append(altFiles, dagindex.YAMLFileMeta{
					Name:     filepath.ToSlash(rel),
					LoadPath: file.ResolvedPath,
					Size:     file.Size,
					ModTime:  file.ModTime,
				})
			}
			altIdx := dagindex.Build(ctx, store.baseDir, altFiles, flags, store.defaultLoadOptions()...)
			entries = append(entries, altIdx.Entries...)
		}
	}

	return newCatalog(entries, allErrors, store.recursive), nil
}

// withoutBaseConfig drops the global base config from discovered files. The
// base config applies to every DAG but is not one, even when it lives in a DAG
// directory.
func (store *Store) withoutBaseConfig(files []DiscoveredFile) []DiscoveredFile {
	baseConfig := store.resolvedBaseConfig()
	if baseConfig == "" {
		return files
	}
	return slices.DeleteFunc(files, func(file DiscoveredFile) bool {
		return file.ResolvedPath == baseConfig
	})
}

// resolvedBaseConfig returns the absolute path of the base config with its
// links resolved, or "" when the store has none or the file does not exist.
func (store *Store) resolvedBaseConfig() string {
	if store.baseConfigPath == "" {
		return ""
	}
	baseConfig, err := filepath.Abs(store.baseConfigPath)
	if err != nil {
		return ""
	}
	baseConfig, err = filepath.EvalSymlinks(baseConfig)
	if err != nil {
		return ""
	}
	return baseConfig
}

func newCatalog(entries []*indexv1.DAGIndexEntry, scanErrors []error, checkConflicts bool) *catalog {
	result := &catalog{
		byStem:       make(map[string]*indexv1.DAGIndexEntry),
		skippedLinks: make(map[string]struct{}),
	}

	stemGroups := make(map[string][]string)
	nameGroups := make(map[string][]string)
	for _, entry := range entries {
		entry.FilePath = filepath.ToSlash(entry.FilePath)
		stem := entryStem(entry)
		if entry.Name == "" {
			entry.Name = stem
		}
		stemGroups[stem] = append(stemGroups[stem], entry.FilePath)
		nameGroups[entry.Name] = append(nameGroups[entry.Name], entry.FilePath)
	}

	conflicted := make(map[string]struct{})
	appendCollisions := func(kind string, groups map[string][]string) {
		keys := make([]string, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			paths := groups[key]
			sort.Strings(paths)
			if len(paths) < 2 {
				continue
			}
			for _, path := range paths {
				conflicted[path] = struct{}{}
			}
			result.errors = append(result.errors,
				fmt.Sprintf("duplicate DAG %s %q: %s", kind, key, strings.Join(paths, ", ")))
		}
	}
	if checkConflicts {
		appendCollisions("file name", stemGroups)
		appendCollisions("name", nameGroups)
	}

	for _, entry := range entries {
		if _, excluded := conflicted[entry.FilePath]; excluded {
			continue
		}
		result.entries = append(result.entries, entry)
		result.byStem[entryStem(entry)] = entry
	}
	sort.Slice(result.entries, func(i, j int) bool {
		left, right := entryStem(result.entries[i]), entryStem(result.entries[j])
		if left == right {
			return result.entries[i].FilePath < result.entries[j].FilePath
		}
		return left < right
	})

	for _, err := range scanErrors {
		result.errors = append(result.errors, fmt.Sprintf("DAG discovery failed: %s", err))
		if skipped, ok := errors.AsType[*skippedSymlinkError](err); ok {
			result.skippedLinks[fileutil.TrimYAMLFileExtension(path.Base(skipped.relPath))] = struct{}{}
		}
	}
	sort.Strings(result.errors)
	return result
}

func entryStem(entry *indexv1.DAGIndexEntry) string {
	base := filepath.Base(filepath.FromSlash(entry.FilePath))
	return strings.TrimSuffix(base, filepath.Ext(base))
}
