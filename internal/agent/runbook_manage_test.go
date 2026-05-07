// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/core/exec"
	workspacepkg "github.com/dagucloud/dagu/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errRunbookForced = errors.New("forced runbook error")

type mockRunbookDocStore struct {
	docs                   map[string]*Doc
	listErr                error
	getErr                 error
	createErr              error
	updateErr              error
	searchErr              error
	ignoreExcludePathRoots bool
}

func newMockRunbookDocStore() *mockRunbookDocStore {
	return &mockRunbookDocStore{docs: map[string]*Doc{}}
}

func (s *mockRunbookDocStore) List(context.Context, ListDocsOptions) (*exec.PaginatedResult[*DocTreeNode], error) {
	return nil, nil
}

func (s *mockRunbookDocStore) ListFlat(_ context.Context, opts ListDocsOptions) (*exec.PaginatedResult[DocMetadata], error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	items := make([]DocMetadata, 0, len(s.docs))
	for id, doc := range s.docs {
		if opts.PathPrefix != "" && !strings.HasPrefix(id, opts.PathPrefix) {
			continue
		}
		if !s.ignoreExcludePathRoots && runbookTestRootExcluded(id, opts.ExcludePathRoots) {
			continue
		}
		items = append(items, DocMetadata{
			ID:          id,
			Title:       doc.Title,
			Description: doc.Description,
			ModTime:     time.Unix(1700000000, 0),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return &exec.PaginatedResult[DocMetadata]{Items: items, TotalCount: len(items)}, nil
}

func runbookTestRootExcluded(id string, roots []string) bool {
	root, _, _ := strings.Cut(id, "/")
	return slices.Contains(roots, root)
}

func (s *mockRunbookDocStore) Get(_ context.Context, id string) (*Doc, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	doc, ok := s.docs[id]
	if !ok {
		return nil, ErrDocNotFound
	}
	copy := *doc
	return &copy, nil
}

func (s *mockRunbookDocStore) Create(_ context.Context, id, content string) error {
	if s.createErr != nil {
		return s.createErr
	}
	if _, ok := s.docs[id]; ok {
		return ErrDocAlreadyExists
	}
	s.docs[id] = docFromContent(id, content)
	return nil
}

func (s *mockRunbookDocStore) Update(_ context.Context, id, content string) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	if _, ok := s.docs[id]; !ok {
		return ErrDocNotFound
	}
	s.docs[id] = docFromContent(id, content)
	return nil
}

func (s *mockRunbookDocStore) Delete(_ context.Context, id string) error {
	if _, ok := s.docs[id]; !ok {
		return ErrDocNotFound
	}
	delete(s.docs, id)
	return nil
}

func (s *mockRunbookDocStore) DeleteBatch(context.Context, []string) ([]string, []DeleteError, error) {
	return nil, nil, nil
}

func (s *mockRunbookDocStore) Rename(context.Context, string, string) error { return nil }

func (s *mockRunbookDocStore) Search(_ context.Context, query string) ([]*DocSearchResult, error) {
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	var results []*DocSearchResult
	q := strings.ToLower(query)
	for id, doc := range s.docs {
		if strings.Contains(strings.ToLower(id), q) || strings.Contains(strings.ToLower(doc.Title), q) || strings.Contains(strings.ToLower(doc.Description), q) || strings.Contains(strings.ToLower(doc.Content), q) {
			results = append(results, &DocSearchResult{ID: id, Title: doc.Title, Description: doc.Description})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results, nil
}

func (s *mockRunbookDocStore) SearchCursor(context.Context, SearchDocsOptions) (*exec.CursorResult[DocSearchResult], error) {
	return nil, nil
}

func (s *mockRunbookDocStore) SearchMatches(context.Context, string, SearchDocMatchesOptions) (*exec.CursorResult[*exec.Match], error) {
	return nil, nil
}

type mockRunbookWorkspaceStore struct {
	workspaces []*workspacepkg.Workspace
	err        error
}

func (s *mockRunbookWorkspaceStore) Create(context.Context, *workspacepkg.Workspace) error {
	return nil
}
func (s *mockRunbookWorkspaceStore) GetByID(context.Context, string) (*workspacepkg.Workspace, error) {
	return nil, workspacepkg.ErrWorkspaceNotFound
}
func (s *mockRunbookWorkspaceStore) GetByName(context.Context, string) (*workspacepkg.Workspace, error) {
	return nil, workspacepkg.ErrWorkspaceNotFound
}
func (s *mockRunbookWorkspaceStore) List(context.Context) ([]*workspacepkg.Workspace, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.workspaces, nil
}
func (s *mockRunbookWorkspaceStore) Update(context.Context, *workspacepkg.Workspace) error {
	return nil
}
func (s *mockRunbookWorkspaceStore) Delete(context.Context, string) error { return nil }

func docFromContent(id, content string) *Doc {
	title := id
	description := ""
	if after, ok := strings.CutPrefix(content, "---\n"); ok {
		parts := strings.SplitN(after, "\n---\n", 2)
		if len(parts) == 2 {
			for line := range strings.SplitSeq(parts[0], "\n") {
				if v, ok := strings.CutPrefix(line, "title: "); ok {
					title = strings.Trim(v, `"`)
				}
				if v, ok := strings.CutPrefix(line, "description: "); ok {
					description = strings.Trim(v, `"`)
				}
			}
		}
	}
	return &Doc{ID: id, Title: title, Description: description, Content: content}
}

func runbookInput(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

func decodeRunbookOutput(t *testing.T, out ToolOut) map[string]any {
	t.Helper()
	require.False(t, out.IsError, out.Content)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.Content), &decoded))
	return decoded
}

func TestRunbookManageTool_ListSearchGet(t *testing.T) {
	t.Parallel()
	store := newMockRunbookDocStore()
	store.docs["runbooks/deploy"] = &Doc{ID: "runbooks/deploy", Title: "Deploy", Description: "Deploy production safely", Content: "# Deploy\n"}
	store.docs["notes/other"] = &Doc{ID: "notes/other", Title: "Other", Content: "# Other\n"}
	tool := NewRunbookManageTool(store)

	list := decodeRunbookOutput(t, tool.Run(ToolContext{Context: context.Background()}, runbookInput(t, map[string]any{"action": "list", "limit": 10})))
	runbooks := list["runbooks"].([]any)
	require.Len(t, runbooks, 2)
	assert.Equal(t, "runbooks/deploy", runbooks[1].(map[string]any)["id"])
	assert.Equal(t, "Deploy production safely", runbooks[1].(map[string]any)["description"])
	assert.NotContains(t, runbooks[1].(map[string]any), "content")

	search := decodeRunbookOutput(t, tool.Run(ToolContext{Context: context.Background()}, runbookInput(t, map[string]any{"action": "search", "query": "production"})))
	results := search["results"].([]any)
	require.Len(t, results, 1)
	assert.Equal(t, "runbooks/deploy", results[0].(map[string]any)["id"])

	get := decodeRunbookOutput(t, tool.Run(ToolContext{Context: context.Background()}, runbookInput(t, map[string]any{"action": "get", "id": "runbooks/deploy"})))
	assert.Equal(t, "Deploy", get["title"])
	assert.Equal(t, "# Deploy\n", get["body"])
}

func TestRunbookManageTool_CreatePatchEnsureMetadata(t *testing.T) {
	t.Parallel()
	store := newMockRunbookDocStore()
	tool := NewRunbookManageTool(store)
	ctx := ToolContext{Context: context.Background(), Role: auth.RoleAdmin}

	create := decodeRunbookOutput(t, tool.Run(ctx, runbookInput(t, map[string]any{
		"action":      "create",
		"id":          "runbooks/restart-api",
		"title":       "Restart API",
		"description": "Restart the API and verify health",
		"content":     "# Restart API\n\nRun the restart command.\n",
	})))
	assert.Equal(t, true, create["updated"])
	require.Contains(t, store.docs, "runbooks/restart-api")
	assert.Contains(t, store.docs["runbooks/restart-api"].Content, "title: Restart API")
	assert.Contains(t, store.docs["runbooks/restart-api"].Content, "description: Restart the API and verify health")

	patch := decodeRunbookOutput(t, tool.Run(ctx, runbookInput(t, map[string]any{
		"action":     "patch",
		"id":         "runbooks/restart-api",
		"old_string": "Run the restart command.",
		"new_string": "Run the restart command, then check /healthz.",
	})))
	assert.Equal(t, true, patch["updated"])
	assert.Contains(t, store.docs["runbooks/restart-api"].Content, "check /healthz")

	store.docs["runbooks/plain"] = &Doc{ID: "runbooks/plain", Title: "plain", Content: "# Plain\n"}
	ensure := decodeRunbookOutput(t, tool.Run(ctx, runbookInput(t, map[string]any{
		"action":      "ensure_metadata",
		"id":          "runbooks/plain",
		"title":       "Plain",
		"description": "Plain runbook",
	})))
	assert.Equal(t, true, ensure["updated"])
	assert.Contains(t, store.docs["runbooks/plain"].Content, "title: Plain")
	assert.Contains(t, store.docs["runbooks/plain"].Content, "# Plain")
}

func TestRunbookManageTool_WritePermissionAndInvalidID(t *testing.T) {
	t.Parallel()
	store := newMockRunbookDocStore()
	tool := NewRunbookManageTool(store)

	out := tool.Run(ToolContext{Context: context.Background(), Role: auth.RoleViewer}, runbookInput(t, map[string]any{"action": "create", "id": "runbooks/new", "content": "# New"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "write permission")

	out = tool.Run(ToolContext{Context: context.Background()}, runbookInput(t, map[string]any{"action": "get", "id": "../secrets"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "invalid")
}

func TestRunbookManageTool_AvailabilityAndInputErrors(t *testing.T) {
	t.Parallel()
	tool := NewRunbookManageTool(nil)
	out := tool.Run(ToolContext{}, runbookInput(t, map[string]any{"action": "list"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "unavailable")

	tool = NewRunbookManageTool(newMockRunbookDocStore())
	out = tool.Run(ToolContext{}, json.RawMessage(`{invalid`))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "Failed to parse")

	out = tool.Run(ToolContext{}, runbookInput(t, map[string]any{"action": "list"}))
	assert.False(t, out.IsError, out.Content)

	out = tool.Run(ToolContext{Context: context.Background()}, runbookInput(t, map[string]any{"action": "bogus"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "Unknown action")
}

func TestRunbookManageTool_ListAndSearchErrors(t *testing.T) {
	t.Parallel()

	storeWithMany := newMockRunbookDocStore()
	storeWithMany.docs["runbooks/a"] = &Doc{ID: "runbooks/a", Title: "A", Content: "# A"}
	storeWithMany.docs["runbooks/b"] = &Doc{ID: "runbooks/b", Title: "B", Content: "# B"}
	tool := NewRunbookManageTool(storeWithMany)
	list := decodeRunbookOutput(t, tool.Run(ToolContext{Context: context.Background()}, runbookInput(t, map[string]any{"action": "list", "limit": 1})))
	require.Len(t, list["runbooks"].([]any), 1)

	search := decodeRunbookOutput(t, tool.Run(ToolContext{Context: context.Background()}, runbookInput(t, map[string]any{"action": "search", "query": "runbooks", "limit": 1})))
	require.Len(t, search["results"].([]any), 1)

	tool = NewRunbookManageTool(&mockRunbookDocStore{docs: map[string]*Doc{}, listErr: errRunbookForced})
	out := tool.Run(ToolContext{Context: context.Background()}, runbookInput(t, map[string]any{"action": "list"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "Failed to list")

	store := newMockRunbookDocStore()
	tool = NewRunbookManageToolWithWorkspaceStore(store, &mockRunbookWorkspaceStore{err: errRunbookForced})
	out = tool.Run(ToolContext{
		Context: context.Background(),
		User: UserIdentity{
			UserID:          "u1",
			Role:            auth.RoleViewer,
			WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: "ops", Role: auth.RoleViewer}}},
		},
	}, runbookInput(t, map[string]any{"action": "list"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "Failed to filter")

	tool = NewRunbookManageTool(store)
	out = tool.Run(ToolContext{Context: context.Background()}, runbookInput(t, map[string]any{"action": "search"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "query is required")

	store.searchErr = errRunbookForced
	out = tool.Run(ToolContext{Context: context.Background()}, runbookInput(t, map[string]any{"action": "search", "query": "deploy"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "Failed to search")
}

func TestRunbookManageTool_GetErrors(t *testing.T) {
	t.Parallel()

	store := newMockRunbookDocStore()
	tool := NewRunbookManageTool(store)
	out := tool.Run(ToolContext{Context: context.Background()}, runbookInput(t, map[string]any{"action": "get", "id": "runbooks/missing"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "doc not found")

	store.getErr = errRunbookForced
	out = tool.Run(ToolContext{Context: context.Background()}, runbookInput(t, map[string]any{"action": "get", "id": "runbooks/forced"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "forced runbook error")

	tool = NewRunbookManageToolWithWorkspaceStore(store, &mockRunbookWorkspaceStore{err: errRunbookForced})
	out = tool.Run(ToolContext{Context: context.Background(), Role: auth.RoleViewer}, runbookInput(t, map[string]any{"action": "get", "id": "ops/runbooks/restart"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "Failed to filter")
}

func TestRunbookManageTool_EnforcesVisibleWorkspacesForAuthenticatedUser(t *testing.T) {
	t.Parallel()
	store := newMockRunbookDocStore()
	store.docs["ops/runbooks/restart"] = &Doc{ID: "ops/runbooks/restart", Title: "Restart", Description: "visible", Content: "# Restart\n"}
	store.docs["prod/runbooks/deploy"] = &Doc{ID: "prod/runbooks/deploy", Title: "Deploy", Description: "hidden production", Content: "# Deploy\n"}
	store.docs["runbooks/general"] = &Doc{ID: "runbooks/general", Title: "General", Description: "unscoped", Content: "# General\n"}
	tool := NewRunbookManageToolWithWorkspaceStore(store, &mockRunbookWorkspaceStore{workspaces: []*workspacepkg.Workspace{{Name: "ops"}, {Name: "prod"}}})
	ctx := ToolContext{
		Context: context.Background(),
		User: UserIdentity{
			UserID:          "u1",
			Role:            auth.RoleViewer,
			WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: "ops", Role: auth.RoleViewer}}},
		},
	}

	list := decodeRunbookOutput(t, tool.Run(ctx, runbookInput(t, map[string]any{"action": "list", "limit": 10})))
	runbooks := list["runbooks"].([]any)
	require.Len(t, runbooks, 2)
	ids := []string{runbooks[0].(map[string]any)["id"].(string), runbooks[1].(map[string]any)["id"].(string)}
	assert.Contains(t, ids, "ops/runbooks/restart")
	assert.Contains(t, ids, "runbooks/general")
	assert.NotContains(t, ids, "prod/runbooks/deploy")

	search := decodeRunbookOutput(t, tool.Run(ctx, runbookInput(t, map[string]any{"action": "search", "query": "hidden", "limit": 10})))
	results := search["results"].([]any)
	require.Empty(t, results)

	out := tool.Run(ctx, runbookInput(t, map[string]any{"action": "get", "id": "prod/runbooks/deploy"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "not found")

	store.ignoreExcludePathRoots = true
	list = decodeRunbookOutput(t, tool.Run(ctx, runbookInput(t, map[string]any{"action": "list", "limit": 10})))
	runbooks = list["runbooks"].([]any)
	require.Len(t, runbooks, 2)
	ids = []string{runbooks[0].(map[string]any)["id"].(string), runbooks[1].(map[string]any)["id"].(string)}
	assert.NotContains(t, ids, "prod/runbooks/deploy")
}

func TestRunbookManageTool_EnforcesWorkspaceWriteRole(t *testing.T) {
	t.Parallel()
	store := newMockRunbookDocStore()
	store.docs["ops/runbooks/restart"] = &Doc{ID: "ops/runbooks/restart", Title: "Restart", Content: "# Restart\n"}
	tool := NewRunbookManageToolWithWorkspaceStore(store, &mockRunbookWorkspaceStore{workspaces: []*workspacepkg.Workspace{{Name: "ops"}}})

	viewerCtx := ToolContext{Context: context.Background(), User: UserIdentity{UserID: "u1", Role: auth.RoleViewer, WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: "ops", Role: auth.RoleViewer}}}}}
	out := tool.Run(viewerCtx, runbookInput(t, map[string]any{"action": "patch", "id": "ops/runbooks/restart", "old_string": "Restart", "new_string": "Restart API"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "write permission")

	managerCtx := ToolContext{Context: context.Background(), User: UserIdentity{UserID: "u1", Role: auth.RoleViewer, WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: "ops", Role: auth.RoleManager}}}}}
	out = tool.Run(managerCtx, runbookInput(t, map[string]any{"action": "patch", "id": "ops/runbooks/restart", "old_string": "Restart", "new_string": "Restart API"}))
	require.False(t, out.IsError, out.Content)
}

func TestRunbookManageTool_CreateUpdatePatchErrors(t *testing.T) {
	t.Parallel()
	store := newMockRunbookDocStore()
	tool := NewRunbookManageTool(store)
	ctx := ToolContext{Context: context.Background(), Role: auth.RoleAdmin}

	out := tool.Run(ctx, runbookInput(t, map[string]any{"action": "create", "id": "../bad", "content": "# Bad"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "invalid")

	_, ok := decodeRunbookOutput(t, tool.Run(ctx, runbookInput(t, map[string]any{"action": "create", "id": "runbooks/existing", "content": "# Existing"})))["updated"].(bool)
	assert.True(t, ok)
	out = tool.Run(ctx, runbookInput(t, map[string]any{"action": "create", "id": "runbooks/existing", "content": "# Existing"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "already exists")

	store.createErr = errRunbookForced
	out = tool.Run(ctx, runbookInput(t, map[string]any{"action": "create", "id": "runbooks/create-error", "content": "# Error"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "forced runbook error")
	store.createErr = nil

	update := decodeRunbookOutput(t, tool.Run(ctx, runbookInput(t, map[string]any{"action": "update", "id": "runbooks/existing", "content": "# Updated\n"})))
	assert.Equal(t, true, update["updated"])
	assert.Equal(t, "# Updated\n", store.docs["runbooks/existing"].Content)

	update = decodeRunbookOutput(t, tool.Run(ctx, runbookInput(t, map[string]any{
		"action":      "update",
		"id":          "runbooks/existing",
		"title":       "Updated",
		"description": "Updated desc",
		"content":     "# Body\n",
	})))
	assert.Equal(t, true, update["updated"])
	assert.Contains(t, store.docs["runbooks/existing"].Content, "title: Updated")
	assert.Contains(t, store.docs["runbooks/existing"].Content, "# Body")

	out = tool.Run(ctx, runbookInput(t, map[string]any{"action": "update", "id": "../bad", "content": "# Bad"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "invalid")

	out = tool.Run(ctx, runbookInput(t, map[string]any{"action": "update", "id": "runbooks/missing", "content": "# Missing"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "doc not found")

	store.updateErr = errRunbookForced
	out = tool.Run(ctx, runbookInput(t, map[string]any{"action": "update", "id": "runbooks/existing", "content": "# Forced"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "forced runbook error")
	store.updateErr = nil

	out = tool.Run(ctx, runbookInput(t, map[string]any{"action": "patch", "id": "runbooks/existing"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "old_string is required")

	out = tool.Run(ctx, runbookInput(t, map[string]any{"action": "patch", "id": "runbooks/missing", "old_string": "x", "new_string": "y"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "doc not found")

	out = tool.Run(ctx, runbookInput(t, map[string]any{"action": "patch", "id": "runbooks/existing", "old_string": "absent", "new_string": "present"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "not found")

	store.docs["runbooks/repeated"] = &Doc{ID: "runbooks/repeated", Title: "Repeated", Content: "same same"}
	out = tool.Run(ctx, runbookInput(t, map[string]any{"action": "patch", "id": "runbooks/repeated", "old_string": "same", "new_string": "different"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "found 2 times")

	store.updateErr = errRunbookForced
	out = tool.Run(ctx, runbookInput(t, map[string]any{"action": "patch", "id": "runbooks/existing", "old_string": "Body", "new_string": "Forced"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "forced runbook error")

	out = runbookCreate(ctx, store, runbookManageInput{ID: "../bad", Content: "# Bad"})
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "invalid")
}

func TestRunbookManageTool_EnsureMetadataErrorsAndNoop(t *testing.T) {
	t.Parallel()
	store := newMockRunbookDocStore()
	tool := NewRunbookManageTool(store)
	ctx := ToolContext{Context: context.Background(), Role: auth.RoleAdmin}

	out := tool.Run(ctx, runbookInput(t, map[string]any{"action": "ensure_metadata", "id": "../bad"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "invalid")

	out = tool.Run(ctx, runbookInput(t, map[string]any{"action": "ensure_metadata", "id": "runbooks/missing"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "doc not found")

	content := "---\ntitle: Plain\ndescription: Plain runbook\n---\n\n# Plain\n"
	store.docs["runbooks/plain"] = docFromContent("runbooks/plain", content)
	noop := decodeRunbookOutput(t, tool.Run(ctx, runbookInput(t, map[string]any{
		"action":      "ensure_metadata",
		"id":          "runbooks/plain",
		"title":       "Plain",
		"description": "Plain runbook",
	})))
	assert.Equal(t, false, noop["updated"])

	store.updateErr = errRunbookForced
	out = tool.Run(ctx, runbookInput(t, map[string]any{
		"action":      "ensure_metadata",
		"id":          "runbooks/plain",
		"title":       "Different",
		"description": "Plain runbook",
	}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "forced runbook error")

	store.updateErr = nil
	store.docs["runbooks/fallback"] = &Doc{ID: "runbooks/fallback", Title: "Fallback", Description: "Fallback desc", Content: "# Fallback\n"}
	updated := decodeRunbookOutput(t, tool.Run(ctx, runbookInput(t, map[string]any{
		"action": "ensure_metadata",
		"id":     "runbooks/fallback",
	})))
	assert.Equal(t, true, updated["updated"])
	assert.Contains(t, store.docs["runbooks/fallback"].Content, "title: Fallback")

	out = runbookEnsureMetadata(ctx, store, runbookManageInput{ID: "../bad"})
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "invalid")
}

func TestRunbookManageHelpers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, defaultRunbookLimit, normalizeRunbookLimit(0))
	assert.Equal(t, maxRunbookLimit, normalizeRunbookLimit(maxRunbookLimit+1))
	assert.Equal(t, 7, normalizeRunbookLimit(7))

	assert.Empty(t, formatRunbookTime(time.Time{}))
	assert.NotEmpty(t, formatRunbookTime(time.Unix(1700000000, 0)))

	assert.Equal(t, "plain", sanitizeFrontmatterValue(" plain "))
	assert.Equal(t, `"needs: quoting"`, sanitizeFrontmatterValue("needs: quoting"))

	out := runbookJSON(func() {})
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "Failed to encode")

	deps := runbookManageDeps{
		workspaceStore: &mockRunbookWorkspaceStore{workspaces: []*workspacepkg.Workspace{{Name: "ops"}}},
	}
	workspaceName, err := runbookWorkspaceName(context.Background(), deps, "ops")
	require.NoError(t, err)
	assert.Empty(t, workspaceName)

	workspaceName, err = runbookWorkspaceName(context.Background(), deps, "other/runbook")
	require.NoError(t, err)
	assert.Empty(t, workspaceName)

	roots, err := runbookExcludedPathRoots(ToolContext{Context: context.Background()}, deps)
	require.NoError(t, err)
	assert.Nil(t, roots)
}

func TestRunbookManageTool_RejectsDeleteAndUpdateWithoutContent(t *testing.T) {
	t.Parallel()
	store := newMockRunbookDocStore()
	store.docs["runbooks/plain"] = &Doc{ID: "runbooks/plain", Title: "Plain", Content: "# Plain\n"}
	tool := NewRunbookManageTool(store)
	ctx := ToolContext{Context: context.Background(), Role: auth.RoleAdmin}

	out := tool.Run(ctx, runbookInput(t, map[string]any{"action": "delete", "id": "runbooks/plain"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "Unknown action")

	out = tool.Run(ctx, runbookInput(t, map[string]any{"action": "update", "id": "runbooks/plain", "title": "Renamed"}))
	assert.True(t, out.IsError)
	assert.Contains(t, out.Content, "content is required")
	assert.Equal(t, "# Plain\n", store.docs["runbooks/plain"].Content)
}
