// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	dagucrypto "github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/license"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotificationChannels_AvailableWithoutLicense(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	resp := server.Client().Get("/api/v1/notification-channels").
		ExpectStatus(http.StatusOK).Send(t)

	var result api.NotificationChannelListResponse
	resp.Unmarshal(t, &result)
	assert.Empty(t, result.Channels)
}

func TestNotificationChannels_UnavailableWithoutEventStore(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.EventStore.Enabled = false
	}))
	resp := server.Client().Get("/api/v1/notification-channels").
		ExpectStatus(http.StatusServiceUnavailable).Send(t)

	var result api.Error
	resp.Unmarshal(t, &result)
	assert.Contains(t, result.Message, "Notification delivery is unavailable")
	assert.Contains(t, result.Message, "notification store")
	assert.Contains(t, result.Message, "notification state")
}

func TestNotificationChannels_AcceptExistingLicenseWithoutFeatureClaim(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t,
		test.WithServerOptions(frontend.WithLicenseManager(license.NewTestManager())),
	)
	resp := server.Client().Get("/api/v1/notification-channels").
		ExpectStatus(http.StatusOK).Send(t)

	var result api.NotificationChannelListResponse
	resp.Unmarshal(t, &result)
	assert.Empty(t, result.Channels)
}

func TestNotificationRoutes_AvailableWithoutLicense(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	resp := server.Client().Get("/api/v1/notification-routes/global").
		ExpectStatus(http.StatusOK).Send(t)

	var result api.NotificationRouteSet
	resp.Unmarshal(t, &result)
	assert.Equal(t, api.NotificationRouteScopeGlobal, result.Scope)
}

func TestNotificationRoutes_GlobalAndWorkspaceRouteSets(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)

	channelResp := server.Client().Post("/api/v1/notification-channels", api.NotificationChannelInput{
		Name:    "Ops Webhook",
		Type:    api.NotificationProviderTypeWebhook,
		Enabled: true,
		Webhook: &api.NotificationWebhookTargetInput{
			Url: new("https://example.com/webhook"),
		},
	}).ExpectStatus(http.StatusCreated).Send(t)
	var channel api.NotificationChannel
	channelResp.Unmarshal(t, &channel)

	globalResp := server.Client().Put("/api/v1/notification-routes/global", api.NotificationRouteSetInput{
		Enabled:       true,
		InheritGlobal: true,
		Routes: []api.NotificationRouteInput{{
			Id:        new("global-route"),
			ChannelId: channel.Id,
			Enabled:   true,
			Events: &[]api.NotificationEventType{
				api.NotificationEventTypeDagRunFailed,
				api.NotificationEventTypeDagRunPartiallySucceeded,
			},
		}},
	}).ExpectStatus(http.StatusOK).Send(t)
	var globalRoutes api.NotificationRouteSet
	globalResp.Unmarshal(t, &globalRoutes)
	assert.Equal(t, api.NotificationRouteScopeGlobal, globalRoutes.Scope)
	assert.True(t, globalRoutes.InheritGlobal)
	require.Len(t, globalRoutes.Routes, 1)
	assert.Equal(t, "global-route", globalRoutes.Routes[0].Id)
	require.NotNil(t, globalRoutes.Routes[0].Events)
	assert.Contains(t, *globalRoutes.Routes[0].Events, api.NotificationEventTypeDagRunPartiallySucceeded)

	server.Client().Post("/api/v1/workspaces", api.CreateWorkspaceRequest{
		Name: "ops",
	}).ExpectStatus(http.StatusCreated).Send(t)
	workspaceResp := server.Client().Put("/api/v1/notification-routes/workspaces/ops", api.NotificationRouteSetInput{
		Enabled:       true,
		InheritGlobal: false,
		Routes: []api.NotificationRouteInput{{
			Id:        new("ops-route"),
			ChannelId: channel.Id,
			Enabled:   true,
			Events:    &[]api.NotificationEventType{api.NotificationEventTypeDagRunWaiting},
		}},
	}).ExpectStatus(http.StatusOK).Send(t)
	var workspaceRoutes api.NotificationRouteSet
	workspaceResp.Unmarshal(t, &workspaceRoutes)
	assert.Equal(t, api.NotificationRouteScopeWorkspace, workspaceRoutes.Scope)
	assert.Equal(t, "ops", testValue(workspaceRoutes.Workspace))
	assert.False(t, workspaceRoutes.InheritGlobal)
	require.Len(t, workspaceRoutes.Routes, 1)
	assert.Equal(t, "ops-route", workspaceRoutes.Routes[0].Id)

	listResp := server.Client().Get("/api/v1/notification-routes").
		ExpectStatus(http.StatusOK).Send(t)
	var list api.NotificationRouteSetListResponse
	listResp.Unmarshal(t, &list)
	require.Len(t, list.RouteSets, 2)
}

