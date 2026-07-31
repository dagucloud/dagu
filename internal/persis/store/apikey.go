// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package store consolidates small persistence stores that each wrap a [persis.Collection].
package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ auth.APIKeyStore = (*APIKeyStore)(nil)

const apiKeyLastUsedWriteInterval = time.Minute

// APIKeyStore implements [auth.APIKeyStore].
// Name and credential-digest lookups use in-memory indexes rebuilt from the
// collection on startup; all writes keep them in sync under mu.
type APIKeyStore struct {
	col persis.Collection

	mu       sync.RWMutex
	byName   map[string]string // name → keyID
	byDigest map[string]string // digest → keyID
}

// NewAPIKeyStore creates a APIKeyStore backed by col.
func NewAPIKeyStore(col persis.Collection) (*APIKeyStore, error) {
	s := &APIKeyStore{
		col:      col,
		byName:   make(map[string]string),
		byDigest: make(map[string]string),
	}
	if err := s.rebuildIndex(context.Background()); err != nil {
		return nil, fmt.Errorf("apikey store: build index: %w", err)
	}
	return s, nil
}

func (s *APIKeyStore) rebuildIndex(ctx context.Context) error {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range recs {
		var stored auth.APIKeyForStorage
		if err := persis.Decode(rec, &stored); err != nil {
			continue
		}
		s.byName[stored.Name] = stored.ID
		if stored.KeyDigest == "" {
			continue
		}
		if id, exists := s.byDigest[stored.KeyDigest]; exists && id != stored.ID {
			return fmt.Errorf("duplicate API key digest for %q and %q", id, stored.ID)
		}
		s.byDigest[stored.KeyDigest] = stored.ID
	}
	return nil
}

// Create stores a new API key.
// Returns [auth.ErrAPIKeyAlreadyExists] if a key with the same name exists.
func (s *APIKeyStore) Create(ctx context.Context, key *auth.APIKey) error {
	if key == nil {
		return errors.New("apikey store: key cannot be nil")
	}
	if key.ID == "" {
		return auth.ErrInvalidAPIKeyID
	}
	if key.Name == "" {
		return auth.ErrInvalidAPIKeyName
	}

	data, err := persis.Encode(key.ToStorage())
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byName[key.Name]; exists {
		return auth.ErrAPIKeyAlreadyExists
	}
	if key.KeyDigest != "" {
		if _, exists := s.byDigest[key.KeyDigest]; exists {
			return auth.ErrAPIKeyAlreadyExists
		}
	}
	if _, err := s.col.Get(ctx, key.ID); err == nil {
		return auth.ErrAPIKeyAlreadyExists
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        key.ID,
		Data:      data,
		CreatedAt: key.CreatedAt,
		UpdatedAt: key.UpdatedAt,
	}); err != nil {
		return err
	}
	s.byName[key.Name] = key.ID
	if key.KeyDigest != "" {
		s.byDigest[key.KeyDigest] = key.ID
	}
	return nil
}

// GetByID retrieves an API key by its unique ID.
// Returns [auth.ErrAPIKeyNotFound] if the key does not exist.
func (s *APIKeyStore) GetByID(ctx context.Context, id string) (*auth.APIKey, error) {
	if id == "" {
		return nil, auth.ErrInvalidAPIKeyID
	}
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, auth.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return apikeyFromRecord(rec)
}

// GetByDigest retrieves an API key by its credential digest.
// Returns [auth.ErrAPIKeyNotFound] if the key does not exist.
func (s *APIKeyStore) GetByDigest(ctx context.Context, digest string) (*auth.APIKey, error) {
	if digest == "" {
		return nil, auth.ErrAPIKeyNotFound
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.byDigest[digest]
	if !ok {
		return nil, auth.ErrAPIKeyNotFound
	}
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, auth.ErrAPIKeyNotFound
		}
		return nil, err
	}
	key, err := apikeyFromRecord(rec)
	if err != nil {
		return nil, err
	}
	if key.KeyDigest != digest {
		return nil, auth.ErrAPIKeyNotFound
	}
	return key, nil
}

// List returns all API keys in the store.
func (s *APIKeyStore) List(ctx context.Context) ([]*auth.APIKey, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*auth.APIKey, 0, len(recs))
	for _, rec := range recs {
		key, err := apikeyFromRecord(rec)
		if err != nil {
			continue
		}
		out = append(out, key)
	}
	return out, nil
}

