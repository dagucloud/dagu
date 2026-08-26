// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistedDAGRunEventTypeForStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status ir.Status
		want   EventType
		ok     bool
	}{
		{name: "NotStarted", status: ir.NotStarted, ok: false},
		{name: "Queued", status: ir.Queued, want: TypeDAGRunQueued, ok: true},
		{name: "Running", status: ir.Running, want: TypeDAGRunRunning, ok: true},
		{name: "Rejected", status: ir.Rejected, want: TypeDAGRunRejected, ok: true},
		{name: "Waiting", status: ir.Waiting, want: TypeDAGRunWaiting, ok: true},
		{name: "Succeeded", status: ir.Succeeded, want: TypeDAGRunSucceeded, ok: true},
		{name: "PartiallySucceeded", status: ir.PartiallySucceeded, want: TypeDAGRunPartiallySucceeded, ok: true},
		{name: "Failed", status: ir.Failed, want: TypeDAGRunFailed, ok: true},
		{name: "Aborted", status: ir.Aborted, want: TypeDAGRunAborted, ok: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := PersistedDAGRunEventTypeForStatus(tt.status)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPartiallySucceededIsDAGRunEventType(t *testing.T) {
	t.Parallel()

	assert.True(t, IsDAGRunEventType(KindDAGRun, TypeDAGRunPartiallySucceeded))
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

	status := &ir.DAGRunStatus{
		Root:           ir.NewDAGRunRef("root-briefing", "root-run"),
		Parent:         ir.NewDAGRunRef("root-briefing", "parent-run"),
		Name:           "briefing",
		Labels:         []string{"workspace=ops", "team=platform"},
		DAGRunID:       "run-1",
		AttemptID:      "attempt-1",
		ProcGroup:      "priority-high",
		Status:         ir.Failed,
		Error:          "boom",
		Log:            "/tmp/run.log",
		QueuedAt:       "2026-04-01T09:00:00Z",
		StartedAt:      "2026-04-01T09:01:00Z",
		FinishedAt:     "2026-04-01T09:02:00Z",
		AutoRetryCount: 1,
		AutoRetryLimit: 3,
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "fetch"},
				Status: ir.NodeFailed,
				Error:  "node boom",
				StatusDetails: []ir.NodeStatusDetail{
					{Label: "customer-a", Status: ir.NodeFailed},
				},
			},
		},
		OnFailure: &ir.Node{
			Step:  ir.Step{Name: "notify"},
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
	assert.Equal(t, status.Labels, snapshot.Labels)

	restored, err := DAGRunStatusFromEvent(event)
	require.NoError(t, err)
	require.NotNil(t, restored)
	assert.Equal(t, status.Root, restored.Root)
	assert.Equal(t, status.Parent, restored.Parent)
	assert.Equal(t, status.Name, restored.Name)
	assert.Equal(t, status.Labels, restored.Labels)
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
	assert.Equal(t, ir.NodeFailed, restored.Nodes[0].Status)
	assert.Equal(t, "node boom", restored.Nodes[0].Error)
	assert.Equal(t, status.Nodes[0].StatusDetails, restored.Nodes[0].StatusDetails)
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
				"status":     ir.Succeeded,
			},
			DAGFileNameDataKey: "legacy.yaml",
		},
	}

	snapshot, err := DAGRunSnapshotFromEvent(event)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	assert.Equal(t, "legacy.yaml", snapshot.DAGFile)

	status, err := DAGRunStatusFromEvent(event)
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
	status := &ir.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    ir.Running,
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
}

func TestDAGRunUpdateEventIDIncludesRecordedAt(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 23, 1, 2, 3, 0, time.UTC)
	first := DAGRunUpdateEventID("briefing", "run-1", "attempt-1", base)
	second := DAGRunUpdateEventID("briefing", "run-1", "attempt-1", base.Add(time.Nanosecond))

	assert.NotEqual(t, first, second)
}

func TestNewDAGRunEventIdentifiesWaitingOccurrences(t *testing.T) {
	t.Parallel()

	status := &ir.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    ir.Waiting,
		Nodes: []*ir.Node{{
			Step:   ir.Step{Name: "approve-build"},
			Status: ir.NodeWaiting,
		}},
	}
	first := NewDAGRunEvent(Source{Service: SourceServiceServer}, TypeDAGRunWaiting, status, nil)
	repeated := NewDAGRunEvent(Source{Service: SourceServiceServer}, TypeDAGRunWaiting, status, nil)

	status.Nodes[0].ApprovalIteration = 1
	pushedBack := NewDAGRunEvent(Source{Service: SourceServiceServer}, TypeDAGRunWaiting, status, nil)

	status.Nodes = []*ir.Node{{
		Step:   ir.Step{Name: "approve-deploy"},
		Status: ir.NodeWaiting,
	}}
	second := NewDAGRunEvent(Source{Service: SourceServiceServer}, TypeDAGRunWaiting, status, nil)

	require.NotNil(t, first)
	require.NotNil(t, repeated)
	require.NotNil(t, pushedBack)
	require.NotNil(t, second)
	assert.Equal(t, first.ID, repeated.ID)
	assert.NotEqual(t, first.ID, pushedBack.ID)
	assert.NotEqual(t, first.ID, second.ID)
}

func TestNewDAGRunEventDeepClonesData(t *testing.T) {
	t.Parallel()

	status := &ir.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    ir.Failed,
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

func TestDAGRunStatusFromEventRejectsInvalidSnapshot(t *testing.T) {
	t.Parallel()

	status, err := DAGRunStatusFromEvent(&Event{
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

func TestDAGRunServiceNormalizesCursorAtBoundary(t *testing.T) {
	t.Parallel()

	store := &captureStore{
		dagRunHeadCursor: DAGRunCursor{},
		dagRunReadCursor: DAGRunCursor{
			LastInboxFile: "inbox-1",
		},
	}
	service := New(store)

	head, err := service.DAGRunHeadCursor(context.Background())
	require.NoError(t, err)
	require.NotNil(t, head.CommittedOffsets)

	_, nextCursor, err := service.ReadDAGRunEvents(context.Background(), DAGRunCursor{})
	require.NoError(t, err)
	require.NotNil(t, store.lastDAGRunReadCursor.CommittedOffsets)
	require.NotNil(t, nextCursor.CommittedOffsets)
}

type captureStore struct {
	event                *Event
	dagRunHeadCursor     DAGRunCursor
	dagRunReadCursor     DAGRunCursor
	lastDAGRunReadCursor DAGRunCursor
}

func (c *captureStore) Emit(_ context.Context, event *Event) error {
	c.event = event
	return nil
}

func (*captureStore) Query(context.Context, QueryFilter) (*QueryResult, error) {
	return nil, nil
}

func (c *captureStore) DAGRunHeadCursor(context.Context) (DAGRunCursor, error) {
	return c.dagRunHeadCursor, nil
}

func (c *captureStore) ReadDAGRunEvents(_ context.Context, cursor DAGRunCursor) ([]*Event, DAGRunCursor, error) {
	c.lastDAGRunReadCursor = cursor
	return nil, c.dagRunReadCursor, nil
}
