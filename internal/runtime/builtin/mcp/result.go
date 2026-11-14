// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"net/url"
	"slices"
	"strings"

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
)

type output struct {
	Content           []sdk.Content `json:"content"`
	StructuredContent any           `json:"structured_content"`
	IsError           bool          `json:"is_error"`
	Raw               any           `json:"raw,omitempty"`
}

type errorOutput struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type callError struct {
	kind    string
	message string
}

func (e *callError) Error() string {
	return "mcp.call: " + e.kind + " error: " + e.message
}

func newOutput(res *sdk.CallToolResult, includeRaw bool) output {
	out := output{
		Content:           res.Content,
		StructuredContent: res.StructuredContent,
		IsError:           res.IsError,
	}
	if out.Content == nil {
		out.Content = []sdk.Content{}
	}
	if includeRaw {
		out.Raw = res
	}
	return out
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// redactor masks header values, and the credential part of scheme-prefixed
// values such as "Bearer <token>", at any length. URL query values are masked
// only above minQuerySecretLen to avoid hitting ubiquitous short substrings.
// Secrets are applied longest first so a short secret cannot split a longer one.
type redactor []string

const minQuerySecretLen = 4

func newRedactor(headers map[string]string, rawURL string) redactor {
	var r redactor
	for _, v := range headers {
		if v == "" {
			continue
		}
		r = append(r, v)
		if _, rest, ok := strings.Cut(v, " "); ok && rest != "" {
			r = append(r, rest)
		}
	}
	if u, err := url.Parse(rawURL); err == nil {
		for _, vals := range u.Query() {
			for _, v := range vals {
				if len(v) >= minQuerySecretLen {
					r = append(r, v)
				}
			}
		}
	}
	slices.SortFunc(r, func(a, b string) int {
		return cmp.Or(cmp.Compare(len(b), len(a)), cmp.Compare(a, b))
	})
	return r
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
