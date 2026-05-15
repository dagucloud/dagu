// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secret

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryStoreForTest struct {
	mu       sync.RWMutex
	secrets  map[string]*Secret
	byRef    map[string]string
	values   map[string]string
	versions map[string]*VersionMetadata
}

func newMemoryStoreForTest() *memoryStoreForTest {
	return &memoryStoreForTest{
		secrets:  make(map[string]*Secret),
		byRef:    make(map[string]string),
		values:   make(map[string]string),
		versions: make(map[string]*VersionMetadata),
	}
}

func (s *memoryStoreForTest) Create(_ context.Context, sec *Secret, initialValue *WriteValueInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.byRef[refKey(sec.Workspace, sec.Ref)]; ok {
		return ErrAlreadyExists
	}
	s.secrets[sec.ID] = sec.Clone()
	s.byRef[refKey(sec.Workspace, sec.Ref)] = sec.ID
	if initialValue != nil {
		s.writeValueLocked(sec.ID, *initialValue)
	}
	return nil
}

func (s *memoryStoreForTest) GetByID(_ context.Context, id string) (*Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sec, ok := s.secrets[id]
	if !ok {
		return nil, ErrNotFound
	}
	return sec.Clone(), nil
}

func (s *memoryStoreForTest) GetByRef(_ context.Context, workspace, ref string) (*Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.byRef[refKey(workspace, ref)]
	if !ok {
		return nil, ErrNotFound
	}
	return s.secrets[id].Clone(), nil
}

func (s *memoryStoreForTest) List(_ context.Context, opts ListOptions) ([]*Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ret := make([]*Secret, 0, len(s.secrets))
	for _, sec := range s.secrets {
		if opts.Workspace != nil && sec.Workspace != *opts.Workspace {
			continue
		}
		ret = append(ret, sec.Clone())
	}
	return ret, nil
}

func (s *memoryStoreForTest) Update(_ context.Context, sec *Secret) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	old, ok := s.secrets[sec.ID]
	if !ok {
		return ErrNotFound
	}
	oldRef := refKey(old.Workspace, old.Ref)
	newRef := refKey(sec.Workspace, sec.Ref)
	if oldRef != newRef {
		if existingID, taken := s.byRef[newRef]; taken && existingID != sec.ID {
			return ErrAlreadyExists
		}
		delete(s.byRef, oldRef)
		s.byRef[newRef] = sec.ID
	}
	s.secrets[sec.ID] = sec.Clone()
	return nil
}

func (s *memoryStoreForTest) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sec, ok := s.secrets[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.secrets, id)
	delete(s.byRef, refKey(sec.Workspace, sec.Ref))
	delete(s.values, id)
	delete(s.versions, id)
	return nil
}

func (s *memoryStoreForTest) WriteValue(_ context.Context, id string, input WriteValueInput) (*Secret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.secrets[id]; !ok {
		return nil, ErrNotFound
	}
	s.writeValueLocked(id, input)
	return s.secrets[id].Clone(), nil
}

func (s *memoryStoreForTest) GetCurrentVersion(_ context.Context, id string) (*VersionMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	version, ok := s.versions[id]
	if !ok {
		return nil, ErrNoValue
	}
	return cloneVersion(version), nil
}

func (s *memoryStoreForTest) ResolveValue(_ context.Context, id string) (string, *VersionMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	value, ok := s.values[id]
	if !ok {
		return "", nil, ErrNoValue
	}
	return value, cloneVersion(s.versions[id]), nil
}

func (s *memoryStoreForTest) writeValueLocked(id string, input WriteValueInput) {
	sec := s.secrets[id]
	version := sec.CurrentVersion + 1
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	sec.CurrentVersion = version
	sec.LastRotatedAt = &createdAt
	sec.UpdatedBy = input.CreatedBy
	sec.UpdatedAt = createdAt
	s.values[id] = input.Value
	s.versions[id] = &VersionMetadata{
		SecretID:  id,
		Version:   version,
		CreatedBy: input.CreatedBy,
		CreatedAt: createdAt,
	}
}

func TestMemoryStoreForTestListFiltersWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStoreForTest()
	now := time.Date(2026, 5, 14, 1, 2, 3, 0, time.UTC)

	defaultSecret := newMemoryStoreSecret(t, "", "prod/db-password", now)
	paymentsSecret := newMemoryStoreSecret(t, "payments", "prod/db-password", now)
	require.NoError(t, store.Create(ctx, defaultSecret, nil))
	require.NoError(t, store.Create(ctx, paymentsSecret, nil))

	workspace := "payments"
	got, err := store.List(ctx, ListOptions{Workspace: &workspace})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, paymentsSecret.ID, got[0].ID)

	all, err := store.List(ctx, ListOptions{})
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestMemoryStoreForTestUpdateRefreshesRefIndex(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStoreForTest()
	now := time.Date(2026, 5, 14, 1, 2, 3, 0, time.UTC)

	sec := newMemoryStoreSecret(t, "payments", "prod/db-password", now)
	require.NoError(t, store.Create(ctx, sec, nil))

	updated := sec.Clone()
	updated.Ref = "prod/db-password-v2"
	require.NoError(t, store.Update(ctx, updated))

	_, err := store.GetByRef(ctx, "payments", "prod/db-password")
	require.ErrorIs(t, err, ErrNotFound)
	got, err := store.GetByRef(ctx, "payments", "prod/db-password-v2")
	require.NoError(t, err)
	assert.Equal(t, sec.ID, got.ID)

	other := newMemoryStoreSecret(t, "payments", "prod/api-key", now)
	require.NoError(t, store.Create(ctx, other, nil))
	conflicting := updated.Clone()
	conflicting.Ref = other.Ref
	err = store.Update(ctx, conflicting)
	require.ErrorIs(t, err, ErrAlreadyExists)
}

func newMemoryStoreSecret(t *testing.T, workspaceName, ref string, now time.Time) *Secret {
	t.Helper()

	sec, err := New(CreateInput{
		Workspace:    workspaceName,
		Ref:          ref,
		ProviderType: ProviderDaguManaged,
		CreatedBy:    "alice",
	}, now)
	require.NoError(t, err)
	return sec
}

func refKey(workspace, ref string) string {
	return workspace + "\x00" + ref
}

func cloneVersion(in *VersionMetadata) *VersionMetadata {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
