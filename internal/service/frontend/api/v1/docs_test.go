// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"errors"
	"net/http"
	"path"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	apigen "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	workspacepkg "github.com/dagucloud/dagu/internal/workspace"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errForced is a generic error used to trigger internal error paths in the mock.
var errForced = errors.New("forced error")

// mockDocStore is an in-memory implementation of agent.DocStore.
var _ agent.DocStore = (*mockDocStore)(nil)

type mockDocStore struct {
	docs         map[string]*agent.Doc
	failAll      bool // when true, all operations return errForced
	lastListOpts agent.ListDocsOptions
}

type mockWorkspaceStore struct {
	workspaces []*workspacepkg.Workspace
	err        error
}

func (m *mockWorkspaceStore) Create(context.Context, *workspacepkg.Workspace) error {
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

func (m *mockWorkspaceStore) Update(context.Context, *workspacepkg.Workspace) error {
	return nil
}

func (m *mockWorkspaceStore) Delete(context.Context, string) error {
	return nil
}

func (m *mockDocStore) Get(_ context.Context, id string) (*agent.Doc, error) {
	if m.failAll {
		return nil, errForced
	}
	if err := agent.ValidateDocID(id); err != nil {
		return nil, agent.ErrInvalidDocID
	}
	doc, ok := m.docs[id]
	if !ok {
		return nil, agent.ErrDocNotFound
	}
	cp := *doc
	return &cp, nil
}

func (m *mockDocStore) Create(_ context.Context, id, content string) error {
	if m.failAll {
		return errForced
	}
	if err := agent.ValidateDocID(id); err != nil {
		return agent.ErrInvalidDocID
	}
	if _, exists := m.docs[id]; exists {
		return agent.ErrDocAlreadyExists
	}
	m.docs[id] = &agent.Doc{
		ID:      id,
		Title:   path.Base(id),
		Content: content,
	}
	return nil
}

func (m *mockDocStore) Update(_ context.Context, id, content string) error {
	if m.failAll {
		return errForced
	}
	if err := agent.ValidateDocID(id); err != nil {
		return agent.ErrInvalidDocID
	}
	doc, ok := m.docs[id]
	if !ok {
		return agent.ErrDocNotFound
	}
	doc.Content = content
	return nil
}

func (m *mockDocStore) Delete(_ context.Context, id string) error {
	if m.failAll {
		return errForced
	}
	if err := agent.ValidateDocID(id); err != nil {
		return agent.ErrInvalidDocID
	}
	if _, ok := m.docs[id]; !ok {
		return agent.ErrDocNotFound
	}
	delete(m.docs, id)
	return nil
}

func (m *mockDocStore) Rename(_ context.Context, oldID, newID string) error {
	if m.failAll {
		return errForced
	}
	if err := agent.ValidateDocID(oldID); err != nil {
		return agent.ErrInvalidDocID
	}
	if err := agent.ValidateDocID(newID); err != nil {
		return agent.ErrInvalidDocID
	}

	// Try exact match first (file rename).
	if doc, ok := m.docs[oldID]; ok {
		if _, exists := m.docs[newID]; exists {
			return agent.ErrDocAlreadyExists
		}
		delete(m.docs, oldID)
		doc.ID = newID
		doc.Title = path.Base(newID)
		m.docs[newID] = doc
		return nil
	}

	// Try prefix match (directory rename).
	prefix := oldID + "/"
	var toMove []string
	for id := range m.docs {
		if strings.HasPrefix(id, prefix) {
			toMove = append(toMove, id)
		}
	}
	if len(toMove) == 0 {
		return agent.ErrDocNotFound
	}

	// Check target prefix doesn't conflict.
	newPrefix := newID + "/"
	for id := range m.docs {
		if strings.HasPrefix(id, newPrefix) || id == newID {
			return agent.ErrDocAlreadyExists
		}
	}

	// Move all matching docs.
	for _, id := range toMove {
		doc := m.docs[id]
		delete(m.docs, id)
		newDocID := newID + strings.TrimPrefix(id, oldID)
		doc.ID = newDocID
		doc.Title = path.Base(newDocID)
		m.docs[newDocID] = doc
	}
	return nil
}

func (m *mockDocStore) DeleteBatch(_ context.Context, ids []string) ([]string, []agent.DeleteError, error) {
	if m.failAll {
		return nil, nil, errForced
	}
	var deleted []string
	var failed []agent.DeleteError
	for _, id := range ids {
		if err := agent.ValidateDocID(id); err != nil {
			failed = append(failed, agent.DeleteError{ID: id, Error: err.Error()})
			continue
		}
		// Try exact match (file).
		if _, ok := m.docs[id]; ok {
			delete(m.docs, id)
			deleted = append(deleted, id)
			continue
		}
		// Try prefix match (directory).
		prefix := id + "/"
		found := false
		for docID := range m.docs {
			if strings.HasPrefix(docID, prefix) {
				delete(m.docs, docID)
				found = true
			}
		}
		if found {
			deleted = append(deleted, id)
		} else {
			// Not found = success (idempotency).
			deleted = append(deleted, id)
		}
	}
	return deleted, failed, nil
}

func (m *mockDocStore) Search(_ context.Context, query string) ([]*agent.DocSearchResult, error) {
	if m.failAll {
		return nil, errForced
	}
	var results []*agent.DocSearchResult
	for _, doc := range m.docs {
		if strings.Contains(doc.Content, query) {
			// Build matches from content lines containing the query.
			var matches []*exec.Match
			for i, line := range strings.Split(doc.Content, "\n") {
				if strings.Contains(line, query) {
					matches = append(matches, &exec.Match{
						Line:       line,
						LineNumber: i + 1,
						StartLine:  i + 1,
					})
				}
			}
			results = append(results, &agent.DocSearchResult{
				ID:          doc.ID,
				Title:       doc.Title,
				Description: doc.Description,
				Matches:     matches,
			})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results, nil
}

type mockDocSearchCursor struct {
	Version int    `json:"v"`
	Query   string `json:"q"`
	ID      string `json:"id,omitempty"`
}

type mockDocMatchCursor struct {
	Version int    `json:"v"`
	Query   string `json:"q"`
	ID      string `json:"id"`
	Offset  int    `json:"offset"`
}

func (m *mockDocStore) SearchCursor(_ context.Context, opts agent.SearchDocsOptions) (*exec.CursorResult[agent.DocSearchResult], error) {
	results, err := m.Search(context.Background(), opts.Query)
	if err != nil {
		return nil, err
	}
	limit := max(opts.Limit, 1)
	offset := 0
	if opts.Cursor != "" {
		var cursor mockDocSearchCursor
		if err := exec.DecodeSearchCursor(opts.Cursor, &cursor); err != nil {
			return nil, err
		}
		if cursor.Version != 1 || cursor.Query != opts.Query {
			return nil, exec.ErrInvalidCursor
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
	pageItems := make([]agent.DocSearchResult, 0, end-offset)
	for _, item := range results[offset:end] {
		pageItems = append(pageItems, *item)
	}
	result := &exec.CursorResult[agent.DocSearchResult]{
		Items:   pageItems,
		HasMore: end < len(results),
	}
	if result.HasMore && len(pageItems) > 0 {
		result.NextCursor = exec.EncodeSearchCursor(mockDocSearchCursor{
			Version: 1,
			Query:   opts.Query,
			ID:      pageItems[len(pageItems)-1].ID,
		})
	}
	return result, nil
}

func (m *mockDocStore) SearchMatches(_ context.Context, id string, opts agent.SearchDocMatchesOptions) (*exec.CursorResult[*exec.Match], error) {
	if err := agent.ValidateDocID(id); err != nil {
		return nil, agent.ErrInvalidDocID
	}

	doc, ok := m.docs[id]
	if !ok {
		return nil, agent.ErrDocNotFound
	}

	var matches []*exec.Match
	if opts.Query != "" {
		for i, line := range strings.Split(doc.Content, "\n") {
			if strings.Contains(line, opts.Query) {
				matches = append(matches, &exec.Match{
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
		var cursor mockDocMatchCursor
		if err := exec.DecodeSearchCursor(opts.Cursor, &cursor); err != nil {
			return nil, err
		}
		if cursor.Version != 1 || cursor.Query != opts.Query || cursor.ID != id || cursor.Offset < 0 {
			return nil, exec.ErrInvalidCursor
		}
		offset = cursor.Offset
	}

	offset = max(offset, 0)
	offset = min(offset, len(matches))
	end := min(offset+limit, len(matches))
	cursorResult := &exec.CursorResult[*exec.Match]{
		Items:   matches[offset:end],
		HasMore: end < len(matches),
	}
	if cursorResult.HasMore {
		cursorResult.NextCursor = exec.EncodeSearchCursor(mockDocMatchCursor{
			Version: 1,
			Query:   opts.Query,
			ID:      id,
			Offset:  end,
		})
	}
	return cursorResult, nil
}

func mockDocPathRootExcluded(id string, excludedRoots []string) bool {
	root, _, _ := strings.Cut(id, "/")
	return slices.Contains(excludedRoots, root)
}

func (m *mockDocStore) List(_ context.Context, opts agent.ListDocsOptions) (*exec.PaginatedResult[*agent.DocTreeNode], error) {
	m.lastListOpts = opts
	if m.failAll {
		return nil, errForced
	}
	nodes := make([]*agent.DocTreeNode, 0, len(m.docs))
	for _, doc := range m.docs {
		if mockDocPathRootExcluded(doc.ID, opts.ExcludePathRoots) {
			continue
		}
		nodes = append(nodes, &agent.DocTreeNode{
			ID:    doc.ID,
			Name:  path.Base(doc.ID),
			Title: doc.Title,
			Type:  "file",
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	pg := exec.NewPaginator(opts.Page, opts.PerPage)
	start := min(pg.Offset(), len(nodes))
	end := min(start+pg.Limit(), len(nodes))
	result := exec.NewPaginatedResult(nodes[start:end], len(nodes), pg)
	return &result, nil
}

func (m *mockDocStore) ListFlat(_ context.Context, opts agent.ListDocsOptions) (*exec.PaginatedResult[agent.DocMetadata], error) {
	m.lastListOpts = opts
	if m.failAll {
		return nil, errForced
	}
	items := make([]agent.DocMetadata, 0, len(m.docs))
	for _, doc := range m.docs {
		if mockDocPathRootExcluded(doc.ID, opts.ExcludePathRoots) {
			continue
		}
		items = append(items, agent.DocMetadata{
			ID:          doc.ID,
			Title:       doc.Title,
			Description: doc.Description,
			ModTime:     time.Unix(1700000000, 0),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })

	pg := exec.NewPaginator(opts.Page, opts.PerPage)
	start := min(pg.Offset(), len(items))
	end := min(start+pg.Limit(), len(items))
	result := exec.NewPaginatedResult(items[start:end], len(items), pg)
	return &result, nil
}

// docTestSetup contains common test infrastructure for doc API tests.
type docTestSetup struct {
	api   *apiv1.API
	store *mockDocStore
}

func newDocTestSetup(t *testing.T) *docTestSetup {
	t.Helper()
	store := &mockDocStore{docs: make(map[string]*agent.Doc)}
	return newDocTestSetupWithStore(t, store, nil)
}

func newDocTestSetupWithWorkspaces(t *testing.T, names ...string) *docTestSetup {
	t.Helper()
	store := &mockDocStore{docs: make(map[string]*agent.Doc)}
	workspaces := make([]*workspacepkg.Workspace, 0, len(names))
	for _, name := range names {
		workspaces = append(workspaces, &workspacepkg.Workspace{ID: name, Name: name})
	}
	return newDocTestSetupWithStore(t, store, &mockWorkspaceStore{workspaces: workspaces})
}

func newDocTestSetupWithStore(t *testing.T, store *mockDocStore, workspaceStore workspacepkg.Store) *docTestSetup {
	t.Helper()
	cfg := &config.Config{}
	cfg.Server.Permissions = map[config.Permission]bool{
		config.PermissionWriteDAGs: true,
	}
	options := []apiv1.APIOption{apiv1.WithDocStore(store)}
	if workspaceStore != nil {
		options = append(options, apiv1.WithWorkspaceStore(workspaceStore))
	}
	a := apiv1.New(
		nil, nil, nil, nil, runtime.Manager{},
		cfg, nil, nil,
		prometheus.NewRegistry(),
		nil,
		options...,
	)
	return &docTestSetup{api: a, store: store}
}

func TestListDocs(t *testing.T) {
	t.Parallel()

	t.Run("flat mode returns items", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["alpha"] = &agent.Doc{ID: "alpha", Title: "alpha", Description: "Alpha runbook", Content: "content-a"}
		setup.store.docs["beta"] = &agent.Doc{ID: "beta", Title: "beta", Content: "content-b"}

		resp, err := setup.api.ListDocs(adminCtx(), apigen.ListDocsRequestObject{
			Params: apigen.ListDocsParams{
				Flat:    new(true),
				Page:    new(1),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListDocs200JSONResponse)
		require.True(t, ok)
		require.NotNil(t, listResp.Items)
		assert.Len(t, *listResp.Items, 2)
		assert.Equal(t, "Alpha runbook", (*listResp.Items)[0].Description)
	})

	t.Run("tree mode returns nodes", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc-a"] = &agent.Doc{ID: "doc-a", Title: "doc-a", Content: "aaa"}
		setup.store.docs["doc-b"] = &agent.Doc{ID: "doc-b", Title: "doc-b", Content: "bbb"}

		resp, err := setup.api.ListDocs(adminCtx(), apigen.ListDocsRequestObject{
			Params: apigen.ListDocsParams{
				Page:    new(1),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListDocs200JSONResponse)
		require.True(t, ok)
		require.NotNil(t, listResp.Tree)
		assert.Len(t, *listResp.Tree, 2)
	})

	t.Run("no workspace scope filters known workspace roots before pagination", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetupWithWorkspaces(t, "aaa")
		setup.store.docs["aaa/hidden"] = &agent.Doc{ID: "aaa/hidden", Title: "hidden", Content: "private"}
		setup.store.docs["bbb"] = &agent.Doc{ID: "bbb", Title: "bbb", Content: "public"}
		flat := true
		page := 1
		perPage := 1
		workspace := apigen.Workspace("default")

		resp, err := setup.api.ListDocs(adminCtx(), apigen.ListDocsRequestObject{
			Params: apigen.ListDocsParams{
				Workspace: &workspace,
				Flat:      &flat,
				Page:      &page,
				PerPage:   &perPage,
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListDocs200JSONResponse)
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

		store := &mockDocStore{docs: make(map[string]*agent.Doc)}
		setup := newDocTestSetupWithStore(t, store, &mockWorkspaceStore{err: errForced})
		workspace := apigen.Workspace("default")

		_, err := setup.api.ListDocs(adminCtx(), apigen.ListDocsRequestObject{
			Params: apigen.ListDocsParams{Workspace: &workspace},
		})
		require.Error(t, err)
	})

	t.Run("all scope fails closed when workspace names cannot be loaded", func(t *testing.T) {
		t.Parallel()

		store := &mockDocStore{docs: make(map[string]*agent.Doc)}
		setup := newDocTestSetupWithStore(t, store, &mockWorkspaceStore{err: errForced})
		workspace := apigen.Workspace("all")

		_, err := setup.api.ListDocs(adminCtx(), apigen.ListDocsRequestObject{
			Params: apigen.ListDocsParams{Workspace: &workspace},
		})
		require.Error(t, err)
	})

	t.Run("no doc store returns error", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.ListDocs(adminCtx(), apigen.ListDocsRequestObject{})
		require.Error(t, err)
	})
}

func TestListDocsSortParamsForwarded(t *testing.T) {
	t.Parallel()

	t.Run("explicit sort params forwarded to store", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &agent.Doc{ID: "doc1", Title: "doc1", Content: "c"}

		sortParam := apigen.ListDocsParamsSortMtime
		orderParam := apigen.ListDocsParamsOrderDesc

		_, err := setup.api.ListDocs(adminCtx(), apigen.ListDocsRequestObject{
			Params: apigen.ListDocsParams{
				Page:    new(1),
				PerPage: new(10),
				Sort:    &sortParam,
				Order:   &orderParam,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, agent.DocSortFieldMTime, setup.store.lastListOpts.Sort)
		assert.Equal(t, agent.DocSortOrderDesc, setup.store.lastListOpts.Order)
	})

	t.Run("defaults to type asc when omitted", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &agent.Doc{ID: "doc1", Title: "doc1", Content: "c"}

		_, err := setup.api.ListDocs(adminCtx(), apigen.ListDocsRequestObject{
			Params: apigen.ListDocsParams{
				Page:    new(1),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)
		assert.Equal(t, agent.DocSortFieldType, setup.store.lastListOpts.Sort)
		assert.Equal(t, agent.DocSortOrderAsc, setup.store.lastListOpts.Order)
	})

	t.Run("flat mode forwards sort params", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &agent.Doc{ID: "doc1", Title: "doc1", Content: "c"}

		sortParam := apigen.ListDocsParamsSortName
		orderParam := apigen.ListDocsParamsOrderDesc

		_, err := setup.api.ListDocs(adminCtx(), apigen.ListDocsRequestObject{
			Params: apigen.ListDocsParams{
				Flat:    new(true),
				Page:    new(1),
				PerPage: new(10),
				Sort:    &sortParam,
				Order:   &orderParam,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, agent.DocSortFieldName, setup.store.lastListOpts.Sort)
		assert.Equal(t, agent.DocSortOrderDesc, setup.store.lastListOpts.Order)
	})
}

func TestCreateDoc(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		resp, err := setup.api.CreateDoc(adminCtx(), apigen.CreateDocRequestObject{
			Body: &apigen.CreateDocJSONRequestBody{
				Id:      "test-doc",
				Content: "hello",
			},
		})
		require.NoError(t, err)

		_, ok := resp.(apigen.CreateDoc201JSONResponse)
		require.True(t, ok)

		// Verify stored
		_, exists := setup.store.docs["test-doc"]
		assert.True(t, exists)
	})

	t.Run("invalid ID", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.CreateDoc(adminCtx(), apigen.CreateDocRequestObject{
			Body: &apigen.CreateDocJSONRequestBody{
				Id:      "..bad",
				Content: "x",
			},
		})
		require.Error(t, err)
	})

	t.Run("already exists", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["existing"] = &agent.Doc{ID: "existing", Title: "existing", Content: "old"}

		_, err := setup.api.CreateDoc(adminCtx(), apigen.CreateDocRequestObject{
			Body: &apigen.CreateDocJSONRequestBody{
				Id:      "existing",
				Content: "new",
			},
		})
		require.Error(t, err)
	})

	t.Run("omitted workspace rejects known workspace-prefixed path", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetupWithWorkspaces(t, "ops")

		_, err := setup.api.CreateDoc(adminCtx(), apigen.CreateDocRequestObject{
			Body: &apigen.CreateDocJSONRequestBody{
				Id:      "ops/runbook",
				Content: "private",
			},
		})
		require.Error(t, err)
		var apiErr *apiv1.Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		assert.NotContains(t, setup.store.docs, "ops/runbook")
	})

	t.Run("nil body", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.CreateDoc(adminCtx(), apigen.CreateDocRequestObject{
			Body: nil,
		})
		require.Error(t, err)
	})

	t.Run("no doc store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.CreateDoc(adminCtx(), apigen.CreateDocRequestObject{
			Body: &apigen.CreateDocJSONRequestBody{
				Id:      "test",
				Content: "hello",
			},
		})
		require.Error(t, err)
	})
}

func TestGetDoc(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["my-doc"] = &agent.Doc{ID: "my-doc", Title: "my-doc", Description: "My doc description", Content: "hello"}

		resp, err := setup.api.GetDoc(adminCtx(), apigen.GetDocRequestObject{
			Params: apigen.GetDocParams{Path: "my-doc"},
		})
		require.NoError(t, err)

		getResp, ok := resp.(apigen.GetDoc200JSONResponse)
		require.True(t, ok)
		assert.Equal(t, "my-doc", getResp.Id)
		assert.Equal(t, "hello", getResp.Content)
		assert.Equal(t, "my-doc", getResp.Title)
		assert.Equal(t, "My doc description", getResp.Description)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.GetDoc(adminCtx(), apigen.GetDocRequestObject{
			Params: apigen.GetDocParams{Path: "nonexistent"},
		})
		require.Error(t, err)
	})

	t.Run("invalid path", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.GetDoc(adminCtx(), apigen.GetDocRequestObject{
			Params: apigen.GetDocParams{Path: "..bad"},
		})
		require.Error(t, err)
	})

	t.Run("no doc store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.GetDoc(adminCtx(), apigen.GetDocRequestObject{
			Params: apigen.GetDocParams{Path: "my-doc"},
		})
		require.Error(t, err)
	})
}

func TestSearchDocs(t *testing.T) {
	t.Parallel()

	t.Run("returns results", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &agent.Doc{ID: "doc1", Title: "doc1", Description: "World runbook", Content: "hello world"}
		setup.store.docs["doc2"] = &agent.Doc{ID: "doc2", Title: "doc2", Content: "goodbye world"}
		setup.store.docs["doc3"] = &agent.Doc{ID: "doc3", Title: "doc3", Content: "nothing here"}

		resp, err := setup.api.SearchDocs(adminCtx(), apigen.SearchDocsRequestObject{
			Params: apigen.SearchDocsParams{Q: "world"},
		})
		require.NoError(t, err)

		searchResp, ok := resp.(apigen.SearchDocs200JSONResponse)
		require.True(t, ok)
		assert.Len(t, searchResp.Results, 2)
		assert.Equal(t, "World runbook", searchResp.Results[0].Description)
	})

	t.Run("empty query", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.SearchDocs(adminCtx(), apigen.SearchDocsRequestObject{
			Params: apigen.SearchDocsParams{Q: ""},
		})
		require.Error(t, err)
	})

	t.Run("no results", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &agent.Doc{ID: "doc1", Title: "doc1", Content: "hello"}

		resp, err := setup.api.SearchDocs(adminCtx(), apigen.SearchDocsRequestObject{
			Params: apigen.SearchDocsParams{Q: "nonexistent-term"},
		})
		require.NoError(t, err)

		searchResp, ok := resp.(apigen.SearchDocs200JSONResponse)
		require.True(t, ok)
		assert.Empty(t, searchResp.Results)
	})

	t.Run("no doc store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.SearchDocs(adminCtx(), apigen.SearchDocsRequestObject{
			Params: apigen.SearchDocsParams{Q: "hello"},
		})
		require.Error(t, err)
	})
}

