// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// projectScope is the project whose lookups a store resolves.
type projectScope struct {
	name   string
	owners *Owners
}

// projectLookupError reports a lookup target that is not the project's and
// not in the library. It is persis.ErrDAGNotFound and os.ErrNotExist, so
// callers treat it as any DAG they cannot find.
type projectLookupError struct {
	msg string
}

func (e *projectLookupError) Error() string { return e.msg }

func (e *projectLookupError) Is(target error) bool {
	return target == persis.ErrDAGNotFound || target == os.ErrNotExist
}

// locateInProject resolves target as the store's project does. Each match
// is checked against its owner once its links resolve, so a match that
// another project owns is passed over and named if nothing else matches.
func (store *Store) locateInProject(ctx context.Context, target string) (ResolvedFile, error) {
	scope := store.project
	library := scope.owners.dirs.Library
	var (
		other   Owner
		linkOut bool
	)
	find := func(dir string) (ResolvedFile, bool, error) {
		resolved, err := store.locateInDirs(ctx, target, []string{dir})
		switch {
		case err == nil:
			return resolved, true, nil
		case errors.Is(err, ErrExternalSymlinkDisabled):
			linkOut = true
			return ResolvedFile{}, false, nil
		case errors.Is(err, os.ErrNotExist):
			return ResolvedFile{}, false, nil
		default:
			return ResolvedFile{}, false, err
		}
	}
	// The project's base config lies in its DAG directory but is no DAG.
	// Only a match of the same file name is compared by identity, which
	// also holds where the file system ignores case.
	isBaseConfig := func(resolved ResolvedFile) bool {
		return store.baseConfigPath != "" &&
			strings.EqualFold(filepath.Base(resolved.ResolvedPath), filepath.Base(store.baseConfigPath)) &&
			sameFile(resolved.ResolvedPath, store.baseConfigPath)
	}
	accept := func(resolved ResolvedFile) (bool, error) {
		if isBaseConfig(resolved) {
			return false, nil
		}
		owner, err := scope.owners.OfIn(ctx, scope.name, resolved.ResolvedPath)
		if err != nil {
			return false, err
		}
		if owner.Library || owner.Is(scope.name) {
			return true, nil
		}
		if other.Project == "" {
			other = owner
		}
		return false, nil
	}

	for _, dir := range store.searchPaths {
		if library != "" && filepath.Clean(dir) == filepath.Clean(library) {
			continue
		}
		resolved, ok, err := find(dir)
		if err != nil {
			return ResolvedFile{}, err
		}
		if !ok {
			continue
		}
		if accepted, err := accept(resolved); err != nil {
			return ResolvedFile{}, err
		} else if accepted {
			return resolved, nil
		}
	}

	if !isExplicitPath(target) {
		resolved, ok, err := store.locateInPlace(ctx, target)
		if err != nil || ok {
			return resolved, err
		}
	}

	if library != "" {
		resolved, ok, err := find(library)
		if err != nil {
			return ResolvedFile{}, err
		}
		if ok {
			if accepted, err := accept(resolved); err != nil {
				return ResolvedFile{}, err
			} else if accepted {
				return resolved, nil
			}
		}
	}

	if isExplicitPath(target) {
		// A path may also reach the project's file or a library DAG by a
		// symbolic link, or a file that runs in place as the project's.
		file, exists := store.targetFile(target)
		owner, err := scope.owners.OfIn(ctx, scope.name, file)
		if err != nil {
			return ResolvedFile{}, err
		}
		if exists && (owner.Library || owner.Is(scope.name)) {
			resolved, ok, err := resolveTargetFile(file)
			switch {
			case err != nil:
				return ResolvedFile{}, err
			case ok && isBaseConfig(resolved):
				return ResolvedFile{}, &projectLookupError{
					msg: fmt.Sprintf("%s is the base config of project %q, not a DAG", target, scope.name),
				}
			case ok:
				return resolved, nil
			}
		}
		if other.Project == "" {
			other = owner
		}
	}
	return ResolvedFile{}, scope.lookupError(target, other, linkOut)
}

// locateInPlace resolves the name target among the files that run in place
// as the store's project.
func (store *Store) locateInPlace(ctx context.Context, target string) (ResolvedFile, bool, error) {
	scope := store.project
	if scope.owners.inPlace == nil {
		return ResolvedFile{}, false, nil
	}
	files, err := scope.owners.inPlace.InPlaceFiles(ctx, scope.name)
	if err != nil {
		return ResolvedFile{}, false, err
	}
	name := fileutil.TrimYAMLFileExtension(target)
	for _, file := range files {
		if fileutil.TrimYAMLFileExtension(filepath.Base(file)) != name {
			continue
		}
		if resolved, ok, err := resolveTargetFile(file); err != nil || ok {
			return resolved, ok, err
		}
	}
	return ResolvedFile{}, false, nil
}

// targetFile returns the file a path target names, relative to the base
// directory: the first DAG file among its candidates that exists, and true,
// or the path itself and false when none does.
func (store *Store) targetFile(target string) (string, bool) {
	file := filepath.FromSlash(target)
	if !filepath.IsAbs(file) {
		file = filepath.Join(store.baseDir, file)
	}
	for _, candidate := range dagFileCandidates(file) {
		if fileExists(candidate) {
			return candidate, true
		}
	}
	return file, false
}

// resolveTargetFile resolves a file that a lookup reaches outside the
// directories it searches. A file that is absent or not a regular file once
// its links resolve is not found, as a record of a file that runs in place
// stays while its file is absent.
func resolveTargetFile(file string) (ResolvedFile, bool, error) {
	entry, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return ResolvedFile{}, false, nil
	}
	if err != nil {
		return ResolvedFile{}, false, err
	}
	resolved, err := resolveExisting(file)
	if err != nil {
		return ResolvedFile{}, false, err
	}
	info, err := os.Stat(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return ResolvedFile{}, false, nil
	}
	if err != nil {
		return ResolvedFile{}, false, err
	}
	if !info.Mode().IsRegular() {
		return ResolvedFile{}, false, nil
	}
	// A link is reached from outside the directory its target lies in, so
	// writes through it are refused as through any external link.
	symlink := entry.Mode()&os.ModeSymlink != 0
	return ResolvedFile{
		EntryPath:       file,
		ResolvedPath:    resolved,
		Symlink:         symlink,
		ExternalSymlink: symlink,
	}, true, nil
}

// lookupError names the store's project, and the project that owns the
// target when one does, else the library. linkOut reports a match passed
// over because it links out of its DAG directory.
func (scope *projectScope) lookupError(target string, other Owner, linkOut bool) error {
	var msg string
	switch {
	case other.Project != "":
		msg = fmt.Sprintf("DAG %s belongs to project %q; project %q runs only its own DAGs and library DAGs",
			target, other.Project, scope.name)
	case scope.owners.dirs.Library == "":
		msg = fmt.Sprintf("DAG %s is not in project %q", target, scope.name)
	default:
		msg = fmt.Sprintf("DAG %s is in neither project %q nor the library", target, scope.name)
	}
	if linkOut {
		msg += "; a project never follows a symbolic link out of its DAG directory"
	}
	return &projectLookupError{msg: msg}
}
