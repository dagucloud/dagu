// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
)

const (
	procFileVersion   = 1
	procFilePrefix    = "proc_"
	procFileExt       = ".proc"
	procHeartbeatSize = 8
	procDateTimeUTC   = "20060102_150405"
	procFileTimeFmt   = procDateTimeUTC + "Z"
)

var (
	errInvalidProcFile       = errors.New("invalid proc file")
	procFileRegex            = regexp.MustCompile(`^proc_(\d{8}_\d{6}Z)_([0-9a-f]+)_([0-9a-f]+)\.proc$`)
	procLegacyFileRegex      = regexp.MustCompile(`^proc_(\d{8}_\d{6}Z)_([-a-zA-Z0-9_]+)\.proc$`)
	procSafeAttemptIDPattern = regexp.MustCompile(`^[-a-zA-Z0-9_]+$`)
)

type procDiskMeta struct {
	Version      int    `json:"version"`
	DAGName      string `json:"dag_name"`
	DAGRunID     string `json:"dag_run_id"`
	AttemptID    string `json:"attempt_id"`
	RootName     string `json:"root_name,omitempty"`
	RootDAGRunID string `json:"root_dag_run_id,omitempty"`
	StartedAt    int64  `json:"started_at"`
}

type procFileFormat int

const (
	procFileFormatCurrent procFileFormat = iota + 1
	procFileFormatLegacy
)

type procFileName struct {
	format    procFileFormat
	createdAt time.Time
	dagRunID  string
	attemptID string
}

func validateProcMeta(meta exec.ProcMeta) error {
	if meta.Name == "" {
		return fmt.Errorf("proc meta name is required")
	}
	if err := exec.ValidateDAGRunID(meta.DAGRunID); err != nil {
		return fmt.Errorf("invalid proc meta dag run id: %w", err)
	}
	if meta.AttemptID == "" {
		return fmt.Errorf("proc meta attempt id is required")
	}
	if !procSafeAttemptIDPattern.MatchString(meta.AttemptID) {
		return fmt.Errorf("proc meta attempt id must only contain alphanumeric characters, dashes, and underscores")
	}
	if meta.StartedAt <= 0 {
		return fmt.Errorf("proc meta started at must be > 0")
	}
	if (meta.RootName == "") != (meta.RootDAGRunID == "") {
		return fmt.Errorf("proc meta root name and root dag run id must both be set or both be empty")
	}
	if meta.RootDAGRunID != "" {
		if err := exec.ValidateDAGRunID(meta.RootDAGRunID); err != nil {
			return fmt.Errorf("invalid proc meta root dag run id: %w", err)
		}
	}
	return nil
}

func procRecordID(groupName string, meta exec.ProcMeta, t time.Time) string {
	return filepath.ToSlash(filepath.Join(groupName, meta.Name, procRecordName(meta, t)))
}

func procRecordName(meta exec.ProcMeta, t time.Time) string {
	return fmt.Sprintf("%s%sZ_%s_%s",
		procFilePrefix,
		t.UTC().Format(procDateTimeUTC),
		hex.EncodeToString([]byte(meta.DAGRunID)),
		hex.EncodeToString([]byte(meta.AttemptID)),
	)
}

func procLegacyFilePath(root, groupName string, meta exec.ProcMeta, t time.Time) string {
	return filepath.Join(root, groupName, meta.Name, procRecordName(meta, t)+procFileExt)
}

func procEntryIsLegacyPath(path string) bool {
	return strings.HasSuffix(path, procFileExt)
}

func writeLegacyProcFile(path string, heartbeatUnix int64, meta exec.ProcMeta) error {
	if err := validateProcMeta(meta); err != nil {
		return err
	}
	metaBytes, err := json.Marshal(procDiskMeta{
		Version:      procFileVersion,
		DAGName:      meta.Name,
		DAGRunID:     meta.DAGRunID,
		AttemptID:    meta.AttemptID,
		RootName:     meta.RootName,
		RootDAGRunID: meta.RootDAGRunID,
		StartedAt:    meta.StartedAt,
	})
	if err != nil {
		return err
	}
	data := make([]byte, procHeartbeatSize+len(metaBytes))
	binary.BigEndian.PutUint64(data[:procHeartbeatSize], uint64(heartbeatUnix)) //nolint:gosec // heartbeat unix time is validated by caller.
	copy(data[procHeartbeatSize:], metaBytes)
	return writeLegacyProcFileAtomic(path, data)
}

func writeLegacyProcFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-proc-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := fileutil.ReplaceFileWithRetry(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func removeLegacyProcFile(path string) error {
	var lastErr error
	for attempt := range 12 {
		err := os.Remove(path)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			removeEmptyLegacyDirs(filepath.Dir(path))
			return nil
		}
		if !isRetryableLegacyProcFileError(err) {
			return err
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	return lastErr
}

func removeEmptyLegacyDirs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > 0 {
		return
	}
	_ = os.Remove(dir)
}

func isRetryableLegacyProcFileError(err error) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "used by another process") || strings.Contains(msg, "access is denied")
}

