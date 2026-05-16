// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package notification

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/internal/service/eventstore"
)

type ProviderType string

const (
	ProviderEmail    ProviderType = "email"
	ProviderWebhook  ProviderType = "webhook"
	ProviderSlack    ProviderType = "slack"
	ProviderTelegram ProviderType = "telegram"
)

var defaultEvents = []eventstore.EventType{
	eventstore.TypeDAGRunFailed,
	eventstore.TypeDAGRunWaiting,
	eventstore.TypeDAGRunAborted,
	eventstore.TypeDAGRunRejected,
}

var supportedEvents = []eventstore.EventType{
	eventstore.TypeDAGRunWaiting,
	eventstore.TypeDAGRunSucceeded,
	eventstore.TypeDAGRunFailed,
	eventstore.TypeDAGRunAborted,
	eventstore.TypeDAGRunRejected,
}

var (
	ErrSettingsNotFound   = errors.New("notification settings not found")
	ErrInvalidSettings    = errors.New("invalid notification settings")
	ErrTargetNotFound     = errors.New("notification target not found")
	ErrUnsupportedEvent   = errors.New("unsupported notification event")
	ErrUnsupportedTarget  = errors.New("unsupported notification target provider")
	ErrSecretStoreMissing = errors.New("notification secret store is not configured")
)

type Settings struct {
	ID        string                 `json:"id"`
	DAGName   string                 `json:"dagName"`
	Enabled   bool                   `json:"enabled"`
	Events    []eventstore.EventType `json:"events"`
	Targets   []Target               `json:"targets"`
	CreatedAt time.Time              `json:"createdAt"`
	UpdatedAt time.Time              `json:"updatedAt"`
	UpdatedBy string                 `json:"updatedBy,omitempty"`
}

type Target struct {
	ID      string       `json:"id"`
	Name    string       `json:"name,omitempty"`
	Type    ProviderType `json:"type"`
	Enabled bool         `json:"enabled"`

	Email    *EmailTarget    `json:"email,omitempty"`
	Webhook  *WebhookTarget  `json:"webhook,omitempty"`
	Slack    *SlackTarget    `json:"slack,omitempty"`
	Telegram *TelegramTarget `json:"telegram,omitempty"`
}

type EmailTarget struct {
	From          string   `json:"from,omitempty"`
	To            []string `json:"to,omitempty"`
	Cc            []string `json:"cc,omitempty"`
	Bcc           []string `json:"bcc,omitempty"`
	SubjectPrefix string   `json:"subjectPrefix,omitempty"`
	AttachLogs    bool     `json:"attachLogs,omitempty"`
}

type WebhookTarget struct {
	URL        string            `json:"url,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	HMACSecret string            `json:"hmacSecret,omitempty"`
}

type SlackTarget struct {
	WebhookURL string `json:"webhookUrl,omitempty"`
}

type TelegramTarget struct {
	BotToken string `json:"botToken,omitempty"`
	ChatID   string `json:"chatId,omitempty"`
}

type PublicSettings struct {
	ID        string         `json:"id"`
	DAGName   string         `json:"dagName"`
	Enabled   bool           `json:"enabled"`
	Events    []string       `json:"events"`
	Targets   []PublicTarget `json:"targets"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	UpdatedBy string         `json:"updatedBy,omitempty"`
}

type PublicTarget struct {
	ID      string       `json:"id"`
	Name    string       `json:"name,omitempty"`
	Type    ProviderType `json:"type"`
	Enabled bool         `json:"enabled"`

	Email    *EmailTarget          `json:"email,omitempty"`
	Webhook  *PublicWebhookTarget  `json:"webhook,omitempty"`
	Slack    *PublicSlackTarget    `json:"slack,omitempty"`
	Telegram *PublicTelegramTarget `json:"telegram,omitempty"`
}

type PublicWebhookTarget struct {
	URLConfigured        bool              `json:"urlConfigured"`
	URLPreview           string            `json:"urlPreview,omitempty"`
	Headers              map[string]string `json:"headers,omitempty"`
	HMACSecretConfigured bool              `json:"hmacSecretConfigured"`
}

type PublicSlackTarget struct {
	WebhookURLConfigured bool   `json:"webhookUrlConfigured"`
	WebhookURLPreview    string `json:"webhookUrlPreview,omitempty"`
}

type PublicTelegramTarget struct {
	BotTokenConfigured bool   `json:"botTokenConfigured"`
	BotTokenPreview    string `json:"botTokenPreview,omitempty"`
	ChatID             string `json:"chatId,omitempty"`
}

