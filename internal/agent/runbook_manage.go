// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/llm"
	workspacepkg "github.com/dagucloud/dagu/internal/workspace"
)

const (
	runbookManageToolName = "runbook_manage"
	defaultRunbookLimit   = 20
	maxRunbookLimit       = 100
)

type runbookManageAction string

const (
	runbookActionList           runbookManageAction = "list"
	runbookActionSearch         runbookManageAction = "search"
	runbookActionGet            runbookManageAction = "get"
	runbookActionCreate         runbookManageAction = "create"
	runbookActionUpdate         runbookManageAction = "update"
	runbookActionPatch          runbookManageAction = "patch"
	runbookActionEnsureMetadata runbookManageAction = "ensure_metadata"
	runbookActionMove           runbookManageAction = "move"
	runbookActionDelete         runbookManageAction = "delete"
)

type runbookManageInput struct {
	Action      runbookManageAction `json:"action"`
	ID          string              `json:"id,omitempty" lenient:"true"`
	NewID       string              `json:"new_id,omitempty" lenient:"true"`
	Query       string              `json:"query,omitempty" lenient:"true"`
	Title       string              `json:"title,omitempty" lenient:"true"`
	Description string              `json:"description,omitempty" lenient:"true"`
	Content     string              `json:"content,omitempty" lenient:"true"`
	OldString   string              `json:"old_string,omitempty" lenient:"true"`
	NewString   string              `json:"new_string,omitempty" lenient:"true"`
	Limit       int                 `json:"limit,omitempty" lenient:"true"`
}

type runbookMetadataOutput struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

type runbookSearchOutput struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

