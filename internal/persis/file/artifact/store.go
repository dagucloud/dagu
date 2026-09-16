// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package artifact

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ persis.ArtifactStore = (*Store)(nil)

// Store lists DAG-run artifacts from the date-partitioned artifact tree.
//
// The tree holds every DAG, so a descending walk of its day directories is
// already newest-first and needs no merge across DAGs. Each day costs one
// directory read; a record is opened only for an entry that survives the
// filters that the directory name alone can decide.
type Store struct {
	rootDir string
	cache   *fileutil.Cache[*Record]
}

// StoreOption configures artifact listing.
type StoreOption func(*Store)

// WithRecordCache reuses decoded index records across queries.
func WithRecordCache(cache *fileutil.Cache[*Record]) StoreOption {
	return func(s *Store) {
		s.cache = cache
	}
}

// NewStore creates a listing store over the artifact root directory.
func NewStore(rootDir string, opts ...StoreOption) *Store {
	s := &Store{rootDir: rootDir}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// QueryArtifacts implements persis.ArtifactStore.
func (s *Store) QueryArtifacts(ctx context.Context, query persis.ArtifactQuery) (persis.ArtifactPage, error) {
	resume, err := decodeCursor(query)
	if err != nil {
		return persis.ArtifactPage{}, err
	}

	bounds := newQueryBounds(query)
	days, err := s.listDaysDesc(bounds)
	if err != nil {
		return persis.ArtifactPage{}, err
	}

	page := persis.ArtifactPage{}
	for _, day := range days {
		if err := ctx.Err(); err != nil {
			return persis.ArtifactPage{}, err
		}
		if resume != nil && day > resume.Day {
			continue
		}

		done, err := s.collectDay(ctx, query, resume, day, bounds, &page)
		if err != nil {
			return persis.ArtifactPage{}, err
		}
		if done {
			return page, nil
		}
	}

	// Every remaining entry was returned, so there is no next page.
	page.NextCursor = ""
	return page, nil
}

// collectDay appends one day's files to page and reports whether the page is
// full.
func (s *Store) collectDay(
	ctx context.Context,
	query persis.ArtifactQuery,
	resume *cursor,
	day string,
	bounds queryBounds,
	page *persis.ArtifactPage,
) (bool, error) {
	runDirs, err := s.listRunDirsDesc(day)
	if err != nil {
		return false, err
	}

	for _, runDir := range runDirs {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if len(page.Items) == query.Limit {
			return true, nil
		}

		from := ""
		if resume != nil && day == resume.Day {
			if runDir.name > resume.RunDir {
				continue
			}
			if runDir.name == resume.RunDir {
				from = resume.Path
			}
		}
		if !matchesName(runDir.dagName, query.Name) {
			continue
		}
		if outsideBounds(day+runDir.timeOfDay, bounds.from, bounds.to) {
			continue
		}

		rec := s.readRecord(ctx, day, runDir.name)
		if rec == nil {
			continue
		}
		if !query.WorkspaceFilter.MatchesLabels(ir.NewLabels(rec.Labels)) {
			continue
		}

		startedAt, _ := stringutil.ParseTime(rec.StartedAt)
		createdAt := runDirTime(day, runDir.timeOfDay)

		// Paging deep into one run re-reads its directory, because a cursor
		// names a path and the filesystem cannot resume a lexical walk from
		// one. That read is the floor. Everything a page skips past costs a
		// string comparison and nothing else: the size lookup below is a
		// syscall, so it waits until an entry is actually being returned.
		full := false
		err := walkFiles(rec.Dir, func(relPath string, entry fs.DirEntry) bool {
			// The cursor names the last file returned, so resume strictly after it.
			if from != "" && !walkOrderAfter(relPath, from) {
				return true
			}
			if !persis.MatchArtifactFileName(relPath, query.FileName) {
				return true
			}
			if len(page.Items) == query.Limit {
				full = true
				return false
			}
			info, err := entry.Info()
			if err != nil {
				// Removed between the directory read and now; nothing to list.
				return true
			}
			page.Items = append(page.Items, persis.ArtifactFile{
				Name:         rec.Name,
				DAGRunID:     rec.DAGRunID,
				CreatedAt:    createdAt,
				StartedAt:    startedAt,
				RootName:     rec.RootName,
				RootDAGRunID: rec.RootDAGRunID,
				Path:         relPath,
				Size:         info.Size(),
			})
			page.NextCursor = encodeCursor(query, day, runDir.name, relPath)
			return true
		})
		if err != nil {
			logger.Warn(ctx, "Failed to list artifact files", tag.Error(err), tag.Dir(rec.Dir))
			continue
		}
		if full {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) readRecord(ctx context.Context, day, runDir string) *Record {
	path := filepath.Join(s.rootDir, filepath.FromSlash(day), runDir+artifactpath.MetaSuffix)

	load := func() (*Record, error) { return ReadRecord(path) }
	if s.cache != nil {
		load = func() (*Record, error) {
			return s.cache.LoadLatest(path, func() (*Record, error) { return ReadRecord(path) })
		}
	}

	rec, err := load()
	if err != nil {
		// A record removed or rewritten mid-scan must not fail the page.
		if !os.IsNotExist(err) {
			logger.Warn(ctx, "Skipping unreadable artifact record", tag.Error(err), tag.File(path))
		}
		return nil
	}
	return rec
}

type runDirEntry struct {
	name      string
	dagName   string
	timeOfDay string
}

// runDirTime rebuilds the moment a run directory was created from the day it
// sits in and the time of day in its name.
func runDirTime(day, timeOfDay string) time.Time {
	at, err := time.ParseInLocation(dayLayoutForBounds+timeOfDayLayoutForBounds, day+timeOfDay, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return at
}

// listRunDirsDesc returns a day's index records newest first.
func (s *Store) listRunDirsDesc(day string) ([]runDirEntry, error) {
	dayPath := filepath.Join(s.rootDir, filepath.FromSlash(day))
	entries, err := os.ReadDir(dayPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	runDirs := make([]runDirEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !artifactpath.IsMetaName(entry.Name()) {
			continue
		}
		name := artifactpath.TrimMetaSuffix(entry.Name())
		parsed, ok := artifactpath.ParseRunDirName(name)
		if !ok {
			continue
		}
		runDirs = append(runDirs, runDirEntry{name: name, dagName: parsed.DAGName, timeOfDay: parsed.TimeOfDay})
	}

	sort.Slice(runDirs, func(i, j int) bool { return runDirs[i].name > runDirs[j].name })
	return runDirs, nil
}

// listDaysDesc returns the "YYYY/MM/DD" days present in the tree within the
// query's range, newest first.
func (s *Store) listDaysDesc(bounds queryBounds) ([]string, error) {
	from, to := bounds.from, bounds.to

	years, err := listNumericDirsDesc(s.rootDir, 4)
	if err != nil {
		return nil, err
	}

	var days []string
	for _, year := range years {
		if outsideBounds(year, from, to) {
			continue
		}
		months, err := listNumericDirsDesc(filepath.Join(s.rootDir, year), 2)
		if err != nil {
			return nil, err
		}
		for _, month := range months {
			monthKey := year + "/" + month
			if outsideBounds(monthKey, from, to) {
				continue
			}
			daysOfMonth, err := listNumericDirsDesc(filepath.Join(s.rootDir, year, month), 2)
			if err != nil {
				return nil, err
			}
			for _, day := range daysOfMonth {
				key := monthKey + "/" + day
				if outsideBounds(key, from, to) {
					continue
				}
				days = append(days, key)
			}
		}
	}
	return days, nil
}

// outsideBounds reports whether a "YYYY", "YYYY/MM" or "YYYY/MM/DD" key falls
// outside the day bounds. Comparing a bound truncated to the key's own width is
// what lets a whole year or month be skipped without reading its directory,
// which is the difference between a day's query costing one read and costing
// one per month of retained history.
func outsideBounds(key, from, to string) bool {
	if from != "" && key < from[:len(key)] {
		return true
	}
	if to != "" && key > to[:len(key)] {
		return true
	}
	return false
}

// timeBounds renders the query range as "YYYY/MM/DDHHMMSS", the same shape a
// run's day and directory name concatenate to. outsideBounds truncates a bound
// to its caller's width, so one string serves the year, month, day and second
// comparisons alike.
type queryBounds struct{ from, to string }

func newQueryBounds(query persis.ArtifactQuery) queryBounds {
	from, to := timeBounds(query)
	return queryBounds{from: from, to: to}
}

func timeBounds(query persis.ArtifactQuery) (from, to string) {
	const layout = dayLayoutForBounds + timeOfDayLayoutForBounds
	if !query.From.IsZero() {
		from = query.From.UTC().Format(layout)
	}
	if !query.To.IsZero() {
		to = query.To.UTC().Format(layout)
	}
	return from, to
}

const (
	dayLayoutForBounds       = "2006/01/02"
	timeOfDayLayoutForBounds = "150405"
)

func listNumericDirsDesc(dir string, width int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || len(name) != width || !isDigits(name) {
			continue
		}
		names = append(names, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names, nil
}

// walkFiles visits a run's regular files in walk order, stopping when visit
// returns false.
//
// Walk order is lexical within each directory, so the sequence is the same on
// every call without materialising the whole tree first. That is what makes
// stopping early safe: a page reads only as far as it needs, and the next page
// resumes into the same order.
//
// The entry is handed over unresolved. Its kind is known from the directory
// read, but its size is a further syscall, and most entries a page visits are
// ones it skips past.
func walkFiles(dir string, visit func(relPath string, entry fs.DirEntry) bool) error {
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if !visit(filepath.ToSlash(rel), entry) {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

// walkOrderAfter reports whether path comes strictly after other in walk order.
//
// Walk order is not the order of the joined paths: a directory is descended
// where its own name sorts, so everything under "a/" precedes "a.txt" even
// though "a.txt" < "a/b.txt" as strings. Comparing component by component
// reproduces that, which lets a cursor resume by comparison rather than by
// searching the tree for the file it names.
func walkOrderAfter(path, other string) bool {
	left, right := strings.Split(path, "/"), strings.Split(other, "/")
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] != right[i] {
			return left[i] > right[i]
		}
	}
	return len(left) > len(right)
}

func matchesName(dagName, filter string) bool {
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(dagName), strings.ToLower(filter))
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
