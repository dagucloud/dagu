// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/textsearch"
	"github.com/dagucloud/dagu/v2/internal/wiki"
	workspacepkg "github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errForced is a generic error used to trigger internal error paths in the mock.
var errForced = errors.New("forced error")

// mockWikiStore is an in-memory implementation of wiki.PageStore.
var _ wiki.PageStore = (*mockWikiStore)(nil)

type mockWikiStore struct {
	pages        map[string]*wiki.Page
	revisions    map[string][]wiki.PageRevision
	attachments  map[string]map[string][]byte
	failAll      bool // when true, all operations return errForced
	lastListOpts wiki.ListPagesOptions
}

type mockWorkspaceStore struct {
	workspaces []*workspacepkg.Workspace
	err        error
	updateErr  error
	deleteErr  error
}

func (m *mockWorkspaceStore) Create(_ context.Context, ws *workspacepkg.Workspace) error {
	for _, existing := range m.workspaces {
		if existing.Name == ws.Name {
			return workspacepkg.ErrWorkspaceAlreadyExists
		}
	}
	cp := *ws
	m.workspaces = append(m.workspaces, &cp)
	return nil
}

func (m *mockWorkspaceStore) GetByID(_ context.Context, id string) (*workspacepkg.Workspace, error) {
	for _, ws := range m.workspaces {
		if ws.ID == id {
			return ws, nil
		}
	}
	return nil, workspacepkg.ErrWorkspaceNotFound
}

func (m *mockWorkspaceStore) GetByName(_ context.Context, name string) (*workspacepkg.Workspace, error) {
	for _, ws := range m.workspaces {
		if ws.Name == name {
			return ws, nil
		}
	}
	return nil, workspacepkg.ErrWorkspaceNotFound
}

func (m *mockWorkspaceStore) List(context.Context) ([]*workspacepkg.Workspace, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.workspaces, nil
}

func (m *mockWorkspaceStore) Update(_ context.Context, ws *workspacepkg.Workspace) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	for _, existing := range m.workspaces {
		if existing.Name == ws.Name && existing.ID != ws.ID {
			return workspacepkg.ErrWorkspaceAlreadyExists
		}
	}
	for i, existing := range m.workspaces {
		if existing.ID == ws.ID {
			cp := *ws
			m.workspaces[i] = &cp
			return nil
		}
	}
	return workspacepkg.ErrWorkspaceNotFound
}

func (m *mockWorkspaceStore) Delete(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	for i, existing := range m.workspaces {
		if existing.ID == id {
			m.workspaces = append(m.workspaces[:i], m.workspaces[i+1:]...)
			return nil
		}
	}
	return workspacepkg.ErrWorkspaceNotFound
}

func (m *mockWikiStore) Get(_ context.Context, id string) (*wiki.Page, error) {
	if m.failAll {
		return nil, errForced
	}
	if err := wiki.ValidatePageID(id); err != nil {
		return nil, wiki.ErrInvalidPageID
	}
	doc, ok := m.pages[id]
	if !ok {
		return nil, wiki.ErrPageNotFound
	}
	cp := *doc
	return &cp, nil
}

func (m *mockWikiStore) Create(_ context.Context, id, content string) error {
	if m.failAll {
		return errForced
	}
	if err := wiki.ValidatePageID(id); err != nil {
		return wiki.ErrInvalidPageID
	}
	if _, exists := m.pages[id]; exists {
		return wiki.ErrPageAlreadyExists
	}
	m.pages[id] = &wiki.Page{
		ID:      id,
		Title:   path.Base(id),
		Content: content,
	}
	return nil
}

func (m *mockWikiStore) Update(_ context.Context, id, content string) error {
	if m.failAll {
		return errForced
	}
	if err := wiki.ValidatePageID(id); err != nil {
		return wiki.ErrInvalidPageID
	}
	doc, ok := m.pages[id]
	if !ok {
		return wiki.ErrPageNotFound
	}
	doc.Content = content
	return nil
}

func (m *mockWikiStore) Delete(_ context.Context, id string) error {
	if m.failAll {
		return errForced
	}
	if err := wiki.ValidatePageID(id); err != nil {
		return wiki.ErrInvalidPageID
	}
	if _, ok := m.pages[id]; !ok {
		return wiki.ErrPageNotFound
	}
	delete(m.pages, id)
	return nil
}

func (m *mockWikiStore) Rename(_ context.Context, oldID, newID string) error {
	if m.failAll {
		return errForced
	}
	if err := wiki.ValidatePageID(oldID); err != nil {
		return wiki.ErrInvalidPageID
	}
	if err := wiki.ValidatePageID(newID); err != nil {
		return wiki.ErrInvalidPageID
	}
	if newID == oldID || strings.HasPrefix(newID, oldID+"/") {
		return wiki.ErrPagePathConflict
	}

	// Try an exact page match first.
	if doc, ok := m.pages[oldID]; ok {
		if _, exists := m.pages[newID]; exists {
			return wiki.ErrPageAlreadyExists
		}
		delete(m.pages, oldID)
		doc.ID = newID
		doc.Title = path.Base(newID)
		m.pages[newID] = doc
		return nil
	}

	// Try prefix match (directory rename).
	prefix := oldID + "/"
	var toMove []string
	for id := range m.pages {
		if strings.HasPrefix(id, prefix) {
			toMove = append(toMove, id)
		}
	}
	if len(toMove) == 0 {
		return wiki.ErrPageNotFound
	}

	// Check target prefix doesn't conflict.
	newPrefix := newID + "/"
	for id := range m.pages {
		if strings.HasPrefix(id, newPrefix) || id == newID {
			return wiki.ErrPageAlreadyExists
		}
	}

	// Move all matching wiki.
	for _, id := range toMove {
		doc := m.pages[id]
		delete(m.pages, id)
		newPageID := newID + strings.TrimPrefix(id, oldID)
		doc.ID = newPageID
		doc.Title = path.Base(newPageID)
		m.pages[newPageID] = doc
	}
	return nil
}

func (m *mockWikiStore) PathExists(_ context.Context, id string) (pageExists, directoryExists bool, err error) {
	if m.failAll {
		return false, false, errForced
	}
	if err := wiki.ValidatePageID(id); err != nil {
		return false, false, err
	}
	_, pageExists = m.pages[id]
	prefix := id + "/"
	for pageID := range m.pages {
		if strings.HasPrefix(pageID, prefix) {
			directoryExists = true
			break
		}
	}
	return pageExists, directoryExists, nil
}

func (m *mockWikiStore) DeleteBatch(_ context.Context, ids []string) ([]string, []wiki.DeleteError, error) {
	if m.failAll {
		return nil, nil, errForced
	}
	var deleted []string
	var failed []wiki.DeleteError
	for _, id := range ids {
		if err := wiki.ValidatePageID(id); err != nil {
			failed = append(failed, wiki.DeleteError{ID: id, Error: err.Error()})
			continue
		}
		// Remove an exact page or every page under a directory path.
		if _, ok := m.pages[id]; ok {
			delete(m.pages, id)
		} else {
			prefix := id + "/"
			for pageID := range m.pages {
				if strings.HasPrefix(pageID, prefix) {
					delete(m.pages, pageID)
				}
			}
		}
		// Missing paths are successful to keep batch deletion idempotent.
		deleted = append(deleted, id)
	}
	return deleted, failed, nil
}

