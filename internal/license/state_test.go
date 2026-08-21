// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestState_ZeroValue(t *testing.T) {
	t.Parallel()

	t.Run("Claims returns nil", func(t *testing.T) {
		t.Parallel()
		var s State
		assert.Nil(t, s.Claims())
	})

	t.Run("Token returns empty string", func(t *testing.T) {
		t.Parallel()
		var s State
		assert.Equal(t, "", s.Token())
	})

	t.Run("IsCommunity returns true", func(t *testing.T) {
		t.Parallel()
		var s State
		assert.True(t, s.IsCommunity())
	})

	t.Run("Plan returns empty string", func(t *testing.T) {
		t.Parallel()
		var s State
		assert.Equal(t, "", s.Plan())
	})
}

func TestState_Update(t *testing.T) {
	t.Parallel()

	t.Run("set claims then read back all fields", func(t *testing.T) {
		t.Parallel()
		var s State
		claims := validClaims()
		token := "test-token-value"

		s.Update(claims, token)

		gotClaims := s.Claims()
		require.NotNil(t, gotClaims)
		assert.Equal(t, claims.Plan, gotClaims.Plan)
		assert.Equal(t, claims.Features, gotClaims.Features)
		assert.Equal(t, claims.ActivationID, gotClaims.ActivationID)
		assert.Equal(t, token, s.Token())
		assert.False(t, s.IsCommunity())
		assert.Equal(t, "pro", s.Plan())
	})

	t.Run("set claims then clear to nil", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(validClaims(), "some-token")

		// Verify state was set.
		require.NotNil(t, s.Claims())
		require.Equal(t, "some-token", s.Token())

		// Now clear.
		s.Update(nil, "")

		assert.Nil(t, s.Claims())
		assert.Equal(t, "", s.Token())
		assert.True(t, s.IsCommunity())
		assert.Equal(t, "", s.Plan())
	})
}

func TestState_ClaimsDeepCopy(t *testing.T) {
	t.Parallel()

	t.Run("mutating returned claims does not affect state", func(t *testing.T) {
		t.Parallel()
		var s State
		claims := validClaims()
		graceDays := 7
		claims.GraceDays = &graceDays
		s.Update(claims, "tok")

		got := s.Claims()
		require.NotNil(t, got)
		got.Features = append(got.Features, "mutated-feature")
		got.Plan = "mutated-plan"
		require.NotNil(t, got.GraceDays)
		*got.GraceDays = 0
		require.NotNil(t, got.ExpiresAt)
		got.ExpiresAt.Time = time.Now().Add(-24 * time.Hour)

		original := s.Claims()
		require.NotNil(t, original)
		assert.Equal(t, "pro", original.Plan)
		assert.NotContains(t, original.Features, "mutated-feature")
		require.NotNil(t, original.GraceDays)
		assert.Equal(t, 7, *original.GraceDays)
		require.NotNil(t, original.ExpiresAt)
		assert.True(t, original.ExpiresAt.After(time.Now()))
	})

	t.Run("mutating input claims does not affect stored state", func(t *testing.T) {
		t.Parallel()
		var s State
		claims := validClaims()
		graceDays := 5
		claims.GraceDays = &graceDays
		s.Update(claims, "tok")

		claims.Features = append(claims.Features, "injected")
		claims.Plan = "injected-plan"
		*claims.GraceDays = 0
		require.NotNil(t, claims.ExpiresAt)
		claims.ExpiresAt.Time = time.Now().Add(-24 * time.Hour)

		got := s.Claims()
		require.NotNil(t, got)
		assert.Equal(t, "pro", got.Plan)
		assert.NotContains(t, got.Features, "injected")
		require.NotNil(t, got.GraceDays)
		assert.Equal(t, 5, *got.GraceDays)
		require.NotNil(t, got.ExpiresAt)
		assert.True(t, got.ExpiresAt.After(time.Now()))
	})
}

func TestState_IsFeatureEnabled(t *testing.T) {
	t.Parallel()

	t.Run("valid token with feature present returns true", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(validClaims(), "tok")
		assert.True(t, s.IsFeatureEnabled(FeatureAudit))
		assert.True(t, s.IsFeatureEnabled(FeatureRBAC))
		assert.True(t, s.IsFeatureEnabled(FeatureSSO))
	})

	t.Run("nil claims returns false", func(t *testing.T) {
		t.Parallel()
		var s State
		assert.False(t, s.IsFeatureEnabled(FeatureAudit))
	})

	t.Run("feature absent returns false", func(t *testing.T) {
		t.Parallel()
		var s State
		claims := &LicenseClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			},
			Plan:     "pro",
			Features: []string{FeatureRBAC},
		}
		s.Update(claims, "tok")
		assert.False(t, s.IsFeatureEnabled(FeatureAudit))
		assert.False(t, s.IsFeatureEnabled(FeatureSSO))
		assert.True(t, s.IsFeatureEnabled(FeatureRBAC))
	})

	t.Run("expired in grace period returns true", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(expiredInGraceClaims(), "tok")
		assert.True(t, s.IsFeatureEnabled(FeatureAudit))
		assert.True(t, s.IsFeatureEnabled(FeatureRBAC))
		assert.True(t, s.IsFeatureEnabled(FeatureSSO))
	})

	t.Run("expired past grace period returns false", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(expiredPastGraceClaims(), "tok")
		assert.False(t, s.IsFeatureEnabled(FeatureAudit))
		assert.False(t, s.IsFeatureEnabled(FeatureRBAC))
		assert.False(t, s.IsFeatureEnabled(FeatureSSO))
	})

	t.Run("perpetual token (nil ExpiresAt) enables claimed features", func(t *testing.T) {
		t.Parallel()
		var s State
		claims := &LicenseClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: nil,
			},
			Plan:     "pro",
			Features: []string{FeatureAudit, FeatureRBAC, FeatureSSO},
		}
		s.Update(claims, "tok")
		assert.True(t, s.IsFeatureEnabled(FeatureAudit))
		assert.True(t, s.IsFeatureEnabled(FeatureRBAC))
		assert.True(t, s.IsFeatureEnabled(FeatureSSO))
		// A feature not in the claims should still return false.
		assert.False(t, s.IsFeatureEnabled("nonexistent-feature"))
	})

	t.Run("expired trial with zero grace disables features immediately", func(t *testing.T) {
		t.Parallel()
		var s State
		zero := 0
		claims := &LicenseClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			},
			Plan:      "trial",
			Features:  []string{FeatureAudit},
			GraceDays: &zero,
		}
		s.Update(claims, "tok")
		assert.False(t, s.IsFeatureEnabled(FeatureAudit))
		assert.False(t, s.IsGracePeriod())
	})
}

