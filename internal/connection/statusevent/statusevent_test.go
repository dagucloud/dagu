// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package statusevent_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/connection/statusevent"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	fileaudit "github.com/dagucloud/dagu/v2/internal/persis/file/audit"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

// Each status change records one event and one audit entry. An app's
// event names every connection on the app.
func TestRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	store, err := file.NewServerStores(&config.Config{Paths: config.PathsConfig{DataDir: dir}}).ConnectionStore()
	require.NoError(t, err)
	newApp := func(name string) *connection.App {
		app, err := store.CreateApp(ctx, &connection.App{
			Name:         name,
			Provider:     connection.ProviderOAuth2,
			Kind:         connection.AppKindBYO,
			ClientID:     name + "-client",
			ClientSecret: "secret",
			ClientType:   connection.ClientTypeWeb,
			Endpoints:    &connection.Endpoints{Token: "https://example.com/token"},
		})
		require.NoError(t, err)
		return app
	}
	newConnection := func(workspace, name, app string) *connection.Connection {
		conn, err := store.Create(ctx, &connection.Connection{
			Workspace: workspace,
			Name:      name,
			Provider:  connection.ProviderOAuth2,
			App:       app,
			Grant:     connection.GrantAuthorizationCode,
		})
		require.NoError(t, err)
		return conn
	}
	google, other := newApp("Google"), newApp("Other")
	work := newConnection("ops", "work", google.ID)
	newConnection(secret.GlobalWorkspace, "shared", google.ID)
	newConnection("ops", "unrelated", other.ID)

	events, err := fileeventstore.New(filepath.Join(dir, "events"))
	require.NoError(t, err)
	eventService := eventstore.New(events)
	auditStore, err := fileaudit.New(filepath.Join(dir, "audit"), 0)
	require.NoError(t, err)
	recorder := statusevent.New(statusevent.Config{
		Store:  func() (connection.Store, error) { return store, nil },
		Events: eventService,
		Audit:  func() (audit.Store, error) { return auditStore, nil },
		Source: eventstore.Source{Service: eventstore.SourceServiceCLI, Instance: "test"},
	})

	recorder.Record(ctx, broker.StatusChange{
		Connection: work.Key(), ConnectionID: work.ID, App: google.ID,
		AppFrom: connection.AppStatusOK, AppTo: connection.AppStatusInvalid,
		Reason: "invalid_client",
	})
	recorder.Record(ctx, broker.StatusChange{
		Connection: work.Key(), ConnectionID: work.ID, App: google.ID,
		ConnectionFrom: connection.TokenStatusNeedsReconnect, ConnectionTo: connection.TokenStatusConnected,
	})

	recorded, _, err := eventService.ReadNotificationEvents(ctx, eventstore.DAGRunCursor{})
	require.NoError(t, err)
	require.Len(t, recorded, 2)
	assert.Equal(t, eventstore.TypeConnectionNeedsAttention, recorded[0].Type)
	appChange, err := eventstore.ConnectionChangeFromEvent(recorded[0])
	require.NoError(t, err)
	assert.Equal(t, google.ID, appChange.AppID)
	assert.Equal(t, "Google", appChange.AppName)
	assert.Equal(t, "invalid", appChange.Status)
	assert.Equal(t, "invalid_client", appChange.Reason)
	assert.ElementsMatch(t, []eventstore.ConnectionName{
		{Workspace: "ops", Name: "work"},
		{Name: "shared"},
	}, appChange.Connections)

	assert.Equal(t, eventstore.TypeConnectionRestored, recorded[1].Type)
	connChange, err := eventstore.ConnectionChangeFromEvent(recorded[1])
	require.NoError(t, err)
	assert.Equal(t, []eventstore.ConnectionName{{Workspace: "ops", Name: "work"}}, connChange.Connections)
	assert.Equal(t, "connected", connChange.Status)

	entries, err := auditStore.Query(ctx, audit.QueryFilter{Category: audit.CategoryConnection})
	require.NoError(t, err)
	require.Len(t, entries.Entries, 2)
	actions := map[string]map[string]any{}
	for _, entry := range entries.Entries {
		var details map[string]any
		require.NoError(t, json.Unmarshal([]byte(entry.Details), &details))
		actions[entry.Action] = details
	}
	assert.Equal(t, "invalid", actions["oauth_app_status_change"]["to"])
	assert.Equal(t, "needs_reconnect", actions["connection_status_change"]["from"])
	assert.Equal(t, "connected", actions["connection_status_change"]["to"])
}
