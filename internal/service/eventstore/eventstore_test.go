// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistedDAGRunEventTypeForStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status core.Status
		want   EventType
		ok     bool
	}{
		{name: "NotStarted", status: core.NotStarted, ok: false},
		{name: "Queued", status: core.Queued, want: TypeDAGRunQueued, ok: true},
		{name: "Running", status: core.Running, want: TypeDAGRunRunning, ok: true},
		{name: "Rejected", status: core.Rejected, want: TypeDAGRunRejected, ok: true},
		{name: "Waiting", status: core.Waiting, want: TypeDAGRunWaiting, ok: true},
		{name: "Succeeded", status: core.Succeeded, want: TypeDAGRunSucceeded, ok: true},
		{name: "PartiallySucceeded", status: core.PartiallySucceeded, want: TypeDAGRunSucceeded, ok: true},
		{name: "Failed", status: core.Failed, want: TypeDAGRunFailed, ok: true},
		{name: "Aborted", status: core.Aborted, want: TypeDAGRunAborted, ok: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := PersistedDAGRunEventTypeForStatus(tt.status)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestServiceEmitDefaultsFieldsWithoutReadTimeRepair(t *testing.T) {
	t.Parallel()

	store := &captureStore{}
	service := New(store)

	event := &Event{
		ID:         "evt-1",
		OccurredAt: time.Date(2026, 4, 1, 10, 0, 0, 0, time.FixedZone("JST", 9*60*60)),
		Kind:       KindDAGRun,
		Type:       TypeDAGRunFailed,
	}

	require.NoError(t, service.Emit(context.Background(), event))
	require.NotNil(t, store.event)
	assert.Equal(t, SchemaVersion, store.event.SchemaVersion)
	assert.Equal(t, SourceServiceUnknown, store.event.SourceService)
	assert.False(t, store.event.RecordedAt.IsZero())
	assert.Equal(t, time.UTC, store.event.OccurredAt.Location())

	readEvent := &Event{}
	readEvent.Normalize()
	assert.Zero(t, readEvent.SchemaVersion)
	assert.Empty(t, readEvent.SourceService)
	assert.True(t, readEvent.RecordedAt.IsZero())
}

func TestStableIDUsesCollisionSafeFraming(t *testing.T) {
	t.Parallel()

	assert.NotEqual(t,
		stableID("a", "b\x1fc"),
		stableID("a\x1fb", "c"),
	)
}

func TestNewDAGRunEventEmbedsDAGRunSnapshot(t *testing.T) {
	t.Parallel()

	status := &exec.DAGRunStatus{
		Root:           exec.NewDAGRunRef("root-briefing", "root-run"),
		Parent:         exec.NewDAGRunRef("root-briefing", "parent-run"),
		Name:           "briefing",
		DAGRunID:       "run-1",
		AttemptID:      "attempt-1",
		ProcGroup:      "priority-high",
		Status:         core.Failed,
		Error:          "boom",
		Log:            "/tmp/run.log",
		QueuedAt:       "2026-04-01T09:00:00Z",
		StartedAt:      "2026-04-01T09:01:00Z",
		FinishedAt:     "2026-04-01T09:02:00Z",
		AutoRetryCount: 1,
		AutoRetryLimit: 3,
		Nodes: []*exec.Node{
			{
				Step:   core.Step{Name: "fetch"},
				Status: core.NodeFailed,
				Error:  "node boom",
			},
		},
		OnFailure: &exec.Node{
			Step:  core.Step{Name: "notify"},
			Error: "handler boom",
		},
	}

	event := NewDAGRunEvent(Source{Service: SourceServiceServer, Instance: "test"}, TypeDAGRunFailed, status, map[string]any{
		"reason":           "boom",
		DAGFileNameDataKey: "briefing.yaml",
	})
	require.NotNil(t, event)
	require.NotNil(t, event.Data)
	assert.Equal(t, "boom", event.Data["reason"])

	snapshot, err := DAGRunSnapshotFromEvent(event)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	assert.Equal(t, "briefing.yaml", snapshot.DAGFile)
	assert.Equal(t, status.Root.Name, snapshot.Root.Name)
	assert.Equal(t, status.Parent.ID, snapshot.Parent.DAGRunID)
	assert.Equal(t, status.ProcGroup, snapshot.ProcGroup)

	restored, err := DAGRunStatusFromEvent(event)
	require.NoError(t, err)
	require.NotNil(t, restored)
	assert.Equal(t, status.Root, restored.Root)
	assert.Equal(t, status.Parent, restored.Parent)
	assert.Equal(t, status.Name, restored.Name)
	assert.Equal(t, status.DAGRunID, restored.DAGRunID)
	assert.Equal(t, status.AttemptID, restored.AttemptID)
	assert.Equal(t, status.ProcGroup, restored.ProcGroup)
	assert.Equal(t, status.Status, restored.Status)
	assert.Equal(t, status.Error, restored.Error)
	assert.Equal(t, status.Log, restored.Log)
	assert.Equal(t, status.StartedAt, restored.StartedAt)
	assert.Equal(t, status.FinishedAt, restored.FinishedAt)
	assert.Equal(t, status.AutoRetryCount, restored.AutoRetryCount)
	assert.Equal(t, status.AutoRetryLimit, restored.AutoRetryLimit)
	require.Len(t, restored.Nodes, 1)
	assert.Equal(t, "fetch", restored.Nodes[0].Step.Name)
	assert.Equal(t, core.NodeFailed, restored.Nodes[0].Status)
	assert.Equal(t, "node boom", restored.Nodes[0].Error)
	require.NotNil(t, restored.OnFailure)
	assert.Equal(t, "notify", restored.OnFailure.Step.Name)
	assert.Equal(t, "handler boom", restored.OnFailure.Error)
}

func TestDAGRunSnapshotFromEventBackfillsLegacyDAGFile(t *testing.T) {
	t.Parallel()

	event := &Event{
		ID:            "evt-legacy",
		SchemaVersion: SchemaVersion,
		OccurredAt:    time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC),
		RecordedAt:    time.Date(2026, 4, 1, 9, 0, 1, 0, time.UTC),
		Kind:          KindDAGRun,
		Type:          TypeDAGRunSucceeded,
		SourceService: SourceServiceServer,
		Data: map[string]any{
			notificationStatusSnapshotDataKey: map[string]any{
				"name":       "legacy",
				"dag_run_id": "run-1",
				"attempt_id": "attempt-1",
				"status":     core.Succeeded,
			},
			DAGFileNameDataKey: "legacy.yaml",
		},
	}

	snapshot, err := DAGRunSnapshotFromEvent(event)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	assert.Equal(t, "legacy.yaml", snapshot.DAGFile)

	status, err := NotificationStatusFromEvent(event)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, "legacy", status.Name)
	assert.Equal(t, "run-1", status.DAGRunID)
}

