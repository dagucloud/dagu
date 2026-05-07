// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
	"github.com/dagucloud/dagu/internal/service/eventstore"
)

var _ exec.DAGRunAttempt = (*Attempt)(nil)

var (
	errAttemptAlreadyOpen = errors.New("postgres dag-run attempt already open")
	errAttemptNotOpen     = errors.New("postgres dag-run attempt is not open")
)

// Attempt is a PostgreSQL-backed DAG-run attempt.
type Attempt struct {
	id                   uuid.UUID
	attemptID            string
	queries              *db.Queries
	workDir              string
	dag                  *core.DAG
	hidden               atomic.Bool
	cancelRequested      atomic.Bool
	opened               atomic.Bool
	mu                   sync.RWMutex
	lastEmittedEventType eventstore.EventType
}

func newAttempt(queries *db.Queries, row db.DaguDagRunAttempt) (*Attempt, error) {
	att := &Attempt{
		id:        row.ID,
		attemptID: row.AttemptID,
		queries:   queries,
		workDir:   row.LocalWorkDir,
	}
	att.hidden.Store(row.Hidden)
	att.cancelRequested.Store(row.CancelRequested)
	if len(row.Data) > 0 {
		doc, err := attemptDocumentFromJSON(row.Data)
		if err != nil {
			return nil, fmt.Errorf("unmarshal DAG-run attempt data for dag %q run %q attempt %q: %w",
				row.DagName, row.DagRunID, row.AttemptID, err)
		}
		att.dag = doc.DAG
	}
	return att, nil
}

func (att *Attempt) ID() string {
	return att.attemptID
}

func (att *Attempt) Open(ctx context.Context) error {
	att.mu.Lock()
	defer att.mu.Unlock()
	if att.opened.Load() {
		return errAttemptAlreadyOpen
	}
	if att.dag != nil {
		data, err := json.Marshal(att.dag)
		if err != nil {
			return fmt.Errorf("marshal DAG definition: %w", err)
		}
		if err := att.queries.UpdateAttemptDAG(ctx, db.UpdateAttemptDAGParams{
			ID:      att.id,
			DagJson: data,
		}); err != nil {
			return fmt.Errorf("persist DAG definition: %w", err)
		}
	}
	if att.workDir != "" {
		if err := os.MkdirAll(att.workDir, 0o750); err != nil {
			return fmt.Errorf("create work directory %s: %w", att.workDir, err)
		}
	}
	att.lastEmittedEventType = att.lastPersistedEventTypeLocked(ctx)
	att.opened.Store(true)
	return nil
}

func (att *Attempt) Write(ctx context.Context, status exec.DAGRunStatus) error {
	if !att.opened.Load() {
		return errAttemptNotOpen
	}
	att.mu.Lock()
	defer att.mu.Unlock()
	if !att.opened.Load() {
		return errAttemptNotOpen
	}

	if err := updateStatus(ctx, att.queries, att.id, status); err != nil {
		return fmt.Errorf("persist status: %w", err)
	}

	nextEventType, _, err := eventstore.EmitPersistedStatusTransitionFromContext(
		ctx,
		att.lastEmittedEventType,
		&status,
		att.eventData(),
	)
	if err != nil {
		logger.Warn(ctx, "Failed to emit DAG-run event", tag.Error(err))
	} else {
		att.lastEmittedEventType = nextEventType
	}
	return nil
}

func (att *Attempt) Close(context.Context) error {
	att.opened.Store(false)
	return nil
}

func (att *Attempt) ReadStatus(ctx context.Context) (*exec.DAGRunStatus, error) {
	row, err := att.queries.GetAttempt(ctx, att.id)
	if err != nil {
		return nil, err
	}
	return statusFromRow(row)
}

func (att *Attempt) ReadDAG(ctx context.Context) (*core.DAG, error) {
	row, err := att.queries.GetAttempt(ctx, att.id)
	if err != nil {
		return nil, err
	}
	doc, err := attemptDocumentFromJSON(row.Data)
	if err != nil {
		return nil, err
	}
	if doc.DAG == nil {
		return nil, errors.New("DAG definition not found")
	}
	return doc.DAG, nil
}

