// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	dagRunStatusSnapshotDataKey       = "dag_run_status"
	notificationStatusSnapshotDataKey = "notification_status"
	DAGFileNameDataKey                = "dag_file"
)

type DAGRunCursor struct {
	LastInboxFile    string            `json:"last_inbox_file,omitempty"`
	CommittedOffsets map[string]int64  `json:"committed_offsets,omitempty"`
	InboxEventIDs    map[string]string `json:"inbox_event_ids,omitempty"`
}

func (c DAGRunCursor) Normalize() DAGRunCursor {
	if c.CommittedOffsets == nil {
		c.CommittedOffsets = make(map[string]int64)
	}
	if c.InboxEventIDs == nil {
		c.InboxEventIDs = make(map[string]string)
	}
	return c
}

func (c DAGRunCursor) Equal(other DAGRunCursor) bool {
	return c.LastInboxFile == other.LastInboxFile &&
		maps.Equal(c.CommittedOffsets, other.CommittedOffsets) &&
		maps.Equal(c.InboxEventIDs, other.InboxEventIDs)
}

type DAGRunReader interface {
	DAGRunHeadCursor(ctx context.Context) (DAGRunCursor, error)
	ReadDAGRunEvents(ctx context.Context, cursor DAGRunCursor) ([]*Event, DAGRunCursor, error)
}

type DAGRunNodeSnapshot struct {
	StepID            string                `json:"step_id,omitempty"`
	StepName          string                `json:"step_name,omitempty"`
	StartedAt         string                `json:"started_at,omitempty"`
	Status            ir.NodeStatus         `json:"status,omitempty"`
	Error             string                `json:"error,omitempty"`
	StatusDetails     []ir.NodeStatusDetail `json:"status_details,omitempty"`
	RetryCount        int                   `json:"retry_count,omitempty"`
	DoneCount         int                   `json:"done_count,omitempty"`
	ApprovalIteration int                   `json:"approval_iteration,omitempty"`
}

func newDAGRunNodeSnapshot(node *ir.Node) *DAGRunNodeSnapshot {
	if node == nil {
		return nil
	}
	return &DAGRunNodeSnapshot{
		StepID:            node.Step.ID,
		StepName:          node.Step.Name,
		StartedAt:         node.StartedAt,
		Status:            node.Status,
		Error:             node.Error,
		StatusDetails:     append([]ir.NodeStatusDetail(nil), node.StatusDetails...),
		RetryCount:        node.RetryCount,
		DoneCount:         node.DoneCount,
		ApprovalIteration: node.ApprovalIteration,
	}
}

func (s *DAGRunNodeSnapshot) Node() *ir.Node {
	if s == nil {
		return nil
	}
	return &ir.Node{
		Step:              ir.Step{ID: s.StepID, Name: s.StepName},
		StartedAt:         s.StartedAt,
		Status:            s.Status,
		Error:             s.Error,
		StatusDetails:     append([]ir.NodeStatusDetail(nil), s.StatusDetails...),
		RetryCount:        s.RetryCount,
		DoneCount:         s.DoneCount,
		ApprovalIteration: s.ApprovalIteration,
	}
}

type DAGRunRefSnapshot struct {
	Name     string `json:"name,omitempty"`
	DAGRunID string `json:"dag_run_id,omitempty"`
}

func newDAGRunRefSnapshot(ref ir.DAGRunRef) DAGRunRefSnapshot {
	return DAGRunRefSnapshot{
		Name:     ref.Name,
		DAGRunID: ref.ID,
	}
}

func (s DAGRunRefSnapshot) DAGRunRef() ir.DAGRunRef {
	if s.Name == "" || s.DAGRunID == "" {
		return ir.DAGRunRef{}
	}
	return ir.NewDAGRunRef(s.Name, s.DAGRunID)
}

