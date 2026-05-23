// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"

	"github.com/dagucloud/dagu/internal/persis"
)

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
