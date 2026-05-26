// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrunindex

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	indexv1 "github.com/dagucloud/dagu/proto/index/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// IndexFileName is the name of the DAG run index file.
	IndexFileName = ".dagrun.index"
	// IndexVersion is the current index format version.
	IndexVersion = 7
	// MinRunsForIndex is the minimum number of runs needed to create an index.
	MinRunsForIndex = 10

	dagRunDirPrefix  = "dag-run_"
	attemptDirPrefix = "attempt_"
	statusFile       = "status.jsonl"
)

var (
	reDAGRunDir  = regexp.MustCompile(`^` + dagRunDirPrefix + `(\d{8}_\d{6}Z)_(.*)$`)
	reAttemptDir = regexp.MustCompile(`^` + attemptDirPrefix + `(\d{8}_\d{6}_\d{3}Z)_(.*)$`)
)

// Entry holds a cached summary for a single DAG run.
type Entry struct {
	DagRunDir            string
	DagRunID             string
	LatestAttemptDir     string
	Status               core.Status
	StartedAtUnix        int64
	FinishedAtUnix       int64
	Labels               []string
	Name                 string
	WorkerID             string
	LeaseAt              int64
	Params               string
	QueuedAt             string
	ScheduleTime         string
	TriggerType          core.TriggerType
	CreatedAt            int64
	AttemptID            string
	AutoRetryCount       int
	ParentName           string
	ParentID             string
	AutoRetryLimit       int
	AutoRetryInterval    time.Duration
	AutoRetryBackoff     float64
	AutoRetryMaxInterval time.Duration
	ProcGroup            string
	SuspendFlagName      string
	ArchiveDir           string
}

// TryLoadForDay attempts to load and validate the index for a day directory.
// dagRunDirs should be the result of os.ReadDir(dayDir).
//
// Returns:
//   - (entries, true, nil) if a valid index was loaded or rebuilt successfully
//   - (entries, false, nil) if entries were computed but no index was written (active runs or <10 runs)
//   - (nil, false, nil) if the day has fewer than MinRunsForIndex runs
//   - (nil, false, err) on unexpected I/O errors during rebuild
func TryLoadForDay(dayDir string, dagRunDirs []os.DirEntry) ([]Entry, bool, error) {
	runDirs := filterDAGRunDirs(dagRunDirs)
	if len(runDirs) < MinRunsForIndex {
		return nil, false, nil
	}

	indexPath := filepath.Join(dayDir, IndexFileName)
	idx, err := readIndex(indexPath)
	if err == nil && validateIndex(dayDir, idx, runDirs) {
		entries := protoToEntries(idx.Entries)
		return entries, true, nil
	}

	// Rebuild.
	return RebuildForDay(dayDir, dagRunDirs)
}

// RebuildForDay scans a day directory, discovers latest attempts, reads statuses,
// and writes the index if all runs are terminal.
func RebuildForDay(dayDir string, dagRunDirs []os.DirEntry) ([]Entry, bool, error) {
	runDirs := filterDAGRunDirs(dagRunDirs)
	if len(runDirs) == 0 {
		return nil, false, nil
	}

	entries := make([]Entry, 0, len(runDirs))
	allTerminal := true

	for _, rd := range runDirs {
		runDir := filepath.Join(dayDir, rd.Name())

		latestAttemptDir, err := findLatestAttempt(runDir)
		if err != nil {
			return nil, false, fmt.Errorf("failed to find latest attempt in %s: %w", runDir, err)
		}
		if latestAttemptDir == "" {
			continue
		}

		statusPath := filepath.Join(runDir, latestAttemptDir, statusFile)
		status, err := parseStatusFile(statusPath)
		if err != nil {
			// Skip runs with unreadable status files; they'll be served from filesystem.
			continue
		}

		if status.Status.IsActive() {
			allTerminal = false
		}

		// Parse dag run ID from directory name.
		dagRunID := parseDagRunID(rd.Name())

		// Parse timestamps.
		startedAt := parseTimeToUnix(status.StartedAt)
		finishedAt := parseTimeToUnix(status.FinishedAt)

		entries = append(entries, Entry{
			DagRunDir:            rd.Name(),
			DagRunID:             dagRunID,
			LatestAttemptDir:     latestAttemptDir,
			Status:               status.Status,
			StartedAtUnix:        startedAt,
			FinishedAtUnix:       finishedAt,
			Labels:               status.Labels,
			Name:                 status.Name,
			WorkerID:             status.WorkerID,
			LeaseAt:              status.LeaseAt,
			Params:               status.Params,
			QueuedAt:             status.QueuedAt,
			ScheduleTime:         status.ScheduleTime,
			TriggerType:          status.TriggerType,
			CreatedAt:            status.CreatedAt,
			AttemptID:            status.AttemptID,
			AutoRetryCount:       status.AutoRetryCount,
			ParentName:           status.Parent.Name,
			ParentID:             status.Parent.ID,
			AutoRetryLimit:       status.AutoRetryLimit,
			AutoRetryInterval:    status.AutoRetryInterval,
			AutoRetryBackoff:     status.AutoRetryBackoff,
			AutoRetryMaxInterval: status.AutoRetryMaxInterval,
			ProcGroup:            status.ProcGroup,
			SuspendFlagName:      status.SuspendFlagName,
			ArchiveDir:           status.ArchiveDir,
		})
	}

	// Write index only if all runs are terminal and there are enough runs.
	if allTerminal && len(entries) >= MinRunsForIndex {
		if err := writeIndex(dayDir, entries); err != nil {
			// Non-fatal: just don't write the index.
			return entries, false, nil
		}
		return entries, true, nil
	}

	return entries, false, nil
}

