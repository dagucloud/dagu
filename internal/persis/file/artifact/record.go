// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package artifact stores the per-run index of DAG-run artifact directories.
package artifact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// RecordVersion identifies the sidecar format. Readers skip records they do
// not understand rather than failing a listing.
const RecordVersion = 1

// Record describes one DAG-run artifact directory. It is written beside the
// directory rather than inside it, because artifact paths are chosen by the
// workflow author and a step could otherwise overwrite it.
//
// It carries the identity that the directory name deliberately omits, so a
// listing can walk the date tree and read names alone until it needs detail.
type Record struct {
	Version   int       `json:"v"`
	Name      string    `json:"name"`
	DAGRunID  string    `json:"dagRunId"`
	AttemptID string    `json:"attemptId,omitempty"`
	Status    ir.Status `json:"status"`

	StartedAt  string   `json:"startedAt,omitempty"`
	FinishedAt string   `json:"finishedAt,omitempty"`
	Labels     []string `json:"labels,omitempty"`

	// Dir is absolute, and points outside the date tree when the DAG relocates
	// its artifacts with artifacts.dir.
	Dir string `json:"dir"`
}

// RecordFromStatus builds the sidecar contents for a finished DAG-run.
func RecordFromStatus(status ir.DAGRunStatus) Record {
	return Record{
		Version:    RecordVersion,
		Name:       status.Name,
		DAGRunID:   status.DAGRunID,
		AttemptID:  status.AttemptID,
		Status:     status.Status,
		StartedAt:  status.StartedAt,
		FinishedAt: status.FinishedAt,
		Labels:     status.Labels,
		Dir:        status.ArchiveDir,
	}
}

// WriteRecord records a run in the artifact index.
//
// A run's terminal status is persisted more than once, so writing the same
// attempt again is a no-op. A later attempt of the same run reuses the
// directory and does replace the record, so that an auto-retried run is not
// indexed forever under the outcome of its first attempt.
func WriteRecord(path string, rec Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal artifact record: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("create artifact index directory: %w", err)
	}

	writeErr := fileutil.WriteFileAtomicExclusive(path, data, 0600)
	if writeErr == nil {
		return nil
	}
	if !errors.Is(writeErr, fs.ErrExist) {
		return fmt.Errorf("write artifact record: %w", writeErr)
	}

	// Comparing the encoded record rather than the attempt it came from keeps a
	// repeated write free while letting anything that actually changed win,
	// including a later attempt and a run whose outcome was revised.
	existing, err := fileutil.ReadFile(path)
	if err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err := fileutil.WriteFileAtomic(path, data, 0600); err != nil {
		return fmt.Errorf("replace artifact record: %w", err)
	}
	return nil
}

// ReadRecord loads a single artifact index record.
func ReadRecord(path string) (*Record, error) {
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("decode artifact record %s: %w", path, err)
	}
	// Only a newer format is unreadable. An older one decodes with its added
	// fields left zero, which every reader already has to handle.
	if rec.Version > RecordVersion {
		return nil, fmt.Errorf("unsupported artifact record version %d in %s", rec.Version, path)
	}
	return &rec, nil
}

// DirHasEntries reports whether a run left anything in its artifact directory.
// A missing directory counts as empty, so a run whose artifacts were already
// removed is not indexed.
func DirHasEntries(dir string) (bool, error) {
	// Reading one name rather than the whole directory. os.ReadDir would read
	// and sort every entry to answer this, which costs milliseconds on a run
	// that wrote thousands of files and nothing on one that wrote three.
	// #nosec G304 -- the directory comes from a run's recorded artifact path.
	f, err := os.Open(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer func() { _ = f.Close() }()

	names, err := f.Readdirnames(1)
	if err != nil && len(names) == 0 {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	return len(names) > 0, nil
}
