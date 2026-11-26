// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"path/filepath"
	"strings"
)

// DAGPathOwner returns the name of the project whose DAG directory holds
// path, a file or a directory, among the default project and the projects
// named in names. It reports library instead when the library holds path.
// Where DAG roots nest, the innermost one holds path, and a directory under
// paths.projects_dir that no project in names uses holds it for none. Both
// results are zero when no DAG root holds path. Paths compare by identity,
// whatever the case or links of their spelling.
func (c *Config) DAGPathOwner(path string, names []string) (owner string, library bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	fs := newLookups()
	target := locateDir(abs, fs)
	depth := -1
	consider := func(dir, name string, isLibrary bool) bool {
		if dir == "" {
			return false
		}
		root := locateDir(dir, fs)
		if !root.same(target) && !root.contains(target) {
			return false
		}
		if d := root.depth(); d > depth {
			depth, owner, library = d, name, isLibrary
		}
		return true
	}

	consider(c.Paths.DAGsDir, defaultProjectName, false)
	consider(c.Paths.AltDAGsDir, "", true)
	// Only a path under paths.projects_dir can lie in a project other than
	// the default one.
	if consider(c.Paths.ProjectsDir, "", false) {
		for _, name := range names {
			if isPathElement(name) && !strings.EqualFold(name, defaultProjectName) {
				consider(filepath.Join(c.Paths.ProjectsDir, name), name, false)
			}
		}
	}
	return owner, library
}

// depth counts the directories from the root of the file system down to d.
func (d dirLocation) depth() int {
	return len(d.existing) + len(d.missing)
}
