// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package providers

import (
	"context"
	"errors"
	"testing"

	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry(t *testing.T) {
	registry := NewRegistry("/tmp")
	require.NotNil(t, registry)

	// Should have built-in providers registered
	providers := registry.Providers()
	assert.Contains(t, providers, "env")
	assert.Contains(t, providers, "file")
	assert.Contains(t, providers, "vault")
	assert.Contains(t, providers, "kubernetes")
}

func TestRegistry_Register(t *testing.T) {
	registry := NewRegistry("/tmp")

	// Create a mock resolver
	mock := &mockResolver{mockName: "mock"}
	registry.Register("mock", mock)

	// Should be retrievable
	resolver := registry.Get("mock")
	require.NotNil(t, resolver)
	assert.Equal(t, "mock", resolver.Name())
}

func TestRegistry_Get(t *testing.T) {
	registry := NewRegistry("/tmp")

	t.Run("ExistingProvider", func(t *testing.T) {
		resolver := registry.Get("env")
		require.NotNil(t, resolver)
		assert.Equal(t, "env", resolver.Name())
	})

	t.Run("NonExistentProvider", func(t *testing.T) {
		resolver := registry.Get("nonexistent")
		assert.Nil(t, resolver)
	})
}

func TestRegistry_Resolve(t *testing.T) {
	ctx := context.Background()
	registry := NewRegistry("/tmp")

	// Set up environment variable for testing
	t.Setenv("TEST_SECRET", "test_value")

	t.Run("SuccessfulResolve", func(t *testing.T) {
		ref := secretref.Ref{
			Name:     "TEST_SECRET",
			Provider: "env",
			Key:      "TEST_SECRET",
		}

		value, err := registry.Resolve(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, "test_value", value)
	})

	t.Run("UnknownProvider", func(t *testing.T) {
		ref := secretref.Ref{
			Name:     "SECRET",
			Provider: "unknown",
			Key:      "key",
		}

		_, err := registry.Resolve(ctx, ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown secret provider: unknown")
	})

	t.Run("EmptyProvider", func(t *testing.T) {
		ref := secretref.Ref{
			Name:     "SECRET",
			Provider: "",
			Key:      "key",
		}

		_, err := registry.Resolve(ctx, ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "provider is required")
	})

	t.Run("InvalidReference", func(t *testing.T) {
		ref := secretref.Ref{
			Name:     "SECRET",
			Provider: "env",
			Key:      "", // Empty key
		}

		_, err := registry.Resolve(ctx, ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid secret reference")
	})

	t.Run("ResolutionFailure", func(t *testing.T) {
		ref := secretref.Ref{
			Name:     "NONEXISTENT",
			Provider: "env",
			Key:      "NONEXISTENT_VAR",
		}

		_, err := registry.Resolve(ctx, ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to resolve secret")
		assert.Contains(t, err.Error(), "NONEXISTENT")
	})

	t.Run("RegistryRefRequiresReferenceResolver", func(t *testing.T) {
		ref := secretref.Ref{
			Name: "DB_PASSWORD",
			Ref:  "prod/db-password",
		}

		_, err := registry.Resolve(ctx, ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "secret registry resolver is not configured")
	})

	t.Run("RegistryRefRejectsProviderFields", func(t *testing.T) {
		ref := secretref.Ref{
			Name:     "DB_PASSWORD",
			Ref:      "prod/db-password",
			Provider: "env",
			Key:      "DB_PASSWORD",
			Options:  map[string]string{"region": "us-east-1"},
		}

		_, err := registry.Resolve(ctx, ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "registry ref cannot include provider, key, or options")
	})

	t.Run("RegistryRefUsesReferenceResolver", func(t *testing.T) {
		registry := NewRegistryWithReferenceResolver(&mockReferenceResolver{
			resolveFunc: func(context.Context, secretref.Ref) (string, error) {
				return "managed-value", nil
			},
		}, "/tmp")

		value, err := registry.Resolve(ctx, secretref.Ref{
			Name: "DB_PASSWORD",
			Ref:  "prod/db-password",
		})
		require.NoError(t, err)
		assert.Equal(t, "managed-value", value)
	})
}

func TestRegistry_ResolveAll(t *testing.T) {
	ctx := context.Background()
	registry := NewRegistry("/tmp")

	t.Run("MultipleSecrets", func(t *testing.T) {
		t.Setenv("SECRET1", "value1")
		t.Setenv("SECRET2", "value2")

		refs := []secretref.Ref{
			{Name: "SECRET1", Provider: "env", Key: "SECRET1"},
			{Name: "SECRET2", Provider: "env", Key: "SECRET2"},
		}

		envVars, err := registry.ResolveAll(ctx, refs)
		require.NoError(t, err)
		assert.Len(t, envVars, 2)
		assert.Contains(t, envVars, "SECRET1=value1")
		assert.Contains(t, envVars, "SECRET2=value2")
	})

	t.Run("EmptyList", func(t *testing.T) {
		envVars, err := registry.ResolveAll(ctx, nil)
		require.NoError(t, err)
		assert.Nil(t, envVars)
	})

	t.Run("OneFailsAll", func(t *testing.T) {
		t.Setenv("SECRET1", "value1")

		refs := []secretref.Ref{
			{Name: "SECRET1", Provider: "env", Key: "SECRET1"},
			{Name: "MISSING", Provider: "env", Key: "MISSING_VAR"},
		}

		_, err := registry.ResolveAll(ctx, refs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "MISSING")
	})
}

func TestRegistry_CheckAccessibility(t *testing.T) {
	ctx := context.Background()
	registry := NewRegistry("/tmp")

	t.Run("AllAccessible", func(t *testing.T) {
		t.Setenv("SECRET1", "value1")
		t.Setenv("SECRET2", "value2")

		refs := []secretref.Ref{
			{Name: "SECRET1", Provider: "env", Key: "SECRET1"},
			{Name: "SECRET2", Provider: "env", Key: "SECRET2"},
		}

		err := registry.CheckAccessibility(ctx, refs)
		require.NoError(t, err)
	})

	t.Run("OneInaccessible", func(t *testing.T) {
		t.Setenv("SECRET1", "value1")

		refs := []secretref.Ref{
			{Name: "SECRET1", Provider: "env", Key: "SECRET1"},
			{Name: "MISSING", Provider: "env", Key: "MISSING_VAR"},
		}

		err := registry.CheckAccessibility(ctx, refs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "MISSING")
		assert.Contains(t, err.Error(), "not accessible")
	})

	t.Run("EmptyList", func(t *testing.T) {
		err := registry.CheckAccessibility(ctx, nil)
		require.NoError(t, err)
	})

	t.Run("UnknownProvider", func(t *testing.T) {
		refs := []secretref.Ref{
			{Name: "SECRET", Provider: "unknown", Key: "key"},
		}

		err := registry.CheckAccessibility(ctx, refs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown secret provider")
	})

	t.Run("RegistryRefRequiresReferenceResolver", func(t *testing.T) {
		refs := []secretref.Ref{
			{Name: "DB_PASSWORD", Ref: "prod/db-password"},
		}

		err := registry.CheckAccessibility(ctx, refs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "secret registry resolver is not configured")
	})

	t.Run("RegistryRefRejectsProviderFields", func(t *testing.T) {
		err := registry.CheckAccessibility(ctx, []secretref.Ref{
			{
				Name:     "DB_PASSWORD",
				Ref:      "prod/db-password",
				Provider: "env",
				Key:      "DB_PASSWORD",
				Options:  map[string]string{"region": "us-east-1"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "registry ref cannot include provider, key, or options")
	})

	t.Run("RegistryRefUsesReferenceResolver", func(t *testing.T) {
		checkCalled := false
		registry := NewRegistryWithReferenceResolver(&mockReferenceResolver{
			checkFunc: func(context.Context, secretref.Ref) error {
				checkCalled = true
				return nil
			},
		}, "/tmp")

		err := registry.CheckAccessibility(ctx, []secretref.Ref{
			{Name: "DB_PASSWORD", Ref: "prod/db-password"},
		})
		require.NoError(t, err)
		assert.True(t, checkCalled)
	})
}

func TestRegistry_CheckAccessibility_RequiresNoFetchCapability(t *testing.T) {
	ctx := context.Background()
	registry := NewRegistry("/tmp")

	checkCalled := false
	registry.Register("value-reader", &mockResolver{
		mockName: "value-reader",
		checkCapabilityFunc: func(secretref.Ref) CheckCapability {
			return CheckCapabilityRequiresValueRead
		},
		checkAccessFunc: func(context.Context, secretref.Ref) error {
			checkCalled = true
			return nil
		},
	})

	err := registry.CheckAccessibility(ctx, []secretref.Ref{
		{Name: "SECRET", Provider: "value-reader", Key: "path/to/secret"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires reading secret values")
	assert.False(t, checkCalled, "registry check must not call provider access checks that require value reads")
}

func TestRegistry_Providers(t *testing.T) {
	registry := NewRegistry("/tmp")

	providers := registry.Providers()
	assert.Contains(t, providers, "env")
	assert.Contains(t, providers, "file")
	assert.Contains(t, providers, "vault")
	assert.Contains(t, providers, "kubernetes")

	// Add custom provider
	mock := &mockResolver{mockName: "custom"}
	registry.Register("custom", mock)

	providers = registry.Providers()
	assert.Contains(t, providers, "custom")
}

func TestRegistry_Close(t *testing.T) {
	errOne := errors.New("close one")
	errTwo := errors.New("close two")
	closed := 0
	registry := &Registry{resolvers: map[string]Resolver{
		"one": &mockClosableResolver{
			mockName: "one",
			closeFunc: func() error {
				closed++
				return errOne
			},
		},
		"two": &mockClosableResolver{
			mockName: "two",
			closeFunc: func() error {
				closed++
				return errTwo
			},
		},
		"plain": &mockResolver{mockName: "plain"},
	}}

	err := registry.Close()
	assert.ErrorIs(t, err, errOne)
	assert.ErrorIs(t, err, errTwo)
	assert.Equal(t, 2, closed)
}

var _ Resolver = (*mockResolver)(nil)

// mockResolver is a test double for the Resolver interface
type mockResolver struct {
	mockName            string
	resolveFunc         func(context.Context, secretref.Ref) (string, error)
	validateFunc        func(secretref.Ref) error
	checkCapabilityFunc func(secretref.Ref) CheckCapability
	checkAccessFunc     func(context.Context, secretref.Ref) error
}

type mockClosableResolver struct {
	mockResolver
	closeFunc func() error
}

func (m *mockClosableResolver) Close() error {
	return m.closeFunc()
}

func (m *mockResolver) Name() string {
	return m.mockName
}

func (m *mockResolver) Resolve(ctx context.Context, ref secretref.Ref) (string, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(ctx, ref)
	}
	return "mock_value", nil
}

func (m *mockResolver) Validate(ref secretref.Ref) error {
	if m.validateFunc != nil {
		return m.validateFunc(ref)
	}
	return nil
}

func (m *mockResolver) CheckCapability(ref secretref.Ref) CheckCapability {
	if m.checkCapabilityFunc != nil {
		return m.checkCapabilityFunc(ref)
	}
	return CheckCapabilityNoFetch
}

func (m *mockResolver) CheckAccessibility(ctx context.Context, ref secretref.Ref) error {
	if m.checkAccessFunc != nil {
		return m.checkAccessFunc(ctx, ref)
	}
	return nil
}

var _ ReferenceResolver = (*mockReferenceResolver)(nil)

type mockReferenceResolver struct {
	resolveFunc func(context.Context, secretref.Ref) (string, error)
	checkFunc   func(context.Context, secretref.Ref) error
}

func (m *mockReferenceResolver) ResolveReference(ctx context.Context, ref secretref.Ref) (string, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(ctx, ref)
	}
	return "mock-reference-value", nil
}

func (m *mockReferenceResolver) CheckReferenceAccessibility(ctx context.Context, ref secretref.Ref) error {
	if m.checkFunc != nil {
		return m.checkFunc(ctx, ref)
	}
	return nil
}
