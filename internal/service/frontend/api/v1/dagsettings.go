// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	profilepkg "github.com/dagucloud/dagu/v2/internal/profile"
)

func dagSettingsStoreUnavailable() *Error {
	return &Error{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       api.ErrorCodeInternalError,
		Message:    "DAG settings store not configured",
	}
}

func dagSettingsBadRequest(message string) api.Error {
	return api.Error{
		Code:    api.ErrorCodeBadRequest,
		Message: message,
	}
}

// DAGProfileSelection reports the configured and effective profiles for a DAG.
type DAGProfileSelection struct {
	Configured string
	Effective  string
}

func (a *API) GetDAGSettings(ctx context.Context, request api.GetDAGSettingsRequestObject) (api.GetDAGSettingsResponseObject, error) {
	if a.dagSettingsStore == nil {
		return nil, dagSettingsStoreUnavailable()
	}
	if _, err := a.getDAGForSettings(ctx, request.FileName); err != nil {
		return nil, err
	}

	settings, err := a.dagSettingsStore.Get(ctx, request.FileName)
	if err != nil {
		if errors.Is(err, dagsettings.ErrNotFound) {
			return api.GetDAGSettings200JSONResponse(toAPIDAGSettings(request.FileName, nil)), nil
		}
		if errors.Is(err, dagsettings.ErrInvalidDAGName) {
			return api.GetDAGSettings404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("DAG %s not found", request.FileName),
			}, nil
		}
		return nil, fmt.Errorf("failed to get DAG settings: %w", err)
	}
	return api.GetDAGSettings200JSONResponse(toAPIDAGSettings(request.FileName, settings)), nil
}

