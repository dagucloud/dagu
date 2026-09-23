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

func validationError(t *testing.T, withJSON string) error {
	t.Helper()
	var with map[string]any
	require.NoError(t, json.Unmarshal([]byte(withJSON), &with))
	return validateStep(ir.Step{
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: with},
		LLM:            &ir.LLMConfig{Provider: "openai", Model: "m"},
	})
}

func TestValidateStep(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		with string
		want string
	}{
		{
			name: "valid",
			with: `{"url":"https://x.test","variables":{"user":"u"},"do":[{"act":"Sign in"},{"act":{"instruction":"Open","cache":false}},{"wait":{"duration":"2s"}},{"screenshot":"after-login"},{"ask":{"prompt":"Code?","as":"otp","timeout":"10m"}}]}`,
		},
		{
			name: "no operations",
			with: `{"do":[]}`,
			want: "do",
		},
		{
			name: "two operations in one item",
			with: `{"do":[{"act":"a","goto":"https://x.test"}]}`,
			want: "invalid browser config",
		},
		{
			name: "extract schema must be an object",
			with: `{"do":[{"extract":{"instruction":"x","schema":{"type":"array"}}}]}`,
			want: "extract schema must have type: object",
		},
		{
			name: "duplicate extracted output",
			with: `{"do":[{"extract":{"instruction":"a","schema":{"type":"object","properties":{"id":{}}}}},{"extract":{"instruction":"b","schema":{"type":"object","properties":{"id":{}}}}}]}`,
			want: `do[1]: output "id" is already extracted by do[0]`,
		},
		{
			name: "wait needs exactly one of selector or duration",
			with: `{"do":[{"wait":{"selector":"#a","duration":"1s"}}]}`,
			want: "wait must set exactly one of selector or duration",
		},
		{
			name: "invalid duration",
			with: `{"do":[{"act":"a","timeout":"soon"}]}`,
			want: `timeout "soon" must be a positive duration`,
		},
		{
			name: "ask name collides with a variable",
			with: `{"variables":{"otp":"x"},"do":[{"ask":{"prompt":"Code?","as":"otp"}}]}`,
			want: `ask.as "otp" collides with a variable`,
		},
		{
			name: "invalid profile name",
			with: `{"browser":{"profile":"../escape"},"do":[{"act":"a"}]}`,
			want: `profile "../escape" must match`,
		},
		{
			name: "unknown variable reference",
			with: `{"variables":{"user":"u"},"do":[{"act":"Type %user% and %pasword%"}]}`,
			want: `do[0]: act references %pasword%, which is not in with.variables or an earlier ask`,
		},
		{
			name: "reference to a later ask",
			with: `{"do":[{"act":"Type %otp%"},{"ask":{"prompt":"Code?","as":"otp"}}]}`,
			want: `do[0]: act references %otp%`,
		},
		{
			name: "condition with two checks",
			with: `{"do":[{"expect":{"text":"a","url":"b"}}]}`,
			want: "invalid browser config",
		},
		{
			name: "invalid within",
			with: `{"do":[{"act":"a","when":{"text":"b","within":"soon"}}]}`,
			want: `within "soon" must be a positive duration`,
		},
		{
			name: "empty condition",
			with: `{"do":[{"act":"a","when":{}}]}`,
			want: "invalid browser config",
		},
		{
			name: "single-label allowed domain",
			with: `{"browser":{"allowed_domains":["localhost"]},"do":[{"act":"a"}]}`,
			want: `allowed domain "localhost" must have at least two labels`,
		},
		{
			name: "wildcard inside allowed domain",
			with: `{"browser":{"allowed_domains":["shop.*.example.com"]},"do":[{"act":"a"}]}`,
			want: `may use * only as a leading *.`,
		},
		{
			name: "allowed domain with a scheme",
			with: `{"browser":{"allowed_domains":["https://example.com"]},"do":[{"act":"a"}]}`,
			want: `must be a host name without a scheme`,
		},
		{
			name: "unknown screenshot policy",
			with: `{"browser":{"screenshots":"sometimes"},"do":[{"act":"a"}]}`,
			want: "invalid browser config",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validationError(t, tc.with)
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestValidateStepRequiresModel(t *testing.T) {
	t.Parallel()

	err := validateStep(ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: map[string]any{
		"do": []any{map[string]any{"act": "a"}},
	}}})
	assert.ErrorContains(t, err, "browser actions need a model")
}
