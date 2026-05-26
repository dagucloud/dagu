// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"container/heap"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun/dagrunindex"
)

type dagRunListKey struct {
	Timestamp time.Time
	Name      string
	DAGRunID  string
}

type dagRunListItem struct {
	Key    dagRunListKey
	Status *exec.DAGRunStatus
}

func compareDagRunListKeys(a, b dagRunListKey) int {
	switch {
	case a.Timestamp.After(b.Timestamp):
		return -1
	case a.Timestamp.Before(b.Timestamp):
		return 1
	case a.Name < b.Name:
		return -1
	case a.Name > b.Name:
		return 1
	case a.DAGRunID < b.DAGRunID:
		return -1
	case a.DAGRunID > b.DAGRunID:
		return 1
	default:
		return 0
	}
}

func (store *Store) ListStatusesPage(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	options, err := prepareListOptions(opts)
	if err != nil {
		return exec.DAGRunStatusPage{}, fmt.Errorf("failed to prepare options: %w", err)
	}

	items, nextCursor, err := store.listStatusesOrdered(ctx, options, options.Limit, true)
	if err != nil {
		return exec.DAGRunStatusPage{}, err
	}

	return exec.DAGRunStatusPage{
		Items:      items,
		NextCursor: nextCursor,
	}, nil
}

func (store *Store) listStatusesOrdered(
	ctx context.Context,
	opts exec.ListDAGRunStatusesOptions,
	limit int,
	returnCursor bool,
) ([]*exec.DAGRunStatus, string, error) {
	cursorKey, err := decodeQueryCursor(opts.Cursor, opts)
	if err != nil {
		return nil, "", err
	}

	iterators, err := store.newStatusIterators(ctx, opts)
	if err != nil {
		return nil, "", err
	}

	target := limit
	if target <= 0 {
		if opts.Unlimited {
			target = math.MaxInt
		} else {
			target = opts.Limit
		}
	}
	if target <= 0 {
		target = 1
	}
	if returnCursor && target < math.MaxInt {
		target++
	}

	pq := make(dagRunIteratorHeap, 0, len(iterators))
	for _, iterator := range iterators {
		item, err := iterator.Next(ctx)
		if err != nil {
			return nil, "", err
		}
		if item == nil {
			continue
		}
		pq = append(pq, dagRunHeapItem{
			Iterator: iterator,
			Item:     *item,
		})
	}
	heap.Init(&pq)

	statuses := make([]*exec.DAGRunStatus, 0, min(target, len(iterators)))
	keys := make([]dagRunListKey, 0, cap(statuses))

	for pq.Len() > 0 && len(statuses) < target {
		current := heap.Pop(&pq).(dagRunHeapItem)
		if opts.Cursor == "" || compareDagRunListKeys(current.Item.Key, cursorKey) > 0 {
			statuses = append(statuses, current.Item.Status)
			keys = append(keys, current.Item.Key)
		}

		nextItem, err := current.Iterator.Next(ctx)
		if err != nil {
			return nil, "", err
		}
		if nextItem != nil {
			current.Item = *nextItem
			heap.Push(&pq, current)
		}
	}

	if !returnCursor || limit <= 0 || len(statuses) <= limit {
		return statuses, "", nil
	}

	nextCursor, err := encodeQueryCursor(opts, keys[limit-1])
	if err != nil {
		return nil, "", err
	}
	return statuses[:limit], nextCursor, nil
}

func (store *Store) newStatusIterators(ctx context.Context, opts exec.ListDAGRunStatusesOptions) ([]*dagRunStatusIterator, error) {
	var roots []DataRoot
	if opts.ExactName == "" {
		listed, err := store.listRoot(ctx, "")
		if err != nil {
			return nil, fmt.Errorf("failed to list root directories: %w", err)
		}
		roots = listed
	} else {
		roots = append(roots, NewDataRoot(store.baseDir, opts.ExactName))
	}

	iterators := make([]*dagRunStatusIterator, 0, len(roots))
	for _, root := range roots {
		iterator, err := newDAGRunStatusIterator(store, root, opts)
		if err != nil {
			return nil, err
		}
		iterators = append(iterators, iterator)
	}

	return iterators, nil
}

