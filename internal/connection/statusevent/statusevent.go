// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package statusevent records the status changes of connections and their
// OAuth apps. The process whose write changed a status records it, once:
//
//	broker or connect engine write -> StatusChange -> Recorder.Record
//	                                                   |-> connection event
//	                                                   '-> audit entry
//
// The notification monitor reads the events and sends their messages.
package statusevent

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

// Audit actions of status changes.
const (
	actionConnectionStatus = "connection_status_change"
	actionAppStatus        = "oauth_app_status_change"
)

// globalWorkspace names the connections of DAGs without a workspace label
// in audit entries, as the REST API does.
const globalWorkspace = "global"

// Config is what a Recorder needs. Store and Audit open their stores on
// each change, so a process that changes no status opens neither.
type Config struct {
	// Store opens the connection store, which names an app and its
	// connections.
	Store func() (connection.Store, error)
	// Events receives the connection events. Nil records none.
	Events *eventstore.Service
	// Audit opens the audit log. Nil writes no audit entry.
	Audit  func() (audit.Store, error)
	Source eventstore.Source
}

// Recorder records each status change as one connection event and one
// audit entry.
type Recorder struct {
	cfg Config
}

// New returns a recorder over cfg.
func New(cfg Config) *Recorder {
	return &Recorder{cfg: cfg}
}

// Record records change. A change that no event type reports, such as a
// first connect, is audited only. Failures are logged, since the status
// has already changed.
func (r *Recorder) Record(ctx context.Context, change broker.StatusChange) {
	occurredAt := time.Now().UTC()
	eventType, reported := eventTypeOf(change)
	reported = reported && r.cfg.Events != nil
	if !reported && r.cfg.Audit == nil {
		return
	}
	eventChange, err := r.eventChange(ctx, change)
	if err != nil {
		logger.Warn(ctx, "Failed to read a connection status change", tag.Error(err))
		return
	}
	if reported {
		event := eventstore.NewConnectionEvent(r.cfg.Source, eventType, *eventChange, occurredAt)
		if err := r.cfg.Events.Emit(ctx, event); err != nil {
			logger.Warn(ctx, "Failed to record a connection event", tag.Error(err), slog.String("type", string(eventType)))
		}
	}
	if r.cfg.Audit != nil {
		if err := r.audit(ctx, change, eventChange.AppName); err != nil {
			logger.Warn(ctx, "Failed to audit a connection status change", tag.Error(err))
		}
	}
}

// eventTypeOf returns the event type that reports change.
func eventTypeOf(change broker.StatusChange) (eventstore.EventType, bool) {
	switch {
	case change.ConnectionTo == connection.TokenStatusNeedsReconnect,
		change.AppTo == connection.AppStatusInvalid:
		return eventstore.TypeConnectionNeedsAttention, true
	case change.ConnectionFrom == connection.TokenStatusNeedsReconnect && change.ConnectionTo == connection.TokenStatusConnected,
		change.AppFrom == connection.AppStatusInvalid && change.AppTo == connection.AppStatusOK:
		return eventstore.TypeConnectionRestored, true
	default:
		return "", false
	}
}

// eventChange returns what the event of change carries: the connection,
// or the app and its connections.
func (r *Recorder) eventChange(ctx context.Context, change broker.StatusChange) (*eventstore.ConnectionChange, error) {
	if change.AppTo == "" {
		return &eventstore.ConnectionChange{
			Connections: []eventstore.ConnectionName{connectionName(change.Connection)},
			Status:      string(change.ConnectionTo),
			Reason:      change.Reason,
		}, nil
	}
	store, err := r.cfg.Store()
	if err != nil {
		return nil, err
	}
	app, err := store.GetApp(ctx, change.App)
	if err != nil {
		return nil, err
	}
	conns, err := store.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	result := &eventstore.ConnectionChange{
		AppID:       app.ID,
		AppName:     app.Name,
		Connections: []eventstore.ConnectionName{},
		Status:      string(change.AppTo),
		Reason:      change.Reason,
	}
	for _, conn := range conns {
		if conn.App == app.ID {
			result.Connections = append(result.Connections, connectionName(conn.Key()))
		}
	}
	return result, nil
}

func (r *Recorder) audit(ctx context.Context, change broker.StatusChange, appName string) error {
	store, err := r.cfg.Audit()
	if err != nil {
		return err
	}
	details := map[string]any{"app": change.App}
	// An app's change names the connection whose refresh found it, if any.
	if change.ConnectionID != "" {
		details["workspace"] = auditWorkspace(change.Connection.Workspace)
		details["name"] = change.Connection.Name
		details["id"] = change.ConnectionID
	}
	action, resourceType, resourceID := actionConnectionStatus, "connection", change.ConnectionID
	if change.AppTo != "" {
		action, resourceType, resourceID = actionAppStatus, "oauth_app", change.App
		details["app_name"] = appName
		details["from"], details["to"] = change.AppFrom, change.AppTo
	} else {
		details["from"], details["to"] = change.ConnectionFrom, change.ConnectionTo
	}
	if change.Reason != "" {
		details["reason"] = change.Reason
	}
	data, err := json.Marshal(details)
	if err != nil {
		return err
	}
	entry := audit.NewEntry(audit.CategoryConnection, action, "", "").WithDetails(string(data))
	entry.Source = r.cfg.Source.Service
	entry.ResourceType = resourceType
	entry.ResourceID = resourceID
	return store.Append(ctx, entry)
}

// connectionName names key in an event, where a connection of DAGs
// without a workspace label has no workspace.
func connectionName(key connection.Key) eventstore.ConnectionName {
	ws := key.Workspace
	if ws == secret.GlobalWorkspace {
		ws = ""
	}
	return eventstore.ConnectionName{Workspace: ws, Name: key.Name}
}

func auditWorkspace(ws string) string {
	if ws == secret.GlobalWorkspace {
		return globalWorkspace
	}
	return ws
}