type DAGRunStatusSnapshot struct {
	Root           DAGRunRefSnapshot    `json:"root"`
	Parent         DAGRunRefSnapshot    `json:"parent"`
	Name           string               `json:"name"`
	DAGFile        string               `json:"dag_file,omitempty"`
	Labels         []string             `json:"labels"`
	DAGRunID       string               `json:"dag_run_id"`
	AttemptID      string               `json:"attempt_id"`
	ProcGroup      string               `json:"proc_group,omitempty"`
	Status         ir.Status            `json:"status"`
	Error          string               `json:"error,omitempty"`
	Log            string               `json:"log,omitempty"`
	QueuedAt       string               `json:"queued_at,omitempty"`
	StartedAt      string               `json:"started_at,omitempty"`
	FinishedAt     string               `json:"finished_at,omitempty"`
	AutoRetryCount int                  `json:"auto_retry_count,omitempty"`
	AutoRetryLimit int                  `json:"auto_retry_limit,omitempty"`
	Nodes          []DAGRunNodeSnapshot `json:"nodes,omitempty"`
	OnFailure      *DAGRunNodeSnapshot  `json:"on_failure,omitempty"`
	OnExit         *DAGRunNodeSnapshot  `json:"on_exit,omitempty"`
	OnWait         *DAGRunNodeSnapshot  `json:"on_wait,omitempty"`
}

func (s *DAGRunStatusSnapshot) Validate() error {
	if s == nil {
		return errors.New("eventstore: dag-run snapshot is nil")
	}
	if s.DAGRunID == "" {
		return errors.New("eventstore: invalid dag-run snapshot: missing dag_run_id")
	}
	if s.AttemptID == "" {
		return errors.New("eventstore: invalid dag-run snapshot: missing attempt_id")
	}
	if s.Name == "" {
		return errors.New("eventstore: invalid dag-run snapshot: missing name")
	}
	switch s.Status { //nolint:exhaustive // persisted DAG-run events only allow lifecycle states
	case ir.Queued, ir.Running, ir.Waiting, ir.Succeeded, ir.PartiallySucceeded, ir.Failed, ir.Aborted, ir.Rejected:
	default:
		return errors.New("eventstore: invalid dag-run snapshot: missing or unsupported status")
	}
	return nil
}

func newDAGRunStatusSnapshot(status *ir.DAGRunStatus, dagFile string) *DAGRunStatusSnapshot {
	if status == nil {
		return nil
	}

	nodes := make([]DAGRunNodeSnapshot, 0, len(status.Nodes))
	for _, node := range status.Nodes {
		snapshot := newDAGRunNodeSnapshot(node)
		if snapshot == nil {
			continue
		}
		nodes = append(nodes, *snapshot)
	}

	return &DAGRunStatusSnapshot{
		Root:           newDAGRunRefSnapshot(status.Root),
		Parent:         newDAGRunRefSnapshot(status.Parent),
		Name:           status.Name,
		DAGFile:        dagFile,
		Labels:         append([]string{}, status.Labels...),
		DAGRunID:       status.DAGRunID,
		AttemptID:      status.AttemptID,
		ProcGroup:      status.ProcGroup,
		Status:         status.Status,
		Error:          status.Error,
		Log:            status.Log,
		QueuedAt:       status.QueuedAt,
		StartedAt:      status.StartedAt,
		FinishedAt:     status.FinishedAt,
		AutoRetryCount: status.AutoRetryCount,
		AutoRetryLimit: status.AutoRetryLimit,
		Nodes:          nodes,
		OnFailure:      newDAGRunNodeSnapshot(status.OnFailure),
		OnExit:         newDAGRunNodeSnapshot(status.OnExit),
		OnWait:         newDAGRunNodeSnapshot(status.OnWait),
	}
}

func (s *DAGRunStatusSnapshot) DAGRunStatus() *ir.DAGRunStatus {
	if s == nil {
		return nil
	}

	nodes := make([]*ir.Node, 0, len(s.Nodes))
	for _, node := range s.Nodes {
		nodes = append(nodes, node.Node())
	}

	return &ir.DAGRunStatus{
		Root:           s.Root.DAGRunRef(),
		Parent:         s.Parent.DAGRunRef(),
		Name:           s.Name,
		Labels:         append([]string(nil), s.Labels...),
		DAGRunID:       s.DAGRunID,
		AttemptID:      s.AttemptID,
		Status:         s.Status,
		ProcGroup:      s.ProcGroup,
		Error:          s.Error,
		Log:            s.Log,
		QueuedAt:       s.QueuedAt,
		StartedAt:      s.StartedAt,
		FinishedAt:     s.FinishedAt,
		AutoRetryCount: s.AutoRetryCount,
		AutoRetryLimit: s.AutoRetryLimit,
		Nodes:          nodes,
		OnFailure:      s.OnFailure.Node(),
		OnExit:         s.OnExit.Node(),
		OnWait:         s.OnWait.Node(),
	}
}

