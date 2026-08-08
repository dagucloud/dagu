// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package doc

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
	"github.com/dagucloud/dagu/v2/internal/dagstore"
	"github.com/dagucloud/dagu/v2/internal/docs"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dag/grep"
)

// Search searches all docs for the given query pattern.
func (s *Store) Search(ctx context.Context, query string) ([]*docs.DocSearchResult, error) {
	var results []*docs.DocSearchResult

	candidates, err := s.listSearchCandidates(ctx, "", "", nil, nil)
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		data, _, err := readRegularDocFile(candidate.AbsPath)
		if err != nil {
			logger.Warn(ctx, "Failed to read doc while searching", tag.File(candidate.RelPath), tag.Error(err))
			continue
		}

		matches, err := grep.Grep(data, query, grep.DefaultGrepOptions)
		if err != nil {
			continue
		}

		doc, parseErr := parseDocFile(data, candidate.ID)
		title := candidate.ID
		var description string
		var docTags []string
		if parseErr == nil {
			title = doc.Title
			description = doc.Description
			docTags = doc.Tags
		}

		results = append(results, &docs.DocSearchResult{
			ID:          candidate.ID,
			Title:       title,
			Description: description,
			Tags:        docTags,
			ModTime:     candidate.ModTime,
			Matches:     matches,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].ID < results[j].ID
	})

	return results, nil
}

func docSearchPattern(query string) string {
	return fmt.Sprintf("(?i)%s", regexp.QuoteMeta(query))
}

