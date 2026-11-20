// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package extauthz

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

var testNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

type testKeys struct {
	edPriv ed25519.PrivateKey
	edPub  ed25519.PublicKey
	ecPriv *ecdsa.PrivateKey
}

func newTestKeys(t *testing.T) testKeys {
	t.Helper()
	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	return testKeys{edPriv: edPriv, edPub: edPub, ecPriv: ecPriv}
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss": "https://gateway.example.org",
		"aud": "dagu",
		"sub": "alice",
		"iat": testNow.Add(-10 * time.Second).Unix(),
		"exp": testNow.Add(50 * time.Second).Unix(),
	}
}

func signEd(t *testing.T, key ed25519.PrivateKey, claims jwt.MapClaims, kid string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

func signES(t *testing.T, key *ecdsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodES256, claims).SignedString(key)
	require.NoError(t, err)
	return signed
}

func pemPublicKey(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func writeFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func newTestVerifier(t *testing.T, keys testKeys) *TokenVerifier {
	t.Helper()
	data := append(pemPublicKey(t, keys.edPub), pemPublicKey(t, &keys.ecPriv.PublicKey)...)
	loaded, err := LoadPublicKeys(writeFile(t, data))
	require.NoError(t, err)
	verifier, err := NewTokenVerifier(TokenConfig{
		Issuer:   "https://gateway.example.org",
		Audience: "dagu",
		MaxAge:   time.Minute,
		Now:      func() time.Time { return testNow },
	}, loaded)
	require.NoError(t, err)
	return verifier
}

func TestVerifyAcceptsEdDSAAndES256(t *testing.T) {
	t.Parallel()
	keys := newTestKeys(t)
	verifier := newTestVerifier(t, keys)

	sub, err := verifier.Verify(signEd(t, keys.edPriv, validClaims(), ""))
	require.NoError(t, err)
	require.Equal(t, "alice", sub)

	sub, err = verifier.Verify(signES(t, keys.ecPriv, validClaims()))
	require.NoError(t, err)
	require.Equal(t, "alice", sub)
}

func TestVerifyRejectsInvalidTokens(t *testing.T) {
	t.Parallel()
	keys := newTestKeys(t)
	other := newTestKeys(t)
	verifier := newTestVerifier(t, keys)

	withClaim := func(key string, value any) string {
		claims := validClaims()
		if value == nil {
			delete(claims, key)
		} else {
			claims[key] = value
		}
		return signEd(t, keys.edPriv, claims, "")
	}
	hs256, err := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims()).SignedString([]byte("shared-secret"))
	require.NoError(t, err)
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims()).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	tests := []struct {
		name  string
		token string
	}{
		{name: "Empty", token: ""},
		{name: "Garbage", token: "not-a-jwt"},
		{name: "WrongKey", token: signEd(t, other.edPriv, validClaims(), "")},
		{name: "HS256", token: hs256},
		{name: "None", token: unsigned},
		{name: "WrongIssuer", token: withClaim("iss", "https://evil.example.org")},
		{name: "WrongAudience", token: withClaim("aud", "other")},
		{name: "Expired", token: withClaim("exp", testNow.Add(-time.Second).Unix())},
		{name: "MissingExp", token: withClaim("exp", nil)},
		{name: "MissingIat", token: withClaim("iat", nil)},
		{name: "TooOld", token: withClaim("iat", testNow.Add(-2*time.Minute).Unix())},
		{name: "IssuedInFuture", token: withClaim("iat", testNow.Add(time.Minute).Unix())},
		{name: "MissingSub", token: withClaim("sub", nil)},
		{name: "ControlInSub", token: withClaim("sub", "alice\nadmin")},
		{name: "TooLarge", token: strings.Repeat("a", maxTokenBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := verifier.Verify(tt.token)
			require.Error(t, err)
		})
	}
}

func TestLoadPublicKeysJWKS(t *testing.T) {
	t.Parallel()
	keys := newTestKeys(t)
	other := newTestKeys(t)

	b64 := base64.RawURLEncoding.EncodeToString
	ecPub := keys.ecPriv.PublicKey
	ecBytes, err := ecPub.Bytes()
	require.NoError(t, err)
	jwks := map[string]any{"keys": []map[string]any{
		{"kty": "RSA", "n": "AQAB", "e": "AQAB", "kid": "rsa"},
		{"kty": "OKP", "crv": "Ed25519", "x": b64(other.edPub), "kid": "old", "use": "enc"},
		{"kty": "OKP", "crv": "Ed25519", "x": b64(keys.edPub), "kid": "k1", "use": "sig"},
		{"kty": "EC", "crv": "P-256", "x": b64(ecBytes[1:33]), "y": b64(ecBytes[33:]), "kid": "k2"},
	}}
	data, err := json.Marshal(jwks)
	require.NoError(t, err)

	loaded, err := LoadPublicKeys(writeFile(t, data))
	require.NoError(t, err)
	require.Len(t, loaded, 2)

	verifier, err := NewTokenVerifier(TokenConfig{
		Issuer: "https://gateway.example.org", Audience: "dagu", MaxAge: time.Minute,
		Now: func() time.Time { return testNow },
	}, loaded)
	require.NoError(t, err)

	_, err = verifier.Verify(signEd(t, keys.edPriv, validClaims(), "k1"))
	require.NoError(t, err)
	_, err = verifier.Verify(signES(t, keys.ecPriv, validClaims()))
	require.NoError(t, err)
	// A known kid selects only that key.
	_, err = verifier.Verify(signEd(t, keys.edPriv, validClaims(), "k2"))
	require.Error(t, err)
	// The key with use "enc" is not loaded.
	_, err = verifier.Verify(signEd(t, other.edPriv, validClaims(), "old"))
	require.Error(t, err)
}

func TestLoadPublicKeysRejectsBadFiles(t *testing.T) {
	t.Parallel()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)

	tests := []struct {
		name string
		data []byte
	}{
		{name: "Empty", data: nil},
		{name: "RSA", data: pemPublicKey(t, &rsaKey.PublicKey)},
		{name: "P384", data: pemPublicKey(t, &p384.PublicKey)},
		{name: "PrivateKeyBlock", data: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}})},
		{name: "TrailingGarbage", data: append(pemPublicKey(t, newTestKeys(t).edPub), []byte("junk")...)},
		{name: "JWKSWithoutUsableKey", data: []byte(`{"keys":[{"kty":"RSA","n":"AQAB","e":"AQAB"}]}`)},
		{name: "JWKSBadEd25519", data: []byte(`{"keys":[{"kty":"OKP","crv":"Ed25519","x":"AAAA"}]}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadPublicKeys(writeFile(t, tt.data))
			require.Error(t, err)
		})
	}

	_, err = LoadPublicKeys(filepath.Join(t.TempDir(), "missing.pem"))
	require.Error(t, err)
}
