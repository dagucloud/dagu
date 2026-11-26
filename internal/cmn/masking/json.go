// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package masking

import (
	"encoding/json"
	"slices"
	"strings"
)

// jsonValue is the byte range of one value in a JSON text. Object keys
// count as string values.
type jsonValue struct {
	start, end int
	str        bool
}

// MaskJSON masks document, a JSON text, keeping it JSON. A match inside a
// string is masked in place. Any other match, such as a secret that is a
// number, a quoted string, or a whole object, masks the smallest value that
// holds it, which becomes the masked string. The rest of the document is
// kept as written. A document that is not JSON is masked as plain text.
func (m *Masker) MaskJSON(document string) string {
	spans := m.spans(document, false)
	if len(spans) == 0 || !json.Valid([]byte(document)) {
		return writeSpans(document, spans)
	}
	return writeSpans(document, jsonSpans(document, spans))
}

// jsonSpans widens spans of document, a JSON text, so that masking them
// keeps it JSON: a span inside a string stays as it is, and any other span
// becomes the smallest value that holds it.
func jsonSpans(document string, spans []span) []span {
	values := jsonValues(document)
	widened := make([]span, 0, len(spans))
	// open holds the values that hold the current span's start, outermost
	// first.
	var open []jsonValue
	next := 0
	for _, s := range spans {
		for ; next < len(values) && values[next].start <= s.start; next++ {
			open = append(closeBefore(open, values[next].start), values[next])
		}
		open = closeBefore(open, s.start)
		widened = append(widened, widen(document, open, s))
	}
	return joinSpans(widened)
}

// closeBefore drops from open the values that end at or before offset.
func closeBefore(open []jsonValue, offset int) []jsonValue {
	for len(open) > 0 && open[len(open)-1].end <= offset {
		open = open[:len(open)-1]
	}
	return open
}

// widen returns s when it can be masked inside its string, and otherwise
// the smallest of open that holds it, or the whole document.
func widen(document string, open []jsonValue, s span) span {
	for _, v := range slices.Backward(open) {
		if v.end < s.end {
			continue
		}
		if v.str && insideString(document, v, s) {
			return s
		}
		return span{start: v.start, end: v.end, value: true}
	}
	return span{start: 0, end: len(document), value: true}
}

// insideString reports whether s lies within the content of v, a string,
// with both ends between escape sequences, so masking it keeps v a string.
func insideString(document string, v jsonValue, s span) bool {
	if s.start <= v.start || s.end >= v.end {
		return false
	}
	i := v.start + 1
	for i < s.start {
		i += charLen(document, i)
	}
	if i != s.start {
		return false
	}
	for i < s.end {
		i += charLen(document, i)
	}
	return i == s.end
}

// charLen returns the length of the escape sequence at offset i of a JSON
// string, or 1 for any other byte.
func charLen(document string, i int) int {
	switch {
	case document[i] != '\\':
		return 1
	case document[i+1] == 'u':
		return 6
	default:
		return 2
	}
}

// jsonValues returns the values of document, a valid JSON text, in order
// of their start.
func jsonValues(document string) []jsonValue {
	var values []jsonValue
	// open holds the indexes in values of the arrays and objects not closed.
	var open []int
	for i := 0; i < len(document); i++ {
		switch document[i] {
		case ' ', '\t', '\r', '\n', ',', ':':
		case '{', '[':
			open = append(open, len(values))
			values = append(values, jsonValue{start: i})
		case '}', ']':
			values[open[len(open)-1]].end = i + 1
			open = open[:len(open)-1]
		case '"':
			end := stringEnd(document, i)
			values = append(values, jsonValue{start: i, end: end, str: true})
			i = end - 1
		default: // a number, true, false, or null
			end := len(document)
			if n := strings.IndexAny(document[i:], " \t\r\n,]}"); n >= 0 {
				end = i + n
			}
			values = append(values, jsonValue{start: i, end: end})
			i = end - 1
		}
	}
	return values
}

// stringEnd returns the offset just past the JSON string at start.
func stringEnd(document string, start int) int {
	for i := start + 1; i < len(document); i++ {
		switch document[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(document)
}
