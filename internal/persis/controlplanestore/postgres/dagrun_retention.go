// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
)

func (s *Store) RemoveOldDAGRuns(ctx context.Context, name string, retentionDays int, opts ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	var options exec.RemoveOldDAGRunsOptions
	for _, opt := range opts {
		opt(&options)
	}

	var runIDs []string
	var err error
	if options.RetentionRuns != nil {
		retentionRuns := *options.RetentionRuns
		if retentionRuns <= 0 {
			return nil, nil
		}
		ids, listErr := s.queries.ListRemovableRunsByCount(ctx, db.ListRemovableRunsByCountParams{
			DagName:        name,
			ActiveStatuses: activeStatusInts(),
			RetentionRuns:  int32(retentionRuns), //nolint:gosec
		})
		err = listErr
		runIDs = uniqueStrings(ids)
	} else {
		if retentionDays < 0 {
			return nil, nil
		}
		cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
		ids, listErr := s.queries.ListRemovableRunsByDays(ctx, db.ListRemovableRunsByDaysParams{
			DagName:        name,
			ActiveStatuses: activeStatusInts(),
			Cutoff:         timestamptz(cutoff),
		})
		err = listErr
		runIDs = uniqueStrings(ids)
	}
	if err != nil {
		return nil, err
	}
	if options.DryRun {
		return runIDs, nil
	}
	for _, runID := range runIDs {
		if err := s.RemoveDAGRun(ctx, exec.NewDAGRunRef(name, runID)); err != nil {
			return nil, err
		}
	}
	return runIDs, nil
}

func (s *Store) RenameDAGRuns(ctx context.Context, oldName, newName string) error {
	return s.queries.RenameDAGRuns(ctx, db.RenameDAGRunsParams{
		OldName: oldName,
		NewName: newName,
	})
}

func (s *Store) RemoveDAGRun(ctx context.Context, dagRun exec.DAGRunRef, opts ...exec.RemoveDAGRunOption) error {
	if dagRun.ID == "" {
		return errors.New("dag-run ID is empty")
	}

	var options exec.RemoveDAGRunOptions
	for _, opt := range opts {
		opt(&options)
	}

	var deleted []string
	if err := s.withTx(ctx, func(q *db.Queries) error {
		if err := q.LockDAGRunKey(ctx, dagLockKey(dagRun.Name, dagRun.ID)); err != nil {
			return err
		}
		if options.RejectActive {
			row, err := q.LatestRootAttemptForUpdate(ctx, db.LatestRootAttemptForUpdateParams{
				DagName:  dagRun.Name,
				DagRunID: dagRun.ID,
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, dagRun.ID)
				}
				return err
			}
			status, err := statusFromRow(row)
			if err != nil {
				return err
			}
			if status.Status.IsActive() {
				return fmt.Errorf("%w: %s", exec.ErrDAGRunActive, status.Status.String())
			}
		}
		ids, err := q.DeleteDAGRunRows(ctx, db.DeleteDAGRunRowsParams{
			RootDagName:  dagRun.Name,
			RootDagRunID: dagRun.ID,
		})
		deleted = ids
		return err
	}); err != nil {
		return err
	}
	if len(deleted) == 0 {
		return fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, dagRun.ID)
	}

	if s.localWorkDirBase != "" {
		_ = os.RemoveAll(s.runWorkDir(dagRun))
	}
	return nil
}

func activeStatusInts() []int32 {
	return []int32{int32(core.Running), int32(core.Queued), int32(core.Waiting)}
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
