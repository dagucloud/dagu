// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	profilepkg "github.com/dagucloud/dagu/v2/internal/profile"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
	"github.com/google/uuid"
	openapitypes "github.com/oapi-codegen/runtime/types"
)

const maxWebhookAllowedProfiles = 100

// ListWebhooks returns all webhooks across all DAGs.
// Requires developer role or above.
func (a *API) ListWebhooks(ctx context.Context, _ api.ListWebhooksRequestObject) (api.ListWebhooksResponseObject, error) {
	if err := a.requireWebhookManagement(ctx); err != nil {
		return nil, err
	}

	webhooks, err := a.authService.ListWebhooks(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list webhooks", tag.Error(err))
		return nil, &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    "failed to list webhooks",
		}
	}

	response := make([]api.WebhookDetails, 0, len(webhooks))
	for _, wh := range webhooks {
		response = append(response, toWebhookDetails(wh))
	}

	return api.ListWebhooks200JSONResponse{Webhooks: response}, nil
}

// GetDAGWebhook returns the webhook configuration for a specific DAG.
// Requires developer role or above.
func (a *API) GetDAGWebhook(ctx context.Context, request api.GetDAGWebhookRequestObject) (api.GetDAGWebhookResponseObject, error) {
	if err := a.requireWebhookManagement(ctx); err != nil {
		return nil, err
	}

	webhook, err := a.authService.GetWebhookByDAGName(ctx, request.FileName)
	if err != nil {
		if errors.Is(err, auth.ErrWebhookNotFound) {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("no webhook configured for DAG %s", request.FileName),
			}
		}
		logger.Error(ctx, "Failed to get webhook", tag.Name(request.FileName), tag.Error(err))
		return nil, &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    "failed to get webhook",
		}
	}

	return api.GetDAGWebhook200JSONResponse(toWebhookDetails(webhook)), nil
}

// CreateDAGWebhook creates a new webhook for a DAG.
// Requires developer role or above.
func (a *API) CreateDAGWebhook(ctx context.Context, request api.CreateDAGWebhookRequestObject) (api.CreateDAGWebhookResponseObject, error) {
	if err := a.requireWebhookManagement(ctx); err != nil {
		return nil, err
	}

	// Check if DAG exists
	if _, err := a.dagRepository.GetDetails(ctx, string(request.FileName), persis.DAGLoadOptions{}); err != nil {
		if errors.Is(err, persis.ErrDAGNotFound) {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("DAG %s not found", request.FileName),
			}
		}
		return nil, err
	}

	// Get creator ID from context
	creatorID := getCreatorID(ctx)

	result, err := a.authService.CreateWebhook(ctx, request.FileName, creatorID)
	if err != nil {
		if errors.Is(err, auth.ErrWebhookAlreadyExists) {
			return nil, &Error{
				HTTPStatus: http.StatusConflict,
				Code:       api.ErrorCodeAlreadyExists,
				Message:    fmt.Sprintf("webhook already exists for DAG %s", request.FileName),
			}
		}
		logger.Error(ctx, "Failed to create webhook", tag.Name(request.FileName), tag.Error(err))
		return nil, &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    "failed to create webhook",
		}
	}

	logger.Info(ctx, "Webhook created", tag.Name(request.FileName))
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_create", map[string]any{
		"dag_name":   request.FileName,
		"webhook_id": result.Webhook.ID,
	})

	return api.CreateDAGWebhook201JSONResponse{
		Webhook: toWebhookDetails(result.Webhook),
		Token:   result.FullToken,
	}, nil
}

