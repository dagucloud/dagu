// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

const (
	retryCandidateDirName       = ".dagrun.retry-candidates"
	retryCandidateDirtyFileName = ".dagrun.retry-candidates.dirty"
	retryCandidateExt           = ".json"
)

type retryCandidateFile struct {
	RunTimestampUnix int64           `json:"runTimestampUnix"`
	Status           ir.DAGRunStatus `json:"status"`
}

type retryCandidateCache struct {
	mu      sync.Mutex
	entries map[string]retryCandidateCacheEntry
}

type retryCandidateCacheEntry struct {
	info      os.FileInfo
	candidate retryCandidateFile
}

type retryCandidateScan struct {
	previous map[string]retryCandidateCacheEntry
	current  map[string]retryCandidateCacheEntry
}

func (store *Store) ListRetryCandidates(ctx context.Context, from persis.TimeInUTC) ([]*ir.DAGRunStatus, error) {
	// A fresh snapshot drops candidate files no longer observed by the scan.
	scan := store.retryCandidates.begin()
	defer store.retryCandidates.finish(scan)

	var candidates []*ir.DAGRunStatus

	roots, err := store.listRoot(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list root directories: %w", err)
	}

	for _, root := range roots {
		dayPaths, err := listDayPathsInRange(root, from, persis.TimeInUTC{})
		if err != nil {
			return nil, err
		}
		for _, dayPath := range dayPaths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			dayCandidates, err := store.listRetryCandidatesForDay(ctx, dayPath, from, scan)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, dayCandidates...)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt > candidates[j].CreatedAt
	})
	return candidates, nil
}

func (store *Store) listRetryCandidatesForDay(ctx context.Context, dayPath string, from persis.TimeInUTC, scan *retryCandidateScan) ([]*ir.DAGRunStatus, error) {
	return store.listRetryCandidatesForDayAfterRebuild(ctx, dayPath, from, scan, false)
}

func (store *Store) listRetryCandidatesForDayAfterRebuild(ctx context.Context, dayPath string, from persis.TimeInUTC, scan *retryCandidateScan, rebuiltCorruptCandidate bool) ([]*ir.DAGRunStatus, error) {
	candidateDir := filepath.Join(dayPath, retryCandidateDirName)
	needsRebuild, err := retryCandidatesNeedRebuild(dayPath)
	if err != nil {
		return nil, err
	}
	if needsRebuild {
		if err := rebuildRetryCandidatesForDay(ctx, dayPath, store.cache); err != nil {
			return nil, err
		}
	}

	entries, err := os.ReadDir(candidateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read retry candidate directory %s: %w", candidateDir, err)
	}

	var candidates []*ir.DAGRunStatus
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), retryCandidateExt) {
			continue
		}
		candidateName := entry.Name()
		candidatePath := filepath.Join(candidateDir, candidateName)
		candidate, err := scan.read(candidateDir, entry)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if rebuiltCorruptCandidate {
				return nil, fmt.Errorf("read retry candidate file %s: %w", candidatePath, err)
			}
			if err := fileutil.Remove(candidatePath); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("remove corrupted retry candidate %s: %w", candidatePath, err)
			}
			if err := markRetryCandidatesDirtyForDay(dayPath); err != nil {
				return nil, err
			}
			if err := rebuildRetryCandidatesForDay(ctx, dayPath, store.cache); err != nil {
				return nil, err
			}
			return store.listRetryCandidatesForDayAfterRebuild(ctx, dayPath, from, scan, true)
		}
		exists, err := retryCandidateRunExists(dayPath, candidate)
		if err != nil {
			return nil, err
		}
		if !exists {
			delete(scan.current, candidatePath)
			if err := fileutil.Remove(candidatePath); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("remove stale retry candidate %s: %w", candidatePath, err)
			}
			continue
		}
		if !from.IsZero() && time.Unix(candidate.RunTimestampUnix, 0).UTC().Before(from.Time) {
			continue
		}
		if !isRetryCandidateStatus(candidate.Status) {
			continue
		}
		status := candidate.Status
		candidates = append(candidates, &status)
	}
	return candidates, nil
}

