// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	profilepkg "github.com/dagucloud/dagu/internal/profile"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/workspace"
)

func profileStoreUnavailable() *Error {
	return &Error{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       api.ErrorCodeInternalError,
		Message:    "Runtime profile store not configured",
	}
}

func runtimeProfileBadRequest(message string) api.Error {
	return api.Error{
		Code:    api.ErrorCodeBadRequest,
		Message: message,
	}
}

func runtimeProfileNotFound() api.Error {
	return api.Error{
		Code:    api.ErrorCodeNotFound,
		Message: "Runtime profile not found",
	}
}

func runtimeProfileConflict(message string) api.Error {
	return api.Error{
		Code:    api.ErrorCodeAlreadyExists,
		Message: message,
	}
}

func serviceAPIError(err error) *api.Error {
	var serviceErr *Error
	if errors.As(err, &serviceErr) {
		return &api.Error{
			Code:    serviceErr.Code,
			Message: serviceErr.Message,
		}
	}
	return &api.Error{
		Code:    api.ErrorCodeBadRequest,
		Message: err.Error(),
	}
}

func (a *API) requireRuntimeProfileManage(ctx context.Context, item *profilepkg.Profile) error {
	if item.Protected {
		return a.requireAdmin(ctx)
	}
	return a.requireManagerOrAbove(ctx)
}

func (a *API) requireRuntimeProfileUpdate(ctx context.Context, item *profilepkg.Profile, protectedRequested bool) error {
	if item.Protected || protectedRequested {
		return a.requireAdmin(ctx)
	}
	return a.requireManagerOrAbove(ctx)
}

func (a *API) requireRuntimeProfileView(ctx context.Context, item *profilepkg.Profile) error {
	if item.Protected {
		return a.requireAdmin(ctx)
	}
	return nil
}

func (a *API) ListRuntimeProfiles(ctx context.Context, _ api.ListRuntimeProfilesRequestObject) (api.ListRuntimeProfilesResponseObject, error) {
	if a.profileStore == nil {
		return nil, profileStoreUnavailable()
	}
	if err := a.requireManagerOrAbove(ctx); err != nil {
		return nil, err
	}

	profiles, err := a.profileStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list runtime profiles: %w", err)
	}

	includeProtected := a.requireAdmin(ctx) == nil
	out := make([]api.RuntimeProfileResponse, 0, len(profiles))
	for _, item := range profiles {
		if item.Protected && !includeProtected {
			continue
		}
		out = append(out, toRuntimeProfileResponse(item))
	}
	return api.ListRuntimeProfiles200JSONResponse{
		Profiles: out,
	}, nil
}

func (a *API) CreateRuntimeProfile(ctx context.Context, request api.CreateRuntimeProfileRequestObject) (api.CreateRuntimeProfileResponseObject, error) {
	if a.profileStore == nil {
		return nil, profileStoreUnavailable()
	}
	if err := a.requireManagerOrAbove(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return api.CreateRuntimeProfile400JSONResponse(runtimeProfileBadRequest("Request body is required")), nil
	}
	protected := valueOf(request.Body.Protected)
	if protected {
		if err := a.requireAdmin(ctx); err != nil {
			return nil, err
		}
	}

	now := time.Now().UTC()
	actor := currentActorID(ctx)
	item, err := profilepkg.New(profilepkg.CreateInput{
		Name:        strings.TrimSpace(request.Body.Name),
		Description: valueOf(request.Body.Description),
		Protected:   protected,
		CreatedBy:   actor,
	}, now)
	if err != nil {
		return api.CreateRuntimeProfile400JSONResponse(runtimeProfileBadRequest(err.Error())), nil
	}

	if err := a.profileStore.Create(ctx, item); err != nil {
		if errors.Is(err, profilepkg.ErrAlreadyExists) {
			return api.CreateRuntimeProfile409JSONResponse(runtimeProfileConflict("Runtime profile already exists")), nil
		}
		if isRuntimeProfileValidationError(err) {
			return api.CreateRuntimeProfile400JSONResponse(runtimeProfileBadRequest(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to create runtime profile: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "runtime_profile_create", runtimeProfileAuditDetails(item))

	return api.CreateRuntimeProfile201JSONResponse(toRuntimeProfileResponse(item)), nil
}

func (a *API) GetGlobalRuntimeProfileDefaults(
	ctx context.Context,
	_ api.GetGlobalRuntimeProfileDefaultsRequestObject,
) (api.GetGlobalRuntimeProfileDefaultsResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	ref := profilepkg.GlobalInheritedRef()
	item, err := a.getInheritedRuntimeProfileForView(ctx, ref)
	if err != nil {
		return nil, err
	}
	return api.GetGlobalRuntimeProfileDefaults200JSONResponse(
		toInheritedRuntimeProfileResponse(ref, "", item),
	), nil
}

func (a *API) UpdateGlobalRuntimeProfileDefaults(
	ctx context.Context,
	request api.UpdateGlobalRuntimeProfileDefaultsRequestObject,
) (api.UpdateGlobalRuntimeProfileDefaultsResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	ref := profilepkg.GlobalInheritedRef()
	updated, clientErr, err := a.updateInheritedRuntimeProfile(ctx, ref, "", request.Body)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		return api.UpdateGlobalRuntimeProfileDefaults400JSONResponse(*clientErr), nil
	}
	return api.UpdateGlobalRuntimeProfileDefaults200JSONResponse(updated), nil
}

func (a *API) SetGlobalRuntimeProfileDefaultVariable(
	ctx context.Context,
	request api.SetGlobalRuntimeProfileDefaultVariableRequestObject,
) (api.SetGlobalRuntimeProfileDefaultVariableResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	ref := profilepkg.GlobalInheritedRef()
	updated, clientErr, err := a.setInheritedRuntimeProfileVariable(ctx, ref, "", request.Key, request.Body)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		return api.SetGlobalRuntimeProfileDefaultVariable400JSONResponse(*clientErr), nil
	}
	return api.SetGlobalRuntimeProfileDefaultVariable200JSONResponse(updated), nil
}

func (a *API) SetGlobalRuntimeProfileDefaultSecret(
	ctx context.Context,
	request api.SetGlobalRuntimeProfileDefaultSecretRequestObject,
) (api.SetGlobalRuntimeProfileDefaultSecretResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	ref := profilepkg.GlobalInheritedRef()
	updated, clientErr, err := a.setInheritedRuntimeProfileSecret(ctx, ref, "", request.Key, request.Body)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		return api.SetGlobalRuntimeProfileDefaultSecret400JSONResponse(*clientErr), nil
	}
	return api.SetGlobalRuntimeProfileDefaultSecret200JSONResponse(updated), nil
}

