// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/textsearch"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const dagSearchCursorVersion = 1

// DAGRepository provides application-level access to DAG definitions.
type DAGRepository struct {
	store   DAGDefinitionStore
	options DAGRepositoryOptions
}

// NewDAGRepository creates a repository backed by the given definition store.
func NewDAGRepository(store DAGDefinitionStore, options DAGRepositoryOptions) *DAGRepository {
	return &DAGRepository{store: store, options: options}
}

func (r *DAGRepository) loadOptions(opts ...spec.LoadOption) []spec.LoadOption {
	loadOpts := make([]spec.LoadOption, 0, len(opts)+2)
	if r.options.BaseConfigPath != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfig(r.options.BaseConfigPath))
	}
	if r.options.WorkspaceBaseConfigDir != "" {
		loadOpts = append(loadOpts, spec.WithWorkspaceBaseConfigDir(r.options.WorkspaceBaseConfigDir))
	}
	return append(loadOpts, opts...)
}

func repositoryLoadOptions(opts DAGLoadOptions) []spec.LoadOption {
	if opts.AllowBuildErrors {
		return []spec.LoadOption{spec.WithAllowBuildErrors()}
	}
	return nil
}

func (r *DAGRepository) Create(ctx context.Context, id string, source []byte) error {
	return r.store.Create(ctx, id, source)
}

func (r *DAGRepository) Delete(ctx context.Context, id string) error {
	return r.store.Delete(ctx, id)
}

func (r *DAGRepository) Rename(ctx context.Context, oldID, newID string) error {
	return r.store.Rename(ctx, oldID, newID)
}

func (r *DAGRepository) GetMetadata(ctx context.Context, id string) (*ir.DAG, error) {
	return r.store.GetMetadata(ctx, id)
}

func (r *DAGRepository) GetDetails(ctx context.Context, id string, opts DAGLoadOptions) (*ir.DAG, error) {
	definition, err := r.store.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to locate DAG %s: %w", id, err)
	}

	loadOpts := r.loadOptions(repositoryLoadOptions(opts)...)
	loadOpts = append(loadOpts, spec.WithoutEval())
	var dag *ir.DAG
	if definition.SourcePath != "" {
		dag, err = spec.LoadYAMLAt(ctx, definition.Source, definition.SourcePath, loadOpts...)
	} else {
		loadOpts = append(loadOpts, spec.WithName(definition.ID))
		dag, err = spec.LoadYAML(ctx, definition.Source, loadOpts...)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load DAG %s: %w", id, err)
	}
	return dag, nil
}

func (r *DAGRepository) GetSpec(ctx context.Context, id string) (string, error) {
	definition, err := r.store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return string(definition.Source), nil
}

func (r *DAGRepository) LoadSpec(ctx context.Context, source []byte, name string, opts DAGLoadOptions) (*ir.DAG, error) {
	loadOpts := r.loadOptions(repositoryLoadOptions(opts)...)
	loadOpts = append(loadOpts, spec.WithName(name), spec.WithoutEval())
	return spec.LoadYAML(ctx, source, loadOpts...)
}

func (r *DAGRepository) UpdateSpec(ctx context.Context, id string, source []byte) error {
	definition, err := r.store.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to locate DAG %s: %w", id, err)
	}

	loadOpts := r.loadOptions(spec.WithoutEval())
	var dag *ir.DAG
	if definition.SourcePath != "" {
		dag, err = spec.LoadYAMLAt(ctx, source, definition.SourcePath, loadOpts...)
	} else {
		loadOpts = append(loadOpts, spec.WithName(id))
		dag, err = spec.LoadYAML(ctx, source, loadOpts...)
	}
	if err != nil {
		return err
	}
	if err := dag.Validate(); err != nil {
		return err
	}
	return r.store.Update(ctx, id, source)
}

func (r *DAGRepository) SetSuspended(ctx context.Context, id string, suspended bool) error {
	return r.store.SetSuspended(ctx, id, suspended)
}

func (r *DAGRepository) IsSuspended(ctx context.Context, id string) (bool, error) {
	return r.store.IsSuspended(ctx, id)
}

