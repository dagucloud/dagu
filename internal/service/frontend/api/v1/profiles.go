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

func runtimeProfileAuditDetails(item *profilepkg.Profile) map[string]any {
	return map[string]any{
		"id":          item.ID,
		"name":        item.Name,
		"status":      string(item.Status),
		"protected":   item.Protected,
		"entry_count": len(item.Entries),
	}
}

func isRuntimeProfileValidationError(err error) bool {
	return errors.Is(err, profilepkg.ErrDuplicateKey) ||
		errors.Is(err, profilepkg.ErrInvalidKey) ||
		errors.Is(err, profilepkg.ErrInvalidName) ||
		errors.Is(err, profilepkg.ErrInvalidStatus) ||
		errors.Is(err, profilepkg.ErrReservedKey)
}
