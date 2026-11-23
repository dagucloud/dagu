// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package masking

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	// DefaultMaskString is the default replacement string for masked values
	DefaultMaskString = "*******"
)

// SourcedEnvVars groups environment variables by their source
type SourcedEnvVars struct {
	Secrets []string // Environment variables from secrets
}

// Masker provides masking functionality for sensitive data
type Masker struct {
	// replacer masks every value in one pass; nil when there are none.
	replacer *strings.Replacer
}

// NewMasker creates a masker from sourced environment variables. Each value
// is masked as written and as it appears inside a JSON string, so a secret
// that JSON escaping rewrites is still hidden in JSON text.
func NewMasker(sources SourcedEnvVars) *Masker {
	values := make([]string, 0, len(sources.Secrets))
	for _, env := range sources.Secrets {
		_, val := splitEnv(env)
		values = append(values, val)
	}
	return NewValueMasker(values)
}

// NewValueMasker creates a masker that hides each of values, as NewMasker
// does for the values of environment variables. Empty values are ignored.
func NewValueMasker(values []string) *Masker {
	seen := make(map[string]bool)
	forms := make([]string, 0, len(values))
	for _, val := range values {
		// An empty value would mask between every character.
		if val == "" {
			continue
		}
		for _, form := range jsonForms(val) {
			if !seen[form] {
				seen[form] = true
				forms = append(forms, form)
			}
		}
	}
	if len(forms) == 0 {
		return &Masker{}
	}
	// The replacer prefers earlier values at a position, so a longer value
	// wins over a shorter one it contains.
	slices.SortFunc(forms, func(a, b string) int {
		if c := cmp.Compare(len(b), len(a)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	pairs := make([]string, 0, 2*len(forms))
	for _, form := range forms {
		pairs = append(pairs, form, DefaultMaskString)
	}
	return &Masker{replacer: strings.NewReplacer(pairs...)}
}

// jsonForms returns val and its distinct spellings inside a JSON string:
// escaped with and without HTML characters as \u escapes, each as written
// and as ASCII only.
func jsonForms(val string) []string {
	forms := []string{val}
	for _, escapeHTML := range []bool{false, true} {
		body := jsonStringBody(val, escapeHTML)
		for _, form := range []string{body, asciiOnly(body)} {
			if !slices.Contains(forms, form) {
				forms = append(forms, form)
			}
		}
	}
	return forms
}

// jsonStringBody encodes val as a JSON string and returns the text between
// the quotes.
func jsonStringBody(val string, escapeHTML bool) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(escapeHTML)
	// Encoding a string cannot fail.
	_ = encoder.Encode(val)
	quoted := strings.TrimSuffix(buf.String(), "\n")
	return quoted[1 : len(quoted)-1]
}

// asciiOnly rewrites every non-ASCII rune of an escaped JSON string body as
// \uXXXX escapes, as encoders that emit ASCII only do.
func asciiOnly(body string) string {
	var out strings.Builder
	for _, r := range body {
		switch {
		case r < utf8.RuneSelf:
			out.WriteRune(r)
		case r > 0xFFFF:
			high, low := utf16.EncodeRune(r)
			fmt.Fprintf(&out, `\u%04x\u%04x`, high, low)
		default:
			fmt.Fprintf(&out, `\u%04x`, r)
		}
	}
	return out.String()
}

// MaskString replaces sensitive values in the input string
func (m *Masker) MaskString(input string) string {
	if m == nil || m.replacer == nil {
		return input
	}
	return m.replacer.Replace(input)
}

// MaskBytes replaces sensitive values in the input bytes
func (m *Masker) MaskBytes(input []byte) []byte {
	return []byte(m.MaskString(string(input)))
}

// splitEnv splits an environment variable string of the form "KEY=value" into its key and value.
// If the input does not contain an '=', it returns two empty strings.
func splitEnv(env string) (string, string) {
	key, value, found := strings.Cut(env, "=")
	if !found {
		return "", ""
	}
	return key, value
}
