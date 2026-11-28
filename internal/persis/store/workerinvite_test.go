// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

var inviteCreatedAt = time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)

func newWorkerInviteStore(t *testing.T, col persis.Collection) *store.WorkerInviteStore {
	t.Helper()
	s, err := store.NewWorkerInviteStore(col)
	require.NoError(t, err)
	return s
}

func newMemoryWorkerInviteStore(t *testing.T) *store.WorkerInviteStore {
	t.Helper()
	return newWorkerInviteStore(t, testutil.NewMemoryBackend().Collection(persis.CollectionWorkerInvites))
}

func secretHash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func workerInvite(secret string, kind persis.WorkerInviteKind) persis.WorkerInvite {
	return persis.WorkerInvite{
		SecretHash: secretHash(secret),
		Kind:       kind,
		Address:    "192.168.1.20:50055",
		CreatedBy:  "admin",
		CreatedAt:  inviteCreatedAt,
	}
}

func createInvite(t *testing.T, s *store.WorkerInviteStore, invite persis.WorkerInvite) persis.WorkerInvite {
	t.Helper()
	created, err := s.Create(t.Context(), invite)
	require.NoError(t, err)
	return created
}

func TestWorkerInviteStore_CreateAndFind(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newMemoryWorkerInviteStore(t)

	first := createInvite(t, s, workerInvite("secret-a", persis.WorkerInviteSingleUse))
	later := workerInvite("secret-b", persis.WorkerInviteReusable)
	later.CreatedAt = inviteCreatedAt.Add(time.Minute)
	later.ExpiresAt = inviteCreatedAt.Add(time.Hour)
	second := createInvite(t, s, later)
	assert.NotEmpty(t, first.ID)
	assert.NotEqual(t, first.ID, second.ID)

	got, err := s.Get(ctx, second.ID)
	require.NoError(t, err)
	assert.Equal(t, second, got)

	got, err = s.GetBySecretHash(ctx, secretHash("secret-a"))
	require.NoError(t, err)
	assert.Equal(t, first, got)

	invites, err := s.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []persis.WorkerInvite{first, second}, invites)

	_, err = s.GetBySecretHash(ctx, secretHash("secret-c"))
	assert.ErrorIs(t, err, persis.ErrWorkerInviteNotFound)
	_, err = s.Get(ctx, "missing")
	assert.ErrorIs(t, err, persis.ErrWorkerInviteNotFound)
}

// List keeps creation order on the file backend after an invite is used.
func TestWorkerInviteStore_ListOldestFirst(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newWorkerInviteStore(t, file.NewBackend(config.PathsConfig{DataDir: t.TempDir()}).Collection(persis.CollectionWorkerInvites))
	first := createInvite(t, s, workerInvite("secret-a", persis.WorkerInviteReusable))
	later := workerInvite("secret-b", persis.WorkerInviteReusable)
	later.CreatedAt = inviteCreatedAt.Add(time.Minute)
	second := createInvite(t, s, later)

	first, err := s.Use(ctx, first.ID, inviteCreatedAt.Add(time.Hour))
	require.NoError(t, err)

	invites, err := s.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []persis.WorkerInvite{first, second}, invites)
}

