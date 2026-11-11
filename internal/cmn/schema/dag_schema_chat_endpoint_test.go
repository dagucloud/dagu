// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 094, "chat.completion": the block endpoint of with, and the key endpoint of
// each entry of a model list.
func TestDAGSchemaChatEndpoint(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	for name, with := range map[string]string{
		"endpoint block": `
      endpoint:
        name: cle1
        model: claude-haiku-4-5
      prompt: hi`,
		"endpoint block with an effort": `
      endpoint:
        name: cle1
        model: ${MODEL}
        effort: high
      prompt: hi`,
		"list with an endpoint and an old entry": `
      model:
        - name: claude-sonnet-4-6
          endpoint: cle1
        - name: gpt-4o
          provider: openai
          api_key_name: OPENAI_API_KEY
      prompt: hi`,
		"old method with thinking": `
      provider: openai
      model: gpt-4o
      thinking: { enabled: true, effort: high }
      prompt: hi`,
		"endpoint of an entry": `
      model:
        - name: claude-sonnet-4-6
          endpoint: cle1
        - name: gpt-4o
          endpoint: cle2
      prompt: hi`,
		"old method": `
      provider: openai
      api_key_name: OPENAI_API_KEY
      base_url: https://example.com/v1
      model: gpt-4o
      prompt: hi`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			doc := mustParseYAMLDocument(t, "steps:\n  - id: ask\n    action: chat.completion\n    with:"+with+"\n")
			require.NoError(t, resolved.Validate(doc))
		})
	}
}

func TestDAGSchemaChatEndpointRejects(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	for name, with := range map[string]string{
		"endpoint and api_key_name": `
      endpoint: { name: cle1, model: m }
      api_key_name: KEY
      prompt: hi`,
		"endpoint and base_url": `
      endpoint: { name: cle1, model: m }
      base_url: https://example.com
      prompt: hi`,
		"endpoint and provider": `
      endpoint: { name: cle1, model: m }
      provider: openai
      prompt: hi`,
		"endpoint and a flat model": `
      endpoint: { name: cle1, model: m }
      model: m
      prompt: hi`,
		"endpoint and thinking": `
      endpoint: { name: cle1, model: m }
      thinking: { enabled: true }
      prompt: hi`,
		"endpoint as a string": `
      endpoint: cle1
      model: m
      prompt: hi`,
		"endpoint with no model": `
      endpoint: { name: cle1 }
      prompt: hi`,
		"endpoint with no name": `
      endpoint: { model: m }
      prompt: hi`,
		"endpoint with an unknown key": `
      endpoint: { name: cle1, model: m, temperature: 1 }
      prompt: hi`,
		"name not a name": `
      endpoint: { name: Cle1, model: m }
      prompt: hi`,
		"name a reference": `
      endpoint: { name: '${ENDPOINT}', model: m }
      prompt: hi`,
		"effort unknown": `
      endpoint: { name: cle1, model: m, effort: huge }
      prompt: hi`,
		"effort a reference": `
      endpoint: { name: cle1, model: m, effort: '${EFFORT}' }
      prompt: hi`,
		"entry with endpoint and provider": `
      model:
        - name: m
          endpoint: cle1
          provider: openai
      prompt: hi`,
		"entry with endpoint and base_url": `
      model:
        - name: m
          endpoint: cle1
          base_url: https://example.com
      prompt: hi`,
		"entry with endpoint and api_key_name": `
      model:
        - name: m
          endpoint: cle1
          api_key_name: KEY
      prompt: hi`,
		"entry with a bad endpoint": `
      model:
        - name: m
          endpoint: a/b
      prompt: hi`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			doc := mustParseYAMLDocument(t, "steps:\n  - id: ask\n    action: chat.completion\n    with:"+with+"\n")
			require.Error(t, resolved.Validate(doc))
		})
	}
}
