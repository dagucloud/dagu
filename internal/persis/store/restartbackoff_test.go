// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

func TestRestartBackoffStore(t *testing.T) {
	ctx := context.Background()
	s := store.NewRestartBackoffStore(testutil.NewMemoryBackend().Collection("scheduler"))

	backoff, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Empty(t, backoff, "a store that was never written is empty")

	want := map[string]schedulerstate.RestartBackoff{"svc": {RunID: "run-3", Failures: 3}}
	require.NoError(t, s.Save(ctx, want))
	backoff, err = s.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, want, backoff)

	require.NoError(t, s.Save(ctx, map[string]schedulerstate.RestartBackoff{}))
	backoff, err = s.Load(ctx)
	require.NoError(t, err)
	assert.Empty(t, backoff, "a save replaces what was there")
}
