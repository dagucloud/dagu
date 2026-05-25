// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/dagucloud/dagu/internal/persis"
)

type strictRecordIDsCollection interface {
	RecordIDs(ctx context.Context, prefix string) ([]string, error)
}

// listAll drains all pages from col matching q, ignoring the Cursor field of q.
func listAll(ctx context.Context, col persis.Collection, q persis.ListQuery) ([]*persis.Record, error) {
	q.Cursor = ""
	var all []*persis.Record
	for {
		page, err := col.List(ctx, q)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Records...)
		if page.NextCursor == "" {
			return all, nil
		}
		q.Cursor = page.NextCursor
	}
}

// listAllStrict drains records like listAll, but uses RecordIDs when available
// so malformed file-backed records are surfaced by Get instead of being skipped
// by collection-level listing.
func listAllStrict(ctx context.Context, col persis.Collection, q persis.ListQuery) ([]*persis.Record, error) {
	idCol, ok := col.(strictRecordIDsCollection)
	if !ok {
		return listAll(ctx, col, q)
	}

	ids, err := idCol.RecordIDs(ctx, q.Prefix)
	if err != nil {
		return nil, err
	}

	recs := make([]*persis.Record, 0, len(ids))
	for _, id := range ids {
		rec, err := col.Get(ctx, id)
		if err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("list record %q: %w", id, err)
		}
		if q.Since != nil && rec.CreatedAt.Before(*q.Since) {
			continue
		}
		if q.Until != nil && !rec.CreatedAt.Before(*q.Until) {
			continue
		}
		recs = append(recs, rec)
	}

	sortRecordsByCreatedAt(recs)
	return recs, nil
}

// listAllBestEffort drains records like listAll, but uses RecordIDs when
// available so callers can observe and skip individual unreadable records.
func listAllBestEffort(
	ctx context.Context,
	col persis.Collection,
	q persis.ListQuery,
	onReadError func(id string, err error),
) ([]*persis.Record, error) {
	idCol, ok := col.(strictRecordIDsCollection)
	if !ok {
		return listAll(ctx, col, q)
	}

	ids, err := idCol.RecordIDs(ctx, q.Prefix)
	if err != nil {
		return nil, err
	}

	recs := make([]*persis.Record, 0, len(ids))
	for _, id := range ids {
		rec, err := col.Get(ctx, id)
		if err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				continue
			}
			if onReadError != nil {
				onReadError(id, err)
			}
			continue
		}
		if q.Since != nil && rec.CreatedAt.Before(*q.Since) {
			continue
		}
		if q.Until != nil && !rec.CreatedAt.Before(*q.Until) {
			continue
		}
		recs = append(recs, rec)
	}

	sortRecordsByCreatedAt(recs)
	return recs, nil
}

func sortRecordsByCreatedAt(recs []*persis.Record) {
	sort.Slice(recs, func(i, j int) bool {
		ti, tj := recs[i].CreatedAt, recs[j].CreatedAt
		if ti.Equal(tj) {
			return recs[i].ID < recs[j].ID
		}
		return ti.Before(tj)
	})
}
