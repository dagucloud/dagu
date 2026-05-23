// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package secret implements [secret.Store] using a [persis.Collection].
package secret

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/secret"
)

var _ secret.Store = (*Store)(nil)

// Store implements [secret.Store].
// byRef is an in-memory index rebuilt on startup;
// all writes keep it in sync under mu.
type Store struct {
	col       persis.Collection
	encryptor *crypto.Encryptor

	mu    sync.RWMutex
	byRef map[string]string // refKey(workspace,ref) → secretID
}

type storedRecord struct {
	Secret   *secret.Secret  `json:"secret"`
	Versions []storedVersion `json:"versions,omitempty"`
}

type storedVersion struct {
	SecretID       string    `json:"secret_id"`
	Version        int       `json:"version"`
	EncryptedValue string    `json:"encrypted_value"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// New creates a Store backed by col.
func New(col persis.Collection, enc *crypto.Encryptor) (*Store, error) {
	if enc == nil {
		return nil, errors.New("secret store: encryptor cannot be nil")
	}
	s := &Store{
		col:       col,
		encryptor: enc,
		byRef:     make(map[string]string),
	}
	if err := s.rebuildIndex(context.Background()); err != nil {
		return nil, fmt.Errorf("secret store: build index: %w", err)
	}
	return s, nil
}

func (s *Store) rebuildIndex(ctx context.Context) error {
	page, err := s.col.List(ctx, persis.ListQuery{})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range page.Records {
		var sr storedRecord
		if err := persis.Decode(rec, &sr); err != nil || sr.Secret == nil {
			continue
		}
		s.byRef[refKey(sr.Secret.Workspace, sr.Secret.Ref)] = sr.Secret.ID
	}
	return nil
}

// Create stores a new secret, optionally writing an initial encrypted value.
func (s *Store) Create(ctx context.Context, sec *secret.Secret, initialValue *secret.WriteValueInput) error {
	if sec == nil {
		return errors.New("secret store: secret cannot be nil")
	}
	if sec.ID == "" {
		return secret.ErrInvalidSecretID
	}
	stored := sec.Clone()
	stored.Workspace = secret.NormalizeWorkspace(stored.Workspace)
	if err := secret.ValidateWorkspace(stored.Workspace); err != nil {
		return err
	}
	if err := secret.ValidateRef(stored.Ref); err != nil {
		return err
	}
	if err := secret.ValidateProviderType(stored.ProviderType); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rk := refKey(stored.Workspace, stored.Ref)
	if _, exists := s.byRef[rk]; exists {
		return secret.ErrAlreadyExists
	}

	sr := &storedRecord{Secret: stored}
	if initialValue != nil {
		if err := s.appendVersion(sr, *initialValue); err != nil {
			return err
		}
	}

	data, enc, err := persis.Encode(sr)
	if err != nil {
		return err
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        sec.ID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: sec.CreatedAt,
		UpdatedAt: sec.UpdatedAt,
	}); err != nil {
		return err
	}
	s.byRef[rk] = sec.ID
	return nil
}

// GetByID retrieves a secret by its unique ID.
func (s *Store) GetByID(ctx context.Context, id string) (*secret.Secret, error) {
	if id == "" {
		return nil, secret.ErrInvalidSecretID
	}
	sr, err := s.loadByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return sr.Secret.Clone(), nil
}

// GetByRef retrieves a secret by workspace and ref.
func (s *Store) GetByRef(ctx context.Context, workspace, ref string) (*secret.Secret, error) {
	workspace = secret.NormalizeWorkspace(workspace)
	if err := secret.ValidateWorkspace(workspace); err != nil {
		return nil, err
	}
	if err := secret.ValidateRef(ref); err != nil {
		return nil, err
	}

	s.mu.RLock()
	id, ok := s.byRef[refKey(workspace, ref)]
	s.mu.RUnlock()
	if !ok {
		return nil, secret.ErrNotFound
	}
	return s.GetByID(ctx, id)
}

// List returns all secrets, optionally filtered by workspace.
func (s *Store) List(ctx context.Context, opts secret.ListOptions) ([]*secret.Secret, error) {
	page, err := s.col.List(ctx, persis.ListQuery{})
	if err != nil {
		return nil, err
	}

	out := make([]*secret.Secret, 0, len(page.Records))
	for _, rec := range page.Records {
		var sr storedRecord
		if err := persis.Decode(rec, &sr); err != nil || sr.Secret == nil {
			continue
		}
		if opts.Workspace != nil && sr.Secret.Workspace != secret.NormalizeWorkspace(*opts.Workspace) {
			continue
		}
		out = append(out, sr.Secret.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Workspace != out[j].Workspace {
			return out[i].Workspace < out[j].Workspace
		}
		return out[i].Ref < out[j].Ref
	})
	return out, nil
}

// Update modifies an existing secret's metadata.
func (s *Store) Update(ctx context.Context, sec *secret.Secret) error {
	if sec == nil {
		return errors.New("secret store: secret cannot be nil")
	}
	if sec.ID == "" {
		return secret.ErrInvalidSecretID
	}
	updated := sec.Clone()
	updated.Workspace = secret.NormalizeWorkspace(updated.Workspace)
	if err := secret.ValidateWorkspace(updated.Workspace); err != nil {
		return err
	}
	if err := secret.ValidateRef(updated.Ref); err != nil {
		return err
	}
	if err := secret.ValidateProviderType(updated.ProviderType); err != nil {
		return err
	}

	existingRec, err := s.col.Get(ctx, sec.ID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return secret.ErrNotFound
		}
		return err
	}
	var existing storedRecord
	if err := persis.Decode(existingRec, &existing); err != nil {
		return fmt.Errorf("secret store: decode existing: %w", err)
	}

	oldRef := refKey(existing.Secret.Workspace, existing.Secret.Ref)
	newRef := refKey(updated.Workspace, updated.Ref)

	s.mu.Lock()
	defer s.mu.Unlock()

	if oldRef != newRef {
		if existingID, taken := s.byRef[newRef]; taken && existingID != sec.ID {
			return secret.ErrAlreadyExists
		}
	}

	existing.Secret = updated
	data, enc, err := persis.Encode(&existing)
	if err != nil {
		return err
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        sec.ID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: existingRec.CreatedAt,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if oldRef != newRef {
		delete(s.byRef, oldRef)
		s.byRef[newRef] = sec.ID
	}
	return nil
}

// Delete removes a secret by its ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	if id == "" {
		return secret.ErrInvalidSecretID
	}
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return secret.ErrNotFound
		}
		return err
	}
	var sr storedRecord
	if err := persis.Decode(rec, &sr); err != nil {
		return fmt.Errorf("secret store: decode for delete: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.col.Delete(ctx, id); err != nil {
		return err
	}
	if sr.Secret != nil {
		delete(s.byRef, refKey(sr.Secret.Workspace, sr.Secret.Ref))
	}
	return nil
}

// WriteValue appends a new encrypted version to the secret.
func (s *Store) WriteValue(ctx context.Context, id string, input secret.WriteValueInput) (*secret.Secret, error) {
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, secret.ErrNotFound
		}
		return nil, err
	}
	var sr storedRecord
	if err := persis.Decode(rec, &sr); err != nil {
		return nil, fmt.Errorf("secret store: decode for WriteValue: %w", err)
	}
	if err := s.appendVersion(&sr, input); err != nil {
		return nil, err
	}
	data, enc, err := persis.Encode(&sr)
	if err != nil {
		return nil, err
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        rec.ID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		return nil, err
	}
	return sr.Secret.Clone(), nil
}

// GetCurrentVersion returns metadata about the current version without decrypting.
func (s *Store) GetCurrentVersion(ctx context.Context, id string) (*secret.VersionMetadata, error) {
	sr, err := s.loadByID(ctx, id)
	if err != nil {
		return nil, err
	}
	v, ok := currentVersion(sr)
	if !ok {
		return nil, secret.ErrNoValue
	}
	return versionMetadata(v), nil
}

// ResolveValue decrypts and returns the current plaintext value.
func (s *Store) ResolveValue(ctx context.Context, id string) (string, *secret.VersionMetadata, error) {
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return "", nil, secret.ErrNotFound
		}
		return "", nil, err
	}
	var sr storedRecord
	if err := persis.Decode(rec, &sr); err != nil {
		return "", nil, fmt.Errorf("secret store: decode for ResolveValue: %w", err)
	}
	if sr.Secret.Status == secret.StatusDisabled {
		return "", nil, secret.ErrDisabled
	}
	v, ok := currentVersion(&sr)
	if !ok {
		return "", nil, secret.ErrNoValue
	}
	plaintext, err := s.encryptor.Decrypt(v.EncryptedValue)
	if err != nil {
		return "", nil, fmt.Errorf("secret store: decrypt version %d: %w", v.Version, err)
	}
	now := time.Now().UTC()
	sr.Secret.LastResolvedAt = &now
	sr.Secret.UpdatedAt = now
	data, enc, err := persis.Encode(&sr)
	if err != nil {
		return "", nil, err
	}
	_ = s.col.Put(ctx, &persis.Record{
		ID:        rec.ID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: now,
	})
	return plaintext, versionMetadata(v), nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func (s *Store) loadByID(ctx context.Context, id string) (*storedRecord, error) {
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, secret.ErrNotFound
		}
		return nil, err
	}
	var sr storedRecord
	if err := persis.Decode(rec, &sr); err != nil {
		return nil, fmt.Errorf("secret store: decode record %q: %w", id, err)
	}
	return &sr, nil
}

func (s *Store) appendVersion(sr *storedRecord, input secret.WriteValueInput) error {
	if sr.Secret.ProviderType != secret.ProviderDaguManaged {
		return secret.ErrUnsupportedProvider
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	encrypted, err := s.encryptor.Encrypt(input.Value)
	if err != nil {
		return fmt.Errorf("secret store: encrypt: %w", err)
	}
	version := sr.Secret.CurrentVersion + 1
	sr.Versions = append(sr.Versions, storedVersion{
		SecretID:       sr.Secret.ID,
		Version:        version,
		EncryptedValue: encrypted,
		CreatedBy:      input.CreatedBy,
		CreatedAt:      input.CreatedAt,
	})
	sr.Secret.CurrentVersion = version
	sr.Secret.LastRotatedAt = &input.CreatedAt
	sr.Secret.UpdatedBy = input.CreatedBy
	sr.Secret.UpdatedAt = input.CreatedAt
	return nil
}

func currentVersion(sr *storedRecord) (storedVersion, bool) {
	for _, v := range sr.Versions {
		if v.Version == sr.Secret.CurrentVersion {
			return v, true
		}
	}
	return storedVersion{}, false
}

func versionMetadata(v storedVersion) *secret.VersionMetadata {
	return &secret.VersionMetadata{
		SecretID:  v.SecretID,
		Version:   v.Version,
		CreatedBy: v.CreatedBy,
		CreatedAt: v.CreatedAt,
	}
}

func refKey(workspace, ref string) string {
	return workspace + "\x00" + ref
}
