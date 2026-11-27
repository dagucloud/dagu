// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package projectregistry

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// InPlace returns the registry's records of the files that run in place.
// projectsDir is paths.projects_dir, which holds the DAG directory of each
// project other than the default one. A record ends once that directory
// holds, at its top, a DAG file of the recorded file's name.
func (r *Registry) InPlace(projectsDir string) project.InPlaceRecords {
	return &inPlaceRecords{registry: r, projectsDir: projectsDir}
}

type inPlaceRecords struct {
	registry    *Registry
	projectsDir string
}

var _ project.InPlaceRecords = (*inPlaceRecords)(nil)

func (rec *inPlaceRecords) InPlaceOwner(_ context.Context, path string) (string, error) {
	entries, err := rec.registry.load()
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		for _, file := range e.inPlace {
			if samePath(file, path) && !rec.ended(e.Name, file) {
				return e.Name, nil
			}
		}
	}
	return "", nil
}

func (rec *inPlaceRecords) InPlaceFiles(_ context.Context, name string) ([]string, error) {
	entries, err := rec.registry.load()
	if err != nil {
		return nil, err
	}
	i := indexByName(entries, name)
	if i < 0 {
		return nil, nil
	}
	var files []string
	for _, file := range entries[i].inPlace {
		if !rec.ended(entries[i].Name, file) {
			files = append(files, file)
		}
	}
	return files, nil
}

// ended reports whether the DAG directory of the project called name holds
// a DAG file of the same name as file, which then wins over file.
func (rec *inPlaceRecords) ended(name, file string) bool {
	if rec.projectsDir == "" {
		return false
	}
	stem := fileutil.TrimYAMLFileExtension(filepath.Base(file))
	for _, ext := range fileutil.ValidYAMLExtensions {
		if fileutil.IsFile(filepath.Join(rec.projectsDir, name, stem+ext)) {
			return true
		}
	}
	return false
}

// samePath reports whether a and b name one file. Paths that differ only
// in case name one file where the file system ignores case.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	if !strings.EqualFold(a, b) {
		return false
	}
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}