// DeleteDAGWebhook removes the webhook for a DAG.
// Requires developer role or above.
func (a *API) DeleteDAGWebhook(ctx context.Context, request api.DeleteDAGWebhookRequestObject) (api.DeleteDAGWebhookResponseObject, error) {
	if err := a.requireWebhookManagement(ctx); err != nil {
		return nil, err
	}

	// Get webhook info before deletion for audit logging
	targetWebhook, _ := a.authService.GetWebhookByDAGName(ctx, request.FileName)

	err := a.authService.DeleteWebhook(ctx, request.FileName)
	if err != nil {
		if errors.Is(err, auth.ErrWebhookNotFound) {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("no webhook configured for DAG %s", request.FileName),
			}
		}
		logger.Error(ctx, "Failed to delete webhook", tag.Name(request.FileName), tag.Error(err))
		return nil, &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    "failed to delete webhook",
		}
	}

	logger.Info(ctx, "Webhook deleted", tag.Name(request.FileName))
	auditDetails := map[string]any{"dag_name": request.FileName}
	if targetWebhook != nil {
		auditDetails["webhook_id"] = targetWebhook.ID
	}
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_delete", auditDetails)

	return api.DeleteDAGWebhook204Response{}, nil
}

// RegenerateDAGWebhookToken generates a new token for an existing webhook.
// Requires developer role or above.
func (a *API) RegenerateDAGWebhookToken(ctx context.Context, request api.RegenerateDAGWebhookTokenRequestObject) (api.RegenerateDAGWebhookTokenResponseObject, error) {
	if err := a.requireWebhookManagement(ctx); err != nil {
		return nil, err
	}

	result, err := a.authService.RegenerateWebhookToken(ctx, request.FileName)
	if err != nil {
		if errors.Is(err, auth.ErrWebhookNotFound) {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("no webhook configured for DAG %s", request.FileName),
			}
		}
		logger.Error(ctx, "Failed to regenerate webhook token", tag.Name(request.FileName), tag.Error(err))
		return nil, &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    "failed to regenerate webhook token",
		}
	}

	logger.Info(ctx, "Webhook token regenerated", tag.Name(request.FileName))
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_token_regenerate", map[string]any{
		"dag_name":   request.FileName,
		"webhook_id": result.Webhook.ID,
	})

	return api.RegenerateDAGWebhookToken200JSONResponse{
		Webhook: toWebhookDetails(result.Webhook),
		Token:   result.FullToken,
	}, nil
}

// ToggleDAGWebhook enables or disables a webhook.
// Requires developer role or above.
func (a *API) ToggleDAGWebhook(ctx context.Context, request api.ToggleDAGWebhookRequestObject) (api.ToggleDAGWebhookResponseObject, error) {
	if err := a.requireWebhookManagement(ctx); err != nil {
		return nil, err
	}

	webhook, err := a.authService.ToggleWebhook(ctx, request.FileName, request.Body.Enabled)
	if err != nil {
		if errors.Is(err, auth.ErrWebhookNotFound) {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("no webhook configured for DAG %s", request.FileName),
			}
		}
		logger.Error(ctx, "Failed to toggle webhook", tag.Name(request.FileName), tag.Error(err))
		return nil, &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    "failed to toggle webhook",
		}
	}

	logger.Info(ctx, "Webhook toggled",
		tag.Name(request.FileName),
		tag.Key("enabled"), tag.Value(request.Body.Enabled),
	)
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_toggle", map[string]any{
		"dag_name":   request.FileName,
		"webhook_id": webhook.ID,
		"enabled":    request.Body.Enabled,
	})

	return api.ToggleDAGWebhook200JSONResponse(toWebhookDetails(webhook)), nil
}

