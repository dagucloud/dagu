// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tokensecret

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/auth"
)

var _ auth.TokenSecretProvider = (*StaticProvider)(nil)

// StaticProvider resolves a token secret from a pre-configured string value.
// It performs no I/O and has no side effects.
type StaticProvider struct {
	secret auth.TokenSecret
}

// NewStatic creates a StaticProvider from a raw string.
// Returns an error if the string is empty (cannot construct a valid TokenSecret).
func NewStatic(raw string) (*StaticProvider, error) {
	ts, err := auth.NewTokenSecretFromString(raw)
	if err != nil {
		return nil, err
	}
	return &StaticProvider{secret: ts}, nil
}

// Resolve returns the pre-configured token secret.
func (p *StaticProvider) Resolve(_ context.Context) (auth.TokenSecret, error) {
	return p.secret, nil
}
