// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
	"github.com/dagucloud/dagu/internal/service/eventstore"
)

var _ eventstore.Store = (*eventStore)(nil)

const postgresEventCursorVersion = 1

type eventStore struct{ store *Store }

type postgresEventCursor struct {
	Version    int    `json:"v"`
	FilterHash string `json:"f"`
	OccurredAt string `json:"o"`
	RecordedAt string `json:"r"`
	EventID    string `json:"e"`
}

// Events returns the event store.
func (s *Store) Events() eventstore.Store {
	return &eventStore{store: s}
}

func (s *eventStore) Emit(ctx context.Context, event *eventstore.Event) error {
	if event == nil {
		return fmt.Errorf("postgres event store: event cannot be nil")
	}
	event.Normalize()
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate event row id: %w", err)
	}
	eventData, err := eventDataJSON(event.Data)
	if err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return s.store.queries.EmitEvent(ctx, db.EmitEventParams{
		ID:             id,
		EventID:        event.ID,
		SchemaVersion:  int32(event.SchemaVersion), //nolint:gosec
		OccurredAt:     timestamptz(event.OccurredAt),
		RecordedAt:     timestamptz(event.RecordedAt),
		Kind:           string(event.Kind),
		EventType:      string(event.Type),
		SourceService:  event.SourceService,
		SourceInstance: event.SourceInstance,
		DagName:        event.DAGName,
		DagRunID:       event.DAGRunID,
		AttemptID:      event.AttemptID,
		SessionID:      event.SessionID,
		UserID:         event.UserID,
		Model:          event.Model,
		Status:         event.Status,
		EventData:      eventData,
		Data:           data,
	})
}

func (s *eventStore) Query(ctx context.Context, filter eventstore.QueryFilter) (*eventstore.QueryResult, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if filter.PaginationMode == eventstore.QueryPaginationModeCursor || filter.Cursor != "" {
		return s.queryCursor(ctx, filter, limit)
	}
	return s.queryOffset(ctx, filter, limit)
}

func (s *eventStore) queryOffset(ctx context.Context, filter eventstore.QueryFilter, limit int) (*eventstore.QueryResult, error) {
	offset := max(filter.Offset, 0)
	params := eventQueryParams(filter, limit, offset)
	rows, err := s.store.queries.QueryEvents(ctx, params)
	if err != nil {
		return nil, err
	}
	total, err := s.store.queries.CountEvents(ctx, eventCountParams(params))
	if err != nil {
		return nil, err
	}
	entries, err := eventsFromRows(rows)
	if err != nil {
		return nil, err
	}
	totalInt := int(total)
	return &eventstore.QueryResult{Entries: entries, Total: &totalInt}, nil
}