func TestNotificationSettings_SMTPTransportIsNotReusableChannelLicensed(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	response := server.Client().Put("/api/v1/notification-settings", api.NotificationWorkspaceSettingsInput{
		Smtp: &api.NotificationSMTPSettingsInput{
			Host:     new("smtp.example.com"),
			Port:     new("587"),
			Username: new("smtp-user"),
			Password: new("smtp-secret"),
			From:     new("dagu@example.com"),
		},
	}).ExpectStatus(http.StatusOK).Send(t)

	var settings api.NotificationWorkspaceSettings
	response.Unmarshal(t, &settings)
	require.NotNil(t, settings.Smtp)
	assert.Equal(t, "smtp.example.com", testValue(settings.Smtp.Host))
	assert.Equal(t, "587", testValue(settings.Smtp.Port))
	assert.Equal(t, "smtp-user", testValue(settings.Smtp.Username))
	assert.Equal(t, "dagu@example.com", testValue(settings.Smtp.From))
	assert.True(t, settings.Smtp.PasswordConfigured)

	response = server.Client().Put("/api/v1/notification-settings", api.NotificationWorkspaceSettingsInput{
		Smtp: &api.NotificationSMTPSettingsInput{
			Host:     new("smtp2.example.com"),
			Port:     new("2525"),
			Username: new("smtp-user"),
			From:     new("dagu@example.com"),
		},
	}).ExpectStatus(http.StatusOK).Send(t)
	response.Unmarshal(t, &settings)
	require.NotNil(t, settings.Smtp)
	assert.Equal(t, "smtp2.example.com", testValue(settings.Smtp.Host))
	assert.True(t, settings.Smtp.PasswordConfigured)

	response = server.Client().Put("/api/v1/notification-settings", api.NotificationWorkspaceSettingsInput{
		Smtp: &api.NotificationSMTPSettingsInput{
			Host:          new("smtp2.example.com"),
			Port:          new("2525"),
			Username:      new("smtp-user"),
			From:          new("dagu@example.com"),
			ClearPassword: new(true),
		},
	}).ExpectStatus(http.StatusOK).Send(t)
	response.Unmarshal(t, &settings)
	require.NotNil(t, settings.Smtp)
	assert.False(t, settings.Smtp.PasswordConfigured)

	server.Client().Put("/api/v1/notification-settings", api.NotificationWorkspaceSettingsInput{
		Smtp: &api.NotificationSMTPSettingsInput{
			Username: new("sender@contoso.com"),
			Password: new("smtp-secret"),
			From:     new("sender@contoso.com"),
			Oauth: &api.NotificationSMTPOAuthSettingsInput{
				Provider:     api.NotificationSMTPOAuthProviderMicrosoft,
				TenantId:     new("tenant"),
				ClientId:     new("client"),
				ClientSecret: new("client-secret"),
			},
		},
	}).ExpectStatus(http.StatusBadRequest).Send(t)

	response = server.Client().Put("/api/v1/notification-settings", api.NotificationWorkspaceSettingsInput{
		Smtp: &api.NotificationSMTPSettingsInput{
			Username: new("sender@contoso.com"),
			From:     new("sender@contoso.com"),
			Oauth: &api.NotificationSMTPOAuthSettingsInput{
				Provider:     api.NotificationSMTPOAuthProviderMicrosoft,
				TenantId:     new("tenant"),
				ClientId:     new("client"),
				ClientSecret: new("client-secret"),
			},
		},
	}).ExpectStatus(http.StatusOK).Send(t)
	response.Unmarshal(t, &settings)
	require.NotNil(t, settings.Smtp)
	assert.Equal(t, "smtp.office365.com", testValue(settings.Smtp.Host))
	assert.Equal(t, "587", testValue(settings.Smtp.Port))
	require.NotNil(t, settings.Smtp.Oauth)
	assert.Equal(t, api.NotificationSMTPOAuthProviderMicrosoft, settings.Smtp.Oauth.Provider)
	assert.True(t, settings.Smtp.Oauth.ClientSecretConfigured)

	response = server.Client().Put("/api/v1/notification-settings", api.NotificationWorkspaceSettingsInput{
		Smtp: &api.NotificationSMTPSettingsInput{
			Username: new("sender@contoso.com"),
			From:     new("sender@contoso.com"),
			Oauth: &api.NotificationSMTPOAuthSettingsInput{
				Provider: api.NotificationSMTPOAuthProviderMicrosoft,
				TenantId: new("tenant"),
				ClientId: new("client"),
			},
		},
	}).ExpectStatus(http.StatusOK).Send(t)
	response.Unmarshal(t, &settings)
	require.NotNil(t, settings.Smtp)
	require.NotNil(t, settings.Smtp.Oauth)
	assert.True(t, settings.Smtp.Oauth.ClientSecretConfigured)

	server.Client().Put("/api/v1/notification-settings", api.NotificationWorkspaceSettingsInput{
		Smtp: &api.NotificationSMTPSettingsInput{
			Username: new("other@contoso.com"),
			From:     new("other@contoso.com"),
			Oauth: &api.NotificationSMTPOAuthSettingsInput{
				Provider: api.NotificationSMTPOAuthProviderMicrosoft,
				TenantId: new("tenant"),
				ClientId: new("client"),
			},
		},
	}).ExpectStatus(http.StatusBadRequest).Send(t)

	response = server.Client().Put("/api/v1/notification-settings", api.NotificationWorkspaceSettingsInput{
		Smtp: &api.NotificationSMTPSettingsInput{
			Username: new("sender@gmail.com"),
			From:     new("sender@gmail.com"),
			Oauth: &api.NotificationSMTPOAuthSettingsInput{
				Provider:     api.NotificationSMTPOAuthProviderGoogleRefresh,
				ClientId:     new("google-client"),
				ClientSecret: new("google-secret"),
				RefreshToken: new("refresh-token"),
			},
		},
	}).ExpectStatus(http.StatusOK).Send(t)
	response.Unmarshal(t, &settings)
	require.NotNil(t, settings.Smtp)
	require.NotNil(t, settings.Smtp.Oauth)
	assert.Equal(t, api.NotificationSMTPOAuthProviderGoogleRefresh, settings.Smtp.Oauth.Provider)
	assert.True(t, settings.Smtp.Oauth.ClientSecretConfigured)
	assert.True(t, settings.Smtp.Oauth.RefreshTokenConfigured)

	serviceAccountJSON := `{"type":"service_account","client_email":"service@example.com","private_key":"private-key"}`
	response = server.Client().Put("/api/v1/notification-settings", api.NotificationWorkspaceSettingsInput{
		Smtp: &api.NotificationSMTPSettingsInput{
			Username: new("sender@example.com"),
			From:     new("sender@example.com"),
			Oauth: &api.NotificationSMTPOAuthSettingsInput{
				Provider:           api.NotificationSMTPOAuthProviderGoogleServiceAccount,
				ServiceAccountJson: &serviceAccountJSON,
			},
		},
	}).ExpectStatus(http.StatusOK).Send(t)
	response.Unmarshal(t, &settings)
	require.NotNil(t, settings.Smtp)
	require.NotNil(t, settings.Smtp.Oauth)
	assert.Equal(t, api.NotificationSMTPOAuthProviderGoogleServiceAccount, settings.Smtp.Oauth.Provider)
	assert.True(t, settings.Smtp.Oauth.ServiceAccountJsonConfigured)

	response = server.Client().Put("/api/v1/notification-settings", api.NotificationWorkspaceSettingsInput{
		Smtp: &api.NotificationSMTPSettingsInput{
			Host:     new("smtp.example.com"),
			Port:     new("587"),
			Username: new("smtp-user"),
			Password: new("smtp-secret"),
			From:     new("dagu@example.com"),
		},
	}).ExpectStatus(http.StatusOK).Send(t)
	settings = api.NotificationWorkspaceSettings{}
	response.Unmarshal(t, &settings)
	require.NotNil(t, settings.Smtp)
	assert.Nil(t, settings.Smtp.Oauth)
	assert.True(t, settings.Smtp.PasswordConfigured)
}

