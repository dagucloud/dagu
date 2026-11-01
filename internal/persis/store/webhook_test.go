// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

// webhookClaims holds the DAG name claims kept beside each test collection.
var webhookClaims sync.Map

// claimsOf returns the DAG name claims kept beside col. Stores over the same
// collection share them, as processes sharing a data directory do.
func claimsOf(col persis.Collection) persis.Collection {
	claims, _ := webhookClaims.LoadOrStore(col, testutil.NewMemoryBackend().Collection("webhook_dag_claims"))
	return claims.(persis.Collection)
}

func newWebhookStore(t *testing.T) *store.WebhookStore {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	s, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)
	return s
}

// conflictingCollection runs write once, right after the first Get, to
// simulate another process changing a record between a store's read and write.
type conflictingCollection struct {
	persis.Collection
	once  sync.Once
	write func()
}

func (c *conflictingCollection) Get(ctx context.Context, id string) (*persis.Record, error) {
	rec, err := c.Collection.Get(ctx, id)
	c.once.Do(c.write)
	return rec, err
}

// newConflictingWebhookStores creates wh through one store and returns it with
// a second store over the same collection. The second store's first read is
// followed by otherWrite, run through the first store.
func newConflictingWebhookStores(
	t *testing.T,
	wh *auth.Webhook,
	otherWrite func(other *store.WebhookStore),
) (s, other *store.WebhookStore) {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	other, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)
	require.NoError(t, other.Create(context.Background(), wh))

	s, err = store.NewWebhookStore(&conflictingCollection{
		Collection: col,
		write:      func() { otherWrite(other) },
	}, claimsOf(col), nil)
	require.NoError(t, err)
	return s, other
}

func newWebhook(dagName string) *auth.Webhook {
	now := time.Now().UTC()
	return &auth.Webhook{
		ID:          "wh-" + dagName,
		DAGName:     dagName,
		TokenHash:   "hash-" + dagName,
		TokenPrefix: "tok1",
		Enabled:     true,
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   "admin",
	}
}

func TestWebhookCreate(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("my-dag")

	require.NoError(t, s.Create(ctx, wh))

	got, err := s.GetByID(ctx, wh.ID)
	require.NoError(t, err)
	assert.Equal(t, wh.ID, got.ID)
	assert.Equal(t, wh.DAGName, got.DAGName)
	assert.Equal(t, wh.TokenHash, got.TokenHash)
	assert.Equal(t, wh.Enabled, got.Enabled)
}

func TestWebhookCreate_DuplicateDAGName(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	require.NoError(t, s.Create(ctx, newWebhook("dag-a")))

	duplicate := newWebhook("dag-a")
	duplicate.ID = "different-id"
	err := s.Create(ctx, duplicate)
	assert.ErrorIs(t, err, auth.ErrWebhookAlreadyExists)
}

func TestWebhookGetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	_, err := s.GetByID(ctx, "no-such-id")
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookGetByDAGName(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-x")
	require.NoError(t, s.Create(ctx, wh))

	got, err := s.GetByDAGName(ctx, "dag-x")
	require.NoError(t, err)
	assert.Equal(t, wh.ID, got.ID)
}