func (s *eventStore) queryCursor(ctx context.Context, filter eventstore.QueryFilter, limit int) (*eventstore.QueryResult, error) {
	cursor, err := decodePostgresEventCursor(filter)
	if err != nil {
		return nil, err
	}
	params := eventCursorQueryParams(filter, cursor, limit+1)
	rows, err := s.store.queries.QueryEventsCursor(ctx, params)
	if err != nil {
		return nil, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	entries, err := eventsFromRows(rows)
	if err != nil {
		return nil, err
	}
	result := &eventstore.QueryResult{Entries: entries}
	if hasMore && len(rows) > 0 {
		nextCursor, err := encodePostgresEventCursor(filter, rows[len(rows)-1])
		if err != nil {
			return nil, err
		}
		result.NextCursor = nextCursor
	}
	return result, nil
}

func eventQueryParams(filter eventstore.QueryFilter, limit, offset int) db.QueryEventsParams {
	return db.QueryEventsParams{
		HasKind:      filter.Kind != "",
		Kind:         string(filter.Kind),
		HasType:      filter.Type != "",
		EventType:    string(filter.Type),
		HasDagName:   filter.DAGName != "",
		DagName:      nullableText(filter.DAGName),
		HasDagRunID:  filter.DAGRunID != "",
		DagRunID:     nullableText(filter.DAGRunID),
		HasAttemptID: filter.AttemptID != "",
		AttemptID:    nullableText(filter.AttemptID),
		HasSessionID: filter.SessionID != "",
		SessionID:    nullableText(filter.SessionID),
		HasUserID:    filter.UserID != "",
		UserID:       nullableText(filter.UserID),
		HasModel:     filter.Model != "",
		Model:        nullableText(filter.Model),
		HasStatus:    filter.Status != "",
		Status:       nullableText(filter.Status),
		HasStartTime: !filter.StartTime.IsZero(),
		StartTime:    timestamptz(filter.StartTime),
		HasEndTime:   !filter.EndTime.IsZero(),
		EndTime:      timestamptz(filter.EndTime),
		RowOffset:    int32(offset), //nolint:gosec
		RowLimit:     int32(limit),  //nolint:gosec
	}
}

func eventCountParams(params db.QueryEventsParams) db.CountEventsParams {
	return db.CountEventsParams{
		HasKind:      params.HasKind,
		Kind:         params.Kind,
		HasType:      params.HasType,
		EventType:    params.EventType,
		HasDagName:   params.HasDagName,
		DagName:      params.DagName,
		HasDagRunID:  params.HasDagRunID,
		DagRunID:     params.DagRunID,
		HasAttemptID: params.HasAttemptID,
		AttemptID:    params.AttemptID,
		HasSessionID: params.HasSessionID,
		SessionID:    params.SessionID,
		HasUserID:    params.HasUserID,
		UserID:       params.UserID,
		HasModel:     params.HasModel,
		Model:        params.Model,
		HasStatus:    params.HasStatus,
		Status:       params.Status,
		HasStartTime: params.HasStartTime,
		StartTime:    params.StartTime,
		HasEndTime:   params.HasEndTime,
		EndTime:      params.EndTime,
	}
}

func eventCursorQueryParams(filter eventstore.QueryFilter, cursor postgresEventCursor, limit int) db.QueryEventsCursorParams {
	params := eventQueryParams(filter, limit, 0)
	return db.QueryEventsCursorParams{
		HasKind:         params.HasKind,
		Kind:            params.Kind,
		HasType:         params.HasType,
		EventType:       params.EventType,
		HasDagName:      params.HasDagName,
		DagName:         params.DagName,
		HasDagRunID:     params.HasDagRunID,
		DagRunID:        params.DagRunID,
		HasAttemptID:    params.HasAttemptID,
		AttemptID:       params.AttemptID,
		HasSessionID:    params.HasSessionID,
		SessionID:       params.SessionID,
		HasUserID:       params.HasUserID,
		UserID:          params.UserID,
		HasModel:        params.HasModel,
		Model:           params.Model,
		HasStatus:       params.HasStatus,
		Status:          params.Status,
		HasStartTime:    params.HasStartTime,
		StartTime:       params.StartTime,
		HasEndTime:      params.HasEndTime,
		EndTime:         params.EndTime,
		HasCursor:       cursor.EventID != "",
		AfterOccurredAt: timestamptz(cursor.occurredAt()),
		AfterRecordedAt: timestamptz(cursor.recordedAt()),
		AfterEventID:    cursor.EventID,
		RowLimit:        int32(limit), //nolint:gosec
	}
}

func eventsFromRows(rows []db.DaguEvent) ([]*eventstore.Event, error) {
	entries := make([]*eventstore.Event, 0, len(rows))
	for _, row := range rows {
		event, err := eventFromRow(row)
		if err != nil {
			return nil, err
		}
		entries = append(entries, event)
	}
	return entries, nil
}

func eventFromRow(row db.DaguEvent) (*eventstore.Event, error) {
	var event eventstore.Event
	if err := json.Unmarshal(row.Data, &event); err != nil {
		return nil, fmt.Errorf("unmarshal event: %w", err)
	}
	event.ID = row.EventID
	event.SchemaVersion = int(row.SchemaVersion)
	event.OccurredAt = timeFromTimestamptz(row.OccurredAt)
	event.RecordedAt = timeFromTimestamptz(row.RecordedAt)
	event.Kind = eventstore.EventKind(row.Kind)
	event.Type = eventstore.EventType(row.EventType)
	event.SourceService = row.SourceService
	event.SourceInstance = row.SourceInstance.String
	event.DAGName = row.DagName.String
	event.DAGRunID = row.DagRunID.String
	event.AttemptID = row.AttemptID.String
	event.SessionID = row.SessionID.String
	event.UserID = row.UserID.String
	event.Model = row.Model.String
	event.Status = row.Status.String
	if len(row.EventData) > 0 {
		if err := json.Unmarshal(row.EventData, &event.Data); err != nil {
			return nil, fmt.Errorf("unmarshal event data: %w", err)
		}
	}
	return &event, nil
}

func eventDataJSON(data map[string]any) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	out, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal event data: %w", err)
	}
	return out, nil
}