func updateRetryCandidateFromStatus(statusFile string, status ir.DAGRunStatus) error {
	runDir, dayDir, ok := retryCandidateRootPaths(statusFile)
	if !ok {
		return nil
	}

	candidatePath := retryCandidatePath(dayDir, status)
	if !isRetryCandidateStatus(status) {
		if err := fileutil.Remove(candidatePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove retry candidate: %w", err)
		}
		return nil
	}

	run, err := NewDAGRun(runDir)
	if err != nil {
		return fmt.Errorf("parse retry candidate run directory: %w", err)
	}

	candidate := retryCandidateFile{
		RunTimestampUnix: run.timestamp.UTC().Unix(),
		Status:           retryCandidateStatus(status),
	}
	data, err := json.Marshal(candidate)
	if err != nil {
		return fmt.Errorf("marshal retry candidate: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(candidatePath), 0750); err != nil {
		return fmt.Errorf("create retry candidate directory: %w", err)
	}
	return fileutil.WriteFileAtomic(candidatePath, data, 0600)
}

func markRetryCandidatesDirty(statusFile string) error {
	_, dayDir, ok := retryCandidateRootPaths(statusFile)
	if !ok {
		return nil
	}
	return markRetryCandidatesDirtyForDay(dayDir)
}

func markRetryCandidatesDirtyForDay(dayDir string) error {
	return fileutil.WriteFileAtomic(filepath.Join(dayDir, retryCandidateDirtyFileName), []byte("dirty\n"), 0600)
}

func retryCandidateRootPaths(statusFile string) (runDir, dayDir string, ok bool) {
	if filepath.Base(statusFile) != JSONLStatusFile {
		return "", "", false
	}
	attemptDir := filepath.Dir(statusFile)
	if !IsAttemptDirName(filepath.Base(attemptDir)) {
		return "", "", false
	}
	runDir = filepath.Dir(attemptDir)
	if !strings.HasPrefix(filepath.Base(runDir), DAGRunDirPrefix) {
		return "", "", false
	}
	return runDir, filepath.Dir(runDir), true
}

func retryCandidatesNeedRebuild(dayPath string) (bool, error) {
	dirtyFile := filepath.Join(dayPath, retryCandidateDirtyFileName)
	if _, err := os.Stat(dirtyFile); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat retry candidate dirty marker %s: %w", dirtyFile, err)
	}

	candidateDir := filepath.Join(dayPath, retryCandidateDirName)
	info, err := os.Stat(candidateDir)
	if err == nil {
		return !info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return true, nil
	}
	return false, fmt.Errorf("stat retry candidate directory %s: %w", candidateDir, err)
}

func rebuildRetryCandidatesForDay(ctx context.Context, dayPath string, cache *fileutil.Cache[*ir.DAGRunStatus]) error {
	candidateDir := filepath.Join(dayPath, retryCandidateDirName)
	if err := fileutil.RemoveAll(candidateDir); err != nil {
		return fmt.Errorf("remove retry candidate directory: %w", err)
	}
	if err := os.MkdirAll(candidateDir, 0750); err != nil {
		return fmt.Errorf("create retry candidate directory: %w", err)
	}

	entries, err := os.ReadDir(dayPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read day directory %s: %w", dayPath, err)
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), DAGRunDirPrefix) {
			continue
		}
		run, err := NewDAGRun(filepath.Join(dayPath, entry.Name()))
		if err != nil {
			continue
		}
		attempt, err := run.LatestAttempt(ctx, cache)
		if err != nil {
			continue
		}
		status, err := attempt.ReadStatus(ctx)
		if err != nil || status == nil {
			continue
		}
		if err := updateRetryCandidateFromStatus(attempt.file, *status); err != nil {
			return err
		}
	}
	dirtyFile := filepath.Join(dayPath, retryCandidateDirtyFileName)
	if err := fileutil.Remove(dirtyFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove retry candidate dirty marker: %w", err)
	}
	return nil
}

