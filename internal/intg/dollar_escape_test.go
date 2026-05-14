// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestDollarEscape(t *testing.T) {
	t.Parallel()

	t.Run("BackslashDollarLiteralInJQ", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `
env:
  - PRICE: '\$9.99'
steps:
  - name: jq-price
    action: jq.filter
    with:
      filter: ".price"
      data: |
        {"price":"${PRICE}"}
      raw: true
    output: PRICE_OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"PRICE_OUT": "$9.99",
		})
	})

	t.Run("SingleQuotedVarPreservesQuotes", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `
steps:
  - name: jq-literal
    action: jq.filter
    with:
      filter: ".literal"
      data: |
        {"literal":"'${HOME}'"}
      raw: true
    output: LITERAL_OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"LITERAL_OUT": "'${HOME}'",
		})
	})

	t.Run("BackslashDollarLiteralInHTTPBody", func(t *testing.T) {
		t.Parallel()

		type httpBodyResult struct {
			body string
			auth string
			err  error
		}
		bodyCh := make(chan httpBodyResult, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := r.Body.Close(); err != nil {
					t.Errorf("failed to close request body: %v", err)
				}
			}()
			body, err := io.ReadAll(r.Body)
			bodyCh <- httpBodyResult{body: string(body), auth: r.Header.Get("Authorization"), err: err}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))
		defer server.Close()

		th := test.Setup(t)
		dag := th.DAG(t, fmt.Sprintf(`
env:
  - TOKEN: secret
steps:
  - name: http-price
    action: http.request
    with:
      method: POST
      url: %s/price
      headers:
        Authorization: "Bearer \\$TOKEN"
        Content-Type: application/json
      body: |-
        {"price":"\$TOKEN"}
      silent: true
`, server.URL))
		agent := dag.Agent()
		agent.RunSuccess(t)

		select {
		case result := <-bodyCh:
			require.NoError(t, result.err)
			require.Equal(t, `Bearer $TOKEN`, result.auth)
			require.Equal(t, `{"price":"$TOKEN"}`, result.body)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "timed out waiting for HTTP body")
		}
	})
}
