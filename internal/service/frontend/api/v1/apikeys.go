// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/license"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
)

// communityAPIKeyLimit caps the API keys of a server without a license that
// unlocks paid features, which includes the free plan.
const communityAPIKeyLimit = 2

// ListAPIKeys returns the API keys the caller manages (see apiKeyManager).
func (a *API) ListAPIKeys(ctx context.Context, _ api.ListAPIKeysRequestObject) (api.ListAPIKeysResponseObject, error) {
	if err := a.requireAPIKeyManagement(); err != nil {
		return nil, err
	}
	manager, err := a.apiKeyManager(ctx)
	if err != nil {
		return nil, err
	}

	keys, err := a.authService.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}

	apiKeys := make([]api.APIKey, 0, len(keys))
	for _, key := range keys {
		if manager.manages(key) {
			apiKeys = append(apiKeys, manager.toAPIKey(key))
		}
	}
	return api.ListAPIKeys200JSONResponse{
		ApiKeys: apiKeys,
	}, nil
}

// CreateAPIKey creates a new API key the caller manages (see apiKeyManager).
func (a *API) CreateAPIKey(ctx context.Context, request api.CreateAPIKeyRequestObject) (api.CreateAPIKeyResponseObject, error) {
	if err := a.requireAPIKeyManagement(); err != nil {
		return nil, err
	}
	manager, err := a.apiKeyManager(ctx)
	if err != nil {
		return nil, err
	}

	if request.Body == nil {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "Invalid request body",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	role, err := auth.ParseRole(string(request.Body.Role))
	if err != nil {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "Invalid role",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	var projectGrants []auth.ProjectGrant
	if manager.projectsOn && request.Body.ProjectGrants != nil {
		if projectGrants, err = manager.projectGrants(*request.Body.ProjectGrants); err != nil {
			return nil, err
		}
	}
	workspaceAccess, err := a.parseAPIKeyWorkspaceAccess(ctx, role, request.Body.WorkspaceAccess, len(projectGrants) > 0)
	if err != nil {
		return nil, err
	}
	if len(request.Body.AllowedSurfaces) == 0 {
		return nil, invalidAPIKeySurfaceError()
	}
	if request.Body.AttributionClass == "" {
		return nil, invalidAPIKeyAttributionError()
	}
	allowedSurfaces := toAuthAPIKeySurfaces(request.Body.AllowedSurfaces)
	attributionClass := auth.APIKeyAttributionClass(request.Body.AttributionClass)

	// Get current user for createdBy
	currentUser, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, &Error{
			Code:       api.ErrorCodeUnauthorized,
			Message:    "Not authenticated",
			HTTPStatus: http.StatusUnauthorized,
		}
	}
	if !manager.manages(&auth.APIKey{
		WorkspaceAccess:  workspaceAccess,
		ProjectGrants:    projectGrants,
		AttributionClass: attributionClass,
		OwnerUserID:      valueOf(request.Body.OwnerUserId),
	}) {
		return nil, errAPIKeyNeedsAdmin
	}

	a.apiKeyWriteMu.Lock()
	defer a.apiKeyWriteMu.Unlock()

	if err := a.requireCommunityAPIKeyLimit(ctx); err != nil {
		return nil, err
	}

	result, err := a.authService.CreateAPIKey(ctx, authservice.CreateAPIKeyInput{
		Name:               request.Body.Name,
		Description:        valueOf(request.Body.Description),
		Role:               role,
		WorkspaceAccess:    workspaceAccess,
		AllowedSurfaces:    allowedSurfaces,
		AttributionClass:   attributionClass,
		OwnerUserID:        valueOf(request.Body.OwnerUserId),
		ServiceAccountName: valueOf(request.Body.ServiceAccountName),
		ProjectGrants:      projectGrants,
	}, currentUser.ID)
	if err != nil {
		if errors.Is(err, auth.ErrAPIKeyAlreadyExists) {
			return nil, &Error{
				Code:       api.ErrorCodeAlreadyExists,
				Message:    "API key with this name already exists",
				HTTPStatus: http.StatusConflict,
			}
		}
		if errors.Is(err, auth.ErrInvalidAPIKeyName) {
			return nil, &Error{
				Code:       api.ErrorCodeBadRequest,
				Message:    "Invalid API key name",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		if errors.Is(err, auth.ErrInvalidWorkspaceAccess) {
			return nil, badWorkspaceAccessError(err.Error())
		}
		if errors.Is(err, auth.ErrInvalidProjectGrant) {
			return nil, badProjectGrantError(err.Error())
		}
		if errors.Is(err, auth.ErrInvalidAPIKeySurface) {
			return nil, invalidAPIKeySurfaceError()
		}
		if errors.Is(err, auth.ErrInvalidAPIKeyAttribution) {
			return nil, invalidAPIKeyAttributionError()
		}
		return nil, err
	}

	createDetails := map[string]any{
		"key_id":            result.APIKey.ID,
		"key_name":          result.APIKey.Name,
		"role":              string(result.APIKey.Role),
		"allowed_surfaces":  auth.APIKeySurfaceStrings(result.APIKey.AllowedSurfaces),
		"attribution_class": string(result.APIKey.AttributionClass),
	}
	if len(projectGrants) > 0 {
		createDetails["project_grants"] = projectGrants
	}
	a.logAudit(ctx, audit.CategoryAPIKey, "api_key_create", createDetails)

	return api.CreateAPIKey201JSONResponse{
		ApiKey: manager.toAPIKey(result.APIKey),
		Key:    result.FullKey,
	}, nil
}

// GetAPIKey returns an API key the caller manages (see apiKeyManager). Any
// other key is not found.
func (a *API) GetAPIKey(ctx context.Context, request api.GetAPIKeyRequestObject) (api.GetAPIKeyResponseObject, error) {
	if err := a.requireAPIKeyManagement(); err != nil {
		return nil, err
	}
	manager, err := a.apiKeyManager(ctx)
	if err != nil {
		return nil, err
	}

	key, err := a.managedAPIKey(ctx, manager, request.KeyId)
	if err != nil {
		return nil, err
	}

	return api.GetAPIKey200JSONResponse{
		ApiKey: manager.toAPIKey(key),
	}, nil
}

// UpdateAPIKey updates an API key the caller manages, which it must still
// manage after the update (see apiKeyManager).
func (a *API) UpdateAPIKey(ctx context.Context, request api.UpdateAPIKeyRequestObject) (api.UpdateAPIKeyResponseObject, error) {
	if err := a.requireAPIKeyManagement(); err != nil {
		return nil, err
	}
	manager, err := a.apiKeyManager(ctx)
	if err != nil {
		return nil, err
	}

	if request.Body == nil {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "Invalid request body",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	// While projects are on, the key's current grants decide who may change
	// it and how its workspace access is checked. The lock keeps the key as
	// read here until the update writes it.
	a.apiKeyWriteMu.Lock()
	defer a.apiKeyWriteMu.Unlock()
	var currentKey *auth.APIKey
	if manager.projectsOn {
		if currentKey, err = a.managedAPIKey(ctx, manager, request.KeyId); err != nil {
			return nil, err
		}
	}

	input := authservice.UpdateAPIKeyInput{}
	if request.Body.Name != nil {
		input.Name = request.Body.Name
	}
	if request.Body.Description != nil {
		input.Description = request.Body.Description
	}
	if request.Body.Role != nil {
		role, err := auth.ParseRole(string(*request.Body.Role))
		if err != nil {
			return nil, &Error{
				Code:       api.ErrorCodeBadRequest,
				Message:    "Invalid role",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		input.Role = &role
	}
	if manager.projectsOn && request.Body.ProjectGrants != nil {
		projectGrants, err := manager.projectGrants(*request.Body.ProjectGrants)
		if err != nil {
			return nil, err
		}
		input.ProjectGrants = &projectGrants
	}
	if request.Body.WorkspaceAccess != nil {
		var roleForAccess auth.Role
		if input.Role != nil {
			roleForAccess = *input.Role
		} else {
			if currentKey == nil {
				if currentKey, err = a.managedAPIKey(ctx, manager, request.KeyId); err != nil {
					return nil, err
				}
			}
			roleForAccess = currentKey.Role
		}
		hasProjectGrants := currentKey != nil && len(currentKey.ProjectGrants) > 0
		if input.ProjectGrants != nil {
			hasProjectGrants = len(*input.ProjectGrants) > 0
		}
		workspaceAccess, err := a.parseAPIKeyWorkspaceAccess(ctx, roleForAccess, request.Body.WorkspaceAccess, hasProjectGrants)
		if err != nil {
			return nil, err
		}
		input.WorkspaceAccess = workspaceAccess
	}
	if request.Body.AllowedSurfaces != nil {
		if len(*request.Body.AllowedSurfaces) == 0 {
			return nil, invalidAPIKeySurfaceError()
		}
		allowedSurfaces := toAuthAPIKeySurfaces(*request.Body.AllowedSurfaces)
		input.AllowedSurfaces = &allowedSurfaces
	}
	if request.Body.AttributionClass != nil {
		attributionClass := auth.APIKeyAttributionClass(*request.Body.AttributionClass)
		input.AttributionClass = &attributionClass
	}
	if request.Body.OwnerUserId != nil {
		input.OwnerUserID = request.Body.OwnerUserId
	}
	if request.Body.ServiceAccountName != nil {
		input.ServiceAccountName = request.Body.ServiceAccountName
	}
	if !manager.admin {
		updated := *currentKey
		if input.WorkspaceAccess != nil {
			updated.WorkspaceAccess = input.WorkspaceAccess
		}
		if input.ProjectGrants != nil {
			updated.ProjectGrants = *input.ProjectGrants
		}
		if input.AttributionClass != nil {
			updated.AttributionClass = *input.AttributionClass
		}
		if input.OwnerUserID != nil {
			updated.OwnerUserID = *input.OwnerUserID
		}
		if !manager.manages(&updated) {
			return nil, errAPIKeyNeedsAdmin
		}
	}

	key, err := a.authService.UpdateAPIKey(ctx, request.KeyId, input)
	if err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotFound) {
			return nil, &Error{
				Code:       api.ErrorCodeNotFound,
				Message:    "API key not found",
				HTTPStatus: http.StatusNotFound,
			}
		}
		if errors.Is(err, auth.ErrAPIKeyAlreadyExists) {
			return nil, &Error{
				Code:       api.ErrorCodeAlreadyExists,
				Message:    "API key with this name already exists",
				HTTPStatus: http.StatusConflict,
			}
		}
		if errors.Is(err, auth.ErrInvalidAPIKeyName) {
			return nil, &Error{
				Code:       api.ErrorCodeBadRequest,
				Message:    "Invalid API key name",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		if errors.Is(err, auth.ErrInvalidWorkspaceAccess) {
			return nil, badWorkspaceAccessError(err.Error())
		}
		if errors.Is(err, auth.ErrInvalidProjectGrant) {
			return nil, badProjectGrantError(err.Error())
		}
		if errors.Is(err, auth.ErrInvalidAPIKeySurface) {
			return nil, invalidAPIKeySurfaceError()
		}
		if errors.Is(err, auth.ErrInvalidAPIKeyAttribution) {
			return nil, invalidAPIKeyAttributionError()
		}
		return nil, err
	}

	updateDetails := map[string]any{"key_id": request.KeyId}
	if input.Name != nil {
		updateDetails["name"] = *input.Name
	}
	if input.Description != nil {
		updateDetails["description"] = *input.Description
	}
	if input.Role != nil {
		updateDetails["role"] = string(*input.Role)
	}
	if input.WorkspaceAccess != nil {
		updateDetails["workspace_access"] = toAPIWorkspaceAccess(input.WorkspaceAccess)
	}
	if input.AllowedSurfaces != nil {
		updateDetails["allowed_surfaces"] = auth.APIKeySurfaceStrings(*input.AllowedSurfaces)
	}
	if input.AttributionClass != nil {
		updateDetails["attribution_class"] = string(*input.AttributionClass)
	}
	if input.OwnerUserID != nil {
		updateDetails["owner_user_id"] = *input.OwnerUserID
	}
	if input.ServiceAccountName != nil {
		updateDetails["service_account_name"] = *input.ServiceAccountName
	}
	if input.ProjectGrants != nil {
		updateDetails["project_grants"] = *input.ProjectGrants
	}
	a.logAudit(ctx, audit.CategoryAPIKey, "api_key_update", updateDetails)

	return api.UpdateAPIKey200JSONResponse{
		ApiKey: manager.toAPIKey(key),
	}, nil
}

// DeleteAPIKey deletes an API key the caller manages (see apiKeyManager).
func (a *API) DeleteAPIKey(ctx context.Context, request api.DeleteAPIKeyRequestObject) (api.DeleteAPIKeyResponseObject, error) {
	if err := a.requireAPIKeyManagement(); err != nil {
		return nil, err
	}
	manager, err := a.apiKeyManager(ctx)
	if err != nil {
		return nil, err
	}

	a.apiKeyWriteMu.Lock()
	defer a.apiKeyWriteMu.Unlock()

	// Get API key info before deletion for audit logging
	targetKey, _ := a.authService.GetAPIKey(ctx, request.KeyId)
	if !manager.admin && (targetKey == nil || !manager.manages(targetKey)) {
		return nil, apiKeyNotFoundError()
	}

	err = a.authService.DeleteAPIKey(ctx, request.KeyId)
	if err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotFound) {
			return nil, &Error{
				Code:       api.ErrorCodeNotFound,
				Message:    "API key not found",
				HTTPStatus: http.StatusNotFound,
			}
		}
		return nil, err
	}

	deleteDetails := map[string]any{"key_id": request.KeyId}
	if targetKey != nil {
		deleteDetails["key_name"] = targetKey.Name
	}
	a.logAudit(ctx, audit.CategoryAPIKey, "api_key_delete", deleteDetails)

	return api.DeleteAPIKey204Response{}, nil
}

// managedAPIKey returns the key with the given ID when the caller manages it,
// and the not-found error of a missing key otherwise.
func (a *API) managedAPIKey(ctx context.Context, manager *apiKeyManager, id string) (*auth.APIKey, error) {
	key, err := a.authService.GetAPIKey(ctx, id)
	if errors.Is(err, auth.ErrAPIKeyNotFound) {
		return nil, apiKeyNotFoundError()
	}
	if err != nil {
		return nil, err
	}
	if !manager.manages(key) {
		return nil, apiKeyNotFoundError()
	}
	return key, nil
}

// requireAPIKeyManagement checks if API key management is enabled.
func (a *API) requireAPIKeyManagement() error {
	if a.authService == nil || !a.authService.HasAPIKeyStore() {
		return &Error{
			Code:       api.ErrorCodeForbidden,
			Message:    "API key management is not available",
			HTTPStatus: http.StatusForbidden,
		}
	}
	return nil
}

func (a *API) requireCommunityAPIKeyLimit(ctx context.Context) error {
	if !a.isCommunityLicense() {
		return nil
	}

	keys, err := a.authService.ListAPIKeys(ctx)
	if err != nil {
		return err
	}
	if len(keys) < communityAPIKeyLimit {
		return nil
	}

	message := "Community edition supports up to 2 API keys. " +
		"Delete an existing key or configure a license to create more."
	if a.isFreeLicense() {
		message = "The Free plan supports up to 2 API keys. Team and Pro remove the limit."
	}
	return &Error{
		Code:       api.ErrorCodeForbidden,
		Message:    message,
		HTTPStatus: http.StatusForbidden,
	}
}

func (a *API) isCommunityLicense() bool {
	if a.licenseManager == nil {
		return true
	}
	return !license.HasActiveLicense(a.licenseManager.Checker())
}

func (a *API) isFreeLicense() bool {
	return a.licenseManager != nil && a.licenseManager.Checker().Claims().IsFree()
}

func invalidAPIKeySurfaceError() *Error {
	return &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    "Invalid API key surface",
		HTTPStatus: http.StatusBadRequest,
	}
}

func invalidAPIKeyAttributionError() *Error {
	return &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    "Invalid API key attribution",
		HTTPStatus: http.StatusBadRequest,
	}
}

// toAPIKey converts a core auth.APIKey into its API representation.
func toAPIKey(key *auth.APIKey) api.APIKey {
	normalized := auth.NormalizeAPIKeyMetadata(key)
	return api.APIKey{
		Id:                       normalized.ID,
		Name:                     normalized.Name,
		Description:              ptrOf(normalized.Description),
		Role:                     api.UserRole(normalized.Role),
		WorkspaceAccess:          toAPIWorkspaceAccess(normalized.WorkspaceAccess),
		AllowedSurfaces:          toAPIAPIKeySurfaces(normalized.AllowedSurfaces),
		AttributionClass:         api.APIKeyAttributionClass(normalized.AttributionClass),
		OwnerUserId:              ptrOf(normalized.OwnerUserID),
		OwnerUsername:            ptrOf(normalized.OwnerUsername),
		ServiceAccountId:         ptrOf(normalized.ServiceAccountID),
		ServiceAccountName:       ptrOf(normalized.ServiceAccountName),
		MigratedAsServiceAccount: ptrOf(normalized.MigratedAsServiceAccount),
		KeyPrefix:                normalized.KeyPrefix,
		CreatedAt:                normalized.CreatedAt,
		UpdatedAt:                normalized.UpdatedAt,
		CreatedBy:                normalized.CreatedBy,
		LastUsedAt:               normalized.LastUsedAt,
	}
}

func toAuthAPIKeySurfaces[T ~string](surfaces []T) []auth.APIKeySurface {
	out := make([]auth.APIKeySurface, 0, len(surfaces))
	for _, surface := range surfaces {
		out = append(out, auth.APIKeySurface(surface))
	}
	return out
}

func toAPIAPIKeySurfaces(surfaces []auth.APIKeySurface) []api.APIKeyAllowedSurfaces {
	normalized := auth.NormalizeAPIKeySurfaces(surfaces)
	out := make([]api.APIKeyAllowedSurfaces, 0, len(normalized))
	for _, surface := range normalized {
		out = append(out, api.APIKeyAllowedSurfaces(surface))
	}
	return out
}