// ConfigureDAGWebhookProfileSelection replaces the profiles webhook callers may select.
func (a *API) ConfigureDAGWebhookProfileSelection(
	ctx context.Context,
	request api.ConfigureDAGWebhookProfileSelectionRequestObject,
) (api.ConfigureDAGWebhookProfileSelectionResponseObject, error) {
	if err := a.requireWebhookManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return api.ConfigureDAGWebhookProfileSelection400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: "request body is required",
		}, nil
	}
	if request.Body.AllowedProfiles == nil {
		return api.ConfigureDAGWebhookProfileSelection400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: "allowedProfiles is required",
		}, nil
	}
	if len(request.Body.AllowedProfiles) > maxWebhookAllowedProfiles {
		return api.ConfigureDAGWebhookProfileSelection400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: fmt.Sprintf("at most %d runtime profiles may be selected", maxWebhookAllowedProfiles),
		}, nil
	}

	allowedProfiles := make([]string, 0, len(request.Body.AllowedProfiles))
	seen := make(map[string]struct{}, len(request.Body.AllowedProfiles))
	for _, requested := range request.Body.AllowedProfiles {
		requestedName := strings.TrimSpace(string(requested))
		if err := profilepkg.ValidateName(requestedName); err != nil {
			return api.ConfigureDAGWebhookProfileSelection400JSONResponse(
				runtimeProfileBadRequest(err.Error()),
			), nil
		}
		profileName, err := a.ensureRunnableRuntimeProfile(ctx, requestedName)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[profileName]; exists {
			continue
		}
		seen[profileName] = struct{}{}
		allowedProfiles = append(allowedProfiles, profileName)
	}
	slices.Sort(allowedProfiles)

	webhook, err := a.authService.ConfigureWebhookProfiles(ctx, request.FileName, allowedProfiles)
	if err != nil {
		if errors.Is(err, auth.ErrWebhookNotFound) {
			return api.ConfigureDAGWebhookProfileSelection404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("no webhook configured for DAG %s", request.FileName),
			}, nil
		}
		return nil, err
	}

	a.logAudit(ctx, audit.CategoryWebhook, "webhook_profile_selection_configure", map[string]any{
		"dag_name":         request.FileName,
		"webhook_id":       webhook.ID,
		"allowed_profiles": append([]string(nil), allowedProfiles...),
	})

	return api.ConfigureDAGWebhookProfileSelection200JSONResponse(toWebhookDetails(webhook)), nil
}

func mapWebhookHMACError(err error, dagName string) (int, api.Error, bool) {
	switch {
	case errors.Is(err, auth.ErrWebhookNotFound):
		return http.StatusNotFound, api.Error{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("no webhook configured for DAG %s", dagName),
		}, true
	case errors.Is(err, authservice.ErrInvalidWebhookAuthMode),
		errors.Is(err, authservice.ErrInvalidWebhookHMACEnforcementMode),
		errors.Is(err, authservice.ErrWebhookHMACNotConfigured):
		return http.StatusBadRequest, api.Error{
			Code:    api.ErrorCodeBadRequest,
			Message: err.Error(),
		}, true
	case errors.Is(err, authservice.ErrWebhookHMACNotSupported):
		return http.StatusNotImplemented, api.Error{
			Code:    api.ErrorCodeInternalError,
			Message: err.Error(),
		}, true
	default:
		return 0, api.Error{}, false
	}
}

// EnableDAGWebhookHMAC enables HMAC auth for an existing webhook.
// Requires developer role or above.
func (a *API) EnableDAGWebhookHMAC(ctx context.Context, request api.EnableDAGWebhookHMACRequestObject) (api.EnableDAGWebhookHMACResponseObject, error) {
	if err := a.requireWebhookManagement(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return api.EnableDAGWebhookHMACdefaultJSONResponse{
			StatusCode: http.StatusBadRequest,
			Body: api.Error{
				Code:    api.ErrorCodeBadRequest,
				Message: "request body is required",
			},
		}, nil
	}

	result, err := a.authService.EnableWebhookHMAC(
		ctx,
		request.FileName,
		auth.WebhookAuthMode(request.Body.AuthMode),
		auth.WebhookHMACEnforcementMode(valueOf(request.Body.EnforcementMode)),
	)
	if err != nil {
		if statusCode, body, handled := mapWebhookHMACError(err, request.FileName); handled {
			if statusCode == http.StatusNotFound {
				return api.EnableDAGWebhookHMAC404JSONResponse(body), nil
			}
			return api.EnableDAGWebhookHMACdefaultJSONResponse{
				StatusCode: statusCode,
				Body:       body,
			}, nil
		}
		logger.Error(ctx, "Failed to enable webhook HMAC", tag.Name(request.FileName), tag.Error(err))
		return api.EnableDAGWebhookHMACdefaultJSONResponse{
			StatusCode: http.StatusInternalServerError,
			Body: api.Error{
				Code:    api.ErrorCodeInternalError,
				Message: "failed to enable webhook HMAC",
			},
		}, nil
	}

	logger.Info(ctx, "Webhook HMAC enabled", tag.Name(request.FileName))
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_hmac_enable", map[string]any{
		"dag_name":   request.FileName,
		"webhook_id": result.Webhook.ID,
		"auth_mode":  result.Webhook.EffectiveAuthMode(),
	})

	return api.EnableDAGWebhookHMAC200JSONResponse{
		Webhook:    toWebhookDetails(result.Webhook),
		HmacSecret: result.FullSecret,
	}, nil
}