func TestWebhookGetByDAGName_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	_, err := s.GetByDAGName(ctx, "unknown-dag")
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookList(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	for _, name := range []string{"d1", "d2", "d3"} {
		require.NoError(t, s.Create(ctx, newWebhook(name)))
	}

	list, err := s.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestWebhookUpdateByDAGName(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-mutate")
	require.NoError(t, s.Create(ctx, wh))
	require.NoError(t, s.UpdateLastUsed(ctx, wh.ID, ""))

	updated, err := s.UpdateByDAGName(ctx, "dag-mutate", func(w *auth.Webhook) error {
		w.Enabled = false
		return nil
	})
	require.NoError(t, err)
	assert.False(t, updated.Enabled)

	got, err := s.GetByID(ctx, wh.ID)
	require.NoError(t, err)
	assert.False(t, got.Enabled)
	// The mutation starts from the stored webhook, so fields written by
	// other calls since the caller last read it are kept.
	assert.NotNil(t, got.LastUsedAt)
}

func TestWebhookUpdateByDAGName_MutateErrorAbortsWrite(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-mutate-error")
	require.NoError(t, s.Create(ctx, wh))

	errRejected := errors.New("rejected")
	_, err := s.UpdateByDAGName(ctx, "dag-mutate-error", func(w *auth.Webhook) error {
		w.Enabled = false
		return errRejected
	})
	assert.ErrorIs(t, err, errRejected)

	got, err := s.GetByID(ctx, wh.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled)
}

func TestWebhookUpdateByDAGName_KeepsConcurrentWrite(t *testing.T) {
	ctx := context.Background()
	wh := newWebhook("dag-mutate-conflict")
	s, other := newConflictingWebhookStores(t, wh, func(other *store.WebhookStore) {
		_, err := other.UpdateByDAGName(ctx, wh.DAGName, func(w *auth.Webhook) error {
			w.AllowedProfiles = []string{"prod"}
			return nil
		})
		require.NoError(t, err)
	})

	_, err := s.UpdateByDAGName(ctx, wh.DAGName, func(w *auth.Webhook) error {
		w.Enabled = false
		return nil
	})
	require.NoError(t, err)

	got, err := other.GetByID(ctx, wh.ID)
	require.NoError(t, err)
	assert.False(t, got.Enabled)
	assert.Equal(t, []string{"prod"}, got.AllowedProfiles)
}

func TestWebhookUpdateByDAGName_NotFound(t *testing.T) {
	s := newWebhookStore(t)

	_, err := s.UpdateByDAGName(context.Background(), "missing", func(*auth.Webhook) error { return nil })
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookDelete(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-del")
	require.NoError(t, s.Create(ctx, wh))

	require.NoError(t, s.Delete(ctx, wh.ID))

	_, err := s.GetByID(ctx, wh.ID)
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)

	// DAGName index cleaned up
	_, err = s.GetByDAGName(ctx, wh.DAGName)
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookDelete_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	err := s.Delete(ctx, "nonexistent")
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookDeleteByDAGName(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-dbn")
	require.NoError(t, s.Create(ctx, wh))

	require.NoError(t, s.DeleteByDAGName(ctx, "dag-dbn"))

	_, err := s.GetByID(ctx, wh.ID)
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookDeleteByDAGName_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	err := s.DeleteByDAGName(ctx, "no-dag")
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookUpdateLastUsed(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-lu")
	require.NoError(t, s.Create(ctx, wh))

	before := time.Now().UTC()
	require.NoError(t, s.UpdateLastUsed(ctx, wh.ID, ""))

	got, err := s.GetByID(ctx, wh.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)
	assert.True(t, got.LastUsedAt.After(before) || got.LastUsedAt.Equal(before))
}

func TestWebhookUpdateLastUsed_ProfileToken(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-lu-token")
	used := auth.NewWebhookProfileToken("a", "profile-a", "hash-a", "tok-a", "admin")
	unused := auth.NewWebhookProfileToken("b", "profile-b", "hash-b", "tok-b", "admin")
	wh.ProfileTokens = []auth.WebhookProfileToken{unused, used}
	require.NoError(t, s.Create(ctx, wh))

	require.NoError(t, s.UpdateLastUsed(ctx, wh.ID, used.ID))

	got, err := s.GetByID(ctx, wh.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)
	require.Len(t, got.ProfileTokens, 2)
	assert.Nil(t, got.ProfileTokens[0].LastUsedAt)
	assert.Equal(t, used.ID, got.ProfileTokens[1].ID)
	assert.Equal(t, got.LastUsedAt, got.ProfileTokens[1].LastUsedAt)
	assert.Equal(t, "hash-a", got.ProfileTokens[1].TokenHash)
}

// A last-used update must not restore a profile token that another process
// revoked after the update read the webhook.
func TestWebhookUpdateLastUsed_KeepsConcurrentRevoke(t *testing.T) {
	ctx := context.Background()
	wh := newWebhook("dag-lu-revoke")
	token := auth.NewWebhookProfileToken("a", "profile-a", "hash-a", "tok-a", "admin")
	wh.ProfileTokens = []auth.WebhookProfileToken{token}
	s, other := newConflictingWebhookStores(t, wh, func(other *store.WebhookStore) {
		_, err := other.UpdateByDAGName(ctx, wh.DAGName, func(w *auth.Webhook) error {
			w.ProfileTokens = nil
			return nil
		})
		require.NoError(t, err)
	})

	require.NoError(t, s.UpdateLastUsed(ctx, wh.ID, token.ID))

	got, err := other.GetByID(ctx, wh.ID)
	require.NoError(t, err)
	assert.Empty(t, got.ProfileTokens)
	assert.NotNil(t, got.LastUsedAt)
}

// Use within a minute of the recorded use is not written again, so a busy
// webhook does not rewrite its record on every request. A profile token's
// first use is still recorded.
func TestWebhookUpdateLastUsed_Throttled(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-lu-throttle")
	token := auth.NewWebhookProfileToken("a", "profile-a", "hash-a", "tok-a", "admin")
	wh.ProfileTokens = []auth.WebhookProfileToken{token}
	require.NoError(t, s.Create(ctx, wh))

	require.NoError(t, s.UpdateLastUsed(ctx, wh.ID, ""))
	first, err := s.GetByID(ctx, wh.ID)
	require.NoError(t, err)
	require.NotNil(t, first.LastUsedAt)

	require.NoError(t, s.UpdateLastUsed(ctx, wh.ID, token.ID))
	got, err := s.GetByID(ctx, wh.ID)
	require.NoError(t, err)
	assert.Equal(t, first.LastUsedAt, got.LastUsedAt)
	assert.NotNil(t, got.ProfileTokens[0].LastUsedAt)
}

func TestWebhookUpdateLastUsed_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	err := s.UpdateLastUsed(ctx, "nonexistent", "")
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookIndexRebuiltOnStartup(t *testing.T) {
	ctx := context.Background()

	// Put records directly into the collection, then create a new store
	// to verify the index is rebuilt correctly from persisted data.
	col := testutil.NewMemoryBackend().Collection("webhooks")

	s1, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)
	require.NoError(t, s1.Create(ctx, newWebhook("d1")))
	require.NoError(t, s1.Create(ctx, newWebhook("d2")))

	// Create a second store over the same collection (simulates restart).
	s2, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)

	got, err := s2.GetByDAGName(ctx, "d1")
	require.NoError(t, err)
	assert.Equal(t, "wh-d1", got.ID)

	got, err = s2.GetByDAGName(ctx, "d2")
	require.NoError(t, err)
	assert.Equal(t, "wh-d2", got.ID)
}

// A store sees the webhooks another process sharing its collection creates
// and deletes after the store started.
// A recent miss does not let a create overlook a webhook another process
// created since.
func TestWebhookCreateSeesOtherProcessesAfterAMiss(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	s1, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)
	s2, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)

	_, err = s2.GetByDAGName(ctx, "d1")
	require.ErrorIs(t, err, auth.ErrWebhookNotFound)
	require.NoError(t, s1.Create(ctx, newWebhook("d1")))

	second := newWebhook("d1")
	second.ID = "wh-d1-second"
	assert.ErrorIs(t, s2.Create(ctx, second), auth.ErrWebhookAlreadyExists)
}