func (m *mockWikiStore) Search(_ context.Context, query string) ([]*wiki.PageSearchResult, error) {
	if m.failAll {
		return nil, errForced
	}
	var results []*wiki.PageSearchResult
	for _, doc := range m.pages {
		if strings.Contains(doc.Content, query) {
			// Build matches from content lines containing the query.
			var matches []*textsearch.Match
			for i, line := range strings.Split(doc.Content, "\n") {
				if strings.Contains(line, query) {
					matches = append(matches, &textsearch.Match{
						Line:       line,
						LineNumber: i + 1,
						StartLine:  i + 1,
					})
				}
			}
			results = append(results, &wiki.PageSearchResult{
				ID:          doc.ID,
				Title:       doc.Title,
				Description: doc.Description,
				ModTime:     time.Unix(1700000000, 0),
				Matches:     matches,
			})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results, nil
}

type mockWikiSearchCursor struct {
	Version      int    `json:"v"`
	Query        string `json:"q"`
	PathPrefix   string `json:"prefix,omitempty"`
	FilterPrefix string `json:"filter,omitempty"`
	ID           string `json:"id,omitempty"`
}

type mockWikiMatchCursor struct {
	Version    int    `json:"v"`
	Query      string `json:"q"`
	PathPrefix string `json:"prefix,omitempty"`
	ID         string `json:"id"`
	Offset     int    `json:"offset"`
}

func mockRelativeWikiPageID(id, prefix string) (string, bool) {
	if prefix == "" {
		return id, true
	}
	prefixWithSlash := prefix + "/"
	if !strings.HasPrefix(id, prefixWithSlash) {
		return "", false
	}
	rel := strings.TrimPrefix(id, prefixWithSlash)
	return rel, rel != ""
}

func (m *mockWikiStore) SearchCursor(_ context.Context, opts wiki.SearchPagesOptions) (*pagination.CursorResult[wiki.PageSearchResult], error) {
	allResults, err := m.Search(context.Background(), opts.Query)
	if err != nil {
		return nil, err
	}
	results := make([]*wiki.PageSearchResult, 0, len(allResults))
	for _, item := range allResults {
		if mockWikiPagePathRootExcluded(item.ID, opts.ExcludePathRoots) {
			continue
		}
		id, ok := mockRelativeWikiPageID(item.ID, opts.PathPrefix)
		if !ok || !wikiPagePathHasPrefixForTest(id, opts.FilterPrefix) {
			continue
		}
		cp := *item
		cp.ID = id
		matchLimit := max(opts.MatchLimit, 1)
		if len(cp.Matches) > matchLimit {
			cp.Matches = cp.Matches[:matchLimit]
			cp.HasMoreMatches = true
			cp.NextMatchesCursor = pagination.EncodeSearchCursor(mockWikiMatchCursor{
				Version:    1,
				Query:      opts.Query,
				PathPrefix: opts.PathPrefix,
				ID:         id,
				Offset:     matchLimit,
			})
		}
		results = append(results, &cp)
	}
	limit := max(opts.Limit, 1)
	offset := 0
	if opts.Cursor != "" {
		var cursor mockWikiSearchCursor
		if err := pagination.DecodeSearchCursor(opts.Cursor, &cursor); err != nil {
			return nil, err
		}
		if cursor.Version != 1 ||
			cursor.Query != opts.Query ||
			cursor.PathPrefix != opts.PathPrefix ||
			cursor.FilterPrefix != opts.FilterPrefix {
			return nil, pagination.ErrInvalidCursor
		}
		for i, item := range results {
			if item.ID <= cursor.ID {
				offset = i + 1
				continue
			}
			break
		}
	}
	end := min(offset+limit, len(results))
	pageItems := make([]wiki.PageSearchResult, 0, end-offset)
	for _, item := range results[offset:end] {
		pageItems = append(pageItems, *item)
	}
	result := &pagination.CursorResult[wiki.PageSearchResult]{
		Items:   pageItems,
		HasMore: end < len(results),
	}
	if result.HasMore && len(pageItems) > 0 {
		result.NextCursor = pagination.EncodeSearchCursor(mockWikiSearchCursor{
			Version:      1,
			Query:        opts.Query,
			PathPrefix:   opts.PathPrefix,
			FilterPrefix: opts.FilterPrefix,
			ID:           pageItems[len(pageItems)-1].ID,
		})
	}
	return result, nil
}

func (m *mockWikiStore) SearchMatches(_ context.Context, id string, opts wiki.SearchPageMatchesOptions) (*pagination.CursorResult[*textsearch.Match], error) {
	if err := wiki.ValidatePageID(id); err != nil {
		return nil, wiki.ErrInvalidPageID
	}

	storedID := id
	if opts.PathPrefix != "" {
		storedID = opts.PathPrefix + "/" + id
	}
	doc, ok := m.pages[storedID]
	if !ok {
		return nil, wiki.ErrPageNotFound
	}

	var matches []*textsearch.Match
	if opts.Query != "" {
		for i, line := range strings.Split(doc.Content, "\n") {
			if strings.Contains(line, opts.Query) {
				matches = append(matches, &textsearch.Match{
					Line:       line,
					LineNumber: i + 1,
					StartLine:  i + 1,
				})
			}
		}
	}

	limit := max(opts.Limit, 1)
	offset := 0
	if opts.Cursor != "" {
		var cursor mockWikiMatchCursor
		if err := pagination.DecodeSearchCursor(opts.Cursor, &cursor); err != nil {
			return nil, err
		}
		if cursor.Version != 1 ||
			cursor.Query != opts.Query ||
			cursor.PathPrefix != opts.PathPrefix ||
			cursor.ID != id ||
			cursor.Offset < 0 {
			return nil, pagination.ErrInvalidCursor
		}
		offset = cursor.Offset
	}

	offset = max(offset, 0)
	offset = min(offset, len(matches))
	end := min(offset+limit, len(matches))
	cursorResult := &pagination.CursorResult[*textsearch.Match]{
		Items:   matches[offset:end],
		HasMore: end < len(matches),
	}
	if cursorResult.HasMore {
		cursorResult.NextCursor = pagination.EncodeSearchCursor(mockWikiMatchCursor{
			Version:    1,
			Query:      opts.Query,
			PathPrefix: opts.PathPrefix,
			ID:         id,
			Offset:     end,
		})
	}
	return cursorResult, nil
}

func mockWikiPagePathRootExcluded(id string, excludedRoots []string) bool {
	root, _, _ := strings.Cut(id, "/")
	return slices.Contains(excludedRoots, root)
}

func wikiPagePathHasPrefixForTest(id, prefix string) bool {
	return prefix == "" || id == prefix || strings.HasPrefix(id, prefix+"/")
}

func (m *mockWikiStore) List(_ context.Context, opts wiki.ListPagesOptions) (*pagination.PaginatedResult[*wiki.PageTreeNode], error) {
	m.lastListOpts = opts
	if m.failAll {
		return nil, errForced
	}
	nodes := make([]*wiki.PageTreeNode, 0, len(m.pages))
	for _, doc := range m.pages {
		if mockWikiPagePathRootExcluded(doc.ID, opts.ExcludePathRoots) {
			continue
		}
		id, ok := mockRelativeWikiPageID(doc.ID, opts.PathPrefix)
		if !ok {
			continue
		}
		nodes = append(nodes, &wiki.PageTreeNode{
			ID:    id,
			Name:  path.Base(id),
			Title: doc.Title,
			Tags:  doc.Tags,
			Type:  "file",
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	pg := pagination.NewPaginator(opts.Page, opts.PerPage)
	start := min(pg.Offset(), len(nodes))
	end := min(start+pg.Limit(), len(nodes))
	result := pagination.NewPaginatedResult(nodes[start:end], len(nodes), pg)
	return &result, nil
}

func (m *mockWikiStore) ListFlat(_ context.Context, opts wiki.ListPagesOptions) (*pagination.PaginatedResult[wiki.PageMetadata], error) {
	m.lastListOpts = opts
	if m.failAll {
		return nil, errForced
	}
	items := make([]wiki.PageMetadata, 0, len(m.pages))
	for _, doc := range m.pages {
		if mockWikiPagePathRootExcluded(doc.ID, opts.ExcludePathRoots) {
			continue
		}
		id, ok := mockRelativeWikiPageID(doc.ID, opts.PathPrefix)
		if !ok {
			continue
		}
		items = append(items, wiki.PageMetadata{
			ID:          id,
			Title:       doc.Title,
			Description: doc.Description,
			Tags:        doc.Tags,
			ModTime:     time.Unix(1700000000, 0),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })

	pg := pagination.NewPaginator(opts.Page, opts.PerPage)
	start := min(pg.Offset(), len(items))
	end := min(start+pg.Limit(), len(items))
	result := pagination.NewPaginatedResult(items[start:end], len(items), pg)
	return &result, nil
}

func (m *mockWikiStore) Backlinks(_ context.Context, target, pathPrefix string) ([]wiki.PageMetadata, error) {
	if m.failAll {
		return nil, errForced
	}
	var results []wiki.PageMetadata
	for _, doc := range m.pages {
		if doc.ID == target {
			continue
		}
		underPrefix := pathPrefix != "" && strings.HasPrefix(doc.ID, pathPrefix+"/")
		for _, link := range wiki.ExtractWikiLinks(doc.Content) {
			if link.Target == target || (underPrefix && pathPrefix+"/"+link.Target == target) {
				results = append(results, wiki.PageMetadata{
					ID:          doc.ID,
					Title:       doc.Title,
					Description: doc.Description,
					Tags:        doc.Tags,
					ModTime:     time.Unix(1700000000, 0),
				})
				break
			}
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results, nil
}

func (m *mockWikiStore) ListRevisions(_ context.Context, id string) ([]wiki.PageRevision, error) {
	if m.failAll {
		return nil, errForced
	}
	return m.revisions[id], nil
}

func (m *mockWikiStore) GetRevision(_ context.Context, id, rev string) (*wiki.PageRevision, error) {
	if m.failAll {
		return nil, errForced
	}
	for _, revision := range m.revisions[id] {
		if revision.Rev == rev {
			cp := revision
			return &cp, nil
		}
	}
	return nil, wiki.ErrPageRevisionNotFound
}

func (m *mockWikiStore) PutAttachment(_ context.Context, id, name string, content io.Reader) (*wiki.PageAttachment, error) {
	if m.failAll {
		return nil, errForced
	}
	if err := wiki.ValidateAttachmentName(name); err != nil {
		return nil, err
	}
	if _, ok := m.pages[id]; !ok {
		return nil, wiki.ErrPageNotFound
	}
	data, err := io.ReadAll(content)
	if err != nil {
		return nil, err
	}
	if m.attachments == nil {
		m.attachments = map[string]map[string][]byte{}
	}
	if m.attachments[id] == nil {
		m.attachments[id] = map[string][]byte{}
	}
	m.attachments[id][name] = data
	return &wiki.PageAttachment{Name: name, Size: int64(len(data)), SavedAt: time.Unix(1700000000, 0)}, nil
}

func (m *mockWikiStore) OpenAttachment(_ context.Context, id, name string) (io.ReadCloser, *wiki.PageAttachment, error) {
	if m.failAll {
		return nil, nil, errForced
	}
	data, ok := m.attachments[id][name]
	if !ok {
		return nil, nil, wiki.ErrPageAttachmentNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), &wiki.PageAttachment{
		Name:    name,
		Size:    int64(len(data)),
		SavedAt: time.Unix(1700000000, 0),
	}, nil
}

// wikiTestSetup contains common test infrastructure for Wiki API tests.
type wikiTestSetup struct {
	api   *apiv1.API
	store *mockWikiStore
}

func newWikiPageTestSetup(t *testing.T) *wikiTestSetup {
	t.Helper()
	store := &mockWikiStore{pages: make(map[string]*wiki.Page)}
	return newWikiPageTestSetupWithStore(t, store, nil)
}

func newWikiPageTestSetupWithWorkspaces(t *testing.T, names ...string) *wikiTestSetup {
	t.Helper()
	store := &mockWikiStore{pages: make(map[string]*wiki.Page)}
	workspaces := make([]*workspacepkg.Workspace, 0, len(names))
	for _, name := range names {
		workspaces = append(workspaces, &workspacepkg.Workspace{ID: name, Name: name})
	}
	return newWikiPageTestSetupWithStore(t, store, &mockWorkspaceStore{workspaces: workspaces})
}

func newWikiPageTestSetupWithStore(t *testing.T, store *mockWikiStore, workspaceStore workspacepkg.Store) *wikiTestSetup {
	t.Helper()
	return newWikiPageTestSetupWithStoreOptions(t, store, workspaceStore)
}

func newWikiPageTestSetupWithStoreOptions(
	t *testing.T,
	store *mockWikiStore,
	workspaceStore workspacepkg.Store,
	extraOptions ...apiv1.APIOption,
) *wikiTestSetup {
	t.Helper()
	cfg := &config.Config{}
	cfg.Server.Permissions = map[config.Permission]bool{
		config.PermissionWriteDAGs: true,
	}
	options := []apiv1.APIOption{apiv1.WithWikiStore(store)}
	if workspaceStore != nil {
		options = append(options, apiv1.WithWorkspaceStore(workspaceStore))
	}
	options = append(options, extraOptions...)
	a := apiv1.New(
		nil, nil, nil, nil, runtime.Manager{},
		cfg, nil, nil,
		prometheus.NewRegistry(),
		nil,
		options...,
	)
	return &wikiTestSetup{api: a, store: store}
}

func TestListWikiPages(t *testing.T) {
	t.Parallel()

	t.Run("flat mode returns items", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["alpha"] = &wiki.Page{ID: "alpha", Title: "alpha", Description: "Alpha doc", Content: "content-a"}
		setup.store.pages["beta"] = &wiki.Page{ID: "beta", Title: "beta", Content: "content-b"}

		resp, err := setup.api.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{
				Flat:    new(true),
				Page:    new(1),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListWikiPages200JSONResponse)
		require.True(t, ok)
		require.NotNil(t, listResp.Items)
		assert.Len(t, *listResp.Items, 2)
		assert.Equal(t, "Alpha doc", (*listResp.Items)[0].Description)
	})

	t.Run("tags filter is forwarded and tags are returned", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["tagged"] = &wiki.Page{ID: "tagged", Title: "tagged", Tags: []string{"ops", "runbook"}, Content: "body"}

		tags := []string{"ops"}
		resp, err := setup.api.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{
				Flat:    new(true),
				Tags:    &tags,
				Page:    new(1),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListWikiPages200JSONResponse)
		require.True(t, ok)
		assert.Equal(t, []string{"ops"}, setup.store.lastListOpts.Tags)
		require.NotNil(t, listResp.Items)
		require.Len(t, *listResp.Items, 1)
		require.NotNil(t, (*listResp.Items)[0].Tags)
		assert.Equal(t, []string{"ops", "runbook"}, *(*listResp.Items)[0].Tags)
	})

	t.Run("tree mode returns nodes", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc-a"] = &wiki.Page{ID: "doc-a", Title: "doc-a", Content: "aaa"}
		setup.store.pages["doc-b"] = &wiki.Page{ID: "doc-b", Title: "doc-b", Content: "bbb"}

		resp, err := setup.api.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{
				Page:    new(1),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListWikiPages200JSONResponse)
		require.True(t, ok)
		require.NotNil(t, listResp.Tree)
		assert.Len(t, *listResp.Tree, 2)
	})

	t.Run("prefix scopes a named workspace and preserves visible paths", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetupWithWorkspaces(t, "ops")
		setup.store.pages["ops/guides/deploy"] = &wiki.Page{ID: "ops/guides/deploy", Title: "deploy", Content: "deploy"}
		setup.store.pages["ops/runbooks/restart"] = &wiki.Page{ID: "ops/runbooks/restart", Title: "restart", Content: "restart"}
		workspace := apigen.Workspace("ops")
		prefix := apigen.WikiPagePrefix("guides")
		flat := true

		resp, err := setup.api.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{
				Workspace: &workspace,
				Prefix:    &prefix,
				Flat:      &flat,
				Page:      new(1),
				PerPage:   new(10),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListWikiPages200JSONResponse)
		require.True(t, ok)
		require.NotNil(t, listResp.Items)
		require.Len(t, *listResp.Items, 1)
		assert.Equal(t, "guides/deploy", (*listResp.Items)[0].Id)
		require.NotNil(t, (*listResp.Items)[0].Workspace)
		assert.Equal(t, "ops", *(*listResp.Items)[0].Workspace)
		assert.Equal(t, "ops/guides", setup.store.lastListOpts.PathPrefix)
	})

	t.Run("no workspace scope filters known workspace roots before pagination", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetupWithWorkspaces(t, "aaa")
		setup.store.pages["aaa/hidden"] = &wiki.Page{ID: "aaa/hidden", Title: "hidden", Content: "private"}
		setup.store.pages["bbb"] = &wiki.Page{ID: "bbb", Title: "bbb", Content: "public"}
		flat := true
		page := 1
		perPage := 1
		workspace := apigen.Workspace("default")

		resp, err := setup.api.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{
				Workspace: &workspace,
				Flat:      &flat,
				Page:      &page,
				PerPage:   &perPage,
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListWikiPages200JSONResponse)
		require.True(t, ok)
		require.NotNil(t, listResp.Items)
		require.NotNil(t, listResp.Pagination)
		require.Len(t, *listResp.Items, 1)
		assert.Equal(t, "bbb", (*listResp.Items)[0].Id)
		assert.Equal(t, 1, listResp.Pagination.TotalRecords)
		assert.Equal(t, 1, listResp.Pagination.TotalPages)
	})

	t.Run("no workspace scope fails closed when workspace names cannot be loaded", func(t *testing.T) {
		t.Parallel()

		store := &mockWikiStore{pages: make(map[string]*wiki.Page)}
		setup := newWikiPageTestSetupWithStore(t, store, &mockWorkspaceStore{err: errForced})
		workspace := apigen.Workspace("default")

		_, err := setup.api.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{Workspace: &workspace},
		})
		require.Error(t, err)
	})

	t.Run("all scope fails closed when workspace names cannot be loaded", func(t *testing.T) {
		t.Parallel()

		store := &mockWikiStore{pages: make(map[string]*wiki.Page)}
		setup := newWikiPageTestSetupWithStore(t, store, &mockWorkspaceStore{err: errForced})
		workspace := apigen.Workspace("all")

		_, err := setup.api.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{Workspace: &workspace},
		})
		require.Error(t, err)
	})

	t.Run("no Wiki store returns error", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{})
		require.Error(t, err)
	})
}

