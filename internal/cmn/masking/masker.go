// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package masking

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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

// providerTokenPatterns match the formats of provider OAuth tokens, which
// a step can print without Dagu knowing the value: Google access tokens and
// Google refresh tokens. Each starts with a literal, so a scan skips to it.
// A match counts only where a token can start; see tokenStart.
var providerTokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`ya29\.[0-9A-Za-z._-]{20,}`),
	regexp.MustCompile(`1//[0-9A-Za-z_-]{40,}`),
}

// Masker provides masking functionality for sensitive data. It is safe for
// concurrent use.
type Masker struct {
	// values finds the values to mask; nil when there are none. Add
	// replaces it, so a reader loads it once per text.
	values atomic.Pointer[valueTrie]
	// patterns match further text to mask, such as token formats.
	patterns []*regexp.Regexp

	// mu serializes Add.
	mu sync.Mutex
	// secrets holds the values given so far, from which Add rebuilds values.
	secrets []string
}

// span is a range of text to mask, from start up to end.
type span struct {
	start, end int
	// value reports that the span is a whole JSON value, so its mask is
	// written as a JSON string.
	value bool
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
// does for the values of environment variables, and every match of
// patterns. Empty values are ignored.
func NewValueMasker(values []string, patterns ...*regexp.Regexp) *Masker {
	m := &Masker{patterns: patterns, secrets: slices.Clone(values)}
	m.values.Store(newValueTrie(maskForms(m.secrets)))
	return m
}

// Add makes m also mask each of values, as NewValueMasker does, in every
// text masked after Add returns. Writers that already wrap m, such as a
// MaskingWriter, mask them in the lines they write from then on. Empty
// values are ignored.
func (m *Masker) Add(values ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	added := false
	for _, val := range values {
		if val != "" && !slices.Contains(m.secrets, val) {
			m.secrets = append(m.secrets, val)
			added = true
		}
	}
	if added {
		m.values.Store(newValueTrie(maskForms(m.secrets)))
	}
}

// maskForms returns the distinct forms of values to mask: each as written
// and as it appears inside a JSON string. Empty values are skipped.
func maskForms(values []string) []string {
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
	return forms
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

// MaskString replaces each value and pattern match in input with the mask.
// Matches that overlap are masked as one span, so no match leaves part of
// another visible.
func (m *Masker) MaskString(input string) string {
	return m.mask(input, false)
}

// mask masks input as MaskString does, and also masks provider token
// formats when tokens is set.
func (m *Masker) mask(input string, tokens bool) string {
	return writeSpans(input, m.spans(input, tokens))
}

// spans returns the ranges of text that values or patterns match, sorted,
// with overlapping ranges joined. With tokens set, the ranges include the
// provider tokens in text.
func (m *Masker) spans(text string, tokens bool) []span {
	if m == nil {
		return nil
	}
	var spans []span
	if values := m.values.Load(); values != nil {
		for i := range len(text) {
			if !values.starts(text[i]) {
				continue
			}
			if n := values.longestPrefix(text[i:]); n > 0 {
				spans = append(spans, span{start: i, end: i + n})
			}
		}
	}
	for _, pattern := range m.patterns {
		for _, match := range pattern.FindAllStringIndex(text, -1) {
			spans = append(spans, span{start: match[0], end: match[1]})
		}
	}
	if tokens {
		for _, pattern := range providerTokenPatterns {
			for _, match := range pattern.FindAllStringIndex(text, -1) {
				if tokenStart(text, match[0]) {
					spans = append(spans, span{start: match[0], end: match[1]})
				}
			}
		}
	}
	return joinSpans(spans)
}

// tokenStart reports whether a provider token can start at offset start of
// text. A match right after a base64 or token character lies inside other
// text, such as base64 data.
func tokenStart(text string, start int) bool {
	if start == 0 {
		return true
	}
	switch b := text[start-1]; {
	case 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z', '0' <= b && b <= '9':
		return false
	case b == '+', b == '/', b == '_', b == '-':
		return false
	default:
		return true
	}
}

// joinSpans sorts spans and joins the ones that overlap. Spans that only
// touch stay apart, each with its own mask.
func joinSpans(spans []span) []span {
	slices.SortFunc(spans, func(a, b span) int { return cmp.Compare(a.start, b.start) })
	joined := spans[:0]
	for _, s := range spans {
		if last := len(joined) - 1; last >= 0 && s.start < joined[last].end {
			joined[last].end = max(joined[last].end, s.end)
			joined[last].value = joined[last].value || s.value
			continue
		}
		joined = append(joined, s)
	}
	return joined
}

// writeSpans returns text with each of spans, which are sorted and apart,
// replaced by the mask.
func writeSpans(text string, spans []span) string {
	if len(spans) == 0 {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	last := 0
	for _, s := range spans {
		out.WriteString(text[last:s.start])
		if s.value {
			out.WriteString(`"` + DefaultMaskString + `"`)
		} else {
			out.WriteString(DefaultMaskString)
		}
		last = s.end
	}
	out.WriteString(text[last:])
	return out.String()
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
