// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// maxSideDirSearchDepth bounds the search for a side directory, such as the
// skills directory, above the directory of a DAG file.
const maxSideDirSearchDepth = 8

// sideDirs searches the directory dirName next to the DAG being built. The
// search starts in the directory of the DAG file and goes up. It stops at the
// DAGs directory when the file is inside it. The directory of the DAGs
// directory is the fallback, which serves a DAG that has no file. It returns
// the candidates that it looked at, in order, and the first one that exists.
// found is empty when none exists.
func sideDirs(ctx buildContext, dirName string) (searched []string, found string) {
	dagsDir := filepath.Clean(ctx.opts.DAGsDir)
	if ctx.opts.DAGsDir == "" {
		dagsDir = ""
	}
	file := ctx.opts.SourceFile
	if file == "" {
		file = ctx.file
	}
	if file != "" {
		if abs, err := filepath.Abs(file); err == nil {
			dir := filepath.Dir(abs)
			for range maxSideDirSearchDepth {
				candidate := filepath.Join(dir, dirName)
				searched = append(searched, candidate)
				if isDir(candidate) {
					return searched, candidate
				}
				parent := filepath.Dir(dir)
				if dir == dagsDir || parent == dir {
					break
				}
				dir = parent
			}
		}
	}
	if dagsDir != "" {
		candidate := filepath.Join(dagsDir, dirName)
		if !slices.Contains(searched, candidate) {
			searched = append(searched, candidate)
		}
		if isDir(candidate) {
			return searched, candidate
		}
	}
	return searched, ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// sideFileCollector records the side files (the files of the skills) that the
// build of one file reads, the version of each skill and the warnings of the
// skills. It is shared by every document of the file.
type sideFileCollector struct {
	paths    []string
	refs     []ir.SkillRef
	warnings []string
}

// addSkill records the version and the warnings of a skill once.
func (c *sideFileCollector) addSkill(ref ir.SkillRef, warnings []string) {
	if c == nil {
		return
	}
	for _, r := range c.refs {
		if r.Name == ref.Name && r.Dir == ref.Dir {
			return
		}
	}
	c.refs = append(c.refs, ref)
	c.warnings = append(c.warnings, warnings...)
}

// skillRefs returns the recorded skill versions, sorted by name.
func (c *sideFileCollector) skillRefs() []ir.SkillRef {
	if c == nil || len(c.refs) == 0 {
		return nil
	}
	out := slices.Clone(c.refs)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// warningList returns the recorded warnings.
func (c *sideFileCollector) warningList() []string {
	if c == nil {
		return nil
	}
	return slices.Clone(c.warnings)
}

// add records the absolute path of a side file once. A nil collector ignores it.
func (c *sideFileCollector) add(path string) {
	if c == nil {
		return
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if !slices.Contains(c.paths, path) {
		c.paths = append(c.paths, path)
	}
}

// list returns the recorded paths, sorted.
func (c *sideFileCollector) list() []string {
	if c == nil || len(c.paths) == 0 {
		return nil
	}
	out := slices.Clone(c.paths)
	sort.Strings(out)
	return out
}

// searchedList formats the directories that a search looked at, for an error.
func searchedList(dirs []string) string {
	if len(dirs) == 0 {
		return "no directory (the DAG has no file and no DAGs directory)"
	}
	return strings.Join(dirs, ", ")
}
