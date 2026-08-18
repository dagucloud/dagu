// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

type settingsRecord struct {
	ID            string               `json:"id"`
	DAGName       string               `json:"dagName"`
	Enabled       bool                 `json:"enabled"`
	Events        []string             `json:"events"`
	Targets       []targetRecord       `json:"targets"`
	Subscriptions []subscriptionRecord `json:"subscriptions,omitempty"`
	CreatedAt     string               `json:"createdAt"`
	UpdatedAt     string               `json:"updatedAt"`
	UpdatedBy     string               `json:"updatedBy,omitempty"`
}

type channelRecord struct {
	ID        string                    `json:"id"`
	Name      string                    `json:"name"`
	Type      notification.ProviderType `json:"type"`
	Enabled   bool                      `json:"enabled"`
	Email     *notification.EmailTarget `json:"email,omitempty"`
	Webhook   *webhookTargetRecord      `json:"webhook,omitempty"`
	Slack     *slackTargetRecord        `json:"slack,omitempty"`
	Telegram  *telegramTargetRecord     `json:"telegram,omitempty"`
	Teams     *teamsTargetRecord        `json:"teams,omitempty"`
	CreatedAt string                    `json:"createdAt"`
	UpdatedAt string                    `json:"updatedAt"`
	UpdatedBy string                    `json:"updatedBy,omitempty"`
}

type workspaceSettingsRecord struct {
	SMTP      *smtpRecord `json:"smtp,omitempty"`
	CreatedAt string      `json:"createdAt"`
	UpdatedAt string      `json:"updatedAt"`
	UpdatedBy string      `json:"updatedBy,omitempty"`
}

type routeSetRecord struct {
	ID            string                  `json:"id"`
	Scope         notification.RouteScope `json:"scope"`
	Workspace     string                  `json:"workspace,omitempty"`
	Enabled       bool                    `json:"enabled"`
	InheritGlobal bool                    `json:"inheritGlobal"`
	Routes        []routeRecord           `json:"routes"`
	CreatedAt     string                  `json:"createdAt"`
	UpdatedAt     string                  `json:"updatedAt"`
	UpdatedBy     string                  `json:"updatedBy,omitempty"`
}

type routeRecord struct {
	ID        string   `json:"id"`
	ChannelID string   `json:"channelId"`
	Enabled   bool     `json:"enabled"`
	Events    []string `json:"events,omitempty"`
}

type smtpRecord struct {
	Host        string           `json:"host,omitempty"`
	Port        string           `json:"port,omitempty"`
	Username    string           `json:"username,omitempty"`
	PasswordEnc string           `json:"passwordEnc,omitempty"`
	OAuth       *smtpOAuthRecord `json:"oauth,omitempty"`
	From        string           `json:"from,omitempty"`
}

type smtpOAuthRecord struct {
	Provider              oauthconfig.Provider `json:"provider"`
	TenantID              string               `json:"tenantId,omitempty"`
	ClientID              string               `json:"clientId,omitempty"`
	ClientSecretEnc       string               `json:"clientSecretEnc,omitempty"`
	RefreshTokenEnc       string               `json:"refreshTokenEnc,omitempty"`
	ServiceAccountJSONEnc string               `json:"serviceAccountJsonEnc,omitempty"`
}

type subscriptionRecord struct {
	ID        string   `json:"id"`
	ChannelID string   `json:"channelId"`
	Enabled   bool     `json:"enabled"`
	Events    []string `json:"events,omitempty"`
}

type targetRecord struct {
	ID       string                    `json:"id"`
	Name     string                    `json:"name,omitempty"`
	Type     notification.ProviderType `json:"type"`
	Enabled  bool                      `json:"enabled"`
	Events   []string                  `json:"events,omitempty"`
	Email    *notification.EmailTarget `json:"email,omitempty"`
	Webhook  *webhookTargetRecord      `json:"webhook,omitempty"`
	Slack    *slackTargetRecord        `json:"slack,omitempty"`
	Telegram *telegramTargetRecord     `json:"telegram,omitempty"`
	Teams    *teamsTargetRecord        `json:"teams,omitempty"`
}