func TestUpdateDoc(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &agent.Doc{ID: "doc1", Title: "doc1", Content: "original"}

		resp, err := setup.api.UpdateDoc(adminCtx(), apigen.UpdateDocRequestObject{
			Params: apigen.UpdateDocParams{Path: "doc1"},
			Body:   &apigen.UpdateDocJSONRequestBody{Content: "updated"},
		})
		require.NoError(t, err)

		_, ok := resp.(apigen.UpdateDoc200JSONResponse)
		require.True(t, ok)

		// Verify store content changed
		assert.Equal(t, "updated", setup.store.docs["doc1"].Content)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.UpdateDoc(adminCtx(), apigen.UpdateDocRequestObject{
			Params: apigen.UpdateDocParams{Path: "nonexistent"},
			Body:   &apigen.UpdateDocJSONRequestBody{Content: "updated"},
		})
		require.Error(t, err)
	})

	t.Run("invalid path", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.UpdateDoc(adminCtx(), apigen.UpdateDocRequestObject{
			Params: apigen.UpdateDocParams{Path: "..bad"},
			Body:   &apigen.UpdateDocJSONRequestBody{Content: "updated"},
		})
		require.Error(t, err)
	})

	t.Run("nil body", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.UpdateDoc(adminCtx(), apigen.UpdateDocRequestObject{
			Params: apigen.UpdateDocParams{Path: "doc1"},
			Body:   nil,
		})
		require.Error(t, err)
	})

	t.Run("no doc store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.UpdateDoc(adminCtx(), apigen.UpdateDocRequestObject{
			Params: apigen.UpdateDocParams{Path: "doc1"},
			Body:   &apigen.UpdateDocJSONRequestBody{Content: "updated"},
		})
		require.Error(t, err)
	})
}

