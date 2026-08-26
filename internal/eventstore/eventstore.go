// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
)

const SchemaVersion = 1

var ErrInvalidQueryCursor = errors.New("eventstore: invalid query cursor")

type EventKind string

const (
	KindDAGRun   EventKind = "dag_run"
	KindLLMUsage EventKind = "llm_usage"
)

type EventType string

const (
	TypeDAGRunQueued             EventType = "dag.run.queued"
	TypeDAGRunRunning            EventType = "dag.run.running"
	TypeDAGRunUpdated            EventType = "dag.run.updated"
	TypeDAGRunWaiting            EventType = "dag.run.waiting"
	TypeDAGRunSucceeded          EventType = "dag.run.succeeded"
	TypeDAGRunPartiallySucceeded EventType = "dag.run.partially_succeeded"
	TypeDAGRunFailed             EventType = "dag.run.failed"
	TypeDAGRunAborted            EventType = "dag.run.aborted"
	TypeDAGRunRejected           EventType = "dag.run.rejected"

	TypeLLMUsageRecorded EventType = "llm.usage.recorded"
)

const (
	SourceServiceCLI         = "cli"
	SourceServiceServer      = "server"
	SourceServiceScheduler   = "scheduler"
	SourceServiceCoordinator = "coordinator"
	SourceServiceUnknown     = "unknown"
)

type Source struct {
	Service  string
	Instance string
}

