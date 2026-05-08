// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
)

func (s *Store) RecentAttempts(ctx context.Context, name string, itemLimit int) []exec.DAGRunAttempt {
	if itemLimit <= 0 {
		itemLimit = 10
	}
	rows, err := s.queries.RecentAttemptsByName(ctx, db.RecentAttemptsByNameParams{
		DagName:   name,
		ItemLimit: int32(itemLimit), //nolint:gosec
	})
	if err != nil {
		logger.Warn(ctx, "postgres dag-run store: recent attempts query failed",
			tag.Error(err),
			slog.String("dag", name),
		)
		return nil
	}

	attempts := make([]exec.DAGRunAttempt, 0, len(rows))
	for _, row := range rows {
		attempt, err := s.attemptFromRow(row)
		if err != nil {
			logger.Warn(ctx, "postgres dag-run store: failed to decode recent attempt; skipping",
				tag.Error(err),
				slog.String("dag", row.DagName),
				slog.String("dag_run_id", row.DagRunID),
			)
			continue
		}
		attempts = append(attempts, attempt)
	}
	return attempts
}

func (s *Store) LatestAttempt(ctx context.Context, name string) (exec.DAGRunAttempt, error) {
	hasFrom := false
	fromAt := pgtype.Timestamptz{}
	if s.latestStatusToday {
		now := time.Now().In(s.location)
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.location)
		hasFrom = true
		fromAt = timestamptz(startOfDay)
	}

	row, err := s.queries.LatestAttemptByName(ctx, db.LatestAttemptByNameParams{
		DagName: name,
		HasFrom: hasFrom,
		FromAt:  fromAt,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, exec.ErrNoStatusData
		}
		return nil, err
	}
	return s.attemptFromRow(row)
}

func (s *Store) FindAttempt(ctx context.Context, dagRun exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	if dagRun.ID == "" {
		return nil, errors.New("dag-run ID is empty")
	}
	row, err := s.latestRootAttempt(ctx, dagRun)
	if err != nil {
		return nil, err
	}
	return s.attemptFromRow(row)
}

func (s *Store) latestRootAttempt(ctx context.Context, dagRun exec.DAGRunRef) (db.DaguDagRunAttempt, error) {
	row, err := s.queries.LatestRootAttempt(ctx, db.LatestRootAttemptParams{
		DagName:  dagRun.Name,
		DagRunID: dagRun.ID,
	})
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.DaguDagRunAttempt{}, err
	}
	if _, anyErr := s.queries.FindRootRun(ctx, db.FindRootRunParams{
		DagName:  dagRun.Name,
		DagRunID: dagRun.ID,
	}); anyErr == nil {
		return db.DaguDagRunAttempt{}, exec.ErrNoStatusData
	} else if !errors.Is(anyErr, pgx.ErrNoRows) {
		return db.DaguDagRunAttempt{}, anyErr
	}
	return db.DaguDagRunAttempt{}, fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, dagRun.ID)
}

func (s *Store) FindSubAttempt(ctx context.Context, dagRun exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	if dagRun.ID == "" {
		return nil, errors.New("dag-run ID is empty")
	}
	row, err := s.queries.LatestSubAttempt(ctx, db.LatestSubAttemptParams{
		RootDagName:  dagRun.Name,
		RootDagRunID: dagRun.ID,
		DagRunID:     subDAGRunID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, subDAGRunID)
		}
		return nil, err
	}
	return s.attemptFromRow(row)
}

func (s *Store) CreateSubAttempt(ctx context.Context, rootRef exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	dag := &core.DAG{Name: rootRef.Name}
	return s.createSubAttempt(ctx, dag, time.Now(), subDAGRunID, exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
	})
}

func (s *Store) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	dagRun exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
) (*exec.DAGRunStatus, bool, error) {
	var result *exec.DAGRunStatus
	var swapped bool
	err := s.withTx(ctx, func(q *db.Queries) error {
		if err := q.LockDAGRunKey(ctx, dagLockKey(dagRun.Name, dagRun.ID)); err != nil {
			return err
		}
		row, err := q.LatestRootAttemptForUpdate(ctx, db.LatestRootAttemptForUpdateParams{
			DagName:  dagRun.Name,
			DagRunID: dagRun.ID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return exec.ErrNoStatusData
			}
			return err
		}
		status, err := statusFromRow(row)
		if err != nil {
			return err
		}
		result = status
		if expectedAttemptID != "" && status.AttemptID != expectedAttemptID {
			return nil
		}
		if status.Status != expectedStatus {
			return nil
		}
		if err := mutate(status); err != nil {
			return err
		}
		if err := updateStatus(ctx, q, row.ID, *status); err != nil {
			return err
		}
		result = status
		swapped = true
		return nil
	})
	return result, swapped, err
}