func TestDeleteDoc(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &agent.Doc{ID: "doc1", Title: "doc1", Content: "content"}

		resp, err := setup.api.DeleteDoc(adminCtx(), apigen.DeleteDocRequestObject{
			Params: apigen.DeleteDocParams{Path: "doc1"},
		})
		require.NoError(t, err)

		_, ok := resp.(apigen.DeleteDoc204Response)
		require.True(t, ok)

		// Verify removed from store
		_, exists := setup.store.docs["doc1"]
		assert.False(t, exists)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.DeleteDoc(adminCtx(), apigen.DeleteDocRequestObject{
			Params: apigen.DeleteDocParams{Path: "nonexistent"},
		})
		require.Error(t, err)
	})

	t.Run("invalid path", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.DeleteDoc(adminCtx(), apigen.DeleteDocRequestObject{
			Params: apigen.DeleteDocParams{Path: "..bad"},
		})
		require.Error(t, err)
	})

	t.Run("no doc store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.DeleteDoc(adminCtx(), apigen.DeleteDocRequestObject{
			Params: apigen.DeleteDocParams{Path: "doc1"},
		})
		require.Error(t, err)
	})
}

func TestRenameDoc(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["old-doc"] = &agent.Doc{ID: "old-doc", Title: "old-doc", Content: "content"}

		resp, err := setup.api.RenameDoc(adminCtx(), apigen.RenameDocRequestObject{
			Params: apigen.RenameDocParams{Path: "old-doc"},
			Body:   &apigen.RenameDocJSONRequestBody{NewPath: "new-doc"},
		})
		require.NoError(t, err)

		_, ok := resp.(apigen.RenameDoc200JSONResponse)
		require.True(t, ok)

		// Verify store has new-doc, not old-doc
		_, oldExists := setup.store.docs["old-doc"]
		assert.False(t, oldExists)
		_, newExists := setup.store.docs["new-doc"]
		assert.True(t, newExists)
	})

	t.Run("source not found", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.RenameDoc(adminCtx(), apigen.RenameDocRequestObject{
			Params: apigen.RenameDocParams{Path: "nonexistent"},
			Body:   &apigen.RenameDocJSONRequestBody{NewPath: "new"},
		})
		require.Error(t, err)
	})

	t.Run("target exists", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["a"] = &agent.Doc{ID: "a", Title: "a", Content: "aaa"}
		setup.store.docs["b"] = &agent.Doc{ID: "b", Title: "b", Content: "bbb"}

		_, err := setup.api.RenameDoc(adminCtx(), apigen.RenameDocRequestObject{
			Params: apigen.RenameDocParams{Path: "a"},
			Body:   &apigen.RenameDocJSONRequestBody{NewPath: "b"},
		})
		require.Error(t, err)
	})

	t.Run("invalid source path", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.RenameDoc(adminCtx(), apigen.RenameDocRequestObject{
			Params: apigen.RenameDocParams{Path: "..bad"},
			Body:   &apigen.RenameDocJSONRequestBody{NewPath: "good"},
		})
		require.Error(t, err)
	})

	t.Run("invalid new path", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["good"] = &agent.Doc{ID: "good", Title: "good", Content: "content"}

		_, err := setup.api.RenameDoc(adminCtx(), apigen.RenameDocRequestObject{
			Params: apigen.RenameDocParams{Path: "good"},
			Body:   &apigen.RenameDocJSONRequestBody{NewPath: "..bad"},
		})
		require.Error(t, err)
	})

	t.Run("nil body", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.RenameDoc(adminCtx(), apigen.RenameDocRequestObject{
			Params: apigen.RenameDocParams{Path: "old"},
			Body:   nil,
		})
		require.Error(t, err)
	})

	t.Run("directory rename success", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["folder/doc1"] = &agent.Doc{ID: "folder/doc1", Title: "doc1", Content: "c1"}
		setup.store.docs["folder/doc2"] = &agent.Doc{ID: "folder/doc2", Title: "doc2", Content: "c2"}

		resp, err := setup.api.RenameDoc(adminCtx(), apigen.RenameDocRequestObject{
			Params: apigen.RenameDocParams{Path: "folder"},
			Body:   &apigen.RenameDocJSONRequestBody{NewPath: "moved"},
		})
		require.NoError(t, err)

		_, ok := resp.(apigen.RenameDoc200JSONResponse)
		require.True(t, ok)

		_, oldExists := setup.store.docs["folder/doc1"]
		assert.False(t, oldExists)
		_, newExists := setup.store.docs["moved/doc1"]
		assert.True(t, newExists)
		_, newExists2 := setup.store.docs["moved/doc2"]
		assert.True(t, newExists2)
	})

	t.Run("directory rename target exists", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["src/doc"] = &agent.Doc{ID: "src/doc", Title: "doc", Content: "c1"}
		setup.store.docs["dst/doc"] = &agent.Doc{ID: "dst/doc", Title: "doc", Content: "c2"}

		_, err := setup.api.RenameDoc(adminCtx(), apigen.RenameDocRequestObject{
			Params: apigen.RenameDocParams{Path: "src"},
			Body:   &apigen.RenameDocJSONRequestBody{NewPath: "dst"},
		})
		require.Error(t, err)
	})

	t.Run("directory not found", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.RenameDoc(adminCtx(), apigen.RenameDocRequestObject{
			Params: apigen.RenameDocParams{Path: "nonexistent-dir"},
			Body:   &apigen.RenameDocJSONRequestBody{NewPath: "target"},
		})
		require.Error(t, err)
	})

	t.Run("no doc store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.RenameDoc(adminCtx(), apigen.RenameDocRequestObject{
			Params: apigen.RenameDocParams{Path: "old"},
			Body:   &apigen.RenameDocJSONRequestBody{NewPath: "new"},
		})
		require.Error(t, err)
	})
}

