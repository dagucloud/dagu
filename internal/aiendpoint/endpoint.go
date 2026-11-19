// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package aiendpoint holds the AI endpoints of Spec 093.
//
// An endpoint is a named entry that holds the address and the key of a model API.
// The key is a managed secret of the secret store. The metadata of the endpoint
// never holds the key. It holds the prefix and the fingerprint of the key.
package aiendpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/secret"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// Protocol is the form of the HTTP API of the provider.
type Protocol string

// The protocols of Spec 093, "Terms".
const (
	ProtocolAnthropic Protocol = "anthropic"
	ProtocolOpenAI    Protocol = "openai"
)

// Auth is the form of the header that the coordinator sets.
type Auth string

// The authentication forms of Spec 093, "Fields".
const (
	AuthXAPIKey        Auth = "x-api-key"
	AuthBearer         Auth = "bearer"
	AuthAnthropicOAuth Auth = "anthropic-oauth"
)

const (
	// Wildcard is the only pattern of an allow list. It must be the only item.
	Wildcard = "*"

	// MaxDescriptionChars is the longest description, in characters.
	MaxDescriptionChars = 200
	// MaxKeyBytes is the longest key, in bytes.
	MaxKeyBytes = 8192

	// KeyRefPrefix starts the ref of the managed secret of an endpoint.
	KeyRefPrefix = secretref.ReservedPrefix + "endpoint/"

	maxPrefixChars   = 6
	fingerprintChars = 12
)

// NamePattern is the rule of the name of an endpoint.
const NamePattern = `^[a-z][a-z0-9-]{0,63}$`

var namePattern = regexp.MustCompile(NamePattern)

// Scope tells where an endpoint lives. The zero value is the global scope.
type Scope struct {
	// Workspace is the workspace of a workspace endpoint. It is empty for the global scope.
	Workspace string
}

// GlobalScope returns the global scope.
func GlobalScope() Scope { return Scope{} }

// WorkspaceScope returns the scope of one workspace.
func WorkspaceScope(name string) Scope { return Scope{Workspace: name} }

// IsGlobal tells if the scope is the global scope.
func (s Scope) IsGlobal() bool { return s.Workspace == "" }

// String returns the form of the scope in Spec 093, "Scope and name": "global" or
// "workspace:<name>".
func (s Scope) String() string {
	if s.IsGlobal() {
		return "global"
	}
	return "workspace:" + s.Workspace
}

// SecretWorkspace returns the workspace of the managed secret of the scope.
func (s Scope) SecretWorkspace() string {
	return secret.NormalizeWorkspace(s.Workspace)
}

// Validate checks the workspace name of a workspace scope. The global scope is
// always valid.
func (s Scope) Validate() error {
	if s.IsGlobal() {
		return nil
	}
	if err := workspace.ValidateName(s.Workspace); err != nil {
		return fmt.Errorf("%w: scope %q: %v", ErrInvalid, s.String(), err)
	}
	return nil
}

// ParseScope reads the form "global" or "workspace:<name>".
func ParseScope(raw string) (Scope, error) {
	if raw == "global" {
		return GlobalScope(), nil
	}
	name, ok := strings.CutPrefix(raw, "workspace:")
	if !ok {
		return Scope{}, fmt.Errorf("%w: scope %q: expected global or workspace:<name>", ErrInvalid, raw)
	}
	if err := workspace.ValidateName(name); err != nil {
		return Scope{}, fmt.Errorf("%w: scope %q: %v", ErrInvalid, raw, err)
	}
	return WorkspaceScope(name), nil
}

// Allow tells which DAGs may use an endpoint.
type Allow struct {
	// Workspaces lists the workspaces of a global endpoint. It is empty for a workspace endpoint.
	Workspaces []string
	// DAGs lists the DAG names.
	DAGs []string
}

// Endpoint is the metadata of an AI endpoint. It never holds the key.
type Endpoint struct {
	Name        string
	Description string
	Preset      string
	Protocol    Protocol
	BaseURL     string
	Auth        Auth
	ModelsURL   string
	Scope       Scope
	Allow       Allow

	// SecretID is the ID of the managed secret that holds the key. It is empty
	// while the endpoint has no key.
	SecretID string
	// KeyPrefix and KeyFingerprint are the display values of the key. See KeyDisplay.
	KeyPrefix      string
	KeyFingerprint string
	KeyUpdatedAt   time.Time

	CreatedBy string
	CreatedAt time.Time
	UpdatedBy string
	UpdatedAt time.Time
}

// Clone returns a deep copy.
func (e *Endpoint) Clone() *Endpoint {
	if e == nil {
		return nil
	}
	clone := *e
	clone.Allow.Workspaces = append([]string(nil), e.Allow.Workspaces...)
	clone.Allow.DAGs = append([]string(nil), e.Allow.DAGs...)
	return &clone
}

// HasKey tells if the endpoint has a key.
func (e *Endpoint) HasKey() bool { return e.SecretID != "" }

// SecretRef returns the ref of the managed secret of the endpoint.
func SecretRef(name string) string { return KeyRefPrefix + name }

// ValidateName checks the name of an endpoint.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%w: name %q: expected a match of %s", ErrInvalid, name, namePattern)
	}
	return nil
}

