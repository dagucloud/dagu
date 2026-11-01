// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/publicwebhook"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
)

// GetDAGWebhookPublic returns a webhook's public URL, the state of the relay
// connection that holds it, and its latest deliveries.
// Requires developer role or above.
func (a *API) GetDAGWebhookPublic(ctx context.Context, request api.GetDAGWebhookPublicRequestObject) (api.GetDAGWebhookPublicResponseObject, error) {
	if err := a.requirePublicWebhooks(ctx); err != nil {
		return nil, err
	}
	webhook, err := a.authService.GetWebhookByDAGName(ctx, request.FileName)
	if err == nil && webhook.Public == nil {
		err = authservice.ErrWebhookPublicNotEnabled
	}
	if err != nil {
		return nil, a.publicWebhookError(ctx, err, request.FileName, "get public webhook URL")
	}
	status, err := a.publicWebhookStore.Status(ctx)
	if err != nil {
		return nil, a.publicWebhookError(ctx, err, request.FileName, "get public webhook URL")
	}
	log, err := a.publicWebhookStore.Log(ctx, webhook.ID)
	if err != nil {
		return nil, a.publicWebhookError(ctx, err, request.FileName, "get public webhook deliveries")
	}
	return api.GetDAGWebhookPublic200JSONResponse(toWebhookPublicDetails(webhook, status, log)), nil
}

// EnableDAGWebhookPublic gives a webhook a public URL through Dagu Cloud.
// Requires admin role.
func (a *API) EnableDAGWebhookPublic(ctx context.Context, request api.EnableDAGWebhookPublicRequestObject) (api.EnableDAGWebhookPublicResponseObject, error) {
	if err := a.requirePublicWebhookAdmin(ctx); err != nil {
		return nil, err
	}
	if !a.connectedToDaguCloud() {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "connect this server to Dagu Cloud to give webhooks public URLs",
		}
	}
	if err := a.checkPublicWebhookLimit(ctx, request.FileName); err != nil {
		return nil, err
	}

	webhook, err := a.authService.EnablePublicWebhook(ctx, request.FileName)
	if err != nil {
		return nil, a.publicWebhookError(ctx, err, request.FileName, "turn on public webhook URL")
	}
	logger.Info(ctx, "Public webhook URL turned on", tag.Name(request.FileName))
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_public_enable", map[string]any{
		"dag_name":   request.FileName,
		"webhook_id": webhook.ID,
	})
	return api.EnableDAGWebhookPublic200JSONResponse(toWebhookDetails(webhook)), nil
}

// DisableDAGWebhookPublic removes a webhook's public URL and its log.
// Requires admin role.
func (a *API) DisableDAGWebhookPublic(ctx context.Context, request api.DisableDAGWebhookPublicRequestObject) (api.DisableDAGWebhookPublicResponseObject, error) {
	if err := a.requirePublicWebhookAdmin(ctx); err != nil {
		return nil, err
	}
	webhook, err := a.authService.DisablePublicWebhook(ctx, request.FileName)
	if err != nil {
		return nil, a.publicWebhookError(ctx, err, request.FileName, "turn off public webhook URL")
	}
	a.deletePublicWebhookLog(ctx, webhook)
	logger.Info(ctx, "Public webhook URL turned off", tag.Name(request.FileName))
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_public_disable", map[string]any{
		"dag_name":   request.FileName,
		"webhook_id": webhook.ID,
	})
	return api.DisableDAGWebhookPublic200JSONResponse(toWebhookDetails(webhook)), nil
}

// RegenerateDAGWebhookPublicURL gives a webhook a new public URL.
// Requires admin role.
func (a *API) RegenerateDAGWebhookPublicURL(ctx context.Context, request api.RegenerateDAGWebhookPublicURLRequestObject) (api.RegenerateDAGWebhookPublicURLResponseObject, error) {
	if err := a.requirePublicWebhookAdmin(ctx); err != nil {
		return nil, err
	}
	webhook, err := a.authService.RegeneratePublicWebhookToken(ctx, request.FileName)
	if err != nil {
		return nil, a.publicWebhookError(ctx, err, request.FileName, "replace public webhook URL")
	}
	logger.Info(ctx, "Public webhook URL replaced", tag.Name(request.FileName))
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_public_regenerate", map[string]any{
		"dag_name":   request.FileName,
		"webhook_id": webhook.ID,
	})
	return api.RegenerateDAGWebhookPublicURL200JSONResponse(toWebhookDetails(webhook)), nil
}