type webhookTargetRecord struct {
	URLEnc              string            `json:"urlEnc,omitempty"`
	HeadersEnc          map[string]string `json:"headersEnc,omitempty"`
	HMACSecretEnc       string            `json:"hmacSecretEnc,omitempty"`
	MessageTemplate     string            `json:"messageTemplate,omitempty"`
	BodyTemplate        string            `json:"bodyTemplate,omitempty"`
	AllowInsecureHTTP   bool              `json:"allowInsecureHttp,omitempty"`
	AllowPrivateNetwork bool              `json:"allowPrivateNetwork,omitempty"`
}

type slackTargetRecord struct {
	WebhookURLEnc   string `json:"webhookUrlEnc,omitempty"`
	MessageTemplate string `json:"messageTemplate,omitempty"`
}

type teamsTargetRecord struct {
	WebhookURLEnc   string `json:"webhookUrlEnc,omitempty"`
	MessageTemplate string `json:"messageTemplate,omitempty"`
}

type telegramTargetRecord struct {
	BotTokenEnc     string `json:"botTokenEnc,omitempty"`
	ChatID          string `json:"chatId,omitempty"`
	TopicID         string `json:"topicId,omitempty"`
	MessageTemplate string `json:"messageTemplate,omitempty"`
}

func (s *NotificationStore) settingsToRecord(settings *notification.Settings) (*settingsRecord, error) {
	events := make([]string, 0, len(settings.Events))
	for _, event := range settings.Events {
		events = append(events, string(event))
	}
	targets := make([]targetRecord, 0, len(settings.Targets))
	for _, target := range settings.Targets {
		stored, err := s.targetToRecord(target)
		if err != nil {
			return nil, err
		}
		targets = append(targets, stored)
	}
	subscriptions := make([]subscriptionRecord, 0, len(settings.Subscriptions))
	for _, subscription := range settings.Subscriptions {
		subscriptions = append(subscriptions, subscriptionRecord{
			ID:        subscription.ID,
			ChannelID: subscription.ChannelID,
			Enabled:   subscription.Enabled,
			Events:    eventStrings(subscription.Events),
		})
	}
	return &settingsRecord{
		ID:            settings.ID,
		DAGName:       settings.DAGName,
		Enabled:       settings.Enabled,
		Events:        events,
		Targets:       targets,
		Subscriptions: subscriptions,
		CreatedAt:     settings.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:     settings.UpdatedAt.Format(time.RFC3339Nano),
		UpdatedBy:     settings.UpdatedBy,
	}, nil
}

func (s *NotificationStore) channelToRecord(channel *notification.Channel) (*channelRecord, error) {
	target, err := s.targetToRecord(channel.ToTarget())
	if err != nil {
		return nil, err
	}
	return &channelRecord{
		ID:        channel.ID,
		Name:      channel.Name,
		Type:      channel.Type,
		Enabled:   channel.Enabled,
		Email:     target.Email,
		Webhook:   target.Webhook,
		Slack:     target.Slack,
		Telegram:  target.Telegram,
		Teams:     target.Teams,
		CreatedAt: channel.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: channel.UpdatedAt.Format(time.RFC3339Nano),
		UpdatedBy: channel.UpdatedBy,
	}, nil
}

func (s *NotificationStore) workspaceSettingsToRecord(
	settings *notification.WorkspaceSettings,
) (*workspaceSettingsRecord, error) {
	stored := &workspaceSettingsRecord{
		CreatedAt: settings.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: settings.UpdatedAt.Format(time.RFC3339Nano),
		UpdatedBy: settings.UpdatedBy,
	}
	if settings.SMTP == nil {
		return stored, nil
	}
	stored.SMTP = &smtpRecord{
		Host:     settings.SMTP.Host,
		Port:     settings.SMTP.Port,
		Username: settings.SMTP.Username,
		From:     settings.SMTP.From,
	}
	var err error
	stored.SMTP.PasswordEnc, err = s.encryptSecret(settings.SMTP.Password)
	if err != nil {
		return nil, err
	}
	if settings.SMTP.OAuth == nil {
		return stored, nil
	}
	stored.SMTP.OAuth = &smtpOAuthRecord{
		Provider: settings.SMTP.OAuth.Provider,
		TenantID: settings.SMTP.OAuth.TenantID,
		ClientID: settings.SMTP.OAuth.ClientID,
	}
	stored.SMTP.OAuth.ClientSecretEnc, err = s.encryptSecret(settings.SMTP.OAuth.ClientSecret)
	if err != nil {
		return nil, err
	}
	stored.SMTP.OAuth.RefreshTokenEnc, err = s.encryptSecret(settings.SMTP.OAuth.RefreshToken)
	if err != nil {
		return nil, err
	}
	stored.SMTP.OAuth.ServiceAccountJSONEnc, err = s.encryptSecret(settings.SMTP.OAuth.ServiceAccountJSON)
	if err != nil {
		return nil, err
	}
	return stored, nil
}

