// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// eddsaKeyFunc returns a jwt.Keyfunc that validates the signing method is EdDSA
// and provides the given Ed25519 public key for signature verification.
func eddsaKeyFunc(pubKey ed25519.PublicKey) jwt.Keyfunc {
	return func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return pubKey, nil
	}
}

// VerifyToken parses and verifies a JWT license token using the given Ed25519 public key.
// It rejects tokens that use algorithms other than EdDSA, and tokens with an
// audience, which Dagu Cloud signs with the same key for other purposes.
func VerifyToken(pubKey ed25519.PublicKey, tokenString string) (*LicenseClaims, error) {
	claims := &LicenseClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, eddsaKeyFunc(pubKey), jwt.WithValidMethods([]string{"EdDSA"}))
	if err != nil {
		return nil, fmt.Errorf("token verification failed: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("token is not valid")
	}
	if len(claims.Audience) > 0 {
		return nil, errNotALicense
	}
	return claims, nil
}

// errNotALicense refuses a token Dagu Cloud signed for another audience.
var errNotALicense = errors.New("token verification failed: the token has an audience, so it is not a license")

// VerifyTokenLenient parses a JWT license token without validating claims (e.g., expiry).
// This is used to extract claims from expired tokens for grace period evaluation.
// It still enforces EdDSA signature verification.
func VerifyTokenLenient(pubKey ed25519.PublicKey, tokenString string) (*LicenseClaims, error) {
	claims := &LicenseClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, eddsaKeyFunc(pubKey), jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithoutClaimsValidation())
	if err != nil {
		return nil, fmt.Errorf("token verification failed: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("token is not valid")
	}

	if len(claims.Audience) > 0 {
		return nil, errNotALicense
	}

	// Manually validate nbf and iat claims that were skipped by WithoutClaimsValidation.
	// Expiry (exp) is intentionally not checked here to allow grace period evaluation.
	now := time.Now()
	if claims.NotBefore != nil && now.Before(claims.NotBefore.Time) {
		return nil, fmt.Errorf("token is not yet valid (nbf)")
	}
	if claims.IssuedAt != nil && claims.IssuedAt.After(now) {
		return nil, fmt.Errorf("token has a future issued-at time (iat)")
	}

	return claims, nil
}
