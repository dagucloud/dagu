// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/test/othername"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// overlap formats an overlap as PathOverlaps describes it.
func overlap(root, rootPath, relation, other, otherPath string) string {
	return fmt.Sprintf("%s %q %s %s %q", root, rootPath, relation, other, otherPath)
}

func TestPathOverlaps(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// edit changes the layout below root and returns the overlaps it makes.
		edit func(root string, p *config.PathsConfig) []string
	}{
		{
			name: "DefaultLayout",
			edit: func(string, *config.PathsConfig) []string { return nil },
		},
		{
			name: "DAGsDirIsDataDir",
			edit: func(_ string, p *config.PathsConfig) []string {
				p.DAGsDir = p.DataDir
				return []string{overlap("paths.dags_dir", p.DAGsDir, "is", "paths.data_dir", p.DataDir)}
			},
		},
		{
			// Containing paths.data_dir accounts for the state roots in it,
			// so they are not listed again. State roots outside it still are.
			name: "DAGsDirContainsDataDir",
			edit: func(root string, p *config.PathsConfig) []string {
				p.DAGsDir = root
				p.CalendarsDir = filepath.Join(root, "calendars")
				return []string{
					overlap("paths.dags_dir", root, "contains", "paths.data_dir", p.DataDir),
					overlap("paths.dags_dir", root, "contains", "paths.calendars_dir", p.CalendarsDir),
				}
			},
		},
		{
			name: "DAGsDirInDataDir",
			edit: func(_ string, p *config.PathsConfig) []string {
				p.DAGsDir = filepath.Join(p.DataDir, "dags")
				return nil
			},
		},
		{
			name: "ProjectsDirIsProjectState",
			edit: func(_ string, p *config.PathsConfig) []string {
				p.ProjectsDir = filepath.Join(p.DataDir, "projects")
				return []string{overlap("paths.projects_dir", p.ProjectsDir, "is", "paths.data_dir/projects", p.ProjectsDir)}
			},
		},
		{
			name: "DAGsDirInsideProjectState",
			edit: func(_ string, p *config.PathsConfig) []string {
				p.DAGsDir = filepath.Join(p.DataDir, "projects", "default")
				return []string{overlap("paths.dags_dir", p.DAGsDir, "lies inside",
					"paths.data_dir/projects", filepath.Join(p.DataDir, "projects"))}
			},
		},
		{
			name: "DAGsDirInsideServerStore",
			edit: func(_ string, p *config.PathsConfig) []string {
				p.DAGsDir = filepath.Join(p.DataDir, "auth", "dags")
				return []string{overlap("paths.dags_dir", p.DAGsDir, "lies inside",
					"paths.data_dir/auth", filepath.Join(p.DataDir, "auth"))}
			},
		},
		{
			// The default tunnel state holds the node's keys.
			name: "DAGsDirIsTunnelState",
			edit: func(_ string, p *config.PathsConfig) []string {
				p.DAGsDir = filepath.Join(p.DataDir, "tailscale")
				return []string{overlap("paths.dags_dir", p.DAGsDir, "is", "paths.data_dir/tailscale", p.DAGsDir)}
			},
		},
		{
			name: "DAGsDirContainsServerStoreSetting",
			edit: func(_ string, p *config.PathsConfig) []string {
				p.UsersDir = filepath.Join(p.DAGsDir, "users")
				return []string{overlap("paths.dags_dir", p.DAGsDir, "contains", "paths.users_dir", p.UsersDir)}
			},
		},
		{
			name: "AltDAGsDirIsServerStoreSetting",
			edit: func(root string, p *config.PathsConfig) []string {
				p.CalendarsDir = filepath.Join(root, "calendars")
				p.AltDAGsDir = p.CalendarsDir
				return []string{overlap("paths.alt_dags_dir", p.AltDAGsDir, "is", "paths.calendars_dir", p.CalendarsDir)}
			},
		},
		{
			name: "DAGsDirInsidePathSetApart",
			edit: func(_ string, p *config.PathsConfig) []string {
				p.Explicit.LogDir = true
				p.DAGsDir = filepath.Join(p.LogDir, "dags")
				return []string{overlap("paths.dags_dir", p.DAGsDir, "lies inside", "paths.log_dir", p.LogDir)}
			},
		},
		{
			// A default log_dir is no path set apart: projects keep their
			// logs in their state directories.
			name: "DAGsDirInsideDefaultLogDir",
			edit: func(_ string, p *config.PathsConfig) []string {
				p.DAGsDir = filepath.Join(p.LogDir, "dags")
				return nil
			},
		},
		{
			name: "DAGRootsSame",
			edit: func(_ string, p *config.PathsConfig) []string {
				p.ProjectsDir = p.DAGsDir
				p.AltDAGsDir = p.DAGsDir
				return []string{
					overlap("paths.dags_dir", p.DAGsDir, "is", "paths.projects_dir", p.ProjectsDir),
					overlap("paths.dags_dir", p.DAGsDir, "is", "paths.alt_dags_dir", p.AltDAGsDir),
					overlap("paths.projects_dir", p.ProjectsDir, "is", "paths.alt_dags_dir", p.AltDAGsDir),
				}
			},
		},
		{
			// Discovery skips a DAG root nested in another.
			name: "DAGRootsNested",
			edit: func(_ string, p *config.PathsConfig) []string {
				p.ProjectsDir = filepath.Join(p.DAGsDir, "projects")
				p.AltDAGsDir = filepath.Join(p.DAGsDir, "library")
				return nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := projectsTestConfig(t, true)
			want := tc.edit(filepath.Dir(cfg.Paths.DataDir), &cfg.Paths)
			assert.Equal(t, want, cfg.PathOverlaps())
		})
	}
}

