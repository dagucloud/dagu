// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filedagrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// Error definitions for directory structure validation
var (
	ErrInvalidDAGRunsDir = errors.New("invalid dag-runs directory name")
)

const (
	// SubDAGRunsDir is the name of the directory where status files for sub dag-runs are stored.
	SubDAGRunsDir = "children"

	// SubDAGRunDirPrefix is the prefix for sub dag-run directories.
	SubDAGRunDirPrefix = "child_"

	// DAGRunDirPrefix is the prefix for dag-run directories.
	DAGRunDirPrefix = "dag-run_"

	// AttemptDirPrefix is the prefix for attempt directories.
	AttemptDirPrefix = "attempt_"
)

// JSONLStatusFile is the name of the status file for each dag-run.
// It contains the status of the dag-run in JSON Lines format.
// While running the dag-run, new lines are appended to this file on each status update.
// After finishing the run, this file will be compacted into a single JSON line file.
const JSONLStatusFile = "status.jsonl"

// DAGRunSummary holds pre-loaded summary data from a day index.
// When non-nil, it allows filtering and constructing list responses
// without reading status.jsonl.
type DAGRunSummary struct {
	LatestAttemptDir     string
	Status               core.Status
	StartedAtUnix        int64
	FinishedAtUnix       int64
	Labels               []string
	Name                 string
	DagRunID             string
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

// DAGRun represents a dag-run with its associated timestamp and run ID.
type DAGRun struct {
	baseDir     string         // Base directory path for this run
	artifactDir string         // Trusted root for artifact cleanup
	timestamp   time.Time      // Timestamp when the run was created
	dagRunID    string         // Unique identifier for the dag-run
	summary     *DAGRunSummary // Optional pre-loaded summary from index
}

// NewDAGRun creates a new Run instance from a directory path.
// It parses the directory name to extract the timestamp and dag-run ID.
func NewDAGRun(dir string) (*DAGRun, error) {
	return newDAGRun(dir, "")
}

func newDAGRun(dir, artifactDir string) (*DAGRun, error) {
	// Determine if the run is a sub dag-run or a regular dag-run.
	parentDir := filepath.Dir(dir)
	if filepath.Base(parentDir) == SubDAGRunsDir {
		matches := reSubDAGRunDir.FindStringSubmatch(filepath.Base(dir))
		if len(matches) != 2 {
			return nil, ErrInvalidDAGRunsDir
		}
		return &DAGRun{
			baseDir:     dir,
			artifactDir: artifactDir,
			dagRunID:    matches[1],
		}, nil
	}

	matches := reDAGRunDir.FindStringSubmatch(filepath.Base(dir))
	if len(matches) != 3 {
		return nil, ErrInvalidDAGRunsDir
	}
	ts, err := parseDAGRunTimestamp(matches[1])
	if err != nil {
		return nil, err
	}
	return &DAGRun{
		baseDir:     dir,
		artifactDir: artifactDir,
		timestamp:   ts,
		dagRunID:    matches[2],
	}, nil
}

// CreateAttempt creates a new Attempt for the dag-run with the given timestamp.
// It creates a new Attempt directory and initializes a record within it.
// If attemptID is provided, it uses that ID instead of generating a new one.
func (dr DAGRun) CreateAttempt(_ context.Context, ts exec.TimeInUTC, cache *fileutil.Cache[*exec.DAGRunStatus], attemptID string, opts ...AttemptOption) (*Attempt, error) {
	attID := attemptID
	if attID == "" {
		var err error
		attID, err = genAttemptID()
		if err != nil {
			return nil, err
		}
	}
	dir := filepath.Join(dr.baseDir, AttemptDirPrefix+formatAttemptTimestamp(ts)+"_"+attID)
	// Error if the directory already exists
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("run directory already exists: %s", dir)
	}
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create the run directory: %w", err)
	}
	return NewAttempt(filepath.Join(dir, JSONLStatusFile), cache, opts...)
}

// CreateSubDAGRun creates a new sub dag-run with the given timestamp and dag-run ID.
func (dr DAGRun) CreateSubDAGRun(_ context.Context, dagRunID string) (*DAGRun, error) {
	dirName := SubDAGRunDirPrefix + dagRunID
	dir := filepath.Join(dr.baseDir, SubDAGRunsDir, dirName)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create sub dag-run directory: %w", err)
	}
	return newDAGRun(dir, dr.artifactDir)
}