type docSearchCursor struct {
	Version       int      `json:"v"`
	Query         string   `json:"q"`
	PathPrefix    string   `json:"prefix,omitempty"`
	FilterPrefix  string   `json:"filter,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	ExcludedRoots []string `json:"exclude,omitempty"`
	ID            string   `json:"id,omitempty"`
}

type docMatchCursor struct {
	Version    int    `json:"v"`
	Query      string `json:"q"`
	PathPrefix string `json:"prefix,omitempty"`
	ID         string `json:"id"`
	Offset     int    `json:"offset"`
}

type docSearchCandidate struct {
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
) ([]docSearchCandidate, error) {
	if err := s.ensureFreshIndex(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	candidates := make([]docSearchCandidate, 0, len(s.docs))
	for _, doc := range s.docs {
		if !doc.Readable || docPathRootExcluded(doc.ID, excludedRoots) {
			continue
		}
		if !docTagsMatch(doc.Tags, tagFilter) {
			continue
		}
		id, ok := relativeDocID(doc.ID, pathPrefix)
		if !ok || !docPathHasPrefix(id, filterPrefix) {
			continue
		}
		candidates = append(candidates, docSearchCandidate{
			ID:      id,
			RelPath: filepath.ToSlash(id + ".md"),
			AbsPath: doc.AbsPath,
			ModTime: doc.ModTime,
		})
	}
	s.mu.RUnlock()

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].ID < candidates[j].ID
	})
	return candidates, nil
}

func docPathHasPrefix(id, prefix string) bool {
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

func decodeDocSearchCursor(
	raw string,
	query string,
	pathPrefix string,
	filterPrefix string,
	tagFilter []string,
	excludedRoots []string,
) (docSearchCursor, error) {
	if raw == "" {
		return docSearchCursor{}, nil
	}
	var cursor docSearchCursor
	if err := pagination.DecodeSearchCursor(raw, &cursor); err != nil {
		return docSearchCursor{}, err
	}
	if cursor.Version != docSearchCursorVersion ||
		cursor.Query != query ||
		cursor.PathPrefix != pathPrefix ||
		cursor.FilterPrefix != filterPrefix ||
		!slices.Equal(cursor.Tags, tagFilter) ||
		!slices.Equal(cursor.ExcludedRoots, excludedRoots) {
		return docSearchCursor{}, pagination.ErrInvalidCursor
	}
	return cursor, nil
}

func decodeDocMatchCursor(raw, query, pathPrefix, id string) (docMatchCursor, error) {
	if raw == "" {
		return docMatchCursor{ID: id}, nil
	}
	var cursor docMatchCursor
	if err := pagination.DecodeSearchCursor(raw, &cursor); err != nil {
		return docMatchCursor{}, err
	}
	if cursor.Version != docSearchCursorVersion || cursor.Query != query || cursor.PathPrefix != pathPrefix || cursor.ID != id || cursor.Offset < 0 {
		return docMatchCursor{}, pagination.ErrInvalidCursor
	}
	return cursor, nil
}

// SearchCursor returns lightweight, cursor-based document search hits.
func (s *Store) SearchCursor(ctx context.Context, opts docs.SearchDocsOptions) (*pagination.CursorResult[docs.DocSearchResult], error) {
	if opts.Query == "" {
		return &pagination.CursorResult[docs.DocSearchResult]{Items: []docs.DocSearchResult{}}, nil
	}
	pathPrefix, err := cleanDocPathPrefix(opts.PathPrefix)
	if err != nil {
		return nil, err
	}
	filterPrefix, err := cleanDocPathPrefix(opts.FilterPrefix)
	if err != nil {
		return nil, err
	}
	tagFilter := normalizeDocTagFilter(opts.Tags)
	excludedRoots := normalizeExcludedPathRoots(opts.ExcludePathRoots)

	cursor, err := decodeDocSearchCursor(opts.Cursor, opts.Query, pathPrefix, filterPrefix, tagFilter, excludedRoots)
	if err != nil {
		return nil, err
	}

	limit := max(opts.Limit, 1)
	matchLimit := max(opts.MatchLimit, 1)
	results := make([]docs.DocSearchResult, 0, limit)
	pattern := docSearchPattern(opts.Query)
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
		data, _, err := readRegularDocFile(candidate.AbsPath)
		if err != nil {
			logger.Warn(ctx, "Failed to read doc while searching", tag.File(candidate.RelPath), tag.Error(err))
			continue
		}

		window, err := grep.GrepWindow(data, pattern, grep.GrepOptions{
			IsRegexp: true,
			Before:   grep.DefaultGrepOptions.Before,
			After:    grep.DefaultGrepOptions.After,
			Limit:    matchLimit,
		})
		if err != nil {
			if errors.Is(err, grep.ErrNoMatch) {
				continue
			}
			logger.Warn(ctx, "Failed to search doc", tag.File(candidate.RelPath), tag.Error(err))
			continue
		}

		if len(results) == limit {
			hasMore = true
			nextCursor = pagination.EncodeSearchCursor(docSearchCursor{
				Version:       docSearchCursorVersion,
				Query:         opts.Query,
				PathPrefix:    pathPrefix,
				FilterPrefix:  filterPrefix,
				Tags:          tagFilter,
				ExcludedRoots: excludedRoots,
				ID:            results[len(results)-1].ID,
			})
			break
		}

		doc, parseErr := parseDocFile(data, candidate.ID)
		title := candidate.ID
		var description string
		var docTags []string
		if parseErr == nil {
			title = doc.Title
			description = doc.Description
			docTags = doc.Tags
		}
		item := docs.DocSearchResult{
			ID:             candidate.ID,
			Title:          title,
			Description:    description,
			Tags:           docTags,
			ModTime:        candidate.ModTime,
			Matches:        window.Matches,
			HasMoreMatches: window.HasMore,
		}
		if window.HasMore {
			item.NextMatchesCursor = pagination.EncodeSearchCursor(docMatchCursor{
				Version:    docSearchCursorVersion,
				Query:      opts.Query,
				PathPrefix: pathPrefix,
				ID:         candidate.ID,
				Offset:     window.NextOffset,
			})
		}
		results = append(results, item)
	}

	return &pagination.CursorResult[docs.DocSearchResult]{
		Items:      results,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

// SearchMatches returns cursor-based snippets for one document.
func (s *Store) SearchMatches(_ context.Context, id string, opts docs.SearchDocMatchesOptions) (*pagination.CursorResult[*dagstore.Match], error) {
	if err := docs.ValidateDocID(id); err != nil {
		return nil, err
	}
	if opts.Query == "" {
		return &pagination.CursorResult[*dagstore.Match]{Items: []*dagstore.Match{}}, nil
	}
	pathPrefix, err := cleanDocPathPrefix(opts.PathPrefix)
	if err != nil {
		return nil, err
	}

	cursor, err := decodeDocMatchCursor(opts.Cursor, opts.Query, pathPrefix, id)
	if err != nil {
		return nil, err
	}

	storedID, err := scopedDocID(pathPrefix, id)
	if err != nil {
		return nil, err
	}
	path, err := s.docFilePath(storedID)
	if err != nil {
		return nil, err
	}
	data, _, err := readRegularDocFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, docs.ErrDocNotFound
		}
		if errors.Is(err, docs.ErrDocNotFound) {
			return nil, docs.ErrDocNotFound
		}
		return nil, err
	}

	window, err := grep.GrepWindow(data, docSearchPattern(opts.Query), grep.GrepOptions{
		IsRegexp: true,
		Before:   grep.DefaultGrepOptions.Before,
		After:    grep.DefaultGrepOptions.After,
		Offset:   cursor.Offset,
		Limit:    max(opts.Limit, 1),
	})
	if err != nil {
		if errors.Is(err, grep.ErrNoMatch) {
			return &pagination.CursorResult[*dagstore.Match]{Items: []*dagstore.Match{}}, nil
		}
		return nil, err
	}

	result := &pagination.CursorResult[*dagstore.Match]{
		Items:   window.Matches,
		HasMore: window.HasMore,
	}
	if window.HasMore {
		result.NextCursor = pagination.EncodeSearchCursor(docMatchCursor{
			Version:    docSearchCursorVersion,
			Query:      opts.Query,
			PathPrefix: pathPrefix,
			ID:         id,
			Offset:     window.NextOffset,
		})
	}
	return result, nil
}
