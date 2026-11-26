// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDAGSchemaOutputs(t *testing.T) {
	t.Parallel()
	const steps = "steps:\n  - id: a\n    run: echo ok\n"
	resolved := mustResolveDAGSchema(t)
	for _, outputs := range []string{
		"  summary: ${a.outputs.summary}",
		"  summary: ${steps.a.outputs.summary}",
	} {
		require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, "outputs:\n"+outputs+"\n"+steps)), outputs)
	}
	for _, outputs := range []string{
		"  1bad: ${a.outputs.summary}",
		"  summary: literal",
		"  summary: 'x ${a.outputs.summary}'",
		"  summary: ${a.outputs.s.t}",
	} {
		require.Error(t, resolved.Validate(mustParseYAMLDocument(t, "outputs:\n"+outputs+"\n"+steps)), outputs)
	}
}
