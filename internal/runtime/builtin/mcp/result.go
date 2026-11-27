// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Error kinds reported in the step output and error message.
const (
	kindTool      = "tool"
	kindProtocol  = "protocol"
	kindTransport = "transport"
	kindAuth      = "auth"
	kindTimeout   = "timeout"
	kindCanceled  = "canceled"
	// kindRateLimited is HTTP 429.
	kindRateLimited = "rate_limited"
	// kindConfirmationRequired is a confirmation request from the server
	// of a call through a connection, which mcp.call declines.
	kindConfirmationRequired = "confirmation_required"
)

type output struct {
	Content           []any `json:"content"`
	StructuredContent any   `json:"structured_content"`
	JSON              any   `json:"json"`
	IsError           bool  `json:"is_error"`
	Raw               any   `json:"raw,omitempty"`
}

type errorOutput struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	// Connection names the connection of an auth or confirmation_required
	// error.
	Connection string `json:"connection,omitempty"`
	// Detail is reconnect_required or insufficient_scope for an auth
	// error, and Scope the missing scope of insufficient_scope.
	Detail string `json:"detail,omitempty"`
	Scope  string `json:"scope,omitempty"`
	// RetryAfter is the Retry-After of a rate_limited error, as sent.
	RetryAfter string `json:"retry_after,omitempty"`
}

type callError struct {
	kind       string
	message    string
	connection string
	detail     string
	scope      string
	retryAfter string
}

func (e *callError) Error() string {
	return "mcp.call: " + e.kind + " error: " + e.message
}

func (e *callError) body() errorBody {
	return errorBody{
		Kind:       e.kind,
		Message:    e.message,
		Connection: e.connection,
		Detail:     e.detail,
		Scope:      e.scope,
		RetryAfter: e.retryAfter,
	}
}

// newOutput builds the step output. A non-nil files store receives the data
// of binary content items, which then carry the saved file's path.
func newOutput(res *sdk.CallToolResult, includeRaw bool, files *agentstep.ArtifactStore) (output, error) {
	out := output{
		Content:           make([]any, 0, len(res.Content)),
		StructuredContent: res.StructuredContent,
		JSON:              textJSON(res),
		IsError:           res.IsError,
	}
	var writer *binaryWriter
	if files != nil {
		writer = &binaryWriter{files: files}
	}
	for i, content := range res.Content {
		item := any(content)
		if writer != nil {
			saved, err := writer.save(i, content)
			if err != nil {
				return output{}, fmt.Errorf("mcp.call: saving binary content: %w", err)
			}
			item = saved
		}
		out.Content = append(out.Content, item)
	}
	if includeRaw {
		out.Raw = res
	}
	return out, nil
}

// textJSON returns the JSON object or array a tool returned as its only text
// item, for tools that send JSON as text instead of structured content. It is
// nil when the tool sent structured content, which already holds the result.
func textJSON(res *sdk.CallToolResult) any {
	if res.StructuredContent != nil {
		return nil
	}
	var texts []string
	for _, content := range res.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	if len(texts) != 1 {
		return nil
	}
	switch v := decodeJSON(texts[0]).(type) {
	case map[string]any, []any:
		return v
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// redactor masks the values of credential headers and query parameters at any
// length, query values both decoded and as written, plus the credential part
// of scheme-prefixed header values such as "Bearer <token>". Other values are
// left alone so ordinary values never rewrite tool output. Secrets are applied
// longest first so a short secret cannot split a longer one.
type redactor []string

// credentialNameWords mark a header or query parameter name as carrying a
// credential.
var credentialNameWords = []string{"auth", "cookie", "key", "token", "secret", "password"}

func isCredentialName(name string) bool {
	name = strings.ToLower(name)
	return slices.ContainsFunc(credentialNameWords, func(w string) bool {
		return strings.Contains(name, w)
	})
}

func newRedactor(headers map[string]string, rawURL string) redactor {
	var r redactor
	for k, v := range headers {
		if v == "" || !isCredentialName(k) {
			continue
		}
		r = append(r, v)
		if _, rest, ok := strings.Cut(v, " "); ok && rest != "" {
			r = append(r, rest)
		}
	}
	if u, err := url.Parse(rawURL); err == nil {
		for pair := range strings.SplitSeq(u.RawQuery, "&") {
			name, raw, _ := strings.Cut(pair, "=")
			if decoded, err := url.QueryUnescape(name); err == nil {
				name = decoded
			}
			if raw == "" || !isCredentialName(name) {
				continue
			}
			r = append(r, raw)
			if decoded, err := url.QueryUnescape(raw); err == nil && decoded != raw {
				r = append(r, decoded)
			}
		}
	}
	return r.with()
}

// with returns r with values added, each a secret masked at any length.
func (r redactor) with(values ...string) redactor {
	out := slices.Clone(r)
	for _, v := range values {
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		return cmp.Or(cmp.Compare(len(b), len(a)), cmp.Compare(a, b))
	})
	return out
}

func (r redactor) apply(s string) string {
	for _, secret := range r {
		s = strings.ReplaceAll(s, secret, "***")
	}
	return s
}

// walk masks secrets in decoded string values only, so JSON syntax, numbers,
// booleans and keys are never altered.
func (r redactor) walk(v any) any {
	switch t := v.(type) {
	case string:
		return r.apply(t)
	case []any:
		for i := range t {
			t[i] = r.walk(t[i])
		}
	case map[string]any:
		for k := range t {
			t[k] = r.walk(t[k])
		}
	}
	return v
}

func writeRedacted(w io.Writer, v any, r redactor) error {
	var buf bytes.Buffer
	if err := writeJSON(&buf, v); err != nil {
		return err
	}
	dec := json.NewDecoder(&buf)
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return err
	}
	return writeJSON(w, r.walk(tree))
}