func (att *Attempt) SetDAG(dag *core.DAG) {
	att.mu.Lock()
	defer att.mu.Unlock()
	att.dag = dag
}

func (att *Attempt) Abort(ctx context.Context) error {
	if err := att.queries.SetAttemptCancelRequested(ctx, att.id); err != nil {
		return err
	}
	att.cancelRequested.Store(true)
	return nil
}

func (att *Attempt) IsAborting(ctx context.Context) (bool, error) {
	if att.cancelRequested.Load() {
		return true, nil
	}
	row, err := att.queries.GetAttempt(ctx, att.id)
	if err != nil {
		return false, err
	}
	att.cancelRequested.Store(row.CancelRequested)
	return row.CancelRequested, nil
}

func (att *Attempt) Hide(ctx context.Context) error {
	if att.opened.Load() {
		return errAttemptAlreadyOpen
	}
	if err := att.queries.SetAttemptHidden(ctx, att.id); err != nil {
		return err
	}
	att.hidden.Store(true)
	logger.Info(ctx, "Hidden attempt",
		tag.AttemptID(att.attemptID),
		slog.String("store", "postgres"))
	return nil
}

func (att *Attempt) Hidden() bool {
	return att.hidden.Load()
}

func (att *Attempt) WriteOutputs(ctx context.Context, outputs *exec.DAGRunOutputs) error {
	if outputs == nil || len(outputs.Outputs) == 0 {
		return nil
	}
	data, err := json.Marshal(outputs)
	if err != nil {
		return fmt.Errorf("marshal outputs: %w", err)
	}
	return att.queries.UpdateAttemptOutputs(ctx, db.UpdateAttemptOutputsParams{
		ID:          att.id,
		OutputsJson: data,
	})
}

func (att *Attempt) ReadOutputs(ctx context.Context) (*exec.DAGRunOutputs, error) {
	row, err := att.queries.GetAttempt(ctx, att.id)
	if err != nil {
		return nil, err
	}
	doc, err := attemptDocumentFromJSON(row.Data)
	if err != nil {
		return nil, err
	}
	if doc.Outputs == nil {
		return nil, nil
	}
	if doc.Outputs.Metadata.DAGRunID == "" {
		return nil, nil
	}
	return doc.Outputs, nil
}

func (att *Attempt) WriteStepMessages(ctx context.Context, stepName string, messages []exec.LLMMessage) error {
	if len(messages) == 0 {
		return nil
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("marshal step messages: %w", err)
	}
	return att.queries.MergeAttemptStepMessages(ctx, db.MergeAttemptStepMessagesParams{
		ID:               att.id,
		StepName:         stepName,
		StepMessagesJson: data,
	})
}

func (att *Attempt) ReadStepMessages(ctx context.Context, stepName string) ([]exec.LLMMessage, error) {
	row, err := att.queries.GetAttempt(ctx, att.id)
	if err != nil {
		return nil, err
	}
	doc, err := attemptDocumentFromJSON(row.Data)
	if err != nil {
		return nil, err
	}
	if len(doc.Messages) == 0 {
		return nil, nil
	}
	return doc.Messages[stepName], nil
}

func (att *Attempt) WorkDir() string {
	return att.workDir
}

func (att *Attempt) lastPersistedEventTypeLocked(ctx context.Context) eventstore.EventType {
	status, err := att.ReadStatus(ctx)
	if err != nil {
		return ""
	}
	eventType, ok := eventstore.PersistedDAGRunEventTypeForStatus(status.Status)
	if !ok {
		return ""
	}
	return eventType
}

func (att *Attempt) eventData() map[string]any {
	if att.dag == nil {
		return nil
	}
	fileName := att.dag.FileName()
	if fileName == "" && att.dag.SourceFile != "" {
		fileName = fileutil.TrimYAMLFileExtension(filepath.Base(att.dag.SourceFile))
	}
	if fileName == "" {
		return nil
	}
	return map[string]any{
		eventstore.DAGFileNameDataKey: fileName,
	}
}
