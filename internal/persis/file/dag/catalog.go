// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/internal/dagdiscovery"
	indexv1 "github.com/dagucloud/dagu/proto/index/v1"
)

type dagCatalog struct {
	entries        []*indexv1.DAGIndexEntry
	byFileName     map[string]*indexv1.DAGIndexEntry
	discoveryError []string
}

func (store *Storage) loadCatalog(ctx context.Context) (*dagCatalog, error) {
	scan, err := dagdiscovery.Scan(store.baseDir, store.recursiveDiscovery)
	if err != nil {
		return nil, err
	}

	entries := store.loadOrRebuildIndexForFiles(ctx, scan.Files)
	return newDAGCatalog(entries, scan.Errors, store.recursiveDiscovery), nil
}

func newDAGCatalog(entries []*indexv1.DAGIndexEntry, scanErrors []error, enforceUniqueness bool) *dagCatalog {
	catalog := &dagCatalog{
		byFileName: make(map[string]*indexv1.DAGIndexEntry),
	}

	fileNameGroups := make(map[string][]string)
	nameGroups := make(map[string][]string)
	for _, entry := range entries {
		entry.FilePath = filepath.ToSlash(entry.FilePath)
		fileName := dagEntryFileName(entry)
		if entry.Name == "" {
			entry.Name = fileName
		}
		fileNameGroups[fileName] = append(fileNameGroups[fileName], entry.FilePath)
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
			catalog.discoveryError = append(catalog.discoveryError,
				fmt.Sprintf("duplicate DAG %s %q: %s", kind, key, strings.Join(paths, ", ")))
		}
	}
	if enforceUniqueness {
		appendCollisions("file name", fileNameGroups)
		appendCollisions("name", nameGroups)
	}

	for _, entry := range entries {
		if _, excluded := conflicted[entry.FilePath]; excluded {
			continue
		}
		catalog.entries = append(catalog.entries, entry)
		catalog.byFileName[dagEntryFileName(entry)] = entry
	}
	sort.Slice(catalog.entries, func(i, j int) bool {
		if enforceUniqueness {
			return dagEntryFileName(catalog.entries[i]) < dagEntryFileName(catalog.entries[j])
		}
		return catalog.entries[i].FilePath < catalog.entries[j].FilePath
	})

	for _, err := range scanErrors {
		catalog.discoveryError = append(catalog.discoveryError, fmt.Sprintf("DAG discovery failed: %s", err))
	}
	sort.Strings(catalog.discoveryError)
	return catalog
}

func dagEntryFileName(entry *indexv1.DAGIndexEntry) string {
	base := filepath.Base(filepath.FromSlash(entry.FilePath))
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func (store *Storage) catalogEntryPath(entry *indexv1.DAGIndexEntry) string {
	return filepath.Join(store.baseDir, filepath.FromSlash(entry.FilePath))
}