func TestListWikiPageBacklinks(t *testing.T) {
	t.Parallel()

	t.Run("default scope returns linkers", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["target"] = &wiki.Page{ID: "target", Title: "target", Content: "doc"}
		setup.store.pages["linker"] = &wiki.Page{ID: "linker", Title: "linker", Content: "see [[target]]"}
		setup.store.pages["other"] = &wiki.Page{ID: "other", Title: "other", Content: "nothing"}

		resp, err := setup.api.ListWikiPageBacklinks(adminCtx(), apigen.ListWikiPageBacklinksRequestObject{
			Params: apigen.ListWikiPageBacklinksParams{Target: "target"},
		})
		require.NoError(t, err)

		linksResp, ok := resp.(apigen.ListWikiPageBacklinks200JSONResponse)
		require.True(t, ok)
		require.Len(t, linksResp.Items, 1)
		assert.Equal(t, "linker", linksResp.Items[0].Id)
	})

	t.Run("workspace scope resolves relative links and trims IDs", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetupWithWorkspaces(t, "ops")
		setup.store.pages["ops/guides/target"] = &wiki.Page{ID: "ops/guides/target", Title: "target", Content: "doc"}
		setup.store.pages["ops/runbooks/linker"] = &wiki.Page{ID: "ops/runbooks/linker", Title: "linker", Content: "see [[guides/target]]"}
		setup.store.pages["outside"] = &wiki.Page{ID: "outside", Title: "outside", Content: "see [[ops/guides/target]]"}
		workspace := apigen.Workspace("ops")

		resp, err := setup.api.ListWikiPageBacklinks(adminCtx(), apigen.ListWikiPageBacklinksRequestObject{
			Params: apigen.ListWikiPageBacklinksParams{Target: "guides/target", Workspace: &workspace},
		})
		require.NoError(t, err)

		linksResp, ok := resp.(apigen.ListWikiPageBacklinks200JSONResponse)
		require.True(t, ok)
		require.Len(t, linksResp.Items, 1)
		assert.Equal(t, "runbooks/linker", linksResp.Items[0].Id)
		require.NotNil(t, linksResp.Items[0].Workspace)
		assert.Equal(t, "ops", *linksResp.Items[0].Workspace)
	})

	t.Run("scheme target matches verbatim", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["runbook"] = &wiki.Page{ID: "runbook", Title: "runbook", Content: "status [[dag:daily-etl]]"}

		resp, err := setup.api.ListWikiPageBacklinks(adminCtx(), apigen.ListWikiPageBacklinksRequestObject{
			Params: apigen.ListWikiPageBacklinksParams{Target: "dag:daily-etl"},
		})
		require.NoError(t, err)

		linksResp, ok := resp.(apigen.ListWikiPageBacklinks200JSONResponse)
		require.True(t, ok)
		require.Len(t, linksResp.Items, 1)
		assert.Equal(t, "runbook", linksResp.Items[0].Id)
	})

	t.Run("invalid Wiki page path target is rejected", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		_, err := setup.api.ListWikiPageBacklinks(adminCtx(), apigen.ListWikiPageBacklinksRequestObject{
			Params: apigen.ListWikiPageBacklinksParams{Target: "../escape"},
		})
		require.Error(t, err)
	})
}

