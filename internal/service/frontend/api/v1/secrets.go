// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/workspace"
)

const (
	defaultSecretListLimit = 100
	maxSecretListLimit     = 500
)

func secretStoreUnavailable() *Error {
	return &Error{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       api.ErrorCodeInternalError,
		Message:    "Secret store not configured",
	}
}

func badSecretRequest(message string) *Error {
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    message,
	}
}

func secretNotFoundError() api.Error {
	return api.Error{
		Code:    api.ErrorCodeNotFound,
		Message: "Secret not found",
	}
}

func secretBadRequestError(message string) api.Error {
	return api.Error{
		Code:    api.ErrorCodeBadRequest,
		Message: message,
	}
}

func secretConflictError(message string) api.Error {
	return api.Error{
		Code:    api.ErrorCodeAlreadyExists,
		Message: message,
	}
}

func (a *API) ListSecrets(ctx context.Context, request api.ListSecretsRequestObject) (api.ListSecretsResponseObject, error) {
	if a.secretStore == nil {
		return nil, secretStoreUnavailable()
	}

	workspaceFilter, err := parseSecretWorkspaceSelector(request.Params.Workspace, false)
	if err != nil {
		return nil, err
	}
	if err := a.requireSecretManageForWorkspace(ctx, *workspaceFilter); err != nil {
		return nil, err
	}

	limit, offset, err := secretPagination(request.Params.Limit, request.Params.Offset)
	if err != nil {
		return nil, err
	}

	secrets, err := a.secretStore.List(ctx, secretpkg.ListOptions{Workspace: workspaceFilter})
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	visible := make([]api.SecretResponse, 0, len(secrets))
	for _, sec := range secrets {
		if !a.canManageSecretWorkspace(ctx, sec.Workspace) {
			continue
		}
		visible = append(visible, toSecretResponse(sec))
	}

	total := len(visible)
	if offset > total {
		visible = nil
	} else {
		end := min(offset+limit, total)
		visible = visible[offset:end]
	}

	return api.ListSecrets200JSONResponse{
		Secrets: visible,
		Total:   total,
	}, nil
}