func (r *DAGRepository) List(ctx context.Context, opts DAGListOptions) (pagination.PaginatedResult[DAGListItem], []string, error) {
	if opts.Paginator == nil {
		paginator := pagination.DefaultPaginator()
		opts.Paginator = &paginator
	}

	catalog, err := r.store.Catalog(ctx)
	if err != nil {
		return pagination.NewPaginatedResult([]DAGListItem{}, 0, *opts.Paginator), catalog.Issues, err
	}

	items := make([]DAGListItem, 0, len(catalog.Items))
	for _, item := range catalog.Items {
		if err := ctx.Err(); err != nil {
			return pagination.NewPaginatedResult([]DAGListItem{}, 0, *opts.Paginator), catalog.Issues, err
		}
		if item.DAG == nil || opts.ActiveOnly && (len(item.Schedule) == 0 || item.Suspended) {
			continue
		}
		if opts.Name != "" && !matchesDAGListSearch(item.Name, item.ID, opts.Name) {
			continue
		}
		if !containsAllLabels(item.Labels, opts.Labels) || !opts.WorkspaceFilter.MatchesLabels(item.Labels) {
			continue
		}
		items = append(items, item)
	}

	sortDAGList(items, opts)
	totalCount := len(items)
	start := opts.Paginator.Offset()
	end := min(start+opts.Paginator.Limit(), totalCount)
	if start >= totalCount {
		items = nil
	} else {
		items = items[start:end]
	}

	return pagination.NewPaginatedResult(items, totalCount, *opts.Paginator), catalog.Issues, nil
}

func sortDAGList(items []DAGListItem, opts DAGListOptions) {
	ascending := opts.Order != "desc"
	if opts.Sort != "nextRun" {
		sort.Slice(items, func(i, j int) bool {
			left, right := strings.ToLower(items[i].Name), strings.ToLower(items[j].Name)
			if ascending {
				return left < right
			}
			return left > right
		})
		return
	}

	now := time.Now()
	if opts.Time != nil {
		now = *opts.Time
	}
	project := opts.NextRunProjection
	if project == nil {
		project = func(dag *ir.DAG, at time.Time) time.Time { return dag.NextRun(at) }
	}
	nextRuns := make(map[*ir.DAG]time.Time, len(items))
	for _, item := range items {
		if !item.Suspended {
			nextRuns[item.DAG] = project(item.DAG, now)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := nextRuns[items[i].DAG], nextRuns[items[j].DAG]
		if left.IsZero() && right.IsZero() {
			if ascending {
				return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
			}
			return strings.ToLower(items[i].Name) > strings.ToLower(items[j].Name)
		}
		if left.IsZero() {
			return false
		}
		if right.IsZero() {
			return true
		}
		if ascending {
			return left.Before(right)
		}
		return right.Before(left)
	})
}

func (r *DAGRepository) LabelList(ctx context.Context) ([]string, []string, error) {
	catalog, err := r.store.Catalog(ctx)
	if err != nil {
		return nil, catalog.Issues, err
	}

	labels := make(map[string]struct{})
	for _, item := range catalog.Items {
		if item.DAG == nil || len(item.BuildErrors) > 0 {
			continue
		}
		for _, label := range item.Labels {
			value := label.String()
			labels[strings.ToLower(value)] = struct{}{}
			if key, _, ok := strings.Cut(value, "="); ok {
				labels[strings.ToLower(key)] = struct{}{}
			}
		}
	}

	result := make([]string, 0, len(labels))
	for label := range labels {
		result = append(result, label)
	}
	sort.Strings(result)
	return result, catalog.Issues, nil
}

func (r *DAGRepository) Grep(ctx context.Context, pattern string) ([]*DAGGrepResult, []string, error) {
	if pattern == "" {
		return nil, nil, nil
	}
	catalog, err := r.store.Catalog(ctx)
	if err != nil {
		return nil, catalog.Issues, err
	}

	issues := append([]string(nil), catalog.Issues...)
	results := make([]*DAGGrepResult, 0)
	for _, item := range sortedDAGCatalogItems(catalog.Items) {
		id := item.ID
		definition, err := r.store.Get(ctx, id)
		if err != nil {
			issues = append(issues, fmt.Sprintf("read %s failed: %s", id, err))
			continue
		}
		matches, err := textsearch.Grep(definition.Source, fmt.Sprintf("(?i)%s", pattern), textsearch.DefaultGrepOptions)
		if errors.Is(err, textsearch.ErrNoMatch) {
			continue
		}
		if err != nil {
			issues = append(issues, fmt.Sprintf("grep %s failed: %s", id, err))
			continue
		}
		dag, err := r.store.GetMetadata(ctx, id)
		if err != nil {
			issues = append(issues, fmt.Sprintf("check %s failed: %s", id, err))
			continue
		}
		results = append(results, &DAGGrepResult{Name: id, DAG: dag, Matches: matches})
	}
	return results, issues, nil
}

