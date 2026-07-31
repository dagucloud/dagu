// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/dagdiscovery"
	indexv1 "github.com/dagucloud/dagu/v2/proto/index/v1"
)

type catalog struct {
	entries []*indexv1.DAGIndexEntry
	byStem  map[string]*indexv1.DAGIndexEntry
	errors  []string
}

func (store *Storage) loadCatalog(ctx context.Context) (*catalog, error) {
	scan, err := dagdiscovery.Scan(store.baseDir, store.recursive)
	if err != nil {
		return nil, err
	}

	entries := store.loadIndex(ctx, scan.Files)
	return newCatalog(entries, scan.Errors, store.recursive), nil
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

func (store *Storage) entryPath(entry *indexv1.DAGIndexEntry) string {
	return filepath.Join(store.baseDir, filepath.FromSlash(entry.FilePath))
}
