// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/workspace"
)

type workspaceSelectionMode int

const (
	workspaceSelectionAll workspaceSelectionMode = iota
	workspaceSelectionDefault
	workspaceSelectionNamed
)

type workspaceSelection struct {
	mode      workspaceSelectionMode
	workspace string
	explicit  bool
}

const invalidWorkspaceLabelName = "\x00invalid-workspace-label"

func toAPIWorkspaceAccess(access *auth.WorkspaceAccess) api.WorkspaceAccess {
	normalized := auth.NormalizeWorkspaceAccess(access)
	grants := make([]api.WorkspaceGrant, 0, len(normalized.Grants))
	for _, grant := range normalized.Grants {
		grants = append(grants, api.WorkspaceGrant{
			Workspace: grant.Workspace,
			Role:      api.UserRole(grant.Role),
		})
	}
	return api.WorkspaceAccess{
		All:    normalized.All,
		Grants: grants,
	}
}

func fromAPIWorkspaceAccess(access *api.WorkspaceAccess) (*auth.WorkspaceAccess, error) {
	if access == nil {
		return auth.AllWorkspaceAccess(), nil
	}

	grants := make([]auth.WorkspaceGrant, 0, len(access.Grants))
	for _, grant := range access.Grants {
		role, err := auth.ParseRole(string(grant.Role))
		if err != nil {
			return nil, err
		}
		grants = append(grants, auth.WorkspaceGrant{
			Workspace: grant.Workspace,
			Role:      role,
		})
	}

	return &auth.WorkspaceAccess{
		All:    access.All,
		Grants: grants,
	}, nil
}

func (a *API) parseAndValidateWorkspaceAccess(
	ctx context.Context,
	role auth.Role,
	access *api.WorkspaceAccess,
) (*auth.WorkspaceAccess, error) {
	parsed, err := fromAPIWorkspaceAccess(access)
	if err != nil {
		return nil, badWorkspaceAccessError("Invalid workspace role")
	}

	var validationErr error
	if !auth.NormalizeWorkspaceAccess(parsed).All {
		if a.workspaceStore == nil {
			return nil, workspaceStoreUnavailable()
		}
		workspaces, err := a.workspaceStore.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list workspaces for access validation: %w", err)
		}
		names := make(map[string]struct{}, len(workspaces))
		for _, ws := range workspaces {
			names[ws.Name] = struct{}{}
		}
		validationErr = auth.ValidateWorkspaceAccess(role, parsed, func(name string) bool {
			_, ok := names[name]
			return ok
		})
	} else {
		validationErr = auth.ValidateWorkspaceAccess(role, parsed, nil)
	}
	if validationErr != nil {
		if errors.Is(validationErr, auth.ErrInvalidWorkspaceAccess) {
			return nil, badWorkspaceAccessError(validationErr.Error())
		}
		return nil, validationErr
	}

	return auth.CloneWorkspaceAccess(parsed), nil
}

