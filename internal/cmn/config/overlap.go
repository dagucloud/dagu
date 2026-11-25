// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// With projects on, DAG roots and state never share a directory:
//
//	DAG roots    paths.dags_dir, paths.projects_dir, paths.alt_dags_dir
//	state roots  <data_dir>/projects, each path set apart, and the
//	             directory of each server-wide store
//
// A DAG root must not be or contain paths.data_dir, must not be, contain, or
// lie inside a state root, and must not be another DAG root. It may lie
// anywhere else in paths.data_dir.

// serverStoreDirNames are the directories that server-wide stores keep in
// paths.data_dir under fixed names. Settings in paths place the others.
var serverStoreDirNames = []string{
	"auth", "cloud", "coordinator", "distributed", "incidents", "license", "migration",
	"notifications", "run-binaries", "scheduler", "server", "tailscale", "upgrade",
}

// Relations between a DAG root and the path it overlaps.
const (
	relationIs       = "is"
	relationContains = "contains"
	relationInside   = "lies inside"
)

// CheckProjectPaths returns an error that names every overlap PathOverlaps
// reports while projects are on. It returns nil while projects are off.
func (c *Config) CheckProjectPaths() error {
	if !c.ProjectsEnabled {
		return nil
	}
	overlaps := c.PathOverlaps()
	if len(overlaps) == 0 {
		return nil
	}
	return errors.New("DAG directories must not share a directory with state or with each other:\n  " +
		strings.Join(overlaps, "\n  ") +
		"\nSet each DAG directory above to a separate directory outside state, including any left at its default.")
}

// PathOverlaps describes each DAG root that is or contains paths.data_dir,
// that is, contains, or lies inside a state root, or that is another DAG
// root. It reports them whether or not projects are on.
func (c *Config) PathOverlaps() []string {
	fs := newLookups()
	roots := locatePaths(c.dagRoots(), fs)
	states := locatePaths(c.stateRoots(), fs)
	var data *locatedPath
	if located := locatePaths([]namedPath{{"paths.data_dir", c.Paths.DataDir}}, fs); len(located) > 0 {
		data = &located[0]
	}

	var overlaps []string
	for i, root := range roots {
		holdsData := false
		if data != nil {
			if rel := root.dir.relation(data.dir); rel == relationIs || rel == relationContains {
				overlaps = append(overlaps, describeOverlap(root, rel, *data))
				holdsData = true
			}
		}
		for _, state := range states {
			// Holding paths.data_dir already accounts for each state root in it.
			if holdsData && data.dir.contains(state.dir) {
				continue
			}
			if rel := root.dir.relation(state.dir); rel != "" {
				overlaps = append(overlaps, describeOverlap(root, rel, state))
			}
		}
		for _, other := range roots[i+1:] {
			if root.dir.same(other.dir) {
				overlaps = append(overlaps, describeOverlap(root, relationIs, other))
			}
		}
	}
	return overlaps
}

func (c *Config) dagRoots() []namedPath {
	return []namedPath{
		{"paths.dags_dir", c.Paths.DAGsDir},
		{"paths.projects_dir", c.Paths.ProjectsDir},
		{"paths.alt_dags_dir", c.Paths.AltDAGsDir},
	}
}

func (c *Config) stateRoots() []namedPath {
	var roots []namedPath
	inData := func(name string) namedPath {
		return namedPath{"<data_dir>/" + name, filepath.Join(c.Paths.DataDir, name)}
	}
	if c.Paths.DataDir != "" {
		roots = append(roots, inData(projectStatesDirName))
	}
	explicit := c.Paths.Explicit
	for _, setApart := range []struct {
		set bool
		namedPath
	}{
		{explicit.LogDir, namedPath{"paths.log_dir", c.Paths.LogDir}},
		{explicit.ArtifactDir, namedPath{"paths.artifact_dir", c.Paths.ArtifactDir}},
		{explicit.DAGRunsDir, namedPath{"paths.dag_runs_dir", c.Paths.DAGRunsDir}},
		{explicit.DAGRunWorkDir, namedPath{"paths.dag_run_work_dir", c.Paths.DAGRunWorkDir}},
	} {
		if setApart.set {
			roots = append(roots, setApart.namedPath)
		}
	}
	roots = append(roots,
		namedPath{"paths.users_dir", c.Paths.UsersDir},
		namedPath{"paths.api_keys_dir", c.Paths.APIKeysDir},
		namedPath{"paths.service_registry_dir", c.Paths.ServiceRegistryDir},
		namedPath{"paths.remote_nodes_dir", c.Paths.RemoteNodesDir},
		namedPath{"paths.contexts_dir", c.Paths.ContextsDir},
		namedPath{"paths.calendars_dir", c.Paths.CalendarsDir},
		namedPath{"paths.admin_logs_dir", c.Paths.AdminLogsDir},
	)
	if c.Paths.DataDir != "" {
		for _, name := range serverStoreDirNames {
			roots = append(roots, inData(name))
		}
	}
	return roots
}

