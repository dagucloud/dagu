// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/backfill"
	filebackfill "github.com/dagucloud/dagu/v2/internal/persis/file/backfill"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The DAG list gives each DAG the backfill it mentions: its newest open
// backfill, else its newest that ended failed and still needs attention.
func TestAttachBackfills(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := filebackfill.New(t.TempDir())
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	submit := func(id, dag string, at time.Time, slots int) {
		require.NoError(t, store.Create(ctx, &backfill.Record{
			ID: id, DAGName: dag, CreatedAt: at, Slots: make([]backfill.RecordSlot, slots),
		}))
	}
	submit("aaaaaaaa", "etl", created, 28)
	submit("bbbbbbbb", "etl", created.Add(time.Hour), 5)
	require.NoError(t, store.End(ctx, "bbbbbbbb", backfill.StatusFailed, created.Add(2*time.Hour)))
	require.NoError(t, store.SaveSummary(ctx, backfill.Summary{
		ID: "aaaaaaaa", DAGName: "etl", Slots: 28, Done: 12, Failed: 1, CreatedAt: created,
	}))
	submit("cccccccc", "report", created, 3)
	require.NoError(t, store.End(ctx, "cccccccc", backfill.StatusFailed, created.Add(time.Hour)))

	files := []openapiv1.DAGFile{
		{Dag: openapiv1.DAG{Name: "etl"}},
		{Dag: openapiv1.DAG{Name: "report"}},
		{Dag: openapiv1.DAG{Name: "quiet"}},
	}
	errs := (&API{backfillStore: store}).attachBackfills(ctx, files, nil)

	assert.Empty(t, errs)
	assert.Equal(t, &openapiv1.DAGBackfillSummary{
		Id: "aaaaaaaa", Status: openapiv1.BackfillStatusRunning, Slots: 28, Done: 12, Failed: 1,
	}, files[0].Backfill, "an open backfill comes before a newer failed one")
	assert.Equal(t, &openapiv1.DAGBackfillSummary{
		Id: "cccccccc", Status: openapiv1.BackfillStatusFailed, Slots: 3,
	}, files[1].Backfill)
	assert.Nil(t, files[2].Backfill)
}
