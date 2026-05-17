// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package notification

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeValidatesTargetsAndEvents(t *testing.T) {
	t.Parallel()

	settings := &Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events: []eventstore.EventType{
			eventstore.TypeDAGRunFailed,
			eventstore.TypeDAGRunFailed,
		},
		Targets: []Target{{
			Type:    ProviderWebhook,
			Enabled: true,
			Events: []eventstore.EventType{
				eventstore.TypeDAGRunFailed,
				eventstore.TypeDAGRunFailed,
			},
			Webhook: &WebhookTarget{
				URL: "https://example.com/webhook",
			},
		}},
		CreatedAt: time.Now().UTC(),
	}

	normalized, err := Normalize(settings, "tester")
	require.NoError(t, err)
	require.Len(t, normalized.Events, 1)
	require.Len(t, normalized.Targets, 1)
	assert.NotEmpty(t, normalized.ID)
	assert.NotEmpty(t, normalized.Targets[0].ID)
	assert.Equal(t, []eventstore.EventType{eventstore.TypeDAGRunFailed}, normalized.Targets[0].Events)
	assert.True(t, IsTargetEventEnabled(normalized, normalized.Targets[0], eventstore.TypeDAGRunFailed))
	assert.False(t, IsTargetEventEnabled(normalized, normalized.Targets[0], eventstore.TypeDAGRunWaiting))
}

func TestNormalizeRejectsEmptyEvents(t *testing.T) {
	t.Parallel()

	_, err := Normalize(&Settings{
		DAGName: "daily-report",
		Targets: []Target{{
			Type:    ProviderWebhook,
			Enabled: true,
			Webhook: &WebhookTarget{URL: "https://example.com/webhook"},
		}},
	}, "tester")
	assert.ErrorIs(t, err, ErrInvalidSettings)
}

func TestPreserveSecrets(t *testing.T) {
	t.Parallel()

	next := &Settings{Targets: []Target{{
		ID:      "webhook-1",
		Type:    ProviderWebhook,
		Webhook: &WebhookTarget{},
	}}}
	existing := &Settings{Targets: []Target{{
		ID:   "webhook-1",
		Type: ProviderWebhook,
		Webhook: &WebhookTarget{
			URL:        "https://example.com/webhook",
			Headers:    map[string]string{"Authorization": "Bearer old"},
			HMACSecret: "old-secret",
		},
	}}}

	PreserveSecrets(next, existing)
	assert.Equal(t, "https://example.com/webhook", next.Targets[0].Webhook.URL)
	assert.Equal(t, "old-secret", next.Targets[0].Webhook.HMACSecret)
	assert.Equal(t, "Bearer old", next.Targets[0].Webhook.Headers["Authorization"])
}

func TestPreserveSecretsAllowsHeaderReplacementAndHMACClear(t *testing.T) {
	t.Parallel()

	next := &Settings{Targets: []Target{{
		ID:   "webhook-1",
		Type: ProviderWebhook,
		Webhook: &WebhookTarget{
			Headers:         map[string]string{"X-New": "value"},
			ClearHMACSecret: true,
		},
	}}}
	existing := &Settings{Targets: []Target{{
		ID:   "webhook-1",
		Type: ProviderWebhook,
		Webhook: &WebhookTarget{
			URL:        "https://example.com/webhook",
			Headers:    map[string]string{"Authorization": "Bearer old"},
			HMACSecret: "old-secret",
		},
	}}}

	PreserveSecrets(next, existing)
	assert.Equal(t, "https://example.com/webhook", next.Targets[0].Webhook.URL)
	assert.Empty(t, next.Targets[0].Webhook.HMACSecret)
	assert.NotContains(t, next.Targets[0].Webhook.Headers, "Authorization")
	assert.Equal(t, "value", next.Targets[0].Webhook.Headers["X-New"])
}

func TestNormalizeRequiresWebhookHTTPSUnlessExplicitlyAllowed(t *testing.T) {
	t.Parallel()

	_, err := Normalize(&Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []Target{{
			Type:    ProviderWebhook,
			Enabled: true,
			Webhook: &WebhookTarget{
				URL: "http://example.com/webhook",
			},
		}},
	}, "tester")
	assert.ErrorIs(t, err, ErrInvalidSettings)

	_, err = Normalize(&Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []Target{{
			Type:    ProviderWebhook,
			Enabled: true,
			Webhook: &WebhookTarget{
				URL:               "http://example.com/webhook",
				AllowInsecureHTTP: true,
			},
		}},
	}, "tester")
	require.NoError(t, err)
}

