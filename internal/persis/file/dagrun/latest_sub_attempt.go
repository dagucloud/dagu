// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// A run of a DAG that another DAG started with dag.run is stored under that
// parent's run, outside the DAG's own dag-runs tree. This pointer in the DAG's
// dag-runs directory names the newest such run, so a listing can show it
// without scanning other DAGs' history.
const latestSubAttemptFileName = ".dagrun.latest-sub"

type latestSubAttemptPointer struct {
	// StatusFile is relative to the store base directory, because the run
	// lives in another DAG's tree.
	StatusFile string `json:"statusFile"`
}

// subAttemptStatusFileInfo locates a nested attempt from its status file path:
//
//	<base>/<dag>/dag-runs/<year>/<month>/<day>/dag-run_<ts>_<id>/sub/<child-id>/a_<ts>_<attempt-id>/status.jsonl
//
// The legacy layout uses "children/child_<id>" in place of "sub/<id>".
type subAttemptStatusFileInfo struct {
	baseDir        string
	statusFile     string
	attemptDirName string
	attemptTime    time.Time
}

// updateLatestSubAttemptPointer points the sub-run pointer of the DAG that
// status belongs to at statusFile, when statusFile is a nested attempt newer
// than the one the pointer names. Like the latest attempt pointer, the update
// is skipped while another process holds the DAG's lock.
func updateLatestSubAttemptPointer(ctx context.Context, statusFile string, status ir.DAGRunStatus) error {
	if status.Ephemeral || status.Name == "" {
		return nil
	}
	candidate, ok := parseSubAttemptStatusFile(statusFile)
	if !ok {
		return nil
	}
	dagRunsDir := NewDataRootWithArtifactDir(candidate.baseDir, status.Name, "").dagRunsDir

	lock := dirlock.New(dagRunsDir, nil)
	if err := lock.TryLock(); err != nil {
		if errors.Is(err, dirlock.ErrLockConflict) {
			return nil
		}
		return err
	}
	defer func() { _ = lock.Unlock() }()

	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := latestSubAttemptInfoFromPointer(candidate.baseDir, dagRunsDir)
	if err == nil && !attemptDirOlder(current.attemptDirName, candidate.attemptDirName) {
		return nil
	}

	relative, err := filepath.Rel(candidate.baseDir, candidate.statusFile)
	if err != nil {
		return err
	}
	data, err := json.Marshal(latestSubAttemptPointer{StatusFile: relative})
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(latestSubAttemptPointerPath(dagRunsDir), data, 0600)
}

// LatestSubAttempt returns the newest attempt of the named DAG that ran as a
// sub DAG-run of another DAG, created at or after NotBefore. It reads only the
// pointer, so once the parent run is removed it reports dagrun.ErrNoStatusData.
func (store *Store) LatestSubAttempt(ctx context.Context, query persis.DAGRunLatestAttemptQuery) (dagrun.Attempt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := latestSubAttemptInfoFromPointer(store.baseDir, store.dataRoot(query.Name).dagRunsDir)
	if err != nil {
		return nil, dagrun.ErrNoStatusData
	}
	if !query.NotBefore.IsZero() && info.attemptTime.Before(query.NotBefore.Time) {
		return nil, dagrun.ErrNoStatusData
	}
	attempt, err := NewAttempt(info.statusFile, store.cache, WithArtifactRoot(store.artifactDir))
	if err != nil {
		return nil, err
	}
	if attempt.Hidden() || !attempt.Exists() {
		return nil, dagrun.ErrNoStatusData
	}
	return attempt, nil
}

func latestSubAttemptInfoFromPointer(baseDir, dagRunsDir string) (subAttemptStatusFileInfo, error) {
	data, err := os.ReadFile(latestSubAttemptPointerPath(dagRunsDir)) //nolint:gosec // path is derived from the DAG-run root.
	if err != nil {
		return subAttemptStatusFileInfo{}, err
	}
	var ptr latestSubAttemptPointer
	if err := json.Unmarshal(data, &ptr); err != nil {
		return subAttemptStatusFileInfo{}, fmt.Errorf("%w: %v", errLatestAttemptPointerInvalid, err)
	}
	if ptr.StatusFile == "" || !filepath.IsLocal(ptr.StatusFile) {
		return subAttemptStatusFileInfo{}, errLatestAttemptPointerInvalid
	}
	info, ok := parseSubAttemptStatusFile(filepath.Join(baseDir, filepath.Clean(ptr.StatusFile)))
	if !ok || info.baseDir != filepath.Clean(baseDir) {
		return subAttemptStatusFileInfo{}, errLatestAttemptPointerInvalid
	}
	if _, err := os.Stat(info.statusFile); err != nil {
		return subAttemptStatusFileInfo{}, err
	}
	return info, nil
}

// parseSubAttemptStatusFile reports whether statusFile belongs to a nested
// attempt and, if so, where its store base directory is.
func parseSubAttemptStatusFile(statusFile string) (subAttemptStatusFileInfo, bool) {
	clean := filepath.Clean(statusFile)
	if filepath.Base(clean) != JSONLStatusFile {
		return subAttemptStatusFileInfo{}, false
	}
	attemptDir := filepath.Dir(clean)
	attemptDirName := filepath.Base(attemptDir)
	matches := reAttemptDir.FindStringSubmatch(attemptDirName)
	if matches == nil {
		return subAttemptStatusFileInfo{}, false
	}
	// Attempt timestamps are "YYYYMMDD_HHMMSS_mmmZ"; the part before the
	// millisecond separator is the "YYYYMMDD_HHMMSSZ" run format.
	attemptTime, err := parseDAGRunTimestamp(matches[1][:dagRunTimestampLen-1] + "Z")
	if err != nil {
		return subAttemptStatusFileInfo{}, false
	}

	// Walk the "<container>/<run-id>" segments up to the top-level run.
	dir := filepath.Dir(attemptDir)
	nested := false
	for !reDAGRunDir.MatchString(filepath.Base(dir)) {
		container := filepath.Dir(dir)
		if _, ok := subDAGRunIDFromDir(filepath.Base(container), filepath.Base(dir)); !ok {
			return subAttemptStatusFileInfo{}, false
		}
		nested = true
		dir = filepath.Dir(container)
	}
	if !nested {
		return subAttemptStatusFileInfo{}, false
	}

	dayDir := filepath.Dir(dir)
	monthDir := filepath.Dir(dayDir)
	yearDir := filepath.Dir(monthDir)
	if !reDay.MatchString(filepath.Base(dayDir)) ||
		!reMonth.MatchString(filepath.Base(monthDir)) ||
		!reYear.MatchString(filepath.Base(yearDir)) {
		return subAttemptStatusFileInfo{}, false
	}
	dagRunsDir := filepath.Dir(yearDir)
	if filepath.Base(dagRunsDir) != "dag-runs" {
		return subAttemptStatusFileInfo{}, false
	}

	return subAttemptStatusFileInfo{
		baseDir:        filepath.Dir(filepath.Dir(dagRunsDir)),
		statusFile:     clean,
		attemptDirName: attemptDirName,
		attemptTime:    attemptTime,
	}, true
}

func latestSubAttemptPointerPath(dagRunsDir string) string {
	return filepath.Join(dagRunsDir, latestSubAttemptFileName)
}