func (s *ProcStore) listLegacyEntries(groupName string) ([]exec.ProcEntry, error) {
	groupDir := filepath.Join(s.legacyDir, groupName)
	if _, err := os.Stat(groupDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	files, err := procLegacyFilesInGroup(groupDir)
	if err != nil {
		return nil, err
	}
	return s.legacyEntriesFromFiles(groupName, files)
}

func (s *ProcStore) listAllLegacyEntries() ([]exec.ProcEntry, error) {
	dirEntries, err := os.ReadDir(s.legacyDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var entries []exec.ProcEntry
	for _, entry := range dirEntries {
		if !entry.IsDir() {
			continue
		}
		groupName := entry.Name()
		files, err := procLegacyFilesInGroup(filepath.Join(s.legacyDir, groupName))
		if err != nil {
			return nil, err
		}
		groupEntries, err := s.legacyEntriesFromFiles(groupName, files)
		if err != nil {
			return nil, err
		}
		entries = append(entries, groupEntries...)
	}
	return entries, nil
}

func procLegacyFilesInGroup(groupDir string) ([]string, error) {
	dagEntries, err := os.ReadDir(groupDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, dagEntry := range dagEntries {
		if !dagEntry.IsDir() || dagEntry.Name() == "" || dagEntry.Name()[0] == '.' {
			continue
		}
		procEntries, err := os.ReadDir(filepath.Join(groupDir, dagEntry.Name()))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, procEntry := range procEntries {
			if procEntry.IsDir() || filepath.Ext(procEntry.Name()) != procFileExt {
				continue
			}
			files = append(files, filepath.Join(groupDir, dagEntry.Name(), procEntry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func (s *ProcStore) legacyEntriesFromFiles(groupName string, files []string) ([]exec.ProcEntry, error) {
	now := time.Now().UTC()
	entries := make([]exec.ProcEntry, 0, len(files))
	for _, file := range files {
		entry, err := readLegacyProcEntry(file, groupName, s.staleTime, now)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *ProcStore) removeLegacyIfStale(ctx context.Context, entry exec.ProcEntry) error {
	current, err := readLegacyProcEntry(entry.FilePath, entry.GroupName, s.staleTime, time.Now().UTC())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Fresh || !sameProcEntry(current, entry) {
		return nil
	}
	if err := removeLegacyProcFile(entry.FilePath); err != nil {
		return err
	}
	logger.Info(ctx, "Removed stale legacy proc file", tag.File(entry.FilePath))
	return nil
}

func readLegacyProcEntry(path, groupName string, staleTime time.Duration, now time.Time) (exec.ProcEntry, error) {
	filename := filepath.Base(path)
	parsedName, err := parseProcFileName(filename)
	if err != nil {
		return exec.ProcEntry{}, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return exec.ProcEntry{}, err
	}

	data, err := fileutil.ReadFileWithRetry(path)
	if err != nil {
		return exec.ProcEntry{}, err
	}
	if len(data) < procHeartbeatSize {
		return exec.ProcEntry{}, fmt.Errorf("%w: proc file %s is shorter than the %d-byte heartbeat header", errInvalidProcFile, path, procHeartbeatSize)
	}

	lastHeartbeatAt := int64(binary.BigEndian.Uint64(data[:procHeartbeatSize])) //nolint:gosec // heartbeat unix time.
	heartbeatTime := time.Unix(lastHeartbeatAt, 0).UTC()
	if heartbeatTime.After(now.Add(5 * time.Minute)) {
		return exec.ProcEntry{}, fmt.Errorf("%w: proc heartbeat timestamp is in the future for %s", errInvalidProcFile, path)
	}

	meta, err := procMetaFromLegacyData(path, parsedName, data[procHeartbeatSize:], heartbeatTime, info)
	if err != nil {
		return exec.ProcEntry{}, err
	}

	fresh := now.Sub(info.ModTime()) < staleTime
	if !fresh {
		fresh = now.Sub(heartbeatTime) < staleTime
	}
	return exec.ProcEntry{
		GroupName:       groupName,
		FilePath:        path,
		Meta:            meta,
		LastHeartbeatAt: lastHeartbeatAt,
		Fresh:           fresh,
	}, nil
}

func procMetaFromLegacyData(path string, parsedName procFileName, payload []byte, heartbeatTime time.Time, info os.FileInfo) (exec.ProcMeta, error) {
	switch parsedName.format {
	case procFileFormatCurrent:
		if len(payload) == 0 {
			return exec.ProcMeta{}, fmt.Errorf("%w: proc file %s is missing metadata payload", errInvalidProcFile, path)
		}
		var diskMeta procDiskMeta
		if err := json.Unmarshal(payload, &diskMeta); err != nil {
			return exec.ProcMeta{}, fmt.Errorf("%w: decode proc metadata: %w", errInvalidProcFile, err)
		}
		if diskMeta.Version != procFileVersion {
			return exec.ProcMeta{}, fmt.Errorf("%w: unsupported proc version %d", errInvalidProcFile, diskMeta.Version)
		}
		meta := exec.ProcMeta{
			StartedAt:    diskMeta.StartedAt,
			Name:         diskMeta.DAGName,
			DAGRunID:     diskMeta.DAGRunID,
			AttemptID:    diskMeta.AttemptID,
			RootName:     diskMeta.RootName,
			RootDAGRunID: diskMeta.RootDAGRunID,
		}
		if err := validateProcMeta(meta); err != nil {
			return exec.ProcMeta{}, fmt.Errorf("%w: %w", errInvalidProcFile, err)
		}
		if parsedName.dagRunID != meta.DAGRunID || parsedName.attemptID != meta.AttemptID {
			return exec.ProcMeta{}, fmt.Errorf("%w: proc filename/body mismatch for %s", errInvalidProcFile, path)
		}
		if filepath.Base(filepath.Dir(path)) != meta.Name {
			return exec.ProcMeta{}, fmt.Errorf("%w: proc path/body DAG name mismatch for %s", errInvalidProcFile, path)
		}
		return meta, nil
	case procFileFormatLegacy:
		if len(payload) != 0 {
			return exec.ProcMeta{}, fmt.Errorf("%w: legacy proc file %s must only contain the heartbeat header", errInvalidProcFile, path)
		}
		return legacyProcMeta(path, parsedName, heartbeatTime, info)
	default:
		return exec.ProcMeta{}, fmt.Errorf("%w: unsupported proc filename format for %s", errInvalidProcFile, path)
	}
}

func parseProcFileName(filename string) (procFileName, error) {
	if matches := procFileRegex.FindStringSubmatch(filename); len(matches) == 4 {
		createdAt, err := time.Parse(procFileTimeFmt, matches[1])
		if err != nil {
			return procFileName{}, fmt.Errorf("%w: parse proc timestamp: %w", errInvalidProcFile, err)
		}
		dagRunID, err := hex.DecodeString(matches[2])
		if err != nil {
			return procFileName{}, fmt.Errorf("%w: decode dag-run id: %w", errInvalidProcFile, err)
		}
		attemptID, err := hex.DecodeString(matches[3])
		if err != nil {
			return procFileName{}, fmt.Errorf("%w: decode attempt id: %w", errInvalidProcFile, err)
		}
		return procFileName{
			format:    procFileFormatCurrent,
			createdAt: createdAt.UTC(),
			dagRunID:  string(dagRunID),
			attemptID: string(attemptID),
		}, nil
	}
	if matches := procLegacyFileRegex.FindStringSubmatch(filename); len(matches) == 3 {
		createdAt, err := time.Parse(procFileTimeFmt, matches[1])
		if err != nil {
			return procFileName{}, fmt.Errorf("%w: parse legacy proc timestamp: %w", errInvalidProcFile, err)
		}
		if err := exec.ValidateDAGRunID(matches[2]); err != nil {
			return procFileName{}, fmt.Errorf("%w: invalid legacy dag-run id: %w", errInvalidProcFile, err)
		}
		return procFileName{
			format:    procFileFormatLegacy,
			createdAt: createdAt.UTC(),
			dagRunID:  matches[2],
			attemptID: legacyProcAttemptID(matches[2]),
		}, nil
	}
	return procFileName{}, fmt.Errorf("%w: invalid proc filename %q", errInvalidProcFile, filename)
}

func legacyProcAttemptID(dagRunID string) string {
	return "legacy_" + hex.EncodeToString([]byte(dagRunID))
}

func legacyProcMeta(path string, parsedName procFileName, heartbeatTime time.Time, info os.FileInfo) (exec.ProcMeta, error) {
	dagName := filepath.Base(filepath.Dir(path))
	if dagName == "" || dagName == "." || dagName == string(filepath.Separator) {
		return exec.ProcMeta{}, fmt.Errorf("%w: invalid legacy proc path %s", errInvalidProcFile, path)
	}

	startedAt := parsedName.createdAt.UTC().Unix()
	if startedAt <= 0 {
		startedAt = heartbeatTime.UTC().Unix()
	}
	if startedAt <= 0 {
		startedAt = info.ModTime().UTC().Unix()
	}

	meta := exec.ProcMeta{
		StartedAt:    startedAt,
		Name:         dagName,
		DAGRunID:     parsedName.dagRunID,
		AttemptID:    parsedName.attemptID,
		RootName:     dagName,
		RootDAGRunID: parsedName.dagRunID,
	}
	if err := validateProcMeta(meta); err != nil {
		return exec.ProcMeta{}, fmt.Errorf("%w: %w", errInvalidProcFile, err)
	}
	return meta, nil
}