type dagRunStatusIterator struct {
	store           *Store
	root            DataRoot
	opts            exec.ListDAGRunStatusesOptions
	dayPaths        []string
	dayIndex        int
	dayItems        []dagRunListItem
	dayItemIndex    int
	labelFilters    []core.LabelFilter
	statusesFilter  map[core.Status]struct{}
	hasStatusFilter bool
}

func newDAGRunStatusIterator(store *Store, root DataRoot, opts exec.ListDAGRunStatusesOptions) (*dagRunStatusIterator, error) {
	dayPaths, err := listDayPathsInRange(root, opts.From, opts.To)
	if err != nil {
		return nil, err
	}

	statusesFilter := make(map[core.Status]struct{}, len(opts.Statuses))
	for _, status := range opts.Statuses {
		statusesFilter[status] = struct{}{}
	}

	labelFilters := make([]core.LabelFilter, 0, len(opts.Labels))
	for _, label := range opts.Labels {
		if trimmed := strings.TrimSpace(label); trimmed != "" {
			labelFilters = append(labelFilters, core.ParseLabelFilter(trimmed))
		}
	}

	return &dagRunStatusIterator{
		store:           store,
		root:            root,
		opts:            opts,
		dayPaths:        dayPaths,
		labelFilters:    labelFilters,
		statusesFilter:  statusesFilter,
		hasStatusFilter: len(statusesFilter) > 0,
	}, nil
}

func (it *dagRunStatusIterator) Next(ctx context.Context) (*dagRunListItem, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if it.dayItemIndex < len(it.dayItems) {
			item := it.dayItems[it.dayItemIndex]
			it.dayItemIndex++
			return &item, nil
		}

		if it.dayIndex >= len(it.dayPaths) {
			return nil, nil
		}

		items, err := it.loadDay(ctx, it.dayPaths[it.dayIndex])
		it.dayIndex++
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			continue
		}

		it.dayItems = items
		it.dayItemIndex = 0
	}
}

func (it *dagRunStatusIterator) loadDay(ctx context.Context, dayPath string) ([]dagRunListItem, error) {
	entries, err := os.ReadDir(dayPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read day directory %s: %w", dayPath, err)
	}

	runs, err := loadDayRuns(dayPath, entries)
	if err != nil {
		return nil, err
	}

	startTime, endTime := effectiveTimeRange(it.opts.From, it.opts.To)
	items := make([]dagRunListItem, 0, len(runs))
	for _, run := range runs {
		if !inTimeRange(run.timestamp, startTime, endTime, it.opts.From.IsZero(), it.opts.To.IsZero()) {
			continue
		}
		if it.opts.DAGRunID != "" && !strings.Contains(run.dagRunID, it.opts.DAGRunID) {
			continue
		}

		status := it.store.resolveStatus(ctx, run, it.labelFilters, it.opts.WorkspaceFilter, it.statusesFilter, it.hasStatusFilter)
		if status == nil {
			continue
		}
		if it.opts.Name != "" && !containsFold(status.Name, it.opts.Name) {
			continue
		}

		items = append(items, dagRunListItem{
			Key: dagRunListKey{
				Timestamp: run.timestamp.UTC(),
				Name:      status.Name,
				DAGRunID:  status.DAGRunID,
			},
			Status: status,
		})
	}

	sortDayItems(items)
	return items, nil
}

func containsFold(value, query string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(query))
}

func effectiveTimeRange(from, to exec.TimeInUTC) (time.Time, time.Time) {
	start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Now().UTC()
	if !from.IsZero() {
		start = from.Time
	}
	if !to.IsZero() {
		end = to.Time
	}
	return start, end
}

