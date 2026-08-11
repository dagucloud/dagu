// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wiki

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/textsearch"
	wikimodel "github.com/dagucloud/dagu/v2/internal/wiki"
)

// Search searches all pages for the given query pattern. Results are ordered
// by relevance: title and description hits outrank body-only hits, then more
// matches outrank fewer, with the ID as a stable tiebreak.
func (s *Store) Search(ctx context.Context, query string) ([]*wikimodel.PageSearchResult, error) {
	if query == "" {
		return nil, nil
	}
	var results []*wikimodel.PageSearchResult
	scores := map[string]int{}

	candidates, err := s.listSearchCandidates(ctx, "", "", nil, nil)
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		data, _, err := readRegularPageFile(candidate.AbsPath)
		if err != nil {
			logger.Warn(ctx, "Failed to read page while searching", tag.File(candidate.RelPath), tag.Error(err))
			continue
		}

		matches, matchCount, err := textsearch.GrepWithCount(data, wikiPageSearchPattern(query), textsearch.GrepOptions{
			IsRegexp: true,
			Before:   textsearch.DefaultGrepOptions.Before,
			After:    textsearch.DefaultGrepOptions.After,
		})
		if err != nil {
			continue
		}

		page, parseErr := parsePageFile(data, candidate.ID)
		title := candidate.ID
		var description string
		var wikiPageTags []string
		if parseErr == nil {
			title = page.Title
			description = page.Description
			wikiPageTags = page.Tags
		}

		scores[candidate.ID] = wikiPageSearchScore(query, title, description, matchCount)
		results = append(results, &wikimodel.PageSearchResult{
			ID:          candidate.ID,
			Title:       title,
			Description: description,
			Tags:        wikiPageTags,
			ModTime:     candidate.ModTime,
			Matches:     matches,
			MatchCount:  matchCount,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if scores[results[i].ID] != scores[results[j].ID] {
			return scores[results[i].ID] > scores[results[j].ID]
		}
		return results[i].ID < results[j].ID
	})

	return results, nil
}

// wikiPageSearchScore ranks a search hit. Title and description hits use the
// metadata already parsed from the file, so scoring adds no file I/O.
func wikiPageSearchScore(query, title, description string, matchCount int) int {
	score := min(matchCount, 50)
	lowered := strings.ToLower(query)
	if strings.Contains(strings.ToLower(title), lowered) {
		score += 100
	}
	if strings.Contains(strings.ToLower(description), lowered) {
		score += 40
	}
	return score
}

func wikiPageSearchPattern(query string) string {
	return fmt.Sprintf("(?i)%s", regexp.QuoteMeta(query))
}

