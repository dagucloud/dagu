// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// minArtifactPruneAge bounds how fresh an artifact entry can be and still be
// reclaimed. A run's artifact directory is created before its record exists,
// so anything younger could still belong to a run that has not been recorded
// yet.
const minArtifactPruneAge = time.Hour

// PruneArtifacts implements persis.DAGRunStore. It removes artifact
// directories and index records that no surviving DAG run points to.
//
// Only entries at artifact layout positions are candidates: run directories
// and index records in <root>/YYYY/MM/DD, and pre-date run directories in
// <root>/<dag>. Run history and logs reuse those names deeper in a tree, so
// nothing below these positions is examined.
//
// Liveness is decided by name rather than by a status's ArchiveDir: a run
// directory name ends in the hash of its DAG-run ID, so a directory whose
// suffix no surviving run record produces is orphaned no matter how the
// record disappeared. Removal is bounded to entries older than
// req.OlderThan, clamped to minArtifactPruneAge, which keeps directories of
// runs that exist but have not written a record yet.
func (store *Store) PruneArtifacts(ctx context.Context, req persis.ArtifactPruneRequest) (*persis.ArtifactPruneResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		root = store.artifactDir
	}
	if root == "" {
		return nil, fmt.Errorf("artifact directory is not configured")
	}
	abs, err := resolveDir(root)
	if err != nil {
		return nil, fmt.Errorf("invalid artifact root %q: %w", root, err)
	}
	if filepath.Dir(abs) == abs {
		return nil, fmt.Errorf("refusing to sweep filesystem root %q", abs)
	}
	if err := store.checkSweepRoot(abs, req.ProtectedDirs); err != nil {
		return nil, err
	}

	cutoff := req.OlderThan.Time
	if cutoff.IsZero() {
		cutoff = time.Now()
	}
	if floor := time.Now().Add(-minArtifactPruneAge); cutoff.After(floor) {
		cutoff = floor
	}

	candidates, err := collectArtifactCandidates(ctx, abs, cutoff)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return &persis.ArtifactPruneResult{}, nil
	}

	// Liveness is read after the walk: a run's record is written before, or
	// within moments of, its artifact directory, so every candidate found
	// above already has its record on disk. An enumeration failure aborts the
	// sweep, because a run missed here would look orphaned while still live.
	live, err := store.liveArtifactNames(ctx)
	if err != nil {
		return nil, err
	}

	s := &artifactSweep{
		root:    abs,
		dryRun:  req.DryRun,
		emptied: map[string]struct{}{},
	}
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if live.claims(c) {
			continue
		}
		s.remove(ctx, c)
	}
	s.pruneEmptied()
	return &s.result, nil
}

// resolveDir returns dir as an absolute path with symlinks evaluated, so that
// guards see the directory a sweep would actually reach: Abs and Clean leave a
// link to the filesystem root looking like an ordinary path. A path that does
// not exist is returned absolute.
func resolveDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return abs, nil
		}
		return "", err
	}
	return resolved, nil
}

// checkSweepRoot refuses a root that equals or contains the run history or a
// protected directory. Such a root is not an artifact tree, and logs share the
// pre-date artifact layout, so sweeping it could remove what no run produced
// as an artifact.
func (store *Store) checkSweepRoot(root string, protected []string) error {
	for _, dir := range append([]string{store.baseDir}, protected...) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		resolved, err := resolveDir(dir)
		if err != nil {
			return fmt.Errorf("invalid protected directory %q: %w", dir, err)
		}
		if dirContains(root, resolved) {
			return fmt.Errorf("refusing to sweep %q: it contains %q", root, resolved)
		}
	}
	return nil
}

