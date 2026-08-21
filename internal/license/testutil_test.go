// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"crypto/ed25519"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// testKeyPair generates a fresh Ed25519 key pair for testing.
func testKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return pub, priv
}

// signToken creates a signed JWT token using the given private key and claims.
func signToken(t *testing.T, privKey ed25519.PrivateKey, claims *LicenseClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(privKey)
	require.NoError(t, err)
	return signed
}

// validClaims returns claims for a pro plan with all 3 features, expiring in 24h.
func validClaims() *LicenseClaims {
	return &LicenseClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "dagu-test",
			Subject:   "test-license",
		},
		ClaimsVersion: 1,
		Plan:          "pro",
		Features:      []string{FeatureAudit, FeatureRBAC, FeatureSSO},
		ActivationID:  "act-test-123",
	}
}

// expiredInGraceClaims returns claims that expired 1 day ago (within 14-day grace period).
func expiredInGraceClaims() *LicenseClaims {
	return &LicenseClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-48 * time.Hour)),
			Issuer:    "dagu-test",
			Subject:   "test-license",
		},
		ClaimsVersion: 1,
		Plan:          "pro",
		Features:      []string{FeatureAudit, FeatureRBAC, FeatureSSO},
		ActivationID:  "act-test-456",
	}
}

// expiredPastGraceClaims returns claims that expired 15 days ago (past 14-day grace period).
func expiredPastGraceClaims() *LicenseClaims {
	return &LicenseClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-15 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-30 * 24 * time.Hour)),
			Issuer:    "dagu-test",
			Subject:   "test-license",
		},
		ClaimsVersion: 1,
		Plan:          "pro",
		Features:      []string{FeatureAudit, FeatureRBAC, FeatureSSO},
		ActivationID:  "act-test-789",
	}
}

// mockActivationStore is a test double for ActivationStore.
type mockActivationStore struct {
	mu          sync.Mutex
	data        *ActivationData
	loadErr     error
	saveErr     error
	removeErr   error
	saveCalls   int
	removeCalls int
}

func (m *mockActivationStore) Load() (*ActivationData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data, m.loadErr
}

func (m *mockActivationStore) Save(data *ActivationData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveCalls++
	if m.saveErr != nil {
		return m.saveErr
	}
	m.data = data
	return nil
}

func (m *mockActivationStore) Remove() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeCalls++
	if m.removeErr != nil {
		return m.removeErr
	}
	m.data = nil
	return nil
}
