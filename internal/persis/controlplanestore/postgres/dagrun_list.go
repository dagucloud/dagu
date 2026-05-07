// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
)

func (s *Store) ListStatuses(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	options, err := prepareListOptions(opts)
	if err != nil {
		return nil, err
	}
	page, err := s.listStatuses(ctx, options, options.Limit, false)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (s *Store) ListStatusesPage(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	options, err := prepareListOptions(opts)
	if err != nil {
		return exec.DAGRunStatusPage{}, err
	}
	return s.listStatuses(ctx, options, options.Limit, true)
}

func (s *Store) listStatuses(ctx context.Context, opts exec.ListDAGRunStatusesOptions, limit int, returnCursor bool) (exec.DAGRunStatusPage, error) {
	cursorKey, err := decodeQueryCursor(opts.Cursor, opts)
	if err != nil {
		return exec.DAGRunStatusPage{}, err
	}

	target := limit
	if target <= 0 {
		if opts.Unlimited {
			target = math.MaxInt
		} else {
			target = opts.Limit
		}
	}
	if target <= 0 {
		target = 1
	}
	need := target
	if returnCursor && target < math.MaxInt {
		need++
	}

	labelFilters := make([]core.LabelFilter, 0, len(opts.Labels))
	for _, label := range opts.Labels {
		if trimmed := strings.TrimSpace(label); trimmed != "" {
			labelFilters = append(labelFilters, core.ParseLabelFilter(trimmed))
		}
	}

	items := make([]*exec.DAGRunStatus, 0, min(need, 1000))
	keys := make([]listKey, 0, cap(items))
	internalCursor := cursorKey
	cursorSet := opts.Cursor != ""
	chunkLimit := need
	if len(labelFilters) > 0 && chunkLimit < 1000 {
		chunkLimit = 1000
	}
	if chunkLimit <= 0 || chunkLimit == math.MaxInt {
		chunkLimit = 1000
	}

	for len(items) < need {
		rows, err := s.queries.ListRootStatusRows(ctx, s.listParams(opts, internalCursor, cursorSet, chunkLimit))
		if err != nil {
			return exec.DAGRunStatusPage{}, err
		}
		if len(rows) == 0 {
			break
		}

		for _, row := range rows {
			key := listKey{
				Timestamp: timeFromTimestamptz(row.RunCreatedAt).UTC(),
				Name:      row.DagName,
				DAGRunID:  row.DagRunID,
			}
			internalCursor = key
			cursorSet = true

			status, err := statusFromListRow(row)
			if err != nil {
				if !errors.Is(err, exec.ErrNoStatusData) {
					logger.Warn(ctx, "postgres dag-run store: failed to decode status row; skipping",
						tag.Error(err),
						slog.String("dag", row.DagName),
						slog.String("dag_run_id", row.DagRunID),
					)
				}
				continue
			}
			if len(labelFilters) > 0 && !core.NewLabels(status.Labels).MatchesFilters(labelFilters) {
				continue
			}
			items = append(items, status)
			keys = append(keys, key)
			if len(items) >= need {
				break
			}
		}
		if len(rows) < chunkLimit {
			break
		}
	}

	if !returnCursor || limit <= 0 || len(items) <= limit {
		return exec.DAGRunStatusPage{Items: items}, nil
	}
	nextCursor, err := encodeQueryCursor(opts, keys[limit-1])
	if err != nil {
		return exec.DAGRunStatusPage{}, err
	}
	return exec.DAGRunStatusPage{
		Items:      items[:limit],
		NextCursor: nextCursor,
	}, nil
}

func (s *Store) listParams(opts exec.ListDAGRunStatusesOptions, cursor listKey, cursorSet bool, pageLimit int) db.ListRootStatusRowsParams {
	statuses := make([]int32, 0, len(opts.Statuses))
	for _, status := range opts.Statuses {
		statuses = append(statuses, int32(status)) //nolint:gosec
	}

	params := db.ListRootStatusRowsParams{
		ExactName:              opts.ExactName,
		NameContains:           opts.Name,
		DagRunIDContains:       opts.DAGRunID,
		HasFrom:                !opts.From.IsZero(),
		FromAt:                 timestamptz(opts.From.Time),
		HasTo:                  !opts.To.IsZero(),
		ToAt:                   timestamptz(opts.To.Time),
		Statuses:               statuses,
		WorkspaceFilterEnabled: opts.WorkspaceFilter != nil && opts.WorkspaceFilter.Enabled,
		PageLimit:              int32(pageLimit), //nolint:gosec
		CursorSet:              cursorSet,
		CursorTimestamp:        timestamptz(cursor.Timestamp),
		CursorName:             cursor.Name,
		CursorDagRunID:         cursor.DAGRunID,
	}
	if opts.WorkspaceFilter != nil {
		params.IncludeUnlabelled = opts.WorkspaceFilter.IncludeUnlabelled
		params.Workspaces = append([]string(nil), opts.WorkspaceFilter.Workspaces...)
	}
	return params
}

func prepareListOptions(opts []exec.ListDAGRunStatusesOption) (exec.ListDAGRunStatusesOptions, error) {
	var options exec.ListDAGRunStatusesOptions
	for _, opt := range opts {
		opt(&options)
	}
	if !options.AllHistory && options.From.IsZero() && options.To.IsZero() {
		options.From = exec.NewUTC(time.Now().Truncate(24 * time.Hour))
	}
	if !options.Unlimited {
		const maxLimit = 1000
		if options.Limit == 0 || options.Limit > maxLimit {
			options.Limit = maxLimit
		}
	}
	return options, nil
}
