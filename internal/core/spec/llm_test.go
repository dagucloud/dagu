// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildLLM_Validation(t *testing.T) {
	ctx := BuildContext{
		opts: BuildOpts{},
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
