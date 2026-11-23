// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package masking

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// MaskJSON masks secrets in document, a JSON text, with mask. Plain
// replacement can split a JSON token, such as a secret matching a number;
// such a document is masked value by value instead, so it stays JSON, and a
// secret spanning values, such as a whole object, masks the smallest value
// holding it. It never keeps secret text that plain replacement masks: when
// re-encoding would, it returns the plainly masked text, which may not be
// JSON.
func MaskJSON(document string, mask func(string) string) string {
	masked := mask(document)
	if masked == document || json.Valid([]byte(masked)) || !json.Valid([]byte(document)) {
		return masked
	}
	decoder := json.NewDecoder(strings.NewReader(document))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return masked
	}
	reencoded, err := encodeJSON(maskJSONValue(mask, decoded))
	if err != nil || mask(reencoded) != reencoded {
		return masked
	}
	return reencoded
}

// encodeJSON encodes value as MaskJSON writes documents.
func encodeJSON(value any) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// maskJSONValue masks secrets in decoded JSON. A number, boolean, or null
// holding a secret becomes the masked string, and so does an array or object
// whose encoding still holds one once its values are masked.
func maskJSONValue(mask func(string) string, value any) any {
	switch typed := value.(type) {
	case string:
		if masked := mask(typed); masked != typed {
			return masked
		}
		// A secret can be the string with its quotes.
		encoded, err := encodeJSON(typed)
		if err != nil {
			return typed
		}
		return maskJSONLiteral(mask, encoded, typed)
	case json.Number:
		return maskJSONLiteral(mask, typed.String(), typed)
	case bool:
		return maskJSONLiteral(mask, strconv.FormatBool(typed), typed)
	case nil:
		return maskJSONLiteral(mask, "null", nil)
	case []any:
		masked := make([]any, len(typed))
		for i, item := range typed {
			masked[i] = maskJSONValue(mask, item)
		}
		return maskJSONComposite(mask, masked)
	case map[string]any:
		masked := make(map[string]any, len(typed))
		for key, item := range typed {
			masked[mask(key)] = maskJSONValue(mask, item)
		}
		return maskJSONComposite(mask, masked)
	default:
		return value
	}
}

// maskJSONComposite returns the masked text of an array or object whose
// encoding holds a secret spanning its values, and value otherwise.
func maskJSONComposite(mask func(string) string, value any) any {
	encoded, err := encodeJSON(value)
	if err != nil {
		return value
	}
	if masked := mask(encoded); masked != encoded {
		return masked
	}
	return value
}

// maskJSONLiteral returns the masked text of a JSON literal holding a secret,
// and value otherwise.
func maskJSONLiteral(mask func(string) string, literal string, value any) any {
	if masked := mask(literal); masked != literal {
		return masked
	}
	return value
}