func TestNormalizeSettingsSupportsReusableChannelSubscriptions(t *testing.T) {
	t.Parallel()

	settings := &Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events: []eventstore.EventType{
			eventstore.TypeDAGRunFailed,
			eventstore.TypeDAGRunSucceeded,
		},
		Subscriptions: []Subscription{{
			ChannelID: "channel-1",
			Enabled:   true,
			Events: []eventstore.EventType{
				eventstore.TypeDAGRunFailed,
				eventstore.TypeDAGRunFailed,
			},
		}},
	}

	normalized, err := Normalize(settings, "tester")
	require.NoError(t, err)
	require.Len(t, normalized.Subscriptions, 1)
	assert.NotEmpty(t, normalized.Subscriptions[0].ID)
	assert.Equal(t, "channel-1", normalized.Subscriptions[0].ChannelID)
	assert.Equal(t, []eventstore.EventType{eventstore.TypeDAGRunFailed}, normalized.Subscriptions[0].Events)
	assert.True(t, IsSubscriptionEventEnabled(normalized, normalized.Subscriptions[0], eventstore.TypeDAGRunFailed))
	assert.False(t, IsSubscriptionEventEnabled(normalized, normalized.Subscriptions[0], eventstore.TypeDAGRunSucceeded))
}

func TestNormalizeSettingsRejectsDuplicateChannelSubscriptions(t *testing.T) {
	t.Parallel()

	_, err := Normalize(&Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Subscriptions: []Subscription{
			{ChannelID: "channel-1", Enabled: true},
			{ChannelID: "channel-1", Enabled: true},
		},
	}, "tester")
	assert.ErrorIs(t, err, ErrInvalidSettings)
}

func TestNormalizeChannelPreservesProviderValidation(t *testing.T) {
	t.Parallel()

	channel, err := NormalizeChannel(&Channel{
		Name:    "Ops Webhook",
		Type:    ProviderWebhook,
		Enabled: true,
		Webhook: &WebhookTarget{
			URL: "https://example.com/webhook",
		},
	}, "tester")
	require.NoError(t, err)
	assert.NotEmpty(t, channel.ID)
	assert.Equal(t, "Ops Webhook", channel.Name)

	_, err = NormalizeChannel(&Channel{
		Name:    "Internal",
		Type:    ProviderWebhook,
		Enabled: true,
		Webhook: &WebhookTarget{
			URL: "http://127.0.0.1:8080/webhook",
		},
	}, "tester")
	assert.ErrorIs(t, err, ErrInvalidSettings)
}

func TestNormalizeWorkspaceSettingsValidatesSMTP(t *testing.T) {
	t.Parallel()

	settings, err := NormalizeWorkspaceSettings(&WorkspaceSettings{
		SMTP: &SMTPConfig{
			Host:     " smtp.example.com ",
			Port:     " 587 ",
			Username: " user ",
			Password: "secret",
			From:     " Dagu <dagu@example.com> ",
		},
	}, "tester")
	require.NoError(t, err)
	require.NotNil(t, settings.SMTP)
	assert.Equal(t, "smtp.example.com", settings.SMTP.Host)
	assert.Equal(t, "587", settings.SMTP.Port)
	assert.Equal(t, "user", settings.SMTP.Username)
	assert.Equal(t, "Dagu <dagu@example.com>", settings.SMTP.From)
	assert.NotZero(t, settings.CreatedAt)
	assert.NotZero(t, settings.UpdatedAt)
	assert.Equal(t, "tester", settings.UpdatedBy)

	_, err = NormalizeWorkspaceSettings(&WorkspaceSettings{
		SMTP: &SMTPConfig{Host: "smtp.example.com", Port: "not-a-port", From: "dagu@example.com"},
	}, "tester")
	assert.ErrorIs(t, err, ErrInvalidSettings)

	_, err = NormalizeWorkspaceSettings(&WorkspaceSettings{
		SMTP: &SMTPConfig{Host: "smtp.example.com", Port: "587", From: "invalid"},
	}, "tester")
	assert.ErrorIs(t, err, ErrInvalidSettings)
}

