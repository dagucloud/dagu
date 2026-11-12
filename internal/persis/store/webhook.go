// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ auth.WebhookStore = (*WebhookStore)(nil)

// webhookLastUsedWriteInterval limits how often a busy webhook rewrites its
// record just to record use.
const webhookLastUsedWriteInterval = time.Minute

// webhookMissRebuildInterval bounds how often lookups of DAGs without a
// webhook reread the whole collection, so a burst of requests for unknown
// names costs one reread, not one each.
const webhookMissRebuildInterval = time.Second

// WebhookStore implements [auth.WebhookStore].
// DAG-name lookups use an in-memory index (byDAGName) built from the
// collection on startup, refreshed by every complete listing and every
// create, and rebuilt when a lookup misses, at most once per
// webhookMissRebuildInterval, since another process sharing the collection
// may have written since; this process's writes keep it in sync under mu.
//
// One webhook per DAG holds across processes through claims: a record per
// DAG name, created atomically, that names the webhook holding the name.
// A write stores the webhook's record first and claims its DAG name after,
// so a claim whose webhook is gone or names another DAG is known to be left
// over from a crash between the two, and may be taken over.
type WebhookStore struct {
	col       persis.Collection
	claims    persis.Collection
	encryptor *crypto.Encryptor // nil = HMAC encryption disabled

	mu        sync.RWMutex
	byDAGName map[string]string // dagName → webhookID

	missMu        sync.Mutex
	missRebuiltAt time.Time // guarded by missMu
}

// NewWebhookStore creates a WebhookStore that keeps webhooks in col and DAG
// name claims in claims. enc may be nil when HMAC secrets are unused.
func NewWebhookStore(col, claims persis.Collection, enc *crypto.Encryptor) (*WebhookStore, error) {
	s := &WebhookStore{
		col:       col,
		claims:    claims,
		encryptor: enc,
		byDAGName: make(map[string]string),
	}
	if err := s.rebuildIndex(context.Background()); err != nil {
		return nil, fmt.Errorf("webhook store: build index: %w", err)
	}
	return s, nil
}

// rebuildIndex replaces the index with one read from the collection. It holds
// mu throughout, so a write from this process lands either before the read or
// after the replacement, never lost in between.
func (s *WebhookStore) rebuildIndex(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadIndexLocked(ctx)
}

// loadIndexLocked replaces the index with one read from the collection. The
// caller holds mu.
func (s *WebhookStore) loadIndexLocked(ctx context.Context) error {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return err
	}
	index := make(map[string]string, len(recs))
	for _, rec := range recs {
		var stored auth.WebhookForStorage
		if err := persis.Decode(rec, &stored); err != nil {
			continue // skip corrupt records
		}
		index[stored.DAGName] = stored.ID
	}
	s.byDAGName = index
	return nil
}