func routeSetToRecord(routeSet *notification.RouteSet) *routeSetRecord {
	routes := make([]routeRecord, 0, len(routeSet.Routes))
	for _, route := range routeSet.Routes {
		routes = append(routes, routeRecord{
			ID:        route.ID,
			ChannelID: route.ChannelID,
			Enabled:   route.Enabled,
			Events:    eventStrings(route.Events),
		})
	}
	return &routeSetRecord{
		ID:            routeSet.ID,
		Scope:         routeSet.Scope,
		Workspace:     routeSet.Workspace,
		Enabled:       routeSet.Enabled,
		InheritGlobal: routeSet.InheritGlobal,
		Routes:        routes,
		CreatedAt:     routeSet.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:     routeSet.UpdatedAt.Format(time.RFC3339Nano),
		UpdatedBy:     routeSet.UpdatedBy,
	}
}

func (s *NotificationStore) targetToRecord(target notification.Target) (targetRecord, error) {
	stored := targetRecord{
		ID:      target.ID,
		Name:    target.Name,
		Type:    target.Type,
		Enabled: target.Enabled,
		Events:  eventStrings(target.Events),
		Email:   target.Email,
	}
	var err error
	if target.Webhook != nil {
		stored.Webhook = &webhookTargetRecord{
			AllowInsecureHTTP:   target.Webhook.AllowInsecureHTTP,
			AllowPrivateNetwork: target.Webhook.AllowPrivateNetwork,
			MessageTemplate:     target.Webhook.MessageTemplate,
			BodyTemplate:        target.Webhook.BodyTemplate,
		}
		stored.Webhook.URLEnc, err = s.encryptSecret(target.Webhook.URL)
		if err != nil {
			return stored, err
		}
		if len(target.Webhook.Headers) > 0 {
			stored.Webhook.HeadersEnc = make(map[string]string, len(target.Webhook.Headers))
			for key, value := range target.Webhook.Headers {
				stored.Webhook.HeadersEnc[key], err = s.encryptSecret(value)
				if err != nil {
					return stored, err
				}
			}
		}
		stored.Webhook.HMACSecretEnc, err = s.encryptSecret(target.Webhook.HMACSecret)
		if err != nil {
			return stored, err
		}
	}
	if target.Slack != nil {
		stored.Slack = &slackTargetRecord{MessageTemplate: target.Slack.MessageTemplate}
		stored.Slack.WebhookURLEnc, err = s.encryptSecret(target.Slack.WebhookURL)
		if err != nil {
			return stored, err
		}
	}
	if target.Telegram != nil {
		stored.Telegram = &telegramTargetRecord{
			ChatID:          target.Telegram.ChatID,
			TopicID:         target.Telegram.TopicID,
			MessageTemplate: target.Telegram.MessageTemplate,
		}
		stored.Telegram.BotTokenEnc, err = s.encryptSecret(target.Telegram.BotToken)
		if err != nil {
			return stored, err
		}
	}
	if target.Teams != nil {
		stored.Teams = &teamsTargetRecord{MessageTemplate: target.Teams.MessageTemplate}
		stored.Teams.WebhookURLEnc, err = s.encryptSecret(target.Teams.WebhookURL)
		if err != nil {
			return stored, err
		}
	}
	return stored, nil
}

