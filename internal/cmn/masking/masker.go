// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package masking

import (
	"bytes"
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
	sensitiveVals map[string]bool // Set of values to mask
}

// NewMasker creates a masker from sourced environment variables. Each value
// is masked as written and as it appears inside a JSON string, so a secret
// that JSON escaping rewrites is still hidden in JSON text.
func NewMasker(sources SourcedEnvVars) *Masker {
	sensitiveVals := make(map[string]bool)

	for _, env := range sources.Secrets {
		_, val := splitEnv(env)
		// Skip empty or invalid values to avoid masking everything
		// (strings.ReplaceAll with empty string would insert mask between every character)
		if val == "" {
			continue
		}
		for _, form := range jsonForms(val) {
			sensitiveVals[form] = true
		}
	}

	return &Masker{
		sensitiveVals: sensitiveVals,
	}
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
	if len(m.sensitiveVals) == 0 {
		return input // Fast path
	}

	// Sort values by length (longest first) to avoid partial matches
	values := make([]string, 0, len(m.sensitiveVals))
	for val := range m.sensitiveVals {
		values = append(values, val)
	}

	// Simple sort by length (descending)
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if len(values[j]) > len(values[i]) {
				values[i], values[j] = values[j], values[i]
			}
		}
	}

	result := input
	for _, val := range values {
		result = strings.ReplaceAll(result, val, DefaultMaskString)
	}

	return result
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
