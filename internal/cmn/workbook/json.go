// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// referencePattern matches a ${...} reference that substitution left in a
// value: a field missing from the item, a step that published nothing, or a
// path the parser does not accept. A reference path never holds a quote or
// a comma, so a ${ that never closes does not swallow the JSON after it.
var referencePattern = regexp.MustCompile(`\$\{[^{}",]+\}`)

// isReference reports whether text is one ${...} reference and nothing else.
func isReference(text string) bool {
	return text != "" && referencePattern.FindString(text) == text
}

// unresolvedReference returns the reference JSON decoding of text stopped
// on, so the error names it instead of the '$' the decoder saw.
func unresolvedReference(text string, err error) (string, bool) {
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) || syntaxErr.Offset < 1 || int(syntaxErr.Offset) > len(text) {
		return "", false
	}
	start := int(syntaxErr.Offset) - 1
	loc := referencePattern.FindStringIndex(text[start:])
	if loc == nil || loc[0] != 0 {
		return "", false
	}
	return text[start : start+loc[1]], true
}

// decodeJSON parses one JSON value with numbers as float64.
func decodeJSON(text string) (any, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(text))
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	// More only looks for another element of the current array or object,
	// so trailing text such as "[]]" needs a second decode to be seen. Its
	// syntax error is kept so the offset of the trailing text stays known.
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("invalid JSON: expected one value: %w", err)
		}
		return nil, fmt.Errorf("invalid JSON: expected one value")
	}
	return v, nil
}

// looksLikeJSON reports whether text starts a JSON array or object.
func looksLikeJSON(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{")
}

// jsonKeyOrder returns the keys of the first object in a JSON array of
// objects in the order they appear, plus every key later objects add. Go
// maps lose that order, and a sheet's columns should follow it.
func jsonKeyOrder(text string) []string {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '[' {
		return nil
	}
	var order []string
	seen := map[string]bool{}
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return order
		}
		for _, key := range objectKeys(raw) {
			if !seen[key] {
				seen[key] = true
				order = append(order, key)
			}
		}
	}
	return order
}

func objectKeys(raw json.RawMessage) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return keys
		}
		key, ok := tok.(string)
		if !ok {
			return keys
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return keys
		}
	}
	return keys
}
