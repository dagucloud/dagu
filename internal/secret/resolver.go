// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secret

import (
	"context"
	"errors"
	"fmt"

	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

type ReferenceResolver struct {
	store     Store
	workspace string
}

func NewReferenceResolver(store Store, workspace string) *ReferenceResolver {
	return &ReferenceResolver{
		store:     store,
		workspace: NormalizeWorkspace(workspace),
	}
}

func (r *ReferenceResolver) ResolveReference(ctx context.Context, ref secretref.Ref) (string, error) {
	sec, err := r.lookup(ctx, ref)
	if err != nil {
		return "", err
	}
	value, _, err := r.store.ResolveValue(ctx, sec.ID)
	return value, err
}

// ReadReference returns the secret ref resolves to and its current value,
// like ResolveReference, without recording the resolution on the secret.
func (r *ReferenceResolver) ReadReference(ctx context.Context, ref secretref.Ref) (*Secret, string, error) {
	sec, err := r.lookup(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	value, _, err := r.store.ReadValue(ctx, sec.ID)
	if err != nil {
		return nil, "", err
	}
	return sec, value, nil
}

func (r *ReferenceResolver) CheckReferenceAccessibility(ctx context.Context, ref secretref.Ref) error {
	sec, err := r.lookup(ctx, ref)
	if err != nil {
		return err
	}
	_, err = r.store.GetCurrentVersion(ctx, sec.ID)
	return err
}

func (r *ReferenceResolver) lookup(ctx context.Context, ref secretref.Ref) (*Secret, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("secret store is not configured")
	}
	sec, err := r.getByRef(ctx, ref.Ref)
	if err != nil {
		return nil, err
	}
	if err := ensureResolvable(sec); err != nil {
		return nil, err
	}
	return sec, nil
}

func (r *ReferenceResolver) getByRef(ctx context.Context, ref string) (*Secret, error) {
	sec, err := r.store.GetByRef(ctx, r.workspace, ref)
	if err == nil {
		return sec, nil
	}
	if !errors.Is(err, ErrNotFound) || r.workspace == GlobalWorkspace {
		return nil, err
	}
	return r.store.GetByRef(ctx, GlobalWorkspace, ref)
}

func ensureResolvable(sec *Secret) error {
	if sec.Status == StatusDisabled {
		return ErrDisabled
	}
	if sec.ProviderType != ProviderDaguManaged {
		return fmt.Errorf("%w: %s", ErrUnsupportedProvider, sec.ProviderType)
	}
	return nil
}
