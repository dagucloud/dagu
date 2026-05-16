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
	require.Len(t, entries, 1)
	raw, err := os.ReadFile(filepath.Join(store.baseDir, entries[0].Name())) //nolint:gosec // test reads its temp directory.
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
