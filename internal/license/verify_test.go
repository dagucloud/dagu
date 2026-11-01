// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signHS256Token creates a JWT signed with HMAC-SHA256 using the provided secret key.
// This is used to test algorithm rejection in VerifyToken and VerifyTokenLenient.
func signHS256Token(t *testing.T, secret []byte, claims *LicenseClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	require.NoError(t, err)
	return signed
}

// tamperToken modifies a single character in the payload portion of the JWT
// to simulate payload tampering without invalidating the structure.
func tamperToken(t *testing.T, tokenString string) string {
	t.Helper()
	parts := strings.Split(tokenString, ".")
	require.Len(t, parts, 3, "expected JWT to have 3 parts")

	payload := []byte(parts[1])
	// Flip one character in the payload to corrupt it.
	if payload[0] == 'A' {
		payload[0] = 'B'
	} else {
		payload[0] = 'A'
	}
	parts[1] = string(payload)
	return strings.Join(parts, ".")
}

func TestVerifyToken(t *testing.T) {
	t.Parallel()

	t.Run("valid token returns parsed claims", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		tokenString := signToken(t, priv, claims)

		got, err := VerifyToken(pub, tokenString)

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, claims.Plan, got.Plan)
		assert.Equal(t, claims.Features, got.Features)
		assert.Equal(t, claims.ActivationID, got.ActivationID)
	})

	t.Run("expired token returns error containing token verification failed", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := expiredInGraceClaims()
		tokenString := signToken(t, priv, claims)

		got, err := VerifyToken(pub, tokenString)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "token verification failed")
	})

	t.Run("wrong key returns error", func(t *testing.T) {
		t.Parallel()

		_, priv := testKeyPair(t)
		wrongPub, _ := testKeyPair(t)
		claims := validClaims()
		tokenString := signToken(t, priv, claims)

		got, err := VerifyToken(wrongPub, tokenString)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "token verification failed")
	})

	t.Run("tampered payload returns error", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		tokenString := signToken(t, priv, claims)
		tampered := tamperToken(t, tokenString)

		got, err := VerifyToken(pub, tampered)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "token verification failed")
	})

	t.Run("malformed string not a JWT returns error", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)

		got, err := VerifyToken(pub, "not-a-jwt")

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "token verification failed")
	})

	t.Run("HS256 signed token returns unexpected signing method error", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)
		secret := []byte("test-hmac-secret")
		claims := validClaims()
		tokenString := signHS256Token(t, secret, claims)

		got, err := VerifyToken(pub, tokenString)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "token verification failed")
	})

	t.Run("claims fields parsed correctly", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		tokenString := signToken(t, priv, claims)

		got, err := VerifyToken(pub, tokenString)

		require.NoError(t, err)
		require.NotNil(t, got)

		assert.Equal(t, 1, got.ClaimsVersion)
		assert.Equal(t, "pro", got.Plan)
		assert.Equal(t, []string{FeatureAudit, FeatureRBAC, FeatureSSO}, got.Features)
		assert.Equal(t, "act-test-123", got.ActivationID)
		require.NotNil(t, got.ExpiresAt)
		assert.True(t, got.ExpiresAt.After(time.Now()), "expiry should be in the future")
	})
}

// Dagu Cloud signs relay tickets and call assertions with the license key,
// each with an audience, so neither may pass for a license.
func TestVerifyRejectsTokensWithAnAudience(t *testing.T) {
	t.Parallel()

	pub, priv := testKeyPair(t)
	claims := validClaims()
	claims.Audience = jwt.ClaimStrings{"dagu-server"}
	tokenString := signToken(t, priv, claims)

	for name, verify := range map[string]func(ed25519.PublicKey, string) (*LicenseClaims, error){
		"VerifyToken": VerifyToken, "VerifyTokenLenient": VerifyTokenLenient,
	} {
		got, err := verify(pub, tokenString)
		require.Error(t, err, name)
		assert.Nil(t, got, name)
	}
}

func TestVerifyTokenLenient(t *testing.T) {
	t.Parallel()

	t.Run("expired token succeeds and returns claims", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := expiredInGraceClaims()
		tokenString := signToken(t, priv, claims)

		got, err := VerifyTokenLenient(pub, tokenString)

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, claims.Plan, got.Plan)
		assert.Equal(t, claims.Features, got.Features)
		assert.Equal(t, claims.ActivationID, got.ActivationID)
	})

	t.Run("valid token succeeds", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		tokenString := signToken(t, priv, claims)

		got, err := VerifyTokenLenient(pub, tokenString)

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, claims.Plan, got.Plan)
		assert.Equal(t, claims.Features, got.Features)
		assert.Equal(t, claims.ActivationID, got.ActivationID)
	})

	t.Run("wrong key returns error", func(t *testing.T) {
		t.Parallel()

		_, priv := testKeyPair(t)
		wrongPub, _ := testKeyPair(t)
		claims := validClaims()
		tokenString := signToken(t, priv, claims)

		got, err := VerifyTokenLenient(wrongPub, tokenString)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "token verification failed")
	})

	t.Run("tampered payload returns error", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		tokenString := signToken(t, priv, claims)
		tampered := tamperToken(t, tokenString)

		got, err := VerifyTokenLenient(pub, tampered)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "token verification failed")
	})

	t.Run("HS256 signed token returns unexpected signing method error", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)
		secret := []byte("test-hmac-secret")
		claims := validClaims()
		tokenString := signHS256Token(t, secret, claims)

		got, err := VerifyTokenLenient(pub, tokenString)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "token verification failed")
	})

	t.Run("expired token claims fields are accessible", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := expiredInGraceClaims()
		tokenString := signToken(t, priv, claims)

		got, err := VerifyTokenLenient(pub, tokenString)

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, 1, got.ClaimsVersion)
		assert.Equal(t, "pro", got.Plan)
		assert.Equal(t, []string{FeatureAudit, FeatureRBAC, FeatureSSO}, got.Features)
		assert.Equal(t, "act-test-456", got.ActivationID)
		require.NotNil(t, got.ExpiresAt)
		assert.True(t, got.ExpiresAt.Before(time.Now()), "expiry should be in the past")
	})

	t.Run("malformed string not a JWT returns error", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)

		got, err := VerifyTokenLenient(pub, "not-a-jwt")

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "token verification failed")
	})
}
