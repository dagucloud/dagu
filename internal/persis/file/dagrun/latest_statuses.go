// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"errors"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// LatestStatuses returns the status of each DAG's newest run when that run was
// created at or after query.NotBefore and matches the query's filters.
func (store *Store) LatestStatuses(ctx context.Context, query persis.DAGRunLatestStatusesQuery) ([]*ir.DAGRunStatus, error) {
	roots, err := store.listRoot(ctx, "")
	if err != nil {
		return nil, err
	}

	labelFilters := make([]ir.LabelFilter, 0, len(query.Labels))
	for _, label := range query.Labels {
		if trimmed := strings.TrimSpace(label); trimmed != "" {
			labelFilters = append(labelFilters, ir.ParseLabelFilter(trimmed))
		}
	}

	var statuses []*ir.DAGRunStatus
	for _, root := range roots {
		attempt, err := store.newestAttemptSince(ctx, root, query.NotBefore)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if !errors.Is(err, errLatestAttemptBeforeCutoff) && !errors.Is(err, dagrun.ErrNoStatusData) {
				logger.Warn(ctx, "Failed to find the latest DAG run", tag.Error(err))
			}
			continue
		}

		status, err := attempt.ReadStatus(ctx)
		if err != nil {
			logger.Warn(ctx, "Failed to read the latest DAG-run status", tag.Error(err))
			continue
		}
		labels := ir.NewLabels(status.Labels)
		if !labels.MatchesFilters(labelFilters) || !query.WorkspaceFilter.MatchesLabels(labels) {
			continue
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

// newestAttemptSince returns the newest visible attempt of root when its run
// was created at or after cutoff.
//
// A pointer update that loses a lock race leaves the pointer on an older run,
// so a pointer older than cutoff is trusted only while no day directory is
// dated on or after cutoff; a run created since cutoff would live in one.
// Otherwise, as for a missing pointer, the root's runs are listed and the
// pointer is repaired.
func (store *Store) newestAttemptSince(ctx context.Context, root DataRoot, cutoff persis.TimeInUTC) (*Attempt, error) {
	attempt, err := root.latestAttemptFromPointer(ctx, store.cache, cutoff)
	if err == nil || ctx.Err() != nil {
		return attempt, err
	}
	if errors.Is(err, errLatestAttemptBeforeCutoff) {
		days, err := listDayPathsInRange(root, cutoff, persis.TimeInUTC{})
		if err != nil {
			return nil, err
		}
		if len(days) == 0 {
			return nil, errLatestAttemptBeforeCutoff
		}
	}
	return store.latestAttemptAfter(ctx, root, cutoff)
}
