// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package projectregistry

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// InPlace returns the registry's records of the files that run in place.
// projectsDir is paths.projects_dir, which holds the DAG directory of each
// project other than the default one. A record ends, and is removed from
// the registry, once that directory holds at its top a DAG file of the
// recorded file's name, since the DAG is then addressed there.
func (r *Registry) InPlace(projectsDir string) project.InPlaceRecords {
	return &inPlaceRecords{registry: r, projectsDir: projectsDir}
}

type inPlaceRecords struct {
	registry    *Registry
	projectsDir string

	// mu guards the entries last read and the stamp of the file they were
	// read from. Migration writes the records before any process reads
	// them, and every later change removes a record, which changes the
	// file's size, so an unchanged stamp means unchanged records.
	mu      sync.Mutex
	stamp   fileStamp
	entries []entry
	loaded  bool
}

var _ project.InPlaceRecords = (*inPlaceRecords)(nil)

func (rec *inPlaceRecords) InPlaceOwner(ctx context.Context, path string) (string, error) {
	entries, err := rec.load()
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		for _, file := range e.inPlace {
			if !samePath(file, path) {
				continue
			}
			ended, err := rec.ended(e.Name, file)
			if err != nil {
				return "", err
			}
			if !ended {
				return e.Name, nil
			}
			return "", rec.registry.endInPlace(ctx, e.ID, []string{file})
		}
	}
	return "", nil
}

func (rec *inPlaceRecords) InPlaceFiles(ctx context.Context, name string) ([]string, error) {
	entries, err := rec.load()
	if err != nil {
		return nil, err
	}
	i := indexByName(entries, name)
	if i < 0 || len(entries[i].inPlace) == 0 {
		return nil, nil
	}
	stems, err := rec.dagFileStems(entries[i].Name)
	if err != nil {
		return nil, err
	}
	var files, ended []string
	for _, file := range entries[i].inPlace {
		if _, ok := stems[stemKey(file)]; ok {
			ended = append(ended, file)
		} else {
			files = append(files, file)
		}
	}
	if len(ended) > 0 {
		if err := rec.registry.endInPlace(ctx, entries[i].ID, ended); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// load returns the registry's entries, read again only when the file
// changed since the last read.
func (rec *inPlaceRecords) load() ([]entry, error) {
	stamp, err := rec.registry.stat()
	if err != nil {
		return nil, err
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.loaded && stamp == rec.stamp {
		return rec.entries, nil
	}
	entries, err := rec.registry.load()
	if err != nil {
		return nil, err
	}
	rec.entries, rec.stamp, rec.loaded = entries, stamp, true
	return entries, nil
}

// ended reports whether the DAG directory of the project called name holds
// a DAG file of the same name as file, which then wins over file.
func (rec *inPlaceRecords) ended(name, file string) (bool, error) {
	if rec.projectsDir == "" {
		return false, nil
	}
	stem := fileutil.TrimYAMLFileExtension(filepath.Base(file))
	for _, ext := range fileutil.ValidYAMLExtensions {
		info, err := os.Stat(filepath.Join(rec.projectsDir, name, stem+ext))
		switch {
		case err == nil && info.Mode().IsRegular():
			return true, nil
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return false, err
		}
	}
	return false, nil
}

// dagFileStems returns, by stemKey, the DAG files at the top of the DAG
// directory of the project called name.
func (rec *inPlaceRecords) dagFileStems(name string) (map[string]struct{}, error) {
	if rec.projectsDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(filepath.Join(rec.projectsDir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stems := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if fileutil.IsYAMLFile(entry.Name()) && !entry.IsDir() {
			stems[stemKey(entry.Name())] = struct{}{}
		}
	}
	return stems, nil
}

// stemKey returns the name of file without its directory and extension, as
// the file system compares it.
func stemKey(file string) string {
	stem := fileutil.TrimYAMLFileExtension(filepath.Base(file))
	if ignoresCase() {
		return strings.ToLower(stem)
	}
	return stem
}

// ignoresCase reports whether the file systems of this OS ignore case by
// default.
func ignoresCase() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
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
