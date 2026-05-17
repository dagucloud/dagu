// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/api/v1"
	dagucrypto "github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/license"
	notificationmodel "github.com/dagucloud/dagu/internal/notification"
	"github.com/dagucloud/dagu/internal/persis/filenotification"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/dagucloud/dagu/internal/service/frontend"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotificationChannels_RequireActiveLicense(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	server.Client().Get("/api/v1/notification-channels").
		ExpectStatus(http.StatusForbidden).Send(t)
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

func TestNotificationRoutes_RequireActiveLicense(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	server.Client().Get("/api/v1/notification-routes/global").
		ExpectStatus(http.StatusForbidden).Send(t)
}

func TestNotificationRoutes_GlobalAndWorkspaceRouteSets(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t,
		test.WithServerOptions(frontend.WithLicenseManager(license.NewTestManager())),
	)

	channelResp := server.Client().Post("/api/v1/notification-channels", api.NotificationChannelInput{
		Name:    "Ops Webhook",
		Type:    api.NotificationProviderTypeWebhook,
		Enabled: true,
		Webhook: &api.NotificationWebhookTargetInput{
			Url: testPtr("https://example.com/webhook"),
		},
	}).ExpectStatus(http.StatusCreated).Send(t)
	var channel api.NotificationChannel
	channelResp.Unmarshal(t, &channel)

	globalResp := server.Client().Put("/api/v1/notification-routes/global", api.NotificationRouteSetInput{
		Enabled:       true,
		InheritGlobal: true,
		Routes: []api.NotificationRouteInput{{
			Id:        testPtr("global-route"),
			ChannelId: channel.Id,
			Enabled:   true,
			Events:    &[]api.NotificationEventType{api.NotificationEventTypeDagRunFailed},
		}},
	}).ExpectStatus(http.StatusOK).Send(t)
	var globalRoutes api.NotificationRouteSet
	globalResp.Unmarshal(t, &globalRoutes)
	assert.Equal(t, api.NotificationRouteScopeGlobal, globalRoutes.Scope)
	assert.True(t, globalRoutes.InheritGlobal)
	require.Len(t, globalRoutes.Routes, 1)
	assert.Equal(t, "global-route", globalRoutes.Routes[0].Id)

	server.Client().Post("/api/v1/workspaces", api.CreateWorkspaceRequest{
		Name: "ops",
	}).ExpectStatus(http.StatusCreated).Send(t)
	workspaceResp := server.Client().Put("/api/v1/notification-routes/workspaces/ops", api.NotificationRouteSetInput{
		Enabled:       true,
		InheritGlobal: false,
		Routes: []api.NotificationRouteInput{{
			Id:        testPtr("ops-route"),
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
			Host:     testPtr("smtp.example.com"),
			Port:     testPtr("587"),
			Username: testPtr("smtp-user"),
			Password: testPtr("smtp-secret"),
			From:     testPtr("dagu@example.com"),
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
			Host:     testPtr("smtp2.example.com"),
			Port:     testPtr("2525"),
			Username: testPtr("smtp-user"),
			From:     testPtr("dagu@example.com"),
		},
	}).ExpectStatus(http.StatusOK).Send(t)
	response.Unmarshal(t, &settings)
	require.NotNil(t, settings.Smtp)
	assert.Equal(t, "smtp2.example.com", testValue(settings.Smtp.Host))
	assert.True(t, settings.Smtp.PasswordConfigured)

	response = server.Client().Put("/api/v1/notification-settings", api.NotificationWorkspaceSettingsInput{
		Smtp: &api.NotificationSMTPSettingsInput{
			Host:          testPtr("smtp2.example.com"),
			Port:          testPtr("2525"),
			Username:      testPtr("smtp-user"),
			From:          testPtr("dagu@example.com"),
			ClearPassword: testPtr(true),
		},
	}).ExpectStatus(http.StatusOK).Send(t)
	response.Unmarshal(t, &settings)
	require.NotNil(t, settings.Smtp)
	assert.False(t, settings.Smtp.PasswordConfigured)
}

func testPtr[T any](value T) *T {
	return &value
}

func testValue[T any](value *T) T {
	if value == nil {
		var zero T
		return zero
	}
	return *value
}

func TestDAGNotifications_UnlicensedSubscriptionUpdates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		subscriptions     *[]api.NotificationSubscriptionInput
		wantStatus        int
		wantSubscriptions int
	}{
		{
			name:              "omitted subscriptions preserves existing reusable subscription",
			subscriptions:     nil,
			wantStatus:        http.StatusOK,
			wantSubscriptions: 1,
		},
		{
			name:              "empty subscriptions is still gated",
			subscriptions:     &[]api.NotificationSubscriptionInput{},
			wantStatus:        http.StatusForbidden,
			wantSubscriptions: 1,
		},
		{
			name: "non-empty subscriptions is gated",
			subscriptions: &[]api.NotificationSubscriptionInput{{
				ChannelId: "channel-1",
				Enabled:   true,
			}},
			wantStatus:        http.StatusForbidden,
			wantSubscriptions: 1,
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
				// nil means an older/unlicensed client is not managing reusable
				// subscriptions; non-nil means it is trying to replace them.
				Subscriptions: tt.subscriptions,
			}).ExpectStatus(tt.wantStatus).Send(t)

			if tt.wantStatus == http.StatusOK {
				var settings api.DAGNotificationSettings
				response.Unmarshal(t, &settings)
				require.Len(t, settings.Subscriptions, tt.wantSubscriptions)
			}

			settings, err := store.GetByDAGName(context.Background(), dagName)
			require.NoError(t, err)
			require.Len(t, settings.Subscriptions, tt.wantSubscriptions)
			assert.Equal(t, "subscription-1", settings.Subscriptions[0].ID)
		})
	}
}

func seedReusableNotificationSubscription(t *testing.T, server test.Server, dagName string) *filenotification.Store {
	t.Helper()

	key, err := dagucrypto.ResolveKey(server.Config.Paths.DataDir)
	require.NoError(t, err)
	encryptor, err := dagucrypto.NewEncryptor(key)
	require.NoError(t, err)
	store, err := filenotification.New(
		filepath.Join(server.Config.Paths.DataDir, "notifications", "dags"),
		filenotification.WithEncryptor(encryptor),
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
