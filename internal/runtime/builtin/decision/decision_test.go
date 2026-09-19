// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const responseJSON = `{
  "model":"jev-test", "provider":"TypeSafe", "id":"request-1",
  "answers":{
    "department":{"type":"choice","choice":"billing","probabilities":{"billing":0.6,"other":0.4},"confidence":0.1},
    "urgency":{"type":"score","score":1.4,"legend":{"0":"Routine","1":"Soon","2":"Now"},"probabilities":{"0":0.1,"1":0.4,"2":0.5},"confidence":0.2},
    "refund":{"type":"noul","noul":0.95}
  },
  "usage":{"input_tokens":100,"output_tokens":25,"cost":0.0000042}
}`

func testConfig(t *testing.T) map[string]any {
	t.Helper()
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
  "provider":"openrouter","model":"jev-test","state":{"message":"Refund please","count":2,"active":true},
  "questions":{
    "department":{"type":"choice","instructions":"Which department?","criteria":{"billing":"Charges","other":null}},
    "urgency":{"type":"score","instructions":"How urgent?","criteria":["Routine","Soon","Now"]},
    "refund":{"type":"noul","instructions":"Refund requested?"}
  }
}`), &raw))
	return raw
}

func TestEvaluate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ provider, path, key string }{
		{openRouter, "/api/alpha/decisions", "OPENROUTER_API_KEY"},
		{typeSafe, "/v1/systemone", "TYPESAFE_API_KEY"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tc.path, r.URL.Path)
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
				var body map[string]any
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				assert.Len(t, body, 3)
				assert.Equal(t, "jev-test", body["model"])
				assert.Equal(t, map[string]any{"message": "Refund please", "count": float64(2), "active": true}, body["state"])
				_, _ = w.Write([]byte(responseJSON))
			}))
			defer server.Close()
			raw := testConfig(t)
			raw["provider"] = tc.provider
			raw["base_url"] = server.URL + tc.path[:strings.LastIndex(tc.path, "/")] + "/"
			scope := value.NewEnvScope(nil, false).WithEntry(tc.key, "test-key", value.EnvSourceSecret)
			ctx := runtime.WithEnv(t.Context(), runtime.Env{Scope: scope})
			exec, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
			require.NoError(t, err)
			defer exec.(*decisionExecutor).Close()
			var stdout bytes.Buffer
			exec.SetStdout(&stdout)
			require.NoError(t, exec.Run(ctx))
			assert.JSONEq(t, responseJSON, stdout.String())
		})
	}
}

func TestConfig(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"provider", func(c map[string]any) { c["provider"] = "openai" }},
		{"model", func(c map[string]any) { c["model"] = "" }},
		{"state", func(c map[string]any) { delete(c, "state") }},
		{"scalar", func(c map[string]any) { c["state"] = 42 }},
		{"questions", func(c map[string]any) { c["questions"] = map[string]any{} }},
		{"extra", func(c map[string]any) { c["stream"] = true }},
		{"url", func(c map[string]any) { c["base_url"] = "file:///tmp/request" }},
		{"key", func(c map[string]any) { c["api_key_name"] = "not a name" }},
		{"type", func(c map[string]any) {
			c["questions"] = map[string]any{"q": map[string]any{"type": "boolean", "instructions": "Yes?"}}
		}},
		{"choice", func(c map[string]any) {
			c["questions"] = map[string]any{"q": map[string]any{"type": "choice", "instructions": "Which?", "criteria": map[string]any{"only": nil}}}
		}},
		{"score", func(c map[string]any) {
			c["questions"] = map[string]any{"q": map[string]any{"type": "score", "instructions": "How much?", "criteria": []any{"one"}}}
		}},
		{"noul", func(c map[string]any) {
			c["questions"] = map[string]any{"q": map[string]any{"type": "noul", "instructions": "Yes?", "criteria": map[string]any{"true": "Yes"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testConfig(t)
			tc.edit(c)
			require.Error(t, validateStep(ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: c}}))
		})
	}
	raw := testConfig(t)
	raw["provider"] = "${env.PROVIDER}"
	raw["base_url"] = "${env.ENDPOINT}"
	require.NoError(t, validateStep(ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}}))
}

