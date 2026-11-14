// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
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
// values such as "Bearer <token>", in text.
type redactor []string

func newRedactor(headers map[string]string, rawURL string) redactor {
	var secrets redactor
	for _, v := range headers {
		if v == "" {
			continue
		}
		secrets = append(secrets, v)
		if _, rest, ok := strings.Cut(v, " "); ok && rest != "" {
			secrets = append(secrets, rest)
		}
	}
	if u, err := url.Parse(rawURL); err == nil {
		for _, vals := range u.Query() {
			for _, v := range vals {
				if v != "" {
					secrets = append(secrets, v)
				}
			}
		}
	}
	return secrets
}

func (r redactor) apply(s string) string {
	for _, secret := range r {
		s = strings.ReplaceAll(s, secret, "***")
	}
	return s
}

func writeRedacted(w io.Writer, v any, r redactor) error {
	var buf bytes.Buffer
	if err := writeJSON(&buf, v); err != nil {
		return err
	}
	_, err := io.WriteString(w, r.apply(buf.String()))
	return err
}