func TestSearchDocsWithMatches(t *testing.T) {
	t.Parallel()

	t.Run("returns results with match details", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &agent.Doc{
			ID: "doc1", Title: "doc1",
			Content: "line one\nhello world\nline three",
		}

		resp, err := setup.api.SearchDocs(adminCtx(), apigen.SearchDocsRequestObject{
			Params: apigen.SearchDocsParams{Q: "hello"},
		})
		require.NoError(t, err)

		searchResp, ok := resp.(apigen.SearchDocs200JSONResponse)
		require.True(t, ok)
		require.Len(t, searchResp.Results, 1)
		item := searchResp.Results[0]
		assert.Equal(t, "doc1", item.Id)
		require.NotNil(t, item.Matches)
		assert.Len(t, *item.Matches, 1)
		assert.Equal(t, "hello world", (*item.Matches)[0].Line)
		assert.Equal(t, 2, (*item.Matches)[0].LineNumber)
	})
}

func TestListDocsTreeWithChildren(t *testing.T) {
	t.Parallel()

	t.Run("tree nodes with children are rendered", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		// Directly put a tree node with children in the store mock.
		// We override List to return a node with children.
		setup.store.docs["parent/child1"] = &agent.Doc{ID: "parent/child1", Title: "child1", Content: "c1"}
		setup.store.docs["parent/child2"] = &agent.Doc{ID: "parent/child2", Title: "child2", Content: "c2"}

		// Replace the store with one that returns a directory structure.
		dirStore := &mockDocStoreWithTree{
			mockDocStore: setup.store,
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
			apiv1.WithDocStore(dirStore),
		)

		resp, err := a.ListDocs(adminCtx(), apigen.ListDocsRequestObject{
			Params: apigen.ListDocsParams{
				Page:    new(1),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListDocs200JSONResponse)
		require.True(t, ok)
		require.NotNil(t, listResp.Tree)
		require.Len(t, *listResp.Tree, 1)

		parent := (*listResp.Tree)[0]
		assert.Equal(t, "directory", string(parent.Type))
		require.NotNil(t, parent.Children)
		assert.Len(t, *parent.Children, 2)
	})
}

// mockDocStoreWithTree wraps mockDocStore but returns a directory tree from List.
type mockDocStoreWithTree struct {
	*mockDocStore
}

func (m *mockDocStoreWithTree) List(_ context.Context, opts agent.ListDocsOptions) (*exec.PaginatedResult[*agent.DocTreeNode], error) {
	nodes := []*agent.DocTreeNode{
		{
			ID:   "parent",
			Name: "parent",
			Type: "directory",
			Children: []*agent.DocTreeNode{
				{ID: "parent/child1", Name: "child1", Title: "child1", Type: "file"},
				{ID: "parent/child2", Name: "child2", Title: "child2", Type: "file"},
			},
		},
	}
	filtered := nodes[:0]
	for _, node := range nodes {
		if !mockDocPathRootExcluded(node.ID, opts.ExcludePathRoots) {
			filtered = append(filtered, node)
		}
	}
	pg := exec.NewPaginator(opts.Page, opts.PerPage)
	start := min(pg.Offset(), len(filtered))
	end := min(start+pg.Limit(), len(filtered))
	result := exec.NewPaginatedResult(filtered[start:end], len(filtered), pg)
	return &result, nil
}

func TestGetDocContentData(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &agent.Doc{ID: "doc1", Title: "doc1", Content: "hello"}

		resp, err := setup.api.GetDocContentData(adminCtx(), "doc1")
		require.NoError(t, err)

		docResp, ok := resp.(apigen.DocResponse)
		require.True(t, ok)
		assert.Equal(t, "doc1", docResp.Id)
		assert.Equal(t, "hello", docResp.Content)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.GetDocContentData(adminCtx(), "nonexistent")
		require.Error(t, err)
	})

	t.Run("no doc store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.GetDocContentData(adminCtx(), "doc1")
		require.Error(t, err)
	})
}