// idOf returns the ID of dagName's webhook, rebuilding the index when it has
// none, since another process sharing the collection may have created one.
func (s *WebhookStore) idOf(ctx context.Context, dagName string) (string, bool, error) {
	s.mu.RLock()
	id, ok := s.byDAGName[dagName]
	s.mu.RUnlock()
	if ok {
		return id, true, nil
	}
	if err := s.rebuildAfterMiss(ctx); err != nil {
		return "", false, fmt.Errorf("webhook store: rebuild index: %w", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok = s.byDAGName[dagName]
	return id, ok, nil
}

// rebuildAfterMiss rebuilds the index unless a miss rebuilt it within
// webhookMissRebuildInterval. Concurrent misses share one rebuild.
func (s *WebhookStore) rebuildAfterMiss(ctx context.Context) error {
	s.missMu.Lock()
	defer s.missMu.Unlock()
	if time.Since(s.missRebuiltAt) < webhookMissRebuildInterval {
		return nil
	}
	if err := s.rebuildIndex(ctx); err != nil {
		return err
	}
	s.missRebuiltAt = time.Now()
	return nil
}

// Create stores a new webhook.
// Returns [auth.ErrWebhookAlreadyExists] if a webhook for the DAG already exists.
func (s *WebhookStore) Create(ctx context.Context, webhook *auth.Webhook) error {
	if webhook == nil {
		return errors.New("webhook store: webhook cannot be nil")
	}
	if webhook.ID == "" {
		return auth.ErrInvalidWebhookID
	}
	if webhook.DAGName == "" {
		return auth.ErrInvalidWebhookDAGName
	}

	stored, err := s.toStorage(webhook)
	if err != nil {
		return err
	}
	data, err := persis.Encode(stored)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// The collection is read afresh, however lookups have been throttled,
	// since another process may have just created this DAG's webhook.
	if err := s.loadIndexLocked(ctx); err != nil {
		return fmt.Errorf("webhook store: rebuild index: %w", err)
	}
	if _, exists := s.byDAGName[webhook.DAGName]; exists {
		return auth.ErrWebhookAlreadyExists
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        webhook.ID,
		Data:      data,
		CreatedAt: webhook.CreatedAt,
		UpdatedAt: webhook.UpdatedAt,
	}); err != nil {
		return err
	}
	// Another process may be creating this DAG's webhook too; the claim
	// decides, and the loser removes its record.
	if err := s.claim(ctx, webhook.DAGName, webhook.ID); err != nil {
		undoCtx, cancel := undoContext(ctx)
		defer cancel()
		if delErr := s.col.Delete(undoCtx, webhook.ID); delErr != nil {
			return errors.Join(err, delErr)
		}
		return err
	}
	s.byDAGName[webhook.DAGName] = webhook.ID
	return nil
}

// webhookUndoTimeout bounds undoing half of a create or rename. It outlasts
// the wait for a record lock left by a crashed process to be recovered.
const webhookUndoTimeout = time.Minute

// undoContext is for undoing half of a create or rename. It outlives ctx,
// since a write the caller gave up on must still be undone.
func undoContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), webhookUndoTimeout)
}

// webhookClaim is a DAG name's claim record: the webhook holding the name.
type webhookClaim struct {
	WebhookID string `json:"webhookId"`
}

// claim gives dagName to webhook id, whose record must already name
// dagName. Returns [auth.ErrWebhookAlreadyExists] while another webhook
// holds the name.
func (s *WebhookStore) claim(ctx context.Context, dagName, id string) error {
	data, err := persis.Encode(webhookClaim{WebhookID: id})
	if err != nil {
		return err
	}
	return retryConflict(ctx, func(ctx context.Context) error {
		now := time.Now().UTC()
		err := s.claims.Create(ctx, &persis.Record{ID: dagName, Data: data, CreatedAt: now, UpdatedAt: now})
		if !errors.Is(err, persis.ErrConflict) {
			return err
		}
		rec, err := s.claims.Get(ctx, dagName)
		if errors.Is(err, persis.ErrNotFound) {
			return persis.ErrConflict // released meanwhile: claim again
		}
		if err != nil {
			return err
		}
		var held webhookClaim
		if err := persis.Decode(rec, &held); err != nil {
			return fmt.Errorf("webhook store: decode claim %q: %w", dagName, err)
		}
		if held.WebhookID == id {
			return nil
		}
		live, err := s.names(ctx, held.WebhookID, dagName)
		if err != nil {
			return err
		}
		if live {
			return auth.ErrWebhookAlreadyExists
		}
		err = s.claims.CompareAndSwap(ctx, dagName, rec.Data, data)
		if errors.Is(err, persis.ErrNotFound) {
			return persis.ErrConflict
		}
		return err
	})
}

// names reports whether webhook id exists and belongs to dagName.
func (s *WebhookStore) names(ctx context.Context, id, dagName string) (bool, error) {
	rec, err := s.col.Get(ctx, id)
	if errors.Is(err, persis.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var stored auth.WebhookForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return false, fmt.Errorf("webhook store: decode %q: %w", id, err)
	}
	return stored.DAGName == dagName, nil
}