// Update modifies an existing API key.
// Credential fields and a newer LastUsedAt value in storage are preserved.
// Returns [auth.ErrAPIKeyNotFound] if the key does not exist.
func (s *APIKeyStore) Update(ctx context.Context, key *auth.APIKey) error {
	if key == nil {
		return errors.New("apikey store: key cannot be nil")
	}
	if key.ID == "" {
		return auth.ErrInvalidAPIKeyID
	}
	if key.Name == "" {
		return auth.ErrInvalidAPIKeyName
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existingRec, err := s.col.Get(ctx, key.ID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return auth.ErrAPIKeyNotFound
		}
		return err
	}
	var existingStored auth.APIKeyForStorage
	if err := persis.Decode(existingRec, &existingStored); err != nil {
		return fmt.Errorf("apikey store: decode existing: %w", err)
	}
	if existingStored.Name != key.Name {
		if id, taken := s.byName[key.Name]; taken && id != key.ID {
			return auth.ErrAPIKeyAlreadyExists
		}
	}

	updatedStored := key.ToStorage()
	updatedStored.KeyHash = existingStored.KeyHash
	updatedStored.KeyDigest = existingStored.KeyDigest
	updatedStored.LastUsedAt = latestTime(existingStored.LastUsedAt, updatedStored.LastUsedAt)
	key.KeyHash = updatedStored.KeyHash
	key.KeyDigest = updatedStored.KeyDigest
	key.LastUsedAt = updatedStored.LastUsedAt

	data, err := persis.Encode(updatedStored)
	if err != nil {
		return err
	}

	if err := s.col.Put(ctx, &persis.Record{
		ID:        key.ID,
		Data:      data,
		CreatedAt: existingRec.CreatedAt,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if existingStored.Name != key.Name {
		delete(s.byName, existingStored.Name)
		s.byName[key.Name] = key.ID
	}
	return nil
}

// Delete removes an API key by its ID.
// Returns [auth.ErrAPIKeyNotFound] if the key does not exist.
func (s *APIKeyStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return auth.ErrInvalidAPIKeyID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return auth.ErrAPIKeyNotFound
		}
		return err
	}
	var stored auth.APIKeyForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return fmt.Errorf("apikey store: decode for delete: %w", err)
	}

	if err := s.col.Delete(ctx, id); err != nil {
		return err
	}
	delete(s.byName, stored.Name)
	if stored.KeyDigest != "" && s.byDigest[stored.KeyDigest] == id {
		delete(s.byDigest, stored.KeyDigest)
	}
	return nil
}

// PromoteDigest atomically assigns a credential digest to an API key.
// Repeating the promotion with the same digest is idempotent.
func (s *APIKeyStore) PromoteDigest(ctx context.Context, id, digest string) error {
	if id == "" {
		return auth.ErrInvalidAPIKeyID
	}
	if digest == "" {
		return auth.ErrInvalidAPIKeyHash
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return auth.ErrAPIKeyNotFound
		}
		return err
	}
	var stored auth.APIKeyForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return fmt.Errorf("apikey store: decode for PromoteDigest: %w", err)
	}
	if stored.KeyDigest == digest {
		s.byDigest[digest] = id
		return nil
	}
	if stored.KeyDigest != "" {
		return fmt.Errorf("apikey store: API key %q already has a credential digest", id)
	}
	if ownerID, exists := s.byDigest[digest]; exists && ownerID != id {
		return auth.ErrAPIKeyAlreadyExists
	}

	stored.KeyDigest = digest
	data, err := persis.Encode(stored)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.col.Put(ctx, &persis.Record{
		ID:        rec.ID,
		Data:      data,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: now,
	}); err != nil {
		return err
	}
	s.byDigest[digest] = id
	return nil
}

// UpdateLastUsed records recent API key use without persisting more than once per minute.
func (s *APIKeyStore) UpdateLastUsed(ctx context.Context, id string) error {
	if id == "" {
		return auth.ErrInvalidAPIKeyID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return auth.ErrAPIKeyNotFound
		}
		return err
	}
	var stored auth.APIKeyForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return fmt.Errorf("apikey store: decode for UpdateLastUsed: %w", err)
	}
	now := time.Now().UTC()
	if stored.LastUsedAt != nil && now.Sub(*stored.LastUsedAt) < apiKeyLastUsedWriteInterval {
		return nil
	}
	stored.LastUsedAt = &now
	data, err := persis.Encode(stored)
	if err != nil {
		return err
	}
	return s.col.Put(ctx, &persis.Record{
		ID:        rec.ID,
		Data:      data,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: now,
	})
}

func apikeyFromRecord(rec *persis.Record) (*auth.APIKey, error) {
	var stored auth.APIKeyForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("apikey store: decode record %q: %w", rec.ID, err)
	}
	return stored.ToAPIKey(), nil
}

func latestTime(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case a.After(*b):
		return a
	default:
		return b
	}
}