// ConfigureDAGWebhookPublicSender sets who may send to a webhook's public URL
// and which events start runs.
// Requires admin role.
func (a *API) ConfigureDAGWebhookPublicSender(ctx context.Context, request api.ConfigureDAGWebhookPublicSenderRequestObject) (api.ConfigureDAGWebhookPublicSenderResponseObject, error) {
	if err := a.requirePublicWebhookAdmin(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "request body is required",
		}
	}
	input := authservice.PublicWebhookSenderInput{
		Sender:         string(valueOf(request.Body.Sender)),
		Secret:         valueOf(request.Body.Secret),
		GenerateSecret: valueOf(request.Body.GenerateSecret),
		Events:         valueOf(request.Body.Events),
	}
	result, err := a.authService.ConfigurePublicWebhookSender(ctx, request.FileName, input)
	if err != nil {
		return nil, a.publicWebhookError(ctx, err, request.FileName, "configure public webhook sender")
	}
	public := result.Webhook.Public
	logger.Info(ctx, "Public webhook sender configured", tag.Name(request.FileName))
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_public_sender_configure", map[string]any{
		"dag_name":         request.FileName,
		"webhook_id":       result.Webhook.ID,
		"sender":           public.Sender,
		"events":           slices.Clone(public.Events),
		"secret_generated": result.GeneratedSecret != "",
	})
	response := api.ConfigureDAGWebhookPublicSender200JSONResponse{Webhook: toWebhookDetails(result.Webhook)}
	if result.GeneratedSecret != "" {
		response.Secret = &result.GeneratedSecret
	}
	return response, nil
}

// DismissDAGWebhookPublicMissed clears the counts of deliveries Dagu Cloud
// dropped for a webhook's public URL.
// Requires admin role.
func (a *API) DismissDAGWebhookPublicMissed(ctx context.Context, request api.DismissDAGWebhookPublicMissedRequestObject) (api.DismissDAGWebhookPublicMissedResponseObject, error) {
	if err := a.requirePublicWebhookAdmin(ctx); err != nil {
		return nil, err
	}
	webhook, err := a.authService.GetWebhookByDAGName(ctx, request.FileName)
	if err == nil && webhook.Public == nil {
		err = authservice.ErrWebhookPublicNotEnabled
	}
	if err == nil {
		err = a.publicWebhookStore.ClearMissed(ctx, webhook.ID)
	}
	if err != nil {
		return nil, a.publicWebhookError(ctx, err, request.FileName, "dismiss missed deliveries")
	}
	a.logAudit(ctx, audit.CategoryWebhook, "webhook_public_missed_dismiss", map[string]any{
		"dag_name":   request.FileName,
		"webhook_id": webhook.ID,
	})
	return api.DismissDAGWebhookPublicMissed204Response{}, nil
}

func (a *API) requirePublicWebhooks(ctx context.Context) error {
	if err := a.requireWebhookManagement(ctx); err != nil {
		return err
	}
	if a.publicWebhookStore == nil {
		return &Error{
			HTTPStatus: http.StatusNotImplemented,
			Code:       api.ErrorCodeInternalError,
			Message:    "public webhook URLs are not available on this server",
		}
	}
	return nil
}

func (a *API) requirePublicWebhookAdmin(ctx context.Context) error {
	if err := a.requirePublicWebhooks(ctx); err != nil {
		return err
	}
	return a.requireAdmin(ctx)
}

func (a *API) connectedToDaguCloud() bool {
	if a.licenseManager == nil {
		return false
	}
	_, connected := a.licenseManager.CloudCredentials()
	return connected
}

// checkPublicWebhookLimit refuses a new public URL past the plan's limit, as
// the relay last reported it. A webhook that has a URL keeps it.
func (a *API) checkPublicWebhookLimit(ctx context.Context, dagName string) error {
	status, err := a.publicWebhookStore.Status(ctx)
	if err != nil {
		return a.publicWebhookError(ctx, err, dagName, "turn on public webhook URL")
	}
	if status.MaxHooks <= 0 {
		return nil
	}
	webhooks, err := a.authService.ListWebhooks(ctx)
	if err != nil {
		return a.publicWebhookError(ctx, err, dagName, "turn on public webhook URL")
	}
	public := 0
	for _, wh := range webhooks {
		if wh.Public == nil {
			continue
		}
		if wh.DAGName == dagName {
			return nil
		}
		public++
	}
	if public < status.MaxHooks {
		return nil
	}
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    fmt.Sprintf("the plan allows %d public webhook URLs; turn one off first", status.MaxHooks),
	}
}

// webhookRestoreTimeout bounds moving a webhook back after a failed rename.
// It outlasts the wait for a record lock left by a crashed process to be
// recovered.
const webhookRestoreTimeout = time.Minute

// moveWebhookForRename moves a DAG's webhook to the DAG's new name before
// the DAG itself moves, so a rename never succeeds with the webhook left
// behind, where its local endpoint and public URL could no longer start the
// DAG. It reports whether the DAG had a webhook to move.
func (a *API) moveWebhookForRename(ctx context.Context, oldName, newName string) (bool, error) {
	if a.authService == nil || !a.authService.HasWebhookStore() {
		return false, nil
	}
	err := a.authService.RenameWebhook(ctx, oldName, newName)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, auth.ErrWebhookNotFound):
		return false, nil
	case errors.Is(err, auth.ErrWebhookAlreadyExists):
		return false, &Error{
			HTTPStatus: http.StatusConflict,
			Code:       api.ErrorCodeAlreadyExists,
			Message:    fmt.Sprintf("a webhook for DAG %s already exists; delete it before renaming", newName),
		}
	default:
		return false, fmt.Errorf("failed to move webhook: %w", err)
	}
}

