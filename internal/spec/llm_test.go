// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildLLM_Validation(t *testing.T) {
	ctx := buildContext{
		opts: buildOpts{},
	}

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "valid single model string",
			yaml: `
provider: openai
model: gpt-4
`,
			wantErr: "",
		},
		{
			name: "valid model array",
			yaml: `
model:
  - provider: openai
    name: gpt-4
  - provider: anthropic
    name: claude-sonnet-4-6
`,
			wantErr: "",
		},
		{
			name: "nil model is valid at DAG level",
			yaml: `
provider: openai
`,
			wantErr: "",
		},
		{
			name:    "empty model array",
			yaml:    "model: []",
			wantErr: "at least one entry",
		},
		{
			name:    "unsupported model type",
			yaml:    "model: 123",
			wantErr: "must be string or array",
		},
		{
			name: "invalid temperature in model entry",
			yaml: `
model:
  - provider: openai
    name: gpt-4
    temperature: 2.5
`,
			wantErr: "temperature: must be between 0.0 and 2.0",
		},
		{
			name: "int temperature validation",
			yaml: `
model:
  - provider: openai
    name: gpt-4
    temperature: 3
`,
			wantErr: "temperature: must be between 0.0 and 2.0",
		},
		{
			name: "invalid top_p in model entry",
			yaml: `
model:
  - provider: openai
    name: gpt-4
    top_p: 1.5
`,
			wantErr: "top_p: must be between 0.0 and 1.0",
		},
		{
			name: "invalid max_tokens in model entry",
			yaml: `
model:
  - provider: openai
    name: gpt-4
    max_tokens: 0
`,
			wantErr: "max_tokens: must be at least 1",
		},
		{
			name: "missing provider in model entry",
			yaml: `
model:
  - name: gpt-4
`,
			wantErr: "provider: required",
		},
		{
			name: "missing name in model entry",
			yaml: `
model:
  - provider: openai
`,
			wantErr: "name: required",
		},
		{
			name: "invalid provider in model entry",
			yaml: `
model:
  - provider: invalid_provider
    name: gpt-4
`,
			wantErr: "invalid provider",
		},
		{
			name: "provider reference is resolved at run time",
			yaml: `
provider: ${params.PROVIDER}
model: ${params.MODEL}
`,
			wantErr: "",
		},
		{
			name: "provider reference in model entry is resolved at run time",
			yaml: `
model:
  - provider: ${env.LLM_PROVIDER}
    name: ${params.MODEL}
`,
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg llmConfig
			err := yaml.Unmarshal([]byte(tt.yaml), &cfg)
			if err != nil {
				// Validation error during unmarshaling
				if tt.wantErr != "" {
					assert.Contains(t, err.Error(), tt.wantErr)
				} else {
					t.Fatalf("unexpected unmarshal error: %v", err)
				}
				return
			}

			_, err = buildLLM(ctx, &dag{LLM: &cfg})
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestChatFiles(t *testing.T) {
	t.Parallel()

	t.Run("one or several", func(t *testing.T) {
		t.Parallel()
		dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: one
    action: chat.completion
    with:
      provider: openai
      model: gpt-4o
      prompt: Read the invoice.
      files: invoices/*.pdf
  - id: several
    action: chat.completion
    with:
      provider: openai
      model: gpt-4o
      prompt: Read them.
      files:
        - receipt.png
        - ${steps.find.outputs.messages}
`))
		require.NoError(t, err)
		assert.Equal(t, []string{"invoices/*.pdf"}, dag.Steps[0].LLM.Files)
		assert.Equal(t, []string{"receipt.png", "${steps.find.outputs.messages}"}, dag.Steps[1].LLM.Files)
	})

	t.Run("not at the DAG level", func(t *testing.T) {
		t.Parallel()
		_, err := LoadYAML(context.Background(), []byte(`
llm:
  provider: openai
  model: gpt-4o
  files: invoice.pdf
steps:
  - id: ask
    action: chat.completion
    with:
      prompt: Read it.
`))
		require.ErrorContains(t, err, "files is not valid in a DAG-level llm block; set it on a chat.completion step, under with")
	})
}

func TestProviderOrder(t *testing.T) {
	t.Parallel()

	t.Run("accepted for openrouter", func(t *testing.T) {
		t.Parallel()
		dag, err := LoadYAML(context.Background(), []byte(`
llm:
  provider: openrouter
  model: openai/gpt-6.1-terra
  provider_order: [azure/eu]
steps:
  - id: inherit
    action: chat.completion
    with:
      prompt: Hello.
  - id: own
    action: chat.completion
    with:
      provider: openrouter
      model: openai/gpt-6.1-terra
      provider_order: [azure/eu, openai]
      prompt: Hello.
  - id: entries
    action: chat.completion
    with:
      model:
        - provider: openrouter
          name: openai/gpt-6.1-terra
          provider_order: [azure/eu]
        - provider: anthropic
          name: claude-sonnet-4-6
      prompt: Hello.
  - id: reference
    action: chat.completion
    with:
      provider: ${env.LLM_PROVIDER}
      model: openai/gpt-6.1-terra
      provider_order: [azure/eu]
      prompt: Hello.
`))
		require.NoError(t, err)
		assert.Equal(t, []string{"azure/eu"}, dag.LLM.ProviderOrder)
		assert.Equal(t, []string{"azure/eu"}, dag.Steps[0].LLM.ProviderOrder)
		assert.Equal(t, []string{"azure/eu", "openai"}, dag.Steps[1].LLM.ProviderOrder)
		assert.Equal(t, []string{"azure/eu"}, dag.Steps[2].LLM.Models[0].ProviderOrder)
		assert.Empty(t, dag.Steps[2].LLM.Models[1].ProviderOrder)
		assert.Equal(t, []string{"azure/eu"}, dag.Steps[3].LLM.ProviderOrder)
	})

	for _, tc := range []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "DAG-level other provider",
			yaml: `
llm:
  provider: openai
  model: gpt-5.4
  provider_order: [azure/eu]
steps:
  - action: chat.completion
    with:
      prompt: Hello.
`,
			wantErr: `field 'llm.provider_order': provider_order is only supported by the openrouter provider, not "openai"`,
		},
		{
			name: "step other provider",
			yaml: `
steps:
  - action: chat.completion
    with:
      provider: anthropic
      model: claude-sonnet-4-6
      provider_order: [azure/eu]
      prompt: Hello.
`,
			wantErr: `field 'llm.provider_order': provider_order is only supported by the openrouter provider, not "anthropic"`,
		},
		{
			name: "shared order reaching another provider's entry",
			yaml: `
steps:
  - action: computer.run
    with:
      do:
        - act: Open the settings.
      llm:
        provider_order: [azure/eu]
        model:
          - provider: openrouter
            name: openai/gpt-6.1-terra
          - provider: anthropic
            name: claude-sonnet-4-6
`,
			wantErr: `field 'llm.provider_order': provider_order is only supported by the openrouter provider, not "anthropic"`,
		},
		{
			name: "entry of another provider",
			yaml: `
steps:
  - action: chat.completion
    with:
      model:
        - provider: openrouter
          name: openai/gpt-6.1-terra
        - provider: gemini
          name: gemini-3.8-flash
          provider_order: [azure/eu]
      prompt: Hello.
`,
			wantErr: `field 'llm.model[1].provider_order': provider_order is only supported by the openrouter provider, not "gemini"`,
		},
		{
			name: "missing provider",
			yaml: `
steps:
  - action: chat.completion
    with:
      model: openai/gpt-6.1-terra
      provider_order: [azure/eu]
      prompt: Hello.
`,
			wantErr: "provider is required",
		},
		{
			name: "entry order not a list",
			yaml: `
steps:
  - action: chat.completion
    with:
      model:
        - provider: openrouter
          name: openai/gpt-6.1-terra
          provider_order: azure/eu
      prompt: Hello.
`,
			wantErr: "model[0].provider_order: must be a list of strings",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadYAML(context.Background(), []byte(tc.yaml))
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}
