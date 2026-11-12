// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudaccess

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/license"
)

const (
	// assertionAudience and assertionUse set an assertion apart from the
	// licenses and relay tickets Dagu Cloud signs with the same key.
	assertionAudience = "dagu-server"
	assertionUse      = "call"
	// maxAssertionLifetime bounds how long after it was issued an assertion
	// is good.
	maxAssertionLifetime = time.Minute
	// clockSkew is how far this server's clock may be from Dagu Cloud's.
	clockSkew = time.Minute
)

// assertion is what Dagu Cloud vouches for on one call.
type assertion struct {
	jwt.RegisteredClaims
	Use       string `json:"use"`
	LicenseID string `json:"license_id"`
	ServerID  string `json:"server_id"`
	Email     string `json:"email"`
	// Workspace is the Dagu Cloud workspace the call comes from.
	Workspace string `json:"cloud_workspace"`
	// Role is the role Dagu Cloud grants the user on this server.
	Role string `json:"role"`
	// Grant identifies the connection the user made from an AI app, and
	// Client names the app.
	Grant  string `json:"grant"`
	Client string `json:"client"`
	// RequestSHA256 binds the assertion to the call's request.
	RequestSHA256 string `json:"req_sha256"`
}

// verifier checks the assertions on calls from Dagu Cloud, and accepts each
// one once.
type verifier struct {
	key ed25519.PublicKey
	now func() time.Time

	mu sync.Mutex
	// seen holds the IDs of the assertions accepted, until they expire.
	seen map[string]time.Time
}

func newVerifier(key ed25519.PublicKey) *verifier {
	return &verifier{key: key, now: time.Now, seen: make(map[string]time.Time)}
}

// verify returns what token asserts about the call it came with: method,
// target, and body, made to this server under creds. It refuses a token
// accepted before.
func (v *verifier) verify(token string, creds license.CloudCredentials, method, target string, body []byte) (*assertion, error) {
	a := &assertion{}
	_, err := jwt.ParseWithClaims(token, a, func(*jwt.Token) (any, error) { return v.key, nil },
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithAudience(assertionAudience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(clockSkew),
		jwt.WithTimeFunc(v.now),
	)
	if err != nil {
		return nil, err
	}
	switch {
	case a.IssuedAt == nil || a.ExpiresAt.Sub(a.IssuedAt.Time) > maxAssertionLifetime:
		return nil, errors.New("the assertion is good for longer than a minute")
	case a.Use != assertionUse:
		return nil, fmt.Errorf("the assertion is for %q, not a call", a.Use)
	case a.ServerID != creds.ServerID || a.LicenseID != creds.LicenseID:
		return nil, errors.New("the assertion is for another server")
	case a.Subject == "" || a.ID == "":
		return nil, errors.New("the assertion names no user or no ID")
	}
	if !slices.Contains(roleOrder, auth.Role(a.Role)) {
		return nil, fmt.Errorf("the assertion grants the role %q, which calls never get", a.Role)
	}
	want := requestDigest(method, target, body)
	if subtle.ConstantTimeCompare([]byte(a.RequestSHA256), []byte(want)) != 1 {
		return nil, errors.New("the assertion is for another request")
	}
	if !v.firstUse(a.ID, a.ExpiresAt.Add(clockSkew)) {
		return nil, errors.New("the assertion was used before")
	}
	return a, nil
}

// firstUse records an assertion ID until forget, and reports false for one
// recorded before.
func (v *verifier) firstUse(id string, forget time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	for seen, until := range v.seen {
		if now.After(until) {
			delete(v.seen, seen)
		}
	}
	if _, ok := v.seen[id]; ok {
		return false
	}
	v.seen[id] = forget
	return true
}

// requestDigest is what an assertion's req_sha256 holds: the unpadded
// base64url SHA-256 of the method, a space, the request target, a newline,
// and the body.
func requestDigest(method, target string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method + " " + target + "\n"))
	h.Write(body)
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
