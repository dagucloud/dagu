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

	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core/exec"
)

const latestAttemptFileName = ".dagrun.latest"

var errLatestAttemptPointerInvalid = errors.New("latest attempt pointer invalid")

type latestAttemptPointer struct {
	StatusFile string `json:"statusFile"`
}

type attemptStatusFileInfo struct {
	dagRunsDir         string
	statusFile         string
	relativeStatusFile string
	runDir             string
	runDirName         string
	attemptDirName     string
}

func updateLatestAttemptPointer(ctx context.Context, statusFile string) error {
	candidate, ok := parseAttemptStatusFileInfo(statusFile)
	if !ok {
		return nil
	}

	lock := dirlock.New(candidate.dagRunsDir, nil)
	if err := lock.TryLock(); err != nil {
		if errors.Is(err, dirlock.ErrLockConflict) {
			return nil
		}
		return err
	}
	unlock := true
	defer func() {
		if unlock {
			_ = lock.Unlock()
		}
	}()

	if err := ctx.Err(); err != nil {
		return err
	}

	current, err := latestAttemptInfoFromPointer(candidate.dagRunsDir)
	if err == nil && !attemptStatusFileInfoLess(current, candidate) {
		if unlockErr := lock.Unlock(); unlockErr != nil {
			return unlockErr
		}
		unlock = false
		return nil
	}

	ptr := latestAttemptPointer{StatusFile: candidate.relativeStatusFile}
	data, err := json.Marshal(ptr)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(latestAttemptPointerPath(candidate.dagRunsDir), data, 0600); err != nil {
		return err
	}
	if unlockErr := lock.Unlock(); unlockErr != nil {
		return unlockErr
	}
	unlock = false
	return nil
}

func (dr DataRoot) latestAttemptFromPointer(ctx context.Context, cache *fileutil.Cache[*exec.DAGRunStatus], cutoff exec.TimeInUTC) (*Attempt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	info, err := latestAttemptInfoFromPointer(dr.dagRunsDir)
	if err != nil {
		return nil, err
	}
	run, err := newDAGRun(info.runDir, dr.artifactDir)
	if err != nil {
		return nil, err
	}
	if !cutoff.IsZero() && run.timestamp.Before(cutoff.Time) {
		return nil, errLatestAttemptPointerInvalid
	}
	attempt, err := NewAttempt(info.statusFile, cache)
	if err != nil {
		return nil, err
	}
	if attempt.Hidden() || !attempt.Exists() {
		return nil, errLatestAttemptPointerInvalid
	}
	return attempt, nil
}

func latestAttemptInfoFromPointer(dagRunsDir string) (attemptStatusFileInfo, error) {
	data, err := os.ReadFile(latestAttemptPointerPath(dagRunsDir)) //nolint:gosec // path is derived from the DAG-run root.
	if err != nil {
		return attemptStatusFileInfo{}, err
	}

	var ptr latestAttemptPointer
	if err := json.Unmarshal(data, &ptr); err != nil {
		return attemptStatusFileInfo{}, fmt.Errorf("%w: %v", errLatestAttemptPointerInvalid, err)
	}
	if ptr.StatusFile == "" || !filepath.IsLocal(ptr.StatusFile) {
		return attemptStatusFileInfo{}, errLatestAttemptPointerInvalid
	}

	statusFile := filepath.Join(dagRunsDir, filepath.Clean(ptr.StatusFile))
	info, ok := parseAttemptStatusFileInfo(statusFile)
	if !ok || info.dagRunsDir != filepath.Clean(dagRunsDir) {
		return attemptStatusFileInfo{}, errLatestAttemptPointerInvalid
	}
	if _, err := os.Stat(info.statusFile); err != nil {
		return attemptStatusFileInfo{}, err
	}
	return info, nil
}

func parseAttemptStatusFileInfo(statusFile string) (attemptStatusFileInfo, bool) {
	cleanStatusFile := filepath.Clean(statusFile)
	if filepath.Base(cleanStatusFile) != JSONLStatusFile {
		return attemptStatusFileInfo{}, false
	}

	attemptDir := filepath.Dir(cleanStatusFile)
	attemptDirName := filepath.Base(attemptDir)
	if !reAttemptDir.MatchString(attemptDirName) {
		return attemptStatusFileInfo{}, false
	}

	runDir := filepath.Dir(attemptDir)
	runDirName := filepath.Base(runDir)
	if !reDAGRunDir.MatchString(runDirName) {
		return attemptStatusFileInfo{}, false
	}

	dayDir := filepath.Dir(runDir)
	monthDir := filepath.Dir(dayDir)
	yearDir := filepath.Dir(monthDir)
	if !reDay.MatchString(filepath.Base(dayDir)) ||
		!reMonth.MatchString(filepath.Base(monthDir)) ||
		!reYear.MatchString(filepath.Base(yearDir)) {
		return attemptStatusFileInfo{}, false
	}

	dagRunsDir := filepath.Dir(yearDir)
	relativeStatusFile, err := filepath.Rel(dagRunsDir, cleanStatusFile)
	if err != nil || !filepath.IsLocal(relativeStatusFile) {
		return attemptStatusFileInfo{}, false
	}

	return attemptStatusFileInfo{
		dagRunsDir:         filepath.Clean(dagRunsDir),
		statusFile:         cleanStatusFile,
		relativeStatusFile: filepath.Clean(relativeStatusFile),
		runDir:             runDir,
		runDirName:         runDirName,
		attemptDirName:     attemptDirName,
	}, true
}

func attemptStatusFileInfoLess(a, b attemptStatusFileInfo) bool {
	if a.runDirName != b.runDirName {
		return a.runDirName < b.runDirName
	}
	return a.attemptDirName < b.attemptDirName
}

func latestAttemptPointerPath(dagRunsDir string) string {
	return filepath.Join(dagRunsDir, latestAttemptFileName)
}