func TestWebhookSeesOtherProcesses(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	s1, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)
	s2, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)

	require.NoError(t, s1.Create(ctx, newWebhook("d1")))
	got, err := s2.GetByDAGName(ctx, "d1")
	require.NoError(t, err)
	assert.Equal(t, "wh-d1", got.ID)
	_, err = s2.UpdateByDAGName(ctx, "d1", func(wh *auth.Webhook) error {
		wh.Enabled = false
		return nil
	})
	require.NoError(t, err)
	assert.ErrorIs(t, s2.Create(ctx, newWebhook("d1")), auth.ErrWebhookAlreadyExists)

	require.NoError(t, s2.DeleteByDAGName(ctx, "d1"))
	assert.NoError(t, s1.Create(ctx, newWebhook("d1")), "a webhook deleted elsewhere can be created again")
}

// listCountingCollection counts how often the whole collection is read.
type listCountingCollection struct {
	persis.Collection
	lists atomic.Int32
}

func (c *listCountingCollection) List(ctx context.Context, q persis.ListQuery) (*persis.Page, error) {
	c.lists.Add(1)
	return c.Collection.List(ctx, q)
}

// Requests for DAGs without a webhook, such as unauthenticated triggers with
// made-up names, cannot make each lookup reread the whole collection.
func TestWebhookMissesShareARereading(t *testing.T) {
	ctx := context.Background()
	col := &listCountingCollection{Collection: testutil.NewMemoryBackend().Collection("webhooks")}
	s, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)
	col.lists.Store(0)

	for i := range 50 {
		_, err := s.GetByDAGName(ctx, fmt.Sprintf("unknown-%d", i))
		require.ErrorIs(t, err, auth.ErrWebhookNotFound)
	}
	assert.Equal(t, int32(1), col.lists.Load())
}

