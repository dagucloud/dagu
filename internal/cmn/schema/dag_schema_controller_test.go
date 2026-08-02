// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDAGSchemaControllerContextLimits(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	doc := mustParseYAMLDocument(t, `
type: controller
llm:
  provider: anthropic
  model: claude-opus-5
  max_context_tokens: 0
  observation_max_bytes: 0
  observation_keep_recent: 0
steps:
  - name: run_tests
    run: make test
tasks:
  - name: tests_green
    description: Tests pass.
`)
	require.NoError(t, resolved.Validate(doc))
}

func TestDAGSchemaControllerContextLimitsAreRootOnly(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	for name, source := range map[string]string{
		"non_controller_root": `
llm:
  provider: anthropic
  model: claude-opus-5
  max_context_tokens: 100000
steps:
  - name: run
    run: echo ok
`,
		"step": `
steps:
  - name: chat
    type: chat
    llm:
      provider: anthropic
      model: claude-opus-5
      observation_max_bytes: 1024
`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			doc := mustParseYAMLDocument(t, source)
			require.Error(t, resolved.Validate(doc))
		})
	}
}