type wikiPageSearchCursor struct {
	Version       int      `json:"v"`
	Query         string   `json:"q"`
	PathPrefix    string   `json:"prefix,omitempty"`
	FilterPrefix  string   `json:"filter,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	ExcludedRoots []string `json:"exclude,omitempty"`
	ID            string   `json:"id,omitempty"`
}

type wikiPageMatchCursor struct {
	Version    int    `json:"v"`
	Query      string `json:"q"`
	PathPrefix string `json:"prefix,omitempty"`
	ID         string `json:"id"`
	Offset     int    `json:"offset"`
}

type wikiPageSearchCandidate struct {
	ID      string
	RelPath string
	AbsPath string
	ModTime time.Time
}

func (s *Store) listSearchCandidates(
	ctx context.Context,
	pathPrefix string,
	filterPrefix string,
	tagFilter []string,
	excludedRoots []string,
) ([]wikiPageSearchCandidate, error) {
	if err := s.ensureFreshIndex(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	candidates := make([]wikiPageSearchCandidate, 0, len(s.pages))
	for _, page := range s.pages {
		if !page.Readable || wikiPagePathRootExcluded(page.ID, excludedRoots) {
			continue
		}
		if !wikiPageTagsMatch(page.Tags, tagFilter) {
			continue
		}
		id, ok := relativePageID(page.ID, pathPrefix)
		if !ok || !wikiPagePathHasPrefix(id, filterPrefix) {
			continue
		}
		candidates = append(candidates, wikiPageSearchCandidate{
			ID:      id,
			RelPath: filepath.ToSlash(id + ".md"),
			AbsPath: page.AbsPath,
			ModTime: page.ModTime,
		})
	}
	s.mu.RUnlock()

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].ID < candidates[j].ID
	})
	return candidates, nil
}

func wikiPagePathHasPrefix(id, prefix string) bool {
	return prefix == "" || id == prefix || strings.HasPrefix(id, prefix+"/")
}

func normalizeExcludedPathRoots(roots []string) []string {
	if len(roots) == 0 {
		return nil
	}
	normalized := slices.Clone(roots)
	sort.Strings(normalized)
	return slices.Compact(normalized)
}

func decodePageSearchCursor(
	raw string,
	query string,
	pathPrefix string,
	filterPrefix string,
	tagFilter []string,
	excludedRoots []string,
) (wikiPageSearchCursor, error) {
	if raw == "" {
		return wikiPageSearchCursor{}, nil
	}
	var cursor wikiPageSearchCursor
	if err := pagination.DecodeSearchCursor(raw, &cursor); err != nil {
		return wikiPageSearchCursor{}, err
	}
	if cursor.Version != pageSearchCursorVersion ||
		cursor.Query != query ||
		cursor.PathPrefix != pathPrefix ||
		cursor.FilterPrefix != filterPrefix ||
		!slices.Equal(cursor.Tags, tagFilter) ||
		!slices.Equal(cursor.ExcludedRoots, excludedRoots) {
		return wikiPageSearchCursor{}, pagination.ErrInvalidCursor
	}
	return cursor, nil
}

func decodeWikiPageMatchCursor(raw, query, pathPrefix, id string) (wikiPageMatchCursor, error) {
	if raw == "" {
		return wikiPageMatchCursor{ID: id}, nil
	}
	var cursor wikiPageMatchCursor
	if err := pagination.DecodeSearchCursor(raw, &cursor); err != nil {
		return wikiPageMatchCursor{}, err
	}
	if cursor.Version != pageSearchCursorVersion || cursor.Query != query || cursor.PathPrefix != pathPrefix || cursor.ID != id || cursor.Offset < 0 {
		return wikiPageMatchCursor{}, pagination.ErrInvalidCursor
	}
	return cursor, nil
}

// SearchCursor returns lightweight, cursor-based page search hits.
func (s *Store) SearchCursor(ctx context.Context, opts wikimodel.SearchPagesOptions) (*pagination.CursorResult[wikimodel.PageSearchResult], error) {
	if opts.Query == "" {
		return &pagination.CursorResult[wikimodel.PageSearchResult]{Items: []wikimodel.PageSearchResult{}}, nil
	}
	pathPrefix, err := cleanPagePathPrefix(opts.PathPrefix)
	if err != nil {
		return nil, err
	}
	filterPrefix, err := cleanPagePathPrefix(opts.FilterPrefix)
	if err != nil {
		return nil, err
	}
	tagFilter := normalizePageTagFilter(opts.Tags)
	excludedRoots := normalizeExcludedPathRoots(opts.ExcludePathRoots)

	cursor, err := decodePageSearchCursor(opts.Cursor, opts.Query, pathPrefix, filterPrefix, tagFilter, excludedRoots)
	if err != nil {
		return nil, err
	}

	limit := max(opts.Limit, 1)
	matchLimit := max(opts.MatchLimit, 1)
	results := make([]wikimodel.PageSearchResult, 0, limit)
	pattern := wikiPageSearchPattern(opts.Query)
	var hasMore bool
	var nextCursor string

	candidates, err := s.listSearchCandidates(ctx, pathPrefix, filterPrefix, tagFilter, excludedRoots)
	if err != nil {
		return nil, err
	}

	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if cursor.ID != "" && candidate.ID <= cursor.ID {
			continue
		}
		data, _, err := readRegularPageFile(candidate.AbsPath)
		if err != nil {
			logger.Warn(ctx, "Failed to read page while searching", tag.File(candidate.RelPath), tag.Error(err))
			continue
		}

		window, err := textsearch.GrepWindow(data, pattern, textsearch.GrepOptions{
			IsRegexp: true,
			Before:   textsearch.DefaultGrepOptions.Before,
			After:    textsearch.DefaultGrepOptions.After,
			Limit:    matchLimit,
		})
		if err != nil {
			if errors.Is(err, textsearch.ErrNoMatch) {
				continue
			}
			logger.Warn(ctx, "Failed to search page", tag.File(candidate.RelPath), tag.Error(err))
			continue
		}

		if len(results) == limit {
			hasMore = true
			nextCursor = pagination.EncodeSearchCursor(wikiPageSearchCursor{
				Version:       pageSearchCursorVersion,
				Query:         opts.Query,
				PathPrefix:    pathPrefix,
				FilterPrefix:  filterPrefix,
				Tags:          tagFilter,
				ExcludedRoots: excludedRoots,
				ID:            results[len(results)-1].ID,
			})
			break
		}

		page, parseErr := parsePageFile(data, candidate.ID)
		title := candidate.ID
		var description string
		var wikiPageTags []string
		if parseErr == nil {
			title = page.Title
			description = page.Description
			wikiPageTags = page.Tags
		}
		item := wikimodel.PageSearchResult{
			ID:             candidate.ID,
			Title:          title,
			Description:    description,
			Tags:           wikiPageTags,
			ModTime:        candidate.ModTime,
			Matches:        window.Matches,
			HasMoreMatches: window.HasMore,
		}
		if window.HasMore {
			item.NextMatchesCursor = pagination.EncodeSearchCursor(wikiPageMatchCursor{
				Version:    pageSearchCursorVersion,
				Query:      opts.Query,
				PathPrefix: pathPrefix,
				ID:         candidate.ID,
				Offset:     window.NextOffset,
			})
		}
		results = append(results, item)
	}

	return &pagination.CursorResult[wikimodel.PageSearchResult]{
		Items:      results,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

// SearchMatches returns cursor-based snippets for one page.
func (s *Store) SearchMatches(_ context.Context, id string, opts wikimodel.SearchPageMatchesOptions) (*pagination.CursorResult[*textsearch.Match], error) {
	if err := wikimodel.ValidatePageID(id); err != nil {
		return nil, err
	}
	if opts.Query == "" {
		return &pagination.CursorResult[*textsearch.Match]{Items: []*textsearch.Match{}}, nil
	}
	pathPrefix, err := cleanPagePathPrefix(opts.PathPrefix)
	if err != nil {
		return nil, err
	}

	cursor, err := decodeWikiPageMatchCursor(opts.Cursor, opts.Query, pathPrefix, id)
	if err != nil {
		return nil, err
	}

	storedID, err := scopedPageID(pathPrefix, id)
	if err != nil {
		return nil, err
	}
	path, err := s.wikiPageFilePath(storedID)
	if err != nil {
		return nil, err
	}
	data, _, err := readRegularPageFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, wikimodel.ErrPageNotFound
		}
		if errors.Is(err, wikimodel.ErrPageNotFound) {
			return nil, wikimodel.ErrPageNotFound
		}
		return nil, err
	}

	window, err := textsearch.GrepWindow(data, wikiPageSearchPattern(opts.Query), textsearch.GrepOptions{
		IsRegexp: true,
		Before:   textsearch.DefaultGrepOptions.Before,
		After:    textsearch.DefaultGrepOptions.After,
		Offset:   cursor.Offset,
		Limit:    max(opts.Limit, 1),
	})
	if err != nil {
		if errors.Is(err, textsearch.ErrNoMatch) {
			return &pagination.CursorResult[*textsearch.Match]{Items: []*textsearch.Match{}}, nil
		}
		return nil, err
	}

	result := &pagination.CursorResult[*textsearch.Match]{
		Items:   window.Matches,
		HasMore: window.HasMore,
	}
	if window.HasMore {
		result.NextCursor = pagination.EncodeSearchCursor(wikiPageMatchCursor{
			Version:    pageSearchCursorVersion,
			Query:      opts.Query,
			PathPrefix: pathPrefix,
			ID:         id,
			Offset:     window.NextOffset,
		})
	}
	return result, nil
}
