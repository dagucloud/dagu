// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/persis/file/dag/dagindex"
	indexv1 "github.com/dagucloud/dagu/v2/proto/index/v1"
)

type catalog struct {
	entries []*indexv1.DAGIndexEntry
	byStem  map[string]*indexv1.DAGIndexEntry
	errors  []string
}

func (store *Store) loadCatalog(ctx context.Context, includeSearchPaths bool) (*catalog, error) {
	scan, err := Discover(store.baseDir, DiscoveryOptions{
		Recursive: store.recursive,
		Symlinks:  store.symlinks,
	})
	if err != nil {
		return nil, err
	}

	entries, err := store.loadIndex(ctx, scan.Files)
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
			altScan, err := Discover(searchPath, DiscoveryOptions{
				Recursive: store.recursive,
				Symlinks:  store.symlinks,
			})
			if err != nil {
				return nil, err
			}
			allErrors = append(allErrors, altScan.Errors...)
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

func newCatalog(entries []*indexv1.DAGIndexEntry, scanErrors []error, checkConflicts bool) *catalog {
	result := &catalog{
		byStem: make(map[string]*indexv1.DAGIndexEntry),
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
	}
	sort.Strings(result.errors)
	return result
}

func entryStem(entry *indexv1.DAGIndexEntry) string {
	base := filepath.Base(filepath.FromSlash(entry.FilePath))
	return strings.TrimSuffix(base, filepath.Ext(base))
}