// A complete listing refreshes the lookups, so a webhook another process
// created is found as soon as it is listed.
func TestWebhookListCompleteRefreshesLookups(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	s1, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)
	s2, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)

	_, err = s1.GetByDAGName(ctx, "d1")
	require.ErrorIs(t, err, auth.ErrWebhookNotFound)
	require.NoError(t, s2.Create(ctx, newWebhook("d1")))
	_, err = s1.ListComplete(ctx)
	require.NoError(t, err)

	got, err := s1.GetByDAGName(ctx, "d1")
	require.NoError(t, err)
	assert.Equal(t, "wh-d1", got.ID)
}

// A record the file backend cannot parse makes a complete listing fail
// instead of leaving the webhook out.
func TestWebhookListCompleteReportsCorruptFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	backend := file.NewBackend(config.PathsConfig{DataDir: dir, WebhooksDir: filepath.Join(dir, "webhooks")})
	s, err := store.NewWebhookStore(backend.Collection(persis.CollectionWebhooks), backend.Collection(persis.CollectionWebhookDAGClaims), nil)
	require.NoError(t, err)
	require.NoError(t, s.Create(ctx, newWebhook("d1")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "webhooks", "broken.json"), []byte("{not json"), 0o600))

	_, err = s.ListComplete(ctx)
	require.Error(t, err)
	some, err := s.List(ctx)
	require.NoError(t, err)
	assert.Len(t, some, 1)
}

// listingCollection, once armed, runs during in its own goroutine while the
// next listing is under way, and waits briefly for it to finish. Listings
// during makes pass straight through.
type listingCollection struct {
	persis.Collection
	armed  atomic.Bool
	during func()
}

func (c *listingCollection) List(ctx context.Context, q persis.ListQuery) (*persis.Page, error) {
	page, err := c.Collection.List(ctx, q)
	if !c.armed.CompareAndSwap(true, false) {
		return page, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.during()
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
	}
	return page, err
}

