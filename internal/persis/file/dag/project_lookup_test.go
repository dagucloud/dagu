// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const projectDAG = "steps:\n  - run: echo ok\n"

// projectLayout is an install with projects on: the default project's DAG
// directory, two registered projects, and the library.
type projectLayout struct {
	dags     string
	projects string
	library  string
	inPlace  *inPlaceFixture
	owners   *Owners
}

// inPlaceFixture stands for the registry's records of the files that run in
// place, by project name.
type inPlaceFixture struct {
	files map[string][]string
}

func (f *inPlaceFixture) InPlaceOwner(_ context.Context, path string) (string, error) {
	for name, files := range f.files {
		for _, file := range files {
			if resolved, err := resolveExisting(file); err == nil && resolved == path {
				return name, nil
			}
		}
	}
	return "", nil
}

func (f *inPlaceFixture) InPlaceFiles(_ context.Context, name string) ([]string, error) {
	return f.files[name], nil
}

func newProjectLayout(t *testing.T) *projectLayout {
	t.Helper()
	root := t.TempDir()
	l := &projectLayout{
		dags:     filepath.Join(root, "dags"),
		projects: filepath.Join(root, "projects"),
		library:  filepath.Join(root, "library"),
		inPlace:  &inPlaceFixture{files: map[string][]string{}},
	}
	registry := projectregistry.New(filepath.Join(root, "data"))
	for _, name := range []string{"weather", "day-ahead"} {
		_, err := registry.Create(t.Context(), project.CreateInput{Name: name})
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Join(l.projects, name), 0o750))
	}
	require.NoError(t, os.MkdirAll(l.dags, 0o750))
	require.NoError(t, os.MkdirAll(l.library, 0o750))
	l.owners = NewOwners(OwnerDirs{DAGsDir: l.dags, ProjectsDir: l.projects, Library: l.library}, registry, l.inPlace)
	return l
}

// dir returns the DAG directory of the project called name.
func (l *projectLayout) dir(name string) string {
	if name == project.DefaultName {
		return l.dags
	}
	return filepath.Join(l.projects, name)
}

// store opens the DAG store of the project called name, as the project's
// stores open it.
func (l *projectLayout) store(name string, opts ...Option) *Store {
	return NewStore(l.dir(name), append([]Option{
		WithSearchPaths([]string{l.library}),
		WithSkipRoots([]string{l.dags, l.projects, l.library}),
		WithSkipExamples(true),
		WithProject(name, l.owners),
	}, opts...)...)
}

func writeDAG(t *testing.T, path string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(projectDAG), 0o600))
	return path
}

func sourcePath(t *testing.T, store *Store, target string) string {
	t.Helper()
	def, err := store.Get(t.Context(), target)
	require.NoError(t, err)
	want, err := resolveExisting(def.SourcePath)
	require.NoError(t, err)
	return want
}

func sameAs(t *testing.T, path string) string {
	t.Helper()
	resolved, err := resolveExisting(path)
	require.NoError(t, err)
	return resolved
}

// A name resolves in the caller's project, then in the library, so a
// project DAG shadows a library DAG of the same name for that project only.
func TestProjectLookupProjectThenLibrary(t *testing.T) {
	l := newProjectLayout(t)
	own := writeDAG(t, filepath.Join(l.dir("weather"), "ABC.yaml"))
	shared := writeDAG(t, filepath.Join(l.library, "ABC.yaml"))
	onlyShared := writeDAG(t, filepath.Join(l.library, "cleanup.yaml"))

	for _, recursive := range []bool{false, true} {
		weather := l.store("weather", WithRecursiveDiscovery(recursive))
		assert.Equal(t, sameAs(t, own), sourcePath(t, weather, "ABC"))
		assert.Equal(t, sameAs(t, onlyShared), sourcePath(t, weather, "cleanup"))
		assert.Equal(t, sameAs(t, shared), sourcePath(t, l.store("day-ahead", WithRecursiveDiscovery(recursive)), "ABC"))
	}
	// A path into the library reaches the library DAG even where a project
	// DAG has its name.
	assert.Equal(t, sameAs(t, shared), sourcePath(t, l.store("weather"), shared))
}

// A target in neither the caller's project nor the library is a load error
// that names both; one in another project names that project instead.
func TestProjectLookupRefusesOtherProjects(t *testing.T) {
	l := newProjectLayout(t)
	forecast := writeDAG(t, filepath.Join(l.dir("weather"), "forecast.yaml"))
	dayAhead := l.store("day-ahead")

	_, err := dayAhead.Get(t.Context(), "forecast")
	require.ErrorIs(t, err, persis.ErrDAGNotFound)
	assert.EqualError(t, err, `DAG forecast is in neither project "day-ahead" nor the library`)

	for _, target := range []string{forecast, "../weather/forecast.yaml"} {
		_, err = dayAhead.Get(t.Context(), target)
		require.ErrorIs(t, err, persis.ErrDAGNotFound)
		assert.ErrorContains(t, err, `belongs to project "weather"; project "day-ahead" runs only its own DAGs and library DAGs`)
	}

	outside := writeDAG(t, filepath.Join(t.TempDir(), "outside.yaml"))
	_, err = dayAhead.Get(t.Context(), outside)
	assert.ErrorContains(t, err, `is in neither project "day-ahead" nor the library`)
}

