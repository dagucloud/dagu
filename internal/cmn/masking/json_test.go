// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package masking

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMasker_MaskJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		values   []string
		document string
		expected string
	}{
		{
			name:     "inside a string",
			values:   []string{"s3cr3t"},
			document: `{"command":"echo s3cr3t now","s3cr3t":1}`,
			expected: `{"command":"echo ******* now","*******":1}`,
		},
		{
			name:     "a number",
			values:   []string{"1234"},
			document: `{"pin":1234,"items":[1234]}`,
			expected: `{"pin":"*******","items":["*******"]}`,
		},
		{
			name:     "a quoted string",
			values:   []string{`"xyz"`},
			document: `{"response":"xyz","other":42}`,
			expected: `{"response":"*******","other":42}`,
		},
		{
			// The rest of the document keeps its key order and spacing.
			name:     "a whole object",
			values:   []string{`{"b":1,"a":2}`},
			document: `{"x": {"b":1,"a":2}, "y": 3}`,
			expected: `{"x": "*******", "y": 3}`,
		},
		{
			name:     "across two values",
			values:   []string{`b","c`},
			document: `{"list":["ab","cd"],"n":1}`,
			expected: `{"list":"*******","n":1}`,
		},
		{
			name:     "across an escape",
			values:   []string{"u0041"},
			document: `{"k":"\u0041x","n":1}`,
			expected: `{"k":"*******","n":1}`,
		},
		{
			name:     "the whole document",
			values:   []string{"42"},
			document: ` 42 `,
			expected: ` "*******" `,
		},
		{
			name:     "not JSON",
			values:   []string{"1234"},
			document: `pin=1234`,
			expected: `pin=*******`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			masked := NewValueMasker(tt.values).MaskJSON(tt.document)

			assert.Equal(t, tt.expected, masked)
		})
	}
}

// A pattern match inside a secret object does not split the object's
// match, so no value of the secret is left in the document.
func TestMasker_MaskJSONJoinsPatternMatches(t *testing.T) {
	t.Parallel()
	credential := `{"id":"tok_abc","key":"plain-key"}`
	masker := NewValueMasker([]string{credential}, regexp.MustCompile(`tok_[a-z]+`))

	masked := masker.MaskJSON(`{"creds":` + credential + `,"n":1}`)

	assert.Equal(t, `{"creds":"*******","n":1}`, masked)
	assert.True(t, json.Valid([]byte(masked)))
}
