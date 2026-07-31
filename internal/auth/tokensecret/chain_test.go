// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tokensecret_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/auth/tokensecret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProvider is a test helper that returns a fixed result.
type mockProvider struct {
	secret auth.TokenSecret
	err    error
}

func (m *mockProvider) Resolve(_ context.Context) (auth.TokenSecret, error) {
	return m.secret, m.err
}

func TestChainProvider(t *testing.T) {
	t.Parallel()

	validSecret, err := auth.NewTokenSecretFromString("valid-secret")
	require.NoError(t, err)

	fallbackSecret, err := auth.NewTokenSecretFromString("fallback-secret")
	require.NoError(t, err)

	t.Run("first provider succeeds", func(t *testing.T) {
		t.Parallel()
		chain := tokensecret.NewChain(
			&mockProvider{secret: validSecret},
			&mockProvider{secret: fallbackSecret},
		)
		ts, err := chain.Resolve(context.Background())
		require.NoError(t, err)
		assert.Equal(t, validSecret.SigningKey(), ts.SigningKey())
	})

	t.Run("skips invalid and uses fallback", func(t *testing.T) {
		t.Parallel()
		chain := tokensecret.NewChain(
			&mockProvider{err: auth.ErrInvalidTokenSecret},
			&mockProvider{secret: fallbackSecret},
		)
		ts, err := chain.Resolve(context.Background())
		require.NoError(t, err)
		assert.Equal(t, fallbackSecret.SigningKey(), ts.SigningKey())
	})

	t.Run("fatal error stops chain", func(t *testing.T) {
		t.Parallel()
		fatalErr := errors.New("permission denied")
		chain := tokensecret.NewChain(
			&mockProvider{err: fatalErr},
			&mockProvider{secret: fallbackSecret},
		)
		_, err := chain.Resolve(context.Background())
		assert.ErrorIs(t, err, fatalErr)
	})

	t.Run("all providers exhausted", func(t *testing.T) {
		t.Parallel()
		chain := tokensecret.NewChain(
			&mockProvider{err: auth.ErrInvalidTokenSecret},
			&mockProvider{err: auth.ErrInvalidTokenSecret},
		)
		_, err := chain.Resolve(context.Background())
		assert.ErrorIs(t, err, auth.ErrInvalidTokenSecret)
	})

	t.Run("empty chain", func(t *testing.T) {
		t.Parallel()
		chain := tokensecret.NewChain()
		_, err := chain.Resolve(context.Background())
		assert.ErrorIs(t, err, auth.ErrInvalidTokenSecret)
	})
}