// DeleteIndex removes the .dagrun.index file from a day directory.
func DeleteIndex(dayDir string) {
	_ = fileutil.Remove(filepath.Join(dayDir, IndexFileName))
}

func readIndex(indexPath string) (*indexv1.DAGRunIndex, error) {
	data, err := fileutil.ReadFile(indexPath)
	if err != nil {
		return nil, err
	}
	var idx indexv1.DAGRunIndex
	if err := proto.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	if idx.Version != IndexVersion {
		return nil, fmt.Errorf("version mismatch: got %d, want %d", idx.Version, IndexVersion)
	}
	return &idx, nil
}

func validateIndex(dayDir string, idx *indexv1.DAGRunIndex, runDirs []os.DirEntry) bool {
	if len(idx.Entries) != len(runDirs) {
		return false
	}

	// Build set of run dir names from filesystem.
	fsSet := make(map[string]struct{}, len(runDirs))
	for _, d := range runDirs {
		fsSet[d.Name()] = struct{}{}
	}

	for _, e := range idx.Entries {
		if _, ok := fsSet[e.DagRunDir]; !ok {
			return false
		}

		// Validate run directory mtime (changes when new attempts are created).
		runDir := filepath.Join(dayDir, e.DagRunDir)
		runDirInfo, err := os.Stat(runDir)
		if err != nil {
			return false
		}
		if runDirInfo.ModTime().UnixNano() != e.RunDirModTime {
			return false
		}

		// Validate status file metadata.
		statusPath := filepath.Join(runDir, e.LatestAttemptDir, statusFile)
		info, err := os.Stat(statusPath)
		if err != nil {
			return false
		}
		if info.Size() != e.LatestStatusSize || info.ModTime().UnixNano() != e.LatestStatusModTime {
			return false
		}
	}

	return true
}

func writeIndex(dayDir string, entries []Entry) error {
	// Stat the run dirs and status files to capture metadata for index validation.
	protoEntries := make([]*indexv1.DAGRunIndexEntry, 0, len(entries))
	for _, e := range entries {
		runDir := filepath.Join(dayDir, e.DagRunDir)
		runDirInfo, err := os.Stat(runDir)
		if err != nil {
			return err
		}

		statusPath := filepath.Join(runDir, e.LatestAttemptDir, statusFile)
		info, err := os.Stat(statusPath)
		if err != nil {
			return err
		}

		protoEntries = append(protoEntries, &indexv1.DAGRunIndexEntry{
			DagRunDir:            e.DagRunDir,
			DagRunId:             e.DagRunID,
			LatestAttemptDir:     e.LatestAttemptDir,
			LatestStatusSize:     info.Size(),
			LatestStatusModTime:  info.ModTime().UnixNano(),
			RunDirModTime:        runDirInfo.ModTime().UnixNano(),
			Status:               int32(e.Status), //nolint:gosec
			StartedAt:            e.StartedAtUnix,
			FinishedAt:           e.FinishedAtUnix,
			Labels:               e.Labels,
			Name:                 e.Name,
			WorkerId:             e.WorkerID,
			Params:               e.Params,
			QueuedAt:             e.QueuedAt,
			ScheduleTime:         e.ScheduleTime,
			TriggerType:          int32(e.TriggerType), //nolint:gosec
			CreatedAt:            e.CreatedAt,
			AttemptId:            e.AttemptID,
			AutoRetryCount:       int32(min(e.AutoRetryCount, math.MaxInt32)), //nolint:gosec
			ParentDagRunName:     e.ParentName,
			ParentDagRunId:       e.ParentID,
			AutoRetryLimit:       int32(min(e.AutoRetryLimit, math.MaxInt32)), //nolint:gosec
			AutoRetryInterval:    int64(e.AutoRetryInterval),
			AutoRetryBackoff:     e.AutoRetryBackoff,
			AutoRetryMaxInterval: int64(e.AutoRetryMaxInterval),
			ProcGroup:            e.ProcGroup,
			SuspendFlagName:      e.SuspendFlagName,
			ArchiveDir:           e.ArchiveDir,
		})
	}

	idx := &indexv1.DAGRunIndex{
		Version:     IndexVersion,
		BuiltAtUnix: time.Now().Unix(),
		Entries:     protoEntries,
	}

	data, err := proto.Marshal(idx)
	if err != nil {
		return fmt.Errorf("failed to marshal DAG run index: %w", err)
	}

	return fileutil.WriteFileAtomic(filepath.Join(dayDir, IndexFileName), data, 0600)
}

