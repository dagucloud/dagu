// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package notification

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/eventstore"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
)

// An app's event reaches the routes of each workspace with a connection on
// the app, and a channel that several of those routes reach gets one
// plain-text message, whatever template the channel sets.
func TestConnectionEventOneMessagePerChannel(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	for _, channel := range []*notificationmodel.Channel{{
		ID:      "shared",
		Name:    "Shared Slack",
		Type:    notificationmodel.ProviderSlack,
		Enabled: true,
		Slack: &notificationmodel.SlackTarget{
			WebhookURL:      "https://93.184.216.34/slack",
			MessageTemplate: "DAG {{dag.name}} {{run.status}}",
		},
	}, {
		ID:      "ops-hook",
		Name:    "Ops webhook",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{URL: "https://93.184.216.34/hook"},
	}} {
		normalized, err := notificationmodel.NormalizeChannel(channel, "tester")
		require.NoError(t, err)
		require.NoError(t, store.SaveChannel(context.Background(), normalized))
	}

	var (
		mu       sync.Mutex
		requests = map[string][]string{}
	)
	svc := New(store, nil,
		WithPublicURL("https://dagu.example.com"),
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			mu.Lock()
			requests[req.URL.Path] = append(requests[req.URL.Path], string(body))
			mu.Unlock()
			return acceptedResponse(req), nil
		})}),
	)
	connectionEvents := []eventstore.EventType{eventstore.TypeConnectionNeedsAttention, eventstore.TypeConnectionRestored}
	saveRoutes := func(workspace string, routes ...notificationmodel.Route) {
		_, err := svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
			Scope:     notificationmodel.RouteScopeWorkspace,
			Workspace: workspace,
			Enabled:   true,
			Routes:    routes,
		}, "tester")
		require.NoError(t, err)
	}
	saveRoutes("ops",
		notificationmodel.Route{ID: "ops-shared", ChannelID: "shared", Enabled: true, Events: connectionEvents},
		notificationmodel.Route{ID: "ops-hook", ChannelID: "ops-hook", Enabled: true, Events: connectionEvents},
	)
	saveRoutes("dev",
		notificationmodel.Route{ID: "dev-shared", ChannelID: "shared", Enabled: true, Events: connectionEvents},
	)

	event := chatbridge.NotificationEvent{
		Key:  "connection_1",
		Type: eventstore.TypeConnectionNeedsAttention,
		Connection: &eventstore.ConnectionChange{
			AppID:   "app-1",
			AppName: "Google",
			Connections: []eventstore.ConnectionName{
				{Workspace: "ops", Name: "work-google"},
				{Workspace: "dev", Name: "calendar"},
			},
			Status: "invalid",
			Reason: "invalid_client",
		},
		ObservedAt: time.Now(),
	}
	destinations := svc.NotificationDestinationsForEvent(event)
	require.Len(t, destinations, 2)
	batch := chatbridge.NotificationBatch{Class: chatbridge.NotificationClassUrgent, Events: []chatbridge.NotificationEvent{event}}
	for _, destination := range destinations {
		var delivered []string
		require.True(t, svc.FlushNotificationBatchWithProgress(context.Background(), destination, batch, false,
			func(e chatbridge.NotificationEvent) { delivered = append(delivered, e.Key) }))
		assert.Equal(t, []string{"connection_1"}, delivered)
	}

	require.Len(t, requests["/slack"], 1)
	var slack map[string]string
	require.NoError(t, json.Unmarshal([]byte(requests["/slack"][0]), &slack))
	assert.Equal(t, `OAuth app "Google" was refused by its provider. Until an admin changes its client ID or secret, its connections cannot get tokens: work-google (ops), calendar (dev).
Reason: invalid_client
Fix it: https://dagu.example.com/connections
`, slack["text"])

	require.Len(t, requests["/hook"], 1)
	var hook struct {
		Message string           `json:"message"`
		Events  []map[string]any `json:"events"`
	}
	require.NoError(t, json.Unmarshal([]byte(requests["/hook"][0]), &hook))
	assert.Equal(t, slack["text"], hook.Message)
	require.Len(t, hook.Events, 1)
	assert.Equal(t, "connection.needs_attention", hook.Events[0]["eventType"])
	assert.Equal(t, map[string]any{"id": "app-1", "name": "Google"}, hook.Events[0]["app"])
}

func TestConnectionText(t *testing.T) {
	t.Parallel()

	ops := []eventstore.ConnectionName{{Workspace: "ops", Name: "work-google"}}
	tests := []struct {
		name      string
		eventType eventstore.EventType
		change    eventstore.ConnectionChange
		publicURL string
		want      string
	}{{
		name:      "NeedsReconnect",
		eventType: eventstore.TypeConnectionNeedsAttention,
		change:    eventstore.ConnectionChange{Connections: ops, Status: "needs_reconnect", Reason: "invalid_grant: Token has been expired or revoked."},
		publicURL: "https://dagu.example.com/base/",
		want: `Connection work-google (ops) needs to be reconnected.
Reason: invalid_grant: Token has been expired or revoked.
Reconnect: https://dagu.example.com/base/connections/work-google?workspace=ops`,
	}, {
		name:      "NoPublicURL",
		eventType: eventstore.TypeConnectionNeedsAttention,
		change:    eventstore.ConnectionChange{Connections: ops, Status: "needs_reconnect", Reason: "invalid_grant"},
		want: `Connection work-google (ops) needs to be reconnected.
Reason: invalid_grant
Reconnect it on the Connections page, or run: dagu connection reconnect work-google --workspace ops`,
	}, {
		name:      "GlobalNoPublicURL",
		eventType: eventstore.TypeConnectionNeedsAttention,
		change:    eventstore.ConnectionChange{Connections: []eventstore.ConnectionName{{Name: "shared"}}, Status: "needs_reconnect"},
		want: `Connection shared needs to be reconnected.
Reconnect it on the Connections page, or run: dagu connection reconnect shared`,
	}, {
		name:      "Restored",
		eventType: eventstore.TypeConnectionRestored,
		change:    eventstore.ConnectionChange{Connections: ops, Status: "connected"},
		publicURL: "https://dagu.example.com",
		want:      `Connection work-google (ops) is connected again.`,
	}, {
		name:      "AppRestored",
		eventType: eventstore.TypeConnectionRestored,
		change:    eventstore.ConnectionChange{AppID: "app-1", AppName: "Google", Connections: ops, Status: "ok"},
		want:      `OAuth app "Google" works again. Its connections can get tokens: work-google (ops).`,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			change := tt.change
			event := chatbridge.NotificationEvent{Type: tt.eventType, Connection: &change}
			assert.Equal(t, tt.want, connectionText(event, tt.publicURL))
		})
	}
}