// restoreWebhookAfterFailedRename moves a webhook back to its DAG's old name
// when the DAG itself could not move. It runs even if the request was
// cancelled, since the webhook would otherwise stay under the new name.
func (a *API) restoreWebhookAfterFailedRename(ctx context.Context, oldName, newName string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), webhookRestoreTimeout)
	defer cancel()
	if err := a.authService.RenameWebhook(ctx, newName, oldName); err != nil {
		logger.Error(ctx, "Failed to move webhook back after a failed rename",
			tag.Error(err),
			slog.String("old_name", oldName),
			slog.String("new_name", newName),
		)
	}
}

func (a *API) deletePublicWebhookLog(ctx context.Context, webhook *auth.Webhook) {
	if a.publicWebhookStore == nil || webhook == nil {
		return
	}
	if err := a.publicWebhookStore.DeleteLog(ctx, webhook.ID); err != nil {
		logger.Warn(ctx, "Failed to delete public webhook deliveries", tag.Name(webhook.DAGName), tag.Error(err))
	}
}

func (a *API) publicWebhookError(ctx context.Context, err error, dagName, action string) error {
	switch {
	case errors.Is(err, auth.ErrWebhookNotFound):
		return &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("no webhook configured for DAG %s", dagName),
		}
	case errors.Is(err, authservice.ErrWebhookPublicNotEnabled):
		return &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("the webhook for DAG %s has no public URL", dagName),
		}
	case errors.Is(err, authservice.ErrInvalidPublicWebhookSettings):
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	case errors.Is(err, authservice.ErrWebhookPublicNotSupported):
		return &Error{
			HTTPStatus: http.StatusNotImplemented,
			Code:       api.ErrorCodeInternalError,
			Message:    err.Error(),
		}
	}
	logger.Error(ctx, "Failed to "+action, tag.Name(dagName), tag.Error(err))
	return &Error{
		HTTPStatus: http.StatusInternalServerError,
		Code:       api.ErrorCodeInternalError,
		Message:    "failed to " + action,
	}
}

func toWebhookPublicSettings(public *auth.WebhookPublic) *api.WebhookPublicSettings {
	if public == nil {
		return nil
	}
	settings := &api.WebhookPublicSettings{
		SecretConfigured: public.SenderSecret != "",
		Events:           append([]string{}, public.Events...),
		CreatedAt:        public.CreatedAt,
	}
	if public.Sender != "" {
		settings.Sender = ptrOf(api.WebhookPublicSender(public.Sender))
	}
	return settings
}

func toWebhookPublicDetails(webhook *auth.Webhook, status publicwebhook.Status, log publicwebhook.Log) api.WebhookPublicDetails {
	details := api.WebhookPublicDetails{
		State:      publicWebhookState(webhook.DAGName, status),
		Deliveries: make([]api.WebhookPublicDelivery, 0, len(log.Deliveries)),
		Missed: api.WebhookPublicMissed{
			Expired: log.Missed.Expired,
			Refused: log.Missed.Refused,
		},
	}
	if status.URL != "" {
		details.Url = ptrOf(status.URL + "/" + webhook.Public.Token)
	}
	if status.Error != "" {
		details.Error = ptrOf(status.Error)
	}
	if status.MaxHooks > 0 {
		details.MaxHooks = ptrOf(status.MaxHooks)
	}
	if !log.Missed.Since.IsZero() {
		details.Missed.Since = ptrOf(log.Missed.Since)
	}
	for _, d := range log.Deliveries {
		delivery := api.WebhookPublicDelivery{
			Id:         d.ID,
			ReceivedAt: d.Received,
			Size:       d.Size,
			Outcome:    api.WebhookPublicDeliveryOutcome(d.Outcome),
		}
		if d.Event != "" {
			delivery.Event = ptrOf(d.Event)
		}
		if d.RunID != "" {
			delivery.DagRunId = ptrOf(d.RunID)
		}
		if d.Error != "" {
			delivery.Error = ptrOf(d.Error)
		}
		details.Deliveries = append(details.Deliveries, delivery)
	}
	return details
}

// publicWebhookState says whether the relay holds a DAG's public URL, from
// the status the scheduler that holds the connection last stored.
func publicWebhookState(dagName string, status publicwebhook.Status) api.WebhookPublicState {
	switch {
	case slices.Contains(status.LeftOut, dagName):
		return api.WebhookPublicStateOverLimit
	case status.Error != "":
		return api.WebhookPublicStateError
	case status.Connected && status.URL != "":
		return api.WebhookPublicStateActive
	default:
		return api.WebhookPublicStateConnecting
	}
}
