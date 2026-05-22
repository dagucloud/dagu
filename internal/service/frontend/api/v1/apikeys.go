// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/service/audit"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
)

const communityAPIKeyLimit = 2

// ListAPIKeys returns a list of all API keys. Requires admin role.
func (a *API) ListAPIKeys(ctx context.Context, _ api.ListAPIKeysRequestObject) (api.ListAPIKeysResponseObject, error) {
	if err := a.requireAPIKeyManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	keys, err := a.authService.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}

	return api.ListAPIKeys200JSONResponse{
		ApiKeys: toAPIKeys(keys),
	}, nil
}

// CreateAPIKey creates a new API key. Requires admin role.
func (a *API) CreateAPIKey(ctx context.Context, request api.CreateAPIKeyRequestObject) (api.CreateAPIKeyResponseObject, error) {
	if err := a.requireAPIKeyManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
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
	workspaceAccess, err := a.parseAndValidateWorkspaceAccess(ctx, role, request.Body.WorkspaceAccess)
	if err != nil {
		return nil, err
	}

	// Get current user for createdBy
	currentUser, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, &Error{
			Code:       api.ErrorCodeUnauthorized,
			Message:    "Not authenticated",
			HTTPStatus: http.StatusUnauthorized,
		}
	}

	a.apiKeyCreateMu.Lock()
	defer a.apiKeyCreateMu.Unlock()

	if err := a.requireCommunityAPIKeyLimit(ctx); err != nil {
		return nil, err
	}

	result, err := a.authService.CreateAPIKey(ctx, authservice.CreateAPIKeyInput{
		Name:            request.Body.Name,
		Description:     valueOf(request.Body.Description),
		Role:            role,
		WorkspaceAccess: workspaceAccess,
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
		return nil, err
	}

	a.logAudit(ctx, audit.CategoryAPIKey, "api_key_create", map[string]any{
		"key_id":   result.APIKey.ID,
		"key_name": result.APIKey.Name,
		"role":     string(result.APIKey.Role),
	})

	return api.CreateAPIKey201JSONResponse{
		ApiKey: toAPIKey(result.APIKey),
		Key:    result.FullKey,
	}, nil
}

// GetAPIKey returns a specific API key by ID. Requires admin role.
func (a *API) GetAPIKey(ctx context.Context, request api.GetAPIKeyRequestObject) (api.GetAPIKeyResponseObject, error) {
	if err := a.requireAPIKeyManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	key, err := a.authService.GetAPIKey(ctx, request.KeyId)
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

	return api.GetAPIKey200JSONResponse{
		ApiKey: toAPIKey(key),
	}, nil
}

// UpdateAPIKey updates an API key's information. Requires admin role.
func (a *API) UpdateAPIKey(ctx context.Context, request api.UpdateAPIKeyRequestObject) (api.UpdateAPIKeyResponseObject, error) {
	if err := a.requireAPIKeyManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	if request.Body == nil {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "Invalid request body",
			HTTPStatus: http.StatusBadRequest,
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
	if request.Body.WorkspaceAccess != nil {
		var roleForAccess auth.Role
		if input.Role != nil {
			roleForAccess = *input.Role
		} else {
			currentKey, err := a.authService.GetAPIKey(ctx, request.KeyId)
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
			roleForAccess = currentKey.Role
		}
		workspaceAccess, err := a.parseAndValidateWorkspaceAccess(ctx, roleForAccess, request.Body.WorkspaceAccess)
		if err != nil {
			return nil, err
		}
		input.WorkspaceAccess = workspaceAccess
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
	a.logAudit(ctx, audit.CategoryAPIKey, "api_key_update", updateDetails)

	return api.UpdateAPIKey200JSONResponse{
		ApiKey: toAPIKey(key),
	}, nil
}

// DeleteAPIKey deletes an API key. Requires admin role.
func (a *API) DeleteAPIKey(ctx context.Context, request api.DeleteAPIKeyRequestObject) (api.DeleteAPIKeyResponseObject, error) {
	if err := a.requireAPIKeyManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	// Get API key info before deletion for audit logging
	targetKey, _ := a.authService.GetAPIKey(ctx, request.KeyId)

	err := a.authService.DeleteAPIKey(ctx, request.KeyId)
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

	return &Error{
		Code: api.ErrorCodeForbidden,
		Message: "Community edition supports up to 2 API keys. " +
			"Delete an existing key or configure a license to create more.",
		HTTPStatus: http.StatusForbidden,
	}
}

func (a *API) isCommunityLicense() bool {
	if a.licenseManager == nil {
		return true
	}
	return !license.HasActiveLicense(a.licenseManager.Checker())
}

// toAPIKey converts a core auth.APIKey into its API representation.
func toAPIKey(key *auth.APIKey) api.APIKey {
	return api.APIKey{
		Id:              key.ID,
		Name:            key.Name,
		Description:     ptrOf(key.Description),
		Role:            api.UserRole(key.Role),
		WorkspaceAccess: toAPIWorkspaceAccess(key.WorkspaceAccess),
		KeyPrefix:       key.KeyPrefix,
		CreatedAt:       key.CreatedAt,
		UpdatedAt:       key.UpdatedAt,
		CreatedBy:       key.CreatedBy,
		LastUsedAt:      key.LastUsedAt,
	}
}

// toAPIKeys converts a slice of core auth.APIKey into their API representations.
func toAPIKeys(keys []*auth.APIKey) []api.APIKey {
	result := make([]api.APIKey, len(keys))
	for i, k := range keys {
		result[i] = toAPIKey(k)
	}
	return result
}
