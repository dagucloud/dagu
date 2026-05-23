// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/persis"
)

var _ auth.WebhookStore = (*WebhookStore)(nil)

// WebhookStore implements [auth.WebhookStore].
// DAG-name lookups use an in-memory index (byDAGName) rebuilt from the
// collection on startup; all writes keep it in sync under mu.
type WebhookStore struct {
	col       persis.Collection
	encryptor *crypto.Encryptor // nil = HMAC encryption disabled

	mu        sync.RWMutex
	byDAGName map[string]string // dagName → webhookID
}

// NewWebhookStore creates a WebhookStore backed by col. enc may be nil when HMAC secrets are unused.
func NewWebhookStore(col persis.Collection, enc *crypto.Encryptor) (*WebhookStore, error) {
	s := &WebhookStore{
		col:       col,
		encryptor: enc,
		byDAGName: make(map[string]string),
	}
	if err := s.rebuildIndex(context.Background()); err != nil {
		return nil, fmt.Errorf("webhook store: build index: %w", err)
	}
	return s, nil
}

func (s *WebhookStore) rebuildIndex(ctx context.Context) error {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range recs {
		var stored auth.WebhookForStorage
		if err := persis.Decode(rec, &stored); err != nil {
			continue // skip corrupt records
		}
		s.byDAGName[stored.DAGName] = stored.ID
	}
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
	data, enc, err := persis.Encode(stored)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byDAGName[webhook.DAGName]; exists {
		return auth.ErrWebhookAlreadyExists
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        webhook.ID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: webhook.CreatedAt,
		UpdatedAt: webhook.UpdatedAt,
	}); err != nil {
		return err
	}
	s.byDAGName[webhook.DAGName] = webhook.ID
	return nil
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
	s.mu.RLock()
	id, ok := s.byDAGName[dagName]
	s.mu.RUnlock()
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

// Update modifies an existing webhook.
// Returns [auth.ErrWebhookNotFound] if the webhook does not exist.
func (s *WebhookStore) Update(ctx context.Context, webhook *auth.Webhook) error {
	if webhook == nil {
		return errors.New("webhook store: webhook cannot be nil")
	}
	if webhook.ID == "" {
		return auth.ErrInvalidWebhookID
	}
	if webhook.DAGName == "" {
		return auth.ErrInvalidWebhookDAGName
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existingRec, err := s.col.Get(ctx, webhook.ID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return auth.ErrWebhookNotFound
		}
		return err
	}
	var existingStored auth.WebhookForStorage
	if err := persis.Decode(existingRec, &existingStored); err != nil {
		return fmt.Errorf("webhook store: decode existing: %w", err)
	}

	stored, err := s.toStorage(webhook)
	if err != nil {
		return err
	}
	data, enc, err := persis.Encode(stored)
	if err != nil {
		return err
	}

	if existingStored.DAGName != webhook.DAGName {
		if id, taken := s.byDAGName[webhook.DAGName]; taken && id != webhook.ID {
			return auth.ErrWebhookAlreadyExists
		}
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        webhook.ID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: existingRec.CreatedAt,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if existingStored.DAGName != webhook.DAGName {
		delete(s.byDAGName, existingStored.DAGName)
		s.byDAGName[webhook.DAGName] = webhook.ID
	}
	return nil
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
	delete(s.byDAGName, stored.DAGName)
	return nil
}

// DeleteByDAGName removes a webhook by its DAG name.
// Returns [auth.ErrWebhookNotFound] if no webhook exists for the DAG.
func (s *WebhookStore) DeleteByDAGName(ctx context.Context, dagName string) error {
	if dagName == "" {
		return auth.ErrInvalidWebhookDAGName
	}
	s.mu.RLock()
	id, ok := s.byDAGName[dagName]
	s.mu.RUnlock()
	if !ok {
		return auth.ErrWebhookNotFound
	}
	return s.Delete(ctx, id)
}

// UpdateLastUsed updates the LastUsedAt timestamp for a webhook.
func (s *WebhookStore) UpdateLastUsed(ctx context.Context, id string) error {
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
		return fmt.Errorf("webhook store: decode for UpdateLastUsed: %w", err)
	}
	now := time.Now().UTC()
	stored.LastUsedAt = &now
	data, enc, err := persis.Encode(stored)
	if err != nil {
		return err
	}
	return s.col.Put(ctx, &persis.Record{
		ID:        rec.ID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: now,
	})
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
	return wh, nil
}
