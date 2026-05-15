// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secret

import (
	"context"
	"sync"
	"time"
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

func (s *memoryStoreForTest) List(_ context.Context, _ ListOptions) ([]*Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ret := make([]*Secret, 0, len(s.secrets))
	for _, sec := range s.secrets {
		ret = append(ret, sec.Clone())
	}
	return ret, nil
}

func (s *memoryStoreForTest) Update(_ context.Context, sec *Secret) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.secrets[sec.ID]; !ok {
		return ErrNotFound
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
