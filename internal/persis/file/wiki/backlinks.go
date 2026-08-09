// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wiki

import (
	"context"
	"sort"
	"strings"

	wikimodel "github.com/dagucloud/dagu/v2/internal/wiki"
)

// Backlinks returns metadata for pages whose wiki links resolve to target.
// The link graph is derived from the in-memory index, so results share the
// index freshness window with listing.
func (s *Store) Backlinks(ctx context.Context, target, pathPrefix string) ([]wikimodel.PageMetadata, error) {
	if target == "" {
		return nil, nil
	}
	pathPrefix, err := cleanPagePathPrefix(pathPrefix)
	if err != nil {
		return nil, err
	}
	if err := s.ensureFreshIndex(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	var results []wikimodel.PageMetadata
	for _, entry := range s.pages {
		if !entry.Readable || entry.ID == target {
			continue
		}
		if !wikiPageLinksTo(entry, target, pathPrefix) {
			continue
		}
		results = append(results, wikimodel.PageMetadata{
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

// wikiPageLinksTo reports whether entry holds a wiki link resolving to target.
// A link matches verbatim, or relative to pathPrefix when the linking
// page itself lives under pathPrefix.
func wikiPageLinksTo(entry pageIndexEntry, target, pathPrefix string) bool {
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