// A webhook created while a listing is under way is not lost from the
// lookups.
func TestWebhookListCompleteKeepsConcurrentCreates(t *testing.T) {
	ctx := context.Background()
	col := &listingCollection{Collection: testutil.NewMemoryBackend().Collection("webhooks")}
	s, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)
	require.NoError(t, s.Create(ctx, newWebhook("d0")))
	// A recent miss keeps the next one from rereading the collection.
	_, err = s.GetByDAGName(ctx, "unknown")
	require.ErrorIs(t, err, auth.ErrWebhookNotFound)
	created := make(chan error, 1)
	col.during = func() { created <- s.Create(ctx, newWebhook("d1")) }
	col.armed.Store(true)

	_, err = s.ListComplete(ctx)
	require.NoError(t, err)
	require.NoError(t, <-created)

	got, err := s.GetByDAGName(ctx, "d1")
	require.NoError(t, err)
	assert.Equal(t, "wh-d1", got.ID)
}

func newEncryptedWebhookStore(t *testing.T, col persis.Collection) *store.WebhookStore {
	t.Helper()
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	s, err := store.NewWebhookStore(col, claimsOf(col), enc)
	require.NoError(t, err)
	return s
}

// A webhook follows its DAG to a new name with its tokens and public URL,
// and other processes see it there.
func TestWebhookRenameDAG(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	s := newEncryptedWebhookStore(t, col)
	other := newEncryptedWebhookStore(t, col)
	wh := newWebhook("d1")
	wh.Public = &auth.WebhookPublic{Token: "public-token", CreatedAt: time.Now().UTC().Truncate(time.Second)}
	require.NoError(t, s.Create(ctx, wh))
	require.NoError(t, s.Create(ctx, newWebhook("d2")))

	require.ErrorIs(t, s.RenameDAG(ctx, "d1", "d2"), auth.ErrWebhookAlreadyExists)
	require.ErrorIs(t, s.RenameDAG(ctx, "missing", "d3"), auth.ErrWebhookNotFound)
	require.ErrorIs(t, s.RenameDAG(ctx, "missing", "d2"), auth.ErrWebhookAlreadyExists, "a webhook at the new name is refused first")
	require.NoError(t, s.RenameDAG(ctx, "d1", "d3"))

	_, err := s.GetByDAGName(ctx, "d1")
	require.ErrorIs(t, err, auth.ErrWebhookNotFound)
	for _, st := range []*store.WebhookStore{s, other} {
		got, err := st.GetByDAGName(ctx, "d3")
		require.NoError(t, err)
		assert.Equal(t, wh.ID, got.ID)
		assert.Equal(t, "d3", got.DAGName)
		assert.Equal(t, wh.TokenHash, got.TokenHash)
		assert.Equal(t, wh.Public, got.Public)
	}
}

// A public URL's token and sender secret are stored encrypted and read back
// as they were.
func TestWebhookPublicURL(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	s := newEncryptedWebhookStore(t, col)
	wh := newWebhook("d1")
	created := time.Now().UTC().Truncate(time.Second)
	wh.Public = &auth.WebhookPublic{
		Token:        "public-token",
		Sender:       "github",
		SenderSecret: "sender-secret",
		Events:       []string{"issues"},
		CreatedAt:    created,
	}
	require.NoError(t, s.Create(ctx, wh))

	rec, err := col.Get(ctx, wh.ID)
	require.NoError(t, err)
	assert.NotContains(t, string(rec.Data), "public-token")
	assert.NotContains(t, string(rec.Data), "sender-secret")

	got, err := s.GetByDAGName(ctx, "d1")
	require.NoError(t, err)
	assert.Equal(t, wh.Public, got.Public)

	unencrypted, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)
	_, err = unencrypted.GetByDAGName(ctx, "d1")
	assert.ErrorIs(t, err, auth.ErrWebhookPublicEncryptorRequired)
	assert.ErrorIs(t, unencrypted.Create(ctx, wh), auth.ErrWebhookPublicEncryptorRequired)
}