func (a *API) CreateSecret(ctx context.Context, request api.CreateSecretRequestObject) (api.CreateSecretResponseObject, error) {
	if a.secretStore == nil {
		return nil, secretStoreUnavailable()
	}
	if request.Body == nil {
		return api.CreateSecret400JSONResponse(secretBadRequestError("Request body is required")), nil
	}

	body := request.Body
	workspaceName, err := parseSecretWorkspaceForMutation(body.Workspace)
	if err != nil {
		return api.CreateSecret400JSONResponse(secretBadRequestError(err.Error())), nil
	}
	if err := a.requireSecretManageForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}

	providerType := secretpkg.ProviderType(body.ProviderType)
	providerConnectionID := ""
	providerRef := ""
	if err := validateSecretProviderRequest(providerType, providerConnectionID, providerRef); err != nil {
		return api.CreateSecret400JSONResponse(secretBadRequestError(err.Error())), nil
	}
	fingerprint, err := a.secretProviderRefFingerprint(providerType, providerConnectionID, providerRef)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	actor := currentActorID(ctx)
	sec, err := secretpkg.New(secretpkg.CreateInput{
		Workspace:              workspaceName,
		Ref:                    body.Ref,
		Description:            valueOf(body.Description),
		ProviderType:           providerType,
		ProviderConnectionID:   providerConnectionID,
		ProviderRef:            providerRef,
		ProviderRefFingerprint: fingerprint,
		CreatedBy:              actor,
	}, now)
	if err != nil {
		return api.CreateSecret400JSONResponse(secretBadRequestError(err.Error())), nil
	}

	var initialValue *secretpkg.WriteValueInput
	if body.Value != nil {
		if *body.Value == "" {
			return api.CreateSecret400JSONResponse(secretBadRequestError("value must not be empty")), nil
		}
		initialValue = &secretpkg.WriteValueInput{
			Value:     *body.Value,
			CreatedBy: actor,
			CreatedAt: now,
		}
	}

	if err := a.secretStore.Create(ctx, sec, initialValue); err != nil {
		if errors.Is(err, secretpkg.ErrAlreadyExists) {
			return api.CreateSecret409JSONResponse(secretConflictError("Secret with this workspace and ref already exists")), nil
		}
		if isSecretValidationError(err) {
			return api.CreateSecret400JSONResponse(secretBadRequestError(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to create secret: %w", err)
	}

	created, err := a.secretStore.GetByID(ctx, sec.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to reload created secret: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "secret_create", secretAuditDetails(created))

	return api.CreateSecret201JSONResponse(toSecretResponse(created)), nil
}

func (a *API) GetSecret(ctx context.Context, request api.GetSecretRequestObject) (api.GetSecretResponseObject, error) {
	sec, err := a.getManagedSecret(ctx, request.SecretId)
	if err != nil {
		if errors.Is(err, secretpkg.ErrNotFound) {
			return api.GetSecret404JSONResponse(secretNotFoundError()), nil
		}
		return nil, err
	}
	return api.GetSecret200JSONResponse(toSecretResponse(sec)), nil
}

func (a *API) UpdateSecret(ctx context.Context, request api.UpdateSecretRequestObject) (api.UpdateSecretResponseObject, error) {
	sec, err := a.getManagedSecret(ctx, request.SecretId)
	if err != nil {
		if errors.Is(err, secretpkg.ErrNotFound) {
			return api.UpdateSecret404JSONResponse(secretNotFoundError()), nil
		}
		return nil, err
	}
	if request.Body == nil {
		return api.UpdateSecret400JSONResponse(secretBadRequestError("Request body is required")), nil
	}

	body := request.Body
	providerConnectionID := sec.ProviderConnectionID
	providerRef := sec.ProviderRef
	if body.ProviderConnectionId != nil {
		providerConnectionID = *body.ProviderConnectionId
	}
	if body.ProviderRef != nil {
		providerRef = *body.ProviderRef
	}
	if err := validateSecretProviderRequest(sec.ProviderType, providerConnectionID, providerRef); err != nil {
		return api.UpdateSecret400JSONResponse(secretBadRequestError(err.Error())), nil
	}

	update := secretpkg.UpdateInput{
		Description:          body.Description,
		ProviderConnectionID: body.ProviderConnectionId,
		ProviderRef:          body.ProviderRef,
		UpdatedBy:            currentActorID(ctx),
	}
	if body.ProviderConnectionId != nil || body.ProviderRef != nil {
		fingerprint, err := a.secretProviderRefFingerprint(sec.ProviderType, providerConnectionID, providerRef)
		if err != nil {
			return nil, err
		}
		update.ProviderRefFingerprint = &fingerprint
	}

	sec.ApplyUpdate(update, time.Now().UTC())
	if err := a.secretStore.Update(ctx, sec); err != nil {
		if errors.Is(err, secretpkg.ErrNotFound) {
			return api.UpdateSecret404JSONResponse(secretNotFoundError()), nil
		}
		if errors.Is(err, secretpkg.ErrAlreadyExists) || isSecretValidationError(err) {
			return api.UpdateSecret400JSONResponse(secretBadRequestError(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to update secret: %w", err)
	}

	updated, err := a.secretStore.GetByID(ctx, sec.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to reload updated secret: %w", err)
	}
	a.logAudit(ctx, audit.CategorySecret, "secret_update", secretAuditDetails(updated))

	return api.UpdateSecret200JSONResponse(toSecretResponse(updated)), nil
}

func (a *API) DeleteSecret(ctx context.Context, request api.DeleteSecretRequestObject) (api.DeleteSecretResponseObject, error) {
	sec, err := a.getManagedSecret(ctx, request.SecretId)
	if err != nil {
		if errors.Is(err, secretpkg.ErrNotFound) {
			return api.DeleteSecret404JSONResponse(secretNotFoundError()), nil
		}
		return nil, err
	}

	if err := a.secretStore.Delete(ctx, request.SecretId); err != nil {
		if errors.Is(err, secretpkg.ErrNotFound) {
			return api.DeleteSecret404JSONResponse(secretNotFoundError()), nil
		}
		return nil, fmt.Errorf("failed to delete secret: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "secret_delete", secretAuditDetails(sec))
	return api.DeleteSecret204Response{}, nil
}

func (a *API) DisableSecret(ctx context.Context, request api.DisableSecretRequestObject) (api.DisableSecretResponseObject, error) {
	sec, err := a.getManagedSecret(ctx, request.SecretId)
	if err != nil {
		if errors.Is(err, secretpkg.ErrNotFound) {
			return api.DisableSecret404JSONResponse(secretNotFoundError()), nil
		}
		return nil, err
	}

	if err := sec.SetStatus(secretpkg.StatusDisabled, currentActorID(ctx), time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := a.secretStore.Update(ctx, sec); err != nil {
		if errors.Is(err, secretpkg.ErrNotFound) {
			return api.DisableSecret404JSONResponse(secretNotFoundError()), nil
		}
		return nil, fmt.Errorf("failed to disable secret: %w", err)
	}

	updated, err := a.secretStore.GetByID(ctx, sec.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to reload disabled secret: %w", err)
	}
	a.logAudit(ctx, audit.CategorySecret, "secret_disable", secretAuditDetails(updated))

	return api.DisableSecret200JSONResponse(toSecretResponse(updated)), nil
}

func (a *API) EnableSecret(ctx context.Context, request api.EnableSecretRequestObject) (api.EnableSecretResponseObject, error) {
	sec, err := a.getManagedSecret(ctx, request.SecretId)
	if err != nil {
		if errors.Is(err, secretpkg.ErrNotFound) {
			return api.EnableSecret404JSONResponse(secretNotFoundError()), nil
		}
		return nil, err
	}

	if err := sec.SetStatus(secretpkg.StatusActive, currentActorID(ctx), time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := a.secretStore.Update(ctx, sec); err != nil {
		if errors.Is(err, secretpkg.ErrNotFound) {
			return api.EnableSecret404JSONResponse(secretNotFoundError()), nil
		}
		return nil, fmt.Errorf("failed to enable secret: %w", err)
	}

	updated, err := a.secretStore.GetByID(ctx, sec.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to reload enabled secret: %w", err)
	}
	a.logAudit(ctx, audit.CategorySecret, "secret_enable", secretAuditDetails(updated))

	return api.EnableSecret200JSONResponse(toSecretResponse(updated)), nil
}

func (a *API) WriteSecretVersion(ctx context.Context, request api.WriteSecretVersionRequestObject) (api.WriteSecretVersionResponseObject, error) {
	sec, err := a.getManagedSecret(ctx, request.SecretId)
	if err != nil {
		if errors.Is(err, secretpkg.ErrNotFound) {
			return api.WriteSecretVersion404JSONResponse(secretNotFoundError()), nil
		}
		return nil, err
	}
	if sec.ProviderType != secretpkg.ProviderDaguManaged {
		return api.WriteSecretVersion400JSONResponse(secretBadRequestError("only Dagu-managed secrets can store values")), nil
	}
	if request.Body == nil || request.Body.Value == nil || *request.Body.Value == "" {
		return api.WriteSecretVersion400JSONResponse(secretBadRequestError("value must not be empty")), nil
	}

	updated, err := a.secretStore.WriteValue(ctx, request.SecretId, secretpkg.WriteValueInput{
		Value:     *request.Body.Value,
		CreatedBy: currentActorID(ctx),
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		if errors.Is(err, secretpkg.ErrNotFound) {
			return api.WriteSecretVersion404JSONResponse(secretNotFoundError()), nil
		}
		if isSecretValidationError(err) || errors.Is(err, secretpkg.ErrUnsupportedProvider) {
			return api.WriteSecretVersion400JSONResponse(secretBadRequestError(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to write secret version: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "secret_value_write", secretAuditDetails(updated))

	return api.WriteSecretVersion200JSONResponse(toSecretResponse(updated)), nil
}

func (a *API) getManagedSecret(ctx context.Context, id string) (*secretpkg.Secret, error) {
	if a.secretStore == nil {
		return nil, secretStoreUnavailable()
	}
	sec, err := a.secretStore.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := a.requireSecretManageForWorkspace(ctx, sec.Workspace); err != nil {
		if apiErr, ok := err.(*Error); ok && apiErr.HTTPStatus == http.StatusNotFound {
			return nil, secretpkg.ErrNotFound
		}
		return nil, err
	}
	return sec, nil
}

func (a *API) requireSecretManageForWorkspace(ctx context.Context, workspaceName string) error {
	role, ok, err := a.effectiveRoleForWorkspace(ctx, workspaceName)
	if err != nil {
		return err
	}
	if !ok {
		return workspaceResourceNotFound()
	}
	if !role.CanManageAudit() {
		return errInsufficientPermissions
	}
	return nil
}

func (a *API) canManageSecretWorkspace(ctx context.Context, workspaceName string) bool {
	role, ok, err := a.effectiveRoleForWorkspace(ctx, workspaceName)
	return err == nil && ok && role.CanManageAudit()
}

func parseSecretWorkspaceSelector(raw *string, allowAll bool) (*string, error) {
	if raw == nil {
		if allowAll {
			return nil, nil
		}
		defaultWorkspace := ""
		return &defaultWorkspace, nil
	}
	value := strings.TrimSpace(*raw)
	switch value {
	case "", "default":
		defaultWorkspace := ""
		return &defaultWorkspace, nil
	case "all":
		if allowAll {
			return nil, nil
		}
		return nil, badSecretRequest("workspace cannot be all")
	default:
		if err := workspace.ValidateName(value); err != nil {
			return nil, badSecretRequest(err.Error())
		}
		return &value, nil
	}
}

func parseSecretWorkspaceForMutation(raw *string) (string, error) {
	workspaceName, err := parseSecretWorkspaceSelector(raw, false)
	if err != nil {
		return "", err
	}
	return *workspaceName, nil
}

func secretPagination(limitParam, offsetParam *int) (int, int, error) {
	limit := defaultSecretListLimit
	if limitParam != nil {
		limit = *limitParam
	}
	if limit <= 0 {
		return 0, 0, badSecretRequest("limit must be greater than zero")
	}
	if limit > maxSecretListLimit {
		limit = maxSecretListLimit
	}

	offset := 0
	if offsetParam != nil {
		offset = *offsetParam
	}
	if offset < 0 {
		return 0, 0, badSecretRequest("offset must not be negative")
	}
	return limit, offset, nil
}

func validateSecretProviderRequest(providerType secretpkg.ProviderType, providerConnectionID, providerRef string) error {
	if err := secretpkg.ValidateProviderType(providerType); err != nil {
		return err
	}
	if providerType != secretpkg.ProviderDaguManaged {
		return errors.New("external secret providers are request-based; contact https://dagu.sh/contact")
	}
	if providerConnectionID != "" || providerRef != "" {
		return errors.New("dagu-managed secrets must not set provider connection or provider ref")
	}
	return nil
}

func (a *API) secretProviderRefFingerprint(providerType secretpkg.ProviderType, providerConnectionID, providerRef string) (string, error) {
	if providerRef == "" {
		return "", nil
	}
	if a.config == nil || a.config.Paths.DataDir == "" {
		return "", errors.New("secret fingerprint key is not configured")
	}
	key, err := crypto.ResolveKey(a.config.Paths.DataDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve secret fingerprint key: %w", err)
	}
	return secretpkg.ProviderRefFingerprint(key, providerType, providerConnectionID, providerRef)
}

func currentActorID(ctx context.Context) string {
	user, ok := auth.UserFromContext(ctx)
	if !ok || user == nil {
		return ""
	}
	if user.ID != "" {
		return user.ID
	}
	return user.Username
}

func isSecretValidationError(err error) bool {
	return errors.Is(err, secretpkg.ErrInvalidRef) ||
		errors.Is(err, secretpkg.ErrInvalidProviderType) ||
		errors.Is(err, secretpkg.ErrInvalidSecretID) ||
		errors.Is(err, secretpkg.ErrInvalidStatus) ||
		errors.Is(err, secretpkg.ErrInvalidWorkspace) ||
		errors.Is(err, secretpkg.ErrNoValue)
}

func toSecretResponse(sec *secretpkg.Secret) api.SecretResponse {
	resp := api.SecretResponse{
		CreatedAt:      sec.CreatedAt,
		CurrentVersion: sec.CurrentVersion,
		HasValue:       sec.CurrentVersion > 0,
		Id:             sec.ID,
		ProviderType:   api.SecretProviderType(sec.ProviderType),
		Ref:            sec.Ref,
		Status:         api.SecretStatus(sec.Status),
		UpdatedAt:      sec.UpdatedAt,
		Workspace:      secretWorkspaceToAPI(sec.Workspace),
	}
	resp.Description = ptrOf(sec.Description)
	resp.ProviderConnectionId = ptrOf(sec.ProviderConnectionID)
	resp.ProviderRef = ptrOf(sec.ProviderRef)
	resp.ProviderRefFingerprint = ptrOf(sec.ProviderRefFingerprint)
	resp.LastCheckedAt = cloneTimePtr(sec.LastCheckedAt)
	resp.LastResolvedAt = cloneTimePtr(sec.LastResolvedAt)
	resp.LastRotatedAt = cloneTimePtr(sec.LastRotatedAt)
	return resp
}

func secretWorkspaceToAPI(workspaceName string) string {
	if workspaceName == "" {
		return "default"
	}
	return workspaceName
}

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	clone := *t
	return &clone
}

func secretAuditDetails(sec *secretpkg.Secret) map[string]any {
	return map[string]any{
		"id":              sec.ID,
		"workspace":       secretWorkspaceToAPI(sec.Workspace),
		"ref":             sec.Ref,
		"provider_type":   string(sec.ProviderType),
		"current_version": sec.CurrentVersion,
		"status":          string(sec.Status),
	}
}