// release gives up webhook id's claim on dagName. A claim it cannot remove is
// left over, which a later claim takes over, so failures are not reported.
func (s *WebhookStore) release(ctx context.Context, dagName, id string) {
	rec, err := s.claims.Get(ctx, dagName)
	if err != nil {
		return
	}
	var held webhookClaim
	if persis.Decode(rec, &held) != nil || held.WebhookID != id {
		return
	}
	_ = s.claims.CompareAndDelete(ctx, rec)
}

// GetByID retrieves a webhook by its unique ID.
// Returns [auth.ErrWebhookNotFound] if the webhook does not exist.
func (s *WebhookStore) GetByID(ctx context.Context, id string) (*auth.Webhook, error) {
	if id == "" {
		return nil, auth.ErrInvalidWebhookID
	}
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, auth.ErrWebhookNotFound
		}
		return nil, err
	}
	return s.fromRecord(rec)
}

// GetByDAGName retrieves the webhook for a specific DAG.
// Returns [auth.ErrWebhookNotFound] if no webhook exists for the DAG.
func (s *WebhookStore) GetByDAGName(ctx context.Context, dagName string) (*auth.Webhook, error) {
	if dagName == "" {
		return nil, auth.ErrInvalidWebhookDAGName
	}
	id, ok, err := s.idOf(ctx, dagName)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, auth.ErrWebhookNotFound
	}
	return s.GetByID(ctx, id)
}

// List returns all webhooks in the store.
func (s *WebhookStore) List(ctx context.Context) ([]*auth.Webhook, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*auth.Webhook, 0, len(recs))
	for _, rec := range recs {
		wh, err := s.fromRecord(rec)
		if err != nil {
			continue // skip corrupt records
		}
		out = append(out, wh)
	}
	return out, nil
}

// ListComplete returns all webhooks in the store, or an error when any record
// cannot be read, for a caller that must not act on part of the set. It also
// refreshes the DAG-name index, so each webhook it returns can be looked up.
func (s *WebhookStore) ListComplete(ctx context.Context) ([]*auth.Webhook, error) {
	// Held throughout, as in rebuildIndex.
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := listAllStrict(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*auth.Webhook, 0, len(recs))
	index := make(map[string]string, len(recs))
	for _, rec := range recs {
		wh, err := s.fromRecord(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, wh)
		index[wh.DAGName] = wh.ID
	}
	s.byDAGName = index
	return out, nil
}

