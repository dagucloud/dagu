// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/service/audit"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
)

// ListUsers returns a list of all users. Requires admin role.
func (a *API) ListUsers(ctx context.Context, _ api.ListUsersRequestObject) (api.ListUsersResponseObject, error) {
	if err := a.requireUserManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	users, err := a.authService.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	syncEnabled := a.oidcWorkspaceAccessSyncEnabled()

	return api.ListUsers200JSONResponse{
		Users:                          toAPIUsers(users),
		OidcWorkspaceAccessSyncEnabled: &syncEnabled,
		ManagedAuthorizationProviders:  a.managedAuthorizationProviders(syncEnabled),
	}, nil
}

func (a *API) managedAuthorizationProviders(oidcSyncEnabled bool) []api.UserAuthProvider {
	providers := make([]api.UserAuthProvider, 0, 2)
	if oidcSyncEnabled {
		providers = append(providers, api.UserAuthProviderOidc)
	}
	if a.config == nil {
		return providers
	}
	if a.licenseManager != nil && !a.licenseManager.Checker().IsFeatureEnabled(license.FeatureSSO) {
		return providers
	}
	authConfig := a.config.Server.Auth
	if authConfig.Mode == config.AuthModeBuiltin &&
		authConfig.Proxy.Enabled &&
		!authConfig.Proxy.RoleMapping.SkipOrgRoleSync {
		providers = append(providers, api.UserAuthProviderProxy)
	}
	return providers
}

func (a *API) oidcWorkspaceAccessSyncEnabled() bool {
	if a.config == nil {
		return false
	}
	if a.licenseManager != nil && !a.licenseManager.Checker().IsFeatureEnabled(license.FeatureSSO) {
		return false
	}
	authConfig := a.config.Server.Auth
	return authConfig.Mode == config.AuthModeBuiltin &&
		authConfig.OIDC.IsConfigured() &&
		authConfig.OIDC.RoleMapping.WorkspaceAccessPolicyActive() &&
		!authConfig.OIDC.RoleMapping.SkipOrgRoleSync
}