func TestWikiPageRevisions(t *testing.T) {
	t.Parallel()

	newSetup := func(t *testing.T) *wikiTestSetup {
		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc"] = &wiki.Page{ID: "doc", Title: "doc", Content: "current"}
		setup.store.revisions = map[string][]wiki.PageRevision{
			"doc": {
				{Rev: "r2", SavedAt: time.Unix(1700000100, 0), Size: 2, Content: "v2"},
				{Rev: "r1", SavedAt: time.Unix(1700000000, 0), Size: 2, Content: "v1"},
			},
		}
		return setup
	}

	t.Run("list returns revisions without content", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		resp, err := setup.api.ListWikiPageRevisions(adminCtx(), apigen.ListWikiPageRevisionsRequestObject{
			Params: apigen.ListWikiPageRevisionsParams{Path: "doc"},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListWikiPageRevisions200JSONResponse)
		require.True(t, ok)
		require.Len(t, listResp.Revisions, 2)
		assert.Equal(t, "r2", listResp.Revisions[0].Rev)
		assert.Nil(t, listResp.Revisions[0].Content)
	})

	t.Run("get returns revision content", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		resp, err := setup.api.GetWikiPageRevision(adminCtx(), apigen.GetWikiPageRevisionRequestObject{
			Params: apigen.GetWikiPageRevisionParams{Path: "doc", Rev: "r1"},
		})
		require.NoError(t, err)

		revResp, ok := resp.(apigen.GetWikiPageRevision200JSONResponse)
		require.True(t, ok)
		require.NotNil(t, revResp.Content)
		assert.Equal(t, "v1", *revResp.Content)
	})

	t.Run("unknown revision returns not found", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		_, err := setup.api.GetWikiPageRevision(adminCtx(), apigen.GetWikiPageRevisionRequestObject{
			Params: apigen.GetWikiPageRevisionParams{Path: "doc", Rev: "missing"},
		})
		require.Error(t, err)
	})

	t.Run("unknown Wiki page returns not found", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		_, err := setup.api.ListWikiPageRevisions(adminCtx(), apigen.ListWikiPageRevisionsRequestObject{
			Params: apigen.ListWikiPageRevisionsParams{Path: "missing"},
		})
		require.Error(t, err)
	})
}

