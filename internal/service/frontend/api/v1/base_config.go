// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/persis/filebaseconfig"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/workspace"
)

var (
	// ErrBaseConfigNotAvailable is returned when base config management is not configured.
	ErrBaseConfigNotAvailable = &Error{
		Code:       api.ErrorCodeForbidden,
		Message:    "Base configuration management is not available",
		HTTPStatus: http.StatusForbidden,
	}

	// ErrFailedToLoadBaseConfig is returned when reading the base config fails.
	ErrFailedToLoadBaseConfig = &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    "Failed to load base configuration",
		HTTPStatus: http.StatusInternalServerError,
	}

	// ErrFailedToSaveBaseConfig is returned when writing the base config fails.
	ErrFailedToSaveBaseConfig = &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    "Failed to save base configuration",
		HTTPStatus: http.StatusInternalServerError,
	}

	// ErrFailedToLoadWorkspaceBaseConfig is returned when reading workspace config fails.
	ErrFailedToLoadWorkspaceBaseConfig = &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    "Failed to load workspace base configuration",
		HTTPStatus: http.StatusInternalServerError,
	}

	// ErrFailedToSaveWorkspaceBaseConfig is returned when writing workspace config fails.
	ErrFailedToSaveWorkspaceBaseConfig = &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    "Failed to save workspace base configuration",
		HTTPStatus: http.StatusInternalServerError,
	}
)

// GetBaseConfig returns the current base DAG configuration YAML. Requires developer role or above.
func (a *API) GetBaseConfig(ctx context.Context, _ api.GetBaseConfigRequestObject) (api.GetBaseConfigResponseObject, error) {
	if err := a.requireBaseConfigManagement(); err != nil {
		return nil, err
	}
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return nil, err
	}

	yamlSpec, err := a.baseConfigStore.GetSpec(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to load base config", tag.Error(err))
		return nil, ErrFailedToLoadBaseConfig
	}

	errs := validateBaseConfig(ctx, yamlSpec)
	if errs == nil {
		errs = []string{}
	}

	return api.GetBaseConfig200JSONResponse{
		Spec:   yamlSpec,
		Errors: errs,
	}, nil
}

// UpdateBaseConfig validates and saves the base DAG configuration YAML. Requires developer role or above.
func (a *API) UpdateBaseConfig(ctx context.Context, request api.UpdateBaseConfigRequestObject) (api.UpdateBaseConfigResponseObject, error) {
	if err := a.requireBaseConfigManagement(); err != nil {
		return nil, err
	}
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}

	// Validate before saving — reject on validation error.
	if errs := validateBaseConfig(ctx, request.Body.Spec); len(errs) > 0 {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    strings.Join(errs, "; "),
			HTTPStatus: http.StatusBadRequest,
		}
	}

	if err := a.baseConfigStore.UpdateSpec(ctx, []byte(request.Body.Spec)); err != nil {
		logger.Error(ctx, "Failed to save base config", tag.Error(err))
		return nil, ErrFailedToSaveBaseConfig
	}

	a.logAudit(ctx, audit.CategorySystem, "base_config_update", nil)

	return api.UpdateBaseConfig200JSONResponse{
		Errors: []string{},
	}, nil
}

// GetWorkspaceBaseConfig returns the workspace-scoped base DAG configuration YAML.
func (a *API) GetWorkspaceBaseConfig(
	ctx context.Context,
	request api.GetWorkspaceBaseConfigRequestObject,
) (api.GetWorkspaceBaseConfigResponseObject, error) {
	workspaceName, err := a.resolveWorkspaceBaseConfigTarget(ctx, request.WorkspaceName)
	if err != nil {
		return nil, err
	}
	if err := a.requireWorkspaceVisible(ctx, workspaceName); err != nil {
		return nil, err
	}

	yamlSpec, err := readWorkspaceBaseConfigSpec(a.config.Paths.DAGsDir, workspaceName)
	if err != nil {
		logger.Error(ctx, "Failed to load workspace base config", tag.Name(workspaceName), tag.Error(err))
		return nil, ErrFailedToLoadWorkspaceBaseConfig
	}

	errs := validateBaseConfig(ctx, yamlSpec)
	if errs == nil {
		errs = []string{}
	}

	return api.GetWorkspaceBaseConfig200JSONResponse{
		Spec:   yamlSpec,
		Errors: errs,
	}, nil
}

