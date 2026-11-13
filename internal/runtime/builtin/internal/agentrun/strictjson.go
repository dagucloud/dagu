// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package agentrun holds the parts of an agent.run step that do not depend on
// the CLI: the strict decoder of the typed result, the check of the result
// against a closed schema, the normalization of the names of tool calls, and
// the records of an attempt.
package agentrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DecodeStrict decodes one JSON value. Unlike encoding/json it fails on an
// object that has the same key two times. It also fails on data after the
// value. Numbers stay json.Number, so an integer keeps all its digits.
//
// The error names the path of the duplicate key.
func DecodeStrict(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder, "$")
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder, path string) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("at %s: an object key must be a string", path)
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate field %q at %s", key, path)
			}
			value, err := decodeValue(decoder, path+"."+key)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		list := []any{}
		for i := 0; decoder.More(); i++ {
			value, err := decodeValue(decoder, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			list = append(list, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return list, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter %v at %s", delim, path)
	}
}

// CutBytes cuts text to at most limit bytes, keeping valid UTF-8, and marks the
// cut with an ellipsis.
func CutBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !isRuneStart(text[cut]) {
		cut--
	}
	return strings.TrimSpace(text[:cut]) + "..."
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
