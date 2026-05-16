// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secret

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/core"
)

type ReferenceResolver struct {
	store     Store
	workspace string
}

func NewReferenceResolver(store Store, workspace string) *ReferenceResolver {
	return &ReferenceResolver{
		store:     store,
		workspace: workspace,
	}
}

func (r *ReferenceResolver) ResolveReference(ctx context.Context, ref core.SecretRef) (string, error) {
	if r == nil || r.store == nil {
		return "", fmt.Errorf("secret store is not configured")
	}
	sec, err := r.store.GetByRef(ctx, r.workspace, ref.Ref)
	if err != nil {
		return "", err
	}
	if err := ensureResolvable(sec); err != nil {
		return "", err
	}
	value, _, err := r.store.ResolveValue(ctx, sec.ID)
	return value, err
}

func (r *ReferenceResolver) CheckReferenceAccessibility(ctx context.Context, ref core.SecretRef) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("secret store is not configured")
	}
	sec, err := r.store.GetByRef(ctx, r.workspace, ref.Ref)
	if err != nil {
		return err
	}
	if err := ensureResolvable(sec); err != nil {
		return err
	}
	_, err = r.store.GetCurrentVersion(ctx, sec.ID)
	return err
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
