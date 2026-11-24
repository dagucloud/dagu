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
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	fileName       = "project.json"
	dirPermission  = 0o750
	filePermission = 0o600
)

// Record identifies the project a state directory belongs to.
type Record struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Write stores record in dir, creating dir when it does not exist and
// replacing any record already there.
func Write(dir string, record Record) error {
	if err := record.validate(); err != nil {
		return err
	}
	if err := fileutil.MkdirAll(dir, dirPermission); err != nil {
		return fmt.Errorf("create project state directory %s: %w", dir, err)
	}
	if err := fileutil.WriteJSONAtomic(filepath.Join(dir, fileName), record, filePermission); err != nil {
		return fmt.Errorf("write project record in %s: %w", dir, err)
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
