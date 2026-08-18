// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
)

func (r *DAGRepository) List(ctx context.Context, opts DAGListOptions) (pagination.PaginatedResult[DAGListItem], []string, error) {
	return r.list(ctx, opts, false)
}

// ListIncludingSearchPaths is like List but also includes DAG definitions found
// under the store's additional search paths (for example alt_dags_dir). The
// combined collection is filtered, sorted, and paginated as a whole.
func (r *DAGRepository) ListIncludingSearchPaths(ctx context.Context, opts DAGListOptions) (pagination.PaginatedResult[DAGListItem], []string, error) {
	return r.list(ctx, opts, true)
}

func (r *DAGRepository) list(ctx context.Context, opts DAGListOptions, includeSearchPaths bool) (pagination.PaginatedResult[DAGListItem], []string, error) {
	if opts.Paginator == nil {
		paginator := pagination.DefaultPaginator()
		opts.Paginator = &paginator
	}

	var (
		catalog DAGCatalog
		err     error
	)
	if includeSearchPaths {
		catalog, err = r.store.CatalogIncludingSearchPaths(ctx)
	} else {
		catalog, err = r.store.Catalog(ctx)
	}
	if err != nil {
		return pagination.NewPaginatedResult([]DAGListItem{}, 0, *opts.Paginator), catalog.Issues, err
	}

	labelFilters := parseLabelFilters(opts.Labels)
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
		if !containsAllLabels(item.Labels, labelFilters) || !opts.WorkspaceFilter.MatchesLabels(item.Labels) {
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

func matchesDAGListSearch(name, id, query string) bool {
	query = strings.ToLower(query)
	return strings.Contains(strings.ToLower(name), query) || strings.Contains(strings.ToLower(id), query)
}

func parseLabelFilters(filters []string) []ir.LabelFilter {
	parsed := make([]ir.LabelFilter, 0, len(filters))
	for _, filter := range filters {
		if filter = strings.TrimSpace(filter); filter != "" {
			parsed = append(parsed, ir.ParseLabelFilter(filter))
		}
	}
	return parsed
}

func containsAllLabels(labels ir.Labels, filters []ir.LabelFilter) bool {
	return labels.MatchesFilters(filters)
}