func TestGetWikiPageTreeDataRejectsMalformedQuery(t *testing.T) {
	t.Parallel()

	setup := newWikiPageTestSetup(t)
	_, err := setup.api.GetWikiPageTreeData(adminCtx(), "page=%zz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid page tree query")
}

func TestGetWikiPageTreeDataSupportsPrefix(t *testing.T) {
	t.Parallel()

	setup := newWikiPageTestSetup(t)
	setup.store.pages["guides/deploy"] = &wiki.Page{ID: "guides/deploy", Title: "deploy", Content: "deploy"}
	setup.store.pages["runbooks/restart"] = &wiki.Page{ID: "runbooks/restart", Title: "restart", Content: "restart"}

	data, err := setup.api.GetWikiPageTreeData(adminCtx(), "prefix=guides&page=1&perPage=10")
	require.NoError(t, err)
	resp, ok := data.(apigen.ListWikiPages200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, resp.Tree)
	require.Len(t, *resp.Tree, 1)
	assert.Equal(t, "guides/deploy", (*resp.Tree)[0].Id)
	assert.Equal(t, "guides", setup.store.lastListOpts.PathPrefix)
}

func TestListWikiPagesSortParamsForwarded(t *testing.T) {
	t.Parallel()

	t.Run("explicit sort params forwarded to store", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc1"] = &wiki.Page{ID: "doc1", Title: "doc1", Content: "c"}

		sortParam := apigen.ListWikiPagesParamsSortMtime
		orderParam := apigen.ListWikiPagesParamsOrderDesc

		_, err := setup.api.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{
				Page:    new(1),
				PerPage: new(10),
				Sort:    &sortParam,
				Order:   &orderParam,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, wiki.PageSortFieldMTime, setup.store.lastListOpts.Sort)
		assert.Equal(t, wiki.PageSortOrderDesc, setup.store.lastListOpts.Order)
	})

	t.Run("defaults to type asc when omitted", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc1"] = &wiki.Page{ID: "doc1", Title: "doc1", Content: "c"}

		_, err := setup.api.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{
				Page:    new(1),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)
		assert.Equal(t, wiki.PageSortFieldType, setup.store.lastListOpts.Sort)
		assert.Equal(t, wiki.PageSortOrderAsc, setup.store.lastListOpts.Order)
	})

	t.Run("flat mode forwards sort params", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc1"] = &wiki.Page{ID: "doc1", Title: "doc1", Content: "c"}

		sortParam := apigen.ListWikiPagesParamsSortName
		orderParam := apigen.ListWikiPagesParamsOrderDesc

		_, err := setup.api.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{
				Flat:    new(true),
				Page:    new(1),
				PerPage: new(10),
				Sort:    &sortParam,
				Order:   &orderParam,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, wiki.PageSortFieldName, setup.store.lastListOpts.Sort)
		assert.Equal(t, wiki.PageSortOrderDesc, setup.store.lastListOpts.Order)
	})
}

func TestWikiPageMutationsNotify(t *testing.T) {
	store := &mockWikiStore{pages: make(map[string]*wiki.Page)}
	var notifications int
	setup := newWikiPageTestSetupWithStoreOptions(t, store, nil, apiv1.WithWikiMutationNotifier(func() {
		notifications++
	}))

	_, err := setup.api.CreateWikiPage(adminCtx(), apigen.CreateWikiPageRequestObject{
		Body: &apigen.CreateWikiPageJSONRequestBody{Id: "doc1", Content: "created"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, notifications)

	_, err = setup.api.UpdateWikiPage(adminCtx(), apigen.UpdateWikiPageRequestObject{
		Params: apigen.UpdateWikiPageParams{Path: "doc1"},
		Body:   &apigen.UpdateWikiPageJSONRequestBody{Content: "updated"},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, notifications)

	_, err = setup.api.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
		Params: apigen.RenameWikiPageParams{Path: "doc1"},
		Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "doc2"},
	})
	require.NoError(t, err)
	assert.Equal(t, 3, notifications)

	_, err = setup.api.DeleteWikiPage(adminCtx(), apigen.DeleteWikiPageRequestObject{
		Params: apigen.DeleteWikiPageParams{Path: "doc2"},
	})
	require.NoError(t, err)
	assert.Equal(t, 4, notifications)

	// Attachment uploads change neither content nor the tree, so they must
	// not fan out Wiki page invalidations.
	_, err = setup.api.CreateWikiPage(adminCtx(), apigen.CreateWikiPageRequestObject{
		Body: &apigen.CreateWikiPageJSONRequestBody{Id: "doc3", Content: "created"},
	})
	require.NoError(t, err)
	require.Equal(t, 5, notifications)
	_, err = setup.api.UploadWikiPageAttachment(adminCtx(), apigen.UploadWikiPageAttachmentRequestObject{
		Params: apigen.UploadWikiPageAttachmentParams{Path: "doc3", Name: "logo.png"},
		Body:   strings.NewReader("png-bytes"),
	})
	require.NoError(t, err)
	assert.Equal(t, 5, notifications)
}

func TestWikiPageAttachments(t *testing.T) {
	t.Parallel()

	newSetup := func(t *testing.T) *wikiTestSetup {
		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc"] = &wiki.Page{ID: "doc", Title: "doc", Content: "body"}
		return setup
	}

	t.Run("upload and download round-trip", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		resp, err := setup.api.UploadWikiPageAttachment(adminCtx(), apigen.UploadWikiPageAttachmentRequestObject{
			Params: apigen.UploadWikiPageAttachmentParams{Path: "doc", Name: "logo.png"},
			Body:   strings.NewReader("png-bytes"),
		})
		require.NoError(t, err)
		uploadResp, ok := resp.(apigen.UploadWikiPageAttachment201JSONResponse)
		require.True(t, ok)
		assert.Equal(t, "logo.png", uploadResp.Name)
		assert.Equal(t, int64(len("png-bytes")), uploadResp.Size)

		dlResp, err := setup.api.DownloadWikiPageAttachment(adminCtx(), apigen.DownloadWikiPageAttachmentRequestObject{
			Params: apigen.DownloadWikiPageAttachmentParams{Path: "doc", Name: "logo.png"},
		})
		require.NoError(t, err)
		stream, ok := dlResp.(apigen.DownloadWikiPageAttachment200ApplicationoctetStreamResponse)
		require.True(t, ok)
		data, err := io.ReadAll(stream.Body)
		require.NoError(t, err)
		assert.Equal(t, "png-bytes", string(data))
		assert.Contains(t, stream.Headers.ContentDisposition, "logo.png")
	})

	t.Run("oversized upload is rejected", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		_, err := setup.api.UploadWikiPageAttachment(adminCtx(), apigen.UploadWikiPageAttachmentRequestObject{
			Params: apigen.UploadWikiPageAttachmentParams{Path: "doc", Name: "big.bin"},
			Body:   bytes.NewReader(make([]byte, 10<<20+1)),
		})
		require.Error(t, err)
		var apiErr *apiv1.Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, apigen.ErrorCodePayloadTooLarge, apiErr.Code)
		assert.Equal(t, http.StatusRequestEntityTooLarge, apiErr.HTTPStatus)
	})

	t.Run("invalid name is rejected", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		_, err := setup.api.UploadWikiPageAttachment(adminCtx(), apigen.UploadWikiPageAttachmentRequestObject{
			Params: apigen.UploadWikiPageAttachmentParams{Path: "doc", Name: "../escape"},
			Body:   strings.NewReader("x"),
		})
		require.Error(t, err)
	})

	t.Run("unknown attachment returns not found", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		_, err := setup.api.DownloadWikiPageAttachment(adminCtx(), apigen.DownloadWikiPageAttachmentRequestObject{
			Params: apigen.DownloadWikiPageAttachmentParams{Path: "doc", Name: "missing.png"},
		})
		require.Error(t, err)
	})
}