// UpdateByDAGName applies mutate to the current webhook for dagName and stores
// the result. Concurrent updates, including those from other processes sharing
// the collection, never overwrite each other. mutate may run more than once.
// An error from mutate aborts the update and is returned unchanged.
// Returns [auth.ErrWebhookNotFound] if no webhook exists for the DAG.
func (s *WebhookStore) UpdateByDAGName(
	ctx context.Context,
	dagName string,
	mutate func(*auth.Webhook) error,
) (*auth.Webhook, error) {
	if dagName == "" {
		return nil, auth.ErrInvalidWebhookDAGName
	}

	id, ok, err := s.idOf(ctx, dagName)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, auth.ErrWebhookNotFound
	}

	var updated *auth.Webhook
	err = retryConflict(ctx, func(ctx context.Context) error {
		rec, err := s.col.Get(ctx, id)
		if err != nil {
			return err
		}
		webhook, err := s.fromRecord(rec)
		if err != nil {
			return err
		}
		if err := mutate(webhook); err != nil {
			return err
		}
		if webhook.ID != id || webhook.DAGName != dagName {
			return errors.New("webhook store: update cannot change the webhook ID or DAG name")
		}
		stored, err := s.toStorage(webhook)
		if err != nil {
			return err
		}
		data, err := persis.Encode(stored)
		if err != nil {
			return err
		}
		if err := s.col.CompareAndSwap(ctx, id, rec.Data, data); err != nil {
			return err
		}
		updated = webhook
		return nil
	})
	if errors.Is(err, persis.ErrNotFound) {
		return nil, auth.ErrWebhookNotFound
	}
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// RenameDAG moves a DAG's webhook to the DAG's new name, keeping its tokens
// and settings. Returns [auth.ErrWebhookAlreadyExists] if a webhook exists
// for newName, checked first, and [auth.ErrWebhookNotFound] if none exists
// for oldName.
func (s *WebhookStore) RenameDAG(ctx context.Context, oldName, newName string) error {
	if oldName == "" || newName == "" {
		return auth.ErrInvalidWebhookDAGName
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Read afresh, as for a create, since the new name must be free.
	if err := s.loadIndexLocked(ctx); err != nil {
		return fmt.Errorf("webhook store: rebuild index: %w", err)
	}
	// The new name is checked first: a webhook left there must not pass to
	// the DAG, whether or not the DAG has its own.
	if _, exists := s.byDAGName[newName]; exists {
		return auth.ErrWebhookAlreadyExists
	}
	id, ok := s.byDAGName[oldName]
	if !ok {
		return auth.ErrWebhookNotFound
	}
	if err := s.setDAGName(ctx, id, newName); err != nil {
		return err
	}
	// Another process may be creating the new name's webhook; if it claims
	// the name first, the webhook moves back.
	if err := s.claim(ctx, newName, id); err != nil {
		undoCtx, cancel := undoContext(ctx)
		defer cancel()
		return errors.Join(err, s.moveBack(undoCtx, id, oldName))
	}
	s.release(ctx, oldName, id)
	delete(s.byDAGName, oldName)
	s.byDAGName[newName] = id
	return nil
}

// moveBack returns webhook id to dagName after a rename lost the new name.
// While the record named the new name, the old name's claim looked left over,
// so another process may have created a webhook for the DAG meanwhile; then
// this one gives way and is removed, leaving the DAG one webhook.
func (s *WebhookStore) moveBack(ctx context.Context, id, dagName string) error {
	if err := s.setDAGName(ctx, id, dagName); err != nil {
		return err
	}
	err := s.claim(ctx, dagName, id)
	if !errors.Is(err, auth.ErrWebhookAlreadyExists) {
		return err
	}
	if err := s.col.Delete(ctx, id); err != nil {
		return err
	}
	return s.loadIndexLocked(ctx)
}

// setDAGName moves webhook id's record to dagName. The stored form is moved
// as it is, so the encrypted secrets need no encryptor.
func (s *WebhookStore) setDAGName(ctx context.Context, id, dagName string) error {
	err := retryConflict(ctx, func(ctx context.Context) error {
		rec, err := s.col.Get(ctx, id)
		if err != nil {
			return err
		}
		var stored auth.WebhookForStorage
		if err := persis.Decode(rec, &stored); err != nil {
			return err
		}
		stored.DAGName = dagName
		stored.UpdatedAt = time.Now().UTC()
		data, err := persis.Encode(stored)
		if err != nil {
			return err
		}
		return s.col.CompareAndSwap(ctx, id, rec.Data, data)
	})
	if errors.Is(err, persis.ErrNotFound) {
		return auth.ErrWebhookNotFound
	}
	return err
}

// Delete removes a webhook by its ID.
// Returns [auth.ErrWebhookNotFound] if the webhook does not exist.
func (s *WebhookStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return auth.ErrInvalidWebhookID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return auth.ErrWebhookNotFound
		}
		return err
	}
	var stored auth.WebhookForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return fmt.Errorf("webhook store: decode for delete: %w", err)
	}

	if err := s.col.Delete(ctx, id); err != nil {
		return err
	}
	s.release(ctx, stored.DAGName, id)
	delete(s.byDAGName, stored.DAGName)
	return nil
}

// DeleteByDAGName removes a webhook by its DAG name.
// Returns [auth.ErrWebhookNotFound] if no webhook exists for the DAG.
func (s *WebhookStore) DeleteByDAGName(ctx context.Context, dagName string) error {
	if dagName == "" {
		return auth.ErrInvalidWebhookDAGName
	}
	id, ok, err := s.idOf(ctx, dagName)
	if err != nil {
		return err
	}
	if !ok {
		return auth.ErrWebhookNotFound
	}
	return s.Delete(ctx, id)
}

