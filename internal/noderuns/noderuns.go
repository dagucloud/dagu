// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package noderuns stores the records that a step with nodes keeps beside
// its log files: the run of each node the step matched, and their counts by
// status (RFC 013 §Run view).
package noderuns

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Dir returns the directory of the records of a step whose standard output
// goes to stdout, or "" when the step has no log file.
func Dir(stdout, stepName string) string {
	if stdout == "" {
		return ""
	}
	return logpath.NodeRunsDir(filepath.Dir(stdout), stepName)
}

// Exists reports whether dir holds records.
func Exists(dir string) bool {
	return fileutil.FileExists(filepath.Join(dir, logpath.NodeRunsFile))
}

// Read returns the records in dir. The error matches fs.ErrNotExist when dir
// holds none.
func Read(dir string) (ir.NodeRuns, error) {
	var records ir.NodeRuns
	err := readJSON(filepath.Join(dir, logpath.NodeRunsFile), &records)
	return records, err
}

// ReadCounts returns the counts of the records in dir.
func ReadCounts(dir string) (ir.NodeRunCounts, error) {
	var counts ir.NodeRunCounts
	err := readJSON(filepath.Join(dir, logpath.NodeRunsSummaryFile), &counts)
	return counts, err
}

// Write records runs in dir and returns their counts. The counts are written
// after the records, so a reader of both never sees counts ahead of the
// records they count.
func Write(dir string, runs []ir.NodeRun) (ir.NodeRunCounts, error) {
	counts := ir.CountNodeRuns(runs)
	if err := fileutil.MkdirAll(dir, 0750); err != nil {
		return counts, err
	}
	if err := writeJSON(filepath.Join(dir, logpath.NodeRunsFile), ir.NodeRuns{Runs: runs}); err != nil {
		return counts, err
	}
	return counts, writeJSON(filepath.Join(dir, logpath.NodeRunsSummaryFile), counts)
}

func readJSON(path string, v any) error {
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0600)
}
