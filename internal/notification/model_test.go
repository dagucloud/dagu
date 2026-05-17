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
