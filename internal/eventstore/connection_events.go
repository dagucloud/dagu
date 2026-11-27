// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const connectionChangeDataKey = "connection_change"

var errNoNotificationReader = errors.New("eventstore: notification reader is not configured")

// ConnectionChange is what a connection event reports. It holds no secret.
type ConnectionChange struct {
	// AppID and AppName are set when the OAuth app's status changed.
	AppID   string `json:"app_id,omitempty"`
	AppName string `json:"app_name,omitempty"`
	// Connections are the connection whose status changed, or the app's
	// connections.
	Connections []ConnectionName `json:"connections"`
	// Status is the new status of the connection or the app.
	Status string `json:"status"`
	// Reason is the provider's error, set for TypeConnectionNeedsAttention.
	Reason string `json:"reason,omitempty"`
}

// ConnectionName names a connection.
type ConnectionName struct {
	// Workspace is empty for a connection of DAGs without a workspace
	// label.
	Workspace string `json:"workspace,omitempty"`
	Name      string `json:"name"`
}

// NotificationReader reads the events that notifications report: DAG-run
// events and connection events, in one pass over the store.
type NotificationReader interface {
	NotificationHeadCursor(ctx context.Context) (DAGRunCursor, error)
	ReadNotificationEvents(ctx context.Context, cursor DAGRunCursor) ([]*Event, DAGRunCursor, error)
}

// isConnectionEventType reports whether kind and eventType are those of a
// connection event.
func isConnectionEventType(kind EventKind, eventType EventType) bool {
	return kind == KindConnection &&
		(eventType == TypeConnectionNeedsAttention || eventType == TypeConnectionRestored)
}

// IsNotificationEventType reports whether a NotificationReader returns
// events of kind and eventType.
func IsNotificationEventType(kind EventKind, eventType EventType) bool {
	return IsDAGRunEventType(kind, eventType) || isConnectionEventType(kind, eventType)
}

// NewConnectionEvent returns the event of one status change. Each call
// returns an event with a new ID.
func NewConnectionEvent(source Source, eventType EventType, change ConnectionChange, occurredAt time.Time) *Event {
	source = normalizeSource(source)
	recordedAt := time.Now().UTC()
	subject := change.AppID
	if subject == "" && len(change.Connections) > 0 {
		subject = change.Connections[0].Workspace + "/" + change.Connections[0].Name
	}
	event := &Event{
		ID:             "connection_" + stableID(string(eventType), subject, strconv.FormatInt(recordedAt.UnixNano(), 10), source.Instance),
		SchemaVersion:  SchemaVersion,
		OccurredAt:     occurredAt,
		RecordedAt:     recordedAt,
		Kind:           KindConnection,
		Type:           eventType,
		SourceService:  source.Service,
		SourceInstance: source.Instance,
		Status:         change.Status,
		Data:           map[string]any{connectionChangeDataKey: change},
	}
	event.Normalize()
	return event
}

// ConnectionChangeFromEvent returns the change that a connection event
// reports.
func ConnectionChangeFromEvent(event *Event) (*ConnectionChange, error) {
	if event == nil {
		return nil, errors.New("eventstore: event is nil")
	}
	if !isConnectionEventType(event.Kind, event.Type) {
		return nil, fmt.Errorf("eventstore: event %q is not a connection event", event.Type)
	}
	raw, ok := event.Data[connectionChangeDataKey]
	if !ok {
		return nil, errors.New("eventstore: connection change is missing")
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("eventstore: marshal connection change: %w", err)
	}
	var change ConnectionChange
	if err := json.Unmarshal(payload, &change); err != nil {
		return nil, fmt.Errorf("eventstore: unmarshal connection change: %w", err)
	}
	if len(change.Connections) == 0 && change.AppID == "" {
		return nil, errors.New("eventstore: connection change names no connection or app")
	}
	return &change, nil
}

// NotificationHeadCursor returns the cursor after the last event that
// ReadNotificationEvents would return.
func (s *Service) NotificationHeadCursor(ctx context.Context) (DAGRunCursor, error) {
	if s == nil || s.store == nil {
		return DAGRunCursor{}, errors.New("eventstore: store is not configured")
	}
	reader, ok := s.store.(NotificationReader)
	if !ok {
		return DAGRunCursor{}, errNoNotificationReader
	}
	cursor, err := reader.NotificationHeadCursor(ctx)
	if err != nil {
		return DAGRunCursor{}, err
	}
	return cursor.Normalize(), nil
}

// ReadNotificationEvents returns the DAG-run and connection events after
// cursor, and the cursor after them.
func (s *Service) ReadNotificationEvents(ctx context.Context, cursor DAGRunCursor) ([]*Event, DAGRunCursor, error) {
	if s == nil || s.store == nil {
		return nil, DAGRunCursor{}, errors.New("eventstore: store is not configured")
	}
	reader, ok := s.store.(NotificationReader)
	if !ok {
		return nil, DAGRunCursor{}, errNoNotificationReader
	}
	events, nextCursor, err := reader.ReadNotificationEvents(ctx, cursor.Normalize())
	if err != nil {
		return nil, DAGRunCursor{}, err
	}
	return events, nextCursor.Normalize(), nil
}
