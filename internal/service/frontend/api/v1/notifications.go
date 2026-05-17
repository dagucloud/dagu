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
	"github.com/dagucloud/dagu/internal/workspace"
)

var errNotificationManagementNotAvailable = &Error{
	HTTPStatus: http.StatusNotFound,
	Code:       api.ErrorCodeNotFound,
	Message:    "Notification management is not available",
}

func (a *API) GetNotificationSettings(ctx context.Context, _ api.GetNotificationSettingsRequestObject) (api.GetNotificationSettingsResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	settings, err := a.notificationService.GetWorkspaceSettings(ctx)
	if err != nil {
		return nil, err
	}
	return api.GetNotificationSettings200JSONResponse(toAPINotificationWorkspaceSettings(settings)), nil
}

func (a *API) UpdateNotificationSettings(ctx context.Context, request api.UpdateNotificationSettingsRequestObject) (api.UpdateNotificationSettingsResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, badNotificationRequest("request body is required")
	}
	settings := notificationWorkspaceSettingsFromRequest(*request.Body)
	saved, err := a.notificationService.SaveWorkspaceSettings(ctx, settings, getCreatorID(ctx))
	if err != nil {
		if notificationRequestError(err) {
			return nil, badNotificationRequest(err.Error())
		}
		return nil, err
	}

	a.logAudit(ctx, audit.CategoryNotification, "notification_workspace_settings_update", map[string]any{
		"smtp_configured": saved.SMTP != nil,
	})
	return api.UpdateNotificationSettings200JSONResponse(toAPINotificationWorkspaceSettings(saved)), nil
}

func (a *API) ListNotificationRoutes(ctx context.Context, _ api.ListNotificationRoutesRequestObject) (api.ListNotificationRoutesResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedReusableNotificationChannels(); err != nil {
		return nil, err
	}
	routeSets, err := a.notificationService.ListRouteSets(ctx)
	if err != nil {
		return nil, err
	}
	return api.ListNotificationRoutes200JSONResponse{
		RouteSets: toAPINotificationRouteSets(routeSets),
	}, nil
}

func (a *API) GetGlobalNotificationRoutes(ctx context.Context, _ api.GetGlobalNotificationRoutesRequestObject) (api.GetGlobalNotificationRoutesResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedReusableNotificationChannels(); err != nil {
		return nil, err
	}
	routeSet, err := a.notificationService.GetRouteSet(ctx, notificationmodel.RouteScopeGlobal, "")
	if err != nil {
		return nil, err
	}
	return api.GetGlobalNotificationRoutes200JSONResponse(toAPINotificationRouteSet(routeSet)), nil
}

func (a *API) UpdateGlobalNotificationRoutes(ctx context.Context, request api.UpdateGlobalNotificationRoutesRequestObject) (api.UpdateGlobalNotificationRoutesResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedReusableNotificationChannels(); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, badNotificationRequest("request body is required")
	}
	routeSet := notificationRouteSetFromRequest(notificationmodel.RouteScopeGlobal, "", *request.Body)
	saved, err := a.notificationService.SaveRouteSet(ctx, routeSet, getCreatorID(ctx))
	if err != nil {
		switch {
		case errors.Is(err, notificationmodel.ErrChannelNotFound):
			return nil, notificationNotFound(err.Error())
		case notificationRequestError(err):
			return nil, badNotificationRequest(err.Error())
		default:
			return nil, err
		}
	}
	a.logAudit(ctx, audit.CategoryNotification, "notification_route_set_update", map[string]any{
		"scope":  saved.Scope,
		"routes": len(saved.Routes),
	})
	return api.UpdateGlobalNotificationRoutes200JSONResponse(toAPINotificationRouteSet(saved)), nil
}

func (a *API) GetWorkspaceNotificationRoutes(ctx context.Context, request api.GetWorkspaceNotificationRoutesRequestObject) (api.GetWorkspaceNotificationRoutesResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedReusableNotificationChannels(); err != nil {
		return nil, err
	}
	workspaceName, err := a.resolveNotificationRouteWorkspace(ctx, string(request.WorkspaceName))
	if err != nil {
		return nil, err
	}
	if err := a.requireWorkspaceVisible(ctx, workspaceName); err != nil {
		return nil, err
	}
	routeSet, err := a.notificationService.GetRouteSet(ctx, notificationmodel.RouteScopeWorkspace, workspaceName)
	if err != nil {
		return nil, err
	}
	return api.GetWorkspaceNotificationRoutes200JSONResponse(toAPINotificationRouteSet(routeSet)), nil
}