// dirContains reports whether path is base or lies beneath it.
func dirContains(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// artifactSweep carries the policy and outcome of one sweep of an artifact
// root.
type artifactSweep struct {
	root    string
	dryRun  bool
	result  persis.ArtifactPruneResult
	emptied map[string]struct{}
}

// artifactCandidate is an aged entry at an artifact layout position. It is
// removed unless a surviving run claims its key.
type artifactCandidate struct {
	path string
	// key is the run ID for a pre-date directory, and the run suffix for a
	// partitioned directory or index record.
	key    string
	legacy bool
	dir    bool
}

// liveSets keeps the two layouts' claim keys apart: pre-date directories are
// keyed by the run ID itself, partitioned directories and their sidecars by
// the derived suffix. A shared set would let a run ID shaped like a suffix —
// a fixed-length hexadecimal string — shield an orphan of the other layout.
type liveSets struct {
	ids      map[string]struct{}
	suffixes map[string]struct{}
}

// claims reports whether a surviving run owns c.
func (l liveSets) claims(c artifactCandidate) bool {
	set := l.suffixes
	if c.legacy {
		set = l.ids
	}
	_, ok := set[c.key]
	return ok
}

// collectArtifactCandidates lists the entries at artifact layout positions
// under root that predate cutoff.
func collectArtifactCandidates(ctx context.Context, root string, cutoff time.Time) ([]artifactCandidate, error) {
	tops, err := listDirsSorted(root, false, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to read artifact root %s: %w", root, err)
	}

	var out []artifactCandidate
	for _, top := range tops {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A top-level name can be both a DAG and a year, so both layouts are
		// checked.
		topDir := filepath.Join(root, top)
		out = append(out, legacyCandidates(ctx, topDir, cutoff)...)
		if reYear.MatchString(top) {
			found, err := partitionedCandidates(ctx, topDir, top, cutoff)
			if err != nil {
				return nil, err
			}
			out = append(out, found...)
		}
	}
	return out, nil
}

// legacyCandidates lists the aged pre-date run directories in dagDir. The
// name itself carries the timestamp and run ID.
func legacyCandidates(ctx context.Context, dagDir string, cutoff time.Time) []artifactCandidate {
	names, err := listDirsSorted(dagDir, false, reDAGRunDir)
	if err != nil {
		logSkippedArtifactDir(ctx, dagDir, err)
		return nil
	}

	var out []artifactCandidate
	for _, name := range names {
		matches := reDAGRunDir.FindStringSubmatch(name)
		at, err := parseDAGRunTimestamp(matches[1])
		if err != nil || !at.Before(cutoff) {
			continue
		}
		out = append(out, artifactCandidate{
			path:   filepath.Join(dagDir, name),
			key:    matches[2],
			legacy: true,
			dir:    true,
		})
	}
	return out
}

// partitionedCandidates lists the aged entries in the day partitions of one
// year directory.
func partitionedCandidates(ctx context.Context, yearDir, year string, cutoff time.Time) ([]artifactCandidate, error) {
	months, err := listDirsSorted(yearDir, false, reMonth)
	if err != nil {
		logSkippedArtifactDir(ctx, yearDir, err)
		return nil, nil
	}

	var out []artifactCandidate
	for _, month := range months {
		monthDir := filepath.Join(yearDir, month)
		days, err := listDirsSorted(monthDir, false, reDay)
		if err != nil {
			logSkippedArtifactDir(ctx, monthDir, err)
			continue
		}
		for _, day := range days {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out = append(out, dayCandidates(ctx, filepath.Join(monthDir, day), year+"/"+month+"/"+day, cutoff)...)
		}
	}
	return out, nil
}

// dayCandidates lists the aged run directories and index records in one day
// partition. A record names the run directory it describes, which may live
// outside the root when artifacts.dir relocated it.
func dayCandidates(ctx context.Context, dayDir, day string, cutoff time.Time) []artifactCandidate {
	entries, err := fileutil.ReadDir(dayDir)
	if err != nil {
		logSkippedArtifactDir(ctx, dayDir, err)
		return nil
	}

	var out []artifactCandidate
	for _, entry := range entries {
		name := entry.Name()
		isDir := entry.IsDir()
		if !isDir {
			if !artifactpath.IsMetaName(name) {
				continue
			}
			name = artifactpath.TrimMetaSuffix(name)
		}
		parsed, ok := artifactpath.ParseRunDirName(name)
		if !ok {
			continue
		}
		at := runDirTimestamp(day, parsed.TimeOfDay)
		if at.IsZero() || !at.Before(cutoff) {
			continue
		}
		out = append(out, artifactCandidate{
			path: filepath.Join(dayDir, entry.Name()),
			key:  parsed.Suffix,
			dir:  isDir,
		})
	}
	return out
}

// logSkippedArtifactDir reports a directory the walk could not read. Skipping
// it can only leave entries behind, never remove a live one.
func logSkippedArtifactDir(ctx context.Context, dir string, err error) {
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	logger.Error(ctx, "Failed to read artifact directory", tag.Error(err), tag.Dir(dir))
}

// remove deletes c, or only reports it in a dry run.
func (s *artifactSweep) remove(ctx context.Context, c artifactCandidate) {
	if s.dryRun {
		s.report(c)
		return
	}
	var err error
	if c.dir {
		err = fileutil.RemoveAll(c.path)
	} else {
		err = fileutil.Remove(c.path)
	}
	if err != nil {
		logger.Error(ctx, "Failed to remove orphaned artifact entry",
			tag.Error(err), tag.Dir(c.path))
		return
	}
	s.report(c)
	s.markAncestors(c.path)
}

func (s *artifactSweep) report(c artifactCandidate) {
	if c.dir {
		s.result.Dirs = append(s.result.Dirs, c.path)
		return
	}
	s.result.Records = append(s.result.Records, c.path)
}

// markAncestors records the directories between path and the root so that
// date partitions a removal emptied are pruned once the sweep finishes.
func (s *artifactSweep) markAncestors(path string) {
	for parent := filepath.Dir(path); parent != s.root && parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		s.emptied[parent] = struct{}{}
	}
}

