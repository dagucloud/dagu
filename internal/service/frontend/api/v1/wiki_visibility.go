// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/wiki"
)

func validateWikiPagePath(path string) error {
	if err := wiki.ValidatePageID(path); err != nil {
		return &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    fmt.Sprintf("invalid page path: %v", err),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return nil
}

func scopedWikiPagePath(workspaceName, path string) (string, error) {
	if err := validateWikiPagePath(path); err != nil {
		return "", err
	}
	if workspaceName == "" {
		return path, nil
	}
	scoped := workspaceName + "/" + path
	if err := validateWikiPagePath(scoped); err != nil {
		return "", err
	}
	return scoped, nil
}

func scopedWikiPageListPrefix(workspaceName, prefix string) (string, error) {
	if prefix == "" {
		return workspaceName, nil
	}
	return scopedWikiPagePath(workspaceName, prefix)
}

func visibleWikiPageListPath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	return prefix + "/" + path
}

func restoreWikiTreePrefix(node *wiki.PageTreeNode, prefix string) {
	node.ID = visibleWikiPageListPath(prefix, node.ID)
	for _, child := range node.Children {
		restoreWikiTreePrefix(child, prefix)
	}
}

func visibleWikiPagePath(workspaceName, path string) string {
	if workspaceName == "" {
		return path
	}
	return strings.TrimPrefix(path, workspaceName+"/")
}

type wikiWorkspaceVisibility struct {
	all     bool
	allowed map[string]struct{}
	known   map[string]struct{}
}

func (a *API) knownWikiWorkspaceNames(ctx context.Context, required bool) (map[string]struct{}, error) {
	if a.workspaceStore == nil {
		if required {
			return nil, workspaceStoreUnavailable()
		}
		return nil, nil
	}
	workspaces, err := a.workspaceStore.List(ctx)
	if err != nil {
		if required {
			return nil, fmt.Errorf("failed to list workspaces: %w", err)
		}
		return nil, nil
	}
	known := make(map[string]struct{}, len(workspaces))
	for _, ws := range workspaces {
		known[ws.Name] = struct{}{}
	}
	return known, nil
}

func (a *API) wikiWorkspaceVisibility(ctx context.Context) (wikiWorkspaceVisibility, error) {
	visibility := wikiWorkspaceVisibility{all: true}
	known, err := a.knownWikiWorkspaceNames(ctx, a.workspaceStore != nil)
	if err != nil {
		return visibility, err
	}
	visibility.known = known
	if a.authService == nil {
		return visibility, nil
	}
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return visibility, errAuthRequired
	}
	access := auth.NormalizeWorkspaceAccess(user.WorkspaceAccess)
	if access.All {
		return visibility, nil
	}
	known, err = a.knownWikiWorkspaceNames(ctx, true)
	if err != nil {
		return visibility, err
	}
	visibility.all = false
	visibility.allowed = make(map[string]struct{}, len(access.Grants))
	visibility.known = known
	for _, grant := range access.Grants {
		visibility.allowed[grant.Workspace] = struct{}{}
	}
	return visibility, nil
}

func (a *API) noWorkspaceWikiVisibility(ctx context.Context) (wikiWorkspaceVisibility, error) {
	known, err := a.knownWikiWorkspaceNames(ctx, a.workspaceStore != nil)
	if err != nil {
		return wikiWorkspaceVisibility{}, err
	}
	return wikiWorkspaceVisibility{
		allowed: make(map[string]struct{}),
		known:   known,
	}, nil
}

func (a *API) wikiWorkspaceVisibilityForSelection(ctx context.Context, selection workspaceSelection) (wikiWorkspaceVisibility, error) {
	switch selection.mode {
	case workspaceSelectionAll:
		return a.wikiWorkspaceVisibility(ctx)
	case workspaceSelectionDefault:
		return a.noWorkspaceWikiVisibility(ctx)
	case workspaceSelectionNamed:
		if err := a.requireWorkspaceVisible(ctx, selection.workspace); err != nil {
			return wikiWorkspaceVisibility{}, err
		}
		return wikiWorkspaceVisibility{all: true}, nil
	default:
		return wikiWorkspaceVisibility{}, badWorkspaceError("invalid workspace")
	}
}