// ListComplete fails rather than leave out a webhook it cannot read, which
// List skips.
func TestWebhookListComplete(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	s := newEncryptedWebhookStore(t, col)
	require.NoError(t, s.Create(ctx, newWebhook("d1")))
	public := newWebhook("d2")
	public.Public = &auth.WebhookPublic{Token: "public-token"}
	require.NoError(t, s.Create(ctx, public))

	all, err := s.ListComplete(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	unencrypted, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)
	_, err = unencrypted.ListComplete(ctx)
	require.ErrorIs(t, err, auth.ErrWebhookPublicEncryptorRequired)
	some, err := unencrypted.List(ctx)
	require.NoError(t, err)
	assert.Len(t, some, 1)
}

// webhooksFor returns the webhooks a listing finds for dagName.
func webhooksFor(t *testing.T, s *store.WebhookStore, dagName string) []*auth.Webhook {
	t.Helper()
	all, err := s.ListComplete(context.Background())
	require.NoError(t, err)
	var found []*auth.Webhook
	for _, wh := range all {
		if wh.DAGName == dagName {
			found = append(found, wh)
		}
	}
	return found
}

// Two processes creating a DAG's webhook at once, each having read that it
// has none, leave the DAG one webhook: the other create is refused.
func TestWebhookCreateIsUniqueAcrossProcesses(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	listing := &listingCollection{Collection: col}
	s1, err := store.NewWebhookStore(listing, claimsOf(col), nil)
	require.NoError(t, err)
	s2, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)

	other := newWebhook("d1")
	other.ID = "wh-d1-other"
	created := make(chan error, 1)
	// s2 creates while s1 has read the collection and not yet written.
	listing.during = func() { created <- s2.Create(ctx, other) }
	listing.armed.Store(true)
	err1 := s1.Create(ctx, newWebhook("d1"))
	err2 := <-created

	succeeded := 0
	for _, err := range []error{err1, err2} {
		if err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, auth.ErrWebhookAlreadyExists)
		}
	}
	assert.Equal(t, 1, succeeded, "exactly one create wins")
	assert.Len(t, webhooksFor(t, s2, "d1"), 1)
}

// A rename onto a name another process claims first is refused, and the
// webhook stays with its DAG.
func TestWebhookRenameYieldsToAConcurrentCreate(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	listing := &listingCollection{Collection: col}
	s1, err := store.NewWebhookStore(listing, claimsOf(col), nil)
	require.NoError(t, err)
	s2, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)
	require.NoError(t, s1.Create(ctx, newWebhook("d1")))

	created := make(chan error, 1)
	listing.during = func() { created <- s2.Create(ctx, newWebhook("d2")) }
	listing.armed.Store(true)
	require.ErrorIs(t, s1.RenameDAG(ctx, "d1", "d2"), auth.ErrWebhookAlreadyExists)
	require.NoError(t, <-created)

	stayed := webhooksFor(t, s2, "d1")
	require.Len(t, stayed, 1)
	assert.Equal(t, "wh-d1", stayed[0].ID)
	taken := webhooksFor(t, s2, "d2")
	require.Len(t, taken, 1)
	assert.Equal(t, "wh-d2", taken[0].ID)
}

// A claim left over by a crash, whose webhook is gone or now belongs to
// another DAG, does not keep the DAG from getting a webhook.
func TestWebhookClaimLeftOverIsTakenOver(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	s, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)
	require.NoError(t, s.Create(ctx, newWebhook("d1")))
	// The record went away without its claim, as a crash mid-delete leaves.
	require.NoError(t, col.Delete(ctx, "wh-d1"))
	fresh, err := store.NewWebhookStore(col, claimsOf(col), nil)
	require.NoError(t, err)

	replacement := newWebhook("d1")
	replacement.ID = "wh-d1-new"
	require.NoError(t, fresh.Create(ctx, replacement))
	assert.Len(t, webhooksFor(t, fresh, "d1"), 1)
}

// pausingCollection, once armed, pauses the next listing until resumed, as a
// process that has read the collection and not yet written.
type pausingCollection struct {
	persis.Collection
	armed   atomic.Bool
	paused  chan struct{}
	resumed chan struct{}
}