// pruneEmptied drops directories that a removal emptied, deepest first so a
// parent is seen after its children. The root itself is never removed.
func (s *artifactSweep) pruneEmptied() {
	dirs := make([]string, 0, len(s.emptied))
	for dir := range s.emptied {
		dirs = append(dirs, dir)
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, dir := range dirs {
		// A directory that still holds entries fails to remove and stays.
		_ = fileutil.Remove(dir)
	}
}

// runDirTimestamp rebuilds when a partitioned run directory was created from
// its day and the time of day in its name.
func runDirTimestamp(day, timeOfDay string) time.Time {
	at, err := time.ParseInLocation("2006/01/02150405", day+timeOfDay, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return at
}

// liveArtifactNames collects the names that keep an artifact directory alive:
// every run ID in the dag-runs tree, plus the directory name suffix derived
// from it. Directory names alone carry the ID, so no status file is read.
func (store *Store) liveArtifactNames(ctx context.Context) (liveSets, error) {
	roots, err := store.listRoot(ctx, "")
	if err != nil {
		return liveSets{}, fmt.Errorf("failed to list DAG data roots: %w", err)
	}
	live := liveSets{ids: map[string]struct{}{}, suffixes: map[string]struct{}{}}
	for _, root := range roots {
		if err := collectRunIDs(ctx, root.dagRunsDir, &live); err != nil {
			return liveSets{}, err
		}
	}
	return live, nil
}

func collectRunIDs(ctx context.Context, dagRunsDir string, live *liveSets) error {
	years, err := listDirsSorted(dagRunsDir, false, reYear)
	if err != nil {
		return fmt.Errorf("failed to list run years under %s: %w", dagRunsDir, err)
	}
	for _, year := range years {
		if err := ctx.Err(); err != nil {
			return err
		}
		months, err := listDirsSorted(filepath.Join(dagRunsDir, year), false, reMonth)
		if err != nil {
			return fmt.Errorf("failed to list run months under %s: %w", filepath.Join(dagRunsDir, year), err)
		}
		for _, month := range months {
			monthPath := filepath.Join(dagRunsDir, year, month)
			days, err := listDirsSorted(monthPath, false, reDay)
			if err != nil {
				return fmt.Errorf("failed to list run days under %s: %w", monthPath, err)
			}
			for _, day := range days {
				if err := collectDayRunIDs(ctx, filepath.Join(monthPath, day), live); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func collectDayRunIDs(ctx context.Context, dayPath string, live *liveSets) error {
	entries, err := fileutil.ReadDir(dayPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read run directory %s: %w", dayPath, err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() {
			continue
		}
		matches := reDAGRunDir.FindStringSubmatch(entry.Name())
		if len(matches) != 3 {
			continue
		}
		live.markLive(matches[2])
		if err := collectSubRunIDs(ctx, filepath.Join(dayPath, entry.Name()), live); err != nil {
			return err
		}
	}
	return nil
}

// collectSubRunIDs marks the run IDs nested under a run directory's sub
// dag-run directories. A sub dag-run can hold children of its own, so this
// recurses.
func collectSubRunIDs(ctx context.Context, runDir string, live *liveSets) error {
	for _, dirName := range []string{SubDAGRunsDir, LegacySubDAGRunsDir} {
		subDir := filepath.Join(runDir, dirName)
		entries, err := fileutil.ReadDir(subDir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to read sub dag-run directory %s: %w", subDir, err)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !entry.IsDir() {
				continue
			}
			dagRunID, ok := subDAGRunIDFromDir(dirName, entry.Name())
			if !ok {
				continue
			}
			live.markLive(dagRunID)
			if err := collectSubRunIDs(ctx, filepath.Join(subDir, entry.Name()), live); err != nil {
				return err
			}
		}
	}
	return nil
}

// markLive records both names a run's artifact directory can be keyed by: the
// run ID verbatim for the pre-date layout and the derived suffix for the
// partitioned layout.
func (l *liveSets) markLive(dagRunID string) {
	l.ids[dagRunID] = struct{}{}
	l.suffixes[artifactpath.RunSuffix(dagRunID)] = struct{}{}
}
