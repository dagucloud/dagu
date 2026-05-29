// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/persis"
)

const licenseRecordID = "activation"

var _ license.ActivationStore = (*LicenseStore)(nil)

// LicenseStore implements [license.ActivationStore] over a single
// [persis.Collection] record.
type LicenseStore struct {
	rec *SingleRecord[license.ActivationData]
}

// NewLicenseStore creates a LicenseStore backed by col.
func NewLicenseStore(col persis.Collection) *LicenseStore {
	return &LicenseStore{rec: NewSingleRecord[license.ActivationData](col, licenseRecordID)}
}

// Load returns the activation data, or (nil, nil) when no record exists.
func (s *LicenseStore) Load() (*license.ActivationData, error) {
	var ad license.ActivationData
	found, err := s.rec.Load(context.Background(), &ad)
	if err != nil {
		return nil, fmt.Errorf("license store: load: %w", err)
	}
	if !found {
		return nil, nil
	}
	return &ad, nil
}

// Save replaces the stored activation data.
func (s *LicenseStore) Save(ad *license.ActivationData) error {
	if err := s.rec.Save(context.Background(), ad); err != nil {
		return fmt.Errorf("license store: save: %w", err)
	}
	return nil
}

// Remove deletes the activation record. Returns nil when no record exists.
func (s *LicenseStore) Remove() error {
	if err := s.rec.Delete(context.Background()); err != nil {
		return fmt.Errorf("license store: remove: %w", err)
	}
	return nil
}
