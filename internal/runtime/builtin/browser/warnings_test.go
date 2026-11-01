// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The browser runtime resolves a link's address only for a string field with
// an address format; any other string field gets the link's element id.
func TestConfigWarnings(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		schema string
		want   []string
	}{
		{
			name:   "link in a list without format",
			schema: `{"type":"object","properties":{"listings":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"link":{"type":"string"}}}}}}`,
			want:   []string{`do[1]: extract field "listings[].link" has no format: uri`},
		},
		{
			name:   "address names",
			schema: `{"type":"object","properties":{"detailURL":{"type":"string"},"permalink":{"type":"string"},"hrefs":{"type":"array","items":{"type":"string"}},"source_uri":{"type":["string","null"]}}}`,
			want: []string{
				`do[1]: extract field "detailURL" has no format: uri`,
				`do[1]: extract field "hrefs[]" has no format: uri`,
				`do[1]: extract field "permalink" has no format: uri`,
				`do[1]: extract field "source_uri" has no format: uri`,
			},
		},
		{
			name:   "address formats",
			schema: `{"type":"object","properties":{"url":{"type":"string","format":"uri"},"links":{"type":"array","items":{"type":"string","format":"uri-reference"}}}}`,
		},
		{
			name:   "fields that are not addresses",
			schema: `{"type":"object","properties":{"link_text":{"type":"string"},"link_count":{"type":"integer"},"url":{"type":"object"}}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var schema map[string]any
			require.NoError(t, json.Unmarshal([]byte(tc.schema), &schema))
			warnings := configWarnings(ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: map[string]any{
				"do": []any{
					map[string]any{"act": "Open the listings"},
					map[string]any{"extract": map[string]any{"instruction": "The listings", "schema": schema}},
				},
			}}})
			require.Len(t, warnings, len(tc.want))
			for i, want := range tc.want {
				assert.Contains(t, warnings[i], want)
			}
		})
	}
}