// ConfigureDAGWebhookHMAC updates HMAC auth mode or enforcement for an existing webhook.
// Requires developer role or above.
func (a *API) ConfigureDAGWebhookHMAC(ctx context.Context, request api.ConfigureDAGWebhookHMACRequestObject) (api.ConfigureDAGWebhookHMACResponseObject, error) {
	if err := a.requireWebhookManagement(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return api.ConfigureDAGWebhookHMACdefaultJSONResponse{
			StatusCode: http.StatusBadRequest,
			Body: api.Error{
				Code:    api.ErrorCodeBadRequest,
				Message: "request body is required",
			},
		}, nil
	}

	webhook, err := a.authService.ConfigureWebhookHMAC(
		ctx,
		request.FileName,
		auth.WebhookAuthMode(request.Body.AuthMode),
		auth.WebhookHMACEnforcementMode(valueOf(request.Body.EnforcementMode)),
	)
	if err != nil {
		if statusCode, body, handled := mapWebhookHMACError(err, request.FileName); handled {
			if statusCode == http.StatusNotFound {
				return api.ConfigureDAGWebhookHMAC404JSONResponse(body), nil
			}
			return api.ConfigureDAGWebhookHMACdefaultJSONResponse{
				StatusCode: statusCode,
				Body:       body,
			}, nil
		}
		logger.Error(ctx, "Failed to configure webhook HMAC", tag.Name(request.FileName), tag.Error(err))
		return api.ConfigureDAGWebhookHMACdefaultJSONResponse{
			StatusCode: http.StatusInternalServerError,
			Body: api.Error{
				Code:    api.ErrorCodeInternalError,
				Message: "failed to configure webhook HMAC",
			},
		}, nil
	}

	logger.Info(ctx, "Webhook HMAC configured", tag.Name(request.FileName))
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_hmac_configure", map[string]any{
		"dag_name":   request.FileName,
		"webhook_id": webhook.ID,
		"auth_mode":  webhook.EffectiveAuthMode(),
	})

	return api.ConfigureDAGWebhookHMAC200JSONResponse(toWebhookDetails(webhook)), nil
}

// RegenerateDAGWebhookHMACSecret regenerates the HMAC secret for an existing webhook.
// Requires developer role or above.
func (a *API) RegenerateDAGWebhookHMACSecret(ctx context.Context, request api.RegenerateDAGWebhookHMACSecretRequestObject) (api.RegenerateDAGWebhookHMACSecretResponseObject, error) {
	if err := a.requireWebhookManagement(ctx); err != nil {
		return nil, err
	}

	result, err := a.authService.RegenerateWebhookHMACSecret(ctx, request.FileName)
	if err != nil {
		if statusCode, body, handled := mapWebhookHMACError(err, request.FileName); handled {
			if statusCode == http.StatusNotFound {
				return api.RegenerateDAGWebhookHMACSecret404JSONResponse(body), nil
			}
			return api.RegenerateDAGWebhookHMACSecretdefaultJSONResponse{
				StatusCode: statusCode,
				Body:       body,
			}, nil
		}
		logger.Error(ctx, "Failed to regenerate webhook HMAC secret", tag.Name(request.FileName), tag.Error(err))
		return api.RegenerateDAGWebhookHMACSecretdefaultJSONResponse{
			StatusCode: http.StatusInternalServerError,
			Body: api.Error{
				Code:    api.ErrorCodeInternalError,
				Message: "failed to regenerate webhook HMAC secret",
			},
		}, nil
	}

	logger.Info(ctx, "Webhook HMAC secret regenerated", tag.Name(request.FileName))
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_hmac_regenerate", map[string]any{
		"dag_name":   request.FileName,
		"webhook_id": result.Webhook.ID,
	})

	return api.RegenerateDAGWebhookHMACSecret200JSONResponse{
		Webhook:    toWebhookDetails(result.Webhook),
		HmacSecret: result.FullSecret,
	}, nil
}