// FindSubDAGRun searches for a sub dag-run by its run ID.
func (dr DAGRun) FindSubDAGRun(_ context.Context, dagRunID string) (*DAGRun, error) {
	globPattern := filepath.Join(dr.baseDir, SubDAGRunsDir, SubDAGRunDirPrefix+dagRunID)
	matches, err := filepath.Glob(globPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to list sub dag-runs: %w", err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no matching sub dag-run found for ID %s (glob=%s): %w", dagRunID, globPattern, exec.ErrDAGRunIDNotFound)
	}
	// Sort the matches by timestamp
	sort.Slice(matches, func(i, j int) bool {
		return matches[i] > matches[j]
	})
	return newDAGRun(matches[0], dr.artifactDir)
}

func (dr DAGRun) ListSubDAGRuns(ctx context.Context) ([]*DAGRun, error) {
	subDir := filepath.Join(dr.baseDir, SubDAGRunsDir)
	entries, err := os.ReadDir(subDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*DAGRun{}, nil
		}
		return nil, fmt.Errorf("failed to read sub dag-runs directory: %w", err)
	}

	var dagRuns []*DAGRun
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// check if the directory name matches the sub dag-run directory pattern
		if !reSubDAGRunDir.MatchString(entry.Name()) {
			continue
		}

		subDAGRun, err := newDAGRun(filepath.Join(subDir, entry.Name()), dr.artifactDir)
		if err != nil {
			logger.Error(ctx, "Failed to read sub dag-run data",
				tag.Error(err),
				tag.RunID(dr.dagRunID),
				tag.Dir(entry.Name()))
			continue
		}
		dagRuns = append(dagRuns, subDAGRun)
	}
	return dagRuns, nil
}

// LatestAttempt returns the most recent Attempt for the dag-run.
// It searches through all run directories and returns the first valid Attempt found.
// It skips hidden attempts (dequeued ones).
func (dr DAGRun) LatestAttempt(ctx context.Context, cache *fileutil.Cache[*exec.DAGRunStatus]) (*Attempt, error) {
	attDirs, err := listDirsSorted(dr.baseDir, true, reAttemptDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list run directories: %w", err)
	}
	// Return the first valid run
	for _, attDir := range attDirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		att, err := NewAttempt(filepath.Join(dr.baseDir, attDir, JSONLStatusFile), cache)
		if err != nil {
			logger.Error(ctx, "Failed to read a run data", tag.Error(err))
			continue
		}
		if att.Hidden() {
			continue
		}
		if att.Exists() {
			return att, nil
		}
	}
	return nil, exec.ErrNoStatusData
}

// AttemptByDir constructs an Attempt directly from a known attempt directory name,
// skipping the directory listing and sorting done by LatestAttempt.
func (dr DAGRun) AttemptByDir(attemptDir string, cache *fileutil.Cache[*exec.DAGRunStatus]) (*Attempt, error) {
	return NewAttempt(filepath.Join(dr.baseDir, attemptDir, JSONLStatusFile), cache)
}

// Remove deletes the entire dag-run directory and all its contents.
func (dr DAGRun) Remove(ctx context.Context) error {
	if err := dr.removeLogFiles(ctx); err != nil {
		logger.Error(ctx, "Failed to remove log files",
			tag.Error(err),
			tag.RunID(dr.dagRunID))
	}
	return fileutil.RemoveAllWithRetry(dr.baseDir)
}