func TestNormalizeRouteSetValidatesScopeAndRoutes(t *testing.T) {
	t.Parallel()

	routeSet, err := NormalizeRouteSet(&RouteSet{
		Scope:     RouteScopeWorkspace,
		Workspace: "ops",
		Enabled:   true,
		Routes: []Route{{
			ChannelID: "channel-1",
			Enabled:   true,
			Events:    []eventstore.EventType{eventstore.TypeDAGRunFailed},
		}},
	}, "tester")
	require.NoError(t, err)
	assert.NotEmpty(t, routeSet.ID)
	assert.Equal(t, RouteScopeWorkspace, routeSet.Scope)
	assert.Equal(t, "ops", routeSet.Workspace)
	require.Len(t, routeSet.Routes, 1)
	assert.NotEmpty(t, routeSet.Routes[0].ID)
	assert.Equal(t, []eventstore.EventType{eventstore.TypeDAGRunFailed}, routeSet.Routes[0].Events)

	_, err = NormalizeRouteSet(&RouteSet{
		Scope:   RouteScopeGlobal,
		Enabled: true,
		Routes: []Route{
			{ChannelID: "channel-1", Enabled: true},
			{ChannelID: "channel-1", Enabled: true},
		},
	}, "tester")
	assert.ErrorIs(t, err, ErrInvalidSettings)

	_, err = NormalizeRouteSet(&RouteSet{
		Scope:     RouteScopeWorkspace,
		Workspace: "global",
		Enabled:   true,
		Routes:    []Route{{ChannelID: "channel-1", Enabled: true}},
	}, "tester")
	assert.ErrorIs(t, err, ErrInvalidSettings)
}

func TestIsRouteEventEnabledDefaultsToOperationalEvents(t *testing.T) {
	t.Parallel()

	routeSet := &RouteSet{Enabled: true}
	route := Route{Enabled: true}
	assert.True(t, IsRouteEventEnabled(routeSet, route, eventstore.TypeDAGRunFailed))
	assert.True(t, IsRouteEventEnabled(routeSet, route, eventstore.TypeDAGRunWaiting))
	assert.False(t, IsRouteEventEnabled(routeSet, route, eventstore.TypeDAGRunSucceeded))

	route.Events = []eventstore.EventType{eventstore.TypeDAGRunSucceeded}
	assert.False(t, IsRouteEventEnabled(routeSet, route, eventstore.TypeDAGRunFailed))
	assert.True(t, IsRouteEventEnabled(routeSet, route, eventstore.TypeDAGRunSucceeded))
}

func TestPreserveWorkspaceSecrets(t *testing.T) {
	t.Parallel()

	next := &WorkspaceSettings{SMTP: &SMTPConfig{
		Host:     "smtp.example.com",
		Port:     "587",
		Username: "user",
		From:     "dagu@example.com",
	}}
	existing := &WorkspaceSettings{SMTP: &SMTPConfig{Password: "old-secret"}}

	PreserveWorkspaceSecrets(next, existing)
	assert.Equal(t, "old-secret", next.SMTP.Password)

	next.SMTP.ClearPassword = true
	next.SMTP.Password = ""
	PreserveWorkspaceSecrets(next, existing)
	assert.Empty(t, next.SMTP.Password)
}

func TestPreserveChannelSecrets(t *testing.T) {
	t.Parallel()

	next := &Channel{
		ID:      "channel-1",
		Name:    "Ops Webhook",
		Type:    ProviderWebhook,
		Enabled: true,
		Webhook: &WebhookTarget{},
	}
	existing := &Channel{
		ID:      "channel-1",
		Name:    "Ops Webhook",
		Type:    ProviderWebhook,
		Enabled: true,
		Webhook: &WebhookTarget{
			URL:        "https://example.com/webhook",
			Headers:    map[string]string{"Authorization": "Bearer old"},
			HMACSecret: "old-secret",
		},
	}

	PreserveChannelSecrets(next, existing)
	assert.Equal(t, "https://example.com/webhook", next.Webhook.URL)
	assert.Equal(t, "old-secret", next.Webhook.HMACSecret)
	assert.Equal(t, "Bearer old", next.Webhook.Headers["Authorization"])
}

func TestNormalizeRejectsPrivateWebhookTargetUnlessExplicitlyAllowed(t *testing.T) {
	t.Parallel()

	_, err := Normalize(&Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []Target{{
			Type:    ProviderWebhook,
			Enabled: true,
			Webhook: &WebhookTarget{
				URL:               "http://127.0.0.1:8080/webhook",
				AllowInsecureHTTP: true,
			},
		}},
	}, "tester")
	assert.ErrorIs(t, err, ErrInvalidSettings)

	_, err = Normalize(&Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []Target{{
			Type:    ProviderWebhook,
			Enabled: true,
			Webhook: &WebhookTarget{
				URL:                 "http://127.0.0.1:8080/webhook",
				AllowInsecureHTTP:   true,
				AllowPrivateNetwork: true,
			},
		}},
	}, "tester")
	require.NoError(t, err)
}
