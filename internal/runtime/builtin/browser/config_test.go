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

func validationError(t *testing.T, withJSON string, llm *ir.LLMConfig) error {
	t.Helper()
	var with map[string]any
	require.NoError(t, json.Unmarshal([]byte(withJSON), &with))
	return validateStep(ir.Step{
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: with},
		LLM:            llm,
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
			name: "client certificate",
			with: `{"browser":{"client_certificates":[{"hosts":["portal.example.com","${HOST}"],"pfx_file":"client.p12","passphrase":"${PASS}"}]},"do":[{"act":"a"}]}`,
		},
		{
			name: "client certificate without a file",
			with: `{"browser":{"client_certificates":[{"hosts":["*"]}]},"do":[{"act":"a"}]}`,
			want: "client_certificates[0]: set pfx_file, or cert_file and key_file",
		},
		{
			name: "client certificate with an invalid host",
			with: `{"browser":{"client_certificates":[{"hosts":["portal.example.com:443"],"pfx_file":"client.p12"}]},"do":[{"act":"a"}]}`,
			want: "client_certificates[0]: host pattern",
		},
		{
			name: "client certificate with no hosts",
			with: `{"browser":{"client_certificates":[{"hosts":[],"pfx_file":"client.p12"}]},"do":[{"act":"a"}]}`,
			want: "invalid browser config",
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
		{
			name: "upload",
			with: `{"do":[{"upload":{"files":"invoice.pdf"}},{"upload":{"files":["a.pdf","${FILES}"],"into":"Receipt","cache":false}}]}`,
		},
		{
			name: "upload without files",
			with: `{"do":[{"upload":{"into":"Receipt"}}]}`,
			want: "invalid browser config",
		},
		{
			name: "upload with no file listed",
			with: `{"do":[{"upload":{"files":[]}}]}`,
			want: "invalid browser config",
		},
		{
			name: "upload with a blank file",
			with: `{"do":[{"upload":{"files":["a.pdf"," "]}}]}`,
			want: "upload files must not be empty",
		},
		{
			name: "upload into nothing",
			with: `{"do":[{"upload":{"files":"a.pdf","into":"  "}}]}`,
			want: "upload into must not be blank",
		},
		{
			name: "ai choices",
			with: `{"ai":"never","do":[{"act":{"instruction":"a","ai":"every_run"}},{"upload":{"files":"a.pdf","into":"Receipt","ai":"on_miss"}},{"expect":{"text":"Done"}}]}`,
		},
		{
			name: "unknown ai",
			with: `{"ai":"sometimes","do":[{"act":"a"}]}`,
			want: "invalid browser config",
		},
		{
			name: "never with expect statement",
			with: `{"ai":"never","do":[{"expect":"The order is placed"}]}`,
			want: "do[0]: expect is judged by AI, which ai: never on the step does not allow",
		},
		{
			name: "never with when statement",
			with: `{"ai":"never","do":[{"act":"a","when":"A banner is shown"}]}`,
			want: "do[0]: when is judged by AI, which ai: never on the step does not allow",
		},
		{
			name: "never with extract",
			with: `{"ai":"never","do":[{"extract":{"instruction":"x","schema":{"type":"object"}}}]}`,
			want: "do[0]: extract reads the page with AI, which ai: never on the step does not allow",
		},
		{
			name: "ai and cache",
			with: `{"ai":"never","cache":true,"do":[{"act":"a"}]}`,
			want: "set ai or cache, not both",
		},
		{
			name: "ai and cache on an act",
			with: `{"do":[{"act":{"instruction":"a","ai":"never","cache":false}}]}`,
			want: "do[0]: act: set ai or cache, not both",
		},
		{
			name: "ai and cache on an upload",
			with: `{"do":[{"upload":{"files":"a.pdf","into":"Receipt","ai":"never","cache":true}}]}`,
			want: "do[0]: upload: set ai or cache, not both",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validationError(t, tc.with, testModel)
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

// A step needs a model only when an operation can call one under its choice
// of how much AI decides.
func TestValidateStepRequiresModel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		with string
		want string
	}{
		{name: "act", with: `{"do":[{"act":"a"}]}`, want: "browser actions need a model for do[0] act"},
		{name: "act under every_run", with: `{"ai":"every_run","do":[{"act":"a"}]}`, want: "need a model for do[0] act"},
		{name: "upload into", with: `{"do":[{"upload":{"files":"a.pdf","into":"Receipt"}}]}`, want: "need a model for do[0] upload"},
		{name: "extract", with: `{"do":[{"extract":{"instruction":"x","schema":{"type":"object"}}}]}`, want: "need a model for do[0] extract"},
		{name: "expect statement", with: `{"do":[{"act":{"instruction":"a","ai":"never"}},{"expect":"Done"}]}`, want: "need a model for do[1] expect"},
		{name: "when statement", with: `{"do":[{"act":{"instruction":"a","ai":"never"},"when":"A banner is shown"}]}`, want: "need a model for do[0] when"},
		{name: "act that overrides never", with: `{"ai":"never","do":[{"act":{"instruction":"a","ai":"on_miss"}}]}`, want: "need a model for do[0] act"},
		{name: "act under never", with: `{"do":[{"act":{"instruction":"a","ai":"never"}}]}`},
		{name: "upload without into", with: `{"do":[{"upload":{"files":"a.pdf"}}]}`},
		{
			name: "step under never",
			with: `{"ai":"never","do":[{"goto":"https://x.test"},{"act":"a","when":{"selector":"form"}},{"upload":{"files":"a.pdf","into":"Receipt"}},{"expect":{"url":"/done"}},{"wait":{"duration":"1s"}},{"screenshot":"done"}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validationError(t, tc.with, nil)
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

// An operation reads back in the form it was written in, so operations a
// browser session ran can be written into a step as they were given.
func TestOperationsWriteBackAsWritten(t *testing.T) {
	t.Parallel()

	for _, written := range []string{
		`{"act":"Click Sign in"}`,
		`{"act":{"instruction":"Pick the newest invoice","cache":false}}`,
		`{"act":{"instruction":"Click Sign in","ai":"never"}}`,
		`{"expect":"The order list is shown"}`,
		`{"expect":{"text":"Orders","within":"10s"}}`,
		`{"act":"Click Next","when":{"selector":"button.next"},"timeout":"30s"}`,
		`{"extract":{"instruction":"The order numbers","schema":{"type":"object","properties":{"numbers":{"type":"array"}}}}}`,
		`{"when":"A cookie banner is shown","act":"Accept the cookies"}`,
		`{"wait":{"duration":"2s"}}`,
		`{"upload":{"files":"invoice.pdf"}}`,
		`{"upload":{"files":["a.pdf","b.pdf"],"into":"The receipt drop area","cache":false}}`,
		`{"upload":{"files":"a.pdf","into":"The receipt drop area","ai":"never"}}`,
	} {
		var op operation
		require.NoError(t, json.Unmarshal([]byte(written), &op), written)
		back, err := json.Marshal(op)
		require.NoError(t, err)
		assert.JSONEq(t, written, string(back))
	}
}