func (a *API) UpdateDAGSettings(ctx context.Context, request api.UpdateDAGSettingsRequestObject) (api.UpdateDAGSettingsResponseObject, error) {
	if a.dagSettingsStore == nil {
		return nil, dagSettingsStoreUnavailable()
	}
	if request.Body == nil {
		return api.UpdateDAGSettings400JSONResponse(dagSettingsBadRequest("request body is required")), nil
	}
	profileName := ""
	if request.Body.Profile != nil {
		profileName = string(*request.Body.Profile)
	}
	profileName, err := a.ValidateDAGProfileUpdate(ctx, request.FileName, profileName)
	if err != nil {
		return nil, err
	}

	if profileName == "" {
		if err := a.dagSettingsStore.Delete(ctx, request.FileName); err != nil && !errors.Is(err, dagsettings.ErrNotFound) {
			return nil, fmt.Errorf("failed to clear DAG settings: %w", err)
		}
		a.logAudit(ctx, audit.CategoryDAG, "dag_settings_update", map[string]any{
			"dag_name": request.FileName,
			"profile":  "",
		})
		return api.UpdateDAGSettings200JSONResponse(toAPIDAGSettings(request.FileName, nil)), nil
	}

	now := time.Now().UTC()
	actor := currentActorID(ctx)
	settings, err := a.dagSettingsStore.Get(ctx, request.FileName)
	if errors.Is(err, dagsettings.ErrNotFound) {
		settings, err = dagsettings.New(dagsettings.UpdateInput{
			DAGName:   request.FileName,
			Profile:   profileName,
			UpdatedBy: actor,
		}, now)
	} else if err == nil {
		err = settings.ApplyUpdate(dagsettings.UpdateInput{
			DAGName:   request.FileName,
			Profile:   profileName,
			UpdatedBy: actor,
		}, now)
	}
	if err != nil {
		if errors.Is(err, dagsettings.ErrInvalidDAGName) || isRuntimeProfileValidationError(err) {
			return api.UpdateDAGSettings400JSONResponse(dagSettingsBadRequest(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to prepare DAG settings: %w", err)
	}

	if err := a.dagSettingsStore.Upsert(ctx, settings); err != nil {
		if errors.Is(err, dagsettings.ErrInvalidDAGName) || isRuntimeProfileValidationError(err) {
			return api.UpdateDAGSettings400JSONResponse(dagSettingsBadRequest(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to update DAG settings: %w", err)
	}

	a.logAudit(ctx, audit.CategoryDAG, "dag_settings_update", map[string]any{
		"dag_name": request.FileName,
		"profile":  profileName,
	})
	return api.UpdateDAGSettings200JSONResponse(toAPIDAGSettings(request.FileName, settings)), nil
}

func (a *API) DeleteDAGSettings(ctx context.Context, request api.DeleteDAGSettingsRequestObject) (api.DeleteDAGSettingsResponseObject, error) {
	if _, err := a.ValidateDAGProfileUpdate(ctx, request.FileName, ""); err != nil {
		return nil, err
	}

	if err := a.dagSettingsStore.Delete(ctx, request.FileName); err != nil && !errors.Is(err, dagsettings.ErrNotFound) {
		return nil, fmt.Errorf("failed to delete DAG settings: %w", err)
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_settings_delete", map[string]any{
		"dag_name": request.FileName,
	})
	return api.DeleteDAGSettings204Response{}, nil
}

// ValidateDAGProfileUpdate validates access and a selectable profile name.
func (a *API) ValidateDAGProfileUpdate(ctx context.Context, fileName, profileName string) (string, error) {
	if a.dagSettingsStore == nil {
		return "", dagSettingsStoreUnavailable()
	}
	if err := a.requireManagerOrAbove(ctx); err != nil {
		return "", err
	}
	if _, err := a.getDAGForSettings(ctx, fileName); err != nil {
		return "", err
	}
	return a.ensureRunnableRuntimeProfile(ctx, profileName)
}

// GetDAGProfileSelection returns the stored selection and resolved default.
func (a *API) GetDAGProfileSelection(ctx context.Context, fileName string) (DAGProfileSelection, error) {
	if a.dagSettingsStore == nil {
		return DAGProfileSelection{}, dagSettingsStoreUnavailable()
	}
	dag, err := a.getDAGForSettings(ctx, fileName)
	if err != nil {
		return DAGProfileSelection{}, err
	}

	selection := DAGProfileSelection{}
	settings, err := a.dagSettingsStore.Get(ctx, fileName)
	if err == nil {
		selection.Configured = settings.Profile
	} else if !errors.Is(err, dagsettings.ErrNotFound) {
		return DAGProfileSelection{}, fmt.Errorf("failed to get DAG settings: %w", err)
	}
	selection.Effective, err = a.defaultRunProfileName(ctx, fileName, dagWorkspaceName(dag))
	if err != nil {
		return DAGProfileSelection{}, err
	}
	return selection, nil
}

func (a *API) defaultRunProfileName(ctx context.Context, dagName string, workspaceName string) (string, error) {
	profileName, err := dagsettings.ResolveProfile(ctx, a.dagSettingsStore, a.profileStore, dagName, workspaceName)
	if err != nil {
		return "", a.defaultRunProfileError(err)
	}
	return profileName, nil
}

func (a *API) defaultRunProfileError(err error) error {
	name := ""
	if refErr, ok := errors.AsType[*dagsettings.ProfileReferenceError](err); ok {
		name = refErr.Name
	}
	if errors.Is(err, profilepkg.ErrInvalidName) {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}
	if errors.Is(err, profilepkg.ErrNotFound) {
		return &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("runtime profile %s not found", name),
		}
	}
	if errors.Is(err, profilepkg.ErrDisabled) {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    fmt.Sprintf("runtime profile %s is disabled", name),
		}
	}
	if errors.Is(err, dagsettings.ErrProfileStoreUnavailable) {
		return profileStoreUnavailable()
	}
	return err
}

func (a *API) runProfileForDAG(ctx context.Context, dagName string, workspaceName string, profile *api.RuntimeProfileOverride) (string, error) {
	profileName, explicit, err := a.explicitRunProfile(ctx, profile)
	if err != nil || explicit {
		return profileName, err
	}
	return a.defaultRunProfileName(ctx, dagName, workspaceName)
}

func (a *API) getDAGForSettings(ctx context.Context, fileName string) (*ir.DAG, error) {
	dag, err := a.dagRepository.GetMetadata(ctx, fileName)
	if err != nil {
		if errors.Is(err, persis.ErrDAGNotFound) {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("DAG %s not found", fileName),
			}
		}
		return nil, err
	}
	if err := a.requireWorkspaceVisible(ctx, dagWorkspaceName(dag)); err != nil {
		return nil, err
	}
	return dag, nil
}

func toAPIDAGSettings(dagName string, settings *dagsettings.Settings) api.DAGSettings {
	resp := api.DAGSettings{
		DagName: dagName,
	}
	if settings == nil {
		return resp
	}
	if settings.Profile != "" {
		profileName := api.RuntimeProfileName(settings.Profile)
		resp.Profile = &profileName
	}
	if !settings.UpdatedAt.IsZero() {
		resp.UpdatedAt = &settings.UpdatedAt
	}
	if settings.UpdatedBy != "" {
		resp.UpdatedBy = &settings.UpdatedBy
	}
	return resp
}

func (a *API) migrateDAGSettingsAfterRename(ctx context.Context, oldName, newName string) {
	if a.dagSettingsStore == nil {
		return
	}
	settings, err := a.dagSettingsStore.Get(ctx, oldName)
	if err != nil {
		if !errors.Is(err, dagsettings.ErrNotFound) {
			logger.Warn(ctx, "Failed to load DAG settings for rename",
				tag.Error(err),
				slog.String("old_name", oldName),
				slog.String("new_name", newName),
			)
		}
		return
	}
	migrated := settings.Clone()
	migrated.DAGName = newName
	migrated.UpdatedBy = currentActorID(ctx)
	migrated.UpdatedAt = time.Now().UTC()
	if err := a.dagSettingsStore.Upsert(ctx, migrated); err != nil {
		logger.Warn(ctx, "Failed to migrate DAG settings after rename",
			tag.Error(err),
			slog.String("old_name", oldName),
			slog.String("new_name", newName),
		)
		return
	}
	if err := a.dagSettingsStore.Delete(ctx, oldName); err != nil && !errors.Is(err, dagsettings.ErrNotFound) {
		logger.Warn(ctx, "Failed to remove old DAG settings after rename",
			tag.Error(err),
			slog.String("old_name", oldName),
			slog.String("new_name", newName),
		)
	}
}
