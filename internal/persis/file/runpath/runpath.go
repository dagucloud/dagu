// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package runpath converts the log, step output, and artifact paths of run
// records between the absolute paths callers use and the paths a record
// stores.
//
// A stored path takes one of three forms:
//
//	./etl/dag-run_.../dag-run_....log   relative to the project's log or
//	                                    artifact directory; written only
//	                                    while projects are on
//	/srv/dagu/logs/etl/...              absolute; read under the project's
//	                                    directory when it lies under a v2
//	                                    directory
//	logs/etl/...                        relative to the working directory,
//	                                    as a DAG set it; read as stored
package runpath

import (
	"path/filepath"
	"strings"
)

// relativePrefix starts a stored path that is relative to its project's
// directory. A path built by joining directories never starts with it,
// since joining cleans it away, so it never marks a path a DAG set.
const relativePrefix = "./"

// Mapper converts the paths of one project's run records. The zero Mapper
// stores and reads every path unchanged.
type Mapper struct {
	logs      root
	artifacts root
	relative  bool
}

// Dirs names a log directory and an artifact directory.
type Dirs struct {
	Log      string
	Artifact string
}

// root is a project's directory and the v2 directory it replaces.
type root struct {
	dir string
	// legacy is the v2 directory whose absolute paths read as paths under
	// dir. It is empty when there is none, or when it is dir.
	legacy string
}

// New returns the Mapper of the project whose directories are project. An
// absolute path under one of legacy, the v2 directories of the project's
// runs, reads as the same path under the project's directory. When relative
// is set, a path under the project's directory is stored relative to it;
// otherwise every path is stored as given.
func New(project, legacy Dirs, relative bool) Mapper {
	return Mapper{
		logs:      newRoot(project.Log, legacy.Log),
		artifacts: newRoot(project.Artifact, legacy.Artifact),
		relative:  relative,
	}
}

func newRoot(dir, legacy string) root {
	if dir == "" {
		return root{}
	}
	dir = filepath.Clean(dir)
	if legacy != "" {
		legacy = filepath.Clean(legacy)
	}
	if legacy == dir {
		legacy = ""
	}
	return root{dir: dir, legacy: legacy}
}

// StoresRelative reports whether the Mapper stores any path in a form other
// than the one given.
func (m Mapper) StoresRelative() bool {
	return m.relative
}

// StoreLog returns the stored form of a log or step output path.
func (m Mapper) StoreLog(path string) string {
	return m.logs.store(path, m.relative)
}

// ResolveLog returns the path that a stored log or step output path names.
func (m Mapper) ResolveLog(path string) string {
	return m.logs.resolve(path)
}

// StoreArtifact returns the stored form of an artifact directory.
func (m Mapper) StoreArtifact(path string) string {
	return m.artifacts.store(path, m.relative)
}

// ResolveArtifact returns the path that a stored artifact directory names.
func (m Mapper) ResolveArtifact(path string) string {
	return m.artifacts.resolve(path)
}

func (r root) store(path string, relative bool) string {
	if !relative {
		return path
	}
	rel, ok := within(r.dir, path)
	if !ok {
		return path
	}
	return relativePrefix + filepath.ToSlash(rel)
}

func (r root) resolve(path string) string {
	if rel, ok := strings.CutPrefix(path, relativePrefix); ok {
		if r.dir == "" {
			return path
		}
		return filepath.Join(r.dir, filepath.FromSlash(rel))
	}
	if r.legacy == "" {
		return path
	}
	// A project directory may lie inside its v2 directory, as
	// <log_dir>/.projects/<name> does in <log_dir>.
	if _, ok := within(r.dir, path); ok {
		return path
	}
	if rel, ok := within(r.legacy, path); ok {
		return filepath.Join(r.dir, rel)
	}
	return path
}

// within returns path relative to dir when path is absolute and lies inside
// dir. It compares names the way the platform does, so Windows ignores case
// and refuses a path on another volume.
func within(dir, path string) (string, bool) {
	if dir == "" || !filepath.IsAbs(path) {
		return "", false
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}
