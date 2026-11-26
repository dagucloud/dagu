// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	filewiki "github.com/dagucloud/dagu/v2/internal/persis/file/wiki"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/textsearch"
	"github.com/dagucloud/dagu/v2/internal/wiki"
)

const (
	// projectWikiDirName is the wiki directory in a project's DAG directory,
	// and the directory of its revisions and page metadata in the project's
	// state directory.
	projectWikiDirName = "wiki"
	// migratedWikiCheckInterval bounds how often the registry is read for
	// the projects that migration made. Every process checks the registry
	// for changes as often.
	migratedWikiCheckInterval = 5 * time.Second
)

// projectWikis opens the wiki of each project while projects are on:
//
//	default project   the default wiki directory, less <wiki>/X/ of each
//	                  project X that migration made
//	migrated X        <wiki>/X/ in place, until <projects_dir>/X/wiki/ exists
//	any other         wiki/ in its DAG directory, with its revisions and page
//	                  metadata in its state directory
type projectWikis struct {
	cfg      *config.Config
	registry project.Registry
	// shared is the store of the default wiki directory.
	shared   wiki.PageStore
	defaults *wikiView

	mu        sync.Mutex
	checkedAt time.Time
	migrated  []string
	// own holds the stores of projects with wiki directories of their own,
	// keyed by project ID.
	own map[string]*filewiki.Store
}

func newProjectWikis(cfg *config.Config, registry project.Registry, shared wiki.PageStore) *projectWikis {
	w := &projectWikis{
		cfg:      cfg,
		registry: registry,
		shared:   shared,
		own:      make(map[string]*filewiki.Store),
	}
	w.defaults = &wikiView{store: shared, excluded: w.migratedRoots}
	return w
}

// storeOf returns the wiki of p, a registered project.
func (w *projectWikis) storeOf(ctx context.Context, p project.Project) (wiki.PageStore, error) {
	if strings.EqualFold(p.Name, project.DefaultName) {
		return w.defaults, nil
	}
	// Drops the stores of projects the registry no longer holds.
	if _, err := w.migratedRoots(ctx); err != nil {
		return nil, err
	}
	paths, err := persisfile.ProjectStorePaths(w.cfg, p.Name)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(paths.DAGsDir, projectWikiDirName)
	if p.MigratedFrom != "" {
		exists, err := dirExists(dir)
		if err != nil {
			return nil, err
		}
		if !exists {
			return &wikiView{store: w.shared, root: p.MigratedFrom}, nil
		}
	}
	return w.ownStore(p, paths, dir)
}

func (w *projectWikis) ownStore(p project.Project, paths config.ProjectPaths, dir string) (*filewiki.Store, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if store, ok := w.own[p.ID]; ok {
		return store, nil
	}
	// Opening a store creates its directory, which must not bring back the
	// DAG directory of a missing project.
	exists, err := dirExists(paths.DAGsDir)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("project %q has no DAG directory %s: %w", p.Name, paths.DAGsDir, os.ErrNotExist)
	}
	stateDir := filepath.Join(paths.OtherStateDir, projectWikiDirName)
	store, err := filewiki.New(dir, filewiki.WithDataDir(stateDir), filewiki.WithMetadataDir(stateDir))
	if err != nil {
		return nil, fmt.Errorf("wiki store of project %q: %w", p.Name, err)
	}
	w.own[p.ID] = store
	return store, nil
}

// migratedRoots returns the v2 workspace names of the projects that
// migration made, whose directories under the default wiki directory are
// theirs, and the spellings those directories have on disk. It reads the
// registry and that directory at most once per check interval.
func (w *projectWikis) migratedRoots(ctx context.Context) ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.checkedAt.IsZero() && time.Since(w.checkedAt) < migratedWikiCheckInterval {
		return w.migrated, nil
	}
	projects, err := w.registry.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the project registry: %w", err)
	}
	var roots []string
	live := make(map[string]bool, len(projects))
	for _, p := range projects {
		if p.MigratedFrom != "" {
			roots = append(roots, p.MigratedFrom)
		}
		if !p.Deleting {
			live[p.ID] = true
		}
	}
	for id := range w.own {
		if !live[id] {
			delete(w.own, id)
		}
	}
	// Listing and search exclude by the spelling a directory has on disk.
	if roots, err = w.withDiskSpellings(roots); err != nil {
		return nil, err
	}
	w.migrated, w.checkedAt = roots, time.Now()
	return roots, nil
}

// withDiskSpellings adds to roots each directory of the default wiki
// directory whose name matches one of them without regard to case.
func (w *projectWikis) withDiskSpellings(roots []string) ([]string, error) {
	if len(roots) == 0 {
		return roots, nil
	}
	entries, err := os.ReadDir(w.cfg.Paths.WikiDir)
	if os.IsNotExist(err) {
		return roots, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the wiki directory: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() && ownedElsewhere(name, roots) && !slices.Contains(roots, name) {
			roots = append(roots, name)
		}
	}
	return roots, nil
}

func dirExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", path, err)
	}
	return info.IsDir(), nil
}

// wikiView presents part of a wiki store as a wiki of its own, with page
// IDs relative to that part. With a root, it holds the pages under the root.
// Without one, it holds every page outside the roots that excluded returns,
// which other projects own, compared without regard to case.
//
// A read of a path outside the view finds nothing, and a write to one
// conflicts with the pages there.
type wikiView struct {
	store    wiki.PageStore
	root     string
	excluded func(context.Context) ([]string, error)
}

var _ wiki.PageStore = (*wikiView)(nil)

// stored returns the store's ID of the page id, and false when id lies
// outside the view.
func (v *wikiView) stored(ctx context.Context, id string) (string, bool, error) {
	if v.root != "" {
		return v.root + "/" + id, true, nil
	}
	roots, err := v.excluded(ctx)
	if err != nil {
		return "", false, err
	}
	return id, !ownedElsewhere(id, roots), nil
}

// relative returns a function that gives the view's ID of a store's page
// ID, and false when the page lies outside the view.
func (v *wikiView) relative(ctx context.Context) (func(id string) (string, bool), error) {
	if v.root != "" {
		return func(id string) (string, bool) {
			rel, ok := strings.CutPrefix(id, v.root+"/")
			return rel, ok && rel != ""
		}, nil
	}
	roots, err := v.excluded(ctx)
	if err != nil {
		return nil, err
	}
	return func(id string) (string, bool) {
		return id, !ownedElsewhere(id, roots)
	}, nil
}

// scope returns the store's path prefix and excluded paths for a query of
// the view under prefix that excludes the paths in excluded.
func (v *wikiView) scope(ctx context.Context, prefix string, excluded []string) (string, []string, error) {
	prefix = cleanPrefix(prefix)
	if v.root == "" {
		roots, err := v.excluded(ctx)
		if err != nil {
			return "", nil, err
		}
		return prefix, append(slices.Clone(excluded), roots...), nil
	}
	scoped := make([]string, 0, len(excluded))
	for _, path := range excluded {
		scoped = append(scoped, v.root+"/"+path)
	}
	if prefix == "" {
		return v.root, scoped, nil
	}
	return v.root + "/" + prefix, scoped, nil
}

// cleanPrefix spells a path prefix as the store reads it.
func cleanPrefix(prefix string) string {
	return strings.Trim(strings.TrimSpace(prefix), "/")
}

func ownedElsewhere(id string, roots []string) bool {
	first, _, _ := strings.Cut(id, "/")
	return slices.ContainsFunc(roots, func(root string) bool {
		return strings.EqualFold(first, root)
	})
}

func (v *wikiView) List(ctx context.Context, opts wiki.ListPagesOptions) (*pagination.PaginatedResult[*wiki.PageTreeNode], error) {
	var err error
	if opts.PathPrefix, opts.ExcludePathRoots, err = v.scope(ctx, opts.PathPrefix, opts.ExcludePathRoots); err != nil {
		return nil, err
	}
	return v.store.List(ctx, opts)
}

func (v *wikiView) ListFlat(ctx context.Context, opts wiki.ListPagesOptions) (*pagination.PaginatedResult[wiki.PageMetadata], error) {
	var err error
	if opts.PathPrefix, opts.ExcludePathRoots, err = v.scope(ctx, opts.PathPrefix, opts.ExcludePathRoots); err != nil {
		return nil, err
	}
	return v.store.ListFlat(ctx, opts)
}

func (v *wikiView) Get(ctx context.Context, id string) (*wiki.Page, error) {
	storedID, ok, err := v.stored(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, wiki.ErrPageNotFound
	}
	page, err := v.store.Get(ctx, storedID)
	if err != nil {
		return nil, err
	}
	page.ID = id
	return page, nil
}

func (v *wikiView) Create(ctx context.Context, id, content string) error {
	return v.write(ctx, id, func(storedID string) error {
		return v.store.Create(ctx, storedID, content)
	})
}

func (v *wikiView) Update(ctx context.Context, id, content string) error {
	return v.write(ctx, id, func(storedID string) error {
		return v.store.Update(ctx, storedID, content)
	})
}

func (v *wikiView) Delete(ctx context.Context, id string) error {
	return v.write(ctx, id, func(storedID string) error {
		return v.store.Delete(ctx, storedID)
	})
}

// write calls fn with the store's ID of id, or refuses a path outside the
// view.
func (v *wikiView) write(ctx context.Context, id string, fn func(storedID string) error) error {
	storedID, ok, err := v.stored(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return wiki.ErrPagePathConflict
	}
	return fn(storedID)
}