func (a *API) UpdateWorkspaceNotificationRoutes(ctx context.Context, request api.UpdateWorkspaceNotificationRoutesRequestObject) (api.UpdateWorkspaceNotificationRoutesResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedReusableNotificationChannels(); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, badNotificationRequest("request body is required")
	}
	workspaceName, err := a.resolveNotificationRouteWorkspace(ctx, string(request.WorkspaceName))
	if err != nil {
		return nil, err
	}
	if err := a.requireWorkspaceConfigWrite(ctx, workspaceName); err != nil {
		return nil, err
	}
	routeSet := notificationRouteSetFromRequest(notificationmodel.RouteScopeWorkspace, workspaceName, *request.Body)
	saved, err := a.notificationService.SaveRouteSet(ctx, routeSet, getCreatorID(ctx))
	if err != nil {
		switch {
		case errors.Is(err, notificationmodel.ErrChannelNotFound):
			return nil, notificationNotFound(err.Error())
		case notificationRequestError(err):
			return nil, badNotificationRequest(err.Error())
		default:
			return nil, err
		}
	}
	a.logAudit(ctx, audit.CategoryNotification, "notification_route_set_update", map[string]any{
		"scope":     saved.Scope,
		"workspace": saved.Workspace,
		"routes":    len(saved.Routes),
	})
	return api.UpdateWorkspaceNotificationRoutes200JSONResponse(toAPINotificationRouteSet(saved)), nil
}

func (a *API) ListNotificationChannels(ctx context.Context, _ api.ListNotificationChannelsRequestObject) (api.ListNotificationChannelsResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedReusableNotificationChannels(); err != nil {
		return nil, err
	}
	channels, err := a.notificationService.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	return api.ListNotificationChannels200JSONResponse{
		Channels: toAPINotificationChannels(channels),
	}, nil
}

func (a *API) CreateNotificationChannel(ctx context.Context, request api.CreateNotificationChannelRequestObject) (api.CreateNotificationChannelResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedReusableNotificationChannels(); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, badNotificationRequest("request body is required")
	}

	channel := notificationChannelFromRequest("", *request.Body)
	saved, err := a.notificationService.SaveChannel(ctx, channel, getCreatorID(ctx))
	if err != nil {
		if notificationRequestError(err) {
			return nil, badNotificationRequest(err.Error())
		}
		return nil, err
	}

	a.logAudit(ctx, audit.CategoryNotification, "notification_channel_create", map[string]any{
		"channel_id": saved.ID,
		"provider":   saved.Type,
		"enabled":    saved.Enabled,
	})
	return api.CreateNotificationChannel201JSONResponse(toAPINotificationChannel(saved)), nil
}

func (a *API) GetNotificationChannel(ctx context.Context, request api.GetNotificationChannelRequestObject) (api.GetNotificationChannelResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedReusableNotificationChannels(); err != nil {
		return nil, err
	}
	channel, err := a.notificationService.GetChannel(ctx, request.ChannelId)
	if err != nil {
		if errors.Is(err, notificationmodel.ErrChannelNotFound) {
			return nil, notificationNotFound(err.Error())
		}
		return nil, err
	}
	return api.GetNotificationChannel200JSONResponse(toAPINotificationChannel(channel)), nil
}

func (a *API) UpdateNotificationChannel(ctx context.Context, request api.UpdateNotificationChannelRequestObject) (api.UpdateNotificationChannelResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedReusableNotificationChannels(); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, badNotificationRequest("request body is required")
	}
	if _, err := a.notificationService.GetChannel(ctx, request.ChannelId); err != nil {
		if errors.Is(err, notificationmodel.ErrChannelNotFound) {
			return nil, notificationNotFound(err.Error())
		}
		return nil, err
	}

	channel := notificationChannelFromRequest(request.ChannelId, *request.Body)
	saved, err := a.notificationService.SaveChannel(ctx, channel, getCreatorID(ctx))
	if err != nil {
		if notificationRequestError(err) {
			return nil, badNotificationRequest(err.Error())
		}
		return nil, err
	}

	a.logAudit(ctx, audit.CategoryNotification, "notification_channel_update", map[string]any{
		"channel_id": saved.ID,
		"provider":   saved.Type,
		"enabled":    saved.Enabled,
	})
	return api.UpdateNotificationChannel200JSONResponse(toAPINotificationChannel(saved)), nil
}

