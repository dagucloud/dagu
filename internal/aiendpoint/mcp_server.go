// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

// This file has the MCP server entries of Spec 095, "MCP servers with
// credentials". An entry is a named URL with credentials. Each credential value
// is a managed secret. The metadata of the entry never holds a value. It holds
// the prefix and the fingerprint of each value.

const (
	// MCPSecretRefPrefix starts the ref of the managed secret of a credential.
	MCPSecretRefPrefix = secretref.ReservedPrefix + "mcp/"
	// MCPTokenHeader is the header that a token gives. The ref of the secret of a
	// token ends with it. An entry has a token or a header with this name, never
	// both, so the ref is unique.
	MCPTokenHeader = "authorization"
	// MaxMCPHeaders is the largest number of headers of one entry.
	MaxMCPHeaders = 16
	// MaxMCPValueBytes is the longest value of a credential, in bytes.
	MaxMCPValueBytes = 8192
	// ReservedMCPName is the name of the tool bridge. An entry cannot use it.
	ReservedMCPName = "dagu"
)

// MCPNamePattern is the rule of the name of a MCP server entry.
const MCPNamePattern = `^[a-z][a-z0-9-]{0,63}$`

var (
	mcpNamePattern   = regexp.MustCompile(MCPNamePattern)
	mcpHeaderPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)

	// reservedMCPHeaders are the headers that the coordinator sets from the request
	// of the client, and the hop-by-hop headers. An entry cannot set them.
	reservedMCPHeaders = []string{
		"host", "content-length", "content-type", "accept", "accept-encoding", "content-encoding",
		"mcp-session-id", "mcp-protocol-version",
		"connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer",
		"transfer-encoding", "upgrade",
	}
)

// Credential is the metadata of one credential value of a MCP server entry. It
// never holds the value.
type Credential struct {
	// Header is the header name, in lower case. The token of an entry has the name
	// authorization.
	Header string
	// SecretID is the ID of the managed secret that holds the value.
	SecretID string
	// Prefix and Fingerprint are the display values of the value. See KeyDisplay.
	Prefix      string
	Fingerprint string
	UpdatedAt   time.Time
}

// MCPServer is the metadata of a MCP server entry. It never holds a credential
// value.
type MCPServer struct {
	Name  string
	URL   string
	Scope Scope
	Allow Allow
	// Token is the bearer token of the entry, or nil. The coordinator sends it as
	// authorization: Bearer.
	Token *Credential
	// Headers are the headers that carry a credential, sorted by name.
	Headers []Credential

	CreatedBy string
	CreatedAt time.Time
	UpdatedBy string
	UpdatedAt time.Time
}

// Clone returns a deep copy.
func (m *MCPServer) Clone() *MCPServer {
	if m == nil {
		return nil
	}
	clone := *m
	clone.Allow.Workspaces = append([]string(nil), m.Allow.Workspaces...)
	clone.Allow.DAGs = append([]string(nil), m.Allow.DAGs...)
	if m.Token != nil {
		token := *m.Token
		clone.Token = &token
	}
	clone.Headers = append([]Credential(nil), m.Headers...)
	return &clone
}

// CheckUse applies the use rule of Spec 093 to the entry. It returns the reason
// mcp_server_forbidden when a condition is false.
func (m *MCPServer) CheckUse(dagName, workspaceName string) error {
	return checkUse("MCP server", ReasonMCPForbidden, m.Name, m.Scope, m.Allow, dagName, workspaceName)
}

// MCPSecretRef returns the ref of the managed secret of a credential. For the
// token, header is MCPTokenHeader.
func MCPSecretRef(name, header string) string {
	return MCPSecretRefPrefix + name + "/" + header
}

// ValidateMCPName checks the name of a MCP server entry.
func ValidateMCPName(name string) error {
	if !mcpNamePattern.MatchString(name) {
		return fmt.Errorf("%w: name %q: expected a match of %s", ErrInvalid, name, mcpNamePattern)
	}
	if name == ReservedMCPName {
		return fmt.Errorf("%w: name %q is the name of the tool bridge; use another name", ErrInvalid, name)
	}
	return nil
}

// validateMCPURL checks the URL of an entry. The scheme is https, or http. The URL
// has a host, no user information and no fragment.
func validateMCPURL(raw string) error {
	return validateURLParts("url", raw, true, true)
}

// normalizeMCPHeaders checks the headers of a credential set and returns the
// names in lower case. It also checks the contradiction of a token with a header
// authorization.
func normalizeMCPHeaders(token string, headers map[string]string) (map[string]string, error) {
	if len(headers) > MaxMCPHeaders {
		return nil, fmt.Errorf("%w: headers has %d items, above the limit of %d", ErrInvalid, len(headers), MaxMCPHeaders)
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		if !mcpHeaderPattern.MatchString(name) {
			return nil, fmt.Errorf("%w: header name %q: expected a match of %s", ErrInvalid, name, mcpHeaderPattern)
		}
		lower := strings.ToLower(name)
		if slices.Contains(reservedMCPHeaders, lower) {
			return nil, fmt.Errorf("%w: header name %q is not valid: the coordinator sets it", ErrInvalid, name)
		}
		if _, dup := out[lower]; dup {
			return nil, fmt.Errorf("%w: header name %q appears twice, with another case", ErrInvalid, name)
		}
		if err := validateCredentialValue("header "+name, value); err != nil {
			return nil, err
		}
		out[lower] = value
	}
	if token != "" {
		if _, clash := out[MCPTokenHeader]; clash {
			return nil, fmt.Errorf("%w: a token and a header %s contradict each other; the token is the header %s", ErrInvalid, MCPTokenHeader, MCPTokenHeader)
		}
	}
	return out, nil
}

// validateCredentialValue checks a token or a header value: it is not empty, has no
// control character and has at most 8192 bytes. The error never holds the value.
func validateCredentialValue(field, value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: %s must not be empty", ErrInvalid, field)
	case len(value) > MaxMCPValueBytes:
		return fmt.Errorf("%w: %s has %d bytes, above the limit of %d", ErrInvalid, field, len(value), MaxMCPValueBytes)
	case !utf8.ValidString(value):
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalid, field)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %s must not have a control character", ErrInvalid, field)
		}
	}
	return nil
}

// validateMCPServer checks the fields of an entry that are not a credential.
func validateMCPServer(m *MCPServer) error {
	if err := ValidateMCPName(m.Name); err != nil {
		return err
	}
	if err := validateMCPURL(m.URL); err != nil {
		return err
	}
	return validateAllow(m.Scope, m.Allow)
}

// MCPCredentials are the values of the credentials of an entry, in clear text. The
// coordinator reads them for one request. No log or error holds them.
type MCPCredentials struct {
	// Token is the bearer token, or empty.
	Token string
	// Headers maps a header name, in lower case, to its value.
	Headers map[string]string
}

// String hides the values, so a format verb or a log line never shows them.
func (c MCPCredentials) String() string { return "[MCP credentials]" }

// LogValue hides the values in a structured log line.
func (c MCPCredentials) LogValue() slog.Value { return slog.StringValue(c.String()) }

// Apply sets the headers of the credentials on h. The token gives the header
// authorization with the scheme Bearer. It removes each credential header of the
// client first, so a client value never goes to the server.
func (c MCPCredentials) Apply(h http.Header) {
	for _, name := range []string{"Authorization", "Proxy-Authorization"} {
		h.Del(name)
	}
	if c.Token != "" {
		h.Set("Authorization", "Bearer "+c.Token)
	}
	for name, value := range c.Headers {
		h.Set(name, value)
	}
}