func (v *wikiView) DeleteBatch(ctx context.Context, ids []string) ([]string, []wiki.DeleteError, error) {
	var failed []wiki.DeleteError
	storedIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		storedID, ok, err := v.stored(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			failed = append(failed, wiki.DeleteError{ID: id, Error: wiki.ErrPagePathConflict.Error()})
			continue
		}
		storedIDs = append(storedIDs, storedID)
	}
	if len(storedIDs) == 0 {
		return nil, failed, nil
	}
	deleted, storeFailed, err := v.store.DeleteBatch(ctx, storedIDs)
	if err != nil {
		return nil, nil, err
	}
	for i, id := range deleted {
		deleted[i] = v.trimRoot(id)
	}
	for _, f := range storeFailed {
		failed = append(failed, wiki.DeleteError{ID: v.trimRoot(f.ID), Error: f.Error})
	}
	return deleted, failed, nil
}

// trimRoot returns the view's ID of a store's ID that the view passed to
// the store.
func (v *wikiView) trimRoot(id string) string {
	if v.root == "" {
		return id
	}
	return strings.TrimPrefix(id, v.root+"/")
}

func (v *wikiView) Rename(ctx context.Context, oldID, newID string) error {
	return v.write(ctx, oldID, func(storedOld string) error {
		return v.write(ctx, newID, func(storedNew string) error {
			return v.store.Rename(ctx, storedOld, storedNew)
		})
	})
}

func (v *wikiView) Backlinks(ctx context.Context, target, pathPrefix string) ([]wiki.PageMetadata, error) {
	// A scheme target such as dag:name is matched as it is.
	if !strings.Contains(target, ":") {
		storedTarget, ok, err := v.stored(ctx, target)
		if err != nil || !ok {
			return nil, err
		}
		target = storedTarget
	}
	prefix, _, err := v.scope(ctx, pathPrefix, nil)
	if err != nil {
		return nil, err
	}
	relative, err := v.relative(ctx)
	if err != nil {
		return nil, err
	}
	results, err := v.store.Backlinks(ctx, target, prefix)
	if err != nil {
		return nil, err
	}
	// A link resolves in the project of the page that holds it.
	inView := results[:0]
	for _, m := range results {
		if id, ok := relative(m.ID); ok {
			m.ID = id
			inView = append(inView, m)
		}
	}
	return inView, nil
}

func (v *wikiView) ListRevisions(ctx context.Context, id string) ([]wiki.PageRevision, error) {
	storedID, ok, err := v.stored(ctx, id)
	if err != nil || !ok {
		return nil, err
	}
	return v.store.ListRevisions(ctx, storedID)
}

func (v *wikiView) GetRevision(ctx context.Context, id, rev string) (*wiki.PageRevision, error) {
	storedID, ok, err := v.stored(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, wiki.ErrPageRevisionNotFound
	}
	return v.store.GetRevision(ctx, storedID, rev)
}

func (v *wikiView) PutAttachment(ctx context.Context, id, name string, content io.Reader) (*wiki.PageAttachment, error) {
	var attachment *wiki.PageAttachment
	err := v.write(ctx, id, func(storedID string) error {
		var err error
		attachment, err = v.store.PutAttachment(ctx, storedID, name, content)
		return err
	})
	return attachment, err
}

func (v *wikiView) OpenAttachment(ctx context.Context, id, name string) (io.ReadCloser, *wiki.PageAttachment, error) {
	storedID, ok, err := v.stored(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, wiki.ErrPageAttachmentNotFound
	}
	return v.store.OpenAttachment(ctx, storedID, name)
}

func (v *wikiView) Search(ctx context.Context, query string) ([]*wiki.PageSearchResult, error) {
	relative, err := v.relative(ctx)
	if err != nil {
		return nil, err
	}
	results, err := v.store.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	inView := results[:0]
	for _, r := range results {
		if id, ok := relative(r.ID); ok {
			r.ID = id
			inView = append(inView, r)
		}
	}
	return inView, nil
}

func (v *wikiView) SearchCursor(ctx context.Context, opts wiki.SearchPagesOptions) (*pagination.CursorResult[wiki.PageSearchResult], error) {
	var err error
	if opts.PathPrefix, opts.ExcludePathRoots, err = v.scope(ctx, opts.PathPrefix, opts.ExcludePathRoots); err != nil {
		return nil, err
	}
	return v.store.SearchCursor(ctx, opts)
}

func (v *wikiView) SearchMatches(ctx context.Context, id string, opts wiki.SearchPageMatchesOptions) (*pagination.CursorResult[*textsearch.Match], error) {
	full := id
	if prefix := cleanPrefix(opts.PathPrefix); prefix != "" {
		full = prefix + "/" + id
	}
	_, ok, err := v.stored(ctx, full)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, wiki.ErrPageNotFound
	}
	if opts.PathPrefix, _, err = v.scope(ctx, opts.PathPrefix, nil); err != nil {
		return nil, err
	}
	return v.store.SearchMatches(ctx, id, opts)
}

func (v *wikiView) PathExists(ctx context.Context, id string) (pageExists, directoryExists bool, err error) {
	storedID, ok, err := v.stored(ctx, id)
	if err != nil || !ok {
		return false, false, err
	}
	return v.store.PathExists(ctx, storedID)
}