func TestCreateWikiPage(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		resp, err := setup.api.CreateWikiPage(adminCtx(), apigen.CreateWikiPageRequestObject{
			Body: &apigen.CreateWikiPageJSONRequestBody{
				Id:      "test-doc",
				Content: "hello",
			},
		})
		require.NoError(t, err)

		_, ok := resp.(apigen.CreateWikiPage201JSONResponse)
		require.True(t, ok)

		// Verify stored
		_, exists := setup.store.pages["test-doc"]
		assert.True(t, exists)
	})

	t.Run("invalid ID", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.CreateWikiPage(adminCtx(), apigen.CreateWikiPageRequestObject{
			Body: &apigen.CreateWikiPageJSONRequestBody{
				Id:      "..bad",
				Content: "x",
			},
		})
		require.Error(t, err)
	})

	t.Run("already exists", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["existing"] = &wiki.Page{ID: "existing", Title: "existing", Content: "old"}

		_, err := setup.api.CreateWikiPage(adminCtx(), apigen.CreateWikiPageRequestObject{
			Body: &apigen.CreateWikiPageJSONRequestBody{
				Id:      "existing",
				Content: "new",
			},
		})
		require.Error(t, err)
	})

	t.Run("omitted workspace rejects known workspace-prefixed path", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetupWithWorkspaces(t, "ops")

		_, err := setup.api.CreateWikiPage(adminCtx(), apigen.CreateWikiPageRequestObject{
			Body: &apigen.CreateWikiPageJSONRequestBody{
				Id:      "ops/doc",
				Content: "private",
			},
		})
		require.Error(t, err)
		var apiErr *apiv1.Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		assert.NotContains(t, setup.store.pages, "ops/doc")
	})

	t.Run("nil body", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.CreateWikiPage(adminCtx(), apigen.CreateWikiPageRequestObject{
			Body: nil,
		})
		require.Error(t, err)
	})

	t.Run("no Wiki store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.CreateWikiPage(adminCtx(), apigen.CreateWikiPageRequestObject{
			Body: &apigen.CreateWikiPageJSONRequestBody{
				Id:      "test",
				Content: "hello",
			},
		})
		require.Error(t, err)
	})
}

func TestGetWikiPage(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["my-doc"] = &wiki.Page{ID: "my-doc", Title: "my-doc", Description: "My doc description", Content: "hello"}

		resp, err := setup.api.GetWikiPage(adminCtx(), apigen.GetWikiPageRequestObject{
			Params: apigen.GetWikiPageParams{Path: "my-doc"},
		})
		require.NoError(t, err)

		getResp, ok := resp.(apigen.GetWikiPage200JSONResponse)
		require.True(t, ok)
		assert.Equal(t, "my-doc", getResp.Id)
		assert.Equal(t, "hello", getResp.Content)
		assert.Equal(t, "my-doc", getResp.Title)
		assert.Equal(t, "My doc description", getResp.Description)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.GetWikiPage(adminCtx(), apigen.GetWikiPageRequestObject{
			Params: apigen.GetWikiPageParams{Path: "nonexistent"},
		})
		require.Error(t, err)
	})

	t.Run("invalid path", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.GetWikiPage(adminCtx(), apigen.GetWikiPageRequestObject{
			Params: apigen.GetWikiPageParams{Path: "..bad"},
		})
		require.Error(t, err)
	})

	t.Run("no Wiki store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.GetWikiPage(adminCtx(), apigen.GetWikiPageRequestObject{
			Params: apigen.GetWikiPageParams{Path: "my-doc"},
		})
		require.Error(t, err)
	})
}

func TestUpdateWikiPage(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc1"] = &wiki.Page{ID: "doc1", Title: "doc1", Content: "original"}

		resp, err := setup.api.UpdateWikiPage(adminCtx(), apigen.UpdateWikiPageRequestObject{
			Params: apigen.UpdateWikiPageParams{Path: "doc1"},
			Body:   &apigen.UpdateWikiPageJSONRequestBody{Content: "updated"},
		})
		require.NoError(t, err)

		_, ok := resp.(apigen.UpdateWikiPage200JSONResponse)
		require.True(t, ok)

		// Verify store content changed
		assert.Equal(t, "updated", setup.store.pages["doc1"].Content)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.UpdateWikiPage(adminCtx(), apigen.UpdateWikiPageRequestObject{
			Params: apigen.UpdateWikiPageParams{Path: "nonexistent"},
			Body:   &apigen.UpdateWikiPageJSONRequestBody{Content: "updated"},
		})
		require.Error(t, err)
	})

	t.Run("invalid path", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.UpdateWikiPage(adminCtx(), apigen.UpdateWikiPageRequestObject{
			Params: apigen.UpdateWikiPageParams{Path: "..bad"},
			Body:   &apigen.UpdateWikiPageJSONRequestBody{Content: "updated"},
		})
		require.Error(t, err)
	})

	t.Run("nil body", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.UpdateWikiPage(adminCtx(), apigen.UpdateWikiPageRequestObject{
			Params: apigen.UpdateWikiPageParams{Path: "doc1"},
			Body:   nil,
		})
		require.Error(t, err)
	})

	t.Run("no Wiki store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.UpdateWikiPage(adminCtx(), apigen.UpdateWikiPageRequestObject{
			Params: apigen.UpdateWikiPageParams{Path: "doc1"},
			Body:   &apigen.UpdateWikiPageJSONRequestBody{Content: "updated"},
		})
		require.Error(t, err)
	})
}

func TestDeleteWikiPage(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc1"] = &wiki.Page{ID: "doc1", Title: "doc1", Content: "content"}

		resp, err := setup.api.DeleteWikiPage(adminCtx(), apigen.DeleteWikiPageRequestObject{
			Params: apigen.DeleteWikiPageParams{Path: "doc1"},
		})
		require.NoError(t, err)

		_, ok := resp.(apigen.DeleteWikiPage204Response)
		require.True(t, ok)

		// Verify removed from store
		_, exists := setup.store.pages["doc1"]
		assert.False(t, exists)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.DeleteWikiPage(adminCtx(), apigen.DeleteWikiPageRequestObject{
			Params: apigen.DeleteWikiPageParams{Path: "nonexistent"},
		})
		require.Error(t, err)
	})

	t.Run("invalid path", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.DeleteWikiPage(adminCtx(), apigen.DeleteWikiPageRequestObject{
			Params: apigen.DeleteWikiPageParams{Path: "..bad"},
		})
		require.Error(t, err)
	})

	t.Run("no Wiki store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.DeleteWikiPage(adminCtx(), apigen.DeleteWikiPageRequestObject{
			Params: apigen.DeleteWikiPageParams{Path: "doc1"},
		})
		require.Error(t, err)
	})
}

