// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDAGSchemaDecision(t *testing.T) {
	t.Parallel()
	const source = `
steps:
  - action: decision.evaluate
    with:
      provider: openrouter
      model: typesafe/jev-1.13
      state: [A refund request, {amount: 42}]
      questions:
        team:
          type: choice
          instructions: {question: "Which team?"}
          criteria: {billing: Charges, other: null}
        urgency:
          type: score
          instructions: How urgent?
          criteria: [Routine, Urgent]
        refund:
          type: noul
          instructions: Refund requested?
          criteria: {"true": Yes, "false": No}
`
	resolved := mustResolveDAGSchema(t)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, source)))
	for _, tc := range []struct{ name, from, to string }{
		{"model", "      model: typesafe/jev-1.13\n", ""},
		{"state", "[A refund request, {amount: 42}]", "42"},
		{"choice", "{billing: Charges, other: null}", "{billing: Charges}"},
		{"score", "[Routine, Urgent]", "[Routine]"},
		{"noul", `{"true": Yes, "false": No}`, `{"true": Yes}`},
		{"unknown", "      model:", "      unsupported:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := mustParseYAMLDocument(t, strings.Replace(source, tc.from, tc.to, 1))
			require.Error(t, resolved.Validate(doc))
		})
	}
}
