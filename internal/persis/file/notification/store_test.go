// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package notification

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/notification"
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
					MessageTemplate:     "DAG {{dag.name}} {{run.status}}",
					AllowPrivateNetwork: true,
				},
			},
			{
				ID:      "slack-1",
				Type:    notification.ProviderSlack,
				Enabled: true,
				Slack: &notification.SlackTarget{
					WebhookURL:      "https://hooks.slack.com/services/test",
					MessageTemplate: "Slack {{dag.name}}",
				},
			},
			{
				ID:      "telegram-1",
				Type:    notification.ProviderTelegram,
				Enabled: true,
				Telegram: &notification.TelegramTarget{
					BotToken:        "telegram-token",
					ChatID:          "12345",
					TopicID:         "67890",
					MessageTemplate: "Telegram {{dag.name}}",
				},
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
	assert.Equal(t, "DAG {{dag.name}} {{run.status}}", got.Targets[0].Webhook.MessageTemplate)
	assert.True(t, got.Targets[0].Webhook.AllowPrivateNetwork)
	assert.Equal(t, "https://hooks.slack.com/services/test", got.Targets[1].Slack.WebhookURL)
	assert.Equal(t, "Slack {{dag.name}}", got.Targets[1].Slack.MessageTemplate)
	assert.Equal(t, "telegram-token", got.Targets[2].Telegram.BotToken)
	assert.Equal(t, "12345", got.Targets[2].Telegram.ChatID)
	assert.Equal(t, "67890", got.Targets[2].Telegram.TopicID)
	assert.Equal(t, "Telegram {{dag.name}}", got.Targets[2].Telegram.MessageTemplate)
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
			URL:             "https://example.com/webhook",
			HMACSecret:      "channel-secret",
			MessageTemplate: "Route {{dag.name}}",
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
	assert.Equal(t, "Route {{dag.name}}", gotChannel.Webhook.MessageTemplate)

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

func TestStore_PersistsWorkspaceSMTPSettingsEncrypted(t *testing.T) {
	t.Parallel()

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	store, err := New(t.TempDir(), WithEncryptor(enc))
	require.NoError(t, err)

	settings, err := notification.NormalizeWorkspaceSettings(&notification.WorkspaceSettings{
		SMTP: &notification.SMTPConfig{
			Host:     "smtp.example.com",
			Port:     "587",
			Username: "smtp-user",
			Password: "smtp-secret",
			From:     "dagu@example.com",
		},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.SaveWorkspaceSettings(context.Background(), settings))

	raw, err := os.ReadFile(store.workspaceSettingsFile) //nolint:gosec // test reads its temp directory.
	require.NoError(t, err)
	assert.Contains(t, string(raw), "smtp.example.com")
	assert.NotContains(t, string(raw), "smtp-secret")

	got, err := store.GetWorkspaceSettings(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got.SMTP)
	assert.Equal(t, "smtp.example.com", got.SMTP.Host)
	assert.Equal(t, "587", got.SMTP.Port)
	assert.Equal(t, "smtp-user", got.SMTP.Username)
	assert.Equal(t, "smtp-secret", got.SMTP.Password)
	assert.Equal(t, "dagu@example.com", got.SMTP.From)
	assert.Equal(t, "tester", got.UpdatedBy)
}

func TestStore_PersistsWorkspaceSMTPOAuthSettingsEncrypted(t *testing.T) {
	t.Parallel()

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	store, err := New(t.TempDir(), WithEncryptor(enc))
	require.NoError(t, err)

	settings, err := notification.NormalizeWorkspaceSettings(&notification.WorkspaceSettings{
		SMTP: &notification.SMTPConfig{
			Username: "sender@gmail.com",
			OAuth: &oauthconfig.Config{
				Provider: oauthconfig.ProviderGoogleRefresh, ClientID: "client-id",
				ClientSecret: "client-secret", RefreshToken: "refresh-token",
			},
			From: "sender@gmail.com",
		},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.SaveWorkspaceSettings(context.Background(), settings))

	raw, err := os.ReadFile(store.workspaceSettingsFile) //nolint:gosec // test reads its temp directory.
	require.NoError(t, err)
	assert.Contains(t, string(raw), "google_refresh")
	assert.NotContains(t, string(raw), "client-secret")
	assert.NotContains(t, string(raw), "refresh-token")

	got, err := store.GetWorkspaceSettings(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got.SMTP)
	require.NotNil(t, got.SMTP.OAuth)
	assert.Equal(t, "smtp.gmail.com", got.SMTP.Host)
	assert.Equal(t, oauthconfig.ProviderGoogleRefresh, got.SMTP.OAuth.Provider)
	assert.Equal(t, "client-id", got.SMTP.OAuth.ClientID)
	assert.Equal(t, "client-secret", got.SMTP.OAuth.ClientSecret)
	assert.Equal(t, "refresh-token", got.SMTP.OAuth.RefreshToken)

	serviceAccountJSON := `{"type":"service_account","client_email":"sender@example.com"}`
	serviceAccountSettings := &notification.WorkspaceSettings{
		SMTP: &notification.SMTPConfig{
			Host: "smtp.gmail.com", Port: "587", Username: "sender@example.com", From: "sender@example.com",
			OAuth: &oauthconfig.Config{
				Provider: oauthconfig.ProviderGoogleServiceAccount, ServiceAccountJSON: serviceAccountJSON,
			},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, store.SaveWorkspaceSettings(context.Background(), serviceAccountSettings))
	raw, err = os.ReadFile(store.workspaceSettingsFile) //nolint:gosec // test reads its temp directory.
	require.NoError(t, err)
	assert.NotContains(t, string(raw), serviceAccountJSON)
	got, err = store.GetWorkspaceSettings(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got.SMTP.OAuth)
	assert.Equal(t, serviceAccountJSON, got.SMTP.OAuth.ServiceAccountJSON)

	microsoftSettings := &notification.WorkspaceSettings{
		SMTP: &notification.SMTPConfig{
			Host: "smtp.office365.com", Port: "587", Username: "sender@contoso.com", From: "sender@contoso.com",
			OAuth: &oauthconfig.Config{
				Provider: oauthconfig.ProviderMicrosoft, TenantID: "tenant-id",
				ClientID: "microsoft-client", ClientSecret: "microsoft-secret",
			},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, store.SaveWorkspaceSettings(context.Background(), microsoftSettings))
	raw, err = os.ReadFile(store.workspaceSettingsFile) //nolint:gosec // test reads its temp directory.
	require.NoError(t, err)
	assert.Contains(t, string(raw), "tenant-id")
	assert.NotContains(t, string(raw), "microsoft-secret")
	got, err = store.GetWorkspaceSettings(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got.SMTP.OAuth)
	assert.Equal(t, "tenant-id", got.SMTP.OAuth.TenantID)
	assert.Equal(t, "microsoft-client", got.SMTP.OAuth.ClientID)
	assert.Equal(t, "microsoft-secret", got.SMTP.OAuth.ClientSecret)
}

func TestStore_PersistsGlobalAndWorkspaceRouteSets(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir())
	require.NoError(t, err)

	globalRouteSet, err := notification.NormalizeRouteSet(&notification.RouteSet{
		ID:            "global-routes",
		Scope:         notification.RouteScopeGlobal,
		Enabled:       true,
		InheritGlobal: true,
		Routes: []notification.Route{{
			ID:        "route-1",
			ChannelID: "channel-1",
			Enabled:   true,
			Events:    []eventstore.EventType{eventstore.TypeDAGRunFailed},
		}},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.SaveRouteSet(context.Background(), globalRouteSet))

	workspaceRouteSet, err := notification.NormalizeRouteSet(&notification.RouteSet{
		ID:            "workspace-routes",
		Scope:         notification.RouteScopeWorkspace,
		Workspace:     "ops",
		Enabled:       true,
		InheritGlobal: false,
		Routes: []notification.Route{{
			ID:        "route-2",
			ChannelID: "channel-2",
			Enabled:   true,
			Events:    []eventstore.EventType{eventstore.TypeDAGRunWaiting},
		}},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.SaveRouteSet(context.Background(), workspaceRouteSet))

	gotGlobal, err := store.GetRouteSet(context.Background(), notification.RouteScopeGlobal, "")
	require.NoError(t, err)
	assert.Equal(t, "global-routes", gotGlobal.ID)
	assert.Equal(t, notification.RouteScopeGlobal, gotGlobal.Scope)
	assert.Empty(t, gotGlobal.Workspace)
	require.Len(t, gotGlobal.Routes, 1)
	assert.Equal(t, "channel-1", gotGlobal.Routes[0].ChannelID)

	gotWorkspace, err := store.GetRouteSet(context.Background(), notification.RouteScopeWorkspace, "ops")
	require.NoError(t, err)
	assert.Equal(t, "workspace-routes", gotWorkspace.ID)
	assert.Equal(t, "ops", gotWorkspace.Workspace)
	assert.False(t, gotWorkspace.InheritGlobal)
	require.Len(t, gotWorkspace.Routes, 1)
	assert.Equal(t, "channel-2", gotWorkspace.Routes[0].ChannelID)

	routeSets, err := store.ListRouteSets(context.Background())
	require.NoError(t, err)
	require.Len(t, routeSets, 2)

	require.NoError(t, store.DeleteRouteSet(context.Background(), notification.RouteScopeWorkspace, "ops"))
	_, err = store.GetRouteSet(context.Background(), notification.RouteScopeWorkspace, "ops")
	assert.ErrorIs(t, err, notification.ErrRouteSetNotFound)
}

func TestStore_SaveWorkspaceSMTPPasswordRequiresEncryptor(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir())
	require.NoError(t, err)
	settings, err := notification.NormalizeWorkspaceSettings(&notification.WorkspaceSettings{
		SMTP: &notification.SMTPConfig{
			Host:     "smtp.example.com",
			Port:     "587",
			Username: "smtp-user",
			Password: "smtp-secret",
			From:     "dagu@example.com",
		},
	}, "tester")
	require.NoError(t, err)

	err = store.SaveWorkspaceSettings(context.Background(), settings)
	assert.ErrorIs(t, err, notification.ErrSecretStoreMissing)
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