// removeLogFiles removes all log files associated with the dag-run and its sub dag-runs.
func (dr DAGRun) removeLogFiles(ctx context.Context) error {
	deleteFiles, err := dr.listLogFiles(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list log files to remove",
			tag.Error(err),
			tag.RunID(dr.dagRunID))
	}
	artifactDirs, err := dr.listArtifactDirs(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list artifact directories to remove",
			tag.Error(err),
			tag.RunID(dr.dagRunID))
	}

	children, err := dr.ListSubDAGRuns(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list sub dag-runs",
			tag.Error(err),
			tag.RunID(dr.dagRunID))
	}
	for _, child := range children {
		subLogFiles, err := child.listLogFiles(ctx)
		if err != nil {
			logger.Error(ctx, "Failed to list log files for sub dag-run",
				tag.Error(err),
				tag.RunID(child.dagRunID))
		}
		deleteFiles = append(deleteFiles, subLogFiles...)
		subArtifactDirs, err := child.listArtifactDirs(ctx)
		if err != nil {
			logger.Error(ctx, "Failed to list artifact directories for sub dag-run",
				tag.Error(err),
				tag.RunID(child.dagRunID))
		}
		artifactDirs = append(artifactDirs, subArtifactDirs...)
	}

	parentDirs := make(map[string]struct{})
	uniqueArtifactDirs := make(map[string]struct{})
	for _, dir := range artifactDirs {
		if dir == "" {
			continue
		}
		uniqueArtifactDirs[dir] = struct{}{}
	}

	// Remove all log files.
	for _, file := range deleteFiles {
		if file == "" {
			continue
		}
		if err := fileutil.RemoveWithRetry(file); err != nil {
			logger.Error(ctx, "Failed to remove log file",
				tag.Error(err),
				tag.RunID(dr.dagRunID),
				tag.File(file))
		}
		parentDirs[filepath.Dir(file)] = struct{}{}
	}
	for dir := range uniqueArtifactDirs {
		validDir, ok := dr.validatedArtifactDir(dir)
		if !ok {
			logger.Warn(ctx, "Skipping artifact directory outside trusted artifact root",
				tag.Dir(dir),
				tag.RunID(dr.dagRunID),
			)
			continue
		}
		if err := fileutil.RemoveAllWithRetry(validDir); err != nil {
			logger.Error(ctx, "Failed to remove artifact directory",
				tag.Error(err),
				tag.RunID(dr.dagRunID),
				tag.Dir(validDir))
			continue
		}
		parentDirs[filepath.Dir(validDir)] = struct{}{}
	}

	// Remove parent dirs if they are empty.
	for p := range parentDirs {
		_ = fileutil.RemoveWithRetry(p)
	}

	return nil
}

// listAttemptDirs lists all attempt directories including hidden ones.
func (dr DAGRun) listAttemptDirs() ([]string, error) {
	entries, err := os.ReadDir(dr.baseDir)
	// If the directory does not exist, return nil
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Trim the dot prefix if it is a hidden directory
		name := strings.TrimPrefix(entry.Name(), ".")
		if reAttemptDir.MatchString(name) {
			dirs = append(dirs, entry.Name())
		}
	}

	// Sort in reverse order (newest first) based on timestamp
	sort.Slice(dirs, func(i, j int) bool {
		// Extract timestamps for comparison
		// Remove dot prefix if present for comparison
		nameI := strings.TrimPrefix(dirs[i], ".")
		nameJ := strings.TrimPrefix(dirs[j], ".")

		// Compare timestamps (the format ensures lexical sort = chronological sort)
		// Reverse order: newer (larger) timestamps come first
		return nameI > nameJ
	})
	return dirs, nil
}

// listLogFiles lists all log files associated with the dag-run.
func (dr DAGRun) listLogFiles(ctx context.Context) ([]string, error) {
	attDirs, err := dr.listAttemptDirs()
	if err != nil {
		return nil, fmt.Errorf("failed to list attempt directories: %w", err)
	}

	var logFiles []string
	for _, attDir := range attDirs {
		attempt, err := NewAttempt(filepath.Join(dr.baseDir, attDir, JSONLStatusFile), nil)
		if err != nil {
			logger.Error(ctx, "Failed to read attempt data",
				tag.Error(err),
				tag.RunID(dr.dagRunID),
				tag.Dir(attDir))
			continue
		}
		if !attempt.Exists() {
			continue
		}
		status, err := attempt.ReadStatus(ctx)
		if err != nil {
			logger.Error(ctx, "Failed to read status for attempt",
				tag.Error(err),
				tag.RunID(dr.dagRunID),
				tag.AttemptID(attempt.ID()))
			continue
		}
		logFiles = append(logFiles, status.Log)
		for _, n := range status.Nodes {
			logFiles = append(logFiles, n.Stdout, n.Stderr)
		}
		for _, n := range []*exec.Node{
			status.OnSuccess, status.OnExit, status.OnFailure, status.OnAbort,
		} {
			if n == nil {
				continue
			}
			logFiles = append(logFiles, n.Stdout, n.Stderr)
		}
	}

	return logFiles, nil
}