// describeOverlap leaves out the path of other when root spells it the same.
func describeOverlap(root locatedPath, relation string, other locatedPath) string {
	desc := fmt.Sprintf("%s %q %s %s", root.name, root.path, relation, other.name)
	if other.path != root.path {
		desc += fmt.Sprintf(" %q", other.path)
	}
	return desc
}

// locatePaths returns the location of each path that is set.
func locatePaths(paths []namedPath, fs *lookups) []locatedPath {
	located := make([]locatedPath, 0, len(paths))
	for _, p := range paths {
		if p.path != "" {
			located = append(located, locatedPath{namedPath: p, dir: locateDir(p.path, fs)})
		}
	}
	return located
}

// locatedPath is a configured directory and where it lies on disk.
type locatedPath struct {
	namedPath
	dir dirLocation
}

// namedPath is a configured directory and the setting an operator knows it
// by.
type namedPath struct {
	name string
	path string
}

// dirLocation places a directory that may not exist yet, so that two
// spellings of one directory compare equal whatever links, letter case, or
// Windows short names they use.
type dirLocation struct {
	// existing holds the deepest directory on the path that exists, with
	// links resolved, then each directory above it. It is empty when not
	// even the root can be looked up.
	existing []os.FileInfo
	// missing holds the names below existing[0] that do not exist yet. When
	// existing is empty, it holds the whole path, root first.
	missing []string
	// foldCase reports whether missing names compare without regard to case,
	// as on the file system that would hold them.
	foldCase bool
}

// maxLinkHops bounds the links to missing targets that one lookup follows,
// so a loop of links ends. It is as many as Linux follows; no directory can
// be created through a longer chain.
const maxLinkHops = 40