// KeyDisplay gives the values that the API and the UI show in place of a key.
// The prefix has the first characters of the key. Its length is the smaller of 6
// and 10 percent of the key length, rounded down. A key of fewer than 10
// characters has no prefix. The fingerprint is the first 12 hexadecimal digits of
// the SHA-256 of the key.
func KeyDisplay(key string) (prefix, fingerprint string) {
	runes := []rune(key)
	n := min(maxPrefixChars, len(runes)/10)
	prefix = string(runes[:n])
	sum := sha256.Sum256([]byte(key))
	return prefix, hex.EncodeToString(sum[:])[:fingerprintChars]
}

// ValidateKey checks a key before it is saved. A key is not empty, has no
// control character and has at most 8192 bytes.
func ValidateKey(key string) error {
	switch {
	case key == "":
		return fmt.Errorf("%w: key must not be empty", ErrInvalid)
	case len(key) > MaxKeyBytes:
		return fmt.Errorf("%w: key has %d bytes, above the limit of %d", ErrInvalid, len(key), MaxKeyBytes)
	case !utf8.ValidString(key):
		return fmt.Errorf("%w: key is not valid UTF-8", ErrInvalid)
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: key must not have a control character", ErrInvalid)
		}
	}
	return nil
}

func validateDescription(description string) error {
	if utf8.RuneCountInString(description) > MaxDescriptionChars {
		return fmt.Errorf("%w: description has more than %d characters", ErrInvalid, MaxDescriptionChars)
	}
	return nil
}

// validateURL checks a base URL or a models URL. The scheme is https. The
// scheme http is valid only when allowHTTP is true. The URL has a host, no user
// information, no query and no fragment.
func validateURL(field, raw string, allowHTTP bool) error {
	return validateURLParts(field, raw, allowHTTP, false)
}

// validateURLParts is validateURL with the choice to accept a query. The URL of a
// MCP server entry can have a query.
func validateURLParts(field, raw string, allowHTTP, allowQuery bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return fmt.Errorf("%w: %s %q: expected an absolute URL", ErrInvalid, field, redactURL(raw))
	}
	switch {
	case u.Scheme == "https", u.Scheme == "http" && allowHTTP:
	case u.Scheme == "http":
		return fmt.Errorf("%w: %s %q: the scheme http is valid only for a local model and for custom; use https", ErrInvalid, field, redactURL(raw))
	default:
		return fmt.Errorf("%w: %s %q: expected the scheme https", ErrInvalid, field, redactURL(raw))
	}
	if u.User != nil {
		return fmt.Errorf("%w: %s must not have user information", ErrInvalid, field)
	}
	if !allowQuery && (u.RawQuery != "" || u.ForceQuery) {
		return fmt.Errorf("%w: %s %q must not have a query", ErrInvalid, field, redactURL(raw))
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("%w: %s %q must not have a fragment", ErrInvalid, field, redactURL(raw))
	}
	return nil
}

// redactURL gives the form of a URL for an error message. It drops the query
// and the fragment and hides the password, since each can hold a credential.
func redactURL(raw string) string {
	cut := withoutQuery(raw)
	u, err := url.Parse(cut)
	switch {
	case err == nil && u.User != nil:
		return u.Redacted()
	case err == nil && u.Opaque == "":
		return cut
	}
	// The user information of the whole text is hidden first: a password can
	// hold the ? or the # that would cut it before its @.
	return withoutQuery(hideUserInfo(raw))
}

// withoutQuery cuts text at its first ? or #.
func withoutQuery(text string) string {
	if i := strings.IndexAny(text, "?#"); i >= 0 {
		return text[:i]
	}
	return text
}

// hideUserInfo replaces the text before the last @ of a URL with xxxxx, and
// keeps only a scheme in front of it. It works on the text because a URL that
// does not parse can still hold a credential.
func hideUserInfo(text string) string {
	at := strings.LastIndex(text, "@")
	if at < 0 {
		return text
	}
	prefix := ""
	if scheme, _, ok := strings.Cut(text[:at], "://"); ok && isURLScheme(scheme) {
		prefix = scheme + "://"
	}
	return prefix + "xxxxx" + text[at:]
}

// isURLScheme reports whether text has the form of a URL scheme (RFC 3986).
func isURLScheme(text string) bool {
	for i, r := range text {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		other := r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'
		if !letter && (i == 0 || !other) {
			return false
		}
	}
	return text != ""
}

// validateAllow checks an allow block for a scope.
func validateAllow(scope Scope, allow Allow) error {
	if scope.IsGlobal() {
		if len(allow.Workspaces) == 0 {
			return fmt.Errorf("%w: allow.workspaces is required for the scope global", ErrInvalid)
		}
		if err := validateAllowList("allow.workspaces", allow.Workspaces, workspace.ValidateName); err != nil {
			return err
		}
	} else if len(allow.Workspaces) > 0 {
		return fmt.Errorf("%w: allow.workspaces is not valid in the scope %s", ErrInvalid, scope)
	}
	if len(allow.DAGs) == 0 {
		return fmt.Errorf("%w: allow.dags is required", ErrInvalid)
	}
	return validateAllowList("allow.dags", allow.DAGs, func(name string) error {
		if name == "" {
			return ir.ErrNameInvalidChars
		}
		return ir.ValidateDAGName(name)
	})
}

func validateAllowList(field string, items []string, validate func(string) error) error {
	for _, item := range items {
		if item == Wildcard {
			if len(items) != 1 {
				return fmt.Errorf("%w: %s: the value %s must be the only item", ErrInvalid, field, Wildcard)
			}
			return nil
		}
		if err := validate(item); err != nil {
			return fmt.Errorf("%w: %s: item %q: %v", ErrInvalid, field, item, err)
		}
	}
	return nil
}
