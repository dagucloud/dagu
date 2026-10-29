// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
)

// The fake OpenRouter answers only requests that name the azure/eu endpoint
// first, as an account allowing only zero-data-retention endpoints does for a
// model whose one such endpoint carries a regional surcharge.
func TestChatProviderOrder(t *testing.T) {
	t.Parallel()

	for name, with := range map[string]string{
		"shared": `
      provider: openrouter
      model: openai/gpt-6.1-terra
      base_url: %[1]s
      provider_order: [azure/eu]`,
		"fallback entry": `
      model:
        - provider: openrouter
          name: openai/gpt-6.1-terra
          base_url: %[1]s
        - provider: openrouter
          name: openai/gpt-6.1-terra
          base_url: %[1]s
          provider_order: [azure/eu]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Provider struct {
						Order []string `json:"order"`
					} `json:"provider"`
				}
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				if !slices.Equal(body.Provider.Order, []string{"azure/eu"}) {
					w.WriteHeader(http.StatusNotFound)
					_, _ = fmt.Fprint(w, `{"error":{"message":"No endpoints found matching your data policy"}}`)
					return
				}
				_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"routed"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()

			th := test.Setup(t)
			dag := th.DAG(t, `
env:
  - OPENROUTER_TEST_KEY: test-key
steps:
  - action: chat.completion
    output: ANSWER
    with:
      api_key_name: OPENROUTER_TEST_KEY
      stream: false
      prompt: Hello.`+fmt.Sprintf(strings.TrimRight(with, "\n"), server.URL)+`
`)
			dag.Agent().RunSuccess(t)

			dag.AssertLatestStatus(t, ir.Succeeded)
			dag.AssertOutputs(t, map[string]any{"ANSWER": "routed"})
		})
	}
}
