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

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/persis"
)

var _ auth.APIKeyStore = (*APIKeyStore)(nil)

// APIKeyStore implements [auth.APIKeyStore].
// Name lookups use an in-memory index (byName) rebuilt from the
// collection on startup; all writes keep it in sync under mu.
type APIKeyStore struct {
	col persis.Collection

	mu     sync.RWMutex
	byName map[string]string // name → keyID
}

// NewAPIKeyStore creates a APIKeyStore backed by col.
func NewAPIKeyStore(col persis.Collection) (*APIKeyStore, error) {
	s := &APIKeyStore{
		col:    col,
		byName: make(map[string]string),
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

	data, enc, err := persis.Encode(key.ToStorage())
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byName[key.Name]; exists {
		return auth.ErrAPIKeyAlreadyExists
	}
	if _, err := s.col.Get(ctx, key.ID); err == nil {
		return auth.ErrAPIKeyAlreadyExists
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        key.ID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: key.CreatedAt,
		UpdatedAt: key.UpdatedAt,
	}); err != nil {
		return err
	}
	s.byName[key.Name] = key.ID
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

	data, enc, err := persis.Encode(key.ToStorage())
	if err != nil {
		return err
	}

	if existingStored.Name != key.Name {
		if id, taken := s.byName[key.Name]; taken && id != key.ID {
			return auth.ErrAPIKeyAlreadyExists
		}
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        key.ID,
		Data:      data,
		Encoding:  enc,
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
	return nil
}

// UpdateLastUsed updates the LastUsedAt timestamp for an API key.
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

func apikeyFromRecord(rec *persis.Record) (*auth.APIKey, error) {
	var stored auth.APIKeyForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("apikey store: decode record %q: %w", rec.ID, err)
	}
	return stored.ToAPIKey(), nil
}
