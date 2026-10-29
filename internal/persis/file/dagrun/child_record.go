// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// dagRunsDirName is the directory that holds a DAG's run history under the
// store's base directory.
const dagRunsDirName = "dag-runs"

// childRecordMarkerFile marks a dag-run directory that mirrors a nested child
// attempt. The record's status references log and artifact files owned by the
// canonical record nested under the root dag-run, so removal must skip them.
const childRecordMarkerFile = ".child-record"

// childRecord mirrors status writes from a nested child attempt into the child
// DAG's own run tree. The mirror keeps the child DAG's latest status and
// history consistent while the canonical record stays nested under the root
// dag-run for cancellation, retry, and cleanup.
type childRecord struct {
	resolved bool
	attempt  *Attempt
}

// childAttemptInfo locates a nested attempt's canonical data in its file path:
//
//	<store>/<dag>/dag-runs/<year>/<month>/<day>/dag-run_<ts>_<id>/sub/<child-id>/a_<ts>_<attempt-id>/status.jsonl
//
// Deeper nesting repeats the "sub/<id>" (or legacy "children/child_<id>")
// segment. The innermost run ID is the child dag-run's own ID.
type childAttemptInfo struct {
	storeBaseDir   string    // directory holding each DAG's dag-runs tree
	childRunID     string    // innermost sub dag-run ID
	attemptDirName string    // attempt directory name reused by the mirror record
	attemptTime    time.Time // attempt timestamp used when creating the mirror run
}

// parseChildAttemptStatusFile reports whether statusFile belongs to a nested
// child attempt and, if so, extracts the data needed to place its mirror.
func parseChildAttemptStatusFile(statusFile string) (childAttemptInfo, bool) {
	var info childAttemptInfo

	clean := filepath.Clean(statusFile)
	if filepath.Base(clean) != JSONLStatusFile {
		return info, false
	}
	attemptDir := filepath.Dir(clean)
	attemptDirName := filepath.Base(attemptDir)
	if strings.HasPrefix(attemptDirName, ".") || !IsAttemptDirName(attemptDirName) {
		return info, false
	}
	matches := reAttemptDir.FindStringSubmatch(attemptDirName)
	// Attempt timestamps are "YYYYMMDD_HHMMSS_mmmZ"; keeping the part up to
	// the millisecond separator yields the "YYYYMMDD_HHMMSSZ" run format.
	attemptTime, err := parseDAGRunTimestamp(matches[1][:dagRunTimestampLen-1] + "Z")
	if err != nil {
		return info, false
	}

	// Walk the chain of "<container>/<run-id>" segments up to the top-level
	// dag-run directory.
	dir := filepath.Dir(attemptDir)
	for {
		container := filepath.Dir(dir)
		runID, ok := subDAGRunIDFromDir(filepath.Base(container), filepath.Base(dir))
		if !ok {
			return childAttemptInfo{}, false
		}
		if info.childRunID == "" {
			info.childRunID = runID
		}
		dir = filepath.Dir(container)
		if reDAGRunDir.MatchString(filepath.Base(dir)) {
			break
		}
	}

	dayDir := filepath.Dir(dir)
	monthDir := filepath.Dir(dayDir)
	yearDir := filepath.Dir(monthDir)
	if !reDay.MatchString(filepath.Base(dayDir)) ||
		!reMonth.MatchString(filepath.Base(monthDir)) ||
		!reYear.MatchString(filepath.Base(yearDir)) {
		return childAttemptInfo{}, false
	}
	dagRunsDir := filepath.Dir(yearDir)
	if filepath.Base(dagRunsDir) != dagRunsDirName {
		return childAttemptInfo{}, false
	}

	info.storeBaseDir = filepath.Dir(filepath.Dir(dagRunsDir))
	info.attemptDirName = attemptDirName
	info.attemptTime = attemptTime
	return info, true
}

