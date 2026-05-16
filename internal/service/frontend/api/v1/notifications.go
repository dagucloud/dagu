// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/core/exec"
	notificationmodel "github.com/dagucloud/dagu/internal/notification"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	notificationservice "github.com/dagucloud/dagu/internal/service/notification"
)

var errNotificationManagementNotAvailable = &Error{
	HTTPStatus: http.StatusNotFound,
	Code:       api.ErrorCodeNotFound,
	Message:    "Notification management is not available",
}

func (a *API) GetDAGNotifications(ctx context.Context, request api.GetDAGNotificationsRequestObject) (api.GetDAGNotificationsResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}

	settings, err := a.notificationService.GetByDAGName(ctx, request.FileName)
	if err != nil {
		if errors.Is(err, notificationmodel.ErrSettingsNotFound) {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("no notification settings configured for DAG %s", request.FileName),
			}
		}
		return nil, err
	}
	return api.GetDAGNotifications200JSONResponse(toAPINotificationSettings(settings)), nil
}

func (a *API) UpdateDAGNotifications(ctx context.Context, request api.UpdateDAGNotificationsRequestObject) (api.UpdateDAGNotificationsResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, badNotificationRequest("request body is required")
	}
	if err := a.ensureDAGExists(ctx, request.FileName); err != nil {
		return nil, err
	}

	settings := notificationSettingsFromRequest(request.FileName, request.Body)
	saved, err := a.notificationService.Save(ctx, settings, getCreatorID(ctx))
	if err != nil {
		if notificationRequestError(err) {
			return nil, badNotificationRequest(err.Error())
		}
		return nil, err
	}

	a.logAudit(ctx, audit.CategoryNotification, "notification_settings_update", map[string]any{
		"dag_name":     request.FileName,
		"target_count": len(saved.Targets),
		"enabled":      saved.Enabled,
	})
	return api.UpdateDAGNotifications200JSONResponse(toAPINotificationSettings(saved)), nil
}

func (a *API) DeleteDAGNotifications(ctx context.Context, request api.DeleteDAGNotificationsRequestObject) (api.DeleteDAGNotificationsResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.notificationService.DeleteByDAGName(ctx, request.FileName); err != nil {
		if errors.Is(err, notificationmodel.ErrSettingsNotFound) {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("no notification settings configured for DAG %s", request.FileName),
			}
		}
		return nil, err
	}
	a.logAudit(ctx, audit.CategoryNotification, "notification_settings_delete", map[string]any{
		"dag_name": request.FileName,
	})
	return api.DeleteDAGNotifications204Response{}, nil
}

func (a *API) TestDAGNotifications(ctx context.Context, request api.TestDAGNotificationsRequestObject) (api.TestDAGNotificationsResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.ensureDAGExists(ctx, request.FileName); err != nil {
		return nil, err
	}

	var targetID string
	var eventType eventstore.EventType
	if request.Body != nil {
		targetID = valueOf(request.Body.TargetId)
		eventType = eventstore.EventType(valueOf(request.Body.EventType))
	}
	results, err := a.notificationService.SendTest(ctx, request.FileName, targetID, eventType)
	if err != nil {
		switch {
		case errors.Is(err, notificationmodel.ErrSettingsNotFound),
			errors.Is(err, notificationmodel.ErrTargetNotFound):
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    err.Error(),
			}
		case notificationRequestError(err):
			return nil, badNotificationRequest(err.Error())
		default:
			return nil, err
		}
	}

	a.logAudit(ctx, audit.CategoryNotification, "notification_test_send", map[string]any{
		"dag_name":   request.FileName,
		"target_id":  targetID,
		"event_type": eventType,
	})
	return api.TestDAGNotifications200JSONResponse{
		Results: toAPITestNotificationResults(results),
	}, nil
}

func (a *API) requireNotificationManagement(ctx context.Context) error {
	if a.notificationService == nil {
		return errNotificationManagementNotAvailable
	}
	return a.requireDeveloperOrAbove(ctx)
}

func (a *API) ensureDAGExists(ctx context.Context, dagName string) error {
	if _, err := a.dagStore.GetDetails(ctx, dagName); err != nil {
		if errors.Is(err, exec.ErrDAGNotFound) {
			return &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("DAG %s not found", dagName),
			}
		}
		return err
	}
	return nil
}

func notificationRequestError(err error) bool {
	return errors.Is(err, notificationmodel.ErrInvalidSettings) ||
		errors.Is(err, notificationmodel.ErrUnsupportedEvent) ||
		errors.Is(err, notificationmodel.ErrUnsupportedTarget) ||
		errors.Is(err, notificationmodel.ErrSecretStoreMissing)
}

func badNotificationRequest(message string) *Error {
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    message,
	}
}

func notificationSettingsFromRequest(dagName string, body *api.UpdateDAGNotificationsJSONRequestBody) *notificationmodel.Settings {
	settings := &notificationmodel.Settings{
		DAGName: dagName,
		Enabled: body.Enabled,
		Events:  make([]eventstore.EventType, 0, len(body.Events)),
		Targets: make([]notificationmodel.Target, 0, len(body.Targets)),
	}
	for _, event := range body.Events {
		settings.Events = append(settings.Events, eventstore.EventType(event))
	}
	for _, target := range body.Targets {
		settings.Targets = append(settings.Targets, notificationTargetFromRequest(target))
	}
	return settings
}