// The default project's DAG directory may hold the other DAG roots. Files
// there belong to the innermost root, so the default project does not reach
// a project's DAG by path.
func TestProjectLookupNestedRoots(t *testing.T) {
	root := t.TempDir()
	dags := filepath.Join(root, "dags")
	l := &projectLayout{
		dags:     dags,
		projects: filepath.Join(dags, "projects"),
		library:  filepath.Join(dags, "library"),
		inPlace:  &inPlaceFixture{files: map[string][]string{}},
	}
	registry := projectregistry.New(filepath.Join(root, "data"))
	_, err := registry.Create(t.Context(), project.CreateInput{Name: "weather"})
	require.NoError(t, err)
	l.owners = NewOwners(OwnerDirs{DAGsDir: l.dags, ProjectsDir: l.projects, Library: l.library}, registry, nil)
	forecast := writeDAG(t, filepath.Join(l.projects, "weather", "forecast.yaml"))
	shared := writeDAG(t, filepath.Join(l.library, "ABC.yaml"))

	store := l.store(project.DefaultName)
	_, err = store.Get(t.Context(), forecast)
	assert.ErrorContains(t, err, `belongs to project "weather"; project "default"`)
	assert.Equal(t, sameAs(t, shared), sourcePath(t, store, "ABC"))
}

// With projects on, no lookup follows a symbolic link out of the project's
// DAG directory, whatever dag_discovery.symlinks says. A path target is
// checked once its links resolve.
func TestProjectLookupSymlinks(t *testing.T) {
	l := newProjectLayout(t)
	forecast := writeDAG(t, filepath.Join(l.dir("weather"), "forecast.yaml"))
	shared := writeDAG(t, filepath.Join(l.library, "ABC.yaml"))
	dir := l.dir("day-ahead")
	symlinkOrSkip(t, forecast, filepath.Join(dir, "forecast.yaml"))
	symlinkOrSkip(t, shared, filepath.Join(dir, "shared.yaml"))

	for _, recursive := range []bool{false, true} {
		store := l.store("day-ahead", WithSymlinks(true), WithRecursiveDiscovery(recursive))

		catalog, err := store.Catalog(t.Context())
		require.NoError(t, err)
		assert.Empty(t, catalog.Items)
		assert.Len(t, catalog.Issues, 2)

		_, err = store.Get(t.Context(), "forecast")
		require.ErrorIs(t, err, persis.ErrDAGNotFound)
		_, err = store.Get(t.Context(), "shared")
		require.ErrorIs(t, err, persis.ErrDAGNotFound)
	}

	store := l.store("day-ahead", WithSymlinks(true))
	_, err := store.Get(t.Context(), filepath.Join(dir, "forecast.yaml"))
	assert.ErrorContains(t, err, `belongs to project "weather"`)
	assert.Equal(t, sameAs(t, shared), sourcePath(t, store, filepath.Join(dir, "shared.yaml")))
}

// A file that runs in place resolves as its project's, by name and by path,
// and the project's own file of that name wins. No other project reaches it,
// although it lies in the default project's DAG directory.
func TestProjectLookupInPlaceFiles(t *testing.T) {
	l := newProjectLayout(t)
	invoices := writeDAG(t, filepath.Join(l.dags, "invoices.yaml"))
	reports := writeDAG(t, filepath.Join(l.dags, "reports.yaml"))
	l.inPlace.files["weather"] = []string{invoices, reports}
	ownReports := writeDAG(t, filepath.Join(l.dir("weather"), "reports.yaml"))
	weather := l.store("weather")

	assert.Equal(t, sameAs(t, invoices), sourcePath(t, weather, "invoices"))
	assert.Equal(t, sameAs(t, invoices), sourcePath(t, weather, invoices))
	assert.Equal(t, sameAs(t, ownReports), sourcePath(t, weather, "reports"))

	for _, target := range []string{"invoices", invoices} {
		_, err := l.store(project.DefaultName).Get(t.Context(), target)
		assert.ErrorContains(t, err, `belongs to project "weather"; project "default"`)
	}

	// A record stays while its file is absent; the DAG is then not found.
	require.NoError(t, os.Remove(invoices))
	_, err := weather.Get(t.Context(), "invoices")
	assert.ErrorIs(t, err, persis.ErrDAGNotFound)
}

func TestOwnersOf(t *testing.T) {
	l := newProjectLayout(t)
	unregistered := filepath.Join(l.projects, "scripts", "x.yaml")
	for path, want := range map[string]Owner{
		filepath.Join(l.dags, "a.yaml"):               {Project: project.DefaultName},
		filepath.Join(l.dir("weather"), "a.yaml"):     {Project: "weather"},
		filepath.Join(l.dir("weather"), "b", "a.yml"): {Project: "weather"},
		filepath.Join(l.library, "a.yaml"):            {Library: true},
		unregistered:                                  {},
		filepath.Join(l.projects, "a.yaml"):           {},
		filepath.Join(t.TempDir(), "a.yaml"):          {},
	} {
		got, err := l.owners.Of(t.Context(), path)
		require.NoError(t, err)
		assert.Equal(t, want, got, path)
	}
}

// A root that links to a directory not yet made counts once the directory
// exists, without a restart.
func TestOwnersOfRootMadeLater(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "shared")
	library := filepath.Join(root, "library")
	symlinkOrSkip(t, target, library)
	owners := NewOwners(OwnerDirs{DAGsDir: filepath.Join(root, "dags"), Library: library}, nil, nil)
	got, err := owners.Of(t.Context(), filepath.Join(library, "ABC.yaml"))
	require.NoError(t, err)
	require.Equal(t, Owner{Library: true}, got)

	file := writeDAG(t, filepath.Join(target, "ABC.yaml"))
	got, err = owners.Of(t.Context(), file)
	require.NoError(t, err)
	assert.Equal(t, Owner{Library: true}, got)
}
