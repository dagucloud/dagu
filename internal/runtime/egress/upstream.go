// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package egress

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// UpstreamAuth is the form in which the proxy gives the key to the upstream.
type UpstreamAuth string

const (
	// AuthXAPIKey sends the key in the header x-api-key. It is the default.
	AuthXAPIKey UpstreamAuth = "x-api-key"
	// AuthBearer sends the key in the header authorization, with the prefix Bearer.
	AuthBearer UpstreamAuth = "bearer"
	// AuthAnthropicOAuth sends an OAuth token of a subscription to the Anthropic API:
	// the header authorization with the prefix Bearer, and the beta
	// AnthropicOAuthBeta in the header anthropic-beta. A client with a placeholder
	// API key does not send this beta, and the API refuses the token without it.
	AuthAnthropicOAuth UpstreamAuth = "anthropic-oauth"
)

// AnthropicOAuthBeta is the value of anthropic-beta that the Anthropic API needs
// with an OAuth token.
const AnthropicOAuthBeta = "oauth-2025-04-20"

// ParseUpstreamAuth reads the name of a form.
func ParseUpstreamAuth(text string) (UpstreamAuth, error) {
	switch auth := UpstreamAuth(text); auth {
	case AuthXAPIKey, AuthBearer, AuthAnthropicOAuth:
		return auth, nil
	}
	return "", fmt.Errorf("expected %q, %q or %q, got %q", AuthXAPIKey, AuthBearer, AuthAnthropicOAuth, text)
}

// Digest returns the first 12 hexadecimal digits of the SHA-256 of a value. A log
// line uses it to tell two credentials apart. It does not give the value.
func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

// ValidateUpstreamKey checks that the key is a value that a header can carry. The
// error never holds the key.
func ValidateUpstreamKey(key string) error {
	if key == "" {
		return errors.New("the key is empty")
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] == 0x7f {
			return fmt.Errorf("the key has a control character at byte %d", i)
		}
	}
	return nil
}

// Upstream is the route of a request in origin form: the proxy sends it to one
// URL and adds the key. It holds the key, so each text form of it hides the key.
type Upstream struct {
	target *url.URL
	host   string
	port   int
	auth   UpstreamAuth
	key    string
	// keyDigest is Digest(key), for the log.
	keyDigest string
}

// NewUpstream checks the URL, the form and the key. The scheme of the URL is http
// or https. The URL has no user information, no query and no fragment. The port is
// the explicit port, or 80 for http and 443 for https.
func NewUpstream(rawURL string, auth UpstreamAuth, key string) (*Upstream, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("expected an http or https URL, got %s", describeURL(rawURL))
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("expected the scheme http or https, got %s", describeURL(rawURL))
	}
	if target.User != nil {
		return nil, fmt.Errorf("expected a URL with no user information, got %s", describeURL(rawURL))
	}
	if target.Hostname() == "" || target.Opaque != "" {
		return nil, fmt.Errorf("expected a URL with a host, got %s", describeURL(rawURL))
	}
	if target.RawQuery != "" || target.ForceQuery || target.Fragment != "" || strings.Contains(rawURL, "#") {
		return nil, fmt.Errorf("expected a URL with no query and no fragment, got %s", describeURL(rawURL))
	}
	port := 80
	if target.Scheme == "https" {
		port = 443
	}
	if text := target.Port(); text != "" {
		number, err := strconv.Atoi(text)
		if err != nil || number < 1 || number > 65535 {
			return nil, fmt.Errorf("expected a port from 1 to 65535, got %s", describeURL(rawURL))
		}
		port = number
	}
	if _, err := ParseUpstreamAuth(string(auth)); err != nil {
		return nil, err
	}
	if err := ValidateUpstreamKey(key); err != nil {
		return nil, err
	}
	return &Upstream{
		target:    target,
		host:      strings.ToLower(strings.TrimSuffix(target.Hostname(), ".")),
		port:      port,
		auth:      auth,
		key:       key,
		keyDigest: Digest(key),
	}, nil
}

// describeURL quotes a URL for an error. A URL that holds an @ can hold a
// credential, so the text does not repeat it.
func describeURL(raw string) string {
	if strings.Contains(raw, "@") {
		return "a URL with an @"
	}
	return strconv.Quote(raw)
}

// Host returns the host name or address of the upstream, in lower case.
func (u *Upstream) Host() string { return u.host }

// Port returns the port of the upstream.
func (u *Upstream) Port() int { return u.port }

// String returns the URL and the form. It never holds the key.
func (u Upstream) String() string {
	return fmt.Sprintf("upstream %s (%s)", u.target.Redacted(), u.auth)
}

// GoString is the same as String, so that %#v hides the key too.
func (u Upstream) GoString() string { return u.String() }