type runbookGetOutput struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content"`
	Body        string `json:"body"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

type runbookManageDeps struct {
	docStore       DocStore
	workspaceStore workspacepkg.Store
}

func init() {
	RegisterTool(ToolRegistration{
		Name:           runbookManageToolName,
		Label:          "Runbook Manage",
		Description:    "List, search, read, create, update, move, and delete Markdown runbooks",
		DefaultEnabled: true,
		Factory: func(cfg ToolConfig) *AgentTool {
			return NewRunbookManageToolWithWorkspaceStore(cfg.DocStore, cfg.WorkspaceStore)
		},
	})
}

// NewRunbookManageTool creates a tool for managing Markdown runbooks stored in the docs store.
func NewRunbookManageTool(store DocStore) *AgentTool {
	return NewRunbookManageToolWithWorkspaceStore(store, nil)
}

// NewRunbookManageToolWithWorkspaceStore creates a runbook tool scoped by known workspaces.
func NewRunbookManageToolWithWorkspaceStore(store DocStore, workspaceStore workspacepkg.Store) *AgentTool {
	deps := runbookManageDeps{docStore: store, workspaceStore: workspaceStore}
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        runbookManageToolName,
				Description: "Manage Markdown runbooks and documents in the Dagu docs store. Use this before complex or operational tasks to discover relevant runbooks, read the selected runbook, and keep runbooks updated when instructions are missing, stale, or wrong. Use this tool, not patch, to move or delete docs-store documents. Runbooks use optional YAML frontmatter with title and description.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"enum":        []string{"list", "search", "get", "create", "update", "patch", "ensure_metadata", "move", "delete"},
							"description": "Operation to perform. Use list/search first, get before acting, patch or ensure_metadata to keep runbooks current, move to rename or relocate docs, and delete only when explicitly requested or confirmed.",
						},
						"id": map[string]any{
							"type":        "string",
							"description": "Runbook doc ID, relative path without .md, e.g. runbooks/deploy-production",
						},
						"new_id": map[string]any{
							"type":        "string",
							"description": "For move: destination doc ID, relative path without .md",
						},
						"query":       map[string]any{"type": "string", "description": "Search query"},
						"title":       map[string]any{"type": "string", "description": "Runbook title metadata"},
						"description": map[string]any{"type": "string", "description": "Runbook description metadata"},
						"content":     map[string]any{"type": "string", "description": "Markdown body or full content for create/update"},
						"old_string":  map[string]any{"type": "string", "description": "For patch: exact unique text to replace"},
						"new_string":  map[string]any{"type": "string", "description": "For patch: replacement text"},
						"limit":       map[string]any{"type": "integer", "description": "Maximum results for list/search (default 20, max 100)"},
					},
					"required": []string{"action"},
				},
			},
		},
		Run: func(ctx ToolContext, input json.RawMessage) ToolOut {
			return runbookManageRun(ctx, input, deps)
		},
		Audit: &AuditInfo{
			Action:          "runbook_manage",
			DetailExtractor: ExtractFields("action", "id", "new_id", "query"),
		},
	}
}

func runbookManageRun(ctx ToolContext, input json.RawMessage, deps runbookManageDeps) ToolOut {
	store := deps.docStore
	if store == nil {
		return toolError("runbook_manage is unavailable: doc store is not configured")
	}
	var args runbookManageInput
	if err := decodeToolInput(input, &args); err != nil {
		return toolError("Failed to parse input: %v", err)
	}
	if ctx.Context == nil {
		ctx.Context = defaultToolContext()
	}

	switch args.Action {
	case runbookActionList:
		return runbookList(ctx, deps, args)
	case runbookActionSearch:
		return runbookSearch(ctx, deps, args)
	case runbookActionGet:
		return runbookGet(ctx, deps, args)
	case runbookActionCreate:
		if out, denied := requireRunbookWrite(ctx, deps, args.ID); denied {
			return out
		}
		return runbookCreate(ctx, store, args)
	case runbookActionUpdate:
		if out, denied := requireRunbookWrite(ctx, deps, args.ID); denied {
			return out
		}
		return runbookUpdate(ctx, store, args)
	case runbookActionPatch:
		if out, denied := requireRunbookWrite(ctx, deps, args.ID); denied {
			return out
		}
		return runbookPatch(ctx, store, args)
	case runbookActionEnsureMetadata:
		if out, denied := requireRunbookWrite(ctx, deps, args.ID); denied {
			return out
		}
		return runbookEnsureMetadata(ctx, store, args)
	case runbookActionMove:
		if args.NewID == "" {
			return toolError("new_id is required for move")
		}
		if out, denied := requireRunbookWriteIDs(ctx, deps, args.ID, args.NewID); denied {
			return out
		}
		return runbookMove(ctx, store, args)
	case runbookActionDelete:
		if out, denied := requireRunbookWrite(ctx, deps, args.ID); denied {
			return out
		}
		return runbookDelete(ctx, store, args)
	default:
		return toolError("Unknown action: %s. Use list, search, get, create, update, patch, ensure_metadata, move, or delete.", args.Action)
	}
}

func defaultToolContext() context.Context { return context.Background() }

func requireRunbookWrite(ctx ToolContext, deps runbookManageDeps, id string) (ToolOut, bool) {
	return requireRunbookWriteIDs(ctx, deps, id)
}

func requireRunbookWriteIDs(ctx ToolContext, deps runbookManageDeps, ids ...string) (ToolOut, bool) {
	for _, id := range ids {
		if err := validateRunbookID(id); err != nil {
			return toolError("invalid runbook id: %v", err), true
		}
	}
	role, access, ok := runbookAuthContext(ctx)
	if ok {
		checked := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			workspaceName, err := runbookWorkspaceName(ctx.Context, deps, id)
			if err != nil {
				return toolError("Failed to resolve runbook workspace: %v", err), true
			}
			if _, ok := checked[workspaceName]; ok {
				continue
			}
			checked[workspaceName] = struct{}{}
			effectiveRole, ok := auth.EffectiveRole(role, access, workspaceName)
			if !ok || !effectiveRole.CanWrite() {
				return toolError("Permission denied: runbook_manage write actions require write permission for workspace %q", workspaceName), true
			}
		}
	}
	return ToolOut{}, false
}

func runbookAuthContext(ctx ToolContext) (auth.Role, *auth.WorkspaceAccess, bool) {
	role := ctx.Role
	var access *auth.WorkspaceAccess
	if ctx.User.UserID != "" {
		role = ctx.User.Role
		access = ctx.User.WorkspaceAccess
	}
	return role, access, role.IsSet()
}

func runbookKnownWorkspaces(ctx context.Context, deps runbookManageDeps) (map[string]struct{}, error) {
	if deps.workspaceStore == nil {
		return nil, nil
	}
	workspaces, err := deps.workspaceStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}
	known := make(map[string]struct{}, len(workspaces))
	for _, ws := range workspaces {
		known[ws.Name] = struct{}{}
	}
	return known, nil
}

func runbookWorkspaceName(ctx context.Context, deps runbookManageDeps, id string) (string, error) {
	known, err := runbookKnownWorkspaces(ctx, deps)
	if err != nil {
		return "", err
	}
	if len(known) == 0 {
		return "", nil
	}
	workspaceName, rest, hasSlash := strings.Cut(id, "/")
	if workspaceName == "" || !hasSlash || rest == "" {
		return "", nil
	}
	if _, ok := known[workspaceName]; ok {
		return workspaceName, nil
	}
	return "", nil
}

func runbookVisible(ctx ToolContext, deps runbookManageDeps, id string) (bool, error) {
	role, access, ok := runbookAuthContext(ctx)
	if !ok {
		return true, nil
	}
	workspaceName, err := runbookWorkspaceName(ctx.Context, deps, id)
	if err != nil {
		return false, err
	}
	_, allowed := auth.EffectiveRole(role, access, workspaceName)
	return allowed, nil
}

func runbookExcludedPathRoots(ctx ToolContext, deps runbookManageDeps) ([]string, error) {
	_, access, ok := runbookAuthContext(ctx)
	if !ok || deps.workspaceStore == nil {
		return nil, nil
	}
	normalized := auth.NormalizeWorkspaceAccess(access)
	if normalized.All {
		return nil, nil
	}
	known, err := runbookKnownWorkspaces(ctx.Context, deps)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(normalized.Grants))
	for _, grant := range normalized.Grants {
		allowed[grant.Workspace] = struct{}{}
	}
	roots := make([]string, 0, len(known))
	for name := range known {
		if _, ok := allowed[name]; !ok {
			roots = append(roots, name)
		}
	}
	sort.Strings(roots)
	return roots, nil
}

func runbookList(ctx ToolContext, deps runbookManageDeps, args runbookManageInput) ToolOut {
	limit := normalizeRunbookLimit(args.Limit)
	excludedRoots, err := runbookExcludedPathRoots(ctx, deps)
	if err != nil {
		return toolError("Failed to filter runbook workspaces: %v", err)
	}
	result, err := deps.docStore.ListFlat(ctx.Context, ListDocsOptions{PerPage: limit, ExcludePathRoots: excludedRoots})
	if err != nil {
		return toolError("Failed to list runbooks: %v", err)
	}
	items := result.Items
	if len(items) > limit {
		items = items[:limit]
	}
	runbooks := make([]runbookMetadataOutput, 0, len(items))
	for _, item := range items {
		visible, err := runbookVisible(ctx, deps, item.ID)
		if err != nil {
			return toolError("Failed to filter runbook workspaces: %v", err)
		}
		if !visible {
			continue
		}
		runbooks = append(runbooks, runbookMetadataOutput{ID: item.ID, Title: item.Title, Description: item.Description, UpdatedAt: formatRunbookTime(item.ModTime)})
	}
	return runbookJSON(map[string]any{"runbooks": runbooks})
}

func runbookSearch(ctx ToolContext, deps runbookManageDeps, args runbookManageInput) ToolOut {
	if strings.TrimSpace(args.Query) == "" {
		return toolError("query is required for search")
	}
	limit := normalizeRunbookLimit(args.Limit)
	results, err := deps.docStore.Search(ctx.Context, args.Query)
	if err != nil {
		return toolError("Failed to search runbooks: %v", err)
	}
	out := make([]runbookSearchOutput, 0, len(results))
	for _, result := range results {
		visible, err := runbookVisible(ctx, deps, result.ID)
		if err != nil {
			return toolError("Failed to filter runbook workspaces: %v", err)
		}
		if !visible {
			continue
		}
		out = append(out, runbookSearchOutput{ID: result.ID, Title: result.Title, Description: result.Description})
		if len(out) == limit {
			break
		}
	}
	return runbookJSON(map[string]any{"results": out})
}

func runbookGet(ctx ToolContext, deps runbookManageDeps, args runbookManageInput) ToolOut {
	if err := validateRunbookID(args.ID); err != nil {
		return toolError("invalid runbook id: %v", err)
	}
	visible, err := runbookVisible(ctx, deps, args.ID)
	if err != nil {
		return toolError("Failed to filter runbook workspace: %v", err)
	}
	if !visible {
		return toolError("Failed to get runbook %q: %v", args.ID, ErrDocNotFound)
	}
	doc, err := deps.docStore.Get(ctx.Context, args.ID)
	if err != nil {
		return toolError("Failed to get runbook %q: %v", args.ID, err)
	}
	return runbookJSON(runbookGetOutput{ID: doc.ID, Title: doc.Title, Description: doc.Description, Content: doc.Content, Body: stripMarkdownFrontmatter(doc.Content), UpdatedAt: doc.UpdatedAt})
}

func runbookCreate(ctx ToolContext, store DocStore, args runbookManageInput) ToolOut {
	if err := validateRunbookID(args.ID); err != nil {
		return toolError("invalid runbook id: %v", err)
	}
	content := normalizeRunbookContent(args.Content, args.Title, args.Description)
	if err := store.Create(ctx.Context, args.ID, content); err != nil {
		return toolError("Failed to create runbook %q: %v", args.ID, err)
	}
	return runbookJSON(map[string]any{"id": args.ID, "updated": true, "action": "created"})
}

func runbookUpdate(ctx ToolContext, store DocStore, args runbookManageInput) ToolOut {
	if err := validateRunbookID(args.ID); err != nil {
		return toolError("invalid runbook id: %v", err)
	}
	if args.Content == "" {
		return toolError("content is required for update")
	}
	content := args.Content
	if args.Title != "" || args.Description != "" {
		content = normalizeRunbookContent(content, args.Title, args.Description)
	}
	if err := store.Update(ctx.Context, args.ID, content); err != nil {
		return toolError("Failed to update runbook %q: %v", args.ID, err)
	}
	return runbookJSON(map[string]any{"id": args.ID, "updated": true, "action": "updated"})
}

func runbookPatch(ctx ToolContext, store DocStore, args runbookManageInput) ToolOut {
	if err := validateRunbookID(args.ID); err != nil {
		return toolError("invalid runbook id: %v", err)
	}
	if args.OldString == "" {
		return toolError("old_string is required for patch")
	}
	doc, err := store.Get(ctx.Context, args.ID)
	if err != nil {
		return toolError("Failed to get runbook %q: %v", args.ID, err)
	}
	count := strings.Count(doc.Content, args.OldString)
	if count == 0 {
		return toolError("old_string not found in runbook")
	}
	if count > 1 {
		return toolError("old_string found %d times in runbook; include more context so it is unique", count)
	}
	updated := strings.Replace(doc.Content, args.OldString, args.NewString, 1)
	if err := store.Update(ctx.Context, args.ID, updated); err != nil {
		return toolError("Failed to patch runbook %q: %v", args.ID, err)
	}
	return runbookJSON(map[string]any{"id": args.ID, "updated": true, "action": "patched"})
}

func runbookEnsureMetadata(ctx ToolContext, store DocStore, args runbookManageInput) ToolOut {
	if err := validateRunbookID(args.ID); err != nil {
		return toolError("invalid runbook id: %v", err)
	}
	doc, err := store.Get(ctx.Context, args.ID)
	if err != nil {
		return toolError("Failed to get runbook %q: %v", args.ID, err)
	}
	title := args.Title
	if title == "" {
		title = doc.Title
	}
	description := args.Description
	if description == "" {
		description = doc.Description
	}
	updated := upsertRunbookFrontmatter(doc.Content, title, description)
	if updated == doc.Content {
		return runbookJSON(map[string]any{"id": args.ID, "updated": false, "action": "ensure_metadata"})
	}
	if err := store.Update(ctx.Context, args.ID, updated); err != nil {
		return toolError("Failed to update runbook metadata %q: %v", args.ID, err)
	}
	return runbookJSON(map[string]any{"id": args.ID, "updated": true, "action": "ensure_metadata"})
}

func runbookMove(ctx ToolContext, store DocStore, args runbookManageInput) ToolOut {
	if err := validateRunbookID(args.ID); err != nil {
		return toolError("invalid runbook id: %v", err)
	}
	if args.NewID == "" {
		return toolError("new_id is required for move")
	}
	if err := validateRunbookID(args.NewID); err != nil {
		return toolError("invalid destination runbook id: %v", err)
	}
	if err := store.Rename(ctx.Context, args.ID, args.NewID); err != nil {
		return toolError("Failed to move runbook %q to %q: %v", args.ID, args.NewID, err)
	}
	return runbookJSON(map[string]any{"id": args.NewID, "old_id": args.ID, "updated": true, "action": "moved"})
}

func runbookDelete(ctx ToolContext, store DocStore, args runbookManageInput) ToolOut {
	if err := validateRunbookID(args.ID); err != nil {
		return toolError("invalid runbook id: %v", err)
	}
	if err := store.Delete(ctx.Context, args.ID); err != nil {
		return toolError("Failed to delete runbook %q: %v", args.ID, err)
	}
	return runbookJSON(map[string]any{"id": args.ID, "updated": true, "action": "deleted"})
}

func validateRunbookID(id string) error {
	return ValidateDocID(id)
}

func normalizeRunbookLimit(limit int) int {
	if limit <= 0 {
		return defaultRunbookLimit
	}
	if limit > maxRunbookLimit {
		return maxRunbookLimit
	}
	return limit
}

func normalizeRunbookContent(content, title, description string) string {
	body := stripMarkdownFrontmatter(content)
	return buildRunbookContent(title, description, body)
}

func buildRunbookContent(title, description, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	if title != "" {
		b.WriteString("title: ")
		b.WriteString(sanitizeFrontmatterValue(title))
		b.WriteString("\n")
	}
	b.WriteString("description: ")
	b.WriteString(sanitizeFrontmatterValue(description))
	b.WriteString("\n---\n\n")
	b.WriteString(strings.TrimLeft(body, "\r\n"))
	return b.String()
}

func upsertRunbookFrontmatter(content, title, description string) string {
	body := stripMarkdownFrontmatter(content)
	return buildRunbookContent(title, description, body)
}

var markdownFrontmatterRE = regexp.MustCompile(`^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)`)

func stripMarkdownFrontmatter(content string) string {
	return markdownFrontmatterRE.ReplaceAllString(content, "")
}

func sanitizeFrontmatterValue(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, `:#{}[],&*?|-<>=!%@\\"'`) {
		return fmt.Sprintf("%q", value)
	}
	return value
}

func formatRunbookTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func runbookJSON(v any) ToolOut {
	data, err := json.Marshal(v)
	if err != nil {
		return toolError("Failed to encode runbook_manage output: %v", err)
	}
	return ToolOut{Content: string(data)}
}