// locateDir returns the location of path. A link whose target does not
// exist yet places the names below it at that target. A directory that
// cannot be looked up for a reason other than its absence counts as missing,
// so the names from it down compare as spelled.
func locateDir(path string, fs *lookups) dirLocation {
	var missing []string
	current := filepath.Clean(path)
	for hops := 0; ; {
		if resolved := fs.resolve(current); resolved != "" {
			if existing := fs.ancestors(resolved); len(existing) > 0 {
				slices.Reverse(missing)
				loc := dirLocation{existing: existing, missing: missing}
				if len(missing) > 0 {
					loc.foldCase = fs.foldsCase(resolved, missing[0])
				}
				return loc
			}
		}
		if target, ok := fs.links[current]; ok && hops < maxLinkHops {
			hops++
			current = target
			continue
		}
		parent := filepath.Dir(current)
		if parent == current {
			missing = append(missing, current)
			slices.Reverse(missing)
			return dirLocation{missing: missing}
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// relation returns how d relates to other, or "" when neither holds the
// other.
func (d dirLocation) relation(other dirLocation) string {
	switch {
	case d.same(other):
		return relationIs
	case d.contains(other):
		return relationContains
	case other.contains(d):
		return relationInside
	default:
		return ""
	}
}

// same reports whether d and other are one directory.
func (d dirLocation) same(other dirLocation) bool {
	return len(d.missing) == len(other.missing) && d.sameAnchor(other) &&
		d.namesEqual(d.missing, other.missing)
}

// contains reports whether other lies below d.
func (d dirLocation) contains(other dirLocation) bool {
	if len(d.missing) > 0 {
		// Below a directory that does not exist, only directories that do not
		// exist either can lie, hanging from the same existing directory.
		return len(other.missing) > len(d.missing) && d.sameAnchor(other) &&
			d.namesEqual(d.missing, other.missing[:len(d.missing)])
	}
	above := other.existing
	if len(other.missing) == 0 {
		above = above[1:]
	}
	return slices.ContainsFunc(above, func(info os.FileInfo) bool {
		return os.SameFile(info, d.existing[0])
	})
}

func (d dirLocation) sameAnchor(other dirLocation) bool {
	if len(d.existing) == 0 || len(other.existing) == 0 {
		return len(d.existing) == len(other.existing)
	}
	return os.SameFile(d.existing[0], other.existing[0])
}

func (d dirLocation) namesEqual(a, b []string) bool {
	if d.foldCase {
		return slices.EqualFunc(a, b, strings.EqualFold)
	}
	return slices.Equal(a, b)
}

// lookups keeps what one check learns from the file system, since the
// configured paths share most of their ancestors.
type lookups struct {
	// resolved maps a path to the path with its links resolved, or to ""
	// when it cannot be resolved.
	resolved map[string]string
	// links maps a link that cannot be resolved to its absolute target.
	links map[string]string
	// infos maps a resolved path to its file info, or to nil when it cannot
	// be looked up.
	infos map[string]os.FileInfo
	// foldCase maps a resolved directory to whether names below it compare
	// without regard to case.
	foldCase map[string]bool
}

func newLookups() *lookups {
	return &lookups{
		resolved: map[string]string{},
		links:    map[string]string{},
		infos:    map[string]os.FileInfo{},
		foldCase: map[string]bool{},
	}
}

// resolve returns path with its links resolved, or "" when path does not
// exist or cannot be resolved. One Lstat rules out a missing path before
// links are walked. When path is a link that cannot be resolved, as one to
// a directory not created yet, it records the link's target.
func (fs *lookups) resolve(path string) string {
	if resolved, ok := fs.resolved[path]; ok {
		return resolved
	}
	var resolved string
	if info, err := os.Lstat(path); err == nil {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			resolved = real
		} else if info.Mode()&os.ModeSymlink != 0 {
			fs.recordLink(path)
		}
	}
	fs.resolved[path] = resolved
	return resolved
}

// recordLink records the target of the link at path. A relative target
// starts at the link's directory with its links resolved.
func (fs *lookups) recordLink(path string) {
	target, err := os.Readlink(path)
	if err != nil {
		return
	}
	if !filepath.IsAbs(target) {
		dir := filepath.Dir(path)
		if real := fs.resolve(dir); real != "" {
			dir = real
		}
		// Joined unclean, so a ".." in target is not applied before the
		// links ahead of it.
		target = dir + string(filepath.Separator) + target
	}
	fs.links[path] = resolveExistingPrefix(target)
}

// resolveExistingPrefix returns the absolute path with the links of its
// longest existing prefix resolved in order, so a ".." after a link leaves
// the link's target, as EvalSymlinks does for a path that exists. The names
// after that prefix join as spelled.
func resolveExistingPrefix(path string) string {
	sep := string(filepath.Separator)
	volume := filepath.VolumeName(path)
	names := strings.Split(strings.TrimPrefix(path[len(volume):], sep), sep)
	for n := len(names); n > 0; n-- {
		prefix := volume + sep + strings.Join(names[:n], sep)
		if real, err := filepath.EvalSymlinks(prefix); err == nil {
			return filepath.Join(append([]string{real}, names[n:]...)...)
		}
	}
	return filepath.Clean(path)
}

// foldsCase reports whether name, and every other name below dir, compares
// without regard to case.
func (fs *lookups) foldsCase(dir, name string) bool {
	fold, ok := fs.foldCase[dir]
	if !ok {
		fold = fileutil.IsCaseInsensitiveFS(filepath.Join(dir, name))
		fs.foldCase[dir] = fold
	}
	return fold
}

// ancestors returns resolved, a path without links, then each directory
// above it, up to the first that cannot be looked up.
func (fs *lookups) ancestors(resolved string) []os.FileInfo {
	var infos []os.FileInfo
	for current := resolved; ; {
		info, cached := fs.infos[current]
		if !cached {
			if stat, err := os.Stat(current); err == nil {
				info = stat
			}
			fs.infos[current] = info
		}
		if info == nil {
			return infos
		}
		infos = append(infos, info)
		parent := filepath.Dir(current)
		if parent == current {
			return infos
		}
		current = parent
	}
}
