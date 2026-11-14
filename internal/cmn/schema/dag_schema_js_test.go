// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDAGSchemaJSRun(t *testing.T) {
	t.Parallel()
	const source = `
steps:
  - action: js.run
    with:
      script: return input.urls.length
      input: {urls: [a, b]}
      timeout: 5s
`
	resolved := mustResolveDAGSchema(t)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, source)))
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, strings.Replace(source,
		"      input: {urls: [a, b]}\n", "      input_file: page.html\n      format: json\n", 1))))
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, strings.Replace(source, "timeout: 5s", "timeout: 30", 1))))

	for _, tc := range []struct{ name, from, to string }{
		{"script", "      script: return input.urls.length\n", ""},
		{"empty script", "return input.urls.length", "''"},
		{"both inputs", "      timeout: 5s\n", "      input_file: page.html\n"},
		{"format", "      timeout: 5s\n", "      format: xml\n"},
		{"timeout", "timeout: 5s", "timeout: 0"},
		{"unknown", "      timeout:", "      args:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := mustParseYAMLDocument(t, strings.Replace(source, tc.from, tc.to, 1))
			require.Error(t, resolved.Validate(doc))
		})
	}
}