func TestRenameWikiPage(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["old-doc"] = &wiki.Page{ID: "old-doc", Title: "old-doc", Content: "content"}

		resp, err := setup.api.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "old-doc"},
			Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "new-doc"},
		})
		require.NoError(t, err)

		_, ok := resp.(apigen.RenameWikiPage200JSONResponse)
		require.True(t, ok)

		// Verify store has new-doc, not old-doc
		_, oldExists := setup.store.pages["old-doc"]
		assert.False(t, oldExists)
		_, newExists := setup.store.pages["new-doc"]
		assert.True(t, newExists)
	})

	t.Run("source not found", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "nonexistent"},
			Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "new"},
		})
		require.Error(t, err)
	})

	t.Run("target exists", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["a"] = &wiki.Page{ID: "a", Title: "a", Content: "aaa"}
		setup.store.pages["b"] = &wiki.Page{ID: "b", Title: "b", Content: "bbb"}

		_, err := setup.api.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "a"},
			Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "b"},
		})
		require.Error(t, err)
	})

	t.Run("invalid source path", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "..bad"},
			Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "good"},
		})
		require.Error(t, err)
	})

	t.Run("invalid new path", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["good"] = &wiki.Page{ID: "good", Title: "good", Content: "content"}

		_, err := setup.api.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "good"},
			Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "..bad"},
		})
		require.Error(t, err)
	})

	t.Run("nil body", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "old"},
			Body:   nil,
		})
		require.Error(t, err)
	})

	t.Run("directory rename success", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["folder/doc1"] = &wiki.Page{ID: "folder/doc1", Title: "doc1", Content: "c1"}
		setup.store.pages["folder/doc2"] = &wiki.Page{ID: "folder/doc2", Title: "doc2", Content: "c2"}

		resp, err := setup.api.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "folder"},
			Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "moved"},
		})
		require.NoError(t, err)

		_, ok := resp.(apigen.RenameWikiPage200JSONResponse)
		require.True(t, ok)

		_, oldExists := setup.store.pages["folder/doc1"]
		assert.False(t, oldExists)
		_, newExists := setup.store.pages["moved/doc1"]
		assert.True(t, newExists)
		_, newExists2 := setup.store.pages["moved/doc2"]
		assert.True(t, newExists2)
	})

	t.Run("directory rename target exists", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["src/doc"] = &wiki.Page{ID: "src/doc", Title: "doc", Content: "c1"}
		setup.store.pages["dst/doc"] = &wiki.Page{ID: "dst/doc", Title: "doc", Content: "c2"}

		_, err := setup.api.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "src"},
			Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "dst"},
		})
		require.Error(t, err)
	})

	t.Run("directory rename into own subtree", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["guides/intro/start"] = &wiki.Page{
			ID: "guides/intro/start", Title: "start", Content: "start content",
		}
		setup.store.pages["guides/reference"] = &wiki.Page{
			ID: "guides/reference", Title: "reference", Content: "reference content",
		}

		_, err := setup.api.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "guides"},
			Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "guides/intro/guides"},
		})
		var apiErr *apiv1.Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.HTTPStatus)
		assert.Equal(t, "start content", setup.store.pages["guides/intro/start"].Content)
		assert.Equal(t, "reference content", setup.store.pages["guides/reference"].Content)
		assert.Len(t, setup.store.pages, 2)
	})

	t.Run("directory not found", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "nonexistent-dir"},
			Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "target"},
		})
		require.Error(t, err)
	})

	t.Run("no Wiki store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "old"},
			Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "new"},
		})
		require.Error(t, err)
	})
}

func TestListWikiPagesTreeWithChildren(t *testing.T) {
	t.Parallel()

	t.Run("tree nodes with children are rendered", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		// Directly put a tree node with children in the store mock.
		// We override List to return a node with children.
		setup.store.pages["parent/child1"] = &wiki.Page{ID: "parent/child1", Title: "child1", Content: "c1"}
		setup.store.pages["parent/child2"] = &wiki.Page{ID: "parent/child2", Title: "child2", Content: "c2"}

		// Replace the store with one that returns a directory structure.
		dirStore := &mockWikiStoreWithTree{
			mockWikiStore: setup.store,
		}
		cfg := &config.Config{}
		cfg.Server.Permissions = map[config.Permission]bool{
			config.PermissionWriteDAGs: true,
		}
		a := apiv1.New(
			nil, nil, nil, nil, runtime.Manager{},
			cfg, nil, nil,
			prometheus.NewRegistry(),
			nil,
			apiv1.WithWikiStore(dirStore),
		)

		resp, err := a.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{
				Page:    new(1),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListWikiPages200JSONResponse)
		require.True(t, ok)
		require.NotNil(t, listResp.Tree)
		require.Len(t, *listResp.Tree, 1)

		parent := (*listResp.Tree)[0]
		assert.Equal(t, "directory", string(parent.Type))
		require.NotNil(t, parent.Children)
		assert.Len(t, *parent.Children, 2)
	})
}

// mockWikiStoreWithTree wraps mockWikiStore but returns a directory tree from List.
type mockWikiStoreWithTree struct {
	*mockWikiStore
}

func (m *mockWikiStoreWithTree) List(_ context.Context, opts wiki.ListPagesOptions) (*pagination.PaginatedResult[*wiki.PageTreeNode], error) {
	nodes := []*wiki.PageTreeNode{
		{
			ID:   "parent",
			Name: "parent",
			Type: "directory",
			Children: []*wiki.PageTreeNode{
				{ID: "parent/child1", Name: "child1", Title: "child1", Type: "file"},
				{ID: "parent/child2", Name: "child2", Title: "child2", Type: "file"},
			},
		},
	}
	filtered := nodes[:0]
	for _, node := range nodes {
		if !mockWikiPagePathRootExcluded(node.ID, opts.ExcludePathRoots) {
			filtered = append(filtered, node)
		}
	}
	pg := pagination.NewPaginator(opts.Page, opts.PerPage)
	start := min(pg.Offset(), len(filtered))
	end := min(start+pg.Limit(), len(filtered))
	result := pagination.NewPaginatedResult(filtered[start:end], len(filtered), pg)
	return &result, nil
}

func TestGetWikiPageContentData(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc1"] = &wiki.Page{ID: "doc1", Title: "doc1", Content: "hello"}

		resp, err := setup.api.GetWikiPageContentData(adminCtx(), "doc1")
		require.NoError(t, err)

		wikiPageResp, ok := resp.(apigen.WikiPageResponse)
		require.True(t, ok)
		assert.Equal(t, "doc1", wikiPageResp.Id)
		assert.Equal(t, "hello", wikiPageResp.Content)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.GetWikiPageContentData(adminCtx(), "nonexistent")
		var apiErr *apiv1.Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.HTTPStatus)
		assert.Equal(t, apigen.ErrorCodeNotFound, apiErr.Code)
	})

	t.Run("no Wiki store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.GetWikiPageContentData(adminCtx(), "doc1")
		require.Error(t, err)
	})
}

// TestWikiStoreInternalErrors covers error paths where the store returns
// unexpected (non-sentinel) errors, triggering the internalError() paths.
func TestWikiStoreInternalErrors(t *testing.T) {
	t.Parallel()

	newFailSetup := func(t *testing.T) *wikiTestSetup {
		t.Helper()
		s := newWikiPageTestSetup(t)
		s.store.failAll = true
		return s
	}

	t.Run("ListWikiPages flat store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{Flat: new(true), Page: new(1), PerPage: new(10)},
		})
		require.Error(t, err)
	})

	t.Run("ListWikiPages tree store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.ListWikiPages(adminCtx(), apigen.ListWikiPagesRequestObject{
			Params: apigen.ListWikiPagesParams{Page: new(1), PerPage: new(10)},
		})
		require.Error(t, err)
	})

	t.Run("CreateWikiPage store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.CreateWikiPage(adminCtx(), apigen.CreateWikiPageRequestObject{
			Body: &apigen.CreateWikiPageJSONRequestBody{Id: "test", Content: "hello"},
		})
		require.Error(t, err)
	})

	t.Run("GetWikiPage store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.GetWikiPage(adminCtx(), apigen.GetWikiPageRequestObject{
			Params: apigen.GetWikiPageParams{Path: "test"},
		})
		require.Error(t, err)
	})

	t.Run("SearchWikiPages store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.SearchWikiPages(adminCtx(), apigen.SearchWikiPagesRequestObject{
			Params: apigen.SearchWikiPagesParams{Q: "hello"},
		})
		require.Error(t, err)
	})

	t.Run("UpdateWikiPage store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.UpdateWikiPage(adminCtx(), apigen.UpdateWikiPageRequestObject{
			Params: apigen.UpdateWikiPageParams{Path: "test"},
			Body:   &apigen.UpdateWikiPageJSONRequestBody{Content: "new"},
		})
		require.Error(t, err)
	})

	t.Run("DeleteWikiPage store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.DeleteWikiPage(adminCtx(), apigen.DeleteWikiPageRequestObject{
			Params: apigen.DeleteWikiPageParams{Path: "test"},
		})
		require.Error(t, err)
	})

	t.Run("RenameWikiPage store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "old"},
			Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "new"},
		})
		require.Error(t, err)
	})
}