func TestEmitPersistedStatusTransitionFromContextEmitsUpdateForRepeatedStatus(t *testing.T) {
	t.Parallel()

	store := &captureStore{}
	service := New(store)
	ctx := WithContext(context.Background(), service, Source{Service: SourceServiceServer})
	status := &exec.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    core.Running,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}

	previous, emitted, err := EmitPersistedStatusTransitionFromContext(ctx, "", status, nil)
	require.NoError(t, err)
	require.True(t, emitted)
	require.NotNil(t, store.event)
	assert.Equal(t, TypeDAGRunRunning, store.event.Type)
	assert.Equal(t, TypeDAGRunRunning, previous)

	store.event = nil
	next, emitted, err := EmitPersistedStatusTransitionFromContext(ctx, previous, status, nil)
	require.NoError(t, err)
	require.True(t, emitted)
	require.NotNil(t, store.event)
	assert.Equal(t, TypeDAGRunUpdated, store.event.Type)
	assert.Equal(t, TypeDAGRunRunning, next)
	assert.True(t, IsDAGRunEventType(store.event.Kind, store.event.Type))
	assert.False(t, IsNotificationEventType(store.event.Kind, store.event.Type))
}

func TestDAGRunUpdateEventIDIncludesRecordedAt(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 23, 1, 2, 3, 0, time.UTC)
	first := DAGRunUpdateEventID("briefing", "run-1", "attempt-1", base)
	second := DAGRunUpdateEventID("briefing", "run-1", "attempt-1", base.Add(time.Nanosecond))

	assert.NotEqual(t, first, second)
}

