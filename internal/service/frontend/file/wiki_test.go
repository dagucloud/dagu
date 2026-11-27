// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/wiki"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wikiFixture is a server with projects on whose default wiki directory
// holds the page home and, in the v2 directory of workspace ops, the page
// runbook. Migration made project ops from that workspace.
type wikiFixture struct {
	cfg      *config.Config
	registry *projectregistry.Registry
	wikis    *projectWikis
	ops      project.Project
}

func newWikiFixture(t *testing.T) *wikiFixture {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{ProjectsEnabled: true}
	cfg.Paths = config.PathsConfig{
		DAGsDir:     filepath.Join(root, "dags"),
		ProjectsDir: filepath.Join(root, "projects"),
		DataDir:     filepath.Join(root, "data"),
		WikiDir:     filepath.Join(root, "dags", "wiki"),
	}
	registry := projectregistry.New(cfg.Paths.DataDir)
	ops, err := registry.Create(t.Context(), project.CreateInput{Name: "ops", MigratedFrom: "ops"})
	require.NoError(t, err)

	shared, err := newWikiStore(cfg)
	require.NoError(t, err)
	require.NoError(t, shared.Create(t.Context(), "home", "needle [[runbook]]"))
	require.NoError(t, shared.Create(t.Context(), "ops/runbook", "needle [[home]]"))
	return &wikiFixture{
		cfg:      cfg,
		registry: registry,
		wikis:    newProjectWikis(cfg, registry, shared),
		ops:      ops,
	}
}

func (f *wikiFixture) wiki(t *testing.T, p project.Project) wiki.PageStore {
	t.Helper()
	store, err := f.wikis.storeOf(t.Context(), p)
	require.NoError(t, err)
	return store
}

func (f *wikiFixture) defaultWiki(t *testing.T) wiki.PageStore {
	t.Helper()
	return f.wiki(t, project.Project{Name: project.DefaultName})
}

