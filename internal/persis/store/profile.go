// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/profile"
)

var _ profile.Store = (*ProfileStore)(nil)

type ProfileStore struct {
	col persis.Collection
}

type profileStoredRecord struct {
	Profile *profile.Profile `json:"profile"`
}

func NewProfileStore(col persis.Collection) (*ProfileStore, error) {
	if col == nil {
		return nil, errors.New("profile store: collection cannot be nil")
	}
	return &ProfileStore{col: col}, nil
}

func (s *ProfileStore) Create(ctx context.Context, p *profile.Profile) error {
	if p == nil {
		return errors.New("profile store: profile cannot be nil")
	}
	if err := validateStoredProfile(p); err != nil {
		return err
	}
	stored := p.Clone()
	data, err := persis.Encode(&profileStoredRecord{Profile: stored})
	if err != nil {
		return err
	}
	err = s.col.Create(ctx, &persis.Record{
		ID:        stored.Name,
		Data:      data,
		CreatedAt: stored.CreatedAt,
		UpdatedAt: stored.UpdatedAt,
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, persis.ErrConflict) {
		return profile.ErrAlreadyExists
	}
	return err
}

func (s *ProfileStore) GetByName(ctx context.Context, name string) (*profile.Profile, error) {
	if err := profile.ValidateName(name); err != nil {
		return nil, err
	}
	return s.getByStorageName(ctx, name)
}

func (s *ProfileStore) GetInherited(ctx context.Context, ref profile.InheritedRef) (*profile.Profile, error) {
	if !ref.Valid() {
		return nil, profile.ErrInvalidName
	}
	return s.getByStorageName(ctx, ref.StorageName())
}

func (s *ProfileStore) getByStorageName(ctx context.Context, name string) (*profile.Profile, error) {
	if err := validateProfileStorageName(name); err != nil {
		return nil, err
	}
	rec, err := s.col.Get(ctx, name)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, profile.ErrNotFound
		}
		return nil, err
	}
	var stored profileStoredRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("profile store: decode %q: %w", name, err)
	}
	if stored.Profile == nil {
		return nil, fmt.Errorf("profile store: decode %q: missing profile payload", name)
	}
	return stored.Profile.Clone(), nil
}

func (s *ProfileStore) List(ctx context.Context) ([]*profile.Profile, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*profile.Profile, 0, len(recs))
	for _, rec := range recs {
		var stored profileStoredRecord
		if err := persis.Decode(rec, &stored); err != nil {
			return nil, fmt.Errorf("profile store: decode %q: %w", rec.ID, err)
		}
		if stored.Profile == nil {
			return nil, fmt.Errorf("profile store: decode %q: missing profile payload", rec.ID)
		}
		if profile.IsInheritedStorageName(stored.Profile.Name) {
			continue
		}
		out = append(out, stored.Profile.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *ProfileStore) Update(ctx context.Context, p *profile.Profile) error {
	if p == nil {
		return errors.New("profile store: profile cannot be nil")
	}
	if err := validateStoredProfile(p); err != nil {
		return err
	}
	rec, err := s.col.Get(ctx, p.Name)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return profile.ErrNotFound
		}
		return err
	}
	stored := p.Clone()
	data, err := persis.Encode(&profileStoredRecord{Profile: stored})
	if err != nil {
		return err
	}
	return s.col.Put(ctx, &persis.Record{
		ID:        stored.Name,
		Data:      data,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: time.Now().UTC(),
	})
}

func (s *ProfileStore) Delete(ctx context.Context, name string) error {
	if err := profile.ValidateName(name); err != nil {
		return err
	}
	if err := s.col.Delete(ctx, name); err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return profile.ErrNotFound
		}
		return err
	}
	return nil
}

func validateStoredProfile(p *profile.Profile) error {
	if err := validateProfileStorageName(p.Name); err != nil {
		return err
	}
	if profile.IsInheritedStorageName(p.Name) {
		if !p.Protected {
			return errors.New("profile store: inherited profiles must be protected")
		}
		if p.Status != profile.StatusActive {
			return errors.New("profile store: inherited profiles must be active")
		}
	}
	for _, entry := range p.Entries {
		if err := profile.ValidateKey(entry.Key); err != nil {
			return err
		}
	}
	return nil
}

func validateProfileStorageName(name string) error {
	if profile.IsInheritedStorageName(name) {
		return nil
	}
	return profile.ValidateName(name)
}