func encodePostgresEventCursor(filter eventstore.QueryFilter, row db.DaguEvent) (string, error) {
	payload := postgresEventCursor{
		Version:    postgresEventCursorVersion,
		FilterHash: postgresEventFilterHash(filter),
		OccurredAt: timeFromTimestamptz(row.OccurredAt).UTC().Format(time.RFC3339Nano),
		RecordedAt: timeFromTimestamptz(row.RecordedAt).UTC().Format(time.RFC3339Nano),
		EventID:    row.EventID,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("postgres event store: marshal cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodePostgresEventCursor(filter eventstore.QueryFilter) (postgresEventCursor, error) {
	if filter.Cursor == "" {
		return postgresEventCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
	if err != nil {
		return postgresEventCursor{}, invalidPostgresEventCursor("decode cursor")
	}
	var cursor postgresEventCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return postgresEventCursor{}, invalidPostgresEventCursor("parse cursor")
	}
	if cursor.Version != postgresEventCursorVersion || cursor.EventID == "" || cursor.OccurredAt == "" || cursor.RecordedAt == "" {
		return postgresEventCursor{}, invalidPostgresEventCursor("cursor is incomplete")
	}
	if cursor.FilterHash != postgresEventFilterHash(filter) {
		return postgresEventCursor{}, invalidPostgresEventCursor("cursor does not match the current filters")
	}
	if cursor.occurredAt().IsZero() || cursor.recordedAt().IsZero() {
		return postgresEventCursor{}, invalidPostgresEventCursor("cursor time is invalid")
	}
	return cursor, nil
}

func (c postgresEventCursor) occurredAt() time.Time {
	t, _ := time.Parse(time.RFC3339Nano, c.OccurredAt)
	return t.UTC()
}

func (c postgresEventCursor) recordedAt() time.Time {
	t, _ := time.Parse(time.RFC3339Nano, c.RecordedAt)
	return t.UTC()
}

func postgresEventFilterHash(filter eventstore.QueryFilter) string {
	normalized := struct {
		Kind      string `json:"kind,omitempty"`
		Type      string `json:"type,omitempty"`
		DAGName   string `json:"dag_name,omitempty"`
		DAGRunID  string `json:"dag_run_id,omitempty"`
		AttemptID string `json:"attempt_id,omitempty"`
		SessionID string `json:"session_id,omitempty"`
		UserID    string `json:"user_id,omitempty"`
		Model     string `json:"model,omitempty"`
		Status    string `json:"status,omitempty"`
		StartTime string `json:"start_time,omitempty"`
		EndTime   string `json:"end_time,omitempty"`
	}{
		Kind:      string(filter.Kind),
		Type:      string(filter.Type),
		DAGName:   filter.DAGName,
		DAGRunID:  filter.DAGRunID,
		AttemptID: filter.AttemptID,
		SessionID: filter.SessionID,
		UserID:    filter.UserID,
		Model:     filter.Model,
		Status:    filter.Status,
	}
	if !filter.StartTime.IsZero() {
		normalized.StartTime = filter.StartTime.UTC().Format(time.RFC3339Nano)
	}
	if !filter.EndTime.IsZero() {
		normalized.EndTime = filter.EndTime.UTC().Format(time.RFC3339Nano)
	}
	data, _ := json.Marshal(normalized)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func invalidPostgresEventCursor(reason string) error {
	return fmt.Errorf("%w: %s", eventstore.ErrInvalidQueryCursor, reason)
}