// DisableDAGWebhookHMAC disables HMAC auth for an existing webhook.
// Requires developer role or above.
func (a *API) DisableDAGWebhookHMAC(ctx context.Context, request api.DisableDAGWebhookHMACRequestObject) (api.DisableDAGWebhookHMACResponseObject, error) {
	if err := a.requireWebhookManagement(ctx); err != nil {
		return nil, err
	}

	webhook, err := a.authService.DisableWebhookHMAC(ctx, request.FileName)
	if err != nil {
		if statusCode, body, handled := mapWebhookHMACError(err, request.FileName); handled {
			if statusCode == http.StatusNotFound {
				return api.DisableDAGWebhookHMAC404JSONResponse(body), nil
			}
			return api.DisableDAGWebhookHMACdefaultJSONResponse{
				StatusCode: statusCode,
				Body:       body,
			}, nil
		}
		logger.Error(ctx, "Failed to disable webhook HMAC", tag.Name(request.FileName), tag.Error(err))
		return api.DisableDAGWebhookHMACdefaultJSONResponse{
			StatusCode: http.StatusInternalServerError,
			Body: api.Error{
				Code:    api.ErrorCodeInternalError,
				Message: "failed to disable webhook HMAC",
			},
		}, nil
	}

	logger.Info(ctx, "Webhook HMAC disabled", tag.Name(request.FileName))
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_hmac_disable", map[string]any{
		"dag_name":   request.FileName,
		"webhook_id": webhook.ID,
	})

	return api.DisableDAGWebhookHMAC200JSONResponse(toWebhookDetails(webhook)), nil
}