func flatIDs(t *testing.T, store wiki.PageStore) []string {
	t.Helper()
	result, err := store.ListFlat(t.Context(), wiki.ListPagesOptions{Page: 1, PerPage: 100})
	require.NoError(t, err)
	ids := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestDefaultWikiLeavesOutMigratedProjects(t *testing.T) {
	f := newWikiFixture(t)
	ctx := t.Context()
	store := f.defaultWiki(t)

	assert.Equal(t, []string{"home"}, flatIDs(t, store))

	_, err := store.Get(ctx, "ops/runbook")
	require.ErrorIs(t, err, wiki.ErrPageNotFound)

	search, err := store.Search(ctx, "needle")
	require.NoError(t, err)
	require.Len(t, search, 1)
	assert.Equal(t, "home", search[0].ID)

	// The directory belongs to ops in any case, so no write of the default
	// project reaches it.
	for _, id := range []string{"ops/new", "OPS/new"} {
		require.ErrorIs(t, store.Create(ctx, id, "x"), wiki.ErrPagePathConflict, id)
	}
	require.ErrorIs(t, store.Rename(ctx, "home", "ops/home"), wiki.ErrPagePathConflict)
	deleted, failed, err := store.DeleteBatch(ctx, []string{"ops"})
	require.NoError(t, err)
	assert.Empty(t, deleted)
	require.Len(t, failed, 1)
	assert.FileExists(t, filepath.Join(f.cfg.Paths.WikiDir, "ops", "runbook.md"))
}

// teamProject registers project Team, which migration made from workspace
// Team, whose directory is spelled team on disk.
func (f *wikiFixture) teamProject(t *testing.T) project.Project {
	t.Helper()
	team, err := f.registry.Create(t.Context(), project.CreateInput{Name: "Team", MigratedFrom: "Team"})
	require.NoError(t, err)
	dir := filepath.Join(f.cfg.Paths.WikiDir, "team")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plan.md"), []byte("needle\n"), 0o600))
	return team
}

func TestDefaultWikiLeavesOutMigratedRootInAnyCase(t *testing.T) {
	f := newWikiFixture(t)
	ctx := t.Context()
	f.teamProject(t)
	store := f.defaultWiki(t)

	assert.Equal(t, []string{"home"}, flatIDs(t, store))
	search, err := store.SearchCursor(ctx, wiki.SearchPagesOptions{Query: "needle", Limit: 10, MatchLimit: 1})
	require.NoError(t, err)
	require.Len(t, search.Items, 1)
	assert.Equal(t, "home", search.Items[0].ID)
	_, err = store.SearchMatches(ctx, "runbook", wiki.SearchPageMatchesOptions{Query: "needle", Limit: 1, PathPrefix: "/ops/"})
	require.ErrorIs(t, err, wiki.ErrPageNotFound)
}

func TestMigratedProjectWikiFindsRootInAnyCase(t *testing.T) {
	f := newWikiFixture(t)
	ctx := t.Context()
	store := f.wiki(t, f.teamProject(t))

	assert.Equal(t, []string{"plan"}, flatIDs(t, store))
	search, err := store.SearchCursor(ctx, wiki.SearchPagesOptions{Query: "needle", Limit: 10, MatchLimit: 1})
	require.NoError(t, err)
	require.Len(t, search.Items, 1)
	assert.Equal(t, "plan", search.Items[0].ID)
	_, err = store.Get(ctx, "plan")
	require.NoError(t, err)
}

func TestMigratedProjectWikiStaysInPlace(t *testing.T) {
	f := newWikiFixture(t)
	ctx := t.Context()
	store := f.wiki(t, f.ops)

	assert.Equal(t, []string{"runbook"}, flatIDs(t, store))
	page, err := store.Get(ctx, "runbook")
	require.NoError(t, err)
	assert.Equal(t, "runbook", page.ID)

	require.NoError(t, store.Create(ctx, "notes", "x"))
	assert.FileExists(t, filepath.Join(f.cfg.Paths.WikiDir, "ops", "notes.md"))
	assert.Equal(t, []string{"home"}, flatIDs(t, f.defaultWiki(t)))
}

func TestMigratedProjectUsesItsOwnWikiOnceItExists(t *testing.T) {
	f := newWikiFixture(t)
	ownDir := filepath.Join(f.cfg.Paths.ProjectsDir, "ops", "wiki")
	require.NoError(t, os.MkdirAll(ownDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(ownDir, "moved.md"), []byte("x\n"), 0o600))

	assert.Equal(t, []string{"moved"}, flatIDs(t, f.wiki(t, f.ops)))
	assert.Equal(t, []string{"home"}, flatIDs(t, f.defaultWiki(t)))
}

// TestMovedWikiKeepsHistory moves the v2 directory of ops to its own wiki
// directory, as an admin would. Its page keeps its revision and attachment,
// which lived outside that directory.
func TestMovedWikiKeepsHistory(t *testing.T) {
	f := newWikiFixture(t)
	ctx := t.Context()
	inPlace := f.wiki(t, f.ops)
	require.NoError(t, inPlace.Update(ctx, "runbook", "v2"))
	_, err := inPlace.PutAttachment(ctx, "runbook", "diagram.png", strings.NewReader("png"))
	require.NoError(t, err)

	moveOpsWiki(t, f)
	store := f.wiki(t, f.ops)

	revisions, err := store.ListRevisions(ctx, "runbook")
	require.NoError(t, err)
	require.Len(t, revisions, 1)
	revision, err := store.GetRevision(ctx, "runbook", revisions[0].Rev)
	require.NoError(t, err)
	assert.Equal(t, "needle [[home]]", revision.Content)
	file, _, err := store.OpenAttachment(ctx, "runbook", "diagram.png")
	require.NoError(t, err)
	data, err := io.ReadAll(file)
	require.NoError(t, file.Close())
	require.NoError(t, err)
	assert.Equal(t, "png", string(data))
	assert.NoDirExists(t, filepath.Join(f.cfg.Paths.WikiDir, ".attachments"))
}

func moveOpsWiki(t *testing.T, f *wikiFixture) {
	t.Helper()
	ownDir := filepath.Join(f.cfg.Paths.ProjectsDir, "ops", "wiki")
	require.NoError(t, os.MkdirAll(filepath.Dir(ownDir), 0o750))
	require.NoError(t, os.Rename(filepath.Join(f.cfg.Paths.WikiDir, "ops"), ownDir))
}

// TestWikiPagesLeftBehindAreLogged leaves a page in the v2 directory of ops
// after its own wiki directory exists. That page shows in no wiki, so the
// server names the directory in a warning.
func TestWikiPagesLeftBehindAreLogged(t *testing.T) {
	f := newWikiFixture(t)
	moveOpsWiki(t, f)
	left := filepath.Join(f.cfg.Paths.WikiDir, "ops")
	require.NoError(t, os.MkdirAll(left, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(left, "old.md"), []byte("x\n"), 0o600))

	var logs bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithWriter(&logs), logger.WithQuiet(), logger.WithFormat("json")))
	store, err := f.wikis.storeOf(ctx, f.ops)
	require.NoError(t, err)

	assert.Equal(t, []string{"runbook"}, flatIDs(t, store))
	assert.Equal(t, []string{"home"}, flatIDs(t, f.defaultWiki(t)))
	var entry struct {
		Msg   string `json:"msg"`
		Dir   string `json:"dir"`
		Count int    `json:"count"`
	}
	require.NoError(t, json.Unmarshal(logs.Bytes(), &entry), logs.String())
	assert.Contains(t, entry.Msg, "Wiki pages left in the v2 directory")
	assert.Equal(t, left, entry.Dir)
	assert.Equal(t, 1, entry.Count)
}