type dagSearchCursor struct {
	Version  int    `json:"v"`
	Query    string `json:"q"`
	Labels   string `json:"labels,omitempty"`
	FileName string `json:"fileName,omitempty"`
}

type dagMatchCursor struct {
	Version  int    `json:"v"`
	Query    string `json:"q"`
	Labels   string `json:"labels,omitempty"`
	FileName string `json:"fileName"`
	Offset   int    `json:"offset"`
}

func (r *DAGRepository) SearchCursor(ctx context.Context, opts DAGSearchOptions) (*pagination.CursorResult[DAGSearchResult], []string, error) {
	if opts.Query == "" {
		return &pagination.CursorResult[DAGSearchResult]{Items: []DAGSearchResult{}}, nil, nil
	}

	scope := dagSearchScopeKey(opts.Labels, opts.WorkspaceFilter)
	cursor, err := decodeDAGSearchCursor(opts.Cursor, opts.Query, scope)
	if err != nil {
		return nil, nil, err
	}
	catalog, err := r.store.Catalog(ctx)
	if err != nil {
		return nil, catalog.Issues, err
	}

	limit := max(opts.Limit, 1)
	matchLimit := max(opts.MatchLimit, 1)
	pattern := dagSearchPattern(opts.Query)
	issues := append([]string(nil), catalog.Issues...)
	results := make([]DAGSearchResult, 0, limit)
	var hasMore bool
	var nextCursor string

	for _, item := range sortedDAGCatalogItems(catalog.Items) {
		if err := ctx.Err(); err != nil {
			return nil, issues, err
		}
		id := item.ID
		if cursor.FileName != "" && id <= cursor.FileName {
			continue
		}
		if !containsAllLabels(item.Labels, opts.Labels) || !opts.WorkspaceFilter.MatchesLabels(item.Labels) {
			continue
		}
		definition, err := r.store.Get(ctx, id)
		if err != nil {
			issues = append(issues, fmt.Sprintf("read %s failed: %s", id, err))
			continue
		}
		window, err := textsearch.GrepWindow(definition.Source, pattern, textsearch.GrepOptions{
			IsRegexp: true,
			Before:   textsearch.DefaultGrepOptions.Before,
			After:    textsearch.DefaultGrepOptions.After,
			Limit:    matchLimit,
		})
		if errors.Is(err, textsearch.ErrNoMatch) {
			continue
		}
		if err != nil {
			issues = append(issues, fmt.Sprintf("grep %s failed: %s", id, err))
			continue
		}
		if len(results) == limit {
			hasMore = true
			nextCursor = pagination.EncodeSearchCursor(dagSearchCursor{
				Version: dagSearchCursorVersion, Query: opts.Query, Labels: scope,
				FileName: results[len(results)-1].FileName,
			})
			break
		}

		workspaceName := ""
		if name, ok := workspace.WorkspaceNameFromLabels(item.Labels); ok {
			workspaceName = name
		}
		result := DAGSearchResult{
			Name: id, FileName: id, Workspace: workspaceName,
			Matches: window.Matches, HasMoreMatches: window.HasMore,
		}
		if window.HasMore {
			result.NextMatchesCursor = pagination.EncodeSearchCursor(dagMatchCursor{
				Version: dagSearchCursorVersion, Query: opts.Query, Labels: scope,
				FileName: id, Offset: window.NextOffset,
			})
		}
		results = append(results, result)
	}

	return &pagination.CursorResult[DAGSearchResult]{Items: results, HasMore: hasMore, NextCursor: nextCursor}, issues, nil
}