type Event struct {
	ID             string         `json:"id"`
	SchemaVersion  int            `json:"schema_version"`
	OccurredAt     time.Time      `json:"occurred_at"`
	RecordedAt     time.Time      `json:"recorded_at"`
	Kind           EventKind      `json:"kind"`
	Type           EventType      `json:"type"`
	SourceService  string         `json:"source_service"`
	SourceInstance string         `json:"source_instance,omitempty"`
	DAGName        string         `json:"dag_name,omitempty"`
	DAGRunID       string         `json:"dag_run_id,omitempty"`
	AttemptID      string         `json:"attempt_id,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	UserID         string         `json:"user_id,omitempty"`
	Model          string         `json:"model,omitempty"`
	Status         string         `json:"status,omitempty"`
	Data           map[string]any `json:"data,omitempty"`
}

func (e *Event) Normalize() {
	if e == nil {
		return
	}
	if e.Kind == KindDAGRun &&
		e.Type == TypeDAGRunSucceeded &&
		e.Status == ir.PartiallySucceeded.String() {
		e.Type = TypeDAGRunPartiallySucceeded
	}
	if !e.RecordedAt.IsZero() {
		e.RecordedAt = e.RecordedAt.UTC()
	}
	if !e.OccurredAt.IsZero() {
		e.OccurredAt = e.OccurredAt.UTC()
	}
}

func (e *Event) applyDefaults() {
	if e == nil {
		return
	}
	if e.SchemaVersion == 0 {
		e.SchemaVersion = SchemaVersion
	}
	if e.SourceService == "" {
		e.SourceService = SourceServiceUnknown
	}
	if e.SourceInstance == "" {
		e.SourceInstance = DefaultSourceInstance()
	}
	if e.RecordedAt.IsZero() {
		e.RecordedAt = time.Now().UTC()
	}
}

func (e *Event) Validate() error {
	if e == nil {
		return errors.New("eventstore: event is nil")
	}
	if e.ID == "" {
		return errors.New("eventstore: event id is required")
	}
	if e.SchemaVersion <= 0 {
		return errors.New("eventstore: schema_version must be greater than zero")
	}
	if e.OccurredAt.IsZero() {
		return errors.New("eventstore: occurred_at is required")
	}
	if e.RecordedAt.IsZero() {
		return errors.New("eventstore: recorded_at is required")
	}
	if e.Kind == "" {
		return errors.New("eventstore: kind is required")
	}
	if e.Type == "" {
		return errors.New("eventstore: type is required")
	}
	if e.SourceService == "" {
		return errors.New("eventstore: source_service is required")
	}
	return nil
}

type QueryFilter struct {
	Kind           EventKind
	Type           EventType
	DAGName        string
	DAGRunID       string
	AttemptID      string
	SessionID      string
	UserID         string
	Model          string
	Status         string
	StartTime      time.Time
	EndTime        time.Time
	Limit          int
	Offset         int
	Cursor         string
	PaginationMode QueryPaginationMode
}

type QueryResult struct {
	Entries    []*Event
	Total      *int
	NextCursor string
}

type QueryPaginationMode string

const (
	QueryPaginationModeOffset QueryPaginationMode = "offset"
	QueryPaginationModeCursor QueryPaginationMode = "cursor"
)

type Store interface {
	Emit(ctx context.Context, event *Event) error
	Query(ctx context.Context, filter QueryFilter) (*QueryResult, error)
}

type Service struct {
	store Store
}

func New(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) Emit(ctx context.Context, event *Event) error {
	if s == nil || s.store == nil {
		return nil
	}
	event.applyDefaults()
	event.Normalize()
	if err := event.Validate(); err != nil {
		return err
	}
	return s.store.Emit(ctx, event)
}

func (s *Service) Query(ctx context.Context, filter QueryFilter) (*QueryResult, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("eventstore: store is not configured")
	}
	return s.store.Query(ctx, filter)
}

func DefaultSourceInstance() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	return fmt.Sprintf("%s:%d", hostname, os.Getpid())
}

func DAGRunEventID(eventType EventType, dagName, dagRunID, attemptID string) string {
	return "dag_" + stableID(string(eventType), dagName, dagRunID, attemptID)
}

// DAGRunWaitingEventID identifies a waiting occurrence represented by a status snapshot.
func DAGRunWaitingEventID(status *ir.DAGRunStatus) string {
	if status == nil {
		return ""
	}

	parts := []string{string(TypeDAGRunWaiting), status.Name, status.DAGRunID, status.AttemptID}
	for _, node := range status.NodesInRunOrder() {
		if node == nil || node.Status != ir.NodeWaiting {
			continue
		}

		details := make([]string, 0, len(node.StatusDetails))
		for _, detail := range node.StatusDetails {
			if detail.Status == ir.NodeWaiting {
				details = append(details, detail.Label)
			}
		}
		slices.Sort(details)
		parts = append(parts, stableID(
			node.Step.ID,
			node.Step.Name,
			node.StartedAt,
			strconv.Itoa(node.RetryCount),
			strconv.Itoa(node.DoneCount),
			strconv.Itoa(node.ApprovalIteration),
			stableID(details...),
		))
	}

	return "dag_" + stableID(parts...)
}

func DAGRunUpdateEventID(dagName, dagRunID, attemptID string, recordedAt time.Time) string {
	// Same-status updates are invalidation edges. The file collector deduplicates
	// by event ID globally, so update IDs must remain unique across writes.
	return "dag_update_" + stableID(
		string(TypeDAGRunUpdated),
		dagName,
		dagRunID,
		attemptID,
		recordedAt.UTC().Format(time.RFC3339Nano),
	)
}

func LLMUsageEventID(sessionID, messageID string) string {
	return "llm_" + stableID(string(TypeLLMUsageRecorded), sessionID, messageID)
}

func NewDAGRunEvent(source Source, eventType EventType, status *ir.DAGRunStatus, data map[string]any) *Event {
	if status == nil {
		return nil
	}
	source = normalizeSource(source)
	recordedAt := time.Now().UTC()
	eventID := DAGRunEventID(eventType, status.Name, status.DAGRunID, status.AttemptID)
	if eventType == TypeDAGRunWaiting {
		eventID = DAGRunWaitingEventID(status)
	}
	if eventType == TypeDAGRunUpdated {
		eventID = DAGRunUpdateEventID(status.Name, status.DAGRunID, status.AttemptID, recordedAt)
	}
	occurredAt := dagRunOccurredAt(status, eventType)
	if eventType == TypeDAGRunUpdated {
		occurredAt = recordedAt
	}
	data = cloneData(data)
	if snapshot := newDAGRunStatusSnapshot(status, dagRunFileNameFromData(data)); snapshot != nil {
		if data == nil {
			data = make(map[string]any, 1)
		}
		data[dagRunStatusSnapshotDataKey] = snapshot
	}
	event := &Event{
		ID:             eventID,
		SchemaVersion:  SchemaVersion,
		OccurredAt:     occurredAt,
		RecordedAt:     recordedAt,
		Kind:           KindDAGRun,
		Type:           eventType,
		SourceService:  source.Service,
		SourceInstance: source.Instance,
		DAGName:        status.Name,
		DAGRunID:       status.DAGRunID,
		AttemptID:      status.AttemptID,
		Status:         status.Status.String(),
		Data:           data,
	}
	event.Normalize()
	return event
}

func NewLLMUsageEvent(
	source Source,
	sessionID string,
	userID string,
	model string,
	messageID string,
	createdAt time.Time,
	usage *llm.Usage,
	cost *float64,
) *Event {
	if usage == nil {
		return nil
	}
	source = normalizeSource(source)
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	data := map[string]any{
		"message_id":        messageID,
		"prompt_tokens":     usage.PromptTokens,
		"completion_tokens": usage.CompletionTokens,
		"total_tokens":      usage.TotalTokens,
	}
	if cost != nil {
		data["cost_usd"] = *cost
	}

	event := &Event{
		ID:             LLMUsageEventID(sessionID, messageID),
		SchemaVersion:  SchemaVersion,
		OccurredAt:     createdAt.UTC(),
		RecordedAt:     time.Now().UTC(),
		Kind:           KindLLMUsage,
		Type:           TypeLLMUsageRecorded,
		SourceService:  source.Service,
		SourceInstance: source.Instance,
		SessionID:      sessionID,
		UserID:         userID,
		Model:          model,
		Data:           data,
	}
	event.Normalize()
	return event
}

func PersistedDAGRunEventTypeForStatus(status ir.Status) (EventType, bool) {
	switch status {
	case ir.NotStarted:
		return "", false
	case ir.Queued:
		return TypeDAGRunQueued, true
	case ir.Running:
		return TypeDAGRunRunning, true
	case ir.Waiting:
		return TypeDAGRunWaiting, true
	case ir.Succeeded:
		return TypeDAGRunSucceeded, true
	case ir.PartiallySucceeded:
		return TypeDAGRunPartiallySucceeded, true
	case ir.Failed:
		return TypeDAGRunFailed, true
	case ir.Aborted:
		return TypeDAGRunAborted, true
	case ir.Rejected:
		return TypeDAGRunRejected, true
	default:
		return "", false
	}
}

func normalizeSource(source Source) Source {
	if source.Service == "" {
		source.Service = SourceServiceUnknown
	}
	if source.Instance == "" {
		source.Instance = DefaultSourceInstance()
	}
	return source
}

func dagRunOccurredAt(status *ir.DAGRunStatus, eventType EventType) time.Time {
	if status == nil {
		return time.Now().UTC()
	}
	switch eventType {
	case TypeDAGRunQueued:
		if t, err := stringutil.ParseTime(status.QueuedAt); err == nil && !t.IsZero() {
			return t.UTC()
		}
	case TypeDAGRunRunning:
		if t, err := stringutil.ParseTime(status.StartedAt); err == nil && !t.IsZero() {
			return t.UTC()
		}
		if t, err := stringutil.ParseTime(status.QueuedAt); err == nil && !t.IsZero() {
			return t.UTC()
		}
	case TypeDAGRunUpdated:
		return time.Now().UTC()
	case TypeDAGRunWaiting, TypeDAGRunSucceeded, TypeDAGRunPartiallySucceeded, TypeDAGRunFailed, TypeDAGRunAborted, TypeDAGRunRejected:
		if t, err := stringutil.ParseTime(status.FinishedAt); err == nil && !t.IsZero() {
			return t.UTC()
		}
		if t, err := stringutil.ParseTime(status.StartedAt); err == nil && !t.IsZero() {
			return t.UTC()
		}
		if t, err := stringutil.ParseTime(status.QueuedAt); err == nil && !t.IsZero() {
			return t.UTC()
		}
	case TypeLLMUsageRecorded:
		// This builder is only used for DAG-run events, but fall back safely.
	}
	if status.CreatedAt > 0 {
		return time.UnixMilli(status.CreatedAt).UTC()
	}
	return time.Now().UTC()
}

func stableID(parts ...string) string {
	h := sha256.New()
	var lenBuf [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(part)))
		_, _ = h.Write(lenBuf[:])
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func cloneData(data map[string]any) map[string]any {
	if len(data) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(data))
	for key, value := range data {
		cloned[key] = cloneDataValue(value)
	}
	return cloned
}

func cloneDataValue(value any) any {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	case map[string]any:
		return cloneData(v)
	case []any:
		cloned := make([]any, len(v))
		for i, item := range v {
			cloned[i] = cloneDataValue(item)
		}
		return cloned
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() { //nolint:exhaustive // non-container kinds are returned as-is below
	case reflect.Map:
		if rv.IsNil() {
			return value
		}
		cloned := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			clonedValue := cloneDataValue(iter.Value().Interface())
			if clonedValue == nil {
				cloned.SetMapIndex(iter.Key(), reflect.Zero(rv.Type().Elem()))
				continue
			}
			valueRV := reflect.ValueOf(clonedValue)
			if !valueRV.Type().AssignableTo(rv.Type().Elem()) {
				if valueRV.Type().ConvertibleTo(rv.Type().Elem()) {
					valueRV = valueRV.Convert(rv.Type().Elem())
				} else {
					valueRV = iter.Value()
				}
			}
			cloned.SetMapIndex(iter.Key(), valueRV)
		}
		return cloned.Interface()
	case reflect.Slice:
		if rv.IsNil() {
			return value
		}
		cloned := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := range rv.Len() {
			item := cloneDataValue(rv.Index(i).Interface())
			if item == nil {
				cloned.Index(i).SetZero()
				continue
			}
			itemRV := reflect.ValueOf(item)
			if !itemRV.Type().AssignableTo(rv.Type().Elem()) {
				if itemRV.Type().ConvertibleTo(rv.Type().Elem()) {
					itemRV = itemRV.Convert(rv.Type().Elem())
				} else {
					itemRV = rv.Index(i)
				}
			}
			cloned.Index(i).Set(itemRV)
		}
		return cloned.Interface()
	default:
		return value
	}
}