func badWorkspaceAccessError(message string) *Error {
	return &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

func workspaceResourceNotFound() *Error {
	return &Error{
		Code:       api.ErrorCodeNotFound,
		Message:    "Resource not found",
		HTTPStatus: http.StatusNotFound,
	}
}

func badWorkspaceError(message string) *Error {
	return &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

func validateWorkspaceParam(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if err := workspace.ValidateName(name); err != nil {
		return "", badWorkspaceError(err.Error())
	}
	return name, nil
}

func parseWorkspaceSelection(workspaceParam *api.Workspace) (workspaceSelection, error) {
	if workspaceParam == nil {
		return workspaceSelection{mode: workspaceSelectionAll}, nil
	}
	raw := string(*workspaceParam)
	if raw == "" {
		return workspaceSelection{}, badWorkspaceError("workspace must not be empty")
	}
	switch raw {
	case "all":
		return workspaceSelection{mode: workspaceSelectionAll, explicit: true}, nil
	case "default":
		return workspaceSelection{mode: workspaceSelectionDefault, explicit: true}, nil
	default:
		workspaceName, err := validateWorkspaceParam(raw)
		if err != nil {
			return workspaceSelection{}, err
		}
		return workspaceSelection{
			mode:      workspaceSelectionNamed,
			workspace: workspaceName,
			explicit:  true,
		}, nil
	}
}

func workspaceParamFromValues(params url.Values) *api.Workspace {
	var workspaceParam *api.Workspace
	if rawValues, ok := params["workspace"]; ok {
		raw := ""
		if len(rawValues) > 0 {
			raw = rawValues[0]
		}
		workspace := api.Workspace(raw)
		workspaceParam = &workspace
	}
	return workspaceParam
}

func dagWorkspaceName(dag *core.DAG) string {
	if dag == nil {
		return ""
	}
	workspaceName, state := exec.WorkspaceLabelFromLabels(dag.Labels)
	switch state {
	case exec.WorkspaceLabelValid:
		return workspaceName
	case exec.WorkspaceLabelInvalid:
		return invalidWorkspaceLabelName
	case exec.WorkspaceLabelMissing:
		return ""
	}
	return ""
}

func statusWorkspaceName(status *exec.DAGRunStatus) string {
	if status == nil {
		return ""
	}
	workspaceName, state := exec.WorkspaceLabelFromLabels(core.NewLabels(status.Labels))
	switch state {
	case exec.WorkspaceLabelValid:
		return workspaceName
	case exec.WorkspaceLabelInvalid:
		return invalidWorkspaceLabelName
	case exec.WorkspaceLabelMissing:
		return ""
	}
	return ""
}

func workspaceNameFromLabelString(labels string) string {
	if strings.TrimSpace(labels) == "" {
		return ""
	}
	workspaceName, state := exec.WorkspaceLabelFromLabels(core.NewLabels(strings.Split(labels, ",")))
	switch state {
	case exec.WorkspaceLabelValid:
		return workspaceName
	case exec.WorkspaceLabelInvalid:
		return invalidWorkspaceLabelName
	case exec.WorkspaceLabelMissing:
		return ""
	}
	return ""
}

func runtimeWorkspaceName(dag *core.DAG, labels string) string {
	if workspaceName := workspaceNameFromLabelString(labels); workspaceName != "" {
		return workspaceName
	}
	return dagWorkspaceName(dag)
}

func (a *API) workspaceFilterForContext(ctx context.Context) *exec.WorkspaceFilter {
	if a.authService == nil {
		return nil
	}
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return &exec.WorkspaceFilter{Enabled: true}
	}
	access := auth.NormalizeWorkspaceAccess(user.WorkspaceAccess)
	if access.All {
		return nil
	}
	names := make([]string, 0, len(access.Grants))
	includeUnlabelled := true
	for _, grant := range access.Grants {
		if isMCPSourceContext(ctx) && grant.Workspace == "default" {
			continue
		}
		names = append(names, grant.Workspace)
	}
	if isMCPSourceContext(ctx) {
		includeUnlabelled = workspaceAccessHasGrant(access, "default")
	}
	return &exec.WorkspaceFilter{
		Enabled:           true,
		Workspaces:        names,
		IncludeUnlabelled: includeUnlabelled,
	}
}

func (a *API) workspaceFilterForSelection(ctx context.Context, selection workspaceSelection) (*exec.WorkspaceFilter, error) {
	switch selection.mode {
	case workspaceSelectionAll:
		return a.workspaceFilterForContext(ctx), nil
	case workspaceSelectionDefault:
		if err := a.requireWorkspaceVisible(ctx, ""); err != nil {
			return nil, err
		}
		return &exec.WorkspaceFilter{
			Enabled:           true,
			IncludeUnlabelled: true,
		}, nil
	case workspaceSelectionNamed:
		if err := a.requireWorkspaceVisible(ctx, selection.workspace); err != nil {
			return nil, err
		}
		return &exec.WorkspaceFilter{
			Enabled:    true,
			Workspaces: []string{selection.workspace},
		}, nil
	default:
		return nil, badWorkspaceError("invalid workspace")
	}
}

func (a *API) workspaceFilterForParams(ctx context.Context, workspaceParam *api.Workspace) (*exec.WorkspaceFilter, error) {
	selection, err := parseWorkspaceSelection(workspaceParam)
	if err != nil {
		return nil, err
	}
	return a.workspaceFilterForSelection(ctx, selection)
}

func (a *API) effectiveRoleForWorkspace(ctx context.Context, workspaceName string) (auth.Role, bool, error) {
	if a.authService == nil {
		return auth.RoleAdmin, true, nil
	}
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return auth.RoleNone, false, errAuthRequired
	}
	workspaceName = canonicalWorkspaceForAuth(ctx, workspaceName)
	role, ok := auth.EffectiveRole(user.Role, user.WorkspaceAccess, workspaceName)
	return role, ok, nil
}

