// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	planFileName       = "plan.json"
	movedFileName      = "moved.log"
	installingFileName = "installing.log"
	statusFileName     = "status.json"
	filePerm           = 0o600
)

// journal keeps the plan, the moves made so far, and the progress in one
// directory, so a killed migration resumes with the plan it started.
type journal struct {
	dir string
}

type planFile struct {
	Moves []move `json:"moves"`
}

// readStatus returns the progress last written, and false when none was.
// An unreadable status is an error rather than "not done": migrating again
// after a finished migration could undo moves that later processes built on.
func (j journal) readStatus() (Progress, bool, error) {
	var status Progress
	found, err := j.readJSON(statusFileName, &status)
	if err != nil || !found {
		return Progress{}, found, err
	}
	if status.Phase == "" {
		return Progress{}, false, fmt.Errorf("read %s: no phase", j.path(statusFileName))
	}
	return status, true, nil
}

func (j journal) writeStatus(status Progress) error {
	return j.writeJSON(statusFileName, status)
}

// readPlan returns the recorded moves, and false when no plan was recorded.
func (j journal) readPlan() ([]move, bool, error) {
	var plan planFile
	found, err := j.readJSON(planFileName, &plan)
	return plan.Moves, found, err
}

func (j journal) writePlan(moves []move) error {
	return j.writeJSON(planFileName, planFile{Moves: moves})
}

// readMoved returns the indexes below total that the journal records as
// moved. A line that a crash cut short is skipped; its move runs again and
// finds itself done.
func (j journal) readMoved(total int) (map[int]struct{}, error) {
	return j.readIndexes(movedFileName, total)
}

func (j journal) markMoved(index int) error {
	if err := fileutil.AppendLines(j.path(movedFileName), filePerm, []byte(strconv.Itoa(index))); err != nil {
		return fmt.Errorf("record move %d in %s: %w", index, j.path(movedFileName), err)
	}
	return nil
}

// readInstalling returns the indexes below total whose copy the journal
// records as about to take its destination's name.
func (j journal) readInstalling(total int) (map[int]struct{}, error) {
	return j.readIndexes(installingFileName, total)
}

// markInstalling records that the copy of the move at index is about to take
// its destination's name. The record is durable once it returns, so no
// installed copy outlasts a crash without it.
func (j journal) markInstalling(index int) error {
	path := j.path(installingFileName)
	if err := fileutil.AppendLines(path, filePerm, []byte(strconv.Itoa(index))); err != nil {
		return fmt.Errorf("record the copy of move %d in %s: %w", index, path, err)
	}
	if err := syncFile(path); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	return fileutil.SyncDir(j.dir)
}

func (j journal) readIndexes(name string, total int) (map[int]struct{}, error) {
	indexes := make(map[int]struct{})
	data, err := fileutil.ReadFile(j.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return indexes, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", j.path(name), err)
	}
	for line := range bytes.Lines(data) {
		index, err := strconv.Atoi(string(bytes.TrimSpace(line)))
		if err == nil && index >= 0 && index < total {
			indexes[index] = struct{}{}
		}
	}
	return indexes, nil
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // path lies in the journal directory
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (j journal) readJSON(name string, v any) (bool, error) {
	data, err := fileutil.ReadFile(j.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", j.path(name), err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("read %s: %w", j.path(name), err)
	}
	return true, nil
}

func (j journal) writeJSON(name string, v any) error {
	if err := fileutil.MkdirAll(j.dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", j.dir, err)
	}
	if err := fileutil.WriteJSONAtomic(j.path(name), v, filePerm); err != nil {
		return fmt.Errorf("write %s: %w", j.path(name), err)
	}
	return nil
}

func (j journal) path(name string) string {
	return filepath.Join(j.dir, name)
}