func protoToEntries(protoEntries []*indexv1.DAGRunIndexEntry) []Entry {
	entries := make([]Entry, len(protoEntries))
	for i, pe := range protoEntries {
		entries[i] = Entry{
			DagRunDir:            pe.DagRunDir,
			DagRunID:             pe.DagRunId,
			LatestAttemptDir:     pe.LatestAttemptDir,
			Status:               core.Status(pe.Status),
			StartedAtUnix:        pe.StartedAt,
			FinishedAtUnix:       pe.FinishedAt,
			Labels:               pe.Labels,
			Name:                 pe.Name,
			WorkerID:             pe.WorkerId,
			Params:               pe.Params,
			QueuedAt:             pe.QueuedAt,
			ScheduleTime:         pe.ScheduleTime,
			TriggerType:          core.TriggerType(pe.TriggerType),
			CreatedAt:            pe.CreatedAt,
			AttemptID:            pe.AttemptId,
			AutoRetryCount:       int(pe.AutoRetryCount),
			ParentName:           pe.ParentDagRunName,
			ParentID:             pe.ParentDagRunId,
			AutoRetryLimit:       int(pe.AutoRetryLimit),
			AutoRetryInterval:    time.Duration(pe.AutoRetryInterval),
			AutoRetryBackoff:     pe.AutoRetryBackoff,
			AutoRetryMaxInterval: time.Duration(pe.AutoRetryMaxInterval),
			ProcGroup:            pe.ProcGroup,
			SuspendFlagName:      pe.SuspendFlagName,
			ArchiveDir:           pe.ArchiveDir,
		}
	}
	return entries
}

func filterDAGRunDirs(entries []os.DirEntry) []os.DirEntry {
	var result []os.DirEntry
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), dagRunDirPrefix) {
			result = append(result, e)
		}
	}
	return result
}

func findLatestAttempt(runDir string) (string, error) {
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return "", err
	}

	var attemptDirs []string
	for _, e := range entries {
		name := e.Name()
		// Skip hidden (dequeued) attempts.
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() && reAttemptDir.MatchString(name) {
			attemptDirs = append(attemptDirs, name)
		}
	}

	if len(attemptDirs) == 0 {
		return "", nil
	}

	// Sort descending (newest first).
	sort.Sort(sort.Reverse(sort.StringSlice(attemptDirs)))
	return attemptDirs[0], nil
}

func parseDagRunID(dirName string) string {
	matches := reDAGRunDir.FindStringSubmatch(dirName)
	if len(matches) < 3 {
		return ""
	}
	return matches[2]
}

func parseTimeToUnix(s string) int64 {
	t, err := stringutil.ParseTime(s)
	if err != nil || t.IsZero() {
		return 0
	}
	return t.Unix()
}

// parseStatusFile reads the status file. This is a local wrapper to avoid
// importing the parent dagrun package (which would create a circular dependency).
// It reads the file and finds the last valid JSON line.
// Keep in sync with internal/core/exec/runstatus.go:StatusFromJSON if the format changes.
func parseStatusFile(filePath string) (*exec.DAGRunStatus, error) {
	data, err := fileutil.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// Walk backwards to find the last valid status line.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		status, err := exec.StatusFromJSON(line)
		if err == nil {
			return status, nil
		}
	}

	return nil, fmt.Errorf("no valid status found in %s", filePath)
}