func (cache *retryCandidateCache) begin() *retryCandidateScan {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	return &retryCandidateScan{
		previous: cache.entries,
		current:  make(map[string]retryCandidateCacheEntry, len(cache.entries)),
	}
}

func (cache *retryCandidateCache) finish(scan *retryCandidateScan) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.entries = scan.current
}

func (scan *retryCandidateScan) read(dir string, entry os.DirEntry) (*retryCandidateFile, error) {
	path := filepath.Join(dir, entry.Name())
	info, err := entry.Info()
	if err != nil {
		return nil, err
	}
	if cached, ok := scan.previous[path]; ok && sameRetryCandidateFile(cached.info, info) {
		scan.current[path] = cached
		candidate := cached.candidate
		return &candidate, nil
	}

	candidate, info, err := readRetryCandidateFile(dir, entry.Name())
	if err != nil {
		return nil, err
	}
	scan.current[path] = retryCandidateCacheEntry{
		info:      info,
		candidate: *candidate,
	}
	return candidate, nil
}

func sameRetryCandidateFile(cached, current os.FileInfo) bool {
	return os.SameFile(cached, current) &&
		cached.Size() == current.Size() &&
		cached.Mode() == current.Mode() &&
		cached.ModTime().Equal(current.ModTime())
}

func readRetryCandidateFile(dir, name string) (*retryCandidateFile, os.FileInfo, error) {
	if filepath.Base(name) != name || !strings.HasSuffix(name, retryCandidateExt) {
		return nil, nil, fmt.Errorf("invalid retry candidate file name %q", name)
	}
	file, err := os.OpenInRoot(dir, name)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, err
	}
	var candidate retryCandidateFile
	if err := json.Unmarshal(data, &candidate); err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	return &candidate, info, nil
}

func retryCandidatePath(dayDir string, status ir.DAGRunStatus) string {
	key := status.Name + "\x00" + status.DAGRunID
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(dayDir, retryCandidateDirName, hex.EncodeToString(sum[:])+retryCandidateExt)
}

func retryCandidateRunExists(dayPath string, candidate *retryCandidateFile) (bool, error) {
	runTimestamp := persis.NewUTC(time.Unix(candidate.RunTimestampUnix, 0).UTC())
	runDir := filepath.Join(dayPath, DAGRunDirPrefix+formatDAGRunTimestamp(runTimestamp)+"_"+candidate.Status.DAGRunID)
	info, err := os.Stat(runDir)
	if err == nil {
		return info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("stat retry candidate run directory %s: %w", runDir, err)
}

func isRetryCandidateStatus(status ir.DAGRunStatus) bool {
	return status.Status == ir.Failed &&
		status.Parent.Zero() &&
		status.AutoRetryLimit > 0 &&
		status.ProcGroup != ""
}

func retryCandidateStatus(status ir.DAGRunStatus) ir.DAGRunStatus {
	return ir.DAGRunStatus{
		Root:                 status.Root,
		Parent:               status.Parent,
		Name:                 status.Name,
		DAGRunID:             status.DAGRunID,
		AttemptID:            status.AttemptID,
		Status:               status.Status,
		TriggerType:          status.TriggerType,
		TriggerActor:         status.TriggerActor,
		CreatedAt:            status.CreatedAt,
		QueuedAt:             status.QueuedAt,
		ScheduleTime:         status.ScheduleTime,
		StartedAt:            status.StartedAt,
		FinishedAt:           status.FinishedAt,
		AutoRetryCount:       status.AutoRetryCount,
		AutoRetryLimit:       status.AutoRetryLimit,
		AutoRetryInterval:    status.AutoRetryInterval,
		AutoRetryBackoff:     status.AutoRetryBackoff,
		AutoRetryMaxInterval: status.AutoRetryMaxInterval,
		ProcGroup:            status.ProcGroup,
		DefinitionID:         status.DAGDefinitionID(),
	}
}