func notificationTargetFromRequest(input api.NotificationTargetInput) notificationmodel.Target {
	target := notificationmodel.Target{
		ID:      valueOf(input.Id),
		Name:    valueOf(input.Name),
		Type:    notificationmodel.ProviderType(input.Type),
		Enabled: input.Enabled,
	}
	if input.Email != nil {
		target.Email = &notificationmodel.EmailTarget{
			From:          valueOf(input.Email.From),
			To:            append([]string(nil), input.Email.To...),
			SubjectPrefix: valueOf(input.Email.SubjectPrefix),
			AttachLogs:    valueOf(input.Email.AttachLogs),
		}
		if input.Email.Cc != nil {
			target.Email.Cc = append([]string(nil), (*input.Email.Cc)...)
		}
		if input.Email.Bcc != nil {
			target.Email.Bcc = append([]string(nil), (*input.Email.Bcc)...)
		}
	}
	if input.Webhook != nil {
		target.Webhook = &notificationmodel.WebhookTarget{
			URL:        valueOf(input.Webhook.Url),
			HMACSecret: valueOf(input.Webhook.HmacSecret),
		}
		if input.Webhook.Headers != nil {
			target.Webhook.Headers = mapsClone(*input.Webhook.Headers)
		}
	}
	if input.Slack != nil {
		target.Slack = &notificationmodel.SlackTarget{
			WebhookURL: valueOf(input.Slack.WebhookUrl),
		}
	}
	if input.Telegram != nil {
		target.Telegram = &notificationmodel.TelegramTarget{
			BotToken: valueOf(input.Telegram.BotToken),
			ChatID:   valueOf(input.Telegram.ChatId),
		}
	}
	return target
}

func toAPINotificationSettings(settings *notificationmodel.Settings) api.DAGNotificationSettings {
	pub := notificationmodel.ToPublic(settings)
	events := make([]api.NotificationEventType, 0, len(pub.Events))
	for _, event := range pub.Events {
		events = append(events, api.NotificationEventType(event))
	}
	targets := make([]api.NotificationTarget, 0, len(pub.Targets))
	for _, target := range pub.Targets {
		targets = append(targets, toAPINotificationTarget(target))
	}
	return api.DAGNotificationSettings{
		Id:        pub.ID,
		DagName:   pub.DAGName,
		Enabled:   pub.Enabled,
		Events:    events,
		Targets:   targets,
		CreatedAt: pub.CreatedAt,
		UpdatedAt: pub.UpdatedAt,
		UpdatedBy: ptrOf(pub.UpdatedBy),
	}
}

func toAPINotificationTarget(target notificationmodel.PublicTarget) api.NotificationTarget {
	result := api.NotificationTarget{
		Id:      target.ID,
		Name:    ptrOf(target.Name),
		Type:    api.NotificationProviderType(target.Type),
		Enabled: target.Enabled,
	}
	if target.Email != nil {
		result.Email = toAPIEmailTarget(target.Email)
	}
	if target.Webhook != nil {
		result.Webhook = &api.NotificationWebhookTarget{
			UrlConfigured:        target.Webhook.URLConfigured,
			UrlPreview:           ptrOf(target.Webhook.URLPreview),
			Headers:              ptrOf(target.Webhook.Headers),
			HmacSecretConfigured: target.Webhook.HMACSecretConfigured,
		}
	}
	if target.Slack != nil {
		result.Slack = &api.NotificationSlackTarget{
			WebhookUrlConfigured: target.Slack.WebhookURLConfigured,
			WebhookUrlPreview:    ptrOf(target.Slack.WebhookURLPreview),
		}
	}
	if target.Telegram != nil {
		result.Telegram = &api.NotificationTelegramTarget{
			BotTokenConfigured: target.Telegram.BotTokenConfigured,
			BotTokenPreview:    ptrOf(target.Telegram.BotTokenPreview),
			ChatId:             ptrOf(target.Telegram.ChatID),
		}
	}
	return result
}

func toAPIEmailTarget(email *notificationmodel.EmailTarget) *api.NotificationEmailTarget {
	if email == nil {
		return nil
	}
	return &api.NotificationEmailTarget{
		From:          ptrOf(email.From),
		To:            append([]string(nil), email.To...),
		Cc:            ptrOf(append([]string(nil), email.Cc...)),
		Bcc:           ptrOf(append([]string(nil), email.Bcc...)),
		SubjectPrefix: ptrOf(email.SubjectPrefix),
		AttachLogs:    &email.AttachLogs,
	}
}

func toAPITestNotificationResults(results []notificationservice.TestResult) []api.TestDAGNotificationResult {
	out := make([]api.TestDAGNotificationResult, 0, len(results))
	for _, result := range results {
		out = append(out, api.TestDAGNotificationResult{
			TargetId:   result.TargetID,
			TargetName: result.TargetName,
			Provider:   api.NotificationProviderType(result.Provider),
			Delivered:  result.Delivered,
			Error:      ptrOf(result.Error),
		})
	}
	return out
}

func mapsClone(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