// childRecordAttemptLocked resolves the mirror attempt for a nested child
// attempt. It must be called with att.mu held. It returns nil when the attempt
// is a top-level record or the status does not belong to it. A missing mirror
// run record is created only when create is true.
func (att *Attempt) childRecordAttemptLocked(ctx context.Context, status ir.DAGRunStatus, create bool) (*Attempt, error) {
	if att.childRecord.resolved {
		return att.childRecord.attempt, nil
	}

	info, ok := parseChildAttemptStatusFile(att.file)
	if !ok ||
		status.Name == "" ||
		status.DAGRunID != info.childRunID ||
		status.Root == ir.NewDAGRunRef(status.Name, status.DAGRunID) {
		att.childRecord.resolved = true
		return nil, nil
	}

	dataRoot := NewDataRootWithArtifactDir(info.storeBaseDir, status.Name, att.artifactRoot)
	run, err := dataRoot.FindByDAGRunID(ctx, status.DAGRunID)
	if err != nil {
		if !errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			return nil, err
		}
		if !create {
			// The record may still appear through a later status write, so
			// leave the mirror unresolved rather than caching nil.
			return nil, nil
		}
		run, err = dataRoot.CreateDAGRun(persis.NewUTC(info.attemptTime), status.DAGRunID)
		if err != nil {
			return nil, fmt.Errorf("failed to create child dag-run record: %w", err)
		}
	}
	if err := writeChildRecordMarker(run.baseDir, att.file); err != nil {
		logger.Warn(ctx, "Failed to mark child dag-run record", tag.Error(err))
	}

	mirror, err := NewAttempt(
		filepath.Join(run.baseDir, info.attemptDirName, JSONLStatusFile),
		att.cache,
		WithArtifactRoot(att.artifactRoot),
	)
	if err != nil {
		return nil, err
	}
	if att.dag != nil {
		mirror.SetDAG(att.dag)
	}
	att.childRecord.resolved = true
	att.childRecord.attempt = mirror
	return mirror, nil
}

// writeChildRecordLocked appends the status to the child DAG's own run record.
// It must be called with att.mu held after the canonical write succeeded.
func (att *Attempt) writeChildRecordLocked(ctx context.Context, status ir.DAGRunStatus) error {
	mirror, err := att.childRecordAttemptLocked(ctx, status, true)
	if err != nil || mirror == nil {
		return err
	}
	if mirror.writer == nil {
		if err := mirror.Open(ctx); err != nil {
			return fmt.Errorf("failed to open child dag-run record: %w", err)
		}
	}
	return mirror.Write(ctx, status)
}

// childRecordMirror returns the mirror attempt for a nested child record,
// resolving it from the persisted status when needed, for example after the
// attempt was loaded back from disk. The mirror run record is never created
// here: it first appears when a status is written.
func (att *Attempt) childRecordMirror(ctx context.Context) (*Attempt, error) {
	att.mu.RLock()
	if att.childRecord.resolved {
		mirror := att.childRecord.attempt
		att.mu.RUnlock()
		return mirror, nil
	}
	att.mu.RUnlock()

	if _, ok := parseChildAttemptStatusFile(att.file); !ok {
		return nil, nil
	}
	status, err := att.ReadStatus(ctx)
	if err != nil || status == nil {
		return nil, err
	}

	att.mu.Lock()
	defer att.mu.Unlock()
	return att.childRecordAttemptLocked(ctx, *status, false)
}

// closeChildRecordLocked closes the mirror record. It must be called with
// att.mu held.
func (att *Attempt) closeChildRecordLocked(ctx context.Context) {
	if att.childRecord.attempt == nil {
		return
	}
	if err := att.childRecord.attempt.Close(ctx); err != nil {
		logger.Warn(ctx, "Failed to close child dag-run record", tag.Error(err))
	}
}

// hideChildRecordLocked hides the mirror record alongside the canonical one.
// It must be called with att.mu held.
func (att *Attempt) hideChildRecordLocked(ctx context.Context) {
	mirror := att.childRecord.attempt
	if mirror == nil {
		return
	}
	// A lazily opened mirror may still be open; settle it before hiding.
	if err := mirror.Close(ctx); err != nil {
		logger.Warn(ctx, "Failed to close child dag-run record", tag.Error(err))
	}
	if err := mirror.Hide(ctx); err != nil {
		logger.Warn(ctx, "Failed to hide child dag-run record", tag.Error(err))
	}
}

// isChildRecordDir reports whether runDir only records a child dag-run whose
// files belong to the canonical attempt nested under the root dag-run.
func isChildRecordDir(runDir string) bool {
	info, err := fileutil.Stat(filepath.Join(runDir, childRecordMarkerFile))
	return err == nil && !info.IsDir()
}

// isChildStatus reports whether the status belongs to a child dag-run: the
// parent ref is set, or the root ref names a different dag-run.
func isChildStatus(status ir.DAGRunStatus) bool {
	if !status.Parent.Zero() {
		return true
	}
	root := status.Root
	return !root.Zero() && root != ir.NewDAGRunRef(status.Name, status.DAGRunID)
}

// writeChildRecordMarker tags the mirror run directory so removal code does
// not delete log or artifact files owned by the canonical nested record.
func writeChildRecordMarker(runDir, canonicalStatusFile string) error {
	return fileutil.WriteFileAtomic(
		filepath.Join(runDir, childRecordMarkerFile),
		[]byte(canonicalStatusFile+"\n"),
		0600,
	)
}