// TestProjectWikiInDAGDirectory proves that a project's pages live in wiki/
// of its DAG directory, and that its revisions and page metadata stay out of
// the projects directory.
func TestProjectWikiInDAGDirectory(t *testing.T) {
	f := newWikiFixture(t)
	ctx := t.Context()
	weather, err := f.registry.Create(ctx, project.CreateInput{Name: "weather"})
	require.NoError(t, err)
	dagsDir := filepath.Join(f.cfg.Paths.ProjectsDir, "weather")
	require.NoError(t, os.MkdirAll(dagsDir, 0o750))

	store := f.wiki(t, weather)
	require.NoError(t, store.Create(ctx, "forecast", "v1"))
	require.NoError(t, store.Update(ctx, "forecast", "v2"))

	revisions, err := store.ListRevisions(ctx, "forecast")
	require.NoError(t, err)
	assert.Len(t, revisions, 1)
	var files []string
	require.NoError(t, filepath.WalkDir(dagsDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dagsDir, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return err
	}))
	assert.Equal(t, []string{"wiki/forecast.md"}, files)
	assert.Equal(t, []string{"home"}, flatIDs(t, f.defaultWiki(t)))
}

func TestProjectWikiNeedsDAGDirectory(t *testing.T) {
	f := newWikiFixture(t)
	weather, err := f.registry.Create(t.Context(), project.CreateInput{Name: "weather"})
	require.NoError(t, err)

	_, err = f.wikis.storeOf(t.Context(), weather)
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.NoDirExists(t, filepath.Join(f.cfg.Paths.ProjectsDir, "weather"))
}

// TestWikiLinksResolveInPageProject links home in the default project to
// runbook, and runbook in ops to home. Each link names a page of its own
// project, so ops gets a home that links back, and neither project's pages
// link into the other.
func TestWikiLinksResolveInPageProject(t *testing.T) {
	f := newWikiFixture(t)
	ctx := t.Context()
	opsWiki := f.wiki(t, f.ops)
	require.NoError(t, opsWiki.Create(ctx, "home", "[[runbook]]"))

	backlinkIDs := func(store wiki.PageStore, target string) []string {
		results, err := store.Backlinks(ctx, target, "")
		require.NoError(t, err)
		ids := []string{}
		for _, m := range results {
			ids = append(ids, m.ID)
		}
		return ids
	}
	assert.Equal(t, []string{"home"}, backlinkIDs(opsWiki, "runbook"))
	assert.Equal(t, []string{"runbook"}, backlinkIDs(opsWiki, "home"))
	assert.Empty(t, backlinkIDs(f.defaultWiki(t), "home"))
	assert.Equal(t, []string{"home"}, backlinkIDs(f.defaultWiki(t), "runbook"))
}
