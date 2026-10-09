// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"slices"
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

// sortDAGList lists pinned DAGs first and applies opts.Sort and opts.Order
// within each part. Remaining ties fall back to the ID so that pages stay
// stable between requests.
func sortDAGList(items []DAGListItem, opts DAGListOptions) {
	compare := dagListComparator(items, opts)
	slices.SortFunc(items, func(a, b DAGListItem) int {
		if c := comparePinned(opts.PinnedIDs, a.ID, b.ID); c != 0 {
			return c
		}
		if c := compare(a, b); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
}

func comparePinned(pinned map[string]struct{}, a, b string) int {
	_, aPinned := pinned[a]
	_, bPinned := pinned[b]
	switch {
	case aPinned == bPinned:
		return 0
	case aPinned:
		return -1
	default:
		return 1
	}
}

func dagListComparator(items []DAGListItem, opts DAGListOptions) func(a, b DAGListItem) int {
	direction := 1
	if opts.Order == "desc" {
		direction = -1
	}
	byName := func(a, b DAGListItem) int {
		return direction * strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	}
	if opts.Sort != "nextRun" {
		return byName
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
	return func(a, b DAGListItem) int {
		left, right := nextRuns[a.DAG], nextRuns[b.DAG]
		switch {
		case left.IsZero() && right.IsZero():
			return byName(a, b)
		case left.IsZero():
			// DAGs without a next run come last in either order.
			return 1
		case right.IsZero():
			return -1
		}
		if c := direction * left.Compare(right); c != 0 {
			return c
		}
		return byName(a, b)
	}
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
