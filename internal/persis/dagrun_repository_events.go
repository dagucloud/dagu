// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

type eventingAttempt struct {
	dagrun.Attempt

	mu                   sync.Mutex
	dag                  *ir.DAG
	lastEmittedEventType eventstore.EventType
}

func newEventingAttempt(attempt dagrun.Attempt, dag *ir.DAG) dagrun.Attempt {
	if attempt == nil {
		return nil
	}
	return &eventingAttempt{Attempt: attempt, dag: dag}
}

func (a *eventingAttempt) Open(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.Attempt.Open(ctx); err != nil {
		return err
	}
	a.lastEmittedEventType = ""
	if _, _, ok := eventstore.FromContext(ctx); ok {
		if status, err := a.ReadStatus(ctx); err == nil && status != nil {
			a.lastEmittedEventType, _ = eventstore.PersistedDAGRunEventTypeForStatus(status.Status)
		}
	}
	return nil
}

func (a *eventingAttempt) Write(ctx context.Context, status ir.DAGRunStatus) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.Attempt.Write(ctx, status); err != nil {
		return err
	}
	if _, _, ok := eventstore.FromContext(ctx); !ok {
		return nil
	}
	a.lastEmittedEventType = emitStatusEvent(
		ctx,
		a.lastEmittedEventType,
		&status,
		a.eventData(ctx),
	)
	return nil
}

func (a *eventingAttempt) Close(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Attempt.Close(ctx)
}

func (a *eventingAttempt) SetDAG(dag *ir.DAG) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dag = dag
	a.Attempt.SetDAG(dag)
}

func (a *eventingAttempt) eventData(ctx context.Context) map[string]any {
	if a.dag == nil {
		a.dag, _ = a.ReadDAG(ctx)
	}
	return dagRunEventData(a.dag)
}

func (r *DAGRunRepository) emitStatusEventAfterSwap(
	ctx context.Context,
	root ir.DAGRunRef,
	dagRun ir.DAGRunRef,
	previousStatus ir.Status,
	status *ir.DAGRunStatus,
) {
	if _, _, ok := eventstore.FromContext(ctx); !ok {
		return
	}
	previousEventType, _ := eventstore.PersistedDAGRunEventTypeForStatus(previousStatus)
	emitStatusEvent(ctx, previousEventType, status, r.eventDataForDAGRun(ctx, root, dagRun))
}

func (r *DAGRunRepository) eventDataForDAGRun(ctx context.Context, root, dagRun ir.DAGRunRef) map[string]any {
	var (
		attempt dagrun.Attempt
		err     error
	)
	if root.ID != dagRun.ID || root.Name != dagRun.Name {
		attempt, err = r.store.FindSubAttempt(ctx, root, dagRun.ID)
	} else {
		attempt, err = r.store.FindAttempt(ctx, dagRun)
	}
	if err != nil {
		return nil
	}
	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return nil
	}
	return dagRunEventData(dag)
}

func emitStatusEvent(
	ctx context.Context,
	previous eventstore.EventType,
	status *ir.DAGRunStatus,
	data map[string]any,
) eventstore.EventType {
	next, _, err := eventstore.EmitPersistedStatusTransitionFromContext(ctx, previous, status, data)
	if err != nil {
		logger.Warn(ctx, "Failed to emit DAG-run event", tag.Error(err))
		return previous
	}
	return next
}

func dagRunEventData(dag *ir.DAG) map[string]any {
	if dag == nil {
		return nil
	}
	fileName := dag.FileName()
	if fileName == "" && dag.SourceFile != "" {
		fileName = fileutil.TrimYAMLFileExtension(filepath.Base(dag.SourceFile))
	}
	if fileName == "" {
		return nil
	}
	return map[string]any{eventstore.DAGFileNameDataKey: fileName}
}
