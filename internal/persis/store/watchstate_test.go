// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatchStateStoreRoundTrip(t *testing.T) {
	t.Parallel()

	col := testutil.NewMemoryBackend().Collection("watch")
	s := store.NewWatchStateStore(col)
	ctx := context.Background()

	ledgers, err := s.LoadWatch(ctx)
	require.NoError(t, err)
	assert.Empty(t, ledgers)

	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.FixedZone("JST", 9*3600))
	require.NoError(t, s.SaveWatch(ctx, map[string]schedulerstate.WatchLedger{
		"inbox": {Path: "/srv/inbox", Pattern: "*.pdf", Files: map[string]schedulerstate.FileMark{"a.pdf": {Size: 5, ModTime: at}}},
	}))

	ledgers, err = store.NewWatchStateStore(col).LoadWatch(ctx)
	require.NoError(t, err)
	require.Contains(t, ledgers, "inbox")
	assert.Equal(t, "/srv/inbox", ledgers["inbox"].Path)
	assert.Equal(t, "*.pdf", ledgers["inbox"].Pattern)
	assert.True(t, ledgers["inbox"].Files["a.pdf"].Same(schedulerstate.FileMark{Size: 5, ModTime: at}))
}

func TestWatchStateStoreStartsFreshFromACorruptRecord(t *testing.T) {
	t.Parallel()

	col := testutil.NewMemoryBackend().Collection("watch")
	ctx := context.Background()
	require.NoError(t, col.Put(ctx, &persis.Record{ID: "watch", Data: []byte("{not json"), CreatedAt: time.Now(), UpdatedAt: time.Now()}))

	ledgers, err := store.NewWatchStateStore(col).LoadWatch(ctx)
	require.NoError(t, err)
	assert.Empty(t, ledgers)
}