// Each case spells a path differently from the directory it overlaps, on
// disk or not yet created, and the overlap is still found.
func TestPathOverlapsByIdentity(t *testing.T) {
	t.Parallel()

	t.Run("Symlink", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		root := filepath.Dir(cfg.Paths.DataDir)
		require.NoError(t, os.MkdirAll(cfg.Paths.DataDir, 0750))
		link := filepath.Join(t.TempDir(), "home")
		symlinkOrSkip(t, root, link)
		cfg.Paths.DAGsDir = link

		assert.Equal(t, []string{
			overlap("paths.dags_dir", link, "contains", "paths.data_dir", cfg.Paths.DataDir),
		}, cfg.PathOverlaps())
	})

	t.Run("MissingBelowSymlink", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		require.NoError(t, os.MkdirAll(cfg.Paths.DataDir, 0750))
		link := filepath.Join(t.TempDir(), "data")
		symlinkOrSkip(t, cfg.Paths.DataDir, link)
		cfg.Paths.ProjectsDir = filepath.Join(link, "projects")

		assert.Equal(t, []string{
			overlap("paths.projects_dir", cfg.Paths.ProjectsDir, "is",
				"paths.data_dir/projects", filepath.Join(cfg.Paths.DataDir, "projects")),
		}, cfg.PathOverlaps())
	})

	// A link to a directory not created yet places the DAG root at its
	// target, so the first start, which would create the target, refuses.
	t.Run("LinkToMissingDir", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		root := filepath.Dir(cfg.Paths.DataDir)
		require.NoError(t, os.MkdirAll(cfg.Paths.DataDir, 0750))
		projectsLink := filepath.Join(t.TempDir(), "projects")
		symlinkOrSkip(t, filepath.Join(cfg.Paths.DataDir, "projects"), projectsLink)
		cfg.Paths.ProjectsDir = projectsLink
		// A relative target resolves from the link's directory.
		authLink := filepath.Join(root, "auth")
		symlinkOrSkip(t, filepath.Join("data", "auth"), authLink)
		cfg.Paths.AltDAGsDir = filepath.Join(authLink, "library")

		assert.Equal(t, []string{
			overlap("paths.projects_dir", projectsLink, "is",
				"paths.data_dir/projects", filepath.Join(cfg.Paths.DataDir, "projects")),
			overlap("paths.alt_dags_dir", cfg.Paths.AltDAGsDir, "lies inside",
				"paths.data_dir/auth", filepath.Join(cfg.Paths.DataDir, "auth")),
		}, cfg.PathOverlaps())
	})

	// A ".." after a link in a missing target leaves that link's target, as
	// for a path that exists.
	t.Run("LinkToMissingDirThroughLink", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		root := filepath.Dir(cfg.Paths.DataDir)
		live := filepath.Join(cfg.Paths.DataDir, "auth", "live")
		require.NoError(t, os.MkdirAll(live, 0750))
		symlinkOrSkip(t, live, filepath.Join(root, "jump"))
		library := filepath.Join(root, "library")
		sep := string(filepath.Separator)
		symlinkOrSkip(t, "jump"+sep+".."+sep+"library", library)
		cfg.Paths.AltDAGsDir = library

		assert.Equal(t, []string{
			overlap("paths.alt_dags_dir", library, "lies inside",
				"paths.data_dir/auth", filepath.Join(cfg.Paths.DataDir, "auth")),
		}, cfg.PathOverlaps())
	})

	t.Run("LetterCase", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		root := filepath.Dir(cfg.Paths.DataDir)
		require.NoError(t, os.MkdirAll(cfg.Paths.DataDir, 0750))
		upper := filepath.Join(root, "DATA")
		skipIfCaseSensitive(t, cfg.Paths.DataDir, upper)
		cfg.Paths.DAGsDir = upper
		// Neither directory exists, so their names decide.
		cfg.Paths.ProjectsDir = filepath.Join(root, "Shared", "DAGs")
		cfg.Paths.AltDAGsDir = filepath.Join(root, "shared", "dags")

		assert.Equal(t, []string{
			overlap("paths.dags_dir", upper, "is", "paths.data_dir", cfg.Paths.DataDir),
			overlap("paths.projects_dir", cfg.Paths.ProjectsDir, "is", "paths.alt_dags_dir", cfg.Paths.AltDAGsDir),
		}, cfg.PathOverlaps())
	})

	// The other name is a short name on Windows and the decomposed Unicode
	// form on APFS.
	t.Run("OtherName", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		root := filepath.Dir(cfg.Paths.DataDir)
		dir, otherName := othername.Mkdir(t, root)
		cfg.Paths.DataDir = dir
		cfg.Paths.DAGsDir = filepath.Join(root, otherName, "projects", "default")

		assert.Equal(t, []string{
			overlap("paths.dags_dir", cfg.Paths.DAGsDir, "lies inside",
				"paths.data_dir/projects", filepath.Join(dir, "projects")),
		}, cfg.PathOverlaps())
	})
}