func TestNewDAGRunEventDeepClonesData(t *testing.T) {
	t.Parallel()

	status := &exec.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    core.Failed,
	}
	nested := map[string]any{
		"details": map[string]any{"reason": "boom"},
		"steps": []any{
			map[string]any{"name": "fetch"},
		},
	}

	event := NewDAGRunEvent(Source{Service: SourceServiceServer}, TypeDAGRunFailed, status, nested)
	require.NotNil(t, event)
	require.NotNil(t, event.Data)

	nestedDetails := nested["details"].(map[string]any)
	nestedDetails["reason"] = "changed"
	nestedSteps := nested["steps"].([]any)
	nestedSteps[0].(map[string]any)["name"] = "mutated"

	assert.Equal(t, "boom", event.Data["details"].(map[string]any)["reason"])
	assert.Equal(t, "fetch", event.Data["steps"].([]any)[0].(map[string]any)["name"])
}

func TestNotificationStatusFromEventRejectsInvalidSnapshot(t *testing.T) {
	t.Parallel()

	status, err := NotificationStatusFromEvent(&Event{
		ID:            "evt-1",
		SchemaVersion: SchemaVersion,
		OccurredAt:    time.Now().UTC(),
		RecordedAt:    time.Now().UTC(),
		Kind:          KindDAGRun,
		Type:          TypeDAGRunFailed,
		SourceService: SourceServiceServer,
		Data: map[string]any{
			notificationStatusSnapshotDataKey: map[string]any{},
		},
	})
	require.Error(t, err)
	assert.Nil(t, status)
	assert.ErrorContains(t, err, "missing dag_run_id")
}

func TestNotificationServiceNormalizesCursorAtBoundary(t *testing.T) {
	t.Parallel()

	store := &captureStore{
		notificationHeadCursor: NotificationCursor{},
		notificationReadCursor: NotificationCursor{
			LastInboxFile: "inbox-1",
		},
	}
	service := New(store)

	head, err := service.NotificationHeadCursor(context.Background())
	require.NoError(t, err)
	require.NotNil(t, head.CommittedOffsets)

	_, nextCursor, err := service.ReadNotificationEvents(context.Background(), NotificationCursor{})
	require.NoError(t, err)
	require.NotNil(t, store.lastNotificationReadCursor.CommittedOffsets)
	require.NotNil(t, nextCursor.CommittedOffsets)
}

type captureStore struct {
	event                      *Event
	notificationHeadCursor     NotificationCursor
	notificationReadCursor     NotificationCursor
	lastNotificationReadCursor NotificationCursor
}

func (c *captureStore) Emit(_ context.Context, event *Event) error {
	c.event = event
	return nil
}

func (*captureStore) Query(context.Context, QueryFilter) (*QueryResult, error) {
	return nil, nil
}

func (c *captureStore) NotificationHeadCursor(context.Context) (NotificationCursor, error) {
	return c.notificationHeadCursor, nil
}

func (c *captureStore) ReadNotificationEvents(_ context.Context, cursor NotificationCursor) ([]*Event, NotificationCursor, error) {
	c.lastNotificationReadCursor = cursor
	return nil, c.notificationReadCursor, nil
}
