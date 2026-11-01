// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/publicwebhook"
)

func newPublicWebhookStore() *store.PublicWebhookStore {
	return store.NewPublicWebhookStore(testutil.NewMemoryBackend().Collection("public_webhooks"))
}

// A log keeps the latest deliveries, newest first, and a delivery sent again
// replaces its earlier entry.
func TestPublicWebhookLog(t *testing.T) {
	ctx := context.Background()
	s := newPublicWebhookStore()

	log, err := s.Log(ctx, "wh-1")
	require.NoError(t, err)
	assert.Empty(t, log.Deliveries)

	for i := range publicwebhook.LogSize + 2 {
		require.NoError(t, s.AddDelivery(ctx, "wh-1", publicwebhook.Delivery{ID: fmt.Sprintf("d-%d", i), Outcome: publicwebhook.OutcomeStarted}))
	}
	require.NoError(t, s.AddDelivery(ctx, "wh-1", publicwebhook.Delivery{ID: "d-15", Outcome: publicwebhook.OutcomeWaiting}))

	log, err = s.Log(ctx, "wh-1")
	require.NoError(t, err)
	require.Len(t, log.Deliveries, publicwebhook.LogSize)
	assert.Equal(t, publicwebhook.Delivery{ID: "d-15", Outcome: publicwebhook.OutcomeWaiting}, log.Deliveries[0])
	assert.Equal(t, "d-21", log.Deliveries[1].ID)
	ids := make([]string, len(log.Deliveries))
	for i, d := range log.Deliveries {
		ids[i] = d.ID
	}
	assert.NotContains(t, ids, "d-0", "the oldest deliveries are dropped")
	assert.Len(t, ids, publicwebhook.LogSize)

	other, err := s.Log(ctx, "wh-2")
	require.NoError(t, err)
	assert.Empty(t, other.Deliveries, "logs are per webhook")

	require.NoError(t, s.DeleteLog(ctx, "wh-1"))
	log, err = s.Log(ctx, "wh-1")
	require.NoError(t, err)
	assert.Empty(t, log.Deliveries)
}

func TestPublicWebhookMissed(t *testing.T) {
	ctx := context.Background()
	s := newPublicWebhookStore()
	first := time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)

	require.NoError(t, s.AddMissed(ctx, "wh-1", 2, 0, first))
	require.NoError(t, s.AddMissed(ctx, "wh-1", 1, 3, first.Add(time.Hour)))
	log, err := s.Log(ctx, "wh-1")
	require.NoError(t, err)
	assert.Equal(t, publicwebhook.Missed{Expired: 3, Refused: 3, Since: first}, log.Missed)

	require.NoError(t, s.ClearMissed(ctx, "wh-1"))
	log, err = s.Log(ctx, "wh-1")
	require.NoError(t, err)
	assert.Equal(t, publicwebhook.Missed{}, log.Missed)
}

// Deliveries added at once, as from several in flight, are all kept.
func TestPublicWebhookConcurrentDeliveries(t *testing.T) {
	ctx := context.Background()
	s := newPublicWebhookStore()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			assert.NoError(t, s.AddDelivery(ctx, "wh-1", publicwebhook.Delivery{ID: fmt.Sprintf("d-%d", i)}))
		})
	}
	wg.Wait()
	log, err := s.Log(ctx, "wh-1")
	require.NoError(t, err)
	assert.Len(t, log.Deliveries, 8)
}

func TestPublicWebhookStatus(t *testing.T) {
	ctx := context.Background()
	s := newPublicWebhookStore()

	status, err := s.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, publicwebhook.Status{}, status)

	want := publicwebhook.Status{
		Connected: true,
		URL:       "https://hooks.example/obj",
		MaxHooks:  1,
		LeftOut:   []string{"d2"},
		UpdatedAt: time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC),
	}
	require.NoError(t, s.SetStatus(ctx, want))
	status, err = s.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, want, status)
}