func TestState_IsGracePeriod(t *testing.T) {
	t.Parallel()

	t.Run("not expired returns false", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(validClaims(), "tok")
		assert.False(t, s.IsGracePeriod())
	})

	t.Run("just expired 1 day ago returns true", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(expiredInGraceClaims(), "tok")
		assert.True(t, s.IsGracePeriod())
	})

	t.Run("boundary 14 days minus 1 second returns true", func(t *testing.T) {
		t.Parallel()
		var s State
		// Expired exactly (14d - 1s) ago — still within grace window.
		expiry := time.Now().Add(-(gracePeriod - time.Second))
		claims := &LicenseClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(expiry),
			},
			Plan:     "pro",
			Features: []string{FeatureAudit},
		}
		s.Update(claims, "tok")
		assert.True(t, s.IsGracePeriod())
	})

	t.Run("boundary 14 days plus 1 second returns false", func(t *testing.T) {
		t.Parallel()
		var s State
		// Expired (14d + 1s) ago — just past the grace window.
		expiry := time.Now().Add(-(gracePeriod + time.Second))
		claims := &LicenseClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(expiry),
			},
			Plan:     "pro",
			Features: []string{FeatureAudit},
		}
		s.Update(claims, "tok")
		assert.False(t, s.IsGracePeriod())
	})

	t.Run("nil claims returns false", func(t *testing.T) {
		t.Parallel()
		var s State
		assert.False(t, s.IsGracePeriod())
	})

	t.Run("nil ExpiresAt returns false", func(t *testing.T) {
		t.Parallel()
		var s State
		claims := &LicenseClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: nil,
			},
			Plan:     "pro",
			Features: []string{FeatureAudit},
		}
		s.Update(claims, "tok")
		assert.False(t, s.IsGracePeriod())
	})
}

func TestState_Concurrency(t *testing.T) {
	t.Parallel()

	// This test validates that concurrent access to State does not race.
	// Run with -race to detect any data races.
	var s State

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			switch i % 7 {
			case 0:
				s.Update(validClaims(), "tok-valid")
			case 1:
				s.Update(expiredInGraceClaims(), "tok-grace")
			case 2:
				s.Update(nil, "")
			case 3:
				_ = s.Claims()
			case 4:
				_ = s.Token()
			case 5:
				_ = s.IsFeatureEnabled(FeatureAudit)
				_ = s.IsFeatureEnabled(FeatureRBAC)
				_ = s.IsFeatureEnabled(FeatureSSO)
			case 6:
				_ = s.Plan()
				_ = s.IsGracePeriod()
				_ = s.IsCommunity()
				_ = s.WarningCode()
			}
		}(i)
	}

	wg.Wait()
	// No assertion needed — the test passes if no race is detected.
}

// TestState_Plan verifies Plan() independently of the Update round-trip test.
func TestState_Plan(t *testing.T) {
	t.Parallel()

	t.Run("returns plan from claims", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(validClaims(), "tok")
		assert.Equal(t, "pro", s.Plan())
	})

	t.Run("returns empty string when nil claims", func(t *testing.T) {
		t.Parallel()
		var s State
		assert.Equal(t, "", s.Plan())
	})
}

// TestState_IsCommunity verifies community detection beyond the zero-value case.
func TestState_IsCommunity(t *testing.T) {
	t.Parallel()

	t.Run("returns false when claims are loaded", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(validClaims(), "tok")
		assert.False(t, s.IsCommunity())
	})

	t.Run("returns true after claims cleared to nil", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(validClaims(), "tok")
		s.Update(nil, "")
		assert.True(t, s.IsCommunity())
	})
}

// TestState_WarningCode verifies WarningCode() accessor.
func TestState_WarningCode(t *testing.T) {
	t.Parallel()

	t.Run("returns empty string when claims are nil", func(t *testing.T) {
		t.Parallel()
		var s State
		assert.Equal(t, "", s.WarningCode())
	})

	t.Run("returns empty string when warning code is empty", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(validClaims(), "tok")
		assert.Equal(t, "", s.WarningCode())
	})

	t.Run("returns warning code when set", func(t *testing.T) {
		t.Parallel()
		var s State
		claims := validClaims()
		claims.WarningCode = "MACHINE_LIMIT_EXCEEDED"
		s.Update(claims, "tok")
		assert.Equal(t, "MACHINE_LIMIT_EXCEEDED", s.WarningCode())
	})
}

// Compile-time check: *State satisfies the Checker interface.
var _ Checker = (*State)(nil)
