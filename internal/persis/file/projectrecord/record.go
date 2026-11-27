// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package projectrecord keeps the record of a project's ID and name in
// each of the project's state directories, so a state directory is never
// attached to a project by its name alone.
package projectrecord

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	fileName       = "project.json"
	dirPermission  = 0o750
	filePermission = 0o600
)

// ErrOtherProject reports that a state directory records another project.
var ErrOtherProject = errors.New("state directory records another project")

// Record identifies the project a state directory belongs to.
type Record struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Write stores record in dir, creating dir when it does not exist. A dir
// that already holds record is left as it is. A dir that holds any other
// record is refused with an error wrapping ErrOtherProject, so a state
// directory never passes to a new project of the same name. Of concurrent
// writers, also in other processes, only one records its project.
func Write(dir string, record Record) error {
	if err := record.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project record: %w", err)
	}
	if err := fileutil.MkdirAll(dir, dirPermission); err != nil {
		return fmt.Errorf("create project state directory %s: %w", dir, err)
	}
	err = fileutil.WriteFileAtomicExclusive(filepath.Join(dir, fileName), data, filePermission)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("write project record in %s: %w", dir, err)
	}

	existing, err := Read(dir)
	if err != nil {
		return err
	}
	if existing != record {
		return fmt.Errorf("%s records project %s (%s), not %s (%s): %w",
			dir, existing.Name, existing.ID, record.Name, record.ID, ErrOtherProject)
	}
	return nil
}

// Remove deletes the record stored in dir when it is record. Any other
// record, or none, is left as it is.
func Remove(dir string, record Record) error {
	existing, err := Read(dir)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && existing != record) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := fileutil.Remove(filepath.Join(dir, fileName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove project record in %s: %w", dir, err)
	}
	return nil
}

// Read returns the record stored in dir. The error wraps fs.ErrNotExist
// when dir holds no record.
func Read(dir string) (Record, error) {
	data, err := fileutil.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		return Record{}, fmt.Errorf("read project record in %s: %w", dir, err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("read project record in %s: %w", dir, err)
	}
	if err := record.validate(); err != nil {
		return Record{}, fmt.Errorf("read project record in %s: %w", dir, err)
	}
	return record, nil
}

func (r Record) validate() error {
	switch {
	case r.ID == "":
		return errors.New("project record has no ID")
	case r.Name == "":
		return errors.New("project record has no name")
	}
	return nil
}