func (dr DAGRun) listArtifactDirs(ctx context.Context) ([]string, error) {
	attDirs, err := dr.listAttemptDirs()
	if err != nil {
		return nil, fmt.Errorf("failed to list attempt directories: %w", err)
	}

	var artifactDirs []string
	for _, attDir := range attDirs {
		attempt, err := NewAttempt(filepath.Join(dr.baseDir, attDir, JSONLStatusFile), nil)
		if err != nil {
			logger.Error(ctx, "Failed to read attempt data",
				tag.Error(err),
				tag.RunID(dr.dagRunID),
				tag.Dir(attDir))
			continue
		}
		if !attempt.Exists() {
			continue
		}
		status, err := attempt.ReadStatus(ctx)
		if err != nil {
			logger.Error(ctx, "Failed to read status for attempt",
				tag.Error(err),
				tag.RunID(dr.dagRunID),
				tag.AttemptID(attempt.ID()))
			continue
		}
		if validDir, ok := dr.validatedArtifactDir(status.ArchiveDir); ok {
			artifactDirs = append(artifactDirs, validDir)
		} else if status.ArchiveDir != "" {
			logger.Warn(ctx, "Skipping persisted artifact directory outside trusted artifact root",
				tag.Dir(status.ArchiveDir),
				tag.RunID(dr.dagRunID),
				tag.AttemptID(attempt.ID()),
			)
		}
	}

	return artifactDirs, nil
}

func (dr DAGRun) validatedArtifactDir(dir string) (string, bool) {
	dir = strings.TrimSpace(dir)
	if dir == "" || dr.artifactDir == "" {
		return "", false
	}

	cleanDir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", false
	}
	cleanRoot, err := filepath.Abs(filepath.Clean(dr.artifactDir))
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(cleanRoot, cleanDir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return cleanDir, true
}

// Regular expressions for parsing directory names
var reDAGRunDir = regexp.MustCompile(`^` + DAGRunDirPrefix + `(\d{8}_\d{6}Z)_(.*)$`)         // Matches dag-run directory names
var reAttemptDir = regexp.MustCompile(`^` + AttemptDirPrefix + `(\d{8}_\d{6}_\d{3}Z)_(.*)$`) // Matches attempt directory names
var reSubDAGRunDir = regexp.MustCompile(`^` + SubDAGRunDirPrefix + `(.*)$`)                  // Matches sub dag-run directory names

// formatDAGRunTimestamp formats a models.TimeInUTC instance into a string representation (without milliseconds).
// The format is "YYYYMMDD_HHMMSSZ".
// This is used for generating 'run' directory names.
func formatDAGRunTimestamp(t exec.TimeInUTC) string {
	return t.Format(dateTimeFormatUTC)
}

// parseDAGRunTimestamp converts a timestamp string from a filename into a time.Time.
// The timestamp format is expected to match dateTimeFormatUTC.
func parseDAGRunTimestamp(s string) (time.Time, error) {
	t, err := time.Parse(dateTimeFormatUTC, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse UTC timestamp %s: %w", s, err)
	}
	return t, nil
}

// dateTimeFormatUTC is the format for run timestamps.
const dateTimeFormatUTC = "20060102_150405Z"

// formatAttemptTimestamp formats a models.TimeInUTC instance into a string representation with milliseconds.
// The format is "YYYYMMDD_HHMMSS_mmmZ" where "mmm" is the milliseconds part.
func formatAttemptTimestamp(t exec.TimeInUTC) string {
	const format = "20060102_150405"
	mill := t.UnixMilli()
	return t.Format(format) + "_" + fmt.Sprintf("%03d", mill%1000) + "Z"
}

// genAttemptID generates unique run ID
func genAttemptID() (string, error) {
	// 3 bytes → 6 hex characters
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to read random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}