// UpdateWorkspaceBaseConfig validates and saves a workspace-scoped base DAG configuration YAML.
func (a *API) UpdateWorkspaceBaseConfig(
	ctx context.Context,
	request api.UpdateWorkspaceBaseConfigRequestObject,
) (api.UpdateWorkspaceBaseConfigResponseObject, error) {
	workspaceName, err := a.resolveWorkspaceBaseConfigTarget(ctx, request.WorkspaceName)
	if err != nil {
		return nil, err
	}
	if err := a.requireWorkspaceConfigWrite(ctx, workspaceName); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}

	if errs := validateBaseConfig(ctx, request.Body.Spec); len(errs) > 0 {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    strings.Join(errs, "; "),
			HTTPStatus: http.StatusBadRequest,
		}
	}

	store, err := workspaceBaseConfigStore(a.config.Paths.DAGsDir, workspaceName)
	if err != nil {
		logger.Error(ctx, "Failed to initialize workspace base config store", tag.Name(workspaceName), tag.Error(err))
		return nil, ErrFailedToSaveWorkspaceBaseConfig
	}
	if err := store.UpdateSpec(ctx, []byte(request.Body.Spec)); err != nil {
		logger.Error(ctx, "Failed to save workspace base config", tag.Name(workspaceName), tag.Error(err))
		return nil, ErrFailedToSaveWorkspaceBaseConfig
	}

	a.logAudit(ctx, audit.CategoryWorkspace, "workspace_base_config_update", map[string]string{
		"workspace": workspaceName,
	})

	return api.UpdateWorkspaceBaseConfig200JSONResponse{
		Errors: []string{},
	}, nil
}

func (a *API) requireBaseConfigManagement() error {
	if a.baseConfigStore == nil {
		return ErrBaseConfigNotAvailable
	}
	return nil
}

func (a *API) resolveWorkspaceBaseConfigTarget(ctx context.Context, name string) (string, error) {
	if err := a.requireBaseConfigManagement(); err != nil {
		return "", err
	}
	if a.workspaceStore == nil {
		return "", workspaceStoreUnavailable()
	}
	workspaceName, err := validateWorkspaceParam(name)
	if err != nil {
		return "", err
	}
	if workspaceName == "" {
		return "", badWorkspaceError("workspace name is required")
	}

	ws, err := a.workspaceStore.GetByName(ctx, workspaceName)
	if err != nil {
		if errors.Is(err, workspace.ErrWorkspaceNotFound) {
			return "", workspaceResourceNotFound()
		}
		return "", err
	}
	return ws.Name, nil
}

func (a *API) requireWorkspaceConfigWrite(ctx context.Context, workspaceName string) error {
	role, ok, err := a.effectiveRoleForWorkspace(ctx, workspaceName)
	if err != nil {
		return err
	}
	if !ok {
		return workspaceResourceNotFound()
	}
	if !role.CanWrite() {
		return errInsufficientPermissions
	}
	return nil
}

func readWorkspaceBaseConfigSpec(dagsDir, workspaceName string) (string, error) {
	data, err := os.ReadFile(workspace.BaseConfigPath(dagsDir, workspaceName)) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func workspaceBaseConfigStore(dagsDir, workspaceName string) (*filebaseconfig.Store, error) {
	return filebaseconfig.New(
		workspace.BaseConfigPath(dagsDir, workspaceName),
		filebaseconfig.WithSkipDefault(true),
	)
}

// validateBaseConfig parses the YAML spec and returns any validation errors.
// Returns nil when the spec is empty or valid.
func validateBaseConfig(ctx context.Context, yamlSpec string) []string {
	if yamlSpec == "" {
		return nil
	}

	_, err := spec.LoadYAML(ctx,
		[]byte(yamlSpec),
		spec.WithoutEval(),
	)

	var loadErrs core.ErrorList
	if errors.As(err, &loadErrs) {
		return loadErrs.ToStringList()
	}
	if err != nil {
		return []string{err.Error()}
	}

	return nil
}