func (a *API) wikiReadScopeForParams(
	ctx context.Context,
	workspaceParam *api.Workspace,
) (string, wikiWorkspaceVisibility, error) {
	selection, err := parseWorkspaceSelection(workspaceParam)
	if err != nil {
		return "", wikiWorkspaceVisibility{}, err
	}
	visibility, err := a.wikiWorkspaceVisibilityForSelection(ctx, selection)
	if err != nil {
		return "", wikiWorkspaceVisibility{}, err
	}
	if selection.mode == workspaceSelectionNamed {
		return selection.workspace, visibility, nil
	}
	return "", visibility, nil
}

func wikiTargetWorkspaceForParam(workspaceParam *api.Workspace) (string, error) {
	if workspaceParam == nil {
		return "", nil
	}
	raw := string(*workspaceParam)
	if raw == "" {
		return "", badWorkspaceError("workspace must not be empty")
	}
	switch raw {
	case "all":
		return "", badWorkspaceError("workspace=all cannot target a single Wiki page")
	case "default":
		return "", nil
	default:
		return validateWorkspaceParam(raw)
	}
}

func (a *API) wikiPointReadScopeForParams(
	ctx context.Context,
	workspaceParam *api.Workspace,
) (string, wikiWorkspaceVisibility, error) {
	workspaceName, err := wikiTargetWorkspaceForParam(workspaceParam)
	if err != nil {
		return "", wikiWorkspaceVisibility{}, err
	}
	if workspaceName == "" {
		visibility, err := a.noWorkspaceWikiVisibility(ctx)
		if err != nil {
			return "", wikiWorkspaceVisibility{}, err
		}
		return "", visibility, nil
	}
	if err := a.requireWorkspaceVisible(ctx, workspaceName); err != nil {
		return "", wikiWorkspaceVisibility{}, err
	}
	return workspaceName, wikiWorkspaceVisibility{all: true}, nil
}

func wikiMutationScopeForParams(workspaceParam *api.Workspace) (string, error) {
	return wikiTargetWorkspaceForParam(workspaceParam)
}

func (a *API) scopedWikiPageMutationPath(ctx context.Context, workspaceName, path string) (string, error) {
	if workspaceName == "" {
		known, err := a.knownWikiWorkspaceNames(ctx, a.workspaceStore != nil)
		if err != nil {
			return "", err
		}
		if wikiWorkspaceNameForPath(path, wikiWorkspaceVisibility{known: known}, true) != "" {
			return "", badWorkspaceError("path targets a workspace; set workspace")
		}
	}
	return scopedWikiPagePath(workspaceName, path)
}

func (v wikiWorkspaceVisibility) knownWorkspace(name string) bool {
	if name == "" {
		return false
	}
	if v.known != nil {
		_, ok := v.known[name]
		return ok
	}
	if v.allowed != nil {
		_, ok := v.allowed[name]
		return ok
	}
	return false
}

func wikiWorkspaceNameForPath(path string, visibility wikiWorkspaceVisibility, includeWorkspaceRoot bool) string {
	workspaceName, rest, hasSlash := strings.Cut(path, "/")
	if workspaceName == "" {
		return ""
	}
	if !hasSlash && !includeWorkspaceRoot {
		return ""
	}
	if hasSlash && rest == "" {
		return ""
	}
	if visibility.knownWorkspace(workspaceName) {
		return workspaceName
	}
	return ""
}

func wikiWorkspaceValue(workspaceName, path string, visibility wikiWorkspaceVisibility, includeWorkspaceRoot bool) *string {
	if workspaceName != "" {
		return ptrOf(workspaceName)
	}
	return optionalString(wikiWorkspaceNameForPath(path, visibility, includeWorkspaceRoot))
}

func (v wikiWorkspaceVisibility) visible(path string) bool {
	if v.all {
		return true
	}
	workspaceName, _, _ := strings.Cut(path, "/")
	if workspaceName == "" {
		return true
	}
	if _, ok := v.known[workspaceName]; !ok {
		return true
	}
	_, ok := v.allowed[workspaceName]
	return ok
}

func (v wikiWorkspaceVisibility) excludedPathRoots() []string {
	if v.all || len(v.known) == 0 {
		return nil
	}
	roots := make([]string, 0, len(v.known))
	for name := range v.known {
		if _, ok := v.allowed[name]; !ok {
			roots = append(roots, name)
		}
	}
	sort.Strings(roots)
	return roots
}