func TestCheckProjectPaths(t *testing.T) {
	t.Parallel()

	overlapping := func(enabled bool) *config.Config {
		cfg := projectsTestConfig(t, enabled)
		cfg.Paths.ProjectsDir = filepath.Join(cfg.Paths.DataDir, "projects")
		cfg.Paths.AltDAGsDir = cfg.Paths.DAGsDir
		return cfg
	}

	t.Run("Off", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, overlapping(false).CheckProjectPaths())
	})

	t.Run("OnWithoutOverlap", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, projectsTestConfig(t, true).CheckProjectPaths())
	})

	t.Run("OnNamesEveryOverlap", func(t *testing.T) {
		t.Parallel()
		cfg := overlapping(true)
		err := cfg.CheckProjectPaths()
		require.Error(t, err)
		for _, o := range cfg.PathOverlaps() {
			assert.Contains(t, err.Error(), o)
		}
		assert.Len(t, cfg.PathOverlaps(), 2)
	})
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
}

// skipIfCaseSensitive skips the test unless other, which differs from dir
// only in letter case, opens dir.
func skipIfCaseSensitive(t *testing.T, dir, other string) {
	t.Helper()
	require.True(t, strings.EqualFold(dir, other))
	want, err := os.Stat(dir)
	require.NoError(t, err)
	got, err := os.Stat(other)
	if err != nil || !os.SameFile(want, got) {
		t.Skip("the file system tells letter case apart")
	}
}