// TestWikiWritePermissionDenied covers the requireDAGWrite error path
// when PermissionWriteDAGs is not set.
func TestWikiWritePermissionDenied(t *testing.T) {
	t.Parallel()

	newNoWriteSetup := func(t *testing.T) *apiv1.API {
		t.Helper()
		store := &mockWikiStore{pages: make(map[string]*wiki.Page)}
		cfg := &config.Config{}
		// Permissions map exists but write is false.
		cfg.Server.Permissions = map[config.Permission]bool{
			config.PermissionWriteDAGs: false,
		}
		return apiv1.New(
			nil, nil, nil, nil, runtime.Manager{},
			cfg, nil, nil,
			prometheus.NewRegistry(),
			nil,
			apiv1.WithWikiStore(store),
		)
	}

	t.Run("CreateWikiPage denied", func(t *testing.T) {
		t.Parallel()
		a := newNoWriteSetup(t)
		_, err := a.CreateWikiPage(adminCtx(), apigen.CreateWikiPageRequestObject{
			Body: &apigen.CreateWikiPageJSONRequestBody{Id: "test", Content: "hello"},
		})
		require.Error(t, err)
	})

	t.Run("UpdateWikiPage denied", func(t *testing.T) {
		t.Parallel()
		a := newNoWriteSetup(t)
		_, err := a.UpdateWikiPage(adminCtx(), apigen.UpdateWikiPageRequestObject{
			Params: apigen.UpdateWikiPageParams{Path: "test"},
			Body:   &apigen.UpdateWikiPageJSONRequestBody{Content: "new"},
		})
		require.Error(t, err)
	})

	t.Run("UploadWikiPageAttachment denied", func(t *testing.T) {
		t.Parallel()
		a := newNoWriteSetup(t)
		_, err := a.UploadWikiPageAttachment(adminCtx(), apigen.UploadWikiPageAttachmentRequestObject{
			Params: apigen.UploadWikiPageAttachmentParams{Path: "test", Name: "logo.png"},
			Body:   strings.NewReader("x"),
		})
		require.Error(t, err)
	})

	t.Run("DeleteWikiPage denied", func(t *testing.T) {
		t.Parallel()
		a := newNoWriteSetup(t)
		_, err := a.DeleteWikiPage(adminCtx(), apigen.DeleteWikiPageRequestObject{
			Params: apigen.DeleteWikiPageParams{Path: "test"},
		})
		require.Error(t, err)
	})

	t.Run("RenameWikiPage denied", func(t *testing.T) {
		t.Parallel()
		a := newNoWriteSetup(t)
		_, err := a.RenameWikiPage(adminCtx(), apigen.RenameWikiPageRequestObject{
			Params: apigen.RenameWikiPageParams{Path: "old"},
			Body:   &apigen.RenameWikiPageJSONRequestBody{NewPath: "new"},
		})
		require.Error(t, err)
	})

	t.Run("DeleteWikiPageBatch denied", func(t *testing.T) {
		t.Parallel()
		a := newNoWriteSetup(t)
		_, err := a.DeleteWikiPageBatch(adminCtx(), apigen.DeleteWikiPageBatchRequestObject{
			Body: &apigen.DeleteWikiPageBatchJSONRequestBody{Paths: []string{"test"}},
		})
		require.Error(t, err)
	})
}

func TestDeleteWikiPageBatch(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc1"] = &wiki.Page{ID: "doc1", Title: "doc1", Content: "c1"}
		setup.store.pages["doc2"] = &wiki.Page{ID: "doc2", Title: "doc2", Content: "c2"}

		resp, err := setup.api.DeleteWikiPageBatch(adminCtx(), apigen.DeleteWikiPageBatchRequestObject{
			Body: &apigen.DeleteWikiPageBatchJSONRequestBody{Paths: []string{"doc1", "doc2"}},
		})
		require.NoError(t, err)

		batchResp, ok := resp.(apigen.DeleteWikiPageBatch200JSONResponse)
		require.True(t, ok)
		assert.Len(t, batchResp.Deleted, 2)
		assert.Empty(t, batchResp.Failed)
		assert.Equal(t, 0, len(setup.store.pages))
	})

	t.Run("partial failure", func(t *testing.T) {
		t.Parallel()
		setup := newWikiPageTestSetup(t)
		setup.store.pages["valid"] = &wiki.Page{ID: "valid", Title: "valid", Content: "c"}

		resp, err := setup.api.DeleteWikiPageBatch(adminCtx(), apigen.DeleteWikiPageBatchRequestObject{
			Body: &apigen.DeleteWikiPageBatchJSONRequestBody{Paths: []string{"valid", "nonexistent"}},
		})
		require.NoError(t, err)

		batchResp, ok := resp.(apigen.DeleteWikiPageBatch200JSONResponse)
		require.True(t, ok)
		assert.Len(t, batchResp.Deleted, 2) // nonexistent treated as success
		assert.Empty(t, batchResp.Failed)
	})

	t.Run("directory delete", func(t *testing.T) {
		t.Parallel()
		setup := newWikiPageTestSetup(t)
		setup.store.pages["dir/child1"] = &wiki.Page{ID: "dir/child1", Title: "child1", Content: "c1"}
		setup.store.pages["dir/child2"] = &wiki.Page{ID: "dir/child2", Title: "child2", Content: "c2"}

		resp, err := setup.api.DeleteWikiPageBatch(adminCtx(), apigen.DeleteWikiPageBatchRequestObject{
			Body: &apigen.DeleteWikiPageBatchJSONRequestBody{Paths: []string{"dir"}},
		})
		require.NoError(t, err)

		batchResp, ok := resp.(apigen.DeleteWikiPageBatch200JSONResponse)
		require.True(t, ok)
		assert.Len(t, batchResp.Deleted, 1)
		assert.Empty(t, batchResp.Failed)
		assert.Equal(t, 0, len(setup.store.pages))
	})

	t.Run("nil body", func(t *testing.T) {
		t.Parallel()
		setup := newWikiPageTestSetup(t)
		_, err := setup.api.DeleteWikiPageBatch(adminCtx(), apigen.DeleteWikiPageBatchRequestObject{Body: nil})
		require.Error(t, err)
	})

	t.Run("empty paths", func(t *testing.T) {
		t.Parallel()
		setup := newWikiPageTestSetup(t)
		_, err := setup.api.DeleteWikiPageBatch(adminCtx(), apigen.DeleteWikiPageBatchRequestObject{
			Body: &apigen.DeleteWikiPageBatchJSONRequestBody{Paths: []string{}},
		})
		require.Error(t, err)
	})

	t.Run("invalid path", func(t *testing.T) {
		t.Parallel()
		setup := newWikiPageTestSetup(t)
		_, err := setup.api.DeleteWikiPageBatch(adminCtx(), apigen.DeleteWikiPageBatchRequestObject{
			Body: &apigen.DeleteWikiPageBatchJSONRequestBody{Paths: []string{"..bad"}},
		})
		require.Error(t, err)
	})

	t.Run("no Wiki store", func(t *testing.T) {
		t.Parallel()
		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)
		_, err := a.DeleteWikiPageBatch(adminCtx(), apigen.DeleteWikiPageBatchRequestObject{
			Body: &apigen.DeleteWikiPageBatchJSONRequestBody{Paths: []string{"test"}},
		})
		require.Error(t, err)
	})

	t.Run("store error", func(t *testing.T) {
		t.Parallel()
		setup := newWikiPageTestSetup(t)
		setup.store.failAll = true
		_, err := setup.api.DeleteWikiPageBatch(adminCtx(), apigen.DeleteWikiPageBatchRequestObject{
			Body: &apigen.DeleteWikiPageBatchJSONRequestBody{Paths: []string{"test"}},
		})
		require.Error(t, err)
	})
}
