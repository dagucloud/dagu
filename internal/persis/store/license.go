// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/persis"
)

// licenseRecordID is the single record ID under which activation data is
// stored. With the file backend, this maps to "{collection_dir}/activation.json"
// — byte-identical to the pre-refactor [filelicense] on-disk layout.
const licenseRecordID = "activation"

var _ license.ActivationStore = (*LicenseStore)(nil)

// LicenseStore implements [license.ActivationStore] by persisting a single
// record (id "activation") in a [persis.Collection].
type LicenseStore struct {
	col persis.Collection
}

// NewLicenseStore creates a LicenseStore backed by col.
func NewLicenseStore(col persis.Collection) *LicenseStore {
	return &LicenseStore{col: col}
}

// Load returns the activation data, or (nil, nil) when no record exists.
func (s *LicenseStore) Load() (*license.ActivationData, error) {
	rec, err := s.col.Get(context.Background(), licenseRecordID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("license store: load: %w", err)
	}
	var ad license.ActivationData
	if err := persis.Decode(rec, &ad); err != nil {
		return nil, fmt.Errorf("license store: decode: %w", err)
	}
	return &ad, nil
}

// Save replaces the stored activation data.
func (s *LicenseStore) Save(ad *license.ActivationData) error {
	data, err := persis.Encode(ad)
	if err != nil {
		return fmt.Errorf("license store: encode: %w", err)
	}
	if err := s.col.Put(context.Background(), &persis.Record{
		ID:   licenseRecordID,
		Data: data,
	}); err != nil {
		return fmt.Errorf("license store: save: %w", err)
	}
	return nil
}

// Remove deletes the activation record. Idempotent: returns nil when no record exists.
func (s *LicenseStore) Remove() error {
	if err := s.col.Delete(context.Background(), licenseRecordID); err != nil {
		return fmt.Errorf("license store: remove: %w", err)
	}
	return nil
}