func (c *pausingCollection) List(ctx context.Context, q persis.ListQuery) (*persis.Page, error) {
	page, err := c.Collection.List(ctx, q)
	if c.armed.CompareAndSwap(true, false) {
		close(c.paused)
		<-c.resumed
	}
	return page, err
}

// claimingCollection, once armed, runs during before the next claim is
// written, as other processes acting between a store's steps.
type claimingCollection struct {
	persis.Collection
	armed  atomic.Bool
	during func()
}

func (c *claimingCollection) Create(ctx context.Context, rec *persis.Record) error {
	if c.armed.CompareAndSwap(true, false) {
		c.during()
	}
	return c.Collection.Create(ctx, rec)
}

// A rename that loses the new name moves back, and gives way when another
// process gave the old name a webhook meanwhile, so no DAG gets two.
func TestWebhookRenameRollbackKeepsOneWebhookPerDAG(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	claims := claimsOf(col)
	renaming := &claimingCollection{Collection: claims}
	a, err := store.NewWebhookStore(col, renaming, nil)
	require.NoError(t, err)
	b, err := store.NewWebhookStore(col, claims, nil)
	require.NoError(t, err)
	reading := &pausingCollection{Collection: col, paused: make(chan struct{}), resumed: make(chan struct{})}
	z, err := store.NewWebhookStore(reading, claims, nil)
	require.NoError(t, err)
	require.NoError(t, a.Create(ctx, newWebhook("d1")))

	// z reads that d2 is free before the rename starts.
	reading.armed.Store(true)
	zDone := make(chan error, 1)
	go func() { zDone <- z.Create(ctx, newWebhook("d2")) }()
	<-reading.paused

	// While a's webhook names d2 and before a claims it, z claims d2 and b
	// gives d1, whose claim now looks left over, a webhook.
	other := newWebhook("d1")
	other.ID = "wh-d1-other"
	var zErr, bErr error
	renaming.during = func() {
		close(reading.resumed)
		zErr = <-zDone
		bErr = b.Create(ctx, other)
	}
	renaming.armed.Store(true)

	require.ErrorIs(t, a.RenameDAG(ctx, "d1", "d2"), auth.ErrWebhookAlreadyExists)
	require.NoError(t, zErr)
	require.NoError(t, bErr)
	d1 := webhooksFor(t, b, "d1")
	require.Len(t, d1, 1)
	assert.Equal(t, "wh-d1-other", d1[0].ID)
	d2 := webhooksFor(t, b, "d2")
	require.Len(t, d2, 1)
	assert.Equal(t, "wh-d2", d2[0].ID)
}

// swappingCollection, once armed, runs during after the next swap, as a
// caller that gives up while a write is half done.
type swappingCollection struct {
	persis.Collection
	armed  atomic.Bool
	during func()
}

func (c *swappingCollection) CompareAndSwap(ctx context.Context, id string, expected, next []byte) error {
	err := c.Collection.CompareAndSwap(ctx, id, expected, next)
	if c.armed.CompareAndSwap(true, false) {
		c.during()
	}
	return err
}

// A rename the caller gives up on after its webhook moved still moves the
// webhook back.
func TestWebhookRenameUndoesAfterCancel(t *testing.T) {
	col := testutil.NewMemoryBackend().Collection("webhooks")
	swapping := &swappingCollection{Collection: col}
	s, err := store.NewWebhookStore(swapping, claimsOf(col), nil)
	require.NoError(t, err)
	require.NoError(t, s.Create(context.Background(), newWebhook("d1")))

	ctx, cancel := context.WithCancel(context.Background())
	swapping.during = cancel
	swapping.armed.Store(true)
	require.ErrorIs(t, s.RenameDAG(ctx, "d1", "d2"), context.Canceled)

	got, err := s.GetByDAGName(context.Background(), "d1")
	require.NoError(t, err)
	assert.Equal(t, "wh-d1", got.ID)
	assert.Empty(t, webhooksFor(t, s, "d2"))
}
