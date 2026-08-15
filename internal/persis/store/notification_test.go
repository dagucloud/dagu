// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func newMemoryNotificationStore(t *testing.T, encrypted bool) (*store.NotificationStore, persis.Collection) {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection(persis.CollectionNotifications)
	var err error
	var s *store.NotificationStore
	if encrypted {
		s, err = store.NewNotificationStore(col, newTestEncryptor(t))
	} else {
		s, err = store.NewNotificationStore(col, nil)
	}
	require.NoError(t, err)
	return s, col
}

func TestNotificationStoreEncryptsTargetSecrets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, col := newMemoryNotificationStore(t, true)
	settings := &notification.Settings{
		ID:      "settings-1",
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notification.Target{
			{
				ID: "webhook-1", Type: notification.ProviderWebhook, Enabled: true,
				Events: []eventstore.EventType{eventstore.TypeDAGRunFailed},
				Webhook: &notification.WebhookTarget{
					URL: "https://example.com/webhook", Headers: map[string]string{"Authorization": "Bearer secret-token"},
					HMACSecret: "hmac-secret", MessageTemplate: "DAG {{dag.name}}", AllowPrivateNetwork: true,
				},
			},
			{
				ID: "slack-1", Type: notification.ProviderSlack, Enabled: true,
				Slack: &notification.SlackTarget{WebhookURL: "https://hooks.slack.com/services/test"},
			},
			{
				ID: "telegram-1", Type: notification.ProviderTelegram, Enabled: true,
				Telegram: &notification.TelegramTarget{BotToken: "telegram-token", ChatID: "12345", TopicID: "67890"},
			},
		},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	settings, err := notification.Normalize(settings, "tester")
	require.NoError(t, err)
	require.NoError(t, s.Save(ctx, settings))

	page, err := col.List(ctx, persis.ListQuery{Prefix: "dags/"})
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	for _, secret := range []string{
		"https://example.com/webhook", "Bearer secret-token", "hmac-secret",
		"https://hooks.slack.com/services/test", "telegram-token",
	} {
		assert.NotContains(t, string(page.Records[0].Data), secret)
	}
	originalCreatedAt := time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC)
	page.Records[0].CreatedAt = originalCreatedAt
	require.NoError(t, col.Put(ctx, page.Records[0]))
	settings.Enabled = false
	require.NoError(t, s.Save(ctx, settings))
	updated, err := col.Get(ctx, page.Records[0].ID)
	require.NoError(t, err)
	assert.Equal(t, originalCreatedAt, updated.CreatedAt)

	got, err := s.GetByDAGName(ctx, "daily-report")
	require.NoError(t, err)
	require.Len(t, got.Targets, 3)
	assert.Equal(t, "https://example.com/webhook", got.Targets[0].Webhook.URL)
	assert.Equal(t, "Bearer secret-token", got.Targets[0].Webhook.Headers["Authorization"])
	assert.Equal(t, "hmac-secret", got.Targets[0].Webhook.HMACSecret)
	assert.True(t, got.Targets[0].Webhook.AllowPrivateNetwork)
	assert.Equal(t, "https://hooks.slack.com/services/test", got.Targets[1].Slack.WebhookURL)
	assert.Equal(t, "telegram-token", got.Targets[2].Telegram.BotToken)
	assert.Equal(t, "67890", got.Targets[2].Telegram.TopicID)
}

func TestNotificationStoreRejectsInvalidRouteScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := newMemoryNotificationStore(t, false)
	global, err := notification.NormalizeRouteSet(&notification.RouteSet{
		ID: "global", Scope: notification.RouteScopeGlobal, Enabled: true,
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, s.SaveRouteSet(ctx, global))

	invalid := *global
	invalid.ID = "invalid"
	invalid.Scope = notification.RouteScope("invalid")
	assert.ErrorIs(t, s.SaveRouteSet(ctx, &invalid), notification.ErrInvalidSettings)
	assert.ErrorIs(t, s.DeleteRouteSet(ctx, invalid.Scope, ""), notification.ErrInvalidSettings)
	_, err = s.GetRouteSet(ctx, invalid.Scope, "")
	assert.ErrorIs(t, err, notification.ErrInvalidSettings)

	stored, err := s.GetRouteSet(ctx, notification.RouteScopeGlobal, "")
	require.NoError(t, err)
	assert.Equal(t, global.ID, stored.ID)
}