type Store interface {
	Save(ctx context.Context, settings *Settings) error
	GetByDAGName(ctx context.Context, dagName string) (*Settings, error)
	List(ctx context.Context) ([]*Settings, error)
	DeleteByDAGName(ctx context.Context, dagName string) error
}

func NewSettings(dagName, updatedBy string) (*Settings, error) {
	if strings.TrimSpace(dagName) == "" {
		return nil, fmt.Errorf("%w: dagName is required", ErrInvalidSettings)
	}
	now := time.Now().UTC()
	return &Settings{
		ID:        uuid.New().String(),
		DAGName:   dagName,
		Enabled:   true,
		Events:    append([]eventstore.EventType(nil), defaultEvents...),
		CreatedAt: now,
		UpdatedAt: now,
		UpdatedBy: updatedBy,
	}, nil
}

func Normalize(settings *Settings, updatedBy string) (*Settings, error) {
	if settings == nil {
		return nil, fmt.Errorf("%w: settings is nil", ErrInvalidSettings)
	}
	settings.DAGName = strings.TrimSpace(settings.DAGName)
	if settings.DAGName == "" {
		return nil, fmt.Errorf("%w: dagName is required", ErrInvalidSettings)
	}
	if settings.ID == "" {
		settings.ID = uuid.New().String()
	}
	events := make([]eventstore.EventType, 0, len(settings.Events))
	for _, event := range settings.Events {
		if event == "" {
			continue
		}
		if !slices.Contains(supportedEvents, event) {
			return nil, fmt.Errorf("%w: %s", ErrUnsupportedEvent, event)
		}
		if !slices.Contains(events, event) {
			events = append(events, event)
		}
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("%w: at least one notification event is required", ErrInvalidSettings)
	}
	settings.Events = events

	for i := range settings.Targets {
		if err := normalizeTarget(&settings.Targets[i]); err != nil {
			return nil, err
		}
	}

	now := time.Now().UTC()
	if settings.CreatedAt.IsZero() {
		settings.CreatedAt = now
	}
	settings.UpdatedAt = now
	settings.UpdatedBy = updatedBy
	return settings, nil
}

func normalizeTarget(target *Target) error {
	target.ID = strings.TrimSpace(target.ID)
	if target.ID == "" {
		target.ID = uuid.New().String()
	}
	target.Name = strings.TrimSpace(target.Name)
	switch target.Type {
	case ProviderEmail:
		if target.Email == nil {
			return fmt.Errorf("%w: email target config is required", ErrInvalidSettings)
		}
		if err := validateEmails(target.Email.To); err != nil {
			return err
		}
		if err := validateEmails(target.Email.Cc); err != nil {
			return err
		}
		if err := validateEmails(target.Email.Bcc); err != nil {
			return err
		}
		if len(target.Email.To) == 0 {
			return fmt.Errorf("%w: email target requires at least one recipient", ErrInvalidSettings)
		}
		if target.Email.From != "" {
			if _, err := mail.ParseAddress(target.Email.From); err != nil {
				return fmt.Errorf("%w: invalid from address: %w", ErrInvalidSettings, err)
			}
		}
	case ProviderWebhook:
		if target.Webhook == nil {
			return fmt.Errorf("%w: webhook target config is required", ErrInvalidSettings)
		}
		if target.Webhook.URL == "" {
			return fmt.Errorf("%w: webhook target requires url", ErrInvalidSettings)
		}
		if err := validateAbsoluteURL(target.Webhook.URL); err != nil {
			return err
		}
	case ProviderSlack:
		if target.Slack == nil {
			return fmt.Errorf("%w: slack target config is required", ErrInvalidSettings)
		}
		if target.Slack.WebhookURL == "" {
			return fmt.Errorf("%w: slack target requires webhookUrl", ErrInvalidSettings)
		}
		if err := validateAbsoluteURL(target.Slack.WebhookURL); err != nil {
			return err
		}
	case ProviderTelegram:
		if target.Telegram == nil {
			return fmt.Errorf("%w: telegram target config is required", ErrInvalidSettings)
		}
		target.Telegram.BotToken = strings.TrimSpace(target.Telegram.BotToken)
		if target.Telegram.BotToken == "" {
			return fmt.Errorf("%w: telegram target requires botToken", ErrInvalidSettings)
		}
		target.Telegram.ChatID = strings.TrimSpace(target.Telegram.ChatID)
		if target.Telegram.ChatID == "" {
			return fmt.Errorf("%w: telegram target requires chatId", ErrInvalidSettings)
		}
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedTarget, target.Type)
	}
	return nil
}