// TriggerWebhook triggers a DAG execution via webhook.
// Authentication is performed according to the webhook's configured auth mode.
func (a *API) TriggerWebhook(ctx context.Context, request api.TriggerWebhookRequestObject) (api.TriggerWebhookResponseObject, error) {
	// Ensure webhook triggering is configured on this server
	if a.authService == nil || !a.authService.HasWebhookStore() {
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    "webhook triggering is not configured on this server",
		}
	}

	token := extractWebhookToken(valueOf(request.Params.Authorization))
	signature := valueOf(request.Params.XDaguSignature)
	requestedProfile, profileErr := requestedWebhookProfile(ctx, request.Params.XDaguProfile)
	if profileErr != nil {
		return nil, profileErr
	}
	rawBody := rawBodyFromContext(ctx)
	maxPayloadSize := a.webhookMaxPayloadSize()
	if len(rawBody) > maxPayloadSize {
		logger.Warn(ctx, "Webhook: payload too large",
			tag.Name(request.FileName),
			tag.Key("size"), tag.Value(len(rawBody)),
			tag.Key("maxSize"), tag.Value(maxPayloadSize),
		)
		return nil, &Error{
			HTTPStatus: http.StatusRequestEntityTooLarge,
			Code:       api.ErrorCodeBadRequest,
			Message:    fmt.Sprintf("webhook payload too large (max %d bytes)", maxPayloadSize),
		}
	}

	webhook, err := a.authService.AuthorizeWebhookRequest(
		ctx,
		authservice.AuthorizeWebhookRequestInput{
			DAGName:     request.FileName,
			Token:       token,
			Signature:   signature,
			ProfileName: requestedProfile,
			Body:        rawBody,
		},
	)
	if err != nil {
		if errors.Is(err, authservice.ErrInvalidWebhookToken) {
			logger.Warn(ctx, "Webhook: invalid token", tag.Name(request.FileName))
			return nil, &Error{
				HTTPStatus: http.StatusUnauthorized,
				Code:       api.ErrorCodeUnauthorized,
				Message:    "invalid webhook token",
			}
		}
		if errors.Is(err, authservice.ErrMissingWebhookHMACSignature) {
			logger.Warn(ctx, "Webhook: missing HMAC signature", tag.Name(request.FileName))
			return nil, &Error{
				HTTPStatus: http.StatusUnauthorized,
				Code:       api.ErrorCodeUnauthorized,
				Message:    "missing webhook HMAC signature",
			}
		}
		if errors.Is(err, authservice.ErrInvalidWebhookHMACSignature) {
			logger.Warn(ctx, "Webhook: invalid HMAC signature", tag.Name(request.FileName))
			return nil, &Error{
				HTTPStatus: http.StatusUnauthorized,
				Code:       api.ErrorCodeUnauthorized,
				Message:    "invalid webhook HMAC signature",
			}
		}
		if errors.Is(err, authservice.ErrWebhookDisabled) {
			logger.Warn(ctx, "Webhook: disabled", tag.Name(request.FileName))
			return nil, &Error{
				HTTPStatus: http.StatusForbidden,
				Code:       api.ErrorCodeForbidden,
				Message:    "webhook is disabled",
			}
		}
		logger.Error(ctx, "Webhook: validation failed", tag.Name(request.FileName), tag.Error(err))
		return nil, &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    "webhook validation failed",
		}
	}

	// Load the DAG (we need it for enqueuing)
	dag, err := a.dagRepository.GetDetails(ctx, string(request.FileName), persis.DAGLoadOptions{})
	if err != nil {
		logger.Warn(ctx, "Webhook: DAG not found",
			tag.Name(request.FileName),
			tag.Error(err),
		)
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("DAG %s not found", request.FileName),
		}
	}

	profileName, err := a.webhookRunProfile(ctx, webhook, request.FileName, dagWorkspaceName(dag), requestedProfile)
	if err != nil {
		return nil, err
	}

	params, apiErr := buildWebhookRequestRuntimeParams(ctx, dag, request.Body, maxPayloadSize)
	if apiErr != nil {
		return nil, apiErr
	}

	// Determine the dag-run ID (use provided one for idempotency, or generate new)
	var dagRunID string
	if request.Body != nil && request.Body.DagRunId != nil && *request.Body.DagRunId != "" {
		dagRunID = *request.Body.DagRunId
		// Check if a dag-run with this ID already exists
		statuses, err := a.dagRunRepository.ListStatuses(ctx, persis.DAGRunListOptions{DAGRunID: dagRunID})
		if err != nil {
			return nil, &Error{
				HTTPStatus: http.StatusInternalServerError,
				Code:       api.ErrorCodeInternalError,
				Message:    "failed to verify webhook DAG-run ID",
			}
		}
		if len(statuses) > 0 {
			// DAG run already exists - return 409 Conflict
			logger.Info(ctx, "Webhook: DAG run already exists (idempotency)",
				tag.DAG(dag.Name),
				tag.RunID(dagRunID),
			)
			return api.TriggerWebhook409JSONResponse{
				Code:    api.ErrorCodeAlreadyExists,
				Message: fmt.Sprintf("dag-run with ID %s already exists", dagRunID),
			}, nil
		}
		// If no results, proceed with creating.
	} else {
		dagRunID = uuid.Must(uuid.NewV7()).String()
	}

	// Enqueue directly from the API layer so accepted webhook payloads do not
	// need to fit in a subprocess command line.
	if err := a.enqueuePreparedDAGRun(ctx, dag, params, dagRunID, ir.TriggerTypeWebhook, profileName, false); err != nil {
		logger.Error(ctx, "Webhook: failed to enqueue DAG run",
			tag.DAG(dag.Name),
			tag.Error(err),
		)
		return nil, &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    "failed to enqueue DAG run",
		}
	}

	logger.Info(ctx, "Webhook: DAG run enqueued",
		tag.DAG(dag.Name),
		tag.RunID(dagRunID),
		tag.Key("webhookID"), tag.Value(webhook.ID),
		tag.Key("profile"), tag.Value(profileName),
	)

	return api.TriggerWebhook200JSONResponse{
		DagRunId: dagRunID,
		DagName:  dag.Name,
	}, nil
}

