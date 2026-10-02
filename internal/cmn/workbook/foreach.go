// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"encoding/json"
	"fmt"
	"strings"
)

// A foreach step publishes an aggregate of exactly three fields: a summary
// object with numeric total, succeeded, and failed counts, an items list,
// and an outputs list holding the collect maps of the item bodies that
// succeeded, in item order. Writers accept that aggregate as rows and take
// its outputs list. Nothing else is unwrapped: an object carrying any other
// field, or one of these three in another shape, is an ordinary row.

// rowList turns a writer's rows value into a list of rows. JSON text is
// decoded and returned as well, so the caller can take key order from it;
// a foreach aggregate yields its outputs; a single object or scalar stands
// alone for the caller to judge. shape names the accepted element kinds
// in the error for text that is not JSON.
func rowList(value any, shape string) (list []any, jsonText string, err error) {
	if text, ok := value.(string); ok {
		trimmed := strings.TrimSpace(text)
		if !looksLikeJSON(trimmed) {
			return nil, "", fmt.Errorf("rows must be a JSON array of %s", shape)
		}
		// Taking the outputs text, not the decoded list, keeps the key
		// order of the collected objects.
		if outputs, ok := foreachOutputsJSON(trimmed); ok {
			trimmed = outputs
		}
		decoded, err := decodeJSON(trimmed)
		if err != nil {
			return nil, "", fmt.Errorf("rows: %w", err)
		}
		value, jsonText = decoded, trimmed
	}
	if outputs, ok := foreachOutputs(value); ok {
		value = outputs
	}
	switch x := value.(type) {
	case []any:
		return x, jsonText, nil
	case nil:
		return nil, "", fmt.Errorf("rows must be a list")
	default:
		return []any{x}, jsonText, nil
	}
}

// foreachOutputs returns the outputs list of a decoded foreach aggregate.
func foreachOutputs(value any) ([]any, bool) {
	obj, ok := value.(map[string]any)
	if !ok || len(obj) != 3 {
		return nil, false
	}
	summary, ok := obj["summary"].(map[string]any)
	if !ok || !isForeachSummary(summary) {
		return nil, false
	}
	if _, ok := obj["items"].([]any); !ok {
		return nil, false
	}
	outputs, ok := obj["outputs"].([]any)
	return outputs, ok
}

// foreachOutputsJSON returns the text of the outputs list when text is a
// foreach aggregate. Only an object is decoded, so a rows array, the common
// case, does not pay for the attempt. The envelope is read once as a map
// keyed by the decoded field names, so an escaped spelling of a name still
// counts and a field that differs only in case cannot stand in for outputs.
func foreachOutputsJSON(text string) (string, bool) {
	if !strings.HasPrefix(text, "{") {
		return "", false
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &envelope); err != nil || len(envelope) != 3 {
		return "", false
	}
	var summary map[string]any
	if err := json.Unmarshal(envelope["summary"], &summary); err != nil || !isForeachSummary(summary) {
		return "", false
	}
	items, outputs := envelope["items"], envelope["outputs"]
	if !isJSONList(items) || !isJSONList(outputs) {
		return "", false
	}
	return string(outputs), true
}

// isForeachSummary reports whether summary has the numeric total,
// succeeded, and failed counts of a foreach aggregate.
func isForeachSummary(summary map[string]any) bool {
	for _, field := range []string{"total", "succeeded", "failed"} {
		if _, isNumber := toFloatStrict(summary[field]); !isNumber {
			return false
		}
	}
	return true
}

// isJSONList reports whether a raw JSON value is present and is an array.
func isJSONList(raw json.RawMessage) bool {
	return strings.HasPrefix(strings.TrimSpace(string(raw)), "[")
}