func IsDAGRunEventType(kind EventKind, eventType EventType) bool {
	if kind != KindDAGRun {
		return false
	}
	switch eventType {
	case TypeDAGRunQueued,
		TypeDAGRunRunning,
		TypeDAGRunUpdated,
		TypeDAGRunWaiting,
		TypeDAGRunSucceeded,
		TypeDAGRunPartiallySucceeded,
		TypeDAGRunFailed,
		TypeDAGRunAborted,
		TypeDAGRunRejected:
		return true
	case TypeLLMUsageRecorded:
		return false
	default:
		return false
	}
}

func DAGRunStatusFromEvent(event *Event) (*ir.DAGRunStatus, error) {
	snapshot, err := DAGRunSnapshotFromEvent(event)
	if err != nil {
		return nil, err
	}
	return snapshot.DAGRunStatus(), nil
}

func DAGRunSnapshotFromEvent(event *Event) (*DAGRunStatusSnapshot, error) {
	if event == nil {
		return nil, errors.New("eventstore: event is nil")
	}
	if !IsDAGRunEventType(event.Kind, event.Type) {
		return nil, fmt.Errorf("eventstore: event %q is not a dag-run event", event.Type)
	}
	return dagRunSnapshotFromData(event.Data)
}

func dagRunSnapshotFromData(data map[string]any) (*DAGRunStatusSnapshot, error) {
	snapshot, err := dagRunStatusSnapshotFromData(data)
	if err != nil {
		return nil, err
	}
	if snapshot.DAGFile == "" {
		snapshot.DAGFile = dagRunFileNameFromData(data)
	}
	return snapshot, nil
}

func dagRunStatusSnapshotFromData(data map[string]any) (*DAGRunStatusSnapshot, error) {
	if len(data) == 0 {
		return nil, errors.New("eventstore: dag-run snapshot is missing")
	}

	raw, ok := data[dagRunStatusSnapshotDataKey]
	if !ok {
		raw, ok = data[notificationStatusSnapshotDataKey]
	}
	if !ok {
		return nil, errors.New("eventstore: dag-run snapshot is missing")
	}

	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("eventstore: marshal dag-run snapshot: %w", err)
	}

	var snapshot DAGRunStatusSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return nil, fmt.Errorf("eventstore: unmarshal dag-run snapshot: %w", err)
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func dagRunFileNameFromData(data map[string]any) string {
	if len(data) == 0 {
		return ""
	}
	raw, ok := data[DAGFileNameDataKey]
	if !ok {
		return ""
	}
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	return value
}

func (s *Service) DAGRunHeadCursor(ctx context.Context) (DAGRunCursor, error) {
	if s == nil || s.store == nil {
		return DAGRunCursor{}, errors.New("eventstore: store is not configured")
	}
	if reader, ok := s.store.(DAGRunReader); ok {
		cursor, err := reader.DAGRunHeadCursor(ctx)
		if err != nil {
			return DAGRunCursor{}, err
		}
		return cursor.Normalize(), nil
	}
	return DAGRunCursor{}, errors.New("eventstore: dag-run reader is not configured")
}

func (s *Service) ReadDAGRunEvents(ctx context.Context, cursor DAGRunCursor) ([]*Event, DAGRunCursor, error) {
	if s == nil || s.store == nil {
		return nil, DAGRunCursor{}, errors.New("eventstore: store is not configured")
	}
	cursor = cursor.Normalize()
	if reader, ok := s.store.(DAGRunReader); ok {
		events, nextCursor, err := reader.ReadDAGRunEvents(ctx, cursor)
		if err != nil {
			return nil, DAGRunCursor{}, err
		}
		return events, nextCursor.Normalize(), nil
	}
	return nil, DAGRunCursor{}, errors.New("eventstore: dag-run reader is not configured")
}