func (a *API) DeleteGlobalRuntimeProfileDefaultEntry(
	ctx context.Context,
	request api.DeleteGlobalRuntimeProfileDefaultEntryRequestObject,
) (api.DeleteGlobalRuntimeProfileDefaultEntryResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	ref := profilepkg.GlobalInheritedRef()
	clientErr, err := a.deleteInheritedRuntimeProfileEntry(ctx, ref, "", request.Key)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		if clientErr.Code == api.ErrorCodeNotFound {
			return api.DeleteGlobalRuntimeProfileDefaultEntry404JSONResponse(*clientErr), nil
		}
		return api.DeleteGlobalRuntimeProfileDefaultEntry400JSONResponse(*clientErr), nil
	}
	return api.DeleteGlobalRuntimeProfileDefaultEntry204Response{}, nil
}

func (a *API) GetWorkspaceRuntimeProfileDefaults(
	ctx context.Context,
	request api.GetWorkspaceRuntimeProfileDefaultsRequestObject,
) (api.GetWorkspaceRuntimeProfileDefaultsResponseObject, error) {
	target, clientErr, err := a.workspaceInheritedRuntimeProfileRef(ctx, request.WorkspaceName)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		if clientErr.Code == api.ErrorCodeNotFound {
			return api.GetWorkspaceRuntimeProfileDefaults404JSONResponse(*clientErr), nil
		}
		return api.GetWorkspaceRuntimeProfileDefaults400JSONResponse(*clientErr), nil
	}
	item, err := a.getInheritedRuntimeProfileForView(ctx, target.ref)
	if err != nil {
		return nil, err
	}
	return api.GetWorkspaceRuntimeProfileDefaults200JSONResponse(
		toInheritedRuntimeProfileResponse(target.ref, target.workspaceName, item),
	), nil
}

func (a *API) UpdateWorkspaceRuntimeProfileDefaults(
	ctx context.Context,
	request api.UpdateWorkspaceRuntimeProfileDefaultsRequestObject,
) (api.UpdateWorkspaceRuntimeProfileDefaultsResponseObject, error) {
	target, clientErr, err := a.workspaceInheritedRuntimeProfileRef(ctx, request.WorkspaceName)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		if clientErr.Code == api.ErrorCodeNotFound {
			return api.UpdateWorkspaceRuntimeProfileDefaults404JSONResponse(*clientErr), nil
		}
		return api.UpdateWorkspaceRuntimeProfileDefaults400JSONResponse(*clientErr), nil
	}
	updated, clientErr, err := a.updateInheritedRuntimeProfile(ctx, target.ref, target.workspaceName, request.Body)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		return api.UpdateWorkspaceRuntimeProfileDefaults400JSONResponse(*clientErr), nil
	}
	return api.UpdateWorkspaceRuntimeProfileDefaults200JSONResponse(updated), nil
}