// TestDocStoreInternalErrors covers error paths where the store returns
// unexpected (non-sentinel) errors, triggering the internalError() paths.
func TestDocStoreInternalErrors(t *testing.T) {
	t.Parallel()

	newFailSetup := func(t *testing.T) *docTestSetup {
		t.Helper()
		s := newDocTestSetup(t)
		s.store.failAll = true
		return s
	}

	t.Run("ListDocs flat store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.ListDocs(adminCtx(), apigen.ListDocsRequestObject{
			Params: apigen.ListDocsParams{Flat: new(true), Page: new(1), PerPage: new(10)},
		})
		require.Error(t, err)
	})

	t.Run("ListDocs tree store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.ListDocs(adminCtx(), apigen.ListDocsRequestObject{
			Params: apigen.ListDocsParams{Page: new(1), PerPage: new(10)},
		})
		require.Error(t, err)
	})

	t.Run("CreateDoc store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.CreateDoc(adminCtx(), apigen.CreateDocRequestObject{
			Body: &apigen.CreateDocJSONRequestBody{Id: "test", Content: "hello"},
		})
		require.Error(t, err)
	})

	t.Run("GetDoc store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.GetDoc(adminCtx(), apigen.GetDocRequestObject{
			Params: apigen.GetDocParams{Path: "test"},
		})
		require.Error(t, err)
	})

	t.Run("SearchDocs store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.SearchDocs(adminCtx(), apigen.SearchDocsRequestObject{
			Params: apigen.SearchDocsParams{Q: "hello"},
		})
		require.Error(t, err)
	})

	t.Run("UpdateDoc store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.UpdateDoc(adminCtx(), apigen.UpdateDocRequestObject{
			Params: apigen.UpdateDocParams{Path: "test"},
			Body:   &apigen.UpdateDocJSONRequestBody{Content: "new"},
		})
		require.Error(t, err)
	})

	t.Run("DeleteDoc store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.DeleteDoc(adminCtx(), apigen.DeleteDocRequestObject{
			Params: apigen.DeleteDocParams{Path: "test"},
		})
		require.Error(t, err)
	})

	t.Run("RenameDoc store error", func(t *testing.T) {
		t.Parallel()
		setup := newFailSetup(t)
		_, err := setup.api.RenameDoc(adminCtx(), apigen.RenameDocRequestObject{
			Params: apigen.RenameDocParams{Path: "old"},
			Body:   &apigen.RenameDocJSONRequestBody{NewPath: "new"},
		})
		require.Error(t, err)
	})
}