func requestedWebhookProfile(ctx context.Context, raw *api.RuntimeProfileName) (string, *Error) {
	values := requestHeadersFromContext(ctx)[ir.NormalizeWebhookForwardHeader("X-Dagu-Profile")]
	if len(values) > 1 {
		return "", invalidWebhookProfileHeader()
	}
	if raw == nil {
		return "", nil
	}

	profileName := strings.TrimSpace(string(*raw))
	if profileName == "" || profilepkg.ValidateName(profileName) != nil {
		return "", invalidWebhookProfileHeader()
	}
	return profileName, nil
}

func invalidWebhookProfileHeader() *Error {
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    "invalid X-Dagu-Profile header",
	}
}

func (a *API) webhookRunProfile(
	ctx context.Context,
	webhook *auth.Webhook,
	dagName string,
	workspaceName string,
	requestedProfile string,
) (string, error) {
	if requestedProfile == "" {
		return a.defaultRunProfileName(ctx, dagName, workspaceName)
	}
	if !slices.Contains(webhook.AllowedProfiles, requestedProfile) {
		return "", &Error{
			HTTPStatus: http.StatusForbidden,
			Code:       api.ErrorCodeForbidden,
			Message:    "runtime profile selection is not allowed for this webhook",
		}
	}
	return a.ensureRunnableRuntimeProfileAvailable(ctx, requestedProfile)
}

func buildWebhookRequestRuntimeParams(
	ctx context.Context,
	dag *ir.DAG,
	body *api.TriggerWebhookJSONRequestBody,
	maxPayloadSize int,
) (string, *Error) {
	payload, err := marshalWebhookPayload(ctx, body)
	if err != nil {
		logger.Warn(ctx, "Webhook: failed to marshal payload",
			tag.DAG(dag.Name),
			tag.Error(err),
		)
		return "", &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "failed to process request body",
		}
	}
	if len(payload) > maxPayloadSize {
		logger.Warn(ctx, "Webhook: payload too large",
			tag.DAG(dag.Name),
			tag.Key("size"), tag.Value(len(payload)),
			tag.Key("maxSize"), tag.Value(maxPayloadSize),
		)
		return "", &Error{
			HTTPStatus: http.StatusRequestEntityTooLarge,
			Code:       api.ErrorCodeBadRequest,
			Message:    fmt.Sprintf("webhook payload too large (max %d bytes)", maxPayloadSize),
		}
	}

	headerAllowList := []string(nil)
	if dag.Webhook != nil {
		headerAllowList = dag.Webhook.ForwardHeaders
	}

	headers, err := marshalWebhookHeaders(ctx, headerAllowList)
	if err != nil {
		logger.Warn(ctx, "Webhook: failed to marshal headers",
			tag.DAG(dag.Name),
			tag.Error(err),
		)
		return "", &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "failed to process request headers",
		}
	}

	return buildWebhookRuntimeParams(payload, headers, nil), nil
}

// requireWebhookManagement checks if webhook management is enabled and
// enforces developer-or-above role for all webhook management operations.
func (a *API) requireWebhookManagement(ctx context.Context) error {
	if a.authService == nil || !a.authService.HasWebhookStore() {
		return &Error{
			Code:       api.ErrorCodeUnauthorized,
			Message:    "Webhook management is not enabled",
			HTTPStatus: http.StatusUnauthorized,
		}
	}
	// Enforce developer-or-above role when auth is enabled
	return a.requireDeveloperOrAbove(ctx)
}

// extractWebhookToken extracts the token from the Authorization header.
// It expects the format "Bearer <token>".
func extractWebhookToken(authHeader string) string {
	token, found := strings.CutPrefix(authHeader, "Bearer ")
	if !found {
		return ""
	}
	return token
}

// toWebhookDetails converts an auth.Webhook to an api.WebhookDetails.
func toWebhookDetails(wh *auth.Webhook) api.WebhookDetails {
	// Parse UUID - if invalid (shouldn't happen as we generate it), use nil UUID
	parsedID, err := uuid.Parse(wh.ID)
	if err != nil {
		parsedID = uuid.Nil
	}

	return api.WebhookDetails{
		Id:          openapitypes.UUID(parsedID),
		DagName:     wh.DAGName,
		TokenPrefix: wh.TokenPrefix,
		Enabled:     wh.Enabled,
		AuthMode:    api.WebhookAuthMode(wh.EffectiveAuthMode()),
		Hmac:        toWebhookHMACDetails(wh),
		ProfileSelection: api.WebhookProfileSelectionDetails{
			AllowedProfiles: toRuntimeProfileNames(wh.AllowedProfiles),
		},
		CreatedAt:  wh.CreatedAt,
		UpdatedAt:  wh.UpdatedAt,
		CreatedBy:  ptrOf(wh.CreatedBy),
		LastUsedAt: wh.LastUsedAt,
	}
}

