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
	"github.com/dagucloud/dagu/v2/internal/core/docs"
)

func validateDocPath(path string) error {
	if err := docs.ValidateDocID(path); err != nil {
		return &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    fmt.Sprintf("invalid doc path: %v", err),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return nil
}

func scopedDocPath(workspaceName, path string) (string, error) {
	if err := validateDocPath(path); err != nil {
		return "", err
	}
	if workspaceName == "" {
		return path, nil
	}
	scoped := workspaceName + "/" + path
	if err := validateDocPath(scoped); err != nil {
		return "", err
	}
	return scoped, nil
}

func scopedDocListPrefix(workspaceName, prefix string) (string, error) {
	if prefix == "" {
		return workspaceName, nil
	}
	return scopedDocPath(workspaceName, prefix)
}

func visibleDocListPath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	return prefix + "/" + path
}

func restoreDocTreePrefix(node *docs.DocTreeNode, prefix string) {
	node.ID = visibleDocListPath(prefix, node.ID)
	for _, child := range node.Children {
		restoreDocTreePrefix(child, prefix)
	}
}

func visibleDocPath(workspaceName, path string) string {
	if workspaceName == "" {
		return path
	}
	return strings.TrimPrefix(path, workspaceName+"/")
}

type docWorkspaceVisibility struct {
	all     bool
	allowed map[string]struct{}
	known   map[string]struct{}
}

func (a *API) knownDocWorkspaceNames(ctx context.Context, required bool) (map[string]struct{}, error) {
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

func (a *API) docWorkspaceVisibility(ctx context.Context) (docWorkspaceVisibility, error) {
	visibility := docWorkspaceVisibility{all: true}
	known, err := a.knownDocWorkspaceNames(ctx, a.workspaceStore != nil)
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
	known, err = a.knownDocWorkspaceNames(ctx, true)
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

func (a *API) noWorkspaceDocVisibility(ctx context.Context) (docWorkspaceVisibility, error) {
	known, err := a.knownDocWorkspaceNames(ctx, a.workspaceStore != nil)
	if err != nil {
		return docWorkspaceVisibility{}, err
	}
	return docWorkspaceVisibility{
		allowed: make(map[string]struct{}),
		known:   known,
	}, nil
}

func (a *API) docWorkspaceVisibilityForSelection(ctx context.Context, selection workspaceSelection) (docWorkspaceVisibility, error) {
	switch selection.mode {
	case workspaceSelectionAll:
		return a.docWorkspaceVisibility(ctx)
	case workspaceSelectionDefault:
		return a.noWorkspaceDocVisibility(ctx)
	case workspaceSelectionNamed:
		if err := a.requireWorkspaceVisible(ctx, selection.workspace); err != nil {
			return docWorkspaceVisibility{}, err
		}
		return docWorkspaceVisibility{all: true}, nil
	default:
		return docWorkspaceVisibility{}, badWorkspaceError("invalid workspace")
	}
}

func (a *API) docReadScopeForParams(
	ctx context.Context,
	workspaceParam *api.Workspace,
) (string, docWorkspaceVisibility, error) {
	selection, err := parseWorkspaceSelection(workspaceParam)
	if err != nil {
		return "", docWorkspaceVisibility{}, err
	}
	visibility, err := a.docWorkspaceVisibilityForSelection(ctx, selection)
	if err != nil {
		return "", docWorkspaceVisibility{}, err
	}
	if selection.mode == workspaceSelectionNamed {
		return selection.workspace, visibility, nil
	}
	return "", visibility, nil
}

func docTargetWorkspaceForParam(workspaceParam *api.Workspace) (string, error) {
	if workspaceParam == nil {
		return "", nil
	}
	raw := string(*workspaceParam)
	if raw == "" {
		return "", badWorkspaceError("workspace must not be empty")
	}
	switch raw {
	case "all":
		return "", badWorkspaceError("workspace=all cannot target a single document")
	case "default":
		return "", nil
	default:
		return validateWorkspaceParam(raw)
	}
}

func (a *API) docPointReadScopeForParams(
	ctx context.Context,
	workspaceParam *api.Workspace,
) (string, docWorkspaceVisibility, error) {
	workspaceName, err := docTargetWorkspaceForParam(workspaceParam)
	if err != nil {
		return "", docWorkspaceVisibility{}, err
	}
	if workspaceName == "" {
		visibility, err := a.noWorkspaceDocVisibility(ctx)
		if err != nil {
			return "", docWorkspaceVisibility{}, err
		}
		return "", visibility, nil
	}
	if err := a.requireWorkspaceVisible(ctx, workspaceName); err != nil {
		return "", docWorkspaceVisibility{}, err
	}
	return workspaceName, docWorkspaceVisibility{all: true}, nil
}

func docMutationScopeForParams(workspaceParam *api.Workspace) (string, error) {
	return docTargetWorkspaceForParam(workspaceParam)
}

func (a *API) scopedDocMutationPath(ctx context.Context, workspaceName, path string) (string, error) {
	if workspaceName == "" {
		known, err := a.knownDocWorkspaceNames(ctx, a.workspaceStore != nil)
		if err != nil {
			return "", err
		}
		if docWorkspaceNameForPath(path, docWorkspaceVisibility{known: known}, true) != "" {
			return "", badWorkspaceError("path targets a workspace; set workspace")
		}
	}
	return scopedDocPath(workspaceName, path)
}

func (v docWorkspaceVisibility) knownWorkspace(name string) bool {
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

func docWorkspaceNameForPath(path string, visibility docWorkspaceVisibility, includeWorkspaceRoot bool) string {
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

func docWorkspaceValue(workspaceName, path string, visibility docWorkspaceVisibility, includeWorkspaceRoot bool) *string {
	if workspaceName != "" {
		return ptrOf(workspaceName)
	}
	return optionalString(docWorkspaceNameForPath(path, visibility, includeWorkspaceRoot))
}

func (v docWorkspaceVisibility) visible(path string) bool {
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

func (v docWorkspaceVisibility) excludedPathRoots() []string {
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
