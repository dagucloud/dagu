// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNullableSchema(t *testing.T) {
	t.Parallel()

	schema := map[string]any{"type": "object", "required": []any{"rows"}, "properties": map[string]any{
		"rows": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
			"name":   map[string]any{"type": "string"},
			"status": map[string]any{"type": "string", "enum": []any{"open", "closed"}},
			"price":  map[string]any{"type": []any{"number", "null"}},
		}}},
	}}
	data, err := nullableSchema(schema)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type": "object", "required": ["rows"], "properties": {
		"rows": {"type": ["array", "null"], "items": {"type": ["object", "null"], "properties": {
			"name": {"type": ["string", "null"]},
			"status": {"type": ["string", "null"], "enum": ["open", "closed", null]},
			"price": {"type": ["number", "null"]}
		}}}
	}}`, string(data))
	assert.Equal(t, "array", schema["properties"].(map[string]any)["rows"].(map[string]any)["type"], "the step's schema is left as it was")
}

func TestSettleAnswer(t *testing.T) {
	t.Parallel()

	schema := map[string]any{"type": "object", "required": []any{"title", "rows"}, "properties": map[string]any{
		"title": map[string]any{"type": "string"},
		"note":  map[string]any{"type": "string"},
		"rows": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "required": []any{"name"},
			"properties": map[string]any{"name": map[string]any{"type": "string"}, "price": map[string]any{"type": "number"}},
		}},
	}}
	tests := map[string]struct {
		answer string
		found  bool
		want   string
		err    string
	}{
		"nothing": {
			answer: `{"title": null, "note": null, "rows": [{"name": null, "price": null}, {"name": " "}]}`,
			want:   `{"title": null, "rows": []}`,
		},
		"a list answered null is empty": {
			answer: `{"title": "Shop", "rows": null}`,
			found:  true,
			want:   `{"title": "Shop", "rows": []}`,
		},
		"blank items and optional nulls are left out": {
			answer: `{"title": "Shop", "note": null, "rows": [{"name": "Alpha", "price": null}, {"name": null}]}`,
			found:  true,
			want:   `{"title": "Shop", "rows": [{"name": "Alpha"}]}`,
		},
		"a required null": {
			answer: `{"title": null, "rows": [{"name": "Alpha"}, {"name": null, "price": 3}]}`,
			found:  true,
			err:    "the model answered null for rows[1].name (expected a string), title (expected a string), which the extract schema requires",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var values map[string]any
			require.NoError(t, json.Unmarshal([]byte(tt.answer), &values))
			found, err := settleAnswer(schema, values)
			assert.Equal(t, tt.found, found)
			if tt.err != "" {
				require.EqualError(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			got, _ := json.Marshal(values)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestSettleAnswerBoundsTheFieldsItNames(t *testing.T) {
	t.Parallel()

	schema := map[string]any{"type": "object", "properties": map[string]any{
		"rows": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "required": []any{"name", "id"},
			"properties": map[string]any{"name": map[string]any{"type": "string"}, "id": map[string]any{"type": "integer"}},
		}},
	}}
	values := map[string]any{"rows": []any{
		map[string]any{"name": "a", "id": nil}, map[string]any{"name": "b", "id": nil},
		map[string]any{"name": "c", "id": nil}, map[string]any{"name": "d", "id": nil},
		map[string]any{"name": "e", "id": nil}, map[string]any{"name": "f", "id": nil},
		map[string]any{"name": "g", "id": nil},
	}}
	_, err := settleAnswer(schema, values)
	require.ErrorContains(t, err, "rows[4].id (expected an integer) and 2 more, which")
}

func TestDescribeValidationReport(t *testing.T) {
	t.Parallel()

	// The report the issue quotes, as the runtime gives it.
	report := `[
	  {"expected": "string", "code": "invalid_type", "path": ["structuredContent", "statements", 0, "speaker"], "message": "Invalid input: expected string, received null"},
	  {"code": "invalid_value", "values": ["open", "closed"], "path": ["structuredContent", "status"], "message": "Invalid option"},
	  {"code": "unrecognized_keys", "keys": ["extra"], "path": ["structuredContent"], "message": "Unrecognized key"},
	  {"code": "too_small", "path": ["structuredContent", "items"], "message": "Too small: expected array to have >=1 items"}
	]`
	mismatch, ok := describeValidationReport(report)
	require.True(t, ok)
	assert.Equal(t, "the model's answer does not fit the extract schema: "+
		"statements[0].speaker is not a string; "+
		`status is not one of "open", "closed"; `+
		"the answer has fields the schema does not list: extra; "+
		"items: Too small: expected array to have >=1 items", mismatch.Error())

	for _, message := range []string{"Element not visible (no box model)", `[]`, `[{"message": "no code"}]`} {
		_, ok := describeValidationReport(message)
		assert.False(t, ok, message)
	}
}
