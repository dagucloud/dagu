// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package a2a

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"net/url"
	"slices"
	"strings"
)

// minPlainHeaderLen is the shortest value of a header without a credential
// name that is still masked. Shorter values such as "1" or "us" would rewrite
// ordinary data, so they are left alone; name a header after a credential
// word to mask it at any length.
const minPlainHeaderLen = 4

// redactor masks the value of every header with at least minPlainHeaderLen
// characters and of every credential-named header or query parameter at any
// length, the credential part of scheme-prefixed header values such as
// "Bearer <token>", and the JSON-escaped form of each. Longest secrets go
// first so a short one cannot split a longer one. Masking is by value:
// object keys are never masked.
type redactor []string

var credentialNameWords = []string{"auth", "cookie", "key", "token", "secret", "password"}

func isCredentialName(name string) bool {
	name = strings.ToLower(name)
	return slices.ContainsFunc(credentialNameWords, func(w string) bool {
		return strings.Contains(name, w)
	})
}

func newRedactor(headers map[string]string, rawURL string) redactor {
	var secrets []string
	for k, v := range headers {
		named := isCredentialName(k)
		if v == "" || (!named && len(v) < minPlainHeaderLen) {
			continue
		}
		secrets = append(secrets, v)
		if _, rest, ok := strings.Cut(v, " "); ok {
			// The scheme separator may be several spaces; the token is what follows.
			if rest = strings.TrimSpace(rest); rest != "" && (named || len(rest) >= minPlainHeaderLen) {
				secrets = append(secrets, rest)
			}
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
			secrets = append(secrets, raw)
			if decoded, err := url.QueryUnescape(raw); err == nil && decoded != raw {
				secrets = append(secrets, decoded)
			}
		}
	}
	for _, s := range slices.Clone(secrets) {
		if b, err := json.Marshal(s); err == nil {
			escaped := string(b[1 : len(b)-1])
			if escaped != s {
				secrets = append(secrets, escaped)
			}
		}
	}
	slices.SortFunc(secrets, func(a, b string) int {
		return cmp.Or(cmp.Compare(len(b), len(a)), cmp.Compare(a, b))
	})
	return slices.Compact(secrets)
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

// marshal encodes v as one JSON line with secrets masked.
func (r redactor) marshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r.walk(tree)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeAll(w io.Writer, b []byte) error {
	_, err := w.Write(b)
	return err
}
