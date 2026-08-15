// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis"
)

type recordIDsCollection interface {
	RecordIDs(ctx context.Context, prefix string) ([]string, error)
}

type recordReadErrorHandler func(id string, err error) (handled bool, handleErr error)

type recordHelper struct {
	col  persis.Collection
	name string
}

func (h recordHelper) put(ctx context.Context, id string, value any) error {
	data, err := persis.Encode(value)
	if err != nil {
		return fmt.Errorf("%s: encode record: %w", h.name, err)
	}

	now := time.Now().UTC()
	createdAt := now
	existing, err := h.col.Get(ctx, id)
	if err == nil {
		createdAt = existing.CreatedAt
	} else if !errors.Is(err, persis.ErrNotFound) {
		return fmt.Errorf("%s: save record: %w", h.name, err)
	}

	if err := h.col.Put(ctx, &persis.Record{
		ID: id, Data: data, CreatedAt: createdAt, UpdatedAt: now,
	}); err != nil {
		return fmt.Errorf("%s: save record: %w", h.name, err)
	}
	return nil
}

func (h recordHelper) get(ctx context.Context, id string, notFound error) (*persis.Record, error) {
	rec, err := h.col.Get(ctx, id)
	if errors.Is(err, persis.ErrNotFound) {
		return nil, notFound
	}
	if err != nil {
		return nil, err
	}
	return rec, nil
}

func (h recordHelper) delete(ctx context.Context, id string, notFound error, kind string) error {
	if _, err := h.col.Get(ctx, id); errors.Is(err, persis.ErrNotFound) {
		return notFound
	} else if err != nil {
		return fmt.Errorf("%s: delete %s: %w", h.name, kind, err)
	}
	if err := h.col.Delete(ctx, id); err != nil {
		return fmt.Errorf("%s: delete %s: %w", h.name, kind, err)
	}
	return nil
}

func (h recordHelper) listTolerant(ctx context.Context, prefix, kind string) ([]*persis.Record, error) {
	recs, err := listAllStrictWithReadError(ctx, h.col, persis.ListQuery{Prefix: prefix}, func(id string, err error) (bool, error) {
		slog.Warn(h.name+": failed to load "+kind, "record", id, "error", err)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].ID < recs[j].ID })
	return recs, nil
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
	return listAllStrictWithReadError(ctx, col, q, nil)
}

func listAllStrictWithReadError(
	ctx context.Context,
	col persis.Collection,
	q persis.ListQuery,
	onReadError recordReadErrorHandler,
) ([]*persis.Record, error) {
	idCol, ok := col.(recordIDsCollection)
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
				handled, handleErr := onReadError(id, err)
				if handleErr != nil {
					return nil, handleErr
				}
				if handled {
					continue
				}
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

func sortRecordsByCreatedAt(recs []*persis.Record) {
	sort.Slice(recs, func(i, j int) bool {
		ti, tj := recs[i].CreatedAt, recs[j].CreatedAt
		if ti.Equal(tj) {
			return recs[i].ID < recs[j].ID
		}
		return ti.Before(tj)
	})
}

func hashRecordID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