// TestDocWritePermissionDenied covers the requireDAGWrite error path
// when PermissionWriteDAGs is not set.
func TestDocWritePermissionDenied(t *testing.T) {
	t.Parallel()

	newNoWriteSetup := func(t *testing.T) *apiv1.API {
		t.Helper()
		store := &mockDocStore{docs: make(map[string]*agent.Doc)}
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
			apiv1.WithDocStore(store),
		)
	}

	t.Run("CreateDoc denied", func(t *testing.T) {
		t.Parallel()
		a := newNoWriteSetup(t)
		_, err := a.CreateDoc(adminCtx(), apigen.CreateDocRequestObject{
			Body: &apigen.CreateDocJSONRequestBody{Id: "test", Content: "hello"},
		})
		require.Error(t, err)
	})

	t.Run("UpdateDoc denied", func(t *testing.T) {
		t.Parallel()
		a := newNoWriteSetup(t)
		_, err := a.UpdateDoc(adminCtx(), apigen.UpdateDocRequestObject{
			Params: apigen.UpdateDocParams{Path: "test"},
			Body:   &apigen.UpdateDocJSONRequestBody{Content: "new"},
		})
		require.Error(t, err)
	})

	t.Run("DeleteDoc denied", func(t *testing.T) {
		t.Parallel()
		a := newNoWriteSetup(t)
		_, err := a.DeleteDoc(adminCtx(), apigen.DeleteDocRequestObject{
			Params: apigen.DeleteDocParams{Path: "test"},
		})
		require.Error(t, err)
	})

	t.Run("RenameDoc denied", func(t *testing.T) {
		t.Parallel()
		a := newNoWriteSetup(t)
		_, err := a.RenameDoc(adminCtx(), apigen.RenameDocRequestObject{
			Params: apigen.RenameDocParams{Path: "old"},
			Body:   &apigen.RenameDocJSONRequestBody{NewPath: "new"},
		})
		require.Error(t, err)
	})

	t.Run("DeleteDocBatch denied", func(t *testing.T) {
		t.Parallel()
		a := newNoWriteSetup(t)
		_, err := a.DeleteDocBatch(adminCtx(), apigen.DeleteDocBatchRequestObject{
			Body: &apigen.DeleteDocBatchJSONRequestBody{Paths: []string{"test"}},
		})
		require.Error(t, err)
	})
}

