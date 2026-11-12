// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDAGSchemaClickHouseQuery(t *testing.T) {
	t.Parallel()
	const source = `
steps:
  - action: clickhouse.query
    with:
      url: http://localhost:8123
      query: SELECT 1
      settings: {readonly: 1, x: "a", y: true}
      timeout: 5
      headers: true
      tls_ca: ca.pem
`
	resolved := mustResolveDAGSchema(t)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, source)))
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, strings.Replace(source, "query: SELECT 1", "file: a.sql", 1))))
	// References are resolved when the step runs.
	for _, ref := range []string{"timeout: 5", "headers: true", "tls_ca: ca.pem"} {
		field, _, _ := strings.Cut(ref, ":")
		require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, strings.Replace(source, ref, field+": ${VALUE}", 1))), field)
	}

	for _, tc := range []struct{ name, from, to string }{
		{"no query", "      query: SELECT 1\n", ""},
		{"query and file", "      timeout: 5\n", "      file: a.sql\n"},
		{"no url", "      url: http://localhost:8123\n", ""},
		{"format", "      timeout: 5\n", "      output_format: xml\n"},
		{"unknown", "      timeout:", "      args:"},
		{"timeout", "timeout: 5", "timeout: -1"},
		{"setting value", "x: \"a\"", "x: {a: 1}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := mustParseYAMLDocument(t, strings.Replace(source, tc.from, tc.to, 1))
			require.Error(t, resolved.Validate(doc))
		})
	}
}