func validateEmails(values []string) error {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, err := mail.ParseAddress(value); err != nil {
			return fmt.Errorf("%w: invalid email address %q: %w", ErrInvalidSettings, value, err)
		}
	}
	return nil
}

func validateAbsoluteURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%w: invalid target url", ErrInvalidSettings)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("%w: target url must use http or https", ErrInvalidSettings)
	}
	return nil
}

func IsEventEnabled(settings *Settings, event eventstore.EventType) bool {
	if settings == nil || !settings.Enabled {
		return false
	}
	return slices.Contains(settings.Events, event)
}

func ToPublic(settings *Settings) *PublicSettings {
	if settings == nil {
		return nil
	}
	events := make([]string, 0, len(settings.Events))
	for _, event := range settings.Events {
		events = append(events, string(event))
	}
	targets := make([]PublicTarget, 0, len(settings.Targets))
	for _, target := range settings.Targets {
		targets = append(targets, target.ToPublic())
	}
	return &PublicSettings{
		ID:        settings.ID,
		DAGName:   settings.DAGName,
		Enabled:   settings.Enabled,
		Events:    events,
		Targets:   targets,
		CreatedAt: settings.CreatedAt,
		UpdatedAt: settings.UpdatedAt,
		UpdatedBy: settings.UpdatedBy,
	}
}

func (t Target) ToPublic() PublicTarget {
	pub := PublicTarget{
		ID:      t.ID,
		Name:    t.Name,
		Type:    t.Type,
		Enabled: t.Enabled,
	}
	switch t.Type {
	case ProviderEmail:
		if t.Email != nil {
			copy := *t.Email
			pub.Email = &copy
		}
	case ProviderWebhook:
		if t.Webhook != nil {
			pub.Webhook = &PublicWebhookTarget{
				URLConfigured:        t.Webhook.URL != "",
				URLPreview:           PreviewSecret(t.Webhook.URL),
				Headers:              previewHeaderValues(t.Webhook.Headers),
				HMACSecretConfigured: t.Webhook.HMACSecret != "",
			}
		}
	case ProviderSlack:
		if t.Slack != nil {
			pub.Slack = &PublicSlackTarget{
				WebhookURLConfigured: t.Slack.WebhookURL != "",
				WebhookURLPreview:    PreviewSecret(t.Slack.WebhookURL),
			}
		}
	case ProviderTelegram:
		if t.Telegram != nil {
			pub.Telegram = &PublicTelegramTarget{
				BotTokenConfigured: t.Telegram.BotToken != "",
				BotTokenPreview:    PreviewSecret(t.Telegram.BotToken),
				ChatID:             t.Telegram.ChatID,
			}
		}
	}
	return pub
}

func PreviewSecret(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "********"
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func previewHeaderValues(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	result := make(map[string]string, len(headers))
	for key, value := range headers {
		result[key] = PreviewSecret(value)
	}
	return result
}

func PreserveSecrets(next, existing *Settings) {
	if next == nil || existing == nil {
		return
	}
	existingByID := make(map[string]Target, len(existing.Targets))
	for _, target := range existing.Targets {
		existingByID[target.ID] = target
	}
	for i := range next.Targets {
		prev, ok := existingByID[next.Targets[i].ID]
		if !ok {
			continue
		}
		preserveTargetSecrets(&next.Targets[i], prev)
	}
}

func preserveTargetSecrets(next *Target, prev Target) {
	if next.Webhook != nil && prev.Webhook != nil {
		if next.Webhook.URL == "" {
			next.Webhook.URL = prev.Webhook.URL
		}
		if next.Webhook.HMACSecret == "" {
			next.Webhook.HMACSecret = prev.Webhook.HMACSecret
		}
		for key, oldValue := range prev.Webhook.Headers {
			if next.Webhook.Headers == nil {
				next.Webhook.Headers = make(map[string]string)
			}
			if next.Webhook.Headers[key] == "" {
				next.Webhook.Headers[key] = oldValue
			}
		}
	}
	if next.Slack != nil && prev.Slack != nil && next.Slack.WebhookURL == "" {
		next.Slack.WebhookURL = prev.Slack.WebhookURL
	}
	if next.Telegram != nil && prev.Telegram != nil && next.Telegram.BotToken == "" {
		next.Telegram.BotToken = prev.Telegram.BotToken
	}
}