func (a *API) SetWorkspaceRuntimeProfileDefaultVariable(
	ctx context.Context,
	request api.SetWorkspaceRuntimeProfileDefaultVariableRequestObject,
) (api.SetWorkspaceRuntimeProfileDefaultVariableResponseObject, error) {
	target, clientErr, err := a.workspaceInheritedRuntimeProfileRef(ctx, request.WorkspaceName)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		if clientErr.Code == api.ErrorCodeNotFound {
			return api.SetWorkspaceRuntimeProfileDefaultVariable404JSONResponse(*clientErr), nil
		}
		return api.SetWorkspaceRuntimeProfileDefaultVariable400JSONResponse(*clientErr), nil
	}
	updated, clientErr, err := a.setInheritedRuntimeProfileVariable(ctx, target.ref, target.workspaceName, request.Key, request.Body)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		return api.SetWorkspaceRuntimeProfileDefaultVariable400JSONResponse(*clientErr), nil
	}
	return api.SetWorkspaceRuntimeProfileDefaultVariable200JSONResponse(updated), nil
}

func (a *API) SetWorkspaceRuntimeProfileDefaultSecret(
	ctx context.Context,
	request api.SetWorkspaceRuntimeProfileDefaultSecretRequestObject,
) (api.SetWorkspaceRuntimeProfileDefaultSecretResponseObject, error) {
	target, clientErr, err := a.workspaceInheritedRuntimeProfileRef(ctx, request.WorkspaceName)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		if clientErr.Code == api.ErrorCodeNotFound {
			return api.SetWorkspaceRuntimeProfileDefaultSecret404JSONResponse(*clientErr), nil
		}
		return api.SetWorkspaceRuntimeProfileDefaultSecret400JSONResponse(*clientErr), nil
	}
	updated, clientErr, err := a.setInheritedRuntimeProfileSecret(ctx, target.ref, target.workspaceName, request.Key, request.Body)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		return api.SetWorkspaceRuntimeProfileDefaultSecret400JSONResponse(*clientErr), nil
	}
	return api.SetWorkspaceRuntimeProfileDefaultSecret200JSONResponse(updated), nil
}

func (a *API) DeleteWorkspaceRuntimeProfileDefaultEntry(
	ctx context.Context,
	request api.DeleteWorkspaceRuntimeProfileDefaultEntryRequestObject,
) (api.DeleteWorkspaceRuntimeProfileDefaultEntryResponseObject, error) {
	target, clientErr, err := a.workspaceInheritedRuntimeProfileRef(ctx, request.WorkspaceName)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		if clientErr.Code == api.ErrorCodeNotFound {
			return api.DeleteWorkspaceRuntimeProfileDefaultEntry404JSONResponse(*clientErr), nil
		}
		return api.DeleteWorkspaceRuntimeProfileDefaultEntry400JSONResponse(*clientErr), nil
	}
	clientErr, err = a.deleteInheritedRuntimeProfileEntry(ctx, target.ref, target.workspaceName, request.Key)
	if err != nil {
		return nil, err
	}
	if clientErr != nil {
		if clientErr.Code == api.ErrorCodeNotFound {
			return api.DeleteWorkspaceRuntimeProfileDefaultEntry404JSONResponse(*clientErr), nil
		}
		return api.DeleteWorkspaceRuntimeProfileDefaultEntry400JSONResponse(*clientErr), nil
	}
	return api.DeleteWorkspaceRuntimeProfileDefaultEntry204Response{}, nil
}

func (a *API) GetRuntimeProfile(ctx context.Context, request api.GetRuntimeProfileRequestObject) (api.GetRuntimeProfileResponseObject, error) {
	item, err := a.getRuntimeProfileForView(ctx, request.ProfileName)
	if err != nil {
		if errors.Is(err, profilepkg.ErrNotFound) {
			return api.GetRuntimeProfile404JSONResponse(runtimeProfileNotFound()), nil
		}
		if isRuntimeProfileValidationError(err) {
			return api.GetRuntimeProfile404JSONResponse(runtimeProfileNotFound()), nil
		}
		return nil, err
	}
	return api.GetRuntimeProfile200JSONResponse(toRuntimeProfileResponse(item)), nil
}