func (s *NotificationStore) settingsFromRecord(rec *persis.Record) (*notification.Settings, error) {
	var stored settingsRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("notification store: parse settings: %w", err)
	}
	targets := make([]notification.Target, 0, len(stored.Targets))
	for _, target := range stored.Targets {
		decoded, err := s.targetFromRecord(target)
		if err != nil {
			return nil, err
		}
		targets = append(targets, decoded)
	}
	subscriptions := make([]notification.Subscription, 0, len(stored.Subscriptions))
	for _, subscription := range stored.Subscriptions {
		subscriptions = append(subscriptions, notification.Subscription{
			ID:        subscription.ID,
			ChannelID: subscription.ChannelID,
			Enabled:   subscription.Enabled,
			Events:    eventTypes(subscription.Events),
		})
	}
	return &notification.Settings{
		ID:            stored.ID,
		DAGName:       stored.DAGName,
		Enabled:       stored.Enabled,
		Events:        eventTypes(stored.Events),
		Targets:       targets,
		Subscriptions: subscriptions,
		CreatedAt:     parseSettingsTime("CreatedAt", stored.CreatedAt),
		UpdatedAt:     parseSettingsTime("UpdatedAt", stored.UpdatedAt),
		UpdatedBy:     stored.UpdatedBy,
	}, nil
}

func (s *NotificationStore) channelFromRecord(rec *persis.Record) (*notification.Channel, error) {
	var stored channelRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("notification store: parse channel: %w", err)
	}
	target, err := s.targetFromRecord(targetRecord{
		ID:       stored.ID,
		Name:     stored.Name,
		Type:     stored.Type,
		Enabled:  stored.Enabled,
		Email:    stored.Email,
		Webhook:  stored.Webhook,
		Slack:    stored.Slack,
		Telegram: stored.Telegram,
		Teams:    stored.Teams,
	})
	if err != nil {
		return nil, err
	}
	createdAt := parseSettingsTime("CreatedAt", stored.CreatedAt)
	updatedAt := parseSettingsTime("UpdatedAt", stored.UpdatedAt)
	return &notification.Channel{
		ID:        stored.ID,
		Name:      stored.Name,
		Type:      stored.Type,
		Enabled:   stored.Enabled,
		Email:     target.Email,
		Webhook:   target.Webhook,
		Slack:     target.Slack,
		Telegram:  target.Telegram,
		Teams:     target.Teams,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		UpdatedBy: stored.UpdatedBy,
	}, nil
}

func (s *NotificationStore) workspaceSettingsFromRecord(rec *persis.Record) (*notification.WorkspaceSettings, error) {
	var stored workspaceSettingsRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("notification store: parse workspace settings: %w", err)
	}
	createdAt := parseSettingsTime("CreatedAt", stored.CreatedAt)
	updatedAt := parseSettingsTime("UpdatedAt", stored.UpdatedAt)
	settings := &notification.WorkspaceSettings{
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		UpdatedBy: stored.UpdatedBy,
	}
	if stored.SMTP == nil {
		return settings, nil
	}
	password, err := s.decryptSecret(stored.SMTP.PasswordEnc)
	if err != nil {
		return nil, err
	}
	settings.SMTP = &notification.SMTPConfig{
		Host:     stored.SMTP.Host,
		Port:     stored.SMTP.Port,
		Username: stored.SMTP.Username,
		Password: password,
		From:     stored.SMTP.From,
	}
	if stored.SMTP.OAuth == nil {
		return settings, nil
	}
	clientSecret, err := s.decryptSecret(stored.SMTP.OAuth.ClientSecretEnc)
	if err != nil {
		return nil, err
	}
	refreshToken, err := s.decryptSecret(stored.SMTP.OAuth.RefreshTokenEnc)
	if err != nil {
		return nil, err
	}
	serviceAccountJSON, err := s.decryptSecret(stored.SMTP.OAuth.ServiceAccountJSONEnc)
	if err != nil {
		return nil, err
	}
	settings.SMTP.OAuth = &oauthconfig.Config{
		Provider:           stored.SMTP.OAuth.Provider,
		TenantID:           stored.SMTP.OAuth.TenantID,
		ClientID:           stored.SMTP.OAuth.ClientID,
		ClientSecret:       clientSecret,
		RefreshToken:       refreshToken,
		ServiceAccountJSON: serviceAccountJSON,
	}
	return settings, nil
}

