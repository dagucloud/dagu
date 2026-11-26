// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectrecord"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// projectDirPermission is the mode of a DAG directory that create makes.
const projectDirPermission = 0o750

// ErrStateDirTaken reports that a state directory of the name to create
// records another project.
var ErrStateDirTaken = errors.New("a state directory of this name records another project")

// CreateProject registers a project, makes its DAG directory when it does
// not exist, and records the project in each of its state directories.
// Besides the errors of Registry.Create, it refuses, and changes nothing,
// when the DAG directory would be or contain a protected path, and with an
// error wrapping ErrStateDirTaken while a state directory of the name
// records another project.
func CreateProject(ctx context.Context, cfg *config.Config, registry project.Registry, in project.CreateInput) (project.Project, error) {
	if err := project.ValidateName(in.Name); err != nil {
		return project.Project{}, err
	}
	// Taken before the state directory, whose name a file system may fold.
	existing, err := registry.Get(ctx, in.Name)
	switch {
	case err == nil:
		return project.Project{}, fmt.Errorf("%w: %s", project.ErrNameTaken, existing.Name)
	case !errors.Is(err, project.ErrNotFound):
		return project.Project{}, err
	}
	paths, err := cfg.ProjectPaths(in.Name)
	if err != nil {
		return project.Project{}, err
	}
	if err := cfg.CheckProjectDAGsDir(paths.DAGsDir); err != nil {
		return project.Project{}, err
	}
	for _, dir := range paths.RecordDirs {
		record, err := projectrecord.Read(dir)
		switch {
		case err == nil:
			return project.Project{}, fmt.Errorf("%s records project %s (%s); restore or delete it first: %w",
				dir, record.Name, record.ID, ErrStateDirTaken)
		case !errors.Is(err, fs.ErrNotExist):
			return project.Project{}, err
		}
	}

	created, err := registry.Create(ctx, in)
	if err != nil {
		return project.Project{}, err
	}
	if err := prepareProjectDirs(paths, created); err != nil {
		if rollbackErr := registry.Delete(ctx, created.ID); rollbackErr != nil {
			err = errors.Join(err, rollbackErr)
		}
		return project.Project{}, err
	}
	return created, nil
}

func prepareProjectDirs(paths config.ProjectPaths, p project.Project) error {
	if err := fileutil.MkdirAll(paths.DAGsDir, projectDirPermission); err != nil {
		return fmt.Errorf("create the DAG directory of project %s: %w", p.Name, err)
	}
	for _, dir := range paths.RecordDirs {
		if err := projectrecord.Write(dir, projectrecord.Record{ID: p.ID, Name: p.Name}); err != nil {
			return err
		}
	}
	return nil
}
