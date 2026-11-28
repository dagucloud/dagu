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
	"github.com/dagucloud/dagu/v2/internal/ir"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
)

// An app's event reaches the routes of each workspace with a connection on
// the app, and a channel that several of those routes reach gets one
// plain-text message, whatever template the channel sets. Each channel
// learns only the connections of the workspaces that route to it, and a
// route saved without events receives no connection event.
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
	saveRoutes("qa",
		notificationmodel.Route{ID: "qa-hook", ChannelID: "ops-hook", Enabled: true},
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
				{Workspace: "qa", Name: "tests"},
			},
			Status: "invalid",
			Reason: "invalid_client",
		},
		ObservedAt: time.Now(),
	}
	destinations := svc.NotificationDestinationsForEvent(event)
	assert.ElementsMatch(t, []string{
		routeDestinationID(notificationmodel.RouteScopeWorkspace, "dev", "dev-shared"),
		routeDestinationID(notificationmodel.RouteScopeWorkspace, "ops", "ops-hook"),
	}, destinations)
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
	assert.Equal(t, `OAuth app "Google" was refused by its provider. Until an admin changes its client ID or secret, its connections cannot get tokens: work-google (ops).
Reason: invalid_client
Fix it: https://dagu.example.com/connections
`, hook.Message)
	require.Len(t, hook.Events, 1)
	assert.Equal(t, "connection.needs_attention", hook.Events[0]["eventType"])
	assert.Equal(t, map[string]any{"id": "app-1", "name": "Google"}, hook.Events[0]["app"])
}

// A batch lists its events newest first. Connection events go out as they
// happened, so the channel's last message tells the current status.
func TestConnectionEventsOldestFirst(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "slack",
		Name:    "Slack",
		Type:    notificationmodel.ProviderSlack,
		Enabled: true,
		Slack:   &notificationmodel.SlackTarget{WebhookURL: "https://93.184.216.34/slack"},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.SaveChannel(context.Background(), channel))

	var texts []string
	svc := New(store, nil,
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var body map[string]string
			require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
			texts = append(texts, body["text"])
			return acceptedResponse(req), nil
		})}),
	)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:   notificationmodel.RouteScopeGlobal,
		Enabled: true,
		Routes: []notificationmodel.Route{{
			ID: "route", ChannelID: "slack", Enabled: true,
			Events: []eventstore.EventType{eventstore.TypeConnectionNeedsAttention, eventstore.TypeConnectionRestored},
		}},
	}, "tester")
	require.NoError(t, err)

	work := []eventstore.ConnectionName{{Name: "work"}}
	now := time.Now()
	broken := chatbridge.NotificationEvent{
		Key: "connection_a", Type: eventstore.TypeConnectionNeedsAttention, ObservedAt: now.Add(-5 * time.Second),
		Connection: &eventstore.ConnectionChange{Connections: work, Status: "needs_reconnect", Reason: "invalid_grant"},
	}
	restored := chatbridge.NotificationEvent{
		Key: "connection_b", Type: eventstore.TypeConnectionRestored, ObservedAt: now,
		Connection: &eventstore.ConnectionChange{Connections: work, Status: "connected"},
	}
	batch := chatbridge.NotificationBatch{
		Class:  chatbridge.NotificationClassUrgent,
		Events: []chatbridge.NotificationEvent{restored, broken},
	}
	require.True(t, svc.FlushNotificationBatch(context.Background(),
		routeDestinationID(notificationmodel.RouteScopeGlobal, "", "route"), batch, false))

	require.Len(t, texts, 2)
	assert.Contains(t, texts[0], "Connection work needs to be reconnected.")
	assert.Equal(t, "Connection work is connected again.\n", texts[1])
}

// A channel that receives connection.needs_attention sends no failure
// message for a run that a broken connection failed; it already got the
// connection's message. A channel without connection events, and any run
// with another cause, still get the failure message. The decision reads the
// run's record alone, so it holds for a run read after a reconnect.
func TestRunFailureSkippedForBrokenConnection(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	for _, id := range []string{"attention", "runs-only"} {
		channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
			ID:      id,
			Name:    id,
			Type:    notificationmodel.ProviderSlack,
			Enabled: true,
			Slack:   &notificationmodel.SlackTarget{WebhookURL: "https://93.184.216.34/" + id},
		}, "tester")
		require.NoError(t, err)
		require.NoError(t, store.SaveChannel(context.Background(), channel))
	}
	svc := New(store, nil)
	_, err := svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:     notificationmodel.RouteScopeWorkspace,
		Workspace: "ops",
		Enabled:   true,
		Routes: []notificationmodel.Route{
			{
				ID: "attention", ChannelID: "attention", Enabled: true,
				Events: []eventstore.EventType{eventstore.TypeDAGRunFailed, eventstore.TypeConnectionNeedsAttention},
			},
			{ID: "runs-only", ChannelID: "runs-only", Enabled: true, Events: []eventstore.EventType{eventstore.TypeDAGRunFailed}},
		},
	}, "tester")
	require.NoError(t, err)

	failedOn := func(failure ir.ConnectionFailure) *ir.Node {
		return &ir.Node{
			Step:       ir.Step{Name: "unread"},
			Status:     ir.NodeFailed,
			Connection: &ir.NodeConnection{Name: "work-google", Failure: failure},
		}
	}
	runsOnly := routeDestinationID(notificationmodel.RouteScopeWorkspace, "ops", "runs-only")
	attention := routeDestinationID(notificationmodel.RouteScopeWorkspace, "ops", "attention")
	tests := []struct {
		name  string
		nodes []*ir.Node
		want  []string
	}{
		{name: "BrokenConnection", nodes: []*ir.Node{failedOn(ir.ConnectionNeedsAttention)}, want: []string{runsOnly}},
		{name: "PendingConnection", nodes: []*ir.Node{failedOn(ir.ConnectionReconnectRequired)}, want: []string{attention, runsOnly}},
		{
			name: "OtherFailedStep",
			nodes: []*ir.Node{
				failedOn(ir.ConnectionNeedsAttention),
				{Step: ir.Step{Name: "local"}, Status: ir.NodeFailed, Error: "exit status 1"},
			},
			want: []string{attention, runsOnly},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			event := chatbridge.NotificationEvent{
				Key:  "run-1",
				Type: eventstore.TypeDAGRunFailed,
				Status: &ir.DAGRunStatus{
					Name: "mail-dag", DAGRunID: "run-1", AttemptID: "attempt-1",
					Labels: []string{"workspace=ops"}, Status: ir.Failed, Nodes: tt.nodes,
				},
				ObservedAt: time.Now(),
			}
			assert.Equal(t, tt.want, svc.NotificationDestinationsForEvent(event))
		})
	}
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
		want:      `OAuth app "Google" has a new client ID or secret. Its connections try it on their next token request: work-google (ops).`,
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
