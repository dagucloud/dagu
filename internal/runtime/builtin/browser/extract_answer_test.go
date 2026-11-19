// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettleAnswer(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		answer string
		found  bool
		want   string
	}{
		"nothing": {
			answer: `{"title": " ", "rows": [{"name": "", "price": null}, {"name": " "}]}`,
			want:   `{"title": " ", "rows": []}`,
		},
		"an empty list": {
			answer: `{"rows": []}`,
			want:   `{"rows": []}`,
		},
		"blank items are left out": {
			answer: `{"title": "Shop", "rows": [{"name": "Alpha", "price": 3}, {"name": ""}]}`,
			found:  true,
			want:   `{"title": "Shop", "rows": [{"name": "Alpha", "price": 3}]}`,
		},
		"a zero is a value": {
			answer: `{"rows": [{"name": "", "price": 0}]}`,
			found:  true,
			want:   `{"rows": [{"name": "", "price": 0}]}`,
		},
		"plain values keep their place": {
			answer: `{"tags": ["a", "", "b"]}`,
			found:  true,
			want:   `{"tags": ["a", "", "b"]}`,
		},
		"a list of blank values": {
			answer: `{"tags": ["", " "]}`,
			want:   `{"tags": ["", " "]}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var values map[string]any
			require.NoError(t, json.Unmarshal([]byte(tt.answer), &values))
			assert.Equal(t, tt.found, settleAnswer(values))
			got, _ := json.Marshal(values)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
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

	// Problems past the limit are counted, not named.
	issues := make([]string, maxListedProblems+2)
	for i := range issues {
		issues[i] = fmt.Sprintf(`{"code": "invalid_type", "expected": "string", "path": ["structuredContent", "f%d"]}`, i)
	}
	mismatch, ok = describeValidationReport("[" + strings.Join(issues, ",") + "]")
	require.True(t, ok)
	assert.True(t, strings.HasSuffix(mismatch.Error(), "f4 is not a string; and 2 more"), mismatch.Error())

	for _, message := range []string{"Element not visible (no box model)", `[]`, `[{"message": "no code"}]`} {
		_, ok := describeValidationReport(message)
		assert.False(t, ok, message)
	}
}