func canonicalWorkspaceForAuth(ctx context.Context, workspaceName string) string {
	if isMCPSourceContext(ctx) && workspaceName == "" {
		return "default"
	}
	return workspaceName
}

func isMCPSourceContext(ctx context.Context) bool {
	source, ok := audit.SourceContextFromContext(ctx)
	return ok && source.Source == "mcp"
}

func workspaceAccessHasGrant(access auth.WorkspaceAccess, workspaceName string) bool {
	for _, grant := range access.Grants {
		if grant.Workspace == workspaceName {
			return true
		}
	}
	return false
}

func (a *API) canAccessWorkspace(ctx context.Context, workspaceName string) bool {
	_, ok, err := a.effectiveRoleForWorkspace(ctx, workspaceName)
	return err == nil && ok
}

func (a *API) requireWorkspaceVisible(ctx context.Context, workspaceName string) error {
	_, ok, err := a.effectiveRoleForWorkspace(ctx, workspaceName)
	if err != nil {
		return err
	}
	if !ok {
		return workspaceResourceNotFound()
	}
	return nil
}

func (a *API) requireDAGWriteForWorkspace(ctx context.Context, workspaceName string) error {
	if !a.config.Server.Permissions[config.PermissionWriteDAGs] {
		return errPermissionDenied
	}
	role, ok, err := a.effectiveRoleForWorkspace(ctx, workspaceName)
	if err != nil {
		return err
	}
	if !ok || !role.CanWrite() {
		return errInsufficientPermissions
	}
	if a.dagWritesDisabled {
		return errDAGWritesDisabled
	}
	return nil
}

func (a *API) requireExecuteForWorkspace(ctx context.Context, workspaceName string) error {
	role, ok, err := a.effectiveRoleForWorkspace(ctx, workspaceName)
	if err != nil {
		return err
	}
	if !ok || !role.CanExecute() {
		return errInsufficientPermissions
	}
	return nil
}

func (a *API) workspaceNameForDAGRun(ctx context.Context, dagRun exec.DAGRunRef) (string, error) {
	attempt, err := a.dagRunStore.FindAttempt(ctx, dagRun)
	if err != nil {
		return "", err
	}
	return workspaceNameForAttempt(ctx, attempt)
}

func workspaceNameForAttempt(ctx context.Context, attempt exec.DAGRunAttempt) (string, error) {
	status, err := attempt.ReadStatus(ctx)
	if err == nil && status != nil {
		if workspaceName := statusWorkspaceName(status); workspaceName != "" {
			return workspaceName, nil
		}
	}
	dag, dagErr := attempt.ReadDAG(ctx)
	if dagErr != nil {
		if err != nil {
			return "", err
		}
		return "", dagErr
	}
	return dagWorkspaceName(dag), nil
}

func (a *API) requireDAGRunVisible(ctx context.Context, dagRun exec.DAGRunRef) error {
	if a.authService == nil {
		return nil
	}
	workspaceName, err := a.workspaceNameForDAGRun(ctx, dagRun)
	if err != nil {
		return err
	}
	return a.requireWorkspaceVisible(ctx, workspaceName)
}

func (a *API) requireDAGRunStatusVisible(ctx context.Context, status *exec.DAGRunStatus) error {
	if status == nil {
		return nil
	}
	return a.requireWorkspaceVisible(ctx, statusWorkspaceName(status))
}

func (a *API) requireDAGRunStatusExecute(ctx context.Context, status *exec.DAGRunStatus) error {
	if status == nil {
		return nil
	}
	return a.requireExecuteForWorkspace(ctx, statusWorkspaceName(status))
}
