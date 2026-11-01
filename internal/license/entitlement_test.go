// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
)

func TestHasActiveLicense(t *testing.T) {
	t.Parallel()

	t.Run("nil checker returns false", func(t *testing.T) {
		t.Parallel()
		assert.False(t, HasActiveLicense(nil))
	})

	t.Run("community state returns false", func(t *testing.T) {
		t.Parallel()
		var s State
		assert.False(t, HasActiveLicense(&s))
	})

	t.Run("valid license does not require feature claim", func(t *testing.T) {
		t.Parallel()
		manager := NewTestManager()
		assert.True(t, HasActiveLicense(manager.Checker()))
	})

	t.Run("expired license in grace period returns true", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(expiredInGraceClaims(), "tok")
		assert.True(t, HasActiveLicense(&s))
	})

	t.Run("expired license past grace period returns false", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(expiredPastGraceClaims(), "tok")
		assert.False(t, HasActiveLicense(&s))
	})

	t.Run("free license returns false", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(freeClaims(), "tok")
		assert.False(t, HasActiveLicense(&s))
	})

	t.Run("perpetual license returns true", func(t *testing.T) {
		t.Parallel()
		var s State
		s.Update(&LicenseClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: nil,
			},
			ClaimsVersion: 1,
			Plan:          "pro",
			ActivationID:  "act-test",
		}, "tok")
		assert.True(t, HasActiveLicense(&s))
	})

	t.Run("expired trial with zero grace returns false", func(t *testing.T) {
		t.Parallel()
		var s State
		zero := 0
		s.Update(&LicenseClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			},
			ClaimsVersion: 1,
			Plan:          "trial",
			GraceDays:     &zero,
		}, "tok")
		assert.False(t, HasActiveLicense(&s))
	})
}

func TestHasLicense(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		claims *LicenseClaims
		want   bool
	}{
		{name: "community", claims: nil, want: false},
		{name: "paid license", claims: validClaims(), want: true},
		{name: "free license", claims: freeClaims(), want: true},
		{name: "expired license in grace period", claims: expiredInGraceClaims(), want: true},
		{name: "expired license past grace period", claims: expiredPastGraceClaims(), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var s State
			s.Update(tc.claims, "tok")
			assert.Equal(t, tc.want, HasLicense(&s))
		})
	}

	t.Run("nil checker", func(t *testing.T) {
		t.Parallel()
		assert.False(t, HasLicense(nil))
	})
}