func (r *DAGRepository) SearchMatches(ctx context.Context, id string, opts DAGMatchSearchOptions) (*pagination.CursorResult[*textsearch.Match], error) {
	if opts.Query == "" {
		return &pagination.CursorResult[*textsearch.Match]{Items: []*textsearch.Match{}}, nil
	}

	scope := dagSearchScopeKey(opts.Labels, opts.WorkspaceFilter)
	cursor, err := decodeDAGMatchCursor(opts.Cursor, opts.Query, scope, id)
	if err != nil {
		return nil, err
	}
	definition, err := r.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(opts.Labels) > 0 || opts.WorkspaceFilter != nil {
		dag, err := r.store.GetMetadata(ctx, id)
		if err != nil {
			return nil, err
		}
		if !containsAllLabels(dag.Labels, opts.Labels) || !opts.WorkspaceFilter.MatchesLabels(dag.Labels) {
			return &pagination.CursorResult[*textsearch.Match]{Items: []*textsearch.Match{}}, nil
		}
	}

	window, err := textsearch.GrepWindow(definition.Source, dagSearchPattern(opts.Query), textsearch.GrepOptions{
		IsRegexp: true,
		Before:   textsearch.DefaultGrepOptions.Before,
		After:    textsearch.DefaultGrepOptions.After,
		Offset:   cursor.Offset,
		Limit:    max(opts.Limit, 1),
	})
	if errors.Is(err, textsearch.ErrNoMatch) {
		return &pagination.CursorResult[*textsearch.Match]{Items: []*textsearch.Match{}}, nil
	}
	if err != nil {
		return nil, err
	}

	result := &pagination.CursorResult[*textsearch.Match]{Items: window.Matches, HasMore: window.HasMore}
	if window.HasMore {
		result.NextCursor = pagination.EncodeSearchCursor(dagMatchCursor{
			Version: dagSearchCursorVersion, Query: opts.Query, Labels: scope,
			FileName: id, Offset: window.NextOffset,
		})
	}
	return result, nil
}

func dagSearchPattern(query string) string {
	return fmt.Sprintf("(?i)%s", regexp.QuoteMeta(query))
}

func dagSearchScopeKey(labels []string, filter *workspace.WorkspaceFilter) string {
	normalized := make([]string, 0, len(labels))
	for _, label := range labels {
		if label = strings.TrimSpace(strings.ToLower(label)); label != "" {
			normalized = append(normalized, label)
		}
	}
	sort.Strings(normalized)
	parts := []string{strings.Join(normalized, ",")}
	if filter != nil && filter.Enabled {
		workspaces := append([]string(nil), filter.Workspaces...)
		sort.Strings(workspaces)
		parts = append(parts, strings.Join(workspaces, ","))
		if filter.IncludeUnlabelled {
			parts = append(parts, "unlabelled")
		}
	}
	return strings.Join(parts, "|")
}

func decodeDAGSearchCursor(raw, query, labels string) (dagSearchCursor, error) {
	if raw == "" {
		return dagSearchCursor{}, nil
	}
	var cursor dagSearchCursor
	if err := pagination.DecodeSearchCursor(raw, &cursor); err != nil {
		return dagSearchCursor{}, err
	}
	if cursor.Version != dagSearchCursorVersion || cursor.Query != query || cursor.Labels != labels {
		return dagSearchCursor{}, pagination.ErrInvalidCursor
	}
	return cursor, nil
}

func decodeDAGMatchCursor(raw, query, labels, id string) (dagMatchCursor, error) {
	if raw == "" {
		return dagMatchCursor{FileName: id}, nil
	}
	var cursor dagMatchCursor
	if err := pagination.DecodeSearchCursor(raw, &cursor); err != nil {
		return dagMatchCursor{}, err
	}
	if cursor.Version != dagSearchCursorVersion || cursor.Query != query || cursor.Labels != labels || cursor.FileName != id || cursor.Offset < 0 {
		return dagMatchCursor{}, pagination.ErrInvalidCursor
	}
	return cursor, nil
}

func matchesDAGListSearch(name, id, query string) bool {
	query = strings.ToLower(query)
	return strings.Contains(strings.ToLower(name), query) || strings.Contains(strings.ToLower(id), query)
}

func containsAllLabels(labels ir.Labels, filters []string) bool {
	parsed := make([]ir.LabelFilter, 0, len(filters))
	for _, filter := range filters {
		if filter = strings.TrimSpace(filter); filter != "" {
			parsed = append(parsed, ir.ParseLabelFilter(filter))
		}
	}
	return labels.MatchesFilters(parsed)
}

func sortedDAGCatalogItems(items []DAGListItem) []DAGListItem {
	result := append([]DAGListItem(nil), items...)
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}