func toRuntimeProfileNames(names []string) []api.RuntimeProfileName {
	result := make([]api.RuntimeProfileName, 0, len(names))
	for _, name := range names {
		result = append(result, api.RuntimeProfileName(name))
	}
	return result
}

func toWebhookHMACDetails(wh *auth.Webhook) api.WebhookHMACDetails {
	enabled := wh.HMACEnabled()
	details := api.WebhookHMACDetails{
		Enabled:          enabled,
		SecretConfigured: wh.HMACSecretConfigured(),
	}

	if !enabled {
		return details
	}

	enforcementMode := api.WebhookHMACEnforcementMode(wh.HMACEnforcementMode)
	if enforcementMode == "" {
		enforcementMode = api.WebhookHMACEnforcementModeStrict
	}

	details.EnforcementMode = ptrOf(enforcementMode)
	details.Algorithm = ptrOf(auth.WebhookHMACAlgorithm)
	details.HeaderName = ptrOf(auth.WebhookHMACHeaderName)
	details.Format = ptrOf(auth.WebhookHMACHeaderValueFormat)
	details.UpdatedAt = wh.HMACSecretGeneratedAt

	return details
}

// getCreatorID extracts the user ID from context or returns a default value.
func getCreatorID(ctx context.Context) string {
	user, ok := auth.UserFromContext(ctx)
	if ok && user != nil {
		return user.ID
	}
	return "system"
}

// marshalWebhookPayload returns the JSON representation of the webhook payload.
// If the structured "payload" field is present, it is used (backwards-compatible).
// Otherwise, it falls back to the raw request body captured by the middleware.
// Returns "{}" if neither is available.
func marshalWebhookPayload(ctx context.Context, body *api.TriggerWebhookJSONRequestBody) (string, error) {
	// If the structured "payload" field is present, use it (backwards-compatible).
	if body != nil && body.Payload != nil {
		payloadBytes, err := json.Marshal(*body.Payload)
		if err != nil {
			return "", err
		}
		return string(payloadBytes), nil
	}

	// Fall back to the raw body from context (captured by middleware).
	if rawBody := rawBodyFromContext(ctx); len(rawBody) > 0 {
		if json.Valid(rawBody) {
			return string(rawBody), nil
		}
	}

	return "{}", nil
}

// marshalWebhookHeaders returns the JSON representation of the selected webhook
// request headers. Header names are matched case-insensitively and emitted as
// lowercase keys with []string values. Authorization is never forwarded.
func marshalWebhookHeaders(ctx context.Context, allowList []string) (string, error) {
	forwarded := make(map[string][]string)
	if len(allowList) == 0 {
		return "{}", nil
	}

	headers := requestHeadersFromContext(ctx)
	if len(headers) == 0 {
		return "{}", nil
	}

	for _, raw := range allowList {
		headerName := ir.NormalizeWebhookForwardHeader(raw)
		if headerName == "" || ir.IsDeniedWebhookForwardHeader(headerName) {
			continue
		}
		values := headers[headerName]
		if len(values) == 0 {
			continue
		}
		forwarded[headerName] = append([]string(nil), values...)
	}

	encoded, err := json.Marshal(forwarded)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func buildWebhookRuntimeParams(payload, headers string, extras map[string]string) string {
	parts := []string{
		fmt.Sprintf("WEBHOOK_PAYLOAD=%s", strconv.Quote(payload)),
		fmt.Sprintf("WEBHOOK_HEADERS=%s", strconv.Quote(headers)),
	}

	for _, key := range slices.Sorted(maps.Keys(extras)) {
		if value := extras[key]; value != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", key, strconv.Quote(value)))
		}
	}

	return strings.Join(parts, " ")
}