func TestResponse(t *testing.T) {
	t.Parallel()
	cfg, err := parseConfig(testConfig(t))
	require.NoError(t, err)
	for _, tc := range []struct{ name, from, to string }{
		{"missing", `"refund":`, `"wrong":`},
		{"type", `"type":"noul"`, `"type":"choice"`},
		{"choice", `"choice":"billing"`, `"choice":"unknown"`},
		{"score", `"score":1.4`, `"score":3`},
		{"confidence", `"confidence":0.1`, `"confidence":true`},
		{"probability", `"noul":0.95`, `"noul":1.5`},
		{"distribution", `"billing":0.6`, `"wrong":0.6`},
		{"legend", `"2":"Now"`, `"2":null`},
		{"usage", `"input_tokens":100`, `"input_tokens":-1`},
		{"model", `"model":"jev-test"`, `"model":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response map[string]any
			require.NoError(t, json.Unmarshal([]byte(strings.Replace(responseJSON, tc.from, tc.to, 1)), &response))
			require.Error(t, validateResponse(response, cfg.Questions))
		})
	}
}

func TestHTTPFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		calls  int32
	}{
		{"unauthorized", http.StatusUnauthorized, `{"error":"secret-\"quoted\"-value"}`, 1},
		{"limited", http.StatusTooManyRequests, `{"error":"busy"}`, 2},
		{"overloaded", 529, `{"error":"busy"}`, 2},
		{"malformed", http.StatusOK, `not json`, 1},
		{"trailing", http.StatusOK, responseJSON + `{}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			cfg := llm.DefaultConfig()
			cfg.MaxRetries = 1
			cfg.InitialInterval = time.Millisecond
			c := client{http: llm.NewHTTPClient(cfg), endpoint: server.URL, apiKey: "key"}
			_, err := c.evaluate(t.Context(), request{})
			require.Error(t, err)
			if tc.status != http.StatusOK {
				assert.NotContains(t, err.Error(), tc.body)
			}
			assert.Equal(t, tc.calls, calls.Load())
		})
	}
}

func TestCancellation(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	raw := testConfig(t)
	raw["base_url"] = server.URL
	scope := value.NewEnvScope(nil, false).WithEntry("OPENROUTER_API_KEY", "key", value.EnvSourceSecret)
	ctx := runtime.WithEnv(t.Context(), runtime.Env{Scope: scope})
	exec, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
	require.NoError(t, err)
	defer exec.(*decisionExecutor).Close()
	done := make(chan error, 1)
	go func() { done <- exec.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	require.NoError(t, exec.Kill(os.Interrupt))
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("request did not cancel")
	}
}

func TestCredentials(t *testing.T) {
	t.Parallel()
	raw := testConfig(t)
	raw["api_key_name"] = "CUSTOM_KEY"
	ctx := runtime.WithEnv(t.Context(), runtime.Env{Scope: value.NewEnvScope(nil, false)})
	_, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
	require.ErrorContains(t, err, "CUSTOM_KEY")

	secret := "secret-\"quoted\"-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer "+secret, r.Header.Get("Authorization"))
		var req request
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "*******", req.State)
		_, _ = w.Write([]byte(strings.Replace(responseJSON, "request-1", `secret-\"quoted\"-value`, 1)))
	}))
	defer server.Close()
	raw["base_url"] = server.URL
	raw["state"] = secret
	ctx = runtime.WithEnv(t.Context(), runtime.Env{Scope: value.NewEnvScope(nil, false).WithEntry("CUSTOM_KEY", secret, value.EnvSourceSecret)})
	exec, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
	require.NoError(t, err)
	defer exec.(*decisionExecutor).Close()
	var stdout bytes.Buffer
	exec.SetStdout(&stdout)
	require.NoError(t, exec.Run(ctx))
	var result map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.Equal(t, "*******", result["id"])
}
