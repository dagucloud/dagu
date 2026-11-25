// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runpath_test

import (
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/persis/file/runpath"
	"github.com/stretchr/testify/assert"
)

// layout is a v2 install and the directories of its default project after
// projects are on.
type layout struct {
	project runpath.Dirs
	legacy  runpath.Dirs
}

func newLayout(t *testing.T) layout {
	t.Helper()
	home := t.TempDir()
	state := filepath.Join(home, "data", "projects", "default")
	return layout{
		project: runpath.Dirs{Log: filepath.Join(state, "logs"), Artifact: filepath.Join(state, "artifacts")},
		legacy:  runpath.Dirs{Log: filepath.Join(home, "logs"), Artifact: filepath.Join(home, "data", "artifacts")},
	}
}

func TestStoreRelative(t *testing.T) {
	l := newLayout(t)
	m := runpath.New(l.project, l.legacy, true)

	logFile := filepath.Join(l.project.Log, "etl", "dag-run_1", "dag-run_1.log")
	artifactDir := filepath.Join(l.project.Artifact, "2026", "11", "25", "120000_etl_0123456789abcdef")

	assert.Equal(t, "./etl/dag-run_1/dag-run_1.log", m.StoreLog(logFile))
	assert.Equal(t, "./2026/11/25/120000_etl_0123456789abcdef", m.StoreArtifact(artifactDir))
	assert.Equal(t, logFile, m.ResolveLog(m.StoreLog(logFile)))
	assert.Equal(t, artifactDir, m.ResolveArtifact(m.StoreArtifact(artifactDir)))
	assert.True(t, m.StoresRelative())
}

// With projects off, a record stores every path as given, as v2 does.
func TestStoreAsGiven(t *testing.T) {
	l := newLayout(t)
	m := runpath.New(l.project, l.project, false)

	logFile := filepath.Join(l.project.Log, "etl", "dag-run_1", "dag-run_1.log")
	assert.Equal(t, logFile, m.StoreLog(logFile))
	assert.Equal(t, logFile, m.ResolveLog(logFile))
	assert.False(t, m.StoresRelative())
}

func TestResolveRelativeWithProjectsOff(t *testing.T) {
	l := newLayout(t)
	m := runpath.New(l.project, l.project, false)

	assert.Equal(t,
		filepath.Join(l.project.Log, "etl", "dag-run_1", "dag-run_1.log"),
		m.ResolveLog("./etl/dag-run_1/dag-run_1.log"))
}

func TestResolveLegacy(t *testing.T) {
	l := newLayout(t)
	m := runpath.New(l.project, l.legacy, true)

	assert.Equal(t,
		filepath.Join(l.project.Log, "etl", "dag-run_1", "step.out"),
		m.ResolveLog(filepath.Join(l.legacy.Log, "etl", "dag-run_1", "step.out")))
	assert.Equal(t,
		filepath.Join(l.project.Artifact, "2026", "11", "25", "run"),
		m.ResolveArtifact(filepath.Join(l.legacy.Artifact, "2026", "11", "25", "run")))
}

// A path a DAG set outside the project's directories is stored and read as
// the DAG set it.
func TestKeepPathsOutside(t *testing.T) {
	l := newLayout(t)
	m := runpath.New(l.project, l.legacy, true)
	elsewhere := filepath.Join(t.TempDir(), "custom", "etl", "dag-run_1.log")
	// A sibling whose name extends the log directory's is outside it.
	sibling := l.project.Log + "-old" + string(filepath.Separator) + "x.log"

	for _, path := range []string{
		elsewhere,
		sibling,
		filepath.Join("logs", "etl", "dag-run_1.log"),
		"",
	} {
		assert.Equal(t, path, m.StoreLog(path))
		assert.Equal(t, path, m.ResolveLog(path))
	}
}

// When paths.log_dir is set, the project's log directory lies inside the v2
// one, and a path already under it stays where it is.
func TestResolveSetApart(t *testing.T) {
	logRoot := t.TempDir()
	project := runpath.Dirs{Log: filepath.Join(logRoot, ".projects", "weather")}
	m := runpath.New(project, runpath.Dirs{Log: logRoot}, true)

	inProject := filepath.Join(project.Log, "etl", "dag-run_1.log")
	assert.Equal(t, inProject, m.ResolveLog(inProject))
	assert.Equal(t, inProject, m.ResolveLog(filepath.Join(logRoot, "etl", "dag-run_1.log")))
}

func TestZeroMapper(t *testing.T) {
	var m runpath.Mapper
	assert.Equal(t, "./etl/x.log", m.ResolveLog("./etl/x.log"))
	path := filepath.Join(t.TempDir(), "x.log")
	assert.Equal(t, path, m.StoreLog(path))
	assert.Equal(t, path, m.ResolveLog(path))
}
