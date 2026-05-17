// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filenotification

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/notification"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_EncryptsNotificationSecretsAtRest(t *testing.T) {
	t.Parallel()

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	store, err := New(t.TempDir(), WithEncryptor(enc))
	require.NoError(t, err)

	settings := &notification.Settings{
		ID:      "settings-1",
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notification.Target{
			{
				ID:      "webhook-1",
				Type:    notification.ProviderWebhook,
				Enabled: true,
				Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
				Webhook: &notification.WebhookTarget{
					URL:                 "https://example.com/webhook",
					Headers:             map[string]string{"Authorization": "Bearer secret-token"},
					HMACSecret:          "hmac-secret",
					AllowPrivateNetwork: true,
				},
			},
			{
				ID:      "slack-1",
				Type:    notification.ProviderSlack,
				Enabled: true,
				Slack:   &notification.SlackTarget{WebhookURL: "https://hooks.slack.com/services/test"},
			},
			{
				ID:       "telegram-1",
				Type:     notification.ProviderTelegram,
				Enabled:  true,
				Telegram: &notification.TelegramTarget{BotToken: "telegram-token", ChatID: "12345"},
			},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	settings, err = notification.Normalize(settings, "tester")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), settings))

	entries, err := os.ReadDir(store.baseDir)
	require.NoError(t, err)
	var settingsFile string
	for _, entry := range entries {
		if !entry.IsDir() {
			settingsFile = entry.Name()
			break
		}
	}
	require.NotEmpty(t, settingsFile)
	raw, err := os.ReadFile(filepath.Join(store.baseDir, settingsFile)) //nolint:gosec // test reads its temp directory.
	require.NoError(t, err)
	for _, secret := range []string{
		"https://example.com/webhook",
		"Bearer secret-token",
		"hmac-secret",
		"https://hooks.slack.com/services/test",
		"telegram-token",
	} {
		assert.NotContains(t, string(raw), secret)
	}

	got, err := store.GetByDAGName(context.Background(), "daily-report")
	require.NoError(t, err)
	require.Len(t, got.Targets, 3)
	assert.Equal(t, "https://example.com/webhook", got.Targets[0].Webhook.URL)
	assert.Equal(t, []eventstore.EventType{eventstore.TypeDAGRunFailed}, got.Targets[0].Events)
	assert.Equal(t, "Bearer secret-token", got.Targets[0].Webhook.Headers["Authorization"])
	assert.Equal(t, "hmac-secret", got.Targets[0].Webhook.HMACSecret)
	assert.True(t, got.Targets[0].Webhook.AllowPrivateNetwork)
	assert.Equal(t, "https://hooks.slack.com/services/test", got.Targets[1].Slack.WebhookURL)
	assert.Equal(t, "telegram-token", got.Targets[2].Telegram.BotToken)
}

func TestStore_SaveSecretTargetRequiresEncryptor(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir())
	require.NoError(t, err)
	settings := &notification.Settings{
		ID:      "settings-1",
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notification.Target{{
			ID:      "slack-1",
			Type:    notification.ProviderSlack,
			Enabled: true,
			Slack:   &notification.SlackTarget{WebhookURL: "https://hooks.slack.com/services/test"},
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	settings, err = notification.Normalize(settings, "tester")
	require.NoError(t, err)

	err = store.Save(context.Background(), settings)
	assert.ErrorIs(t, err, notification.ErrSecretStoreMissing)
}

func TestStore_PersistsReusableChannelsAndSubscriptions(t *testing.T) {
	t.Parallel()

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	store, err := New(t.TempDir(), WithEncryptor(enc))
	require.NoError(t, err)

	channel, err := notification.NormalizeChannel(&notification.Channel{
		ID:      "channel-1",
		Name:    "Ops Webhook",
		Type:    notification.ProviderWebhook,
		Enabled: true,
		Webhook: &notification.WebhookTarget{
			URL:        "https://example.com/webhook",
			HMACSecret: "channel-secret",
		},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.SaveChannel(context.Background(), channel))

	settings, err := notification.Normalize(&notification.Settings{
		ID:      "settings-1",
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Subscriptions: []notification.Subscription{{
			ID:        "subscription-1",
			ChannelID: "channel-1",
			Enabled:   true,
			Events:    []eventstore.EventType{eventstore.TypeDAGRunFailed},
		}},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), settings))

	rawChannel, err := os.ReadFile(store.channelFilePath("channel-1")) //nolint:gosec // test reads its temp directory.
	require.NoError(t, err)
	assert.NotContains(t, string(rawChannel), "https://example.com/webhook")
	assert.NotContains(t, string(rawChannel), "channel-secret")

	gotChannel, err := store.GetChannel(context.Background(), "channel-1")
	require.NoError(t, err)
	assert.Equal(t, "Ops Webhook", gotChannel.Name)
	assert.Equal(t, "https://example.com/webhook", gotChannel.Webhook.URL)
	assert.Equal(t, "channel-secret", gotChannel.Webhook.HMACSecret)

	gotSettings, err := store.GetByDAGName(context.Background(), "daily-report")
	require.NoError(t, err)
	require.Len(t, gotSettings.Subscriptions, 1)
	assert.Equal(t, "subscription-1", gotSettings.Subscriptions[0].ID)
	assert.Equal(t, "channel-1", gotSettings.Subscriptions[0].ChannelID)

	channels, err := store.ListChannels(context.Background())
	require.NoError(t, err)
	require.Len(t, channels, 1)
	assert.Equal(t, "channel-1", channels[0].ID)
}

func TestStore_SaveSecretChannelRequiresEncryptor(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir())
	require.NoError(t, err)
	channel, err := notification.NormalizeChannel(&notification.Channel{
		ID:      "channel-1",
		Name:    "Ops Slack",
		Type:    notification.ProviderSlack,
		Enabled: true,
		Slack:   &notification.SlackTarget{WebhookURL: "https://hooks.slack.com/services/test"},
	}, "tester")
	require.NoError(t, err)

	err = store.SaveChannel(context.Background(), channel)
	assert.ErrorIs(t, err, notification.ErrSecretStoreMissing)
}
