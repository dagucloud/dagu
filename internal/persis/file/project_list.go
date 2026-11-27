// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectrecord"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// ProjectState tells whether a registered project is in use.
type ProjectState string

// Project states.
const (
	ProjectStateActive ProjectState = "active"
	// ProjectStateMissing is a project whose DAG directory is gone and that
	// runs no file in place that exists. It is not scheduled.
	ProjectStateMissing ProjectState = "missing"
	// ProjectStateDeleting is a project marked deleting.
	ProjectStateDeleting ProjectState = "deleting"
)

// UnattachedStateDir is a state directory that records a project the
// registry lacks.
type UnattachedStateDir struct {
	Path string
	// Name and ID are those of the project the directory records.
	Name string
	ID   string
}

// ProjectState returns the state of p, a registered project.
func (s *ServerStores) ProjectState(ctx context.Context, p project.Project) (ProjectState, error) {
	if p.Deleting {
		return ProjectStateDeleting, nil
	}
	paths, err := s.cfg.ProjectPaths(p.Name)
	if err != nil {
		return "", err
	}
	if fileutil.FileExists(paths.DAGsDir) {
		return ProjectStateActive, nil
	}
	if s.inPlace != nil {
		files, err := s.inPlace.InPlaceFiles(ctx, p.Name)
		if err != nil {
			return "", err
		}
		for _, file := range files {
			if fileutil.FileExists(file) {
				return ProjectStateActive, nil
			}
		}
	}
	return ProjectStateMissing, nil
}

// UnattachedStateDirs returns the state directories in paths.data_dir that
// record a project registry lacks, ordered by name. A state directory is
// never attached to a project by its name.
func (s *ServerStores) UnattachedStateDirs(ctx context.Context, registry project.Registry) ([]UnattachedStateDir, error) {
	defaults, err := s.cfg.ProjectPaths(project.DefaultName)
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(defaults.StateDir)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the state directories in %s: %w", root, err)
	}
	projects, err := registry.List(ctx)
	if err != nil {
		return nil, err
	}
	registered := make(map[string]bool, len(projects))
	for _, p := range projects {
		registered[p.ID] = true
	}

	var unattached []UnattachedStateDir
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		record, err := projectrecord.Read(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !registered[record.ID] {
			unattached = append(unattached, UnattachedStateDir{Path: dir, Name: record.Name, ID: record.ID})
		}
	}
	return unattached, nil
}