func routeSetFromRecord(rec *persis.Record) (*notification.RouteSet, error) {
	var stored routeSetRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("notification store: parse route set: %w", err)
	}
	routes := make([]notification.Route, 0, len(stored.Routes))
	for _, route := range stored.Routes {
		routes = append(routes, notification.Route{
			ID:        route.ID,
			ChannelID: route.ChannelID,
			Enabled:   route.Enabled,
			Events:    eventTypes(route.Events),
		})
	}
	createdAt := parseSettingsTime("CreatedAt", stored.CreatedAt)
	updatedAt := parseSettingsTime("UpdatedAt", stored.UpdatedAt)
	return &notification.RouteSet{
		ID:            stored.ID,
		Scope:         stored.Scope,
		Workspace:     stored.Workspace,
		Enabled:       stored.Enabled,
		InheritGlobal: stored.InheritGlobal,
		Routes:        routes,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		UpdatedBy:     stored.UpdatedBy,
	}, nil
}

func (s *NotificationStore) targetFromRecord(stored targetRecord) (notification.Target, error) {
	target := notification.Target{
		ID:      stored.ID,
		Name:    stored.Name,
		Type:    stored.Type,
		Enabled: stored.Enabled,
		Events:  eventTypes(stored.Events),
		Email:   stored.Email,
	}
	var err error
	if stored.Webhook != nil {
		target.Webhook = &notification.WebhookTarget{
			AllowInsecureHTTP:   stored.Webhook.AllowInsecureHTTP,
			AllowPrivateNetwork: stored.Webhook.AllowPrivateNetwork,
			MessageTemplate:     stored.Webhook.MessageTemplate,
			BodyTemplate:        stored.Webhook.BodyTemplate,
		}
		target.Webhook.URL, err = s.decryptSecret(stored.Webhook.URLEnc)
		if err != nil {
			return target, err
		}
		if len(stored.Webhook.HeadersEnc) > 0 {
			target.Webhook.Headers = make(map[string]string, len(stored.Webhook.HeadersEnc))
			for key, value := range stored.Webhook.HeadersEnc {
				target.Webhook.Headers[key], err = s.decryptSecret(value)
				if err != nil {
					return target, err
				}
			}
		}
		target.Webhook.HMACSecret, err = s.decryptSecret(stored.Webhook.HMACSecretEnc)
		if err != nil {
			return target, err
		}
	}
	if stored.Slack != nil {
		target.Slack = &notification.SlackTarget{MessageTemplate: stored.Slack.MessageTemplate}
		target.Slack.WebhookURL, err = s.decryptSecret(stored.Slack.WebhookURLEnc)
		if err != nil {
			return target, err
		}
	}
	if stored.Telegram != nil {
		target.Telegram = &notification.TelegramTarget{
			ChatID:          stored.Telegram.ChatID,
			TopicID:         stored.Telegram.TopicID,
			MessageTemplate: stored.Telegram.MessageTemplate,
		}
		target.Telegram.BotToken, err = s.decryptSecret(stored.Telegram.BotTokenEnc)
		if err != nil {
			return target, err
		}
	}
	if stored.Teams != nil {
		target.Teams = &notification.TeamsTarget{MessageTemplate: stored.Teams.MessageTemplate}
		target.Teams.WebhookURL, err = s.decryptSecret(stored.Teams.WebhookURLEnc)
		if err != nil {
			return target, err
		}
	}
	return target, nil
}

func eventStrings(events []eventstore.EventType) []string {
	if len(events) == 0 {
		return nil
	}
	result := make([]string, 0, len(events))
	for _, event := range events {
		result = append(result, string(event))
	}
	return result
}

func eventTypes(events []string) []eventstore.EventType {
	if len(events) == 0 {
		return nil
	}
	result := make([]eventstore.EventType, 0, len(events))
	for _, event := range events {
		result = append(result, eventstore.EventType(event))
	}
	return result
}

func parseSettingsTime(field, value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		slog.Debug("Failed to parse notification settings timestamp", "field", field, "value", value, "error", err)
	}
	return parsed
}

func (s *NotificationStore) encryptSecret(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if s.encryptor == nil {
		return "", notification.ErrSecretStoreMissing
	}
	return s.encryptor.Encrypt(value)
}

func (s *NotificationStore) decryptSecret(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if s.encryptor == nil {
		return "", notification.ErrSecretStoreMissing
	}
	return s.encryptor.Decrypt(value)
}