func TestDeleteDocBatch(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &agent.Doc{ID: "doc1", Title: "doc1", Content: "c1"}
		setup.store.docs["doc2"] = &agent.Doc{ID: "doc2", Title: "doc2", Content: "c2"}

		resp, err := setup.api.DeleteDocBatch(adminCtx(), apigen.DeleteDocBatchRequestObject{
			Body: &apigen.DeleteDocBatchJSONRequestBody{Paths: []string{"doc1", "doc2"}},
		})
		require.NoError(t, err)

		batchResp, ok := resp.(apigen.DeleteDocBatch200JSONResponse)
		require.True(t, ok)
		assert.Len(t, batchResp.Deleted, 2)
		assert.Empty(t, batchResp.Failed)
		assert.Equal(t, 0, len(setup.store.docs))
	})

	t.Run("partial failure", func(t *testing.T) {
		t.Parallel()
		setup := newDocTestSetup(t)
		setup.store.docs["valid"] = &agent.Doc{ID: "valid", Title: "valid", Content: "c"}

		resp, err := setup.api.DeleteDocBatch(adminCtx(), apigen.DeleteDocBatchRequestObject{
			Body: &apigen.DeleteDocBatchJSONRequestBody{Paths: []string{"valid", "nonexistent"}},
		})
		require.NoError(t, err)

		batchResp, ok := resp.(apigen.DeleteDocBatch200JSONResponse)
		require.True(t, ok)
		assert.Len(t, batchResp.Deleted, 2) // nonexistent treated as success
		assert.Empty(t, batchResp.Failed)
	})

	t.Run("directory delete", func(t *testing.T) {
		t.Parallel()
		setup := newDocTestSetup(t)
		setup.store.docs["dir/child1"] = &agent.Doc{ID: "dir/child1", Title: "child1", Content: "c1"}
		setup.store.docs["dir/child2"] = &agent.Doc{ID: "dir/child2", Title: "child2", Content: "c2"}

		resp, err := setup.api.DeleteDocBatch(adminCtx(), apigen.DeleteDocBatchRequestObject{
			Body: &apigen.DeleteDocBatchJSONRequestBody{Paths: []string{"dir"}},
		})
		require.NoError(t, err)

		batchResp, ok := resp.(apigen.DeleteDocBatch200JSONResponse)
		require.True(t, ok)
		assert.Len(t, batchResp.Deleted, 1)
		assert.Empty(t, batchResp.Failed)
		assert.Equal(t, 0, len(setup.store.docs))
	})

	t.Run("nil body", func(t *testing.T) {
		t.Parallel()
		setup := newDocTestSetup(t)
		_, err := setup.api.DeleteDocBatch(adminCtx(), apigen.DeleteDocBatchRequestObject{Body: nil})
		require.Error(t, err)
	})

	t.Run("empty paths", func(t *testing.T) {
		t.Parallel()
		setup := newDocTestSetup(t)
		_, err := setup.api.DeleteDocBatch(adminCtx(), apigen.DeleteDocBatchRequestObject{
			Body: &apigen.DeleteDocBatchJSONRequestBody{Paths: []string{}},
		})
		require.Error(t, err)
	})

	t.Run("invalid path", func(t *testing.T) {
		t.Parallel()
		setup := newDocTestSetup(t)
		_, err := setup.api.DeleteDocBatch(adminCtx(), apigen.DeleteDocBatchRequestObject{
			Body: &apigen.DeleteDocBatchJSONRequestBody{Paths: []string{"..bad"}},
		})
		require.Error(t, err)
	})

	t.Run("no doc store", func(t *testing.T) {
		t.Parallel()
		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)
		_, err := a.DeleteDocBatch(adminCtx(), apigen.DeleteDocBatchRequestObject{
			Body: &apigen.DeleteDocBatchJSONRequestBody{Paths: []string{"test"}},
		})
		require.Error(t, err)
	})

	t.Run("store error", func(t *testing.T) {
		t.Parallel()
		setup := newDocTestSetup(t)
		setup.store.failAll = true
		_, err := setup.api.DeleteDocBatch(adminCtx(), apigen.DeleteDocBatchRequestObject{
			Body: &apigen.DeleteDocBatchJSONRequestBody{Paths: []string{"test"}},
		})
		require.Error(t, err)
	})
}
