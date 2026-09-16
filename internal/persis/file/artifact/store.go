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

// maxFilesPerRun bounds how many of a run's files one listing returns.
//
// On a measured tree the median run held three files and the largest 15,131,
// with under two percent of runs holding nearly all of them. A hundred returns
// the vast majority of runs whole and keeps a page bounded when it lands on
// one of the rest; the per-run endpoint serves the full tree.
const maxFilesPerRun = 100

// Store lists DAG-run artifacts from the date-partitioned artifact tree.
//
// The tree holds every DAG, so a descending walk of its day directories is
// already newest-first and needs no merge across DAGs. Each day costs one
// directory read; a record is opened only for an entry that survives the
// filters that the directory name alone can decide, and a run's directory is
// walked only for a run that is being returned.
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

	// Every remaining run was returned, so there is no next page.
	page.NextCursor = ""
	return page, nil
}

// collectDay appends one day's runs to page and reports whether the page is
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

		// The cursor names the last run returned, so resume strictly after it.
		if resume != nil && day == resume.Day && runDir.name >= resume.RunDir {
			continue
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

		files, truncated, err := listRunFiles(rec.Dir, query.FileName)
		if err != nil {
			logger.Warn(ctx, "Failed to list artifact files", tag.Error(err), tag.Dir(rec.Dir))
			continue
		}
		if len(files) == 0 {
			continue
		}

		startedAt, _ := stringutil.ParseTime(rec.StartedAt)
		page.Items = append(page.Items, persis.ArtifactRun{
			Name:           rec.Name,
			DAGRunID:       rec.DAGRunID,
			CreatedAt:      runDirTime(day, runDir.timeOfDay),
			StartedAt:      startedAt,
			Files:          files,
			FilesTruncated: truncated,
		})
		page.NextCursor = encodeCursor(query, day, runDir.name)
	}
	return false, nil
}

// listRunFiles collects a run's files that match pattern, in walk order, up
// to maxFilesPerRun. The second result reports that the run held more.
//
// A file's size is a syscall and its match is a string comparison, so the
// comparison goes first: without a pattern every file is a candidate and the
// walk itself stops at the bound; with one, the walk scans the run and stops
// once the bound is reached.
func listRunFiles(dir, pattern string) ([]persis.ArtifactFile, bool, error) {
	var files []persis.ArtifactFile
	truncated := false
	err := walkFiles(dir, func(relPath string, entry fs.DirEntry) bool {
		if !persis.MatchArtifactFileName(relPath, pattern) {
			return true
		}
		if len(files) == maxFilesPerRun {
			truncated = true
			return false
		}
		info, err := entry.Info()
		if err != nil {
			// Removed between the directory read and now; nothing to list.
			return true
		}
		files = append(files, persis.ArtifactFile{Path: relPath, Size: info.Size()})
		return true
	})
	return files, truncated, err
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

// listRunDirsDesc returns the run directories a day's sidecars address, newest
// first.
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

// queryBounds is the query range rendered as "YYYY/MM/DDHHMMSS", the same
// shape a run's day and directory name concatenate to. outsideBounds truncates
// a bound to its caller's width, so one string serves the year, month, day and
// second comparisons alike.
type queryBounds struct{ from, to string }

func newQueryBounds(query persis.ArtifactQuery) queryBounds {
	const layout = dayLayoutForBounds + timeOfDayLayoutForBounds
	var b queryBounds
	if !query.From.IsZero() {
		b.from = query.From.UTC().Format(layout)
	}
	if !query.To.IsZero() {
		b.to = query.To.UTC().Format(layout)
	}
	return b
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
// stopping early safe.
//
// The entry is handed over unresolved. Its kind is known from the directory
// read, but its size is a further syscall, and a visit may decide it does not
// want the file at all.
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
