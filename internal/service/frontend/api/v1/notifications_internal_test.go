// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"testing"

	openapi "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/license"
	notificationmodel "github.com/dagucloud/dagu/internal/notification"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	localapi "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	notificationservice "github.com/dagucloud/dagu/internal/service/notification"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type notificationServiceStub struct {
	getChannel  func(context.Context, string) (*notificationmodel.Channel, error)
	saveChannel func(context.Context, *notificationmodel.Channel, string) (*notificationmodel.Channel, error)
}

func (s notificationServiceStub) GetByDAGName(context.Context, string) (*notificationmodel.Settings, error) {
	return nil, notificationmodel.ErrSettingsNotFound
}

func (s notificationServiceStub) Save(context.Context, *notificationmodel.Settings, string) (*notificationmodel.Settings, error) {
	return nil, nil
}

func (s notificationServiceStub) DeleteByDAGName(context.Context, string) error {
	return nil
}

func (s notificationServiceStub) GetWorkspaceSettings(context.Context) (*notificationmodel.WorkspaceSettings, error) {
	return &notificationmodel.WorkspaceSettings{}, nil
}

func (s notificationServiceStub) SaveWorkspaceSettings(context.Context, *notificationmodel.WorkspaceSettings, string) (*notificationmodel.WorkspaceSettings, error) {
	return nil, nil
}

func (s notificationServiceStub) GetRouteSet(context.Context, notificationmodel.RouteScope, string) (*notificationmodel.RouteSet, error) {
	return nil, notificationmodel.ErrRouteSetNotFound
}

func (s notificationServiceStub) ListRouteSets(context.Context) ([]*notificationmodel.RouteSet, error) {
	return nil, nil
}

func (s notificationServiceStub) SaveRouteSet(context.Context, *notificationmodel.RouteSet, string) (*notificationmodel.RouteSet, error) {
	return nil, nil
}

func (s notificationServiceStub) ListChannels(context.Context) ([]*notificationmodel.Channel, error) {
	return nil, nil
}

func (s notificationServiceStub) GetChannel(ctx context.Context, channelID string) (*notificationmodel.Channel, error) {
	if s.getChannel != nil {
		return s.getChannel(ctx, channelID)
	}
	return nil, notificationmodel.ErrChannelNotFound
}

func (s notificationServiceStub) SaveChannel(ctx context.Context, channel *notificationmodel.Channel, updatedBy string) (*notificationmodel.Channel, error) {
	if s.saveChannel != nil {
		return s.saveChannel(ctx, channel, updatedBy)
	}
	return channel, nil
}

func (s notificationServiceStub) DeleteChannel(context.Context, string) error {
	return nil
}

func (s notificationServiceStub) SendTest(context.Context, string, string, eventstore.EventType) ([]notificationservice.TestResult, error) {
	return nil, nil
}

func TestUpdateNotificationChannelMapsSaveTimeNotFound(t *testing.T) {
	t.Parallel()

	handler := localapi.New(
		nil,
		nil,
		nil,
		nil,
		runtime.Manager{},
		&config.Config{},
		nil,
		nil,
		prometheus.NewRegistry(),
		nil,
		localapi.WithNotificationService(notificationServiceStub{
			getChannel: func(context.Context, string) (*notificationmodel.Channel, error) {
				return &notificationmodel.Channel{
					ID:      "channel-1",
					Name:    "Ops Webhook",
					Type:    notificationmodel.ProviderWebhook,
					Enabled: true,
					Webhook: &notificationmodel.WebhookTarget{URL: "https://example.com/webhook"},
				}, nil
			},
			saveChannel: func(context.Context, *notificationmodel.Channel, string) (*notificationmodel.Channel, error) {
				return nil, notificationmodel.ErrChannelNotFound
			},
		}),
		localapi.WithLicenseManager(license.NewTestManager()),
	)

	webhookURL := "https://example.com/webhook"

	_, err := handler.UpdateNotificationChannel(context.Background(), openapi.UpdateNotificationChannelRequestObject{
		ChannelId: "channel-1",
		Body: &openapi.UpdateNotificationChannelJSONRequestBody{
			Name:    "Ops Webhook",
			Type:    openapi.NotificationProviderTypeWebhook,
			Enabled: true,
			Webhook: &openapi.NotificationWebhookTargetInput{
				Url: &webhookURL,
			},
		},
	})

	var apiErr *localapi.Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.HTTPStatus)
}