func (a *API) UpdateRuntimeProfile(ctx context.Context, request api.UpdateRuntimeProfileRequestObject) (api.UpdateRuntimeProfileResponseObject, error) {
	item, err := a.getRuntimeProfileForView(ctx, request.ProfileName)
	if err != nil {
		if errors.Is(err, profilepkg.ErrNotFound) {
			return api.UpdateRuntimeProfile404JSONResponse(runtimeProfileNotFound()), nil
		}
		if isRuntimeProfileValidationError(err) {
			return api.UpdateRuntimeProfile400JSONResponse(runtimeProfileBadRequest(err.Error())), nil
		}
		return nil, err
	}
	if request.Body == nil {
		return api.UpdateRuntimeProfile400JSONResponse(runtimeProfileBadRequest("Request body is required")), nil
	}
	if err := a.requireRuntimeProfileUpdate(ctx, item, valueOf(request.Body.Protected)); err != nil {
		return nil, err
	}

	actor := currentActorID(ctx)
	now := time.Now().UTC()
	item.ApplyUpdate(profilepkg.UpdateInput{
		Description: request.Body.Description,
		Protected:   request.Body.Protected,
		UpdatedBy:   actor,
	}, now)
	if request.Body.Status != nil {
		if err := item.SetStatus(profilepkg.Status(*request.Body.Status), actor, now); err != nil {
			return api.UpdateRuntimeProfile400JSONResponse(runtimeProfileBadRequest(err.Error())), nil
		}
	}

	if err := a.profileStore.Update(ctx, item); err != nil {
		if errors.Is(err, profilepkg.ErrNotFound) {
			return api.UpdateRuntimeProfile404JSONResponse(runtimeProfileNotFound()), nil
		}
		if isRuntimeProfileValidationError(err) {
			return api.UpdateRuntimeProfile400JSONResponse(runtimeProfileBadRequest(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to update runtime profile: %w", err)
	}

	updated, err := a.profileStore.GetByName(ctx, item.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to reload runtime profile: %w", err)
	}
	a.logAudit(ctx, audit.CategorySecret, "runtime_profile_update", runtimeProfileAuditDetails(updated))

	return api.UpdateRuntimeProfile200JSONResponse(toRuntimeProfileResponse(updated)), nil
}

func (a *API) DeleteRuntimeProfile(ctx context.Context, request api.DeleteRuntimeProfileRequestObject) (api.DeleteRuntimeProfileResponseObject, error) {
	item, err := a.getRuntimeProfileForManage(ctx, request.ProfileName)
	if err != nil {
		if errors.Is(err, profilepkg.ErrNotFound) || isRuntimeProfileValidationError(err) {
			return api.DeleteRuntimeProfile404JSONResponse(runtimeProfileNotFound()), nil
		}
		return nil, err
	}

	if err := a.profileStore.Delete(ctx, item.Name); err != nil {
		if errors.Is(err, profilepkg.ErrNotFound) {
			return api.DeleteRuntimeProfile404JSONResponse(runtimeProfileNotFound()), nil
		}
		return nil, fmt.Errorf("failed to delete runtime profile: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "runtime_profile_delete", runtimeProfileAuditDetails(item))
	return api.DeleteRuntimeProfile204Response{}, nil
}

func (a *API) SetRuntimeProfileVariable(ctx context.Context, request api.SetRuntimeProfileVariableRequestObject) (api.SetRuntimeProfileVariableResponseObject, error) {
	item, err := a.getRuntimeProfileForManage(ctx, request.ProfileName)
	if err != nil {
		if errors.Is(err, profilepkg.ErrNotFound) {
			return api.SetRuntimeProfileVariable404JSONResponse(runtimeProfileNotFound()), nil
		}
		if isRuntimeProfileValidationError(err) {
			return api.SetRuntimeProfileVariable400JSONResponse(runtimeProfileBadRequest(err.Error())), nil
		}
		return nil, err
	}
	if request.Body == nil {
		return api.SetRuntimeProfileVariable400JSONResponse(runtimeProfileBadRequest("Request body is required")), nil
	}

	updated, err := profilepkg.NewManager(a.profileStore, a.secretStore).
		SetVariable(ctx, item, request.Key, request.Body.Value, currentActorID(ctx))
	if err != nil {
		if errors.Is(err, profilepkg.ErrNotFound) {
			return api.SetRuntimeProfileVariable404JSONResponse(runtimeProfileNotFound()), nil
		}
		if isRuntimeProfileValidationError(err) {
			return api.SetRuntimeProfileVariable400JSONResponse(runtimeProfileBadRequest(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to update runtime profile variable: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "runtime_profile_variable_set", runtimeProfileAuditDetails(updated))

	return api.SetRuntimeProfileVariable200JSONResponse(toRuntimeProfileResponse(updated)), nil
}

func (a *API) SetRuntimeProfileSecret(ctx context.Context, request api.SetRuntimeProfileSecretRequestObject) (api.SetRuntimeProfileSecretResponseObject, error) {
	item, err := a.getRuntimeProfileForManage(ctx, request.ProfileName)
	if err != nil {
		if errors.Is(err, profilepkg.ErrNotFound) {
			return api.SetRuntimeProfileSecret404JSONResponse(runtimeProfileNotFound()), nil
		}
		if isRuntimeProfileValidationError(err) {
			return api.SetRuntimeProfileSecret400JSONResponse(runtimeProfileBadRequest(err.Error())), nil
		}
		return nil, err
	}
	if request.Body == nil || request.Body.Value == nil || *request.Body.Value == "" {
		return api.SetRuntimeProfileSecret400JSONResponse(runtimeProfileBadRequest("value must not be empty")), nil
	}
	if a.secretStore == nil {
		return nil, secretStoreUnavailable()
	}

	updated, err := profilepkg.NewManager(a.profileStore, a.secretStore).
		SetSecret(ctx, item, request.Key, *request.Body.Value, currentActorID(ctx))
	if err != nil {
		if errors.Is(err, profilepkg.ErrNotFound) {
			return api.SetRuntimeProfileSecret404JSONResponse(runtimeProfileNotFound()), nil
		}
		if isRuntimeProfileValidationError(err) || isSecretValidationError(err) || errors.Is(err, secretpkg.ErrUnsupportedProvider) {
			return api.SetRuntimeProfileSecret400JSONResponse(runtimeProfileBadRequest(err.Error())), nil
		}
		return nil, err
	}

	a.logAudit(ctx, audit.CategorySecret, "runtime_profile_secret_set", runtimeProfileAuditDetails(updated))

	return api.SetRuntimeProfileSecret200JSONResponse(toRuntimeProfileResponse(updated)), nil
}

func (a *API) DeleteRuntimeProfileEntry(ctx context.Context, request api.DeleteRuntimeProfileEntryRequestObject) (api.DeleteRuntimeProfileEntryResponseObject, error) {
	item, err := a.getRuntimeProfileForManage(ctx, request.ProfileName)
	if err != nil {
		if errors.Is(err, profilepkg.ErrNotFound) {
			return api.DeleteRuntimeProfileEntry404JSONResponse(runtimeProfileNotFound()), nil
		}
		if isRuntimeProfileValidationError(err) {
			return api.DeleteRuntimeProfileEntry400JSONResponse(runtimeProfileBadRequest(err.Error())), nil
		}
		return nil, err
	}

	if err := profilepkg.NewManager(a.profileStore, a.secretStore).
		DeleteEntry(ctx, item, request.Key, currentActorID(ctx)); err != nil {
		if errors.Is(err, profilepkg.ErrNotFound) {
			return api.DeleteRuntimeProfileEntry404JSONResponse(runtimeProfileNotFound()), nil
		}
		if isRuntimeProfileValidationError(err) {
			return api.DeleteRuntimeProfileEntry400JSONResponse(runtimeProfileBadRequest(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to delete runtime profile entry: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "runtime_profile_entry_delete", map[string]any{
		"profile": item.Name,
		"key":     request.Key,
	})
	return api.DeleteRuntimeProfileEntry204Response{}, nil
}

func (a *API) getInheritedRuntimeProfileForView(ctx context.Context, ref profilepkg.InheritedRef) (*profilepkg.Profile, error) {
	if a.profileStore == nil {
		return nil, profileStoreUnavailable()
	}
	item, err := a.profileStore.GetInherited(ctx, ref)
	if errors.Is(err, profilepkg.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (a *API) getOrCreateInheritedRuntimeProfile(
	ctx context.Context,
	ref profilepkg.InheritedRef,
	actor string,
) (*profilepkg.Profile, error) {
	item, err := a.getInheritedRuntimeProfileForView(ctx, ref)
	if err != nil {
		return nil, err
	}
	if item != nil {
		return item, nil
	}

	created, err := profilepkg.NewInherited(ref, profilepkg.InheritedCreateInput{
		CreatedBy: actor,
	}, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := a.profileStore.Create(ctx, created); err != nil {
		if errors.Is(err, profilepkg.ErrAlreadyExists) {
			return a.profileStore.GetInherited(ctx, ref)
		}
		return nil, err
	}
	return created, nil
}

func (a *API) updateInheritedRuntimeProfile(
	ctx context.Context,
	ref profilepkg.InheritedRef,
	workspaceName string,
	body *api.UpdateInheritedRuntimeProfileRequest,
) (api.InheritedRuntimeProfileResponse, *api.Error, error) {
	if body == nil {
		return api.InheritedRuntimeProfileResponse{}, ptrOf(runtimeProfileBadRequest("Request body is required")), nil
	}

	actor := currentActorID(ctx)
	item, err := a.getOrCreateInheritedRuntimeProfile(ctx, ref, actor)
	if err != nil {
		if isRuntimeProfileValidationError(err) {
			return api.InheritedRuntimeProfileResponse{}, ptrOf(runtimeProfileBadRequest(err.Error())), nil
		}
		return api.InheritedRuntimeProfileResponse{}, nil, err
	}
	item.ApplyUpdate(profilepkg.UpdateInput{
		Description: body.Description,
		UpdatedBy:   actor,
	}, time.Now().UTC())
	item.Protected = true
	item.Status = profilepkg.StatusActive

	if err := a.profileStore.Update(ctx, item); err != nil {
		if isRuntimeProfileValidationError(err) {
			return api.InheritedRuntimeProfileResponse{}, ptrOf(runtimeProfileBadRequest(err.Error())), nil
		}
		return api.InheritedRuntimeProfileResponse{}, nil, fmt.Errorf("failed to update inherited runtime profile: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "runtime_profile_defaults_update",
		inheritedRuntimeProfileAuditDetails(ref, workspaceName, item))

	return toInheritedRuntimeProfileResponse(ref, workspaceName, item), nil, nil
}

func (a *API) setInheritedRuntimeProfileVariable(
	ctx context.Context,
	ref profilepkg.InheritedRef,
	workspaceName string,
	key string,
	body *api.SetRuntimeProfileVariableRequest,
) (api.InheritedRuntimeProfileResponse, *api.Error, error) {
	if body == nil {
		return api.InheritedRuntimeProfileResponse{}, ptrOf(runtimeProfileBadRequest("Request body is required")), nil
	}

	actor := currentActorID(ctx)
	item, err := a.getOrCreateInheritedRuntimeProfile(ctx, ref, actor)
	if err != nil {
		if isRuntimeProfileValidationError(err) {
			return api.InheritedRuntimeProfileResponse{}, ptrOf(runtimeProfileBadRequest(err.Error())), nil
		}
		return api.InheritedRuntimeProfileResponse{}, nil, err
	}
	updated, err := profilepkg.NewManager(a.profileStore, a.secretStore).
		SetVariable(ctx, item, key, body.Value, actor)
	if err != nil {
		if isRuntimeProfileValidationError(err) {
			return api.InheritedRuntimeProfileResponse{}, ptrOf(runtimeProfileBadRequest(err.Error())), nil
		}
		return api.InheritedRuntimeProfileResponse{}, nil, fmt.Errorf("failed to update inherited runtime profile variable: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "runtime_profile_default_variable_set",
		inheritedRuntimeProfileAuditDetails(ref, workspaceName, updated))

	return toInheritedRuntimeProfileResponse(ref, workspaceName, updated), nil, nil
}

func (a *API) setInheritedRuntimeProfileSecret(
	ctx context.Context,
	ref profilepkg.InheritedRef,
	workspaceName string,
	key string,
	body *api.SetRuntimeProfileSecretRequest,
) (api.InheritedRuntimeProfileResponse, *api.Error, error) {
	if body == nil || body.Value == nil || *body.Value == "" {
		return api.InheritedRuntimeProfileResponse{}, ptrOf(runtimeProfileBadRequest("value must not be empty")), nil
	}
	if a.secretStore == nil {
		return api.InheritedRuntimeProfileResponse{}, nil, secretStoreUnavailable()
	}

	actor := currentActorID(ctx)
	item, err := a.getOrCreateInheritedRuntimeProfile(ctx, ref, actor)
	if err != nil {
		if isRuntimeProfileValidationError(err) {
			return api.InheritedRuntimeProfileResponse{}, ptrOf(runtimeProfileBadRequest(err.Error())), nil
		}
		return api.InheritedRuntimeProfileResponse{}, nil, err
	}
	updated, err := profilepkg.NewManager(a.profileStore, a.secretStore).
		SetSecret(ctx, item, key, *body.Value, actor)
	if err != nil {
		if isRuntimeProfileValidationError(err) || isSecretValidationError(err) || errors.Is(err, secretpkg.ErrUnsupportedProvider) {
			return api.InheritedRuntimeProfileResponse{}, ptrOf(runtimeProfileBadRequest(err.Error())), nil
		}
		return api.InheritedRuntimeProfileResponse{}, nil, err
	}

	a.logAudit(ctx, audit.CategorySecret, "runtime_profile_default_secret_set",
		inheritedRuntimeProfileAuditDetails(ref, workspaceName, updated))

	return toInheritedRuntimeProfileResponse(ref, workspaceName, updated), nil, nil
}

func (a *API) deleteInheritedRuntimeProfileEntry(
	ctx context.Context,
	ref profilepkg.InheritedRef,
	workspaceName string,
	key string,
) (*api.Error, error) {
	if err := profilepkg.ValidateKey(key); err != nil {
		return ptrOf(runtimeProfileBadRequest(err.Error())), nil
	}
	if a.profileStore == nil {
		return nil, profileStoreUnavailable()
	}
	item, err := a.profileStore.GetInherited(ctx, ref)
	if errors.Is(err, profilepkg.ErrNotFound) {
		return ptrOf(runtimeProfileNotFound()), nil
	}
	if err != nil {
		return nil, err
	}
	if err := profilepkg.NewManager(a.profileStore, a.secretStore).
		DeleteEntry(ctx, item, key, currentActorID(ctx)); err != nil {
		if errors.Is(err, profilepkg.ErrNotFound) {
			return ptrOf(runtimeProfileNotFound()), nil
		}
		if isRuntimeProfileValidationError(err) {
			return ptrOf(runtimeProfileBadRequest(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to delete inherited runtime profile entry: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "runtime_profile_default_entry_delete", map[string]any{
		"name":      ref.PublicName(),
		"workspace": workspaceName,
		"key":       key,
	})

	return nil, nil
}

type workspaceInheritedRuntimeProfileTarget struct {
	ref           profilepkg.InheritedRef
	workspaceName string
}

func (a *API) workspaceInheritedRuntimeProfileRef(
	ctx context.Context,
	name string,
) (workspaceInheritedRuntimeProfileTarget, *api.Error, error) {
	if a.workspaceStore == nil {
		return workspaceInheritedRuntimeProfileTarget{}, nil, workspaceStoreUnavailable()
	}
	workspaceName, err := validateWorkspaceParam(strings.TrimSpace(name))
	if err != nil {
		return workspaceInheritedRuntimeProfileTarget{}, serviceAPIError(err), nil
	}
	if workspaceName == "" {
		return workspaceInheritedRuntimeProfileTarget{}, ptrOf(runtimeProfileBadRequest("workspace name is required")), nil
	}
	if err := a.requireWorkspaceConfigWrite(ctx, workspaceName); err != nil {
		return workspaceInheritedRuntimeProfileTarget{}, nil, err
	}
	ws, err := a.workspaceStore.GetByName(ctx, workspaceName)
	if err != nil {
		if errors.Is(err, workspace.ErrWorkspaceNotFound) {
			return workspaceInheritedRuntimeProfileTarget{}, &api.Error{
				Code:    api.ErrorCodeNotFound,
				Message: "Resource not found",
			}, nil
		}
		return workspaceInheritedRuntimeProfileTarget{}, nil, err
	}
	ref, err := profilepkg.WorkspaceInheritedRef(ws.Name)
	if err != nil {
		return workspaceInheritedRuntimeProfileTarget{}, ptrOf(runtimeProfileBadRequest(err.Error())), nil
	}
	return workspaceInheritedRuntimeProfileTarget{
		ref:           ref,
		workspaceName: ws.Name,
	}, nil, nil
}

func (a *API) getRuntimeProfileForView(ctx context.Context, name string) (*profilepkg.Profile, error) {
	if a.profileStore == nil {
		return nil, profileStoreUnavailable()
	}
	if err := a.requireManagerOrAbove(ctx); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(name)
	if err := profilepkg.ValidateName(trimmed); err != nil {
		return nil, err
	}
	item, err := a.profileStore.GetByName(ctx, trimmed)
	if err != nil {
		return nil, err
	}
	if err := a.requireRuntimeProfileView(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (a *API) getRuntimeProfileForManage(ctx context.Context, name string) (*profilepkg.Profile, error) {
	item, err := a.getRuntimeProfileForView(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := a.requireRuntimeProfileManage(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (a *API) explicitRunProfile(ctx context.Context, raw *api.RuntimeProfileOverride) (string, bool, error) {
	if raw != nil {
		profileName, err := a.ensureRunnableRuntimeProfile(ctx, string(*raw))
		return profileName, true, err
	}
	return "", false, nil
}

func (a *API) inheritedRunProfileName(ctx context.Context, inherited string) (string, error) {
	return a.ensureRunnableRuntimeProfileAvailable(ctx, strings.TrimSpace(inherited))
}

func (a *API) ensureRunnableRuntimeProfile(ctx context.Context, name string) (string, error) {
	return a.ensureRunnableRuntimeProfileAccess(ctx, name, true)
}

func (a *API) ensureRunnableRuntimeProfileAvailable(ctx context.Context, name string) (string, error) {
	return a.ensureRunnableRuntimeProfileAccess(ctx, name, false)
}

func (a *API) ensureRunnableRuntimeProfileAccess(ctx context.Context, name string, requireProtectedAccess bool) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", nil
	}
	if a.profileStore == nil {
		return "", profileStoreUnavailable()
	}
	item, err := profilepkg.NewManager(a.profileStore, a.secretStore).EnsureRunnable(ctx, trimmed)
	if errors.Is(err, profilepkg.ErrInvalidName) {
		return "", &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}
	if errors.Is(err, profilepkg.ErrNotFound) {
		return "", &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("runtime profile %s not found", trimmed),
		}
	}
	if errors.Is(err, profilepkg.ErrDisabled) {
		return "", &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    fmt.Sprintf("runtime profile %s is disabled", trimmed),
		}
	}
	if err != nil {
		return "", err
	}
	if requireProtectedAccess && item.Protected {
		if err := a.requireAdmin(ctx); err != nil {
			return "", err
		}
	}
	return item.Name, nil
}

func toRuntimeProfileResponse(item *profilepkg.Profile) api.RuntimeProfileResponse {
	entries := runtimeProfileEntriesToAPI(item)

	return api.RuntimeProfileResponse{
		CreatedAt:   item.CreatedAt,
		Description: ptrOf(item.Description),
		Entries:     entries,
		Id:          item.ID,
		Name:        item.Name,
		Protected:   item.Protected,
		Status:      api.RuntimeProfileStatus(item.Status),
		UpdatedAt:   item.UpdatedAt,
	}
}

func toInheritedRuntimeProfileResponse(
	ref profilepkg.InheritedRef,
	workspaceName string,
	item *profilepkg.Profile,
) api.InheritedRuntimeProfileResponse {
	scope := api.InheritedRuntimeProfileScopeGlobal
	var workspaceValue *api.WorkspaceName
	if workspaceName != "" {
		scope = api.InheritedRuntimeProfileScopeWorkspace
		workspace := api.WorkspaceName(workspaceName)
		workspaceValue = &workspace
	}

	resp := api.InheritedRuntimeProfileResponse{
		Entries:   runtimeProfileEntriesToAPI(item),
		Name:      ref.PublicName(),
		Protected: true,
		Scope:     scope,
		Status:    api.RuntimeProfileStatusActive,
		Workspace: workspaceValue,
	}
	if item == nil {
		return resp
	}
	resp.CreatedAt = ptrOf(item.CreatedAt)
	resp.Description = ptrOf(item.Description)
	resp.Id = ptrOf(item.ID)
	resp.UpdatedAt = ptrOf(item.UpdatedAt)
	return resp
}

func runtimeProfileEntriesToAPI(item *profilepkg.Profile) []api.RuntimeProfileEntryResponse {
	if item == nil {
		return []api.RuntimeProfileEntryResponse{}
	}
	entries := make([]api.RuntimeProfileEntryResponse, 0, len(item.Entries))
	for _, entry := range item.Entries {
		resp := api.RuntimeProfileEntryResponse{
			CreatedAt: entry.CreatedAt,
			Key:       entry.Key,
			Kind:      api.RuntimeProfileEntryKind(entry.Kind),
			UpdatedAt: entry.UpdatedAt,
		}
		switch entry.Kind {
		case profilepkg.EntryKindVariable:
			resp.Value = ptrOf(entry.Value)
		case profilepkg.EntryKindSecret:
			resp.SecretId = ptrOf(entry.SecretID)
		}
		entries = append(entries, resp)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Key < entries[j].Key
	})

	return entries
}

func runtimeProfileAuditDetails(item *profilepkg.Profile) map[string]any {
	return map[string]any{
		"id":          item.ID,
		"name":        item.Name,
		"status":      string(item.Status),
		"protected":   item.Protected,
		"entry_count": len(item.Entries),
	}
}

func inheritedRuntimeProfileAuditDetails(
	ref profilepkg.InheritedRef,
	workspaceName string,
	item *profilepkg.Profile,
) map[string]any {
	details := map[string]any{
		"name":      ref.PublicName(),
		"scope":     string(api.InheritedRuntimeProfileScopeGlobal),
		"protected": true,
	}
	if workspaceName != "" {
		details["scope"] = string(api.InheritedRuntimeProfileScopeWorkspace)
		details["workspace"] = workspaceName
	}
	if item != nil {
		details["id"] = item.ID
		details["entry_count"] = len(item.Entries)
	}
	return details
}

func isRuntimeProfileValidationError(err error) bool {
	return errors.Is(err, profilepkg.ErrDuplicateKey) ||
		errors.Is(err, profilepkg.ErrInvalidKey) ||
		errors.Is(err, profilepkg.ErrInvalidName) ||
		errors.Is(err, profilepkg.ErrInvalidStatus) ||
		errors.Is(err, profilepkg.ErrReservedKey)
}
