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
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dagrun/dagrunindex"
)

type dagRunListKey struct {
	Timestamp time.Time
	Name      string
	DAGRunID  string
}

type dagRunListItem struct {
	Key    dagRunListKey
	Status *ir.DAGRunStatus
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

// QueryStatuses executes a normalized status query.
func (store *Store) QueryStatuses(ctx context.Context, query persis.DAGRunStatusQuery) (persis.DAGRunStatusPage, error) {
	items, nextCursor, err := store.listStatusesOrdered(ctx, query)
	if err != nil {
		return persis.DAGRunStatusPage{}, err
	}

	return persis.DAGRunStatusPage{
		Items:      items,
		NextCursor: nextCursor,
	}, nil
}

func (store *Store) listStatusesOrdered(
	ctx context.Context,
	opts persis.DAGRunStatusQuery,
) ([]*ir.DAGRunStatus, string, error) {
	cursorKey, err := decodeQueryCursor(opts.Cursor, opts)
	if err != nil {
		return nil, "", err
	}

	limit := opts.Limit
	target := limit
	if target == 0 {
		target = math.MaxInt
	}
	if target < math.MaxInt {
		target++
	}

	iterOpts := statusIterOptions{seek: cursorKey.Timestamp, hasSeek: opts.Cursor != ""}
	if limit > 0 {
		iterOpts.pageTarget = target
	}
	iterators, err := store.newStatusIterators(ctx, opts, iterOpts)
	if err != nil {
		return nil, "", err
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

	statuses := make([]*ir.DAGRunStatus, 0, min(target, len(iterators)))
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

	if limit <= 0 || len(statuses) <= limit {
		return statuses, "", nil
	}

	nextCursor, err := encodeQueryCursor(opts, keys[limit-1])
	if err != nil {
		return nil, "", err
	}
	return statuses[:limit], nextCursor, nil
}

// statusIterOptions tunes how the iterators of one list call read days.
type statusIterOptions struct {
	// pageTarget is the number of items the list needs, or zero to read
	// every day whole.
	pageTarget int
	// seek is the cursor's creation time. Newer runs sort before the cursor,
	// so they are skipped without being read.
	seek    time.Time
	hasSeek bool
}

func (store *Store) newStatusIterators(ctx context.Context, opts persis.DAGRunStatusQuery, iterOpts statusIterOptions) ([]*dagRunStatusIterator, error) {
	var roots []DataRoot
	if opts.ExactName == "" {
		listed, err := store.listRoot(ctx, "")
		if err != nil {
			return nil, fmt.Errorf("failed to list root directories: %w", err)
		}
		roots = listed
	} else {
		roots = append(roots, store.dataRoot(opts.ExactName))
	}

	iterators := make([]*dagRunStatusIterator, 0, len(roots))
	for _, root := range roots {
		iterator, err := newDAGRunStatusIterator(store, root, opts, iterOpts)
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
	opts            persis.DAGRunStatusQuery
	iterOpts        statusIterOptions
	start           time.Time
	end             time.Time
	dayPaths        []string
	dayIndex        int
	dayItems        []dagRunListItem
	dayItemIndex    int
	day             *lazyDay
	labelFilters    []ir.LabelFilter
	statusesFilter  map[ir.Status]struct{}
	hasStatusFilter bool
	// preferFull reads the remaining days whole once a filter has proven
	// too selective for reading runs one by one.
	preferFull bool
}

// lazyDay reads one large day newest first, one group of runs created in the
// same second at a time, so a page reads about as many runs as it lists.
type lazyDay struct {
	path string
	// entries are sorted by name, and so by creation time.
	entries []os.DirEntry
	// pos is the end of the entries not read yet.
	pos  int
	runs int
	// index lists finished runs without reading their statuses.
	index *dagrunindex.DayIndex
	// before is the creation time of the last group handled. Falling back to
	// a whole-day read takes only the runs created before it.
	before   time.Time
	resolved int
	accepted int
	rejected int
}

// statusListTuning decides when a list reads a day lazily and when it stops.
type statusListTuning struct {
	// minDayRuns is the fewest runs a day needs to be read lazily.
	minDayRuns int
	// pagesPerDay is the number of pages a day must hold to be read lazily.
	pagesPerDay int
	// resolveShare caps the runs a lazy day reads at runs/resolveShare.
	resolveShare int
	// rejectFloor and rejectRatio detect a selective filter: past
	// rejectFloor rejected runs, and rejectRatio times as many rejected as
	// accepted, the rest of the day is read whole.
	rejectFloor int
	rejectRatio int
}

// defaultStatusListTuning reads a day lazily only when it is large enough
// that reading runs one by one beats reading the day whole.
var defaultStatusListTuning = statusListTuning{
	minDayRuns:   1024,
	pagesPerDay:  8,
	resolveShare: 8,
	rejectFloor:  64,
	rejectRatio:  4,
}

func newDAGRunStatusIterator(store *Store, root DataRoot, opts persis.DAGRunStatusQuery, iterOpts statusIterOptions) (*dagRunStatusIterator, error) {
	// Days after the cursor's hold only runs that sort before it.
	dayListEnd := opts.To
	if iterOpts.hasSeek && (dayListEnd.IsZero() || iterOpts.seek.Before(dayListEnd.Time)) {
		dayListEnd = persis.NewUTC(iterOpts.seek)
	}
	dayPaths, err := listDayPathsInRange(root, opts.From, dayListEnd)
	if err != nil {
		return nil, err
	}
	start, end := effectiveTimeRange(opts.From, opts.To)

	statusesFilter := make(map[ir.Status]struct{}, len(opts.Statuses))
	for _, status := range opts.Statuses {
		statusesFilter[status] = struct{}{}
	}

	labelFilters := make([]ir.LabelFilter, 0, len(opts.Labels))
	for _, label := range opts.Labels {
		if trimmed := strings.TrimSpace(label); trimmed != "" {
			labelFilters = append(labelFilters, ir.ParseLabelFilter(trimmed))
		}
	}

	return &dagRunStatusIterator{
		store:           store,
		root:            root,
		opts:            opts,
		iterOpts:        iterOpts,
		start:           start,
		end:             end,
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

		if it.day != nil {
			items, err := it.nextLazyGroup(ctx)
			if err != nil {
				return nil, err
			}
			it.dayItems = items
			it.dayItemIndex = 0
			continue
		}

		if it.dayIndex >= len(it.dayPaths) {
			return nil, nil
		}

		dayPath := it.dayPaths[it.dayIndex]
		it.dayIndex++
		entries, err := os.ReadDir(dayPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read day directory %s: %w", dayPath, err)
		}

		if runs := countDAGRunDirs(entries); it.readsLazily(runs) {
			it.day = &lazyDay{
				path:    dayPath,
				entries: entries,
				pos:     len(entries),
				runs:    runs,
				index:   dagrunindex.NewDayIndex(dayPath, runs/it.store.listTuning.resolveShare),
			}
			continue
		}
		items, err := it.loadDay(ctx, dayPath, entries, time.Time{})
		if err != nil {
			return nil, err
		}
		it.dayItems = items
		it.dayItemIndex = 0
	}
}

// readsLazily reports whether a day with this many runs is read lazily.
func (it *dagRunStatusIterator) readsLazily(runs int) bool {
	if it.iterOpts.pageTarget == 0 || it.preferFull {
		return false
	}
	tuning := it.store.listTuning
	return runs >= max(tuning.minDayRuns, tuning.pagesPerDay*it.iterOpts.pageTarget)
}

// loadDay reads a whole day and returns its matching runs created before
// before, or all of them when before is zero, newest first.
func (it *dagRunStatusIterator) loadDay(ctx context.Context, dayPath string, entries []os.DirEntry, before time.Time) ([]dagRunListItem, error) {
	runs, err := loadDayRuns(ctx, dayPath, entries, it.statusesFilter, it.hasStatusFilter)
	if err != nil {
		return nil, err
	}

	items := make([]dagRunListItem, 0, len(runs))
	for _, run := range runs {
		if !it.allowsTimestamp(run.timestamp, before) {
			continue
		}
		if item, ok := it.toListItem(ctx, run); ok {
			items = append(items, item)
		}
	}

	sortDayItems(items)
	return items, nil
}

// nextLazyGroup returns the next matching runs of the lazy day, newest
// first. Once reading runs one by one stops paying off, it returns the rest
// of the day read whole. It returns no items when the day is done.
func (it *dagRunStatusIterator) nextLazyGroup(ctx context.Context) ([]dagRunListItem, error) {
	day := it.day
	tuning := it.store.listTuning
	for day.pos > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		group, groupStart := nextRunGroup(day.entries, day.pos)
		runs := make([]*DAGRun, 0, len(group))
		for _, entry := range group {
			if run, err := NewDAGRun(filepath.Join(day.path, entry.Name())); err == nil {
				runs = append(runs, run)
			}
		}
		if len(runs) == 0 {
			day.pos = groupStart
			continue
		}

		created := runs[0].timestamp
		if !it.opts.From.IsZero() && created.Before(it.start) {
			break
		}
		if !it.allowsTimestamp(created, time.Time{}) {
			day.pos = groupStart
			day.before = created
			continue
		}

		selective := day.rejected > tuning.rejectFloor && day.rejected > tuning.rejectRatio*day.accepted
		if selective || day.resolved+len(runs) > day.runs/tuning.resolveShare {
			it.preferFull = it.preferFull || selective
			it.day = nil
			return it.loadDay(ctx, day.path, day.entries, day.before)
		}

		items := make([]dagRunListItem, 0, len(runs))
		// A run that the index shows as started by an excluded trigger costs
		// no status read. It counts neither as resolved nor as rejected: both
		// counts decide when to read the day whole, and a DAG that restarts all
		// the time would push every day over that line.
		skipped := 0
		// Index lookups go from the newest run directory down.
		for _, run := range slices.Backward(runs) {
			if entry, ok := day.index.Lookup(filepath.Base(run.baseDir), run.timestamp); ok {
				run.summary = summaryFromIndexEntry(entry)
			}
			if it.excludedByIndex(run) {
				skipped++
				continue
			}
			if item, ok := it.toListItem(ctx, run); ok {
				items = append(items, item)
			}
		}
		day.resolved += len(runs) - skipped
		day.accepted += len(items)
		day.rejected += len(runs) - skipped - len(items)
		day.pos = groupStart
		day.before = created
		if len(items) > 0 {
			sortDayItems(items)
			return items, nil
		}
	}

	it.day = nil
	return nil, nil
}

// dagRunDirTimePrefixLen is the length of "dag-run_YYYYMMDD_HHMMSSZ_", the
// part of a run directory name set by its creation time.
var dagRunDirTimePrefixLen = len(DAGRunDirPrefix) + len("20060102_150405Z_")

// nextRunGroup returns the run directories that share a creation second
// with entries[end-1], and the index where they start. Entries are sorted by
// name, so that group is contiguous. An entry with no creation time in its
// name forms a group of its own with no runs.
func nextRunGroup(entries []os.DirEntry, end int) ([]os.DirEntry, int) {
	name := entries[end-1].Name()
	if len(name) < dagRunDirTimePrefixLen || !strings.HasPrefix(name, DAGRunDirPrefix) {
		return nil, end - 1
	}

	prefix := name[:dagRunDirTimePrefixLen]
	start := end - 1
	for start > 0 && strings.HasPrefix(entries[start-1].Name(), prefix) {
		start--
	}

	group := make([]os.DirEntry, 0, end-start)
	for _, entry := range entries[start:end] {
		if entry.IsDir() {
			group = append(group, entry)
		}
	}
	return group, start
}

// allowsTimestamp reports whether a run created at ts is in the query's
// range, not newer than the cursor, and created before before when that is
// set.
func (it *dagRunStatusIterator) allowsTimestamp(ts, before time.Time) bool {
	if !inTimeRange(ts, it.start, it.end, it.opts.From.IsZero(), it.opts.To.IsZero()) {
		return false
	}
	if it.iterOpts.hasSeek && ts.After(it.iterOpts.seek) {
		return false
	}
	return before.IsZero() || ts.Before(before)
}

// excludedByIndex reports whether the index summary of a run shows that an
// excluded trigger started it. A run without a summary is judged by its status.
func (it *dagRunStatusIterator) excludedByIndex(run *DAGRun) bool {
	return run.summary != nil && slices.Contains(it.opts.ExcludeTriggerTypes, run.summary.TriggerType)
}

// toListItem reads a run into a list item, or reports false when the run is
// unreadable or filtered out.
func (it *dagRunStatusIterator) toListItem(ctx context.Context, run *DAGRun) (dagRunListItem, bool) {
	if it.opts.DAGRunID != "" && !strings.Contains(run.dagRunID, it.opts.DAGRunID) {
		return dagRunListItem{}, false
	}
	if it.excludedByIndex(run) {
		return dagRunListItem{}, false
	}
	status := it.store.resolveStatus(ctx, run, it.labelFilters, it.opts.WorkspaceFilter, it.statusesFilter, it.hasStatusFilter)
	if status == nil {
		return dagRunListItem{}, false
	}
	if slices.Contains(it.opts.ExcludeTriggerTypes, status.TriggerType) {
		return dagRunListItem{}, false
	}
	if it.opts.Name != "" && !containsFold(status.Name, it.opts.Name) {
		return dagRunListItem{}, false
	}
	return dagRunListItem{
		Key: dagRunListKey{
			Timestamp: run.timestamp.UTC(),
			Name:      status.Name,
			DAGRunID:  status.DAGRunID,
		},
		Status: status,
	}, true
}

func containsFold(value, query string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(query))
}

func effectiveTimeRange(from, to persis.TimeInUTC) (time.Time, time.Time) {
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

func listDayPathsInRange(root DataRoot, from, to persis.TimeInUTC) ([]string, error) {
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

func loadDayRuns(ctx context.Context, dayPath string, dayEntries []os.DirEntry, statusesFilter map[ir.Status]struct{}, hasStatusFilter bool) ([]*DAGRun, error) {
	indexEntries, _, indexErr := dagrunindex.TryLoadForDay(ctx, dayPath, dayEntries)
	if indexErr == nil && indexEntries != nil && len(indexEntries) == countDAGRunDirs(dayEntries) {
		runs := make([]*DAGRun, 0, len(indexEntries))
		for _, indexEntry := range indexEntries {
			if hasStatusFilter {
				if _, ok := statusesFilter[indexEntry.Status]; !ok {
					continue
				}
			}
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