// CreateUser creates a new user. Requires admin role.
func (a *API) CreateUser(ctx context.Context, request api.CreateUserRequestObject) (api.CreateUserResponseObject, error) {
	if err := a.requireUserManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedRBAC(); err != nil {
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

	user, err := a.authService.CreateUser(ctx, authservice.CreateUserInput{
		Username:        request.Body.Username,
		Password:        request.Body.Password,
		Role:            role,
		WorkspaceAccess: workspaceAccess,
	})
	if err != nil {
		if errors.Is(err, auth.ErrUserAlreadyExists) {
			return nil, &Error{
				Code:       api.ErrorCodeAlreadyExists,
				Message:    "Username already exists",
				HTTPStatus: http.StatusConflict,
			}
		}
		if errors.Is(err, auth.ErrInvalidUsername) {
			return nil, &Error{
				Code:       api.ErrorCodeBadRequest,
				Message:    "Invalid username",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		if errors.Is(err, authservice.ErrWeakPassword) {
			return nil, &Error{
				Code:       api.ErrorCodeBadRequest,
				Message:    "Password does not meet security requirements",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		if errors.Is(err, auth.ErrInvalidWorkspaceAccess) {
			return nil, badWorkspaceAccessError(err.Error())
		}
		return nil, err
	}

	a.logAudit(ctx, audit.CategoryUser, "user_create", map[string]string{
		"target_user_id":  user.ID,
		"target_username": user.Username,
		"role":            string(user.Role),
	})

	return api.CreateUser201JSONResponse{
		User: toAPIUser(user),
	}, nil
}

// GetUser returns a specific user by ID. Requires admin role.
func (a *API) GetUser(ctx context.Context, request api.GetUserRequestObject) (api.GetUserResponseObject, error) {
	if err := a.requireUserManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	user, err := a.authService.GetUser(ctx, request.UserId)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return nil, &Error{
				Code:       api.ErrorCodeNotFound,
				Message:    "User not found",
				HTTPStatus: http.StatusNotFound,
			}
		}
		return nil, err
	}

	return api.GetUser200JSONResponse{
		User: toAPIUser(user),
	}, nil
}

// UpdateUser updates a user's information. Requires admin role.
func (a *API) UpdateUser(ctx context.Context, request api.UpdateUserRequestObject) (api.UpdateUserResponseObject, error) {
	if err := a.requireUserManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedRBAC(); err != nil {
		return nil, err
	}

	if request.Body == nil {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "Invalid request body",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	input := authservice.UpdateUserInput{}
	if request.Body.Username != nil {
		input.Username = request.Body.Username
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
			currentUser, err := a.authService.GetUser(ctx, request.UserId)
			if err != nil {
				if errors.Is(err, auth.ErrUserNotFound) {
					return nil, &Error{
						Code:       api.ErrorCodeNotFound,
						Message:    "User not found",
						HTTPStatus: http.StatusNotFound,
					}
				}
				return nil, err
			}
			roleForAccess = currentUser.Role
		}
		workspaceAccess, err := a.parseAndValidateWorkspaceAccess(ctx, roleForAccess, request.Body.WorkspaceAccess)
		if err != nil {
			return nil, err
		}
		input.WorkspaceAccess = workspaceAccess
	}
	if request.Body.IsDisabled != nil {
		// Prevent self-disable
		if *request.Body.IsDisabled {
			currentUser, ok := auth.UserFromContext(ctx)
			if ok && currentUser.ID == request.UserId {
				return nil, &Error{
					Code:       api.ErrorCodeForbidden,
					Message:    "Cannot disable your own account",
					HTTPStatus: http.StatusForbidden,
				}
			}
		}
		input.IsDisabled = request.Body.IsDisabled
	}

	user, err := a.authService.UpdateUser(ctx, request.UserId, input)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return nil, &Error{
				Code:       api.ErrorCodeNotFound,
				Message:    "User not found",
				HTTPStatus: http.StatusNotFound,
			}
		}
		if errors.Is(err, auth.ErrUserAlreadyExists) {
			return nil, &Error{
				Code:       api.ErrorCodeAlreadyExists,
				Message:    "Username already exists",
				HTTPStatus: http.StatusConflict,
			}
		}
		if errors.Is(err, auth.ErrInvalidUsername) {
			return nil, &Error{
				Code:       api.ErrorCodeBadRequest,
				Message:    "Invalid username",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		if errors.Is(err, auth.ErrInvalidWorkspaceAccess) {
			return nil, badWorkspaceAccessError(err.Error())
		}
		return nil, err
	}

	changes := map[string]any{"target_user_id": request.UserId}
	if input.Username != nil {
		changes["username"] = *input.Username
	}
	if input.Role != nil {
		changes["role"] = string(*input.Role)
	}
	if input.WorkspaceAccess != nil {
		changes["workspace_access"] = toAPIWorkspaceAccess(input.WorkspaceAccess)
	}
	if input.IsDisabled != nil {
		changes["is_disabled"] = *input.IsDisabled
	}
	a.logAudit(ctx, audit.CategoryUser, "user_update", changes)

	return api.UpdateUser200JSONResponse{
		User: toAPIUser(user),
	}, nil
}

// DeleteUser deletes a user account. Requires admin role. Cannot delete yourself.
func (a *API) DeleteUser(ctx context.Context, request api.DeleteUserRequestObject) (api.DeleteUserResponseObject, error) {
	if err := a.requireUserManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedRBAC(); err != nil {
		return nil, err
	}

	// Get current user to prevent self-deletion
	currentUser, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, &Error{
			Code:       api.ErrorCodeUnauthorized,
			Message:    "Not authenticated",
			HTTPStatus: http.StatusUnauthorized,
		}
	}

	// Get target user info before deletion for audit logging
	targetUser, _ := a.authService.GetUser(ctx, request.UserId)

	err := a.authService.DeleteUser(ctx, request.UserId, currentUser.ID)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return nil, &Error{
				Code:       api.ErrorCodeNotFound,
				Message:    "User not found",
				HTTPStatus: http.StatusNotFound,
			}
		}
		if errors.Is(err, authservice.ErrCannotDeleteSelf) {
			return nil, &Error{
				Code:       api.ErrorCodeForbidden,
				Message:    "Cannot delete your own account",
				HTTPStatus: http.StatusForbidden,
			}
		}
		return nil, err
	}

	deleteDetails := map[string]string{"target_user_id": request.UserId}
	if targetUser != nil {
		deleteDetails["target_username"] = targetUser.Username
	}
	a.logAudit(ctx, audit.CategoryUser, "user_delete", deleteDetails)

	return api.DeleteUser204Response{}, nil
}

// ResetUserPassword resets a user's password. Requires admin role.
func (a *API) ResetUserPassword(ctx context.Context, request api.ResetUserPasswordRequestObject) (api.ResetUserPasswordResponseObject, error) {
	if err := a.requireUserManagement(); err != nil {
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

	// Get target user info for audit logging
	targetUser, _ := a.authService.GetUser(ctx, request.UserId)

	err := a.authService.ResetPassword(ctx, request.UserId, request.Body.NewPassword)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return nil, &Error{
				Code:       api.ErrorCodeNotFound,
				Message:    "User not found",
				HTTPStatus: http.StatusNotFound,
			}
		}
		if errors.Is(err, authservice.ErrWeakPassword) {
			return nil, &Error{
				Code:       api.ErrorCodeBadRequest,
				Message:    "Password does not meet security requirements",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		if errors.Is(err, authservice.ErrExternalAuthPasswordManagement) {
			return nil, &Error{
				Code:       api.ErrorCodeForbidden,
				Message:    "Password is managed by the authentication provider for this user",
				HTTPStatus: http.StatusForbidden,
			}
		}
		return nil, err
	}

	resetDetails := map[string]string{"target_user_id": request.UserId}
	if targetUser != nil {
		resetDetails["target_username"] = targetUser.Username
	}
	a.logAudit(ctx, audit.CategoryUser, "password_reset", resetDetails)

	return api.ResetUserPassword200JSONResponse{
		Message: "Password reset successfully",
	}, nil
}
