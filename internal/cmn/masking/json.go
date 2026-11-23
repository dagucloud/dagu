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
// such a document is masked value by value instead, so it stays JSON. It
// never keeps secret text that plain replacement masks, so a secret that
// spans JSON values leaves the plainly masked text, which may not be JSON.
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
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(maskJSONValue(mask, decoded)); err != nil {
		return masked
	}
	reencoded := strings.TrimSuffix(buf.String(), "\n")
	// A secret spanning JSON values is in no single decoded value, so
	// re-encoding writes it back out.
	if mask(reencoded) != reencoded {
		return masked
	}
	return reencoded
}

// maskJSONValue masks secrets in decoded JSON. A number, boolean, or null
// holding a secret becomes the masked string.
func maskJSONValue(mask func(string) string, value any) any {
	switch typed := value.(type) {
	case string:
		return mask(typed)
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
		return masked
	case map[string]any:
		masked := make(map[string]any, len(typed))
		for key, item := range typed {
			masked[mask(key)] = maskJSONValue(mask, item)
		}
		return masked
	default:
		return value
	}
}

// maskJSONLiteral returns the masked text of a JSON literal holding a secret,
// and value otherwise.
func maskJSONLiteral(mask func(string) string, literal string, value any) any {
	if masked := mask(literal); masked != literal {
		return masked
	}
	return value
}
