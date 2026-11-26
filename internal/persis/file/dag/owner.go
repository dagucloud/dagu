// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/project"
)

// Owners tells which project or library a DAG file belongs to. Each call
// reads the registry and the records it needs, so a project created or a
// file moved since counts at once. It is safe for concurrent use.
type Owners struct {
	dirs     OwnerDirs
	registry project.Registry
	// inPlace is nil while no file runs in place.
	inPlace project.InPlaceRecords

	// rootsMu guards roots, the DAG roots with their links resolved. A
	// root not yet resolved in full is resolved again on each call.
	rootsMu sync.Mutex
	roots   []ownerRoot
}

// OwnerDirs places the DAG roots: paths.dags_dir, paths.projects_dir, and
// paths.alt_dags_dir.
type OwnerDirs struct {
	// DAGsDir is the default project's DAG directory.
	DAGsDir string
	// ProjectsDir holds the DAG directory of every other project.
	ProjectsDir string
	// Library is the library, or "" when there is none.
	Library string
}

// Owner is what a DAG file belongs to: one project, the library, or
// neither.
type Owner struct {
	// Project is the name of the project as the registry stores it. It is
	// empty for a library DAG and for a file that belongs to neither.
	Project string
	// Library reports a library DAG.
	Library bool
}

// NewOwners returns the Owners of the DAG roots in dirs. The registry names
// the projects in dirs.ProjectsDir; inPlace, which may be nil, records the
// files that run in place.
func NewOwners(dirs OwnerDirs, registry project.Registry, inPlace project.InPlaceRecords) *Owners {
	return &Owners{dirs: dirs, registry: registry, inPlace: inPlace}
}

// Is reports whether the file belongs to the project called name, given in
// any case.
func (o Owner) Is(name string) bool {
	return o.Project != "" && strings.EqualFold(o.Project, name)
}

// ownerRoot is a DAG root whose files belong to one owner, except for the
// files of a root nested in it.
type ownerRoot struct {
	dir  string
	kind ownerKind
	// final reports that every link of dir resolved, so dir stays valid.
	final bool
}

type ownerKind int

const (
	ownedByDefault ownerKind = iota
	ownedByProjects
	ownedByLibrary
)

// Of returns what the DAG file at path belongs to, once symbolic links
// resolve. A record of a file that runs in place wins. Otherwise the
// innermost DAG root that holds the file decides, and a directory in
// paths.projects_dir is a project's only while the registry holds that
// project.
func (o *Owners) Of(ctx context.Context, path string) (Owner, error) {
	real, err := resolveExisting(path)
	if err != nil {
		return Owner{}, err
	}
	if o.inPlace != nil {
		name, err := o.inPlace.InPlaceOwner(ctx, real)
		if err != nil {
			return Owner{}, err
		}
		if name != "" {
			return Owner{Project: name}, nil
		}
	}

	roots, err := o.resolvedRoots()
	if err != nil {
		return Owner{}, err
	}
	var (
		inner ownerRoot
		rel   string
	)
	for _, root := range roots {
		r, err := filepath.Rel(root.dir, real)
		if err != nil || !pathWithinRoot(real, root.dir) || len(root.dir) < len(inner.dir) {
			continue
		}
		inner, rel = root, r
	}

	switch {
	case inner.dir == "":
		return Owner{}, nil
	case inner.kind == ownedByLibrary:
		return Owner{Library: true}, nil
	case inner.kind == ownedByDefault:
		return Owner{Project: project.DefaultName}, nil
	default:
		return o.projectIn(ctx, inner.dir, rel)
	}
}

// OfIn is Of for a lookup by the project called caller. A file that Of
// places nowhere is the caller's when it lies in the caller's DAG directory
// once that directory's links resolve, as when the directory is a symbolic
// link to a checkout.
func (o *Owners) OfIn(ctx context.Context, caller, path string) (Owner, error) {
	owner, err := o.Of(ctx, path)
	if err != nil || owner != (Owner{}) {
		return owner, err
	}
	dir := o.dirs.DAGsDir
	if !strings.EqualFold(caller, project.DefaultName) {
		if o.dirs.ProjectsDir == "" {
			return Owner{}, nil
		}
		dir = filepath.Join(o.dirs.ProjectsDir, caller)
	}
	if dir == "" {
		return Owner{}, nil
	}
	realDir, err := resolveExisting(dir)
	if err != nil {
		return Owner{}, err
	}
	real, err := resolveExisting(path)
	if err != nil {
		return Owner{}, err
	}
	if pathWithinRoot(real, realDir) {
		return Owner{Project: caller}, nil
	}
	return Owner{}, nil
}

// resolvedRoots returns the configured DAG roots with their links resolved.
// A root whose links all resolve is kept, since the paths in effect do not
// change while a process runs. One that does not yet exist in full, such as
// a link to a directory not yet made, is resolved again on the next call.
func (o *Owners) resolvedRoots() ([]ownerRoot, error) {
	o.rootsMu.Lock()
	defer o.rootsMu.Unlock()
	if o.roots == nil {
		for _, root := range []ownerRoot{
			{dir: o.dirs.DAGsDir, kind: ownedByDefault},
			{dir: o.dirs.ProjectsDir, kind: ownedByProjects},
			{dir: o.dirs.Library, kind: ownedByLibrary},
		} {
			if root.dir != "" {
				o.roots = append(o.roots, root)
			}
		}
	}
	resolved := make([]ownerRoot, len(o.roots))
	for i, root := range o.roots {
		if !root.final {
			dir, err := resolveExisting(o.configuredDir(root.kind))
			if err != nil {
				return nil, err
			}
			_, statErr := os.Stat(dir)
			o.roots[i] = ownerRoot{dir: dir, kind: root.kind, final: statErr == nil}
		}
		resolved[i] = o.roots[i]
	}
	return resolved, nil
}

// configuredDir returns the configured directory of the root of kind.
func (o *Owners) configuredDir(kind ownerKind) string {
	switch kind {
	case ownedByLibrary:
		return o.dirs.Library
	case ownedByProjects:
		return o.dirs.ProjectsDir
	case ownedByDefault:
		return o.dirs.DAGsDir
	}
	return ""
}

// projectIn returns the registered project whose DAG directory, in
// projectsDir, holds the file at rel below projectsDir.
func (o *Owners) projectIn(ctx context.Context, projectsDir, rel string) (Owner, error) {
	dirName, rest, found := strings.Cut(rel, string(filepath.Separator))
	// The default project's DAG directory is paths.dags_dir, so no project
	// owns a directory of its name in paths.projects_dir.
	if !found || rest == "" || o.registry == nil || strings.EqualFold(dirName, project.DefaultName) {
		return Owner{}, nil
	}
	p, err := o.registry.Get(ctx, dirName)
	switch {
	case errors.Is(err, project.ErrNotFound):
		return Owner{}, nil
	case err != nil:
		return Owner{}, err
	}
	// A name that matches only without regard to case is the project's
	// directory only where the file system ignores case.
	if p.Name != dirName && !sameFile(filepath.Join(projectsDir, dirName), filepath.Join(projectsDir, p.Name)) {
		return Owner{}, nil
	}
	return Owner{Project: p.Name}, nil
}

// resolveExisting returns path, absolute, with the symbolic links of its
// longest existing prefix resolved, so a file not yet written still compares
// against directories that exist.
func resolveExisting(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	missing := ""
	for current := abs; ; {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(resolved, missing), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs, nil
		}
		missing = filepath.Join(filepath.Base(current), missing)
		current = parent
	}
}

func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}