func testValue[T any](value *T) T {
	if value == nil {
		var zero T
		return zero
	}
	return *value
}

func TestDAGNotifications_SubscriptionUpdatesWithoutLicense(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		subscriptions        *[]api.NotificationSubscriptionInput
		wantSubscriptionIDs  []string
		wantSubscriptionRefs []string
	}{
		{
			name:                 "omitted subscriptions preserves existing reusable subscription",
			subscriptions:        nil,
			wantSubscriptionIDs:  []string{"subscription-1"},
			wantSubscriptionRefs: []string{"channel-1"},
		},
		{
			name:          "empty subscriptions clears existing reusable subscriptions",
			subscriptions: &[]api.NotificationSubscriptionInput{},
		},
		{
			name: "non-empty subscriptions replaces reusable subscriptions",
			subscriptions: &[]api.NotificationSubscriptionInput{{
				Id:        new("subscription-2"),
				ChannelId: "channel-1",
				Enabled:   true,
			}},
			wantSubscriptionIDs:  []string{"subscription-2"},
			wantSubscriptionRefs: []string{"channel-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := test.SetupServer(t)
			dagName := "daily-report"
			createTestDAG(t, server, "", dagName)
			store := seedReusableNotificationSubscription(t, server, dagName)

			response := server.Client().Put("/api/v1/dags/"+dagName+"/notifications", api.UpdateDAGNotificationsRequest{
				Enabled: true,
				Events:  []api.NotificationEventType{api.NotificationEventTypeDagRunFailed},
				Targets: []api.NotificationTargetInput{},
				// nil means an older client is not managing reusable subscriptions;
				// non-nil means it is trying to replace them.
				Subscriptions: tt.subscriptions,
			}).ExpectStatus(http.StatusOK).Send(t)

			var apiSettings api.DAGNotificationSettings
			response.Unmarshal(t, &apiSettings)
			require.Len(t, apiSettings.Subscriptions, len(tt.wantSubscriptionIDs))
			for i, wantID := range tt.wantSubscriptionIDs {
				assert.Equal(t, wantID, apiSettings.Subscriptions[i].Id)
				assert.Equal(t, tt.wantSubscriptionRefs[i], apiSettings.Subscriptions[i].ChannelId)
			}

			settings, err := store.GetByDAGName(context.Background(), dagName)
			require.NoError(t, err)
			require.Len(t, settings.Subscriptions, len(tt.wantSubscriptionIDs))
			for i, wantID := range tt.wantSubscriptionIDs {
				assert.Equal(t, wantID, settings.Subscriptions[i].ID)
				assert.Equal(t, tt.wantSubscriptionRefs[i], settings.Subscriptions[i].ChannelID)
			}
		})
	}
}

func seedReusableNotificationSubscription(t *testing.T, server test.Server, dagName string) notificationmodel.Store {
	t.Helper()

	key, err := dagucrypto.ResolveKey(server.Config.Paths.DataDir)
	require.NoError(t, err)
	encryptor, err := dagucrypto.NewEncryptor(key)
	require.NoError(t, err)
	store, err := persisfile.NewNotificationStore(
		server.Backend.Collection(persis.CollectionNotifications),
		encryptor,
	)
	require.NoError(t, err)

	channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "channel-1",
		Name:    "Ops Webhook",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{URL: "https://example.com/webhook"},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.SaveChannel(context.Background(), channel))

	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: dagName,
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Subscriptions: []notificationmodel.Subscription{{
			ID:        "subscription-1",
			ChannelID: channel.ID,
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), settings))

	return store
}