func TestNotificationStorePersistsChannelsAndSubscriptions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, col := newMemoryNotificationStore(t, true)
	channel, err := notification.NormalizeChannel(&notification.Channel{
		ID: "channel-1", Name: "Ops Webhook", Type: notification.ProviderWebhook, Enabled: true,
		Webhook: &notification.WebhookTarget{
			URL: "https://example.com/webhook", HMACSecret: "channel-secret", MessageTemplate: "Route {{dag.name}}",
		},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, s.SaveChannel(ctx, channel))

	settings, err := notification.Normalize(&notification.Settings{
		ID: "settings-1", DAGName: "daily-report", Enabled: true,
		Events: []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Subscriptions: []notification.Subscription{{
			ID: "subscription-1", ChannelID: "channel-1", Enabled: true,
			Events: []eventstore.EventType{eventstore.TypeDAGRunFailed},
		}},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, s.Save(ctx, settings))

	page, err := col.List(ctx, persis.ListQuery{Prefix: "channels/"})
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	assert.NotContains(t, string(page.Records[0].Data), "https://example.com/webhook")
	assert.NotContains(t, string(page.Records[0].Data), "channel-secret")

	gotChannel, err := s.GetChannel(ctx, "channel-1")
	require.NoError(t, err)
	assert.Equal(t, "Ops Webhook", gotChannel.Name)
	assert.Equal(t, "channel-secret", gotChannel.Webhook.HMACSecret)
	gotSettings, err := s.GetByDAGName(ctx, "daily-report")
	require.NoError(t, err)
	require.Len(t, gotSettings.Subscriptions, 1)
	assert.Equal(t, "channel-1", gotSettings.Subscriptions[0].ChannelID)
	channels, err := s.ListChannels(ctx)
	require.NoError(t, err)
	require.Len(t, channels, 1)
}

func TestNotificationStorePersistsWorkspaceSMTPSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, col := newMemoryNotificationStore(t, true)
	settings, err := notification.NormalizeWorkspaceSettings(&notification.WorkspaceSettings{
		SMTP: &notification.SMTPConfig{
			Host: "smtp.example.com", Port: "587", Username: "sender@example.com",
			From: "sender@example.com", Password: "smtp-secret",
		},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, s.SaveWorkspaceSettings(ctx, settings))

	rec, err := col.Get(ctx, "workspace")
	require.NoError(t, err)
	assert.NotContains(t, string(rec.Data), "smtp-secret")

	got, err := s.GetWorkspaceSettings(ctx)
	require.NoError(t, err)
	require.NotNil(t, got.SMTP)
	assert.Equal(t, "smtp-secret", got.SMTP.Password)

	settings, err = notification.NormalizeWorkspaceSettings(&notification.WorkspaceSettings{
		SMTP: &notification.SMTPConfig{
			Username: "sender@gmail.com", From: "sender@gmail.com",
			OAuth: &oauthconfig.Config{
				Provider: oauthconfig.ProviderGoogleRefresh, ClientID: "client-id",
				ClientSecret: "client-secret", RefreshToken: "refresh-token",
			},
		},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, s.SaveWorkspaceSettings(ctx, settings))
	rec, err = col.Get(ctx, "workspace")
	require.NoError(t, err)
	assert.Contains(t, string(rec.Data), "google_refresh")
	assert.NotContains(t, string(rec.Data), "client-secret")
	assert.NotContains(t, string(rec.Data), "refresh-token")
	got, err = s.GetWorkspaceSettings(ctx)
	require.NoError(t, err)
	require.NotNil(t, got.SMTP.OAuth)
	assert.Equal(t, "smtp.gmail.com", got.SMTP.Host)
	assert.Equal(t, "client-secret", got.SMTP.OAuth.ClientSecret)
	assert.Equal(t, "refresh-token", got.SMTP.OAuth.RefreshToken)

	serviceAccountJSON := `{"type":"service_account","client_email":"sender@example.com"}`
	serviceAccount := &notification.WorkspaceSettings{
		SMTP: &notification.SMTPConfig{
			Host: "smtp.gmail.com", Port: "587", Username: "sender@example.com", From: "sender@example.com",
			OAuth: &oauthconfig.Config{
				Provider: oauthconfig.ProviderGoogleServiceAccount, ServiceAccountJSON: serviceAccountJSON,
			},
		},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, s.SaveWorkspaceSettings(ctx, serviceAccount))
	rec, err = col.Get(ctx, "workspace")
	require.NoError(t, err)
	assert.NotContains(t, string(rec.Data), serviceAccountJSON)
	got, err = s.GetWorkspaceSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, serviceAccountJSON, got.SMTP.OAuth.ServiceAccountJSON)

	microsoft := &notification.WorkspaceSettings{
		SMTP: &notification.SMTPConfig{
			Host: "smtp.office365.com", Port: "587", Username: "sender@contoso.com", From: "sender@contoso.com",
			OAuth: &oauthconfig.Config{
				Provider: oauthconfig.ProviderMicrosoft, TenantID: "tenant-id",
				ClientID: "microsoft-client", ClientSecret: "microsoft-secret",
			},
		},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, s.SaveWorkspaceSettings(ctx, microsoft))
	got, err = s.GetWorkspaceSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, "tenant-id", got.SMTP.OAuth.TenantID)
	assert.Equal(t, "microsoft-client", got.SMTP.OAuth.ClientID)
	assert.Equal(t, "microsoft-secret", got.SMTP.OAuth.ClientSecret)
}

