// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package extauthz

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
)

const (
	maxTokenBytes      = 8 << 10
	maxSubjectBytes    = 512
	maxKeysFileBytes   = 1 << 20
	algEdDSA           = "EdDSA"
	algES256           = "ES256"
	p256CoordinateSize = 32
)

// ErrMissingToken reports a request without an identity token.
var ErrMissingToken = errors.New("identity token is missing")

// TokenConfig configures identity token verification.
type TokenConfig struct {
	Issuer   string
	Audience string
	MaxAge   time.Duration
	// Now returns the current time; nil means time.Now.
	Now func() time.Time
}

// TokenVerifier verifies the identity tokens issued by the access gateway.
type TokenVerifier struct {
	cfg    TokenConfig
	keys   []PublicKey
	parser *jwt.Parser
}

// PublicKey is a verification key with an optional JWKS key ID.
type PublicKey struct {
	KeyID string
	Key   crypto.PublicKey
}

// NewTokenVerifier returns a verifier for the given public keys.
func NewTokenVerifier(cfg TokenConfig, keys []PublicKey) (*TokenVerifier, error) {
	if len(keys) == 0 {
		return nil, errors.New("no usable Ed25519 or P-256 public key")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{algEdDSA, algES256}),
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithAudience(cfg.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(cfg.Now),
		jwt.WithStrictDecoding(),
	)
	return &TokenVerifier{cfg: cfg, keys: keys, parser: parser}, nil
}

// Verify checks the compact JWT and returns its subject.
func (v *TokenVerifier) Verify(raw string) (string, error) {
	if raw == "" {
		return "", ErrMissingToken
	}
	if len(raw) > maxTokenBytes {
		return "", errors.New("identity token is too large")
	}

	var claims jwt.RegisteredClaims
	if _, err := v.parser.ParseWithClaims(raw, &claims, v.keyFunc); err != nil {
		return "", fmt.Errorf("invalid identity token: %w", err)
	}
	if claims.IssuedAt == nil {
		return "", errors.New("invalid identity token: iat claim is required")
	}
	if age := v.cfg.Now().Sub(claims.IssuedAt.Time); age > v.cfg.MaxAge {
		return "", fmt.Errorf("invalid identity token: token is older than %s", v.cfg.MaxAge)
	}
	if err := validateSubject(claims.Subject); err != nil {
		return "", err
	}
	return claims.Subject, nil
}

func (v *TokenVerifier) keyFunc(token *jwt.Token) (any, error) {
	kid, _ := token.Header["kid"].(string)
	var set jwt.VerificationKeySet
	for _, k := range v.keys {
		if kid != "" && k.KeyID != "" && k.KeyID != kid {
			continue
		}
		switch token.Method.Alg() {
		case algEdDSA:
			if key, ok := k.Key.(ed25519.PublicKey); ok {
				set.Keys = append(set.Keys, key)
			}
		case algES256:
			if key, ok := k.Key.(*ecdsa.PublicKey); ok {
				set.Keys = append(set.Keys, key)
			}
		}
	}
	if len(set.Keys) == 0 {
		return nil, errors.New("no verification key matches the token")
	}
	return set, nil
}

func validateSubject(sub string) error {
	if sub == "" {
		return errors.New("invalid identity token: sub claim is required")
	}
	if len(sub) > maxSubjectBytes {
		return errors.New("invalid identity token: sub claim is too long")
	}
	if strings.IndexFunc(sub, unicode.IsControl) >= 0 {
		return errors.New("invalid identity token: sub claim contains control characters")
	}
	return nil
}

// LoadPublicKeys reads Ed25519 and P-256 public keys from a PEM file or a
// JWKS document.
func LoadPublicKeys(path string) ([]PublicKey, error) {
	data, err := readLimitedFile(path, maxKeysFileBytes)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("{")) {
		return parseJWKS(trimmed)
	}
	return parsePEMKeys(trimmed)
}

func parsePEMKeys(data []byte) ([]PublicKey, error) {
	var keys []PublicKey
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "PUBLIC KEY" {
			return nil, fmt.Errorf("unsupported PEM block %q; expected PUBLIC KEY", block.Type)
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse public key: %w", err)
		}
		switch key := parsed.(type) {
		case ed25519.PublicKey:
			keys = append(keys, PublicKey{Key: key})
		case *ecdsa.PublicKey:
			if key.Curve != elliptic.P256() {
				return nil, errors.New("unsupported ECDSA curve; expected P-256")
			}
			keys = append(keys, PublicKey{Key: key})
		default:
			return nil, fmt.Errorf("unsupported public key type %T; expected Ed25519 or P-256", parsed)
		}
	}
	if len(bytes.TrimSpace(data)) > 0 {
		return nil, errors.New("public keys file contains data that is not PEM")
	}
	if len(keys) == 0 {
		return nil, errors.New("public keys file contains no PUBLIC KEY block")
	}
	return keys, nil
}

type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
}

// parseJWKS keeps the signature keys of the supported types and ignores the
// others, so that a gateway JWKS that also publishes RSA keys still loads.
func parseJWKS(data []byte) ([]PublicKey, error) {
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("parse JWKS: %w", err)
	}
	var keys []PublicKey
	for i, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch {
		case k.Kty == "OKP" && k.Crv == "Ed25519":
			x, err := base64.RawURLEncoding.DecodeString(k.X)
			if err != nil || len(x) != ed25519.PublicKeySize {
				return nil, fmt.Errorf("JWKS key %d: invalid Ed25519 x", i)
			}
			keys = append(keys, PublicKey{KeyID: k.Kid, Key: ed25519.PublicKey(x)})
		case k.Kty == "EC" && k.Crv == "P-256":
			x, errX := base64.RawURLEncoding.DecodeString(k.X)
			y, errY := base64.RawURLEncoding.DecodeString(k.Y)
			if errX != nil || errY != nil || len(x) != p256CoordinateSize || len(y) != p256CoordinateSize {
				return nil, fmt.Errorf("JWKS key %d: invalid P-256 coordinates", i)
			}
			point := append(append([]byte{4}, x...), y...)
			key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
			if err != nil {
				return nil, fmt.Errorf("JWKS key %d: %w", i, err)
			}
			keys = append(keys, PublicKey{KeyID: k.Kid, Key: key})
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("JWKS contains no Ed25519 or P-256 signature key")
	}
	return keys, nil
}

func readLimitedFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from the server configuration
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return data, nil
}
