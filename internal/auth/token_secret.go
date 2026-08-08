// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"errors"
	"fmt"
)

// ErrInvalidTokenSecret indicates the token secret is empty or otherwise unusable.
var ErrInvalidTokenSecret = errors.New("invalid token secret")

// TokenSecret is an opaque handle to JWT signing key material.
// The zero value is invalid, forcing callers through constructors.
type TokenSecret struct {
	key []byte // unexported: prevents direct access
}

// NewTokenSecret creates a TokenSecret from raw key bytes.
// Returns ErrInvalidTokenSecret if key is nil or empty.
// The input is defensively copied to prevent mutation after construction.
func NewTokenSecret(key []byte) (TokenSecret, error) {
	if len(key) == 0 {
		return TokenSecret{}, ErrInvalidTokenSecret
	}
	cp := make([]byte, len(key))
	copy(cp, key)
	return TokenSecret{key: cp}, nil
}

// NewTokenSecretFromString creates a TokenSecret from a string value.
// Returns ErrInvalidTokenSecret if the string is empty.
func NewTokenSecretFromString(s string) (TokenSecret, error) {
	if s == "" {
		return TokenSecret{}, ErrInvalidTokenSecret
	}
	return NewTokenSecret([]byte(s))
}

// SigningKey returns a copy of the raw key material for JWT signing.
// This is the ONLY way to access the key bytes.
// Returns a defensive copy to prevent callers from mutating the internal key.
func (ts TokenSecret) SigningKey() []byte {
	if len(ts.key) == 0 {
		return nil
	}
	cp := make([]byte, len(ts.key))
	copy(cp, ts.key)
	return cp
}

// IsValid reports whether the TokenSecret holds usable key material.
func (ts TokenSecret) IsValid() bool {
	return len(ts.key) > 0
}

// String implements fmt.Stringer. Always returns "[REDACTED]" to prevent
// accidental logging of secret material.
func (ts TokenSecret) String() string {
	return "[REDACTED]"
}

// GoString implements fmt.GoStringer. Returns a safe representation for %#v.
func (ts TokenSecret) GoString() string {
	return "auth.TokenSecret{[REDACTED]}"
}

// MarshalJSON prevents the secret from being serialized into JSON.
func (ts TokenSecret) MarshalJSON() ([]byte, error) {
	return []byte(`"[REDACTED]"`), nil
}

// MarshalText prevents the secret from being serialized as text.
func (ts TokenSecret) MarshalText() ([]byte, error) {
	return []byte("[REDACTED]"), nil
}

// UnmarshalJSON rejects deserialization to prevent silent zero-value creation.
func (ts *TokenSecret) UnmarshalJSON([]byte) error {
	return fmt.Errorf("auth.TokenSecret must be constructed via NewTokenSecret or NewTokenSecretFromString, not deserialized from JSON")
}

// UnmarshalText rejects deserialization to prevent silent zero-value creation.
func (ts *TokenSecret) UnmarshalText([]byte) error {
	return fmt.Errorf("auth.TokenSecret must be constructed via NewTokenSecret or NewTokenSecretFromString, not deserialized from text")
}
