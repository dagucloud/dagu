// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
)

func (s *Store) CreateAttempt(ctx context.Context, dag *core.DAG, timestamp time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	if dagRunID == "" {
		return nil, errors.New("dag-run ID is empty")
	}
	if err := exec.ValidateDAGRunID(dagRunID); err != nil {
		return nil, err
	}
	if dag == nil {
		return nil, errors.New("DAG must not be nil")
	}
	if err := core.ValidateDAGName(dag.Name); err != nil {
		return nil, err
	}

	if opts.RootDAGRun != nil {
		return s.createSubAttempt(ctx, dag, timestamp, dagRunID, opts)
	}
	return s.createRootAttempt(ctx, dag, timestamp, dagRunID, opts)
}

func (s *Store) createRootAttempt(ctx context.Context, dag *core.DAG, timestamp time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	var row db.DaguDagRunAttempt
	err := s.withTx(ctx, func(q *db.Queries) error {
		lockKey := dagLockKey(dag.Name, dagRunID)
		if err := q.LockDAGRunKey(ctx, lockKey); err != nil {
			return fmt.Errorf("lock dag-run: %w", err)
		}

		run, findErr := q.FindRootRun(ctx, db.FindRootRunParams{
			DagName:  dag.Name,
			DagRunID: dagRunID,
		})
		if opts.Retry {
			if findErr != nil {
				if errors.Is(findErr, pgx.ErrNoRows) {
					return fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, dagRunID)
				}
				return findErr
			}
		} else if findErr == nil {
			return fmt.Errorf("%w: %s", exec.ErrDAGRunAlreadyExists, dagRunID)
		} else if !errors.Is(findErr, pgx.ErrNoRows) {
			return findErr
		} else {
			workspaceName, workspaceValid := workspaceFromLabels(dag.Labels)
			createdRun, err := s.createRun(ctx, q, dag.Name, dagRunID, exec.NewDAGRunRef(dag.Name, dagRunID), true, timestamp, workspaceName, workspaceValid)
			if err != nil {
				return err
			}
			run = createdRun
		}

		runCreatedAt := timestamp
		if findErr == nil {
			runCreatedAt = timeFromTimestamptz(run.RunCreatedAt)
		}

		created, err := s.insertAttempt(ctx, q, run.ID, dag, dagRunID, exec.NewDAGRunRef(dag.Name, dagRunID), true, runCreatedAt, timestamp, opts.AttemptID)
		if err != nil {
			return err
		}
		row = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.attemptFromRow(row)
}

func (s *Store) createSubAttempt(ctx context.Context, dag *core.DAG, timestamp time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	root := *opts.RootDAGRun
	if err := core.ValidateDAGName(root.Name); err != nil {
		return nil, err
	}
	if err := exec.ValidateDAGRunID(root.ID); err != nil {
		return nil, err
	}

	var row db.DaguDagRunAttempt
	err := s.withTx(ctx, func(q *db.Queries) error {
		if err := q.LockDAGRunKey(ctx, dagLockKey(root.Name, root.ID)); err != nil {
			return fmt.Errorf("lock root dag-run: %w", err)
		}
		if _, err := q.FindRootRun(ctx, db.FindRootRunParams{
			DagName:  root.Name,
			DagRunID: root.ID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, root.ID)
			}
			return err
		}

		run, findErr := q.FindSubRun(ctx, db.FindSubRunParams{
			RootDagName:  root.Name,
			RootDagRunID: root.ID,
			DagRunID:     dagRunID,
		})
		if findErr != nil {
			if !errors.Is(findErr, pgx.ErrNoRows) {
				return findErr
			}
			workspaceName, workspaceValid := workspaceFromLabels(dag.Labels)
			createdRun, err := s.createRun(ctx, q, dag.Name, dagRunID, root, false, timestamp, workspaceName, workspaceValid)
			if err != nil {
				return err
			}
			run = createdRun
		}

		runCreatedAt := timestamp
		if findErr == nil {
			runCreatedAt = timeFromTimestamptz(run.RunCreatedAt)
		}

		created, err := s.insertAttempt(ctx, q, run.ID, dag, dagRunID, root, false, runCreatedAt, timestamp, opts.AttemptID)
		if err != nil {
			return err
		}
		row = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.attemptFromRow(row)
}

func (s *Store) createRun(
	ctx context.Context,
	q *db.Queries,
	dagName string,
	dagRunID string,
	root exec.DAGRunRef,
	isRoot bool,
	runCreatedAt time.Time,
	workspace sql.NullString,
	workspaceValid bool,
) (db.DaguDagRun, error) {
	rowID, err := uuid.NewV7()
	if err != nil {
		return db.DaguDagRun{}, err
	}
	return q.CreateRun(ctx, db.CreateRunParams{
		ID:             rowID,
		DagName:        dagName,
		DagRunID:       dagRunID,
		RootDagName:    root.Name,
		RootDagRunID:   root.ID,
		IsRoot:         isRoot,
		RunCreatedAt:   timestamptz(runCreatedAt),
		Workspace:      workspace,
		WorkspaceValid: workspaceValid,
	})
}

func (s *Store) insertAttempt(
	ctx context.Context,
	q *db.Queries,
	runID uuid.UUID,
	dag *core.DAG,
	dagRunID string,
	root exec.DAGRunRef,
	isRoot bool,
	runCreatedAt time.Time,
	attemptCreatedAt time.Time,
	attemptID string,
) (db.DaguDagRunAttempt, error) {
	rowID, err := uuid.NewV7()
	if err != nil {
		return db.DaguDagRunAttempt{}, err
	}
	if attemptID == "" {
		attemptID, err = genAttemptID()
		if err != nil {
			return db.DaguDagRunAttempt{}, err
		}
	}

	data, err := marshalAttemptData(dag)
	if err != nil {
		return db.DaguDagRunAttempt{}, err
	}
	workspaceName, workspaceValid := workspaceFromLabels(dag.Labels)
	workDir := s.workDir(root, dagRunID)

	return q.CreateAttempt(ctx, db.CreateAttemptParams{
		ID:               rowID,
		RunID:            runID,
		DagName:          dag.Name,
		DagRunID:         dagRunID,
		RootDagName:      root.Name,
		RootDagRunID:     root.ID,
		IsRoot:           isRoot,
		AttemptID:        attemptID,
		RunCreatedAt:     timestamptz(runCreatedAt),
		AttemptCreatedAt: timestamptz(attemptCreatedAt),
		Workspace:        workspaceName,
		WorkspaceValid:   workspaceValid,
		Data:             data,
		LocalWorkDir:     workDir,
	})
}

func genAttemptID() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random attempt ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}
