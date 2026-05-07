// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
)

func (s *Store) withTx(ctx context.Context, fn func(*db.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(s.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) attemptFromRow(row db.DaguDagRunAttempt) (*Attempt, error) {
	return newAttempt(s.queries, row)
}

func statusFromListRow(row db.DaguDagRun) (*exec.DAGRunStatus, error) {
	return statusFromDocumentJSON(row.Data)
}

func statusFromRow(row db.DaguDagRunAttempt) (*exec.DAGRunStatus, error) {
	return statusFromDocumentJSON(row.Data)
}

func statusFromDocumentJSON(data []byte) (*exec.DAGRunStatus, error) {
	if len(data) == 0 {
		return nil, exec.ErrNoStatusData
	}
	var doc dagRunDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal DAG-run document: %w", err)
	}
	if doc.Status == nil {
		return nil, exec.ErrNoStatusData
	}
	return doc.Status, nil
}

func updateStatus(ctx context.Context, q *db.Queries, id uuid.UUID, status exec.DAGRunStatus) error {
	data, err := marshalDAGRunStatus(status)
	if err != nil {
		return err
	}
	workspaceName, workspaceValid := workspaceFromLabels(core.NewLabels(status.Labels))
	return q.UpdateAttemptStatus(ctx, db.UpdateAttemptStatusParams{
		ID:             id,
		StatusJson:     data,
		Status:         int32(status.Status), //nolint:gosec
		Workspace:      workspaceName,
		WorkspaceValid: workspaceValid,
		StartedAt:      parseStatusTime(status.StartedAt),
		FinishedAt:     parseStatusTime(status.FinishedAt),
	})
}

type dagRunDocument struct {
	Status   *exec.DAGRunStatus           `json:"status,omitempty"`
	DAG      *core.DAG                    `json:"dag,omitempty"`
	Outputs  *exec.DAGRunOutputs          `json:"outputs,omitempty"`
	Messages map[string][]exec.LLMMessage `json:"messages,omitempty"`
}

func marshalDAGRunStatus(status exec.DAGRunStatus) ([]byte, error) {
	data, err := json.Marshal(status)
	if err != nil {
		return nil, fmt.Errorf("marshal DAG-run status: %w", err)
	}
	return data, nil
}

func marshalAttemptData(dag *core.DAG) ([]byte, error) {
	doc := dagRunDocument{}
	if dag != nil {
		doc.DAG = dag
	}
	return marshalDAGRunDocument(doc)
}

func attemptDocumentFromJSON(data []byte) (*dagRunDocument, error) {
	if len(data) == 0 {
		return &dagRunDocument{}, nil
	}
	var doc dagRunDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal DAG-run attempt document: %w", err)
	}
	return &doc, nil
}

func marshalDAGRunDocument(doc dagRunDocument) ([]byte, error) {
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal DAG-run document: %w", err)
	}
	if string(data) == "null" {
		return []byte("{}"), nil
	}
	return data, nil
}

func workspaceFromLabels(labels core.Labels) (sql.NullString, bool) {
	workspaceName, state := exec.WorkspaceLabelFromLabels(labels)
	switch state {
	case exec.WorkspaceLabelValid:
		return sql.NullString{String: workspaceName, Valid: true}, true
	case exec.WorkspaceLabelMissing:
		return sql.NullString{}, true
	case exec.WorkspaceLabelInvalid:
		return sql.NullString{}, false
	default:
		return sql.NullString{}, false
	}
}

func parseStatusTime(value string) pgtype.Timestamptz {
	parsed, err := stringutil.ParseTime(value)
	if err != nil || parsed.IsZero() {
		return pgtype.Timestamptz{}
	}
	return timestamptz(parsed)
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func timeFromTimestamptz(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}

func dagLockKey(name, runID string) string {
	return name + ":" + runID
}

func (s *Store) workDir(root exec.DAGRunRef, dagRunID string) string {
	rootDir := s.runWorkDir(root)
	if rootDir == "" {
		return ""
	}
	return filepath.Join(rootDir, dagRunID, "work")
}

func (s *Store) runWorkDir(root exec.DAGRunRef) string {
	if s.localWorkDirBase == "" {
		return ""
	}
	return filepath.Join(s.localWorkDirBase, "postgres-work", root.Name, root.ID)
}