func (a *API) DeleteNotificationChannel(ctx context.Context, request api.DeleteNotificationChannelRequestObject) (api.DeleteNotificationChannelResponseObject, error) {
	if err := a.requireNotificationManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.requireLicensedReusableNotificationChannels(); err != nil {
		return nil, err
	}
	if err := a.notificationService.DeleteChannel(ctx, request.ChannelId); err != nil {
		switch {
		case errors.Is(err, notificationmodel.ErrChannelNotFound):
			return nil, notificationNotFound(err.Error())
		case errors.Is(err, notificationmodel.ErrChannelInUse):
			return nil, &Error{
				HTTPStatus: http.StatusConflict,
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
			}
		default:
			return nil, err
		}
	}
	a.logAudit(ctx, audit.CategoryNotification, "notification_channel_delete", map[string]any{
		"channel_id": request.ChannelId,
	})
	return api.DeleteNotificationChannel204Response{}, nil
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
	if request.Body.Subscriptions != nil {
		if err := a.requireLicensedReusableNotificationChannels(); err != nil {
			return nil, err
		}
	}

	settings := notificationSettingsFromRequest(request.FileName, request.Body)
	if request.Body.Subscriptions == nil {
		if existing, err := a.notificationService.GetByDAGName(ctx, request.FileName); err == nil {
			settings.Subscriptions = existing.Subscriptions
		} else if !errors.Is(err, notificationmodel.ErrSettingsNotFound) {
			return nil, err
		}
	}
	saved, err := a.notificationService.Save(ctx, settings, getCreatorID(ctx))
	if err != nil {
		switch {
		case errors.Is(err, notificationmodel.ErrChannelNotFound):
			return nil, notificationNotFound(err.Error())
		case notificationRequestError(err):
			return nil, badNotificationRequest(err.Error())
		default:
			return nil, err
		}
	}

	a.logAudit(ctx, audit.CategoryNotification, "notification_settings_update", map[string]any{
		"dag_name":           request.FileName,
		"target_count":       len(saved.Targets),
		"subscription_count": len(saved.Subscriptions),
		"enabled":            saved.Enabled,
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

func (a *API) resolveNotificationRouteWorkspace(ctx context.Context, name string) (string, error) {
	if a.workspaceStore == nil {
		return "", workspaceStoreUnavailable()
	}
	workspaceName, err := validateWorkspaceParam(name)
	if err != nil {
		return "", err
	}
	if workspaceName == "" {
		return "", badWorkspaceError("workspace name is required")
	}
	ws, err := a.workspaceStore.GetByName(ctx, workspaceName)
	if err != nil {
		if errors.Is(err, workspace.ErrWorkspaceNotFound) {
			return "", workspaceResourceNotFound()
		}
		return "", err
	}
	return ws.Name, nil
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

func notificationNotFound(message string) *Error {
	return &Error{
		HTTPStatus: http.StatusNotFound,
		Code:       api.ErrorCodeNotFound,
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
	if body.Subscriptions != nil {
		settings.Subscriptions = make([]notificationmodel.Subscription, 0, len(*body.Subscriptions))
		for _, subscription := range *body.Subscriptions {
			settings.Subscriptions = append(settings.Subscriptions, notificationSubscriptionFromRequest(subscription))
		}
	}
	return settings
}

func notificationSubscriptionFromRequest(input api.NotificationSubscriptionInput) notificationmodel.Subscription {
	subscription := notificationmodel.Subscription{
		ID:        valueOf(input.Id),
		ChannelID: input.ChannelId,
		Enabled:   input.Enabled,
	}
	if input.Events != nil {
		subscription.Events = make([]eventstore.EventType, 0, len(*input.Events))
		for _, event := range *input.Events {
			subscription.Events = append(subscription.Events, eventstore.EventType(event))
		}
	}
	return subscription
}

func notificationWorkspaceSettingsFromRequest(input api.NotificationWorkspaceSettingsInput) *notificationmodel.WorkspaceSettings {
	settings := &notificationmodel.WorkspaceSettings{}
	if input.Smtp != nil {
		settings.SMTP = &notificationmodel.SMTPConfig{
			Host:          valueOf(input.Smtp.Host),
			Port:          valueOf(input.Smtp.Port),
			Username:      valueOf(input.Smtp.Username),
			Password:      valueOf(input.Smtp.Password),
			From:          valueOf(input.Smtp.From),
			ClearPassword: valueOf(input.Smtp.ClearPassword),
		}
	}
	return settings
}

func notificationRouteSetFromRequest(scope notificationmodel.RouteScope, workspace string, input api.NotificationRouteSetInput) *notificationmodel.RouteSet {
	routeSet := &notificationmodel.RouteSet{
		Scope:         scope,
		Workspace:     workspace,
		Enabled:       input.Enabled,
		InheritGlobal: input.InheritGlobal,
		Routes:        make([]notificationmodel.Route, 0, len(input.Routes)),
	}
	for _, route := range input.Routes {
		routeSet.Routes = append(routeSet.Routes, notificationRouteFromRequest(route))
	}
	return routeSet
}

func notificationRouteFromRequest(input api.NotificationRouteInput) notificationmodel.Route {
	route := notificationmodel.Route{
		ID:        valueOf(input.Id),
		ChannelID: input.ChannelId,
		Enabled:   input.Enabled,
	}
	if input.Events != nil {
		route.Events = make([]eventstore.EventType, 0, len(*input.Events))
		for _, event := range *input.Events {
			route.Events = append(route.Events, eventstore.EventType(event))
		}
	}
	return route
}

func notificationChannelFromRequest(id string, input api.NotificationChannelInput) *notificationmodel.Channel {
	channel := &notificationmodel.Channel{
		ID:      id,
		Name:    input.Name,
		Type:    notificationmodel.ProviderType(input.Type),
		Enabled: input.Enabled,
	}
	if input.Email != nil {
		channel.Email = &notificationmodel.EmailTarget{
			From:          valueOf(input.Email.From),
			To:            append([]string(nil), input.Email.To...),
			SubjectPrefix: valueOf(input.Email.SubjectPrefix),
			AttachLogs:    valueOf(input.Email.AttachLogs),
		}
		if input.Email.Cc != nil {
			channel.Email.Cc = append([]string(nil), (*input.Email.Cc)...)
		}
		if input.Email.Bcc != nil {
			channel.Email.Bcc = append([]string(nil), (*input.Email.Bcc)...)
		}
	}
	if input.Webhook != nil {
		channel.Webhook = &notificationmodel.WebhookTarget{
			URL:                 valueOf(input.Webhook.Url),
			HMACSecret:          valueOf(input.Webhook.HmacSecret),
			AllowInsecureHTTP:   valueOf(input.Webhook.AllowInsecureHttp),
			AllowPrivateNetwork: valueOf(input.Webhook.AllowPrivateNetwork),
			ClearHeaders:        valueOf(input.Webhook.ClearHeaders),
			ClearHMACSecret:     valueOf(input.Webhook.ClearHmacSecret),
		}
		if input.Webhook.Headers != nil {
			channel.Webhook.Headers = mapsClonePreserveEmpty(*input.Webhook.Headers)
		}
	}
	if input.Slack != nil {
		channel.Slack = &notificationmodel.SlackTarget{
			WebhookURL: valueOf(input.Slack.WebhookUrl),
		}
	}
	if input.Telegram != nil {
		channel.Telegram = &notificationmodel.TelegramTarget{
			BotToken: valueOf(input.Telegram.BotToken),
			ChatID:   valueOf(input.Telegram.ChatId),
		}
	}
	return channel
}

func notificationTargetFromRequest(input api.NotificationTargetInput) notificationmodel.Target {
	target := notificationmodel.Target{
		ID:      valueOf(input.Id),
		Name:    valueOf(input.Name),
		Type:    notificationmodel.ProviderType(input.Type),
		Enabled: input.Enabled,
	}
	if input.Events != nil {
		target.Events = make([]eventstore.EventType, 0, len(*input.Events))
		for _, event := range *input.Events {
			target.Events = append(target.Events, eventstore.EventType(event))
		}
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
			URL:                 valueOf(input.Webhook.Url),
			HMACSecret:          valueOf(input.Webhook.HmacSecret),
			AllowInsecureHTTP:   valueOf(input.Webhook.AllowInsecureHttp),
			AllowPrivateNetwork: valueOf(input.Webhook.AllowPrivateNetwork),
			ClearHeaders:        valueOf(input.Webhook.ClearHeaders),
			ClearHMACSecret:     valueOf(input.Webhook.ClearHmacSecret),
		}
		if input.Webhook.Headers != nil {
			target.Webhook.Headers = mapsClonePreserveEmpty(*input.Webhook.Headers)
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
	subscriptions := make([]api.NotificationSubscription, 0, len(pub.Subscriptions))
	for _, subscription := range pub.Subscriptions {
		subscriptions = append(subscriptions, toAPINotificationSubscription(subscription))
	}
	return api.DAGNotificationSettings{
		Id:            pub.ID,
		DagName:       pub.DAGName,
		Enabled:       pub.Enabled,
		Events:        events,
		Targets:       targets,
		Subscriptions: subscriptions,
		CreatedAt:     pub.CreatedAt,
		UpdatedAt:     pub.UpdatedAt,
		UpdatedBy:     ptrOf(pub.UpdatedBy),
	}
}

func toAPINotificationWorkspaceSettings(settings *notificationmodel.WorkspaceSettings) api.NotificationWorkspaceSettings {
	if settings == nil {
		return api.NotificationWorkspaceSettings{}
	}
	pub := settings.ToPublic()
	result := api.NotificationWorkspaceSettings{}
	if !pub.CreatedAt.IsZero() {
		result.CreatedAt = ptrOf(pub.CreatedAt)
	}
	if !pub.UpdatedAt.IsZero() {
		result.UpdatedAt = ptrOf(pub.UpdatedAt)
	}
	if pub.UpdatedBy != "" {
		result.UpdatedBy = ptrOf(pub.UpdatedBy)
	}
	if pub.SMTP != nil {
		result.Smtp = &api.NotificationSMTPSettings{
			Host:               ptrOf(pub.SMTP.Host),
			Port:               ptrOf(pub.SMTP.Port),
			Username:           ptrOf(pub.SMTP.Username),
			From:               ptrOf(pub.SMTP.From),
			PasswordConfigured: pub.SMTP.PasswordConfigured,
		}
	}
	return result
}

func toAPINotificationRouteSets(routeSets []*notificationmodel.RouteSet) []api.NotificationRouteSet {
	out := make([]api.NotificationRouteSet, 0, len(routeSets))
	for _, routeSet := range routeSets {
		out = append(out, toAPINotificationRouteSet(routeSet))
	}
	return out
}

func toAPINotificationRouteSet(routeSet *notificationmodel.RouteSet) api.NotificationRouteSet {
	if routeSet == nil {
		return api.NotificationRouteSet{
			Scope:         api.NotificationRouteScopeGlobal,
			Enabled:       true,
			InheritGlobal: true,
			Routes:        []api.NotificationRoute{},
		}
	}
	pub := routeSet.ToPublic()
	result := api.NotificationRouteSet{
		Id:            ptrOf(pub.ID),
		Scope:         api.NotificationRouteScope(pub.Scope),
		Enabled:       pub.Enabled,
		InheritGlobal: pub.InheritGlobal,
		Routes:        make([]api.NotificationRoute, 0, len(pub.Routes)),
	}
	if pub.Workspace != "" {
		result.Workspace = ptrOf(pub.Workspace)
	}
	if !pub.CreatedAt.IsZero() {
		result.CreatedAt = ptrOf(pub.CreatedAt)
	}
	if !pub.UpdatedAt.IsZero() {
		result.UpdatedAt = ptrOf(pub.UpdatedAt)
	}
	if pub.UpdatedBy != "" {
		result.UpdatedBy = ptrOf(pub.UpdatedBy)
	}
	for _, route := range pub.Routes {
		result.Routes = append(result.Routes, toAPINotificationRoute(route))
	}
	return result
}

func toAPINotificationRoute(route notificationmodel.PublicRoute) api.NotificationRoute {
	result := api.NotificationRoute{
		Id:        route.ID,
		ChannelId: route.ChannelID,
		Enabled:   route.Enabled,
	}
	if len(route.Events) > 0 {
		events := make([]api.NotificationEventType, 0, len(route.Events))
		for _, event := range route.Events {
			events = append(events, api.NotificationEventType(event))
		}
		result.Events = &events
	}
	return result
}

func toAPINotificationChannels(channels []*notificationmodel.Channel) []api.NotificationChannel {
	out := make([]api.NotificationChannel, 0, len(channels))
	for _, channel := range channels {
		out = append(out, toAPINotificationChannel(channel))
	}
	return out
}

func toAPINotificationChannel(channel *notificationmodel.Channel) api.NotificationChannel {
	pub := channel.ToPublic()
	result := api.NotificationChannel{
		Id:        pub.ID,
		Name:      pub.Name,
		Type:      api.NotificationProviderType(pub.Type),
		Enabled:   pub.Enabled,
		CreatedAt: pub.CreatedAt,
		UpdatedAt: pub.UpdatedAt,
		UpdatedBy: ptrOf(pub.UpdatedBy),
	}
	if pub.Email != nil {
		result.Email = toAPIEmailTarget(pub.Email)
	}
	if pub.Webhook != nil {
		result.Webhook = &api.NotificationWebhookTarget{
			UrlConfigured:        pub.Webhook.URLConfigured,
			UrlPreview:           ptrOf(pub.Webhook.URLPreview),
			Headers:              ptrOf(pub.Webhook.Headers),
			HmacSecretConfigured: pub.Webhook.HMACSecretConfigured,
			AllowInsecureHttp:    ptrOf(pub.Webhook.AllowInsecureHTTP),
			AllowPrivateNetwork:  ptrOf(pub.Webhook.AllowPrivateNetwork),
		}
	}
	if pub.Slack != nil {
		result.Slack = &api.NotificationSlackTarget{
			WebhookUrlConfigured: pub.Slack.WebhookURLConfigured,
			WebhookUrlPreview:    ptrOf(pub.Slack.WebhookURLPreview),
		}
	}
	if pub.Telegram != nil {
		result.Telegram = &api.NotificationTelegramTarget{
			BotTokenConfigured: pub.Telegram.BotTokenConfigured,
			BotTokenPreview:    ptrOf(pub.Telegram.BotTokenPreview),
			ChatId:             ptrOf(pub.Telegram.ChatID),
		}
	}
	return result
}

func toAPINotificationSubscription(subscription notificationmodel.PublicSubscription) api.NotificationSubscription {
	result := api.NotificationSubscription{
		Id:        subscription.ID,
		ChannelId: subscription.ChannelID,
		Enabled:   subscription.Enabled,
	}
	if len(subscription.Events) > 0 {
		events := make([]api.NotificationEventType, 0, len(subscription.Events))
		for _, event := range subscription.Events {
			events = append(events, api.NotificationEventType(event))
		}
		result.Events = &events
	}
	return result
}

func toAPINotificationTarget(target notificationmodel.PublicTarget) api.NotificationTarget {
	result := api.NotificationTarget{
		Id:      target.ID,
		Name:    ptrOf(target.Name),
		Type:    api.NotificationProviderType(target.Type),
		Enabled: target.Enabled,
	}
	if len(target.Events) > 0 {
		events := make([]api.NotificationEventType, 0, len(target.Events))
		for _, event := range target.Events {
			events = append(events, api.NotificationEventType(event))
		}
		result.Events = &events
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
			AllowInsecureHttp:    ptrOf(target.Webhook.AllowInsecureHTTP),
			AllowPrivateNetwork:  ptrOf(target.Webhook.AllowPrivateNetwork),
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
	return mapsClonePreserveEmpty(in)
}

func mapsClonePreserveEmpty(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
