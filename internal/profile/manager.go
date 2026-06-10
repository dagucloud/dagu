// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package profile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/secret"
)

// Manager coordinates profile mutations that span the profile and secret stores.
type Manager struct {
	profileStore Store
	secretStore  secret.Store
}

func NewManager(profileStore Store, secretStore secret.Store) *Manager {
	return &Manager{
		profileStore: profileStore,
		secretStore:  secretStore,
	}
}

func (m *Manager) SetVariable(ctx context.Context, p *Profile, key, value, actor string) (*Profile, error) {
	if m == nil || m.profileStore == nil {
		return nil, fmt.Errorf("profile store is not configured")
	}
	if p == nil {
		return nil, fmt.Errorf("profile cannot be nil")
	}
	if err := p.SetVariable(key, value, actor, time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := m.profileStore.Update(ctx, p); err != nil {
		return nil, err
	}
	return p.Clone(), nil
}

func (m *Manager) SetSecret(ctx context.Context, p *Profile, key, value, actor string) (*Profile, error) {
	if m == nil || m.profileStore == nil {
		return nil, fmt.Errorf("profile store is not configured")
	}
	if m.secretStore == nil {
		return nil, fmt.Errorf("secret store is not configured")
	}
	if p == nil {
		return nil, fmt.Errorf("profile cannot be nil")
	}
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if value == "" {
		return nil, fmt.Errorf("secret value must not be empty")
	}

	now := time.Now().UTC()
	ref := SecretRefForProfileName(p.Name, key)
	sec, err := m.secretStore.GetByRef(ctx, "", ref)
	if err == nil {
		return m.setExistingSecret(ctx, p, key, value, sec.ID, actor, now)
	}
	if !errors.Is(err, secret.ErrNotFound) {
		return nil, err
	}
	return m.createSecret(ctx, p, key, value, ref, actor, now)
}

func (m *Manager) DeleteEntry(ctx context.Context, p *Profile, key, actor string) error {
	if m == nil || m.profileStore == nil {
		return fmt.Errorf("profile store is not configured")
	}
	if p == nil {
		return fmt.Errorf("profile cannot be nil")
	}
	if err := p.DeleteEntry(key, actor, time.Now().UTC()); err != nil {
		return err
	}
	return m.profileStore.Update(ctx, p)
}

func (m *Manager) EnsureRunnable(ctx context.Context, name string) (*Profile, error) {
	if m == nil || m.profileStore == nil {
		return nil, fmt.Errorf("profile store is not configured")
	}
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	p, err := m.profileStore.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if p.Status == StatusDisabled {
		return nil, ErrDisabled
	}
	return p, nil
}

func (m *Manager) Resolve(ctx context.Context, name string) (*Resolved, error) {
	if m == nil {
		return nil, fmt.Errorf("profile store is not configured")
	}
	return NewResolver(m.profileStore, m.secretStore).Resolve(ctx, name)
}

func (m *Manager) setExistingSecret(
	ctx context.Context,
	p *Profile,
	key string,
	value string,
	secretID string,
	actor string,
	now time.Time,
) (*Profile, error) {
	original := p.Clone()
	entry, ok := profileEntry(p, key)
	if ok && entry.Kind != EntryKindSecret {
		return nil, fmt.Errorf("%w: %s", ErrDuplicateKey, key)
	}

	profileNeedsUpdate := !ok || entry.SecretID != secretID
	if profileNeedsUpdate {
		if err := p.SetSecret(key, secretID, actor, now); err != nil {
			return nil, err
		}
		if err := m.profileStore.Update(ctx, p); err != nil {
			return nil, err
		}
	}

	if _, err := m.secretStore.WriteValue(ctx, secretID, secret.WriteValueInput{
		Value:     value,
		CreatedBy: actor,
		CreatedAt: now,
	}); err != nil {
		if profileNeedsUpdate {
			if restoreErr := m.profileStore.Update(context.WithoutCancel(ctx), original); restoreErr != nil {
				return nil, fmt.Errorf("failed to write runtime profile secret: %w; failed to restore profile mapping: %v", err, restoreErr)
			}
		}
		return nil, err
	}

	return p.Clone(), nil
}

func (m *Manager) createSecret(
	ctx context.Context,
	p *Profile,
	key string,
	value string,
	ref string,
	actor string,
	now time.Time,
) (*Profile, error) {
	if entry, ok := profileEntry(p, key); ok && entry.Kind != EntryKindSecret {
		return nil, fmt.Errorf("%w: %s", ErrDuplicateKey, key)
	}

	sec, err := secret.New(secret.CreateInput{
		Workspace:    "",
		Ref:          ref,
		Description:  fmt.Sprintf("Runtime profile %s secret %s", p.Name, key),
		ProviderType: secret.ProviderDaguManaged,
		CreatedBy:    actor,
	}, now)
	if err != nil {
		return nil, err
	}
	if err := m.secretStore.Create(ctx, sec, &secret.WriteValueInput{
		Value:     value,
		CreatedBy: actor,
		CreatedAt: now,
	}); err != nil {
		return nil, err
	}

	if err := p.SetSecret(key, sec.ID, actor, now); err != nil {
		_ = m.secretStore.Delete(context.WithoutCancel(ctx), sec.ID)
		return nil, err
	}
	if err := m.profileStore.Update(ctx, p); err != nil {
		_ = m.secretStore.Delete(context.WithoutCancel(ctx), sec.ID)
		return nil, err
	}

	return p.Clone(), nil
}

func profileEntry(p *Profile, key string) (Entry, bool) {
	for _, entry := range p.Entries {
		if entry.Key == key {
			return entry, true
		}
	}
	return Entry{}, false
}
