// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package doc

import (
	"context"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core/docs"
)

// Backlinks returns metadata for documents whose wiki links resolve to target.
// The link graph is derived from the in-memory index, so results share the
// index freshness window with listing.
func (s *Store) Backlinks(ctx context.Context, target, pathPrefix string) ([]docs.DocMetadata, error) {
	if target == "" {
		return nil, nil
	}
	pathPrefix, err := cleanDocPathPrefix(pathPrefix)
	if err != nil {
		return nil, err
	}
	if err := s.ensureFreshIndex(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	var results []docs.DocMetadata
	for _, entry := range s.docs {
		if !entry.Readable || entry.ID == target {
			continue
		}
		if !docLinksTo(entry, target, pathPrefix) {
			continue
		}
		results = append(results, docs.DocMetadata{
			ID:          entry.ID,
			Title:       entry.Title,
			Description: entry.Description,
			Tags:        entry.Tags,
			ModTime:     entry.ModTime,
		})
	}
	s.mu.RUnlock()

	sort.Slice(results, func(i, j int) bool {
		return results[i].ID < results[j].ID
	})
	return results, nil
}

// docLinksTo reports whether entry holds a wiki link resolving to target.
// A link matches verbatim, or relative to pathPrefix when the linking
// document itself lives under pathPrefix.
func docLinksTo(entry docIndexEntry, target, pathPrefix string) bool {
	underPrefix := pathPrefix != "" &&
		strings.HasPrefix(entry.ID, pathPrefix+"/")
	for _, link := range entry.OutLinks {
		if link == target {
			return true
		}
		if underPrefix && pathPrefix+"/"+link == target {
			return true
		}
	}
	return false
}