func TestNotificationStorePersistsRouteSets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := newMemoryNotificationStore(t, false)
	global, err := notification.NormalizeRouteSet(&notification.RouteSet{
		ID: "global-routes", Scope: notification.RouteScopeGlobal, Enabled: true, InheritGlobal: true,
		Routes: []notification.Route{{
			ID: "route-1", ChannelID: "channel-1", Enabled: true,
			Events: []eventstore.EventType{eventstore.TypeDAGRunFailed},
		}},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, s.SaveRouteSet(ctx, global))
	workspace, err := notification.NormalizeRouteSet(&notification.RouteSet{
		ID: "workspace-routes", Scope: notification.RouteScopeWorkspace, Workspace: "ops", Enabled: true,
		Routes: []notification.Route{{ID: "route-2", ChannelID: "channel-2", Enabled: true}},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, s.SaveRouteSet(ctx, workspace))

	got, err := s.GetRouteSet(ctx, notification.RouteScopeWorkspace, "ops")
	require.NoError(t, err)
	assert.Equal(t, "workspace-routes", got.ID)
	assert.Equal(t, "channel-2", got.Routes[0].ChannelID)
	routeSets, err := s.ListRouteSets(ctx)
	require.NoError(t, err)
	require.Len(t, routeSets, 2)
	assert.ElementsMatch(t, []string{"global-routes", "workspace-routes"}, []string{routeSets[0].ID, routeSets[1].ID})
	require.NoError(t, s.DeleteRouteSet(ctx, notification.RouteScopeWorkspace, "ops"))
	_, err = s.GetRouteSet(ctx, notification.RouteScopeWorkspace, "ops")
	assert.ErrorIs(t, err, notification.ErrRouteSetNotFound)
}

func TestNotificationStoreRequiresEncryptorForSecrets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := newMemoryNotificationStore(t, false)

	settings := &notification.Settings{
		ID: "settings-1", DAGName: "daily-report", Enabled: true,
		Events: []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notification.Target{{
			ID: "slack-1", Type: notification.ProviderSlack, Enabled: true,
			Slack: &notification.SlackTarget{WebhookURL: "https://hooks.slack.com/services/test"},
		}},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	settings, err := notification.Normalize(settings, "tester")
	require.NoError(t, err)
	assert.ErrorIs(t, s.Save(ctx, settings), notification.ErrSecretStoreMissing)

	workspaceSettings, err := notification.NormalizeWorkspaceSettings(&notification.WorkspaceSettings{
		SMTP: &notification.SMTPConfig{
			Host: "smtp.example.com", Port: "587", Username: "sender@example.com",
			From: "sender@example.com", Password: "smtp-secret",
		},
	}, "tester")
	require.NoError(t, err)
	assert.ErrorIs(t, s.SaveWorkspaceSettings(ctx, workspaceSettings), notification.ErrSecretStoreMissing)

	channel, err := notification.NormalizeChannel(&notification.Channel{
		ID: "channel-1", Name: "Ops Slack", Type: notification.ProviderSlack, Enabled: true,
		Slack: &notification.SlackTarget{WebhookURL: "https://hooks.slack.com/services/test"},
	}, "tester")
	require.NoError(t, err)
	assert.ErrorIs(t, s.SaveChannel(ctx, channel), notification.ErrSecretStoreMissing)
}

func TestNotificationStoreMissingRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := newMemoryNotificationStore(t, false)

	_, err := s.GetByDAGName(ctx, "missing")
	assert.ErrorIs(t, err, notification.ErrSettingsNotFound)
	_, err = s.GetChannel(ctx, "missing")
	assert.ErrorIs(t, err, notification.ErrChannelNotFound)
	_, err = s.GetRouteSet(ctx, notification.RouteScopeGlobal, "")
	assert.ErrorIs(t, err, notification.ErrRouteSetNotFound)
	workspace, err := s.GetWorkspaceSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, &notification.WorkspaceSettings{}, workspace)

	assert.ErrorIs(t, s.DeleteByDAGName(ctx, "missing"), notification.ErrSettingsNotFound)
	assert.ErrorIs(t, s.DeleteChannel(ctx, "missing"), notification.ErrChannelNotFound)
	assert.ErrorIs(t, s.DeleteRouteSet(ctx, notification.RouteScopeGlobal, ""), notification.ErrRouteSetNotFound)
}