func listDayPathsInRange(root DataRoot, from, to exec.TimeInUTC) ([]string, error) {
	startDate, endDate := effectiveTimeRange(from, to)

	years, err := listDirsSorted(root.dagRunsDir, true, reYear)
	if err != nil {
		return nil, fmt.Errorf("list years in %s: %w", root.dagRunsDir, err)
	}

	dayPaths := make([]string, 0)
	for _, year := range years {
		yearInt, _ := strconv.Atoi(year)
		if yearInt < startDate.Year() || yearInt > endDate.Year() {
			continue
		}

		yearPath := filepath.Join(root.dagRunsDir, year)
		months, err := listDirsSorted(yearPath, true, reMonth)
		if err != nil {
			return nil, fmt.Errorf("list months in %s: %w", yearPath, err)
		}

		for _, month := range months {
			monthInt, _ := strconv.Atoi(month)
			if (yearInt == startDate.Year() && monthInt < int(startDate.Month())) ||
				(yearInt == endDate.Year() && monthInt > int(endDate.Month())) {
				continue
			}

			monthPath := filepath.Join(yearPath, month)
			days, err := listDirsSorted(monthPath, true, reDay)
			if err != nil {
				return nil, fmt.Errorf("list days in %s: %w", monthPath, err)
			}

			for _, day := range days {
				dayInt, _ := strconv.Atoi(day)
				if (yearInt == startDate.Year() && monthInt == int(startDate.Month()) && dayInt < startDate.Day()) ||
					(yearInt == endDate.Year() && monthInt == int(endDate.Month()) && dayInt > endDate.Day()) {
					continue
				}

				dayPaths = append(dayPaths, filepath.Join(monthPath, day))
			}
		}
	}

	return dayPaths, nil
}

func loadDayRuns(dayPath string, dayEntries []os.DirEntry) ([]*DAGRun, error) {
	indexEntries, _, indexErr := dagrunindex.TryLoadForDay(dayPath, dayEntries)
	if indexErr == nil && indexEntries != nil && len(indexEntries) == countDAGRunDirs(dayEntries) {
		runs := make([]*DAGRun, 0, len(indexEntries))
		for _, indexEntry := range indexEntries {
			run, err := NewDAGRun(filepath.Join(dayPath, indexEntry.DagRunDir))
			if err != nil {
				continue
			}
			run.summary = summaryFromIndexEntry(indexEntry)
			runs = append(runs, run)
		}
		return runs, nil
	}

	files, err := filepath.Glob(filepath.Join(dayPath, DAGRunDirPrefix+"*"))
	if err != nil {
		return nil, fmt.Errorf("glob day directory %s: %w", dayPath, err)
	}

	var summaryMap map[string]*dagrunindex.Entry
	if indexErr == nil && indexEntries != nil {
		summaryMap = make(map[string]*dagrunindex.Entry, len(indexEntries))
		for i := range indexEntries {
			entry := indexEntries[i]
			summaryMap[entry.DagRunDir] = &entry
		}
	}

	runs := make([]*DAGRun, 0, len(files))
	for _, filePath := range files {
		run, err := NewDAGRun(filePath)
		if err != nil {
			continue
		}
		if summaryMap != nil {
			if indexEntry, ok := summaryMap[filepath.Base(filePath)]; ok {
				run.summary = summaryFromIndexEntry(*indexEntry)
			}
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func sortDayItems(items []dagRunListItem) {
	sort.Slice(items, func(i, j int) bool {
		return compareDagRunListKeys(items[i].Key, items[j].Key) < 0
	})
}

type dagRunHeapItem struct {
	Iterator *dagRunStatusIterator
	Item     dagRunListItem
}

type dagRunIteratorHeap []dagRunHeapItem

func (h dagRunIteratorHeap) Len() int { return len(h) }

func (h dagRunIteratorHeap) Less(i, j int) bool {
	return compareDagRunListKeys(h[i].Item.Key, h[j].Item.Key) < 0
}

func (h dagRunIteratorHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *dagRunIteratorHeap) Push(x any) {
	*h = append(*h, x.(dagRunHeapItem))
}

func (h *dagRunIteratorHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