// UpdateLastUsed records recent use of a webhook and, when profileTokenID is
// not empty, of that profile token, persisting each timestamp at most once per
// minute. It never restores a profile token removed by a concurrent update.
func (s *WebhookStore) UpdateLastUsed(ctx context.Context, id, profileTokenID string) error {
	if id == "" {
		return auth.ErrInvalidWebhookID
	}
	err := retryConflict(ctx, func(ctx context.Context) error {
		rec, err := s.col.Get(ctx, id)
		if err != nil {
			return err
		}
		var stored auth.WebhookForStorage
		if err := persis.Decode(rec, &stored); err != nil {
			return fmt.Errorf("webhook store: decode for UpdateLastUsed: %w", err)
		}
		now := time.Now().UTC()
		changed := false
		if lastUsedStale(stored.LastUsedAt, now) {
			stored.LastUsedAt = &now
			changed = true
		}
		for i := range stored.ProfileTokens {
			token := &stored.ProfileTokens[i]
			if token.ID == profileTokenID && lastUsedStale(token.LastUsedAt, now) {
				token.LastUsedAt = &now
				changed = true
			}
		}
		if !changed {
			return nil
		}
		data, err := persis.Encode(stored)
		if err != nil {
			return err
		}
		return s.col.CompareAndSwap(ctx, id, rec.Data, data)
	})
	if errors.Is(err, persis.ErrNotFound) {
		return auth.ErrWebhookNotFound
	}
	return err
}

func lastUsedStale(lastUsed *time.Time, now time.Time) bool {
	return lastUsed == nil || now.Sub(*lastUsed) >= webhookLastUsedWriteInterval
}

// ─── encoding helpers ─────────────────────────────────────────────────────────

func (s *WebhookStore) toStorage(wh *auth.Webhook) (*auth.WebhookForStorage, error) {
	stored := wh.ToStorage()
	if wh.HMACSecret != "" {
		if s.encryptor == nil {
			return nil, auth.ErrWebhookHMACEncryptorRequired
		}
		enc, err := s.encryptor.Encrypt(wh.HMACSecret)
		if err != nil {
			return nil, fmt.Errorf("webhook store: encrypt HMAC secret: %w", err)
		}
		stored.HMACSecretEnc = enc
	}
	if wh.Public != nil {
		if s.encryptor == nil {
			return nil, auth.ErrWebhookPublicEncryptorRequired
		}
		enc, err := s.encryptor.Encrypt(wh.Public.Token)
		if err != nil {
			return nil, fmt.Errorf("webhook store: encrypt public URL token: %w", err)
		}
		stored.Public.TokenEnc = enc
		if wh.Public.SenderSecret != "" {
			enc, err := s.encryptor.Encrypt(wh.Public.SenderSecret)
			if err != nil {
				return nil, fmt.Errorf("webhook store: encrypt sender secret: %w", err)
			}
			stored.Public.SenderSecretEnc = enc
		}
	}
	return stored, nil
}

func (s *WebhookStore) fromRecord(rec *persis.Record) (*auth.Webhook, error) {
	var stored auth.WebhookForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("webhook store: decode record %q: %w", rec.ID, err)
	}
	wh := stored.ToWebhook()
	if stored.HMACSecretEnc != "" {
		if s.encryptor == nil {
			return nil, auth.ErrWebhookHMACEncryptorRequired
		}
		secret, err := s.encryptor.Decrypt(stored.HMACSecretEnc)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", auth.ErrWebhookHMACDecryptFailed, err)
		}
		wh.HMACSecret = secret
	}
	if stored.Public != nil {
		if s.encryptor == nil {
			return nil, auth.ErrWebhookPublicEncryptorRequired
		}
		token, err := s.encryptor.Decrypt(stored.Public.TokenEnc)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", auth.ErrWebhookPublicDecryptFailed, err)
		}
		wh.Public.Token = token
		if stored.Public.SenderSecretEnc != "" {
			secret, err := s.encryptor.Decrypt(stored.Public.SenderSecretEnc)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", auth.ErrWebhookPublicDecryptFailed, err)
			}
			wh.Public.SenderSecret = secret
		}
	}
	return wh, nil
}