func TestWorkerInviteStore_RecordHoldsNoSecret(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	col := file.NewBackend(config.PathsConfig{DataDir: dataDir}).Collection(persis.CollectionWorkerInvites)
	const secret = "dGhpcy1pcy1hLXRlc3Qtc2VjcmV0LW9mLWZvcnR5LXRocmVl"
	invite := createInvite(t, newWorkerInviteStore(t, col), workerInvite(secret, persis.WorkerInviteSingleUse))

	raw, err := os.ReadFile(filepath.Join(dataDir, "worker-invites", invite.ID+".json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), secret)
}

func TestWorkerInviteStore_Delete(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newMemoryWorkerInviteStore(t)
	invite := createInvite(t, s, workerInvite("secret", persis.WorkerInviteReusable))

	require.NoError(t, s.Delete(ctx, invite.ID))

	_, err := s.GetBySecretHash(ctx, invite.SecretHash)
	assert.ErrorIs(t, err, persis.ErrWorkerInviteNotFound)
	_, err = s.Use(ctx, invite.ID, inviteCreatedAt)
	assert.ErrorIs(t, err, persis.ErrWorkerInviteNotFound)
	assert.ErrorIs(t, s.Delete(ctx, invite.ID), persis.ErrWorkerInviteNotFound)
}

func TestWorkerInviteStore_Use(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newMemoryWorkerInviteStore(t)
	usedAt := inviteCreatedAt.Add(time.Hour)

	single := createInvite(t, s, workerInvite("single", persis.WorkerInviteSingleUse))
	used, err := s.Use(ctx, single.ID, usedAt)
	require.NoError(t, err)
	assert.Equal(t, 1, used.UseCount)
	assert.True(t, used.LastUsedAt.Equal(usedAt))
	_, err = s.Use(ctx, single.ID, usedAt.Add(time.Second))
	assert.ErrorIs(t, err, persis.ErrWorkerInviteEnded)

	reusable := createInvite(t, s, workerInvite("reusable", persis.WorkerInviteReusable))
	for range 3 {
		_, err := s.Use(ctx, reusable.ID, usedAt)
		require.NoError(t, err)
	}
	got, err := s.Get(ctx, reusable.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, got.UseCount)
}

// An invite ends at its expiry, whatever its kind.
func TestWorkerInviteStore_UseAfterExpiry(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newMemoryWorkerInviteStore(t)
	expiring := workerInvite("reusable", persis.WorkerInviteReusable)
	expiring.ExpiresAt = inviteCreatedAt.Add(24 * time.Hour)
	invite := createInvite(t, s, expiring)

	_, err := s.Use(ctx, invite.ID, expiring.ExpiresAt.Add(-time.Nanosecond))
	require.NoError(t, err)
	_, err = s.Use(ctx, invite.ID, expiring.ExpiresAt)
	assert.ErrorIs(t, err, persis.ErrWorkerInviteEnded)
}

// Prune deletes an invite's record 7 days after the invite was used up or
// expired, and keeps a reusable invite without an expiry (Spec 108 §Invite
// Records).
func TestWorkerInviteStore_Prune(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newMemoryWorkerInviteStore(t)
	const retention = 7 * 24 * time.Hour
	usedAt := inviteCreatedAt.Add(time.Hour)
	expiresAt := inviteCreatedAt.Add(24 * time.Hour)

	used := createInvite(t, s, workerInvite("used", persis.WorkerInviteSingleUse))
	_, err := s.Use(ctx, used.ID, usedAt)
	require.NoError(t, err)
	expiringSingle := workerInvite("expiring-single", persis.WorkerInviteSingleUse)
	expiringSingle.ExpiresAt = expiresAt
	expired := createInvite(t, s, expiringSingle)
	expiringReusable := workerInvite("expiring-reusable", persis.WorkerInviteReusable)
	expiringReusable.ExpiresAt = expiresAt
	expiredReusable := createInvite(t, s, expiringReusable)
	_, err = s.Use(ctx, expiredReusable.ID, usedAt)
	require.NoError(t, err)
	forever := createInvite(t, s, workerInvite("forever", persis.WorkerInviteReusable))
	_, err = s.Use(ctx, forever.ID, usedAt)
	require.NoError(t, err)

	remaining := func() []string {
		invites, err := s.List(ctx)
		require.NoError(t, err)
		ids := make([]string, 0, len(invites))
		for _, invite := range invites {
			ids = append(ids, invite.ID)
		}
		return ids
	}

	removed, err := s.Prune(ctx, usedAt.Add(retention))
	require.NoError(t, err)
	assert.Zero(t, removed, "an invite stays for 7 days after it ends")

	removed, err = s.Prune(ctx, usedAt.Add(retention+time.Second))
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.ElementsMatch(t, []string{expired.ID, expiredReusable.ID, forever.ID}, remaining())

	removed, err = s.Prune(ctx, expiresAt.Add(retention+time.Second))
	require.NoError(t, err)
	assert.Equal(t, 2, removed)
	assert.Equal(t, []string{forever.ID}, remaining())

	removed, err = s.Prune(ctx, expiresAt.Add(100*retention))
	require.NoError(t, err)
	assert.Zero(t, removed)
}

// Coordinators that share a data directory each open their own store, and
// of their concurrent uses of one single-use invite exactly one succeeds.
func TestWorkerInviteStore_SingleUseOnce(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	coordinators := make([]*store.WorkerInviteStore, 2)
	for i := range coordinators {
		col := file.NewBackend(config.PathsConfig{DataDir: dataDir}).Collection(persis.CollectionWorkerInvites)
		coordinators[i] = newWorkerInviteStore(t, col)
	}
	invite := createInvite(t, coordinators[0], workerInvite("single", persis.WorkerInviteSingleUse))

	errs := useConcurrently(t, invite.ID, coordinators[0], coordinators[1], coordinators[0], coordinators[1])

	var wins, ended int
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, persis.ErrWorkerInviteEnded):
			ended++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, wins)
	assert.Equal(t, len(errs)-1, ended)
	got, err := coordinators[1].Get(t.Context(), invite.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, got.UseCount)
}

// Concurrent uses of a reusable invite all count.
func TestWorkerInviteStore_ReusableCountsEveryUse(t *testing.T) {
	t.Parallel()
	s := newMemoryWorkerInviteStore(t)
	invite := createInvite(t, s, workerInvite("reusable", persis.WorkerInviteReusable))

	errs := useConcurrently(t, invite.ID, s, s, s, s, s, s, s, s)

	for _, err := range errs {
		require.NoError(t, err)
	}
	got, err := s.Get(t.Context(), invite.ID)
	require.NoError(t, err)
	assert.Equal(t, len(errs), got.UseCount)
}

// useConcurrently starts one use of the invite per store at once and
// returns their errors.
func useConcurrently(t *testing.T, id string, stores ...*store.WorkerInviteStore) []error {
	t.Helper()
	start := make(chan struct{})
	errs := make([]error, len(stores))
	var wg sync.WaitGroup
	for i, s := range stores {
		wg.Go(func() {
			<-start
			_, errs[i] = s.Use(t.Context(), id, inviteCreatedAt.Add(time.Hour))
		})
	}
	close(start)
	wg.Wait()
	return errs
}
